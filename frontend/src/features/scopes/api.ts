import { apiRequest, booleanValue, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import type { Scope } from './types'

export function parseScope(raw: unknown): Scope {
  const item = record(raw)
  return {
    id: stringValue(item.id ?? item.workspace_id ?? item.scope_id),
    name: stringValue(item.name ?? item.title ?? item.path, 'Workspace'),
    path: stringValue(item.path),
    active: booleanValue(item.active ?? item.is_active),
    createdAt: optionalString(item.created_at ?? item.createdAt),
    updatedAt: optionalString(item.updated_at ?? item.updatedAt),
    iconUrl: optionalString(item.icon_url ?? item.iconUrl),
    contextReady: booleanValue(item.context_ready ?? item.contextReady),
  }
}

export const scopesApi = {
  browse: () => apiRequest<unknown>('/api/setup/browse', { method: 'POST' }).then((raw) => stringValue(record(raw).path)),
  list: () => apiRequest<unknown>('/api/scopes').then((raw) => list(extractValue(raw, 'scopes', 'workspaces', 'items')).map(parseScope)),
  active: () => apiRequest<unknown>('/api/scopes/active').then((raw) => parseScope(extractValue(raw, 'scope', 'workspace'))),
  setActive: (id: string) => apiRequest<unknown>('/api/scopes/active', { method: 'POST', body: JSON.stringify({ workspace_id: id }) }).then((raw) => parseScope(extractValue(raw, 'scope', 'workspace'))),
  create: (values: { name: string; path: string; mode: 'create' | 'connect'; mission?: string; context?: string }) => apiRequest<unknown>('/api/scopes', { method: 'POST', body: JSON.stringify(values) }).then((raw) => parseScope(extractValue(raw, 'scope', 'workspace'))),
  update: (id: string, values: Partial<Pick<Scope, 'name' | 'path'>>) => apiRequest<unknown>(`/api/scopes/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(values) }).then((raw) => parseScope(extractValue(raw, 'scope', 'workspace'))),
  delete: (id: string, confirmation: string) => apiRequest<{ deleted_workspace_id: string; active_workspace_id?: string; folder_preserved: boolean }>(`/api/scopes/${encodeURIComponent(id)}`, { method: 'DELETE', body: JSON.stringify({ confirmation }) }),
  uploadIcon: (id: string, icon: File) => {
    const body = new FormData()
    body.append('icon', icon)
    return apiRequest<unknown>(`/api/scopes/${encodeURIComponent(id)}/icon`, { method: 'POST', body }).then((raw) => parseScope(extractValue(raw, 'scope', 'workspace')))
  },
  deleteIcon: (id: string) => apiRequest<void>(`/api/scopes/${encodeURIComponent(id)}/icon`, { method: 'DELETE' }),
}
