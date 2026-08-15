export type MCPTransport = 'http' | 'stdio'
export type MCPAccess = 'read' | 'full'
export type MCPAuth = 'none' | 'oauth' | 'secret'

export interface MCPSecretBinding {
  secretId: string
  envVar: string
  header?: string
  bearer?: boolean
}

export interface MCPServer {
  id: string
  name: string
  description?: string
  transport: MCPTransport
  command?: string
  args: string[]
  url?: string
  auth: MCPAuth
  enabled: boolean
  access: MCPAccess
  required: boolean
  startupTimeoutSeconds: number
  toolTimeoutSeconds: number
  enabledTools: string[]
  disabledTools: string[]
  secretBindings: MCPSecretBinding[]
  ready: boolean
  authStatus: string
  missingSecrets: string[]
  updatedAt?: string
}

export interface SaveMCPServerInput extends Omit<MCPServer, 'id' | 'ready' | 'authStatus' | 'missingSecrets' | 'updatedAt'> {
  id?: string
}

export interface BrowserMCPStatus {
  name: string
  available: boolean
  provider: string
  packageName: string
  browser: string
  isolated: boolean
  reason?: string
}
