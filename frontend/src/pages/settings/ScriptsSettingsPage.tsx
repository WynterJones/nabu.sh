import { FileCode2, KeyRound, LoaderCircle, Pencil, Play, Plus, Trash2, X } from 'lucide-react'
import { useState } from 'react'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card, EmptyState } from '../../components/ui/Card'
import { Dialog } from '../../components/ui/Dialog'
import { Field, Input, Textarea } from '../../components/ui/Field'
import { Switch } from '../../components/ui/Switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/Select'
import { secretsApi } from '../../features/secrets/api'
import { settingsApi } from '../../features/settings/api'
import type { ScriptEntry } from '../../features/settings/types'
import { useResource } from '../../hooks/useResource'
import { formatRelativeTime, isAbsoluteWorkspacePath } from '../../lib/utils'

interface ScriptBindingForm { key: number; secretId: string; envVar: string }
interface ScriptForm { name: string; path: string; description: string; enabled: boolean; access: 'read' | 'write'; timeout: string; bindings: ScriptBindingForm[] }
const emptyForm = (): ScriptForm => ({ name: '', path: '', description: '', enabled: true, access: 'read', timeout: '300', bindings: [] })
const validEnvVar = (value: string) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(value)

export function ScriptsSettingsPage() {
  const { data, setData, loading, error } = useResource(settingsApi.listScripts)
  const secrets = useResource(secretsApi.list)
  const scripts = data ?? []
  const [editing, setEditing] = useState<ScriptEntry | null | undefined>(undefined)
  const [form, setForm] = useState<ScriptForm>(emptyForm)
  const [nextBindingKey, setNextBindingKey] = useState(1)
  const [busy, setBusy] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<ScriptEntry | null>(null)
  const openForm = (script: ScriptEntry | null) => {
    setEditing(script)
    setForm(script ? { name: script.name, path: script.path ?? '', description: script.description ?? '', enabled: script.enabled, access: script.access, timeout: String(script.timeoutSeconds ?? 300), bindings: script.secretBindings.map((binding, index) => ({ ...binding, key: index + 1 })) } : emptyForm())
    setNextBindingKey((script?.secretBindings.length ?? 0) + 1)
    setActionError(null)
  }
  const save = async () => {
    const bindingEnvVars = form.bindings.map((binding) => binding.envVar.trim())
    if (!form.name.trim() || !isAbsoluteWorkspacePath(form.path) || form.bindings.some((binding) => !binding.secretId || !validEnvVar(binding.envVar.trim())) || new Set(bindingEnvVars).size !== bindingEnvVars.length) return
    setBusy('save'); setActionError(null)
    const value = { name: form.name.trim(), path: form.path.trim(), description: form.description.trim(), enabled: form.enabled, access: form.access, timeoutSeconds: Number(form.timeout), secretBindings: form.bindings.map((binding) => ({ secretId: binding.secretId, envVar: binding.envVar.trim() })) }
    try { const updated = editing ? await settingsApi.updateScript(editing.id, value) : await settingsApi.createScript(value); setData((current) => editing ? (current ?? []).map((item) => item.id === updated.id ? updated : item) : [...(current ?? []), updated]); setEditing(undefined) }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'Script could not be saved.') }
    finally { setBusy(null) }
  }
  const run = async (script: ScriptEntry) => {
    setBusy(script.id); setActionError(null)
    try { await settingsApi.runScript(script.id); setData((current) => (current ?? []).map((item) => item.id === script.id ? { ...item, status: 'running' } : item)) }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'Script could not be started.') }
    finally { setBusy(null) }
  }
  const remove = async () => {
    if (!deleting) return
    setBusy(deleting.id)
    try { await settingsApi.deleteScript(deleting.id); setData((current) => (current ?? []).filter((item) => item.id !== deleting.id)); setDeleting(null) }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'Script could not be deleted.') }
    finally { setBusy(null) }
  }
  if (loading) return <PageLoading label="Loading scripts…" />
  return (
    <div className="settings-content-stack">
      <div className="flex flex-wrap items-end justify-between gap-4"><div><p className="eyebrow">Deterministic work</p><h2 className="settings-title">Scripts</h2><p className="settings-description">Register repeatable local checks that do not need an AI call unless their result is interesting.</p></div><Button variant="primary" onClick={() => openForm(null)}><Plus className="size-4" />Register script</Button></div>
      {(error || actionError || secrets.error) ? <InlineError message={actionError ?? error ?? secrets.error ?? ''} /> : null}
      {!scripts.length ? <EmptyState compact icon={<FileCode2 className="size-5" />} title="No scripts registered" description="Register an executable from the active workspace to use it manually or from a schedule." action={<Button variant="primary" onClick={() => openForm(null)}><Plus className="size-4" />Register script</Button>} /> : <div className="space-y-2">{scripts.map((script) => <Card key={script.id} className="script-row shadow-none"><span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><FileCode2 className="size-5" /></span><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-semibold text-ink">{script.name}</h3><Badge variant={script.status === 'failed' ? 'danger' : script.status === 'running' ? 'warning' : 'default'}>{script.status}</Badge>{!script.enabled ? <Badge>disabled</Badge> : null}{script.secretBindings.length ? <Badge variant="outline"><KeyRound className="size-3" />{script.secretBindings.length} {script.secretBindings.length === 1 ? 'secret' : 'secrets'}</Badge> : null}</div><p className="mt-1 truncate font-mono text-[11px] text-muted">{script.path}</p>{script.lastSummary ? <p className="mt-1.5 line-clamp-1 text-xs text-muted">{script.lastSummary}</p> : null}{script.lastRunAt ? <p className="mt-1 text-[11px] text-muted">Last run {formatRelativeTime(script.lastRunAt)}</p> : null}</div><Button variant="secondary" size="sm" onClick={() => void run(script)} disabled={!script.enabled || busy !== null}>{busy === script.id ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Play className="size-4" />}Run</Button><Button variant="ghost" size="icon" onClick={() => openForm(script)} aria-label={`Edit ${script.name}`}><Pencil className="size-4" /></Button><Button variant="ghost" size="icon" onClick={() => setDeleting(script)} aria-label={`Delete ${script.name}`}><Trash2 className="size-4" /></Button></Card>)}</div>}
      <Dialog open={editing !== undefined} onOpenChange={(open) => { if (!open && !busy) setEditing(undefined) }} title={editing ? 'Edit script' : 'Register script'} description="Scripts run directly on this machine. Saved secrets are exposed only to this process through the environment variables you bind." footer={<><Button variant="ghost" onClick={() => setEditing(undefined)} disabled={busy === 'save'}>Cancel</Button><Button variant="primary" onClick={() => void save()} disabled={!form.name.trim() || !isAbsoluteWorkspacePath(form.path) || form.bindings.some((binding) => !binding.secretId || !validEnvVar(binding.envVar.trim())) || new Set(form.bindings.map((binding) => binding.envVar.trim())).size !== form.bindings.length || busy === 'save'}>{busy === 'save' ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <FileCode2 className="size-4" />}{busy === 'save' ? 'Saving…' : 'Save script'}</Button></>}><div className="space-y-4"><Field label="Name" hint="Required"><Input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} placeholder="site-health" autoFocus /></Field><Field label="Executable path" hint="Required" error={form.path && !isAbsoluteWorkspacePath(form.path) ? 'Use an absolute path beginning with /.' : undefined}><Input value={form.path} onChange={(event) => setForm((current) => ({ ...current, path: event.target.value }))} placeholder="/Users/you/Code/project/scripts/site-health" className="font-mono text-xs" /></Field><Field label="Description"><Textarea value={form.description} onChange={(event) => setForm((current) => ({ ...current, description: event.target.value }))} placeholder="Checks public pages for availability and broken internal links." /></Field><Field label="Timeout in seconds"><Input type="number" min={1} max={86400} value={form.timeout} onChange={(event) => setForm((current) => ({ ...current, timeout: event.target.value }))} /></Field><fieldset><div className="flex items-center justify-between gap-3"><legend className="field-label">Secret environment bindings</legend><Button variant="ghost" size="sm" onClick={() => { setForm((current) => ({ ...current, bindings: [...current.bindings, { key: nextBindingKey, secretId: '', envVar: '' }] })); setNextBindingKey((value) => value + 1) }} disabled={!secrets.data?.length}><Plus className="size-3.5" />Add binding</Button></div><p className="mt-1 text-xs leading-relaxed text-muted">Choose a saved secret and the environment variable name this script expects. Values remain hidden.</p>{!secrets.data?.length ? <div className="mt-3 rounded-lg border border-dashed border-line bg-canvas p-3 text-xs leading-relaxed text-muted">Add a saved value under Settings → Secrets before binding it to a script.</div> : null}<div className="mt-3 space-y-2">{form.bindings.map((binding, index) => <div key={binding.key} className="script-secret-binding"><Select value={binding.secretId || undefined} onValueChange={(secretId) => setForm((current) => ({ ...current, bindings: current.bindings.map((item) => item.key === binding.key ? { ...item, secretId } : item) }))}><SelectTrigger aria-label={`Binding ${index + 1} saved secret`}><SelectValue placeholder="Choose secret" /></SelectTrigger><SelectContent>{(secrets.data ?? []).map((secret) => <SelectItem key={secret.id} value={secret.id}>{secret.name}</SelectItem>)}</SelectContent></Select><Field label="Environment variable" error={binding.envVar && !validEnvVar(binding.envVar) ? 'Use letters, numbers, and underscores; do not begin with a number.' : undefined}><Input value={binding.envVar} onChange={(event) => setForm((current) => ({ ...current, bindings: current.bindings.map((item) => item.key === binding.key ? { ...item, envVar: event.target.value.toUpperCase() } : item) }))} placeholder="PLAUSIBLE_API_KEY" className="font-mono text-xs" /></Field><Button variant="ghost" size="icon" aria-label={`Remove secret binding ${index + 1}`} onClick={() => setForm((current) => ({ ...current, bindings: current.bindings.filter((item) => item.key !== binding.key) }))}><X className="size-4" /></Button></div>)}</div>{new Set(form.bindings.map((binding) => binding.envVar.trim())).size !== form.bindings.length ? <p className="mt-2 text-xs text-danger">Each environment variable may be bound only once.</p> : null}</fieldset><div className="permission-row rounded-lg border"><div><p className="text-sm font-medium text-ink">Enabled</p><p className="mt-0.5 text-xs text-muted">Available to run manually and from schedules.</p></div><Switch checked={form.enabled} onCheckedChange={(enabled) => setForm((current) => ({ ...current, enabled }))} /></div>{actionError ? <InlineError message={actionError} /> : null}</div></Dialog>
      <ConfirmDialog open={deleting !== null} onOpenChange={(open) => { if (!open) setDeleting(null) }} title="Delete script registration?" description="The executable file is not deleted. Nabu only removes its registration and future schedule access." details={deleting?.name} confirmLabel="Delete registration" destructive pending={deleting ? busy === deleting.id : false} onConfirm={() => void remove()} />
    </div>
  )
}
