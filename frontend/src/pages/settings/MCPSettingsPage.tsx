import { Blocks, Camera, ExternalLink, LoaderCircle, LogIn, MonitorCheck, MousePointer2, Pencil, Plus, Server, ShieldCheck, Trash2, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card, EmptyState } from '../../components/ui/Card'
import { Dialog } from '../../components/ui/Dialog'
import { Field, Input, Textarea } from '../../components/ui/Field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/Select'
import { Switch } from '../../components/ui/Switch'
import { mcpApi } from '../../features/mcp/api'
import type { MCPAuth, MCPServer, MCPTransport, SaveMCPServerInput } from '../../features/mcp/types'
import { secretsApi } from '../../features/secrets/api'
import { useResource } from '../../hooks/useResource'
import { cn, formatRelativeTime, isAbsoluteWorkspacePath } from '../../lib/utils'

interface BindingForm { key: number; secretId: string; envVar: string; mode: 'env' | 'bearer' | 'header'; header: string }
interface ServerForm {
  name: string; description: string; transport: MCPTransport; command: string; args: string; url: string
  auth: MCPAuth
  enabled: boolean; access: 'read' | 'full'; required: boolean; startupTimeout: string; toolTimeout: string
  enabledTools: string; disabledTools: string; bindings: BindingForm[]
}

const emptyForm = (): ServerForm => ({ name: '', description: '', transport: 'http', command: '', args: '', url: '', auth: 'oauth', enabled: true, access: 'read', required: false, startupTimeout: '10', toolTimeout: '60', enabledTools: '', disabledTools: '', bindings: [] })
const lines = (value: string) => value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean)
const validEnvironment = (value: string) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(value)

function toForm(server: MCPServer): ServerForm {
  return {
    name: server.name, description: server.description ?? '', transport: server.transport, command: server.command ?? '', args: server.args.join('\n'), url: server.url ?? '', auth: server.auth,
    enabled: server.enabled, access: server.access, required: server.required, startupTimeout: String(server.startupTimeoutSeconds), toolTimeout: String(server.toolTimeoutSeconds),
    enabledTools: server.enabledTools.join('\n'), disabledTools: server.disabledTools.join('\n'), bindings: server.secretBindings.map((binding, index) => ({ key: index + 1, secretId: binding.secretId, envVar: binding.envVar, mode: binding.bearer ? 'bearer' : binding.header ? 'header' : 'env', header: binding.header ?? '' })),
  }
}

