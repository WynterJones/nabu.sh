import type { ScriptEntry } from '../settings/types'

export interface WorkspaceOutput {
  id: string
  kind: string
  name: string
  path?: string
  url?: string
  fileKind?: 'text' | 'image' | 'video' | 'pdf' | 'unsupported'
  mimeType?: string
  size: number
  editable: boolean
  taskId?: string
  taskTitle?: string
  scriptRunId?: string
  createdAt?: string
}

export interface WorkspaceOutputs {
  items: WorkspaceOutput[]
  scripts: ScriptEntry[]
}
