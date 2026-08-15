export type DatabaseFieldType = 'string' | 'integer' | 'number' | 'boolean' | 'datetime' | 'json'

export interface DatabaseField {
  name: string
  type: DatabaseFieldType
}

export interface DatabaseDataset {
  id: string
  name: string
  slug: string
  description?: string
  schema: DatabaseField[]
  uniqueKey: string[]
  rowCount: number
  deletedAt?: string
  createdAt?: string
  updatedAt?: string
}

export interface DatabaseRow {
  id: string
  values: Record<string, unknown>
  createdAt?: string
  updatedAt?: string
}

export interface DatabaseRowsPage {
  rows: DatabaseRow[]
  total?: number
  nextCursor?: string
}

export interface DatabaseRowsQuery {
  limit?: number
  cursor?: string
  sort?: string
  direction?: 'asc' | 'desc'
  q?: string
  filter?: { field: string; value: string }
}

export interface CreateDatabaseDataset {
  name: string
  description?: string
  schema: DatabaseField[]
  uniqueKey?: string[]
}
