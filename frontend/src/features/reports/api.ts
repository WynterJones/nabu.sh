import { apiRequest, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import { parseArtifacts, parseEntityRefs } from '../shared/parsers'
import type { Report, ReportStatus } from './types'

export function parseReport(raw: unknown): Report {
  const item = record(raw)
  const rawStatus = stringValue(item.status).toLowerCase()
  const status: ReportStatus = rawStatus === 'archived' || item.archived_at || item.archivedAt
    ? 'archived'
    : rawStatus === 'read' || item.read_at || item.readAt
      ? 'read'
      : 'unread'
  return {
    id: stringValue(item.id ?? item.report_id),
    title: stringValue(item.title, 'Untitled report'),
    summary: stringValue(item.summary ?? item.description),
    type: optionalString(item.type ?? item.kind),
    status,
    body: stringValue(item.body ?? item.content ?? item.markdown),
    createdAt: optionalString(item.created_at ?? item.createdAt),
    readAt: optionalString(item.read_at ?? item.readAt),
    archivedAt: optionalString(item.archived_at ?? item.archivedAt),
    relatedTasks: parseEntityRefs(item.related_tasks ?? item.tasks),
    artifacts: parseArtifacts(item.artifacts),
  }
}

export const reportsApi = {
  list: () => apiRequest<unknown>('/api/reports').then((raw) => list(extractValue(raw, 'reports', 'items')).map(parseReport)),
  get: (id: string) => apiRequest<unknown>(`/api/reports/${encodeURIComponent(id)}`).then((raw) => parseReport(extractValue(raw, 'report'))),
  update: (id: string, status: ReportStatus) => apiRequest<unknown>(`/api/reports/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ status }) }).then((raw) => parseReport(extractValue(raw, 'report'))),
  delete: (id: string) => apiRequest<void>(`/api/reports/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}