export function MCPSettingsPage() {
  const { data, setData, loading, error, refresh } = useResource(mcpApi.list)
  const browser = useResource(mcpApi.browserStatus)
  const secrets = useResource(secretsApi.list)
  const servers = data ?? []
  const [editing, setEditing] = useState<MCPServer | null | undefined>(undefined)
  const [form, setForm] = useState<ServerForm>(emptyForm)
  const [nextBindingKey, setNextBindingKey] = useState(1)
  const [busy, setBusy] = useState(false)
  const [authenticatingId, setAuthenticatingId] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<MCPServer | null>(null)

  const openForm = (server: MCPServer | null) => {
    setEditing(server)
    setForm(server ? toForm(server) : emptyForm())
    setNextBindingKey((server?.secretBindings.length ?? 0) + 1)
    setActionError(null)
  }
  const bindingInvalid = form.bindings.some((binding) => !binding.secretId || !validEnvironment(binding.envVar) || (form.transport === 'http' && binding.mode === 'header' && !binding.header.trim()))
  const duplicateEnvironment = new Set(form.bindings.map((binding) => binding.envVar.trim())).size !== form.bindings.length
  const endpointInvalid = form.transport === 'http' ? !/^https:\/\//i.test(form.url) && !/^http:\/\/(localhost|127\.0\.0\.1|\[::1\])(?=[:/]|$)/i.test(form.url) : !isAbsoluteWorkspacePath(form.command)
  const formInvalid = !form.name.trim() || endpointInvalid || bindingInvalid || duplicateEnvironment || (form.transport === 'http' && form.auth === 'secret' && !form.bindings.length)

  useEffect(() => {
    if (!authenticatingId) return
    const poll = window.setInterval(() => {
      void mcpApi.authStatus(authenticatingId).then((server) => {
        setData((current) => (current ?? []).map((item) => item.id === server.id ? server : item))
        if (server.authStatus === 'logged_in') setAuthenticatingId(null)
      }).catch(() => undefined)
    }, 1500)
    return () => window.clearInterval(poll)
  }, [authenticatingId, setData])

  const save = async () => {
    if (formInvalid || busy) return
    setBusy(true); setActionError(null)
    const input: SaveMCPServerInput = {
      id: editing?.id, name: form.name, description: form.description, transport: form.transport, command: form.command, args: lines(form.args), url: form.url, auth: form.transport === 'http' ? form.auth : 'none',
      enabled: form.enabled, access: form.access, required: form.required, startupTimeoutSeconds: Number(form.startupTimeout), toolTimeoutSeconds: Number(form.toolTimeout),
      enabledTools: lines(form.enabledTools), disabledTools: lines(form.disabledTools),
      secretBindings: form.bindings.map((binding) => ({ secretId: binding.secretId, envVar: binding.envVar.trim(), bearer: form.transport === 'http' && binding.mode === 'bearer', header: form.transport === 'http' && binding.mode === 'header' ? binding.header.trim() : undefined })),
    }
    try {
      const saved = await mcpApi.save(input)
      setData((current) => editing ? (current ?? []).map((item) => item.id === saved.id ? saved : item) : [...(current ?? []), saved])
      setEditing(undefined)
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'The MCP connector could not be saved.')
    } finally { setBusy(false) }
  }
  const remove = async () => {
    if (!deleting || busy) return
    setBusy(true); setActionError(null)
    try { await mcpApi.delete(deleting.id); setData((current) => (current ?? []).filter((item) => item.id !== deleting.id)); setDeleting(null) }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'The MCP connector could not be deleted.') }
    finally { setBusy(false) }
  }
  const addBinding = () => {
    const mode = form.transport === 'http' ? 'bearer' : 'env'
    setForm((current) => ({ ...current, bindings: [...current.bindings, { key: nextBindingKey, secretId: '', envVar: '', mode, header: '' }] }))
    setNextBindingKey((value) => value + 1)
  }
  const authenticate = async (server: MCPServer) => {
    if (authenticatingId) return
    // Reserve the tab while this click still has a user activation. Opening it
    // only after the API round-trip is commonly blocked as an unsolicited popup.
    const authorizationTab = window.open('', '_blank')
    if (authorizationTab) {
      authorizationTab.opener = null
      authorizationTab.document.title = 'Connecting MCP server…'
    }
    setAuthenticatingId(server.id); setActionError(null)
    try {
      const result = await mcpApi.authenticate(server.id)
      if (result.authorization_url) {
        if (authorizationTab) authorizationTab.location.replace(result.authorization_url)
        else window.open(result.authorization_url, '_blank', 'noopener,noreferrer')
      } else authorizationTab?.close()
    } catch (caught) {
      authorizationTab?.close()
      setAuthenticatingId(null)
      setActionError(caught instanceof Error ? caught.message : 'MCP sign-in could not be started.')
    }
  }

  if ((loading && !data) || (browser.loading && !browser.data)) return <PageLoading label="Loading MCP connectors…" />
  return (
    <div className="settings-content-stack">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div><h2 className="settings-title">MCP connectors</h2><p className="settings-description">Connect any compatible remote server or local process. Its tools become available directly to Nabu in Chat and task runs.</p></div>
        <Button variant="primary" onClick={() => openForm(null)}><Plus className="size-4" />Add connector</Button>
      </div>
      <div className="credential-safety">
        <ShieldCheck className="size-5 shrink-0 text-accent" />
        <div><p className="text-sm font-medium text-ink">Workspace-scoped and secret-safe</p><p className="mt-1 text-xs leading-relaxed text-muted">Connection metadata stays with this workspace. Tokens are resolved from Secrets only when Codex starts and are never written into Chat, SQLite, command arguments, or run output.</p></div>
      </div>
      <Card className="browser-capability-card">
        <span className="browser-capability-icon"><MonitorCheck className="size-5" /></span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold text-ink">Browser tools</h3>
            <Badge variant={browser.data?.available ? 'success' : 'warning'}>{browser.data?.available ? 'Ready' : 'Unavailable'}</Badge>
          </div>
          <p className="mt-1 text-sm leading-relaxed text-muted">Codex can inspect and interact with pages, capture screenshots, and check responsive UI through an isolated Chrome session.</p>
          <div className="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-xs text-muted">
            <span className="inline-flex items-center gap-1.5"><Camera className="size-3.5 text-accent" />Screenshots and visual QA</span>
            <span className="inline-flex items-center gap-1.5"><MousePointer2 className="size-3.5 text-accent" />Interaction and focus checks</span>
          </div>
          {browser.data?.available ? (
            <p className="mt-3 text-[11px] text-muted">{browser.data.provider} · {browser.data.packageName} · {browser.data.browser} · isolated profile</p>
          ) : (
            <p className="mt-3 text-xs text-warning">{browser.data?.reason ?? 'Install Node.js and Google Chrome to enable browser tools.'}</p>
          )}
        </div>
      </Card>
      {(error || actionError || secrets.error || browser.error) ? <InlineError message={actionError ?? error ?? secrets.error ?? browser.error ?? ''} /> : null}
      {!servers.length ? (
        <EmptyState compact icon={<Blocks className="size-5" />} title="No MCP connectors" description="Add a Streamable HTTP server or a local STDIO command. Nabu will use matching tools on its next run." action={<Button variant="primary" onClick={() => openForm(null)}><Plus className="size-4" />Add connector</Button>} />
      ) : (
        <div className="space-y-2">{servers.map((server) => (
          <Card key={server.id} className="mcp-server-row shadow-none">
            <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><Server className="size-5" /></span>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-semibold text-ink">{server.name}</h3><Badge variant={!server.enabled ? 'outline' : server.ready ? 'success' : 'warning'}>{!server.enabled ? 'Disabled' : server.ready ? 'Connected' : server.auth === 'oauth' ? 'Sign in required' : 'Needs secret'}</Badge><Badge variant="outline">{server.transport === 'http' ? 'Remote HTTP' : 'Local STDIO'}</Badge></div>
              <p className="mt-1 truncate font-mono text-[11px] text-muted">{server.transport === 'http' ? server.url : [server.command, ...server.args].join(' ')}</p>
              {server.description ? <p className="mt-1 line-clamp-1 text-xs text-muted">{server.description}</p> : null}
              <p className="mt-1 text-[11px] text-muted">{server.access === 'read' ? 'Read-only tools' : 'Full tool access'}{server.secretBindings.length ? ` · ${server.secretBindings.length} secret ${server.secretBindings.length === 1 ? 'binding' : 'bindings'}` : ''}{server.updatedAt ? ` · Updated ${formatRelativeTime(server.updatedAt)}` : ''}</p>
            </div>
            {server.enabled && server.auth === 'oauth' && server.authStatus !== 'logged_in' ? <Button variant="secondary" size="sm" onClick={() => void authenticate(server)} disabled={Boolean(authenticatingId)}>{authenticatingId === server.id ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <LogIn className="size-4" />}{authenticatingId === server.id ? 'Waiting for sign-in…' : 'Sign in'}</Button> : null}
            <Button variant="ghost" size="icon" onClick={() => openForm(server)} aria-label={`Edit ${server.name}`}><Pencil className="size-4" /></Button>
            <Button variant="ghost" size="icon" onClick={() => setDeleting(server)} aria-label={`Delete ${server.name}`}><Trash2 className="size-4" /></Button>
          </Card>
        ))}</div>
      )}
      <div className="flex justify-between gap-3 text-xs text-muted"><button type="button" className="file-link" onClick={() => void refresh()}>Refresh connectors</button><a className="file-link inline-flex items-center gap-1.5" href="https://developers.openai.com/codex/mcp/" target="_blank" rel="noreferrer">MCP setup reference<ExternalLink className="size-3" /></a></div>

      <Dialog open={editing !== undefined} onOpenChange={(open) => { if (!open && !busy) setEditing(undefined) }} title={editing ? `Edit ${editing.name}` : 'Add MCP connector'} description="Add any compatible MCP server. Nabu receives its tools on new Chat, task, and orientation runs." className="max-w-[720px]" footer={<><Button variant="ghost" disabled={busy} onClick={() => setEditing(undefined)}>Cancel</Button><Button variant="primary" disabled={busy || formInvalid} onClick={() => void save()}>{busy ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Blocks className="size-4" />}{busy ? 'Saving…' : 'Save connector'}</Button></>}>
        <div className="space-y-5">
          <div className="form-grid"><Field label="Name" hint="Required"><Input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} placeholder="Research tools" autoFocus /></Field><Field label="Transport"><Select value={form.transport} onValueChange={(transport: MCPTransport) => setForm((current) => ({ ...current, transport, bindings: current.bindings.map((binding) => ({ ...binding, mode: transport === 'stdio' ? 'env' : binding.mode === 'env' ? 'bearer' : binding.mode, header: transport === 'stdio' ? '' : binding.header })) }))}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="http">Remote HTTP</SelectItem><SelectItem value="stdio">Local STDIO</SelectItem></SelectContent></Select></Field></div>
          <Field label="Description" hint="Optional"><Input value={form.description} onChange={(event) => setForm((current) => ({ ...current, description: event.target.value }))} placeholder="What tools this server gives Nabu." /></Field>
          {form.transport === 'http' ? <><Field label="Server URL" hint="HTTPS required" error={form.url && endpointInvalid ? 'Use HTTPS. Plain HTTP is allowed only for localhost.' : undefined}><Input type="url" value={form.url} onChange={(event) => setForm((current) => ({ ...current, url: event.target.value }))} placeholder="https://mcp.example.com/mcp" className="font-mono text-xs" /></Field><Field label="Authentication"><Select value={form.auth} onValueChange={(auth: MCPAuth) => setForm((current) => ({ ...current, auth, bindings: auth === 'secret' ? current.bindings : [] }))}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="oauth">Sign in with browser (OAuth)</SelectItem><SelectItem value="secret">Saved bearer token or header</SelectItem><SelectItem value="none">No authentication</SelectItem></SelectContent></Select><span className="text-xs leading-relaxed text-muted">{form.auth === 'oauth' ? 'After saving, select Sign in. Nabu opens the provider authorization page in a new tab.' : form.auth === 'secret' ? 'Bind a value from the protected Secrets vault below.' : 'Use only when the MCP server does not require a login or token.'}</span></Field></> : <><Field label="Executable command" hint="Absolute path" error={form.command && endpointInvalid ? 'Use the absolute executable path.' : undefined}><Input value={form.command} onChange={(event) => setForm((current) => ({ ...current, command: event.target.value }))} placeholder="/opt/homebrew/bin/npx" className="font-mono text-xs" /></Field><Field label="Arguments" hint="One per line"><Textarea value={form.args} onChange={(event) => setForm((current) => ({ ...current, args: event.target.value }))} placeholder={'-y\n@modelcontextprotocol/server-filesystem\n/Users/you/Code'} className="font-mono text-xs" /></Field></>}
          <div className="form-grid"><Field label="Tool access"><Select value={form.access} onValueChange={(access: 'read' | 'full') => setForm((current) => ({ ...current, access }))}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="read">Read tools only</SelectItem><SelectItem value="full">Full tool access</SelectItem></SelectContent></Select><span className={form.access === 'full' ? 'text-xs leading-relaxed text-warning' : 'text-xs leading-relaxed text-muted'}>{form.access === 'read' ? 'Tools marked read-only can run automatically. Write tools remain blocked.' : 'Nabu may run this server’s write tools. Use only with a server you trust.'}</span></Field><div className="space-y-2"><div className="permission-row rounded-lg border"><div><p className="text-sm font-medium text-ink">Enabled</p><p className="mt-0.5 text-xs text-muted">Available to new Codex runs.</p></div><Switch checked={form.enabled} onCheckedChange={(enabled) => setForm((current) => ({ ...current, enabled }))} /></div><div className="permission-row rounded-lg border"><div><p className="text-sm font-medium text-ink">Required</p><p className="mt-0.5 text-xs text-muted">Stop a run if its secret is unavailable.</p></div><Switch checked={form.required} onCheckedChange={(required) => setForm((current) => ({ ...current, required }))} /></div></div></div>
          {(form.transport === 'stdio' || form.auth === 'secret') ? <fieldset><div className="flex items-center justify-between gap-3"><legend className="field-label">Secret bindings</legend><Button variant="secondary" size="sm" onClick={addBinding} disabled={!secrets.data?.length}><Plus className="size-3.5" />Add secret</Button></div><p className="mt-1 text-xs leading-relaxed text-muted">Bind a saved value without exposing it. Remote servers support bearer tokens or custom headers; local servers receive environment variables.</p>{!secrets.data?.length ? <div className="mt-3 rounded-lg border border-dashed border-line bg-canvas p-3 text-xs text-muted">Add a value under <Link className="file-link" to="/settings/secrets">Settings → Secrets</Link> first.</div> : null}<div className="mt-3 space-y-3">{form.bindings.map((binding, index) => <div key={binding.key} className={cn('mcp-binding-row', form.transport === 'http' && binding.mode === 'header' && 'mcp-binding-row-header')}><Select value={binding.secretId || undefined} onValueChange={(secretId) => setForm((current) => ({ ...current, bindings: current.bindings.map((item) => item.key === binding.key ? { ...item, secretId } : item) }))}><SelectTrigger aria-label={`Binding ${index + 1} secret`}><SelectValue placeholder="Choose secret" /></SelectTrigger><SelectContent>{(secrets.data ?? []).map((secret) => <SelectItem key={secret.id} value={secret.id}>{secret.name}</SelectItem>)}</SelectContent></Select><Field label="Runtime variable"><Input value={binding.envVar} onChange={(event) => setForm((current) => ({ ...current, bindings: current.bindings.map((item) => item.key === binding.key ? { ...item, envVar: event.target.value.toUpperCase() } : item) }))} placeholder="MCP_API_TOKEN" className="font-mono text-xs" /></Field>{form.transport === 'http' ? <Select value={binding.mode} onValueChange={(mode: 'bearer' | 'header') => setForm((current) => ({ ...current, bindings: current.bindings.map((item) => item.key === binding.key ? { ...item, mode } : item) }))}><SelectTrigger aria-label={`Binding ${index + 1} authentication type`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="bearer">Bearer token</SelectItem><SelectItem value="header">Custom header</SelectItem></SelectContent></Select> : null}{form.transport === 'http' && binding.mode === 'header' ? <Field label="Header"><Input value={binding.header} onChange={(event) => setForm((current) => ({ ...current, bindings: current.bindings.map((item) => item.key === binding.key ? { ...item, header: event.target.value } : item) }))} placeholder="X-API-Key" /></Field> : null}<Button variant="ghost" size="icon" aria-label={`Remove secret binding ${index + 1}`} onClick={() => setForm((current) => ({ ...current, bindings: current.bindings.filter((item) => item.key !== binding.key) }))}><X className="size-4" /></Button></div>)}</div>{(bindingInvalid || duplicateEnvironment) && form.bindings.length ? <p className="mt-2 text-xs text-danger">Complete each binding with a unique valid runtime variable and authentication method.</p> : null}</fieldset> : null}
          <details className="rounded-lg border border-line bg-canvas p-4"><summary className="cursor-pointer text-sm font-medium text-ink">Advanced tool controls</summary><div className="mt-4 space-y-4"><div className="form-grid"><Field label="Allow only these tools" hint="Optional"><Textarea value={form.enabledTools} onChange={(event) => setForm((current) => ({ ...current, enabledTools: event.target.value }))} placeholder="One tool name per line" className="font-mono text-xs" /></Field><Field label="Always block these tools" hint="Optional"><Textarea value={form.disabledTools} onChange={(event) => setForm((current) => ({ ...current, disabledTools: event.target.value }))} placeholder="One tool name per line" className="font-mono text-xs" /></Field></div><div className="form-grid"><Field label="Startup timeout (seconds)"><Input type="number" min={1} max={120} value={form.startupTimeout} onChange={(event) => setForm((current) => ({ ...current, startupTimeout: event.target.value }))} /></Field><Field label="Tool timeout (seconds)"><Input type="number" min={1} max={600} value={form.toolTimeout} onChange={(event) => setForm((current) => ({ ...current, toolTimeout: event.target.value }))} /></Field></div></div></details>
          {actionError ? <InlineError message={actionError} /> : null}
        </div>
      </Dialog>
      <ConfirmDialog open={deleting !== null} onOpenChange={(open) => { if (!open && !busy) setDeleting(null) }} title="Delete MCP connector?" description="This removes the connector from future Nabu runs. Saved secrets remain in the system vault." details={deleting?.name} confirmLabel="Delete connector" destructive pending={busy} onConfirm={() => void remove()} />
    </div>
  )
}
