import { apiRequest, booleanValue, extractValue, list, record, stringValue } from '../../lib/api/client'

export type WorkspaceFileKind = 'directory' | 'text' | 'image' | 'video' | 'pdf' | 'unsupported'

export interface WorkspaceFileEntry {
  path: string
  name: string
  kind: 'directory' | 'file'
  size: number
  modifiedAt?: string
}

export interface WorkspaceFile {
  path: string
  name: string
  kind: WorkspaceFileKind
  mimeType: string
  size: number
  editable: boolean
  content: string
  modifiedAt?: string
  entries: WorkspaceFileEntry[]
  truncated: boolean
}

export function parseWorkspaceFile(raw: unknown): WorkspaceFile {
  const item = record(extractValue(raw, 'file'))
  const kind = stringValue(item.kind, 'unsupported')
  return {
    path: stringValue(item.path),
    name: stringValue(item.name ?? item.path, 'File'),
    kind: kind === 'directory' || kind === 'text' || kind === 'image' || kind === 'video' || kind === 'pdf' ? kind : 'unsupported',
    mimeType: stringValue(item.mime_type ?? item.mimeType, 'application/octet-stream'),
    size: typeof item.size === 'number' ? item.size : 0,
    editable: booleanValue(item.editable),
    content: stringValue(item.content),
    modifiedAt: stringValue(item.modified_at ?? item.modifiedAt) || undefined,
    entries: list(item.entries).map((value) => {
      const entry = record(value)
      return {
        path: stringValue(entry.path),
        name: stringValue(entry.name ?? entry.path, 'Item'),
        kind: stringValue(entry.kind) === 'directory' ? 'directory' as const : 'file' as const,
        size: typeof entry.size === 'number' ? entry.size : 0,
        modifiedAt: stringValue(entry.modified_at ?? entry.modifiedAt) || undefined,
      }
    }).filter((entry) => Boolean(entry.path)),
    truncated: booleanValue(item.truncated),
  }
}

const filePath = (path: string) => `path=${encodeURIComponent(path)}`

export const filesApi = {
  get: (path: string) => apiRequest<unknown>(`/api/files?${filePath(path)}`).then(parseWorkspaceFile),
  save: (path: string, content: string) => apiRequest<unknown>(`/api/files?${filePath(path)}`, { method: 'PUT', body: JSON.stringify({ content }) }).then(parseWorkspaceFile),
  contentUrl: (path: string) => `/api/files/content?${filePath(path)}`,
}
