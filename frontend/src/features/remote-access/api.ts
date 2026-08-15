import { apiRequest, booleanValue, record, stringValue } from '../../lib/api/client'

export interface TailscaleStatus {
  installed: boolean
  connected: boolean
  version?: string
  machineName?: string
  dnsName?: string
  tailnetName?: string
  privateUrl?: string
  serveConfigured: boolean
  funnelConfigured: boolean
  authorizationUrl?: string
}

export interface TailscaleSetupResult {
  status: TailscaleStatus
  authorizationUrl?: string
}

export function parseTailscaleStatus(raw: unknown): TailscaleStatus {
  const item = record(raw)
  return {
    installed: booleanValue(item.installed),
    connected: booleanValue(item.connected),
    version: stringValue(item.version) || undefined,
    machineName: stringValue(item.machine_name ?? item.machineName) || undefined,
    dnsName: stringValue(item.dns_name ?? item.dnsName) || undefined,
    tailnetName: stringValue(item.tailnet_name ?? item.tailnetName) || undefined,
    privateUrl: stringValue(item.private_url ?? item.privateUrl) || undefined,
    serveConfigured: booleanValue(item.serve_configured ?? item.serveConfigured),
    funnelConfigured: booleanValue(item.funnel_configured ?? item.funnelConfigured),
    authorizationUrl: stringValue(item.authorization_url ?? item.authorizationUrl) || undefined,
  }
}

export const remoteAccessApi = {
  tailscale: () => apiRequest<unknown>('/api/remote-access/tailscale').then(parseTailscaleStatus),
  enableTailscale: () => apiRequest<unknown>('/api/remote-access/tailscale/serve', { method: 'POST' }).then((raw): TailscaleSetupResult => {
    const item = record(raw)
    return {
      status: parseTailscaleStatus(item.status),
      authorizationUrl: stringValue(item.authorization_url ?? item.authorizationUrl) || undefined,
    }
  }),
  disableTailscale: () => apiRequest<unknown>('/api/remote-access/tailscale/serve', { method: 'DELETE' }).then(parseTailscaleStatus),
}
