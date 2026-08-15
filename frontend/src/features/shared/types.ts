export interface ArtifactRef {
  id?: string
  kind: string
  name: string
  path?: string
  url?: string
  mimeType?: string
}

export interface EntityRef {
  id: string
  type: 'task' | 'report' | 'approval' | 'run' | string
  title: string
  status?: string
  summary?: string
}
