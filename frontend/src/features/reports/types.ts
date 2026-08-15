import type { ArtifactRef, EntityRef } from '../shared/types'

export type ReportStatus = 'unread' | 'read' | 'archived'

export interface Report {
  id: string
  title: string
  summary: string
  type?: string
  status: ReportStatus
  body: string
  createdAt?: string
  readAt?: string
  archivedAt?: string
  relatedTasks: EntityRef[]
  artifacts: ArtifactRef[]
}
