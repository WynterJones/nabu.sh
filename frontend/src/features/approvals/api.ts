import { apiRequest, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import { parseArtifacts } from '../shared/parsers'
import type { Approval, ApprovalStatus } from './types'

export function parseApproval(raw: unknown): Approval {
  const item = record(raw)
  const relatedTask = record(item.task ?? item.related_task)
  const statusValue = stringValue(item.status, 'pending').toLowerCase()
  const status: ApprovalStatus = ['approved', 'rejected', 'expired'].includes(statusValue) ? statusValue as ApprovalStatus : 'pending'
  return {
    id: stringValue(item.id ?? item.approval_id),
    action: stringValue(item.action ?? item.proposed_action ?? item.title, 'Proposed action'),
    why: stringValue(item.why ?? item.reason ?? item.summary),
    status,
    taskId: optionalString(item.task_id ?? relatedTask.id),
    taskTitle: optionalString(item.task_title ?? relatedTask.title),
    changes: list(item.changes ?? item.what_will_change).map((entry) => stringValue(entry)).filter(Boolean),
    evidence: list(item.evidence ?? item.verification).map((entry) => typeof entry === 'string' ? entry : stringValue(record(entry).details ?? record(entry).summary ?? record(entry).name)).filter(Boolean),
    artifacts: parseArtifacts(item.artifacts ?? item.preview),
    createdAt: optionalString(item.created_at ?? item.createdAt),
    resolvedAt: optionalString(item.resolved_at ?? item.resolvedAt),
    resolutionNote: optionalString(item.resolution_note ?? item.note),
  }
}

export const approvalsApi = {
  list: (status?: ApprovalStatus) => apiRequest<unknown>(`/api/approvals${status ? `?status=${encodeURIComponent(status)}` : ''}`).then((raw) => list(extractValue(raw, 'approvals', 'items')).map(parseApproval)),
  get: (id: string) => apiRequest<unknown>(`/api/approvals/${encodeURIComponent(id)}`).then((raw) => parseApproval(extractValue(raw, 'approval'))),
  resolve: (id: string, decision: 'approved' | 'rejected', note?: string) => apiRequest<unknown>(`/api/approvals/${encodeURIComponent(id)}/resolve`, { method: 'POST', body: JSON.stringify({ decision, rejection_note: decision === 'rejected' ? note ?? '' : undefined }) }).then((raw) => parseApproval(extractValue(raw, 'approval'))),
}
