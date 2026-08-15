import type { ArtifactRef, EntityRef } from './types'
import { list, optionalString, record, stringValue } from '../../lib/api/client'

export function parseArtifact(raw: unknown): ArtifactRef {
  const item = record(raw)
  return {
    id: optionalString(item.id),
    kind: stringValue(item.kind ?? item.type, 'artifact'),
    name: stringValue(item.name ?? item.title ?? item.path ?? item.url, 'Artifact'),
    path: optionalString(item.path),
    url: optionalString(item.url),
    mimeType: optionalString(item.mime_type ?? item.mimeType),
  }
}

export function parseEntityRef(raw: unknown): EntityRef {
  const item = record(raw)
  return {
    id: stringValue(item.id ?? item.entity_id),
    type: stringValue(item.type ?? item.entity_type ?? item.kind, 'task'),
    title: stringValue(item.title ?? item.name ?? item.label, 'Referenced item'),
    status: optionalString(item.status),
    summary: optionalString(item.summary ?? item.description),
  }
}

export const parseArtifacts = (value: unknown): ArtifactRef[] => list(value).map(parseArtifact).filter((item) => item.name)
export const parseEntityRefs = (value: unknown): EntityRef[] => list(value).map(parseEntityRef).filter((item) => item.id)
