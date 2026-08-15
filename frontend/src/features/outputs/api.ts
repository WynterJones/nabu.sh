import { apiRequest, booleanValue, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import { parseScript } from '../settings/api'
import type { WorkspaceOutput, WorkspaceOutputs } from './types'

export function parseWorkspaceOutput(raw: unknown): WorkspaceOutput {
  const item = record(raw)
  const fileKind = optionalString(item.file_kind ?? item.fileKind)?.toLowerCase()
  return {
    id: stringValue(item.id),
    kind: stringValue(item.kind, 'file').toLowerCase(),
    name: stringValue(item.name ?? item.path ?? item.url, 'Workspace output'),
    path: optionalString(item.path),
    url: optionalString(item.url),
    fileKind: fileKind === 'text' || fileKind === 'image' || fileKind === 'video' || fileKind === 'pdf' ? fileKind : fileKind ? 'unsupported' : undefined,
    mimeType: optionalString(item.mime_type ?? item.mimeType),
    size: typeof item.size === 'number' ? item.size : 0,
    editable: booleanValue(item.editable),
    taskId: optionalString(item.task_id ?? item.taskId),
    taskTitle: optionalString(item.task_title ?? item.taskTitle),
    scriptRunId: optionalString(item.script_run_id ?? item.scriptRunId),
    createdAt: optionalString(item.created_at ?? item.createdAt),
  }
}

export function parseWorkspaceOutputs(raw: unknown): WorkspaceOutputs {
  const value = record(extractValue(raw, 'outputs'))
  return {
    items: list(value.items ?? value.outputs ?? value.artifacts).map(parseWorkspaceOutput).filter((item) => item.id && (item.path || item.url)),
    scripts: list(value.scripts).map(parseScript).filter((script) => script.id),
  }
}

export const outputsApi = {
  list: () => apiRequest<unknown>('/api/outputs').then(parseWorkspaceOutputs),
}
