import { afterEach, describe, expect, it, vi } from 'vitest'
import { mcpApi, parseBrowserMCPStatus, parseMCPServer } from './api'

afterEach(() => vi.unstubAllGlobals())

describe('MCP API', () => {
  it('parses built-in browser capability status', () => {
    expect(parseBrowserMCPStatus({ name: 'Nabu Browser', available: true, provider: 'Playwright MCP', package: '@playwright/mcp@0.0.79', browser: 'Google Chrome', isolated: true })).toEqual({
      name: 'Nabu Browser', available: true, provider: 'Playwright MCP', packageName: '@playwright/mcp@0.0.79', browser: 'Google Chrome', isolated: true, reason: undefined,
    })
  })

  it('parses snake case connector metadata without secret values', () => {
    expect(parseMCPServer({ id: 'mcp-1', name: 'Research', transport: 'http', url: 'https://mcp.example.com/mcp', auth: 'secret', auth_status: 'secret', access: 'read', enabled: true, ready: true, secret_bindings: [{ secret_id: 'secret-1', env_var: 'MCP_TOKEN', bearer: true }] })).toMatchObject({
      id: 'mcp-1', ready: true, transport: 'http', auth: 'secret', authStatus: 'secret', secretBindings: [{ secretId: 'secret-1', envVar: 'MCP_TOKEN', bearer: true }],
    })
  })

  it('normalizes Codex OAuth completion status into a connected connector', () => {
    expect(parseMCPServer({ id: 'mcp-1', name: 'Sentry', transport: 'http', url: 'https://mcp.sentry.dev/mcp/example', auth: 'oauth', auth_status: 'o_auth', access: 'read', enabled: true, ready: false })).toMatchObject({
      authStatus: 'logged_in', ready: true,
    })
  })

  it('sends a metadata-only remote connector payload', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      void input; void init
      return new Response(JSON.stringify({ server: { id: 'mcp-1', name: 'Research', transport: 'http', enabled: true, ready: true } }), { status: 201, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    await mcpApi.save({ name: 'Research', transport: 'http', url: 'https://mcp.example.com/mcp', auth: 'secret', args: [], enabled: true, access: 'read', required: false, startupTimeoutSeconds: 10, toolTimeoutSeconds: 60, enabledTools: [], disabledTools: [], secretBindings: [{ secretId: 'secret-1', envVar: 'MCP_TOKEN', bearer: true }] })
    const [, init] = fetchMock.mock.calls[0] as [RequestInfo | URL, RequestInit | undefined]
    expect(JSON.parse(String(init?.body))).toEqual(expect.objectContaining({ url: 'https://mcp.example.com/mcp', auth: 'secret', secret_bindings: [{ secret_id: 'secret-1', env_var: 'MCP_TOKEN', bearer: true }] }))
  })
})
