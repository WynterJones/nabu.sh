import { apiRequest, booleanValue, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import type { BrowserMCPStatus, MCPAccess, MCPAuth, MCPSecretBinding, MCPServer, MCPTransport, SaveMCPServerInput } from './types'

const stringList = (value: unknown) => list(value).map((item) => stringValue(item).trim()).filter(Boolean)

const normalizeAuthStatus = (value: unknown) => {
  const status = stringValue(value, 'not_required').trim().toLowerCase()
  return ['logged_in', 'authenticated', 'oauth', 'o_auth'].includes(status) ? 'logged_in' : status === 'not_authenticated' ? 'not_logged_in' : status
}

function parseBinding(raw: unknown): MCPSecretBinding {
  const item = record(raw)
  return {
    secretId: stringValue(item.secret_id ?? item.secretId),
    envVar: stringValue(item.env_var ?? item.envVar),
    header: optionalString(item.header),
    bearer: booleanValue(item.bearer),
  }
}

export function parseMCPServer(raw: unknown): MCPServer {
  const item = record(raw)
  const transport = stringValue(item.transport) === 'stdio' ? 'stdio' : 'http'
  const access = stringValue(item.access) === 'full' ? 'full' : 'read'
  const authValue = stringValue(item.auth)
  const auth: MCPAuth = authValue === 'oauth' || authValue === 'secret' ? authValue : 'none'
  const enabled = booleanValue(item.enabled, true)
  const authStatus = normalizeAuthStatus(item.auth_status ?? item.authStatus)
  const missingSecrets = stringList(item.missing_secrets ?? item.missingSecrets)
  const oauthReady = enabled && auth === 'oauth' && authStatus === 'logged_in' && missingSecrets.length === 0
  return {
    id: stringValue(item.id),
    name: stringValue(item.name, 'MCP connector'),
    description: optionalString(item.description),
    transport,
    command: optionalString(item.command),
    args: stringList(item.args),
    url: optionalString(item.url),
    auth,
    enabled,
    access,
    required: booleanValue(item.required),
    startupTimeoutSeconds: Number(item.startup_timeout_seconds ?? item.startupTimeoutSeconds ?? 10),
    toolTimeoutSeconds: Number(item.tool_timeout_seconds ?? item.toolTimeoutSeconds ?? 60),
    enabledTools: stringList(item.enabled_tools ?? item.enabledTools),
    disabledTools: stringList(item.disabled_tools ?? item.disabledTools),
    secretBindings: list(item.secret_bindings ?? item.secretBindings).map(parseBinding).filter((binding) => binding.secretId && binding.envVar),
    ready: booleanValue(item.ready) || oauthReady,
    authStatus,
    missingSecrets,
    updatedAt: optionalString(item.updated_at ?? item.updatedAt),
  }
}

export function parseBrowserMCPStatus(raw: unknown): BrowserMCPStatus {
  const item = record(raw)
  return {
    name: stringValue(item.name, 'Nabu Browser'),
    available: booleanValue(item.available),
    provider: stringValue(item.provider, 'Playwright MCP'),
    packageName: stringValue(item.package),
    browser: stringValue(item.browser, 'Google Chrome'),
    isolated: booleanValue(item.isolated, true),
    reason: optionalString(item.reason),
  }
}

const requestBody = (input: SaveMCPServerInput) => ({
  name: input.name.trim(),
  description: input.description?.trim() ?? '',
  transport: input.transport,
  command: input.transport === 'stdio' ? input.command?.trim() ?? '' : '',
  args: input.transport === 'stdio' ? input.args : [],
  url: input.transport === 'http' ? input.url?.trim() ?? '' : '',
  auth: input.transport === 'http' ? input.auth : 'none',
  enabled: input.enabled,
  access: input.access,
  required: input.required,
  startup_timeout_seconds: input.startupTimeoutSeconds,
  tool_timeout_seconds: input.toolTimeoutSeconds,
  enabled_tools: input.enabledTools,
  disabled_tools: input.disabledTools,
  secret_bindings: input.secretBindings.map((binding) => ({
    secret_id: binding.secretId,
    env_var: binding.envVar.trim(),
    ...(binding.header ? { header: binding.header.trim() } : {}),
    ...(binding.bearer ? { bearer: true } : {}),
  })),
})

export const mcpApi = {
  browserStatus: () => apiRequest<unknown>('/api/mcp/browser').then((raw) => parseBrowserMCPStatus(extractValue(raw, 'browser'))),
  list: () => apiRequest<unknown>('/api/mcp/servers').then((raw) => list(extractValue(raw, 'servers', 'items')).map(parseMCPServer).filter((item) => item.id)),
  save: (input: SaveMCPServerInput) => apiRequest<unknown>(input.id ? `/api/mcp/servers/${encodeURIComponent(input.id)}` : '/api/mcp/servers', {
    method: input.id ? 'PATCH' : 'POST',
    body: JSON.stringify(requestBody(input)),
  }).then((raw) => parseMCPServer(extractValue(raw, 'server'))),
  delete: (id: string) => apiRequest<void>(`/api/mcp/servers/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  authenticate: (id: string) => apiRequest<{ started: boolean; authorization_url?: string }>(`/api/mcp/servers/${encodeURIComponent(id)}/authenticate`, { method: 'POST' }),
  authStatus: (id: string) => apiRequest<unknown>(`/api/mcp/servers/${encodeURIComponent(id)}/auth-status`).then((raw) => parseMCPServer(extractValue(raw, 'server'))),
}

export type { MCPAccess, MCPAuth, MCPTransport }
