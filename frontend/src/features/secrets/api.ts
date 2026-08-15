import { apiRequest, booleanValue, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import type { SavedSecret, SaveSecretInput } from './types'

export function parseSavedSecret(raw: unknown): SavedSecret {
  const item = record(raw)
  const count = item.binding_count ?? item.bindingCount ?? item.used_by_count ?? item.usedByCount
  return {
    id: stringValue(item.id ?? item.secret_id),
    name: stringValue(item.name ?? item.label, 'Saved secret'),
    label: optionalString(item.label),
    description: optionalString(item.description),
    configured: item.configured === undefined ? undefined : booleanValue(item.configured),
    createdAt: optionalString(item.created_at ?? item.createdAt),
    updatedAt: optionalString(item.updated_at ?? item.updatedAt),
    bindingCount: typeof count === 'number' ? count : undefined,
  }
}

export const secretsApi = {
  list: () => apiRequest<unknown>('/api/secrets').then((raw) => list(extractValue(raw, 'secrets', 'items')).map(parseSavedSecret).filter((secret) => secret.id)),
  get: (id: string) => apiRequest<unknown>(`/api/secrets/${encodeURIComponent(id)}`).then((raw) => parseSavedSecret(extractValue(raw, 'secret'))),
  save: (input: SaveSecretInput) => apiRequest<unknown>(input.id ? `/api/secrets/${encodeURIComponent(input.id)}` : '/api/secrets', {
    method: input.id ? 'PATCH' : 'POST',
    body: JSON.stringify({
      name: input.name.trim(),
      ...(input.label?.trim() ? { label: input.label.trim() } : {}),
      ...(input.description?.trim() ? { description: input.description.trim() } : {}),
      ...(input.value ? { value: input.value } : {}),
    }),
  }).then((raw) => parseSavedSecret(extractValue(raw, 'secret'))),
  update: (id: string, values: { name?: string; label?: string; description?: string; value?: string }) => apiRequest<unknown>(`/api/secrets/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(values),
  }).then((raw) => parseSavedSecret(extractValue(raw, 'secret'))),
  delete: (id: string) => apiRequest<void>(`/api/secrets/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}
