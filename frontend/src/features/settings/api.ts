import { apiRequest, booleanValue, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import type { CodexModelCatalog, CodexModelOption, CodexReasoningEffort, MemoryDocument, MemoryUpdate, OperatorAISettings, Policy, PolicyDecision, Schedule, ScheduleTrigger, ScriptEntry, ServiceHealth, SoulDocument } from './types'

const reasoningEfforts: readonly CodexReasoningEffort[] = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']

function reasoningEffort(value: unknown): CodexReasoningEffort | undefined {
  const parsed = optionalString(value)?.toLowerCase() ?? ''
  return reasoningEfforts.includes(parsed as CodexReasoningEffort) ? parsed as CodexReasoningEffort : undefined
}

export function parseCodexModel(raw: unknown): CodexModelOption {
  const item = record(raw)
  return {
    id: stringValue(item.id ?? item.model),
    displayName: stringValue(item.display_name ?? item.displayName ?? item.name ?? item.id, 'Codex model'),
    description: optionalString(item.description),
    defaultReasoningEffort: reasoningEffort(item.default_reasoning_effort ?? item.defaultReasoningEffort) ?? '',
    supportedReasoningEfforts: list(item.supported_reasoning_efforts ?? item.supportedReasoningEfforts).map(reasoningEffort).filter((effort): effort is CodexReasoningEffort => Boolean(effort)),
  }
}

export function parseCodexModelCatalog(raw: unknown): CodexModelCatalog {
  const item = record(raw)
  const data = record(item.data)
  return {
    models: list(item.models ?? data.models ?? extractValue(raw, 'models')).map(parseCodexModel).filter((model) => model.id),
    source: stringValue(item.source ?? data.source).toLowerCase() === 'codex' ? 'codex' : 'fallback',
  }
}

function policyDecision(value: unknown, fallback: PolicyDecision): PolicyDecision {
  const parsed = stringValue(value, fallback).toLowerCase()
  return parsed === 'deny' || parsed === 'ask' ? parsed : 'allow'
}

export function parsePolicy(raw: unknown): Policy {
  const item = record(extractValue(raw, 'policy'))
  return {
    read: policyDecision(item.read, 'allow'),
    work: policyDecision(item.work, 'allow'),
    publish: policyDecision(item.publish, 'ask'),
    dangerous: policyDecision(item.dangerous, 'ask'),
    updatedAt: optionalString(item.updated_at ?? item.updatedAt),
  }
}

export function parseSchedule(raw: unknown): Schedule {
  const item = record(raw)
  const trigger = stringValue(item.kind ?? item.trigger_type ?? item.triggerType ?? item.type, 'orient').toLowerCase()
  const triggerType: ScheduleTrigger = trigger === 'script' || trigger === 'task' ? trigger : 'orientation'
  const cadence = record(item.cadence)
  return {
    id: stringValue(item.id ?? item.schedule_id),
    name: stringValue(item.name ?? item.title, 'Untitled schedule'),
    triggerType,
    payload: record(item.payload),
    cadence: {
      expression: optionalString(cadence.expression ?? item.expression ?? item.cron),
      intervalSeconds: typeof cadence.interval_seconds === 'number' ? cadence.interval_seconds : typeof item.interval_seconds === 'number' ? item.interval_seconds : undefined,
    },
    enabled: booleanValue(item.enabled, true),
    nextRunAt: optionalString(item.next_run_at ?? item.nextRunAt),
    lastRunAt: optionalString(item.last_run_at ?? item.lastRunAt),
    lastStatus: optionalString(item.last_status ?? item.lastStatus) ?? (optionalString(item.last_error ?? item.lastError) ? 'failed' : undefined),
    lastError: optionalString(item.last_error ?? item.lastError),
  }
}

export function parseScript(raw: unknown): ScriptEntry {
  const item = record(raw)
  const lastResult = record(item.last_result ?? item.result)
  return {
    id: stringValue(item.id ?? item.name),
    name: stringValue(item.name ?? item.id, 'Unnamed script'),
    description: optionalString(item.description),
    path: optionalString(item.path),
    status: stringValue(item.status, 'ready'),
    enabled: booleanValue(item.enabled, true),
    access: stringValue(item.access, 'read').toLowerCase() === 'write' ? 'write' : 'read',
    timeoutSeconds: typeof item.timeout_seconds === 'number' ? item.timeout_seconds : undefined,
    lastRunAt: optionalString(item.last_run_at ?? item.lastRunAt),
    lastSummary: optionalString(item.last_summary ?? lastResult.summary),
    interesting: item.interesting === undefined && lastResult.interesting === undefined ? undefined : booleanValue(item.interesting ?? lastResult.interesting),
    secretBindings: list(item.secret_bindings ?? item.secretBindings).map((binding) => {
      const value = record(binding)
      return { secretId: stringValue(value.secret_id ?? value.secretId), envVar: stringValue(value.env_var ?? value.envVar) }
    }).filter((binding) => binding.secretId && binding.envVar),
  }
}

export function parseMemory(raw: unknown): MemoryDocument {
  const item = record(extractValue(raw, 'memory'))
  return {
    body: stringValue(item.body ?? item.content ?? item.markdown),
    summary: optionalString(item.summary),
    updatedAt: optionalString(item.updated_at ?? item.updatedAt),
    dailyNotes: list(item.daily_notes ?? item.dailyNotes).map((entry) => ({
      date: stringValue(record(entry).date),
      summary: stringValue(record(entry).summary ?? record(entry).body),
    })).filter((entry) => entry.date),
  }
}

export function parseSoul(raw: unknown): SoulDocument {
  const item = record(extractValue(raw, 'soul'))
  return { body: stringValue(item.body ?? item.content ?? item.markdown), updatedAt: optionalString(item.updated_at ?? item.updatedAt) }
}

export function parseMemoryUpdate(raw: unknown): MemoryUpdate {
  const item = record(raw)
  return { id: stringValue(item.id ?? item.update_id), summary: stringValue(item.summary ?? item.title, 'Proposed memory update'), content: stringValue(item.content ?? item.body ?? item.markdown), reason: optionalString(item.reason ?? item.why), status: stringValue(item.status, 'pending'), createdAt: optionalString(item.created_at ?? item.createdAt) }
}

export function parseServiceHealth(raw: unknown): ServiceHealth {
  const item = record(extractValue(raw, 'health', 'service'))
  const codexAvailableValue = item.codex_available ?? item.codexAvailable
  const codexAvailable = codexAvailableValue === undefined ? undefined : booleanValue(codexAvailableValue)
  const diskFreeBytes = item.disk_free_bytes ?? item.diskFreeBytes
  return {
    status: stringValue(item.status, 'idle'),
    codexState: stringValue(item.codex_state ?? item.codexState, codexAvailable === false ? 'unavailable' : 'available'),
    codexMessage: optionalString(item.codex_reason ?? item.codexReason ?? item.codex_message ?? item.codexMessage ?? item.reason),
    retryAt: optionalString(item.codex_retry_at ?? item.codexRetryAt ?? item.retry_at ?? item.retryAt),
    serviceHealthy: item.service_healthy === undefined && item.serviceHealthy === undefined
      ? undefined
      : booleanValue(item.service_healthy ?? item.serviceHealthy),
    diskFreeBytes: typeof diskFreeBytes === 'number' ? diskFreeBytes : undefined,
    backupAt: optionalString(item.last_backup_at ?? item.lastBackupAt ?? item.backup_at ?? item.backupAt),
  }
}

export function parseOperatorSettings(raw: unknown): OperatorAISettings {
  const item = record(extractValue(raw, 'settings'))
  const effort = (optionalString(item.codex_reasoning_effort ?? item.codexReasoningEffort) ?? '').toLowerCase()
  const parsedParallelTasks = Number(item.max_parallel_tasks ?? item.maxParallelTasks ?? 1)
  return {
    codexModel: optionalString(item.codex_model ?? item.codexModel) ?? '',
    codexReasoningEffort: reasoningEfforts.includes(effort as CodexReasoningEffort) ? effort as CodexReasoningEffort : '',
    maxParallelTasks: Number.isInteger(parsedParallelTasks) && parsedParallelTasks >= 1 && parsedParallelTasks <= 8 ? parsedParallelTasks : 1,
  }
}

export const settingsApi = {
  getOperatorSettings: () => apiRequest<unknown>('/api/settings/operator').then(parseOperatorSettings),
  getOperatorModels: () => apiRequest<unknown>('/api/settings/operator/models').then(parseCodexModelCatalog),
  updateOperatorSettings: (settings: OperatorAISettings) => apiRequest<unknown>('/api/settings/operator', { method: 'PUT', body: JSON.stringify({ codex_model: settings.codexModel.trim(), codex_reasoning_effort: settings.codexReasoningEffort, max_parallel_tasks: settings.maxParallelTasks || 1 }) }).then(parseOperatorSettings),
  getPolicy: () => apiRequest<unknown>('/api/policy').then(parsePolicy),
  updatePolicy: (policy: Policy) => apiRequest<unknown>('/api/policy', { method: 'PUT', body: JSON.stringify({ read: policy.read, work: policy.work, publish: policy.publish, dangerous: policy.dangerous }) }).then(parsePolicy),
  listSchedules: () => apiRequest<unknown>('/api/schedules').then((raw) => list(extractValue(raw, 'schedules', 'items')).map(parseSchedule)),
  createSchedule: (schedule: Omit<Schedule, 'id'>) => apiRequest<unknown>('/api/schedules', { method: 'POST', body: JSON.stringify({ name: schedule.name, kind: schedule.triggerType === 'orientation' ? 'orient' : schedule.triggerType, payload: schedule.payload, cadence: { expression: schedule.cadence.expression, interval_seconds: schedule.cadence.intervalSeconds }, enabled: schedule.enabled }) }).then((raw) => parseSchedule(extractValue(raw, 'schedule'))),
  updateSchedule: (id: string, values: Partial<Schedule>) => apiRequest<unknown>(`/api/schedules/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ name: values.name, kind: values.triggerType === 'orientation' ? 'orient' : values.triggerType, payload: values.payload, cadence: values.cadence ? { expression: values.cadence.expression, interval_seconds: values.cadence.intervalSeconds } : undefined, enabled: values.enabled }) }).then((raw) => parseSchedule(extractValue(raw, 'schedule'))),
  deleteSchedule: (id: string) => apiRequest<void>(`/api/schedules/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  listScripts: () => apiRequest<unknown>('/api/scripts').then((raw) => list(extractValue(raw, 'scripts', 'items')).map(parseScript)),
  createScript: (script: Omit<ScriptEntry, 'id' | 'status'>) => apiRequest<unknown>('/api/scripts', { method: 'POST', body: JSON.stringify({ name: script.name, path: script.path, description: script.description, enabled: script.enabled, access: script.access, timeout_seconds: script.timeoutSeconds, secret_bindings: script.secretBindings.map((binding) => ({ secret_id: binding.secretId, env_var: binding.envVar })) }) }).then((raw) => parseScript(extractValue(raw, 'script'))),
  updateScript: (id: string, values: Partial<ScriptEntry>) => apiRequest<unknown>(`/api/scripts/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ name: values.name, path: values.path, description: values.description, enabled: values.enabled, access: values.access, timeout_seconds: values.timeoutSeconds, secret_bindings: values.secretBindings?.map((binding) => ({ secret_id: binding.secretId, env_var: binding.envVar })) }) }).then((raw) => parseScript(extractValue(raw, 'script'))),
  deleteScript: (id: string) => apiRequest<void>(`/api/scripts/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  runScript: (id: string) => apiRequest<unknown>(`/api/scripts/${encodeURIComponent(id)}/run`, { method: 'POST' }),
  getMemory: () => apiRequest<unknown>('/api/memory').then(parseMemory),
  updateMemory: (body: string) => apiRequest<unknown>('/api/memory', { method: 'PUT', body: JSON.stringify({ content: body }) }).then(parseMemory),
  getSoul: () => apiRequest<unknown>('/api/soul').then(parseSoul),
  updateSoul: (body: string) => apiRequest<unknown>('/api/soul', { method: 'PUT', body: JSON.stringify({ content: body }) }).then(parseSoul),
  listMemoryUpdates: () => apiRequest<unknown>('/api/memory/updates').then((raw) => list(extractValue(raw, 'updates', 'items')).map(parseMemoryUpdate).filter((update) => update.status === 'proposed')),
  resolveMemoryUpdate: (id: string, decision: 'applied' | 'rejected', note?: string) => apiRequest<unknown>(`/api/memory/updates/${encodeURIComponent(id)}/resolve`, { method: 'POST', body: JSON.stringify({ decision, rejection_note: decision === 'rejected' ? note ?? '' : undefined }) }).then((raw) => parseMemoryUpdate(extractValue(raw, 'update'))),
  getHealth: () => apiRequest<unknown>('/api/health').then(parseServiceHealth),
  restartService: () => apiRequest<unknown>('/api/service/restart', { method: 'POST' }),
}
