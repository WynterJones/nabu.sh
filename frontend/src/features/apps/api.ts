import { apiRequest, booleanValue, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import type { LocalApp, LocalAppInput, LocalAppLogs, LocalAppStatus } from './types'

const statusValue = (value: unknown): LocalAppStatus => {
  const status = stringValue(value, 'stopped').toLowerCase()
  return status === 'running' || status === 'failed' ? status : 'stopped'
}

export function parseLocalApp(raw: unknown): LocalApp {
  const item = record(extractValue(raw, 'app'))
  const command = list(item.command).map((value) => stringValue(value)).filter(Boolean)
  const port = typeof item.port === 'number' ? item.port : Number(item.port) || 0
  const pid = typeof item.pid === 'number' ? item.pid : undefined
  const exitCode = typeof item.exit_code === 'number' ? item.exit_code : typeof item.exitCode === 'number' ? item.exitCode : undefined
  return {
    id: stringValue(item.id),
    name: stringValue(item.name, 'Local app'),
    description: optionalString(item.description),
    directory: stringValue(item.directory),
    command,
    port,
    healthPath: stringValue(item.health_path ?? item.healthPath, '/'),
    autoStart: booleanValue(item.auto_start ?? item.autoStart),
    status: statusValue(item.status),
    pid,
    url: stringValue(item.url, port ? `http://127.0.0.1:${port}` : ''),
    healthy: booleanValue(item.healthy),
    startedAt: optionalString(item.started_at ?? item.startedAt),
    stoppedAt: optionalString(item.stopped_at ?? item.stoppedAt),
    exitCode,
    error: optionalString(item.error),
  }
}

const payload = (input: LocalAppInput) => ({
  name: input.name.trim(),
  description: input.description?.trim() ?? '',
  directory: input.directory.trim(),
  command: input.command,
  port: input.port,
  health_path: input.healthPath.trim() || '/',
  auto_start: input.autoStart,
})

export const appsApi = {
  list: () => apiRequest<unknown>('/api/apps').then((raw) => list(extractValue(raw, 'apps', 'items')).map(parseLocalApp).filter((app) => app.id)),
  get: (id: string) => apiRequest<unknown>(`/api/apps/${encodeURIComponent(id)}`).then(parseLocalApp),
  create: (input: LocalAppInput) => apiRequest<unknown>('/api/apps', { method: 'POST', body: JSON.stringify(payload(input)) }).then(parseLocalApp),
  update: (id: string, input: LocalAppInput) => apiRequest<unknown>(`/api/apps/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload(input)) }).then(parseLocalApp),
  remove: (id: string) => apiRequest<void>(`/api/apps/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  start: (id: string) => apiRequest<unknown>(`/api/apps/${encodeURIComponent(id)}/start`, { method: 'POST' }).then(parseLocalApp),
  stop: (id: string) => apiRequest<unknown>(`/api/apps/${encodeURIComponent(id)}/stop`, { method: 'POST' }).then(parseLocalApp),
  restart: (id: string) => apiRequest<unknown>(`/api/apps/${encodeURIComponent(id)}/restart`, { method: 'POST' }).then(parseLocalApp),
  logs: (id: string) => apiRequest<unknown>(`/api/apps/${encodeURIComponent(id)}/logs`).then((raw): LocalAppLogs => {
    const value = record(extractValue(raw, 'logs'))
    return { appId: stringValue(value.app_id ?? value.appId, id), content: stringValue(value.content) }
  }),
}
