import type { Mission, Run, RunEvent, SetupCheck, SetupPayload, StatusResponse, Task, TaskChecklistItem, Workspace } from '../types'
import { normalizeOperatorStatus, normalizePriority, normalizeTaskStatus } from './utils'

type RecordLike = Record<string, unknown>

export class ApiError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

function asRecord(value: unknown): RecordLike {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as RecordLike) : {}
}

function text(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : value == null ? fallback : String(value)
}

function optionalText(value: unknown): string | undefined {
  const result = text(value).trim()
  return result || undefined
}

function boolean(value: unknown, fallback = false): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value !== 0
  if (typeof value === 'string') return ['true', '1', 'yes', 'connected', 'ok'].includes(value.toLowerCase())
  return fallback
}

function array(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function stringArray(value: unknown): string[] {
  return array(value).map((entry) => {
    if (typeof entry === 'string') return entry
    const item = asRecord(entry)
    const name = optionalText(item.name ?? item.label ?? item.title)
    const detail = optionalText(item.details ?? item.path ?? item.url ?? item.status)
    if (name && detail && name !== detail) return `${name}: ${detail}`
    return name ?? detail ?? ''
  }).filter(Boolean)
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })
  if (!response.ok) {
    let message = `Request failed (${response.status})`
    try {
      const body = asRecord(await response.json())
      const nestedError = asRecord(body.error)
      message = text(nestedError.message ?? body.error ?? body.message, message)
    } catch {
      // Preserve the HTTP fallback when a response is not JSON.
    }
    throw new ApiError(message, response.status)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

function extract(raw: unknown, ...keys: string[]): unknown {
  const body = asRecord(raw)
  for (const key of keys) {
    if (body[key] !== undefined) return body[key]
  }
  if (body.data !== undefined) {
    const data = asRecord(body.data)
    for (const key of keys) {
      if (data[key] !== undefined) return data[key]
    }
    return body.data
  }
  return raw
}

export function parseStatus(raw: unknown): StatusResponse {
  const status = asRecord(extract(raw, 'status'))
  const body = Object.keys(status).length ? status : asRecord(raw)
  const paused = boolean(body.paused ?? body.is_paused)
  const activities = array(body.activities).map((entry) => {
    const activity = asRecord(entry)
    return {
      kind: text(activity.kind, 'operator'),
      label: text(activity.label, 'Operator activity'),
      status: text(activity.status, 'running'),
      entityId: optionalText(activity.entity_id ?? activity.entityId),
      detail: optionalText(activity.detail),
    }
  }).filter((activity) => activity.label)
  return {
    status: normalizeOperatorStatus(body.state ?? body.operator_status ?? (typeof asRecord(raw).status === 'string' ? asRecord(raw).status : undefined), paused),
    setupComplete: boolean(body.setup_complete ?? body.setupComplete ?? body.configured ?? body.initialized),
    paused,
    name: text(body.name ?? body.display_name, 'Nabu'),
    version: optionalText(body.version),
    nextOrientationAt: optionalText(body.next_orientation_at ?? body.nextOrientationAt),
    activeTaskId: optionalText(body.active_task_id ?? body.activeTaskId ?? asRecord(body.active_task).id),
    message: optionalText(body.message ?? body.detail),
    missionStarted: body.mission_started === undefined ? undefined : boolean(body.mission_started),
    codexAvailable: body.codex_available === undefined ? undefined : boolean(body.codex_available),
    readyCount: typeof body.ready_count === 'number' ? body.ready_count : undefined,
    needsAttention: typeof body.needs_attention === 'number' ? body.needs_attention : undefined,
    codexState: optionalText(body.codex_state),
    codexMessage: optionalText(body.codex_reason ?? body.codex_message ?? body.codex_error),
    retryAt: optionalText(body.codex_retry_at ?? body.retry_at),
    serviceHealthy: body.service_healthy === undefined ? undefined : boolean(body.service_healthy),
    diskFreeBytes: typeof body.disk_free_bytes === 'number' ? body.disk_free_bytes : undefined,
    lastBackupAt: optionalText(body.last_backup_at),
    contextReady: body.context_ready === undefined ? undefined : boolean(body.context_ready),
    activities,
    chatQueued: typeof body.chat_queued === 'number' ? body.chat_queued : 0,
  }
}

export function parseMission(raw: unknown): Mission {
  const body = asRecord(extract(raw, 'mission'))
  return {
    id: optionalText(body.id),
    statement: text(body.statement ?? body.mission ?? body.title ?? body.text),
    context: optionalText(body.context ?? body.business_context),
    active: body.active === undefined ? true : boolean(body.active),
    updatedAt: optionalText(body.updated_at ?? body.updatedAt),
  }
}

function parseChecklist(value: unknown): TaskChecklistItem[] {
  return array(value).map((entry) => {
    if (typeof entry === 'string') return { label: entry, complete: false }
    const item = asRecord(entry)
    return {
      label: text(item.label ?? item.title ?? item.text),
      complete: boolean(item.complete ?? item.completed ?? item.done),
      failed: boolean(item.failed),
      details: optionalText(item.details ?? item.detail),
    }
  }).filter((item) => item.label)
}

export function parseTask(raw: unknown): Task {
  const body = asRecord(raw)
  const result = asRecord(body.result)
  const definition = body.definition_of_done ?? body.definitionOfDone ?? body.checklist
  const status = normalizeTaskStatus(body.status ?? body.state)
  const verificationEntries = array(body.verification ?? result.verification)
  const failedCheck = verificationEntries
    .map(asRecord)
    .find((entry) => ['failed', 'error', 'blocked'].includes(text(entry.status).toLowerCase()))
  const explicitError = optionalText(body.error ?? result.error)
  const resultSummary = optionalText(body.result_summary ?? body.resultSummary ?? body.summary ?? result.summary)
  const failureReason = optionalText(
    body.failure_reason
    ?? body.failureReason
    ?? result.failure_reason
    ?? result.failureReason
    ?? failedCheck?.details
    ?? failedCheck?.detail
    ?? failedCheck?.message,
  ) ?? (status === 'failed' ? explicitError ?? resultSummary : undefined)
  return {
    id: text(body.id ?? body.task_id),
    title: text(body.title, 'Untitled task'),
    purpose: optionalText(body.purpose ?? body.description),
    whyThisMatters: optionalText(body.why ?? body.why_this_matters ?? body.whyThisMatters ?? body.reason),
    status,
    priority: normalizePriority(body.priority),
    workspace: optionalText(body.workspace_path ?? asRecord(body.workspace).path ?? (typeof body.workspace === 'string' ? body.workspace : undefined)),
    createdAt: optionalText(body.created_at ?? body.createdAt),
    updatedAt: optionalText(body.updated_at ?? body.updatedAt),
    startedAt: optionalText(body.started_at ?? body.startedAt),
    completedAt: optionalText(body.completed_at ?? body.completedAt),
    plannedAt: optionalText(body.planned_at ?? body.plannedAt ?? body.scheduled_at ?? body.starts_at),
    runRequestedAt: optionalText(body.run_requested_at ?? body.runRequestedAt),
    createdBy: optionalText(body.created_by ?? body.createdBy ?? body.source),
    runId: optionalText(body.current_run_id ?? body.run_id ?? body.runId ?? asRecord(body.run).id),
    dependsOnTaskIds: stringArray(body.depends_on_task_ids ?? body.dependsOnTaskIds),
    definitionOfDone: parseChecklist(definition),
    output: optionalText(body.output ?? result.output),
    resultSummary,
    error: explicitError,
    failureReason,
    verification: verificationEntries.map((entry) => {
      if (typeof entry === 'string') return entry
      const item = asRecord(entry)
      const name = optionalText(item.name ?? item.label ?? item.title) ?? 'Verification check'
      const state = optionalText(item.status)
      const detail = optionalText(item.details ?? item.detail ?? item.message)
      const prefix = state ? `${name} (${state.replaceAll('_', ' ')})` : name
      return detail ? `${prefix} — ${detail}` : prefix
    }).filter(Boolean),
    uncertainties: stringArray(body.uncertainties ?? result.uncertainties),
    artifacts: stringArray(body.artifacts ?? result.artifacts),
    artifactFiles: array(body.artifacts ?? result.artifacts).map((entry) => {
      const item = asRecord(entry)
      return { name: text(item.name ?? item.title ?? item.path), path: text(item.path) }
    }).filter((item) => item.path),
    filesChanged: stringArray(body.files_changed ?? body.filesChanged ?? result.files_changed),
  }
}

export function parseTasks(raw: unknown): Task[] {
  return array(extract(raw, 'tasks', 'items')).map(parseTask).filter((task) => task.id)
}

export function parseWorkspaces(raw: unknown): Workspace[] {
  return array(extract(raw, 'workspaces', 'items')).map((entry) => {
    if (typeof entry === 'string') return { path: entry }
    const body = asRecord(entry)
    return {
      id: optionalText(body.id),
      path: text(body.path),
      writable: body.writable === undefined ? undefined : boolean(body.writable),
      git: body.git === undefined && body.is_git === undefined ? undefined : boolean(body.git ?? body.is_git),
      valid: body.valid === undefined ? undefined : boolean(body.valid),
      error: optionalText(body.error),
    }
  }).filter((workspace) => workspace.path)
}

function parseRunEvent(raw: unknown): RunEvent {
  if (typeof raw === 'string') return { type: 'output', message: raw }
  const body = asRecord(raw)
  return {
    id: optionalText(body.id),
    type: text(body.type ?? body.kind, 'activity'),
    message: text(body.message ?? body.text ?? body.output ?? body.data),
    at: optionalText(body.at ?? body.created_at ?? body.timestamp),
    stream: optionalText(body.stream),
  }
}

export function parseRun(raw: unknown): Run {
  const body = asRecord(extract(raw, 'run'))
  const stdout = text(body.output ?? body.stdout ?? body.raw_output)
  const stderr = optionalText(body.stderr)
  const result = asRecord(body.result)
  const events = array(body.events ?? body.activity).map(parseRunEvent)
  if (!events.length && optionalText(body.started_at ?? body.startedAt)) {
    events.push({ type: 'started', message: `Run started in ${text(body.working_directory ?? body.cwd, 'the selected workspace')}.`, at: optionalText(body.started_at ?? body.startedAt) })
  }
  const summary = optionalText(body.result_summary ?? body.summary ?? result.summary)
  if (!events.length || summary) {
    if (summary) events.push({ type: 'result', message: summary, at: optionalText(body.ended_at ?? body.completed_at ?? body.endedAt) })
  }
  const runError = optionalText(body.error)
  if (runError) events.push({ type: 'error', message: runError, at: optionalText(body.ended_at ?? body.endedAt) })
  return {
    id: text(body.id ?? body.run_id),
    taskId: optionalText(body.task_id ?? body.taskId),
    taskTitle: optionalText(body.task_title ?? body.taskTitle),
    type: optionalText(body.type ?? body.run_type),
    status: text(body.status ?? body.state, 'unknown'),
    startedAt: optionalText(body.started_at ?? body.startedAt),
    endedAt: optionalText(body.ended_at ?? body.completed_at ?? body.endedAt),
    exitCode: typeof body.exit_code === 'number' ? body.exit_code : undefined,
    cwd: optionalText(body.cwd ?? body.working_directory),
    output: stdout,
    stderr,
    events,
    resultSummary: summary,
  }
}

export function parseSetupChecks(raw: unknown): SetupCheck[] {
  const values = array(extract(raw, 'checks', 'items'))
  if (values.length) {
    return values.map((entry, index) => {
      const body = asRecord(entry)
      return {
        key: text(body.key ?? body.name, `check-${index}`),
        label: text(body.label ?? body.name, 'System check'),
        ok: boolean(body.ok ?? body.success ?? body.connected ?? body.valid),
        detail: optionalText(body.detail ?? body.message ?? body.error),
      }
    })
  }
  const body = asRecord(raw)
  const nestedChecks: SetupCheck[] = [
    ['codex', 'Codex CLI'],
    ['git', 'Git'],
  ].filter(([key]) => body[key] !== undefined).map(([key, label]) => {
    const check = asRecord(body[key])
    return {
      key,
      label,
      ok: boolean(check.available),
      detail: optionalText(check.error ?? check.version ?? check.path),
    }
  })
  array(body.workspaces).forEach((entry, index) => {
    const check = asRecord(entry)
    nestedChecks.push({
      key: `workspace-${index}`,
      label: `Workspace ${index + 1}`,
      ok: boolean(check.available),
      detail: optionalText(check.error ?? check.path),
    })
  })
  if (nestedChecks.length) return nestedChecks
  return [
    ['codex', 'Codex CLI'],
    ['codex_authenticated', 'Codex authenticated'],
    ['git', 'Git'],
    ['service', 'Nabu service'],
  ].filter(([key]) => body[key] !== undefined).map(([key, label]) => ({ key, label, ok: boolean(body[key]) }))
}

export const api = {
  getStatus: () => request<unknown>('/api/status').then(parseStatus),
  getMission: () => request<unknown>('/api/mission').then(parseMission),
  updateMission: (mission: Pick<Mission, 'statement' | 'context'>) => request<unknown>('/api/mission', {
    method: 'PUT',
    body: JSON.stringify({ statement: mission.statement, context: mission.context ?? '' }),
  }).then(parseMission),
  getTasks: () => request<unknown>('/api/tasks').then(parseTasks),
  createTask: (task: { title: string; purpose?: string; whyThisMatters?: string; priority: string; workspaceId?: string; definitionOfDone: string[]; dependsOnTaskIds?: string[] }) => request<unknown>('/api/tasks', {
    method: 'POST',
    body: JSON.stringify({
      title: task.title,
      purpose: task.purpose,
      why: task.whyThisMatters,
      priority: task.priority,
      workspace_id: task.workspaceId,
      definition_of_done: task.definitionOfDone,
      depends_on_task_ids: task.dependsOnTaskIds ?? [],
    }),
  }).then((raw) => parseTask(extract(raw, 'task'))),
  draftTask: (requestText: string, priority?: string) => request<unknown>('/api/tasks/draft', {
    method: 'POST',
    body: JSON.stringify({ request: requestText, priority }),
  }).then((raw) => {
    const body = asRecord(extract(raw, 'draft', 'task'))
    return {
      title: text(body.title),
      purpose: text(body.purpose),
      why: text(body.why),
      priority: normalizePriority(body.priority ?? priority),
      definitionOfDone: array(body.definition_of_done ?? body.definitionOfDone).map((item) => typeof item === 'string' ? item : text(asRecord(item).text ?? asRecord(item).label)).filter(Boolean),
    }
  }),
  getTask: (id: string) => request<unknown>(`/api/tasks/${encodeURIComponent(id)}`).then((raw) => parseTask(extract(raw, 'task'))),
  updateTask: (id: string, values: Record<string, unknown>) => request<unknown>(`/api/tasks/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(values),
  }).then((raw) => parseTask(extract(raw, 'task'))),
  runTask: (id: string) => request<unknown>(`/api/tasks/${encodeURIComponent(id)}/run`, {
    method: 'POST',
  }).then((raw) => parseTask(extract(raw, 'task'))),
  recoverTask: (id: string, note: string) => request<unknown>(`/api/tasks/${encodeURIComponent(id)}/recover`, {
    method: 'POST',
    body: JSON.stringify({ note }),
  }),
  deleteTask: (id: string) => request<void>(`/api/tasks/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  getRun: (id: string) => request<unknown>(`/api/runs/${encodeURIComponent(id)}`).then(parseRun),
  getWorkspaces: () => request<unknown>('/api/workspaces').then(parseWorkspaces),
  checkSetup: (workspaces: string[]) => request<unknown>('/api/setup/checks', {
    method: 'POST',
    body: JSON.stringify({ workspaces, paths: workspaces }),
  }).then(parseSetupChecks),
  browseWorkspace: () => request<{ path: string }>('/api/setup/browse', { method: 'POST' }).then((result) => result.path),
  completeSetup: (payload: SetupPayload) => request<unknown>('/api/setup/complete', {
    method: 'POST',
    body: JSON.stringify({
      display_name: payload.name,
      mission: payload.mission,
      context: payload.context,
      workspaces: payload.workspaces,
      policy: {
        read: payload.autonomy.research ? 'allow' : 'ask',
        work: payload.autonomy.editWorkspaces && payload.autonomy.runLocal && payload.autonomy.createGitChanges && payload.autonomy.createDrafts ? 'allow' : 'ask',
        publish: 'ask',
        dangerous: 'ask',
      },
    }),
  }),
  startMission: () => request<unknown>('/api/mission/start', { method: 'POST' }),
  setPaused: (paused: boolean) => request<unknown>('/api/pause', {
    method: 'POST',
    body: JSON.stringify({ paused }),
  }),
  orient: () => request<unknown>('/api/orient', { method: 'POST' }),
}
