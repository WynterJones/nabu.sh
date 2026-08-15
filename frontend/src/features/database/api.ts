import { apiRequest, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import type { CreateDatabaseDataset, DatabaseDataset, DatabaseField, DatabaseFieldType, DatabaseRow, DatabaseRowsPage, DatabaseRowsQuery } from './types'

const fieldTypes: DatabaseFieldType[] = ['string', 'integer', 'number', 'boolean', 'datetime', 'json']

export function parseDatabaseField(raw: unknown): DatabaseField {
  const item = record(raw)
  const type = stringValue(item.type, 'string') as DatabaseFieldType
  return { name: stringValue(item.name), type: fieldTypes.includes(type) ? type : 'string' }
}

export function parseDatabaseDataset(raw: unknown): DatabaseDataset {
  const item = record(raw)
  const rowCount = Number(item.row_count ?? item.rowCount ?? 0)
  return {
    id: stringValue(item.id),
    name: stringValue(item.name),
    slug: stringValue(item.slug),
    description: optionalString(item.description),
    schema: list(item.schema ?? item.fields).map(parseDatabaseField).filter((field) => field.name),
    uniqueKey: list(item.unique_key ?? item.uniqueKey).map((value) => stringValue(value)).filter(Boolean),
    rowCount: Number.isFinite(rowCount) ? rowCount : 0,
    deletedAt: optionalString(item.deleted_at ?? item.deletedAt),
    createdAt: optionalString(item.created_at ?? item.createdAt),
    updatedAt: optionalString(item.updated_at ?? item.updatedAt),
  }
}

export function parseDatabaseRow(raw: unknown): DatabaseRow {
  const item = record(raw)
  const values = record(item.values ?? item.data)
  return {
    id: stringValue(item.id ?? item.row_id),
    values,
    createdAt: optionalString(item.created_at ?? item.createdAt),
    updatedAt: optionalString(item.updated_at ?? item.updatedAt),
  }
}

export function parseDatabaseRowsPage(raw: unknown): DatabaseRowsPage {
  const body = record(raw)
  const source = Object.keys(record(body.data)).length ? record(body.data) : body
  const totalValue = source.total == null ? undefined : Number(source.total)
  return {
    rows: list(source.rows ?? source.items ?? extractValue(raw, 'rows')).map(parseDatabaseRow).filter((row) => row.id),
    total: totalValue !== undefined && Number.isFinite(totalValue) ? totalValue : undefined,
    nextCursor: optionalString(source.next_cursor ?? source.nextCursor),
  }
}

const datasetPayload = (input: CreateDatabaseDataset) => ({
  name: input.name.trim(),
  ...(input.description?.trim() ? { description: input.description.trim() } : {}),
  schema: input.schema.map(({ name, type }) => ({ name: name.trim(), type })),
  ...(input.uniqueKey?.length ? { unique_key: input.uniqueKey } : {}),
})

export const databaseApi = {
  listDatasets: (includeDeleted = false) => apiRequest<unknown>(`/api/database/datasets${includeDeleted ? '?include_deleted=true' : ''}`).then((raw) => list(extractValue(raw, 'datasets', 'items')).map(parseDatabaseDataset).filter((item) => item.id)),
  createDataset: (input: CreateDatabaseDataset) => apiRequest<unknown>('/api/database/datasets', { method: 'POST', body: JSON.stringify(datasetPayload(input)) }).then((raw) => parseDatabaseDataset(extractValue(raw, 'dataset'))),
  getDataset: (id: string) => apiRequest<unknown>(`/api/database/datasets/${encodeURIComponent(id)}`).then((raw) => parseDatabaseDataset(extractValue(raw, 'dataset'))),
  updateDataset: (id: string, input: Partial<Pick<CreateDatabaseDataset, 'name' | 'description' | 'schema' | 'uniqueKey'>>) => apiRequest<unknown>(`/api/database/datasets/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ ...(input.name !== undefined ? { name: input.name.trim() } : {}), ...(input.description !== undefined ? { description: input.description.trim() } : {}), ...(input.schema ? { schema: input.schema } : {}), ...(input.uniqueKey ? { unique_key: input.uniqueKey } : {}) }) }).then((raw) => parseDatabaseDataset(extractValue(raw, 'dataset'))),
  deleteDataset: (id: string) => apiRequest<void>(`/api/database/datasets/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  restoreDataset: (id: string) => apiRequest<unknown>(`/api/database/datasets/${encodeURIComponent(id)}/restore`, { method: 'POST' }).then((raw) => parseDatabaseDataset(extractValue(raw, 'dataset'))),
  listRows: (id: string, query: DatabaseRowsQuery = {}) => {
    const params = new URLSearchParams({ limit: String(query.limit ?? 50) })
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.sort) params.set('sort', query.sort)
    if (query.direction) params.set('direction', query.direction)
    if (query.q?.trim()) params.set('q', query.q.trim())
    if (query.filter?.field && query.filter.value.trim()) params.set(`filter[${query.filter.field}]`, query.filter.value.trim())
    return apiRequest<unknown>(`/api/database/datasets/${encodeURIComponent(id)}/rows?${params}`).then(parseDatabaseRowsPage)
  },
  createRows: (id: string, rows: Array<Record<string, unknown>>, mode: 'insert' | 'upsert' = 'insert') => apiRequest<unknown>(`/api/database/datasets/${encodeURIComponent(id)}/rows`, { method: 'POST', body: JSON.stringify({ rows, mode }) }),
  updateRow: (datasetId: string, rowId: string, values: Record<string, unknown>) => apiRequest<unknown>(`/api/database/datasets/${encodeURIComponent(datasetId)}/rows/${encodeURIComponent(rowId)}`, { method: 'PATCH', body: JSON.stringify({ values }) }).then((raw) => parseDatabaseRow(extractValue(raw, 'row'))),
  deleteRow: (datasetId: string, rowId: string) => apiRequest<void>(`/api/database/datasets/${encodeURIComponent(datasetId)}/rows/${encodeURIComponent(rowId)}`, { method: 'DELETE' }),
  exportUrl: (id: string, format: 'csv' | 'json') => `/api/database/datasets/${encodeURIComponent(id)}/export?format=${format}`,
}

export function coerceDatabaseValue(value: string, type: DatabaseFieldType): unknown {
  const trimmed = value.trim()
  if (!trimmed) return type === 'string' ? '' : null
  if (type === 'integer') {
    const parsed = Number.parseInt(trimmed, 10)
    if (!Number.isInteger(parsed) || String(parsed) !== trimmed) throw new Error('Enter a whole number.')
    return parsed
  }
  if (type === 'number') {
    const parsed = Number(trimmed)
    if (!Number.isFinite(parsed)) throw new Error('Enter a valid number.')
    return parsed
  }
  if (type === 'boolean') return trimmed === 'true'
  if (type === 'json') {
    try { return JSON.parse(trimmed) as unknown }
    catch { throw new Error('Enter valid JSON.') }
  }
  return trimmed
}

export function parseDatabaseImport(value: string): Array<Record<string, unknown>> {
  const parsed: unknown = JSON.parse(value)
  const rows = Array.isArray(parsed) ? parsed : list(record(parsed).rows)
  if (!rows.length || rows.some((row) => !Object.keys(record(row)).length)) throw new Error('Use a JSON array containing at least one row object.')
  return rows.map(record)
}
