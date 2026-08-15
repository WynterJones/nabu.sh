export type UnknownRecord = Record<string, unknown>

export class ExtendedApiError extends Error {
  readonly status: number
  readonly code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

export function record(value: unknown): UnknownRecord {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as UnknownRecord : {}
}

export function list(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

export function stringValue(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : value == null ? fallback : String(value)
}

export function optionalString(value: unknown): string | undefined {
  const parsed = stringValue(value).trim()
  return parsed || undefined
}

export function booleanValue(value: unknown, fallback = false): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value !== 0
  if (typeof value === 'string') return ['true', '1', 'yes', 'enabled', 'ok', 'available'].includes(value.toLowerCase())
  return fallback
}

export function extractValue(raw: unknown, ...keys: string[]): unknown {
  const body = record(raw)
  for (const key of keys) if (body[key] !== undefined) return body[key]
  const data = record(body.data)
  for (const key of keys) if (data[key] !== undefined) return data[key]
  return body.data ?? raw
}

export async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const formDataBody = typeof FormData !== 'undefined' && init?.body instanceof FormData
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body && !formDataBody ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })
  if (!response.ok) {
    let message = `Request failed (${response.status})`
    let code: string | undefined
    try {
      const body = record(await response.json())
      const error = record(body.error)
      message = stringValue(error.message ?? body.message ?? body.error, message)
      code = optionalString(error.code ?? body.code)
    } catch {
      // Preserve the HTTP fallback for non-JSON errors.
    }
    throw new ExtendedApiError(message, response.status, code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const jsonBody = (value: unknown): RequestInit => ({ body: JSON.stringify(value) })
