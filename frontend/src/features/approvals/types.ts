import type { ArtifactRef } from '../shared/types'

export type ApprovalStatus = 'pending' | 'approved' | 'rejected' | 'expired'

export interface Approval {
  id: string
  action: string
  why: string
  status: ApprovalStatus
  taskId?: string
  taskTitle?: string
  changes: string[]
  evidence: string[]
  artifacts: ArtifactRef[]
  createdAt?: string
  resolvedAt?: string
  resolutionNote?: string
}
