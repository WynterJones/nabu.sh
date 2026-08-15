import { ExternalLink, FileTerminal, Globe2, LoaderCircle, Pencil, Play, Plus, RefreshCw, RotateCw, Square, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import { appsApi } from '../features/apps/api'
import type { LocalApp, LocalAppInput } from '../features/apps/types'
import { useResource } from '../hooks/useResource'
import { cn } from '../lib/utils'
import { useNabu } from '../state/NabuContext'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { InlineError, PageLoading } from '../components/PageState'
import { Button } from '../components/ui/Button'
import { EmptyState } from '../components/ui/Card'
import { Dialog } from '../components/ui/Dialog'
import { Field, Input, Textarea } from '../components/ui/Field'
import { Switch } from '../components/ui/Switch'

const blankInput = (): LocalAppInput => ({
  name: '',
  description: '',
  directory: 'repos/',
  command: [],
  port: 4173,
  healthPath: '/',
  autoStart: false,
})

export function AppsPage() {
  const { activeScope } = useNabu()
  const { data, error, loading, refresh } = useResource(appsApi.list, activeScope?.id ?? '')
  const apps = useMemo(() => data ?? [], [data])
  const [editor, setEditor] = useState<LocalApp | 'new' | null>(null)
  const [logsApp, setLogsApp] = useState<LocalApp | null>(null)
  const [deleteApp, setDeleteApp] = useState<LocalApp | null>(null)
  const [action, setAction] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  useEffect(() => {
    if (!apps.some((app) => app.status === 'running')) return
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void refresh()
    }, 5000)
    return () => window.clearInterval(timer)
  }, [apps, refresh])

  useEffect(() => {
    const onChanged = (event: Event) => {
      const type = (event as CustomEvent<{ type?: string }>).detail?.type ?? ''
      if (type.startsWith('app.')) void refresh()
    }
    window.addEventListener('nabu:data-changed', onChanged)
    return () => window.removeEventListener('nabu:data-changed', onChanged)
  }, [refresh])

  const runAction = useCallback(async (app: LocalApp, kind: 'start' | 'stop' | 'restart') => {
    setAction(`${kind}:${app.id}`)
    setActionError(null)
    try {
      await appsApi[kind](app.id)
      await refresh()
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : `Nabu could not ${kind} this app.`)
    } finally {
      setAction(null)
    }
  }, [refresh])

  const remove = async () => {
    if (!deleteApp) return
    setAction(`delete:${deleteApp.id}`)
    setActionError(null)
    try {
      await appsApi.remove(deleteApp.id)
      setDeleteApp(null)
      await refresh()
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'The app registration could not be deleted.')
    } finally {
      setAction(null)
    }
  }

  return (
    <div className="page-stack max-w-6xl">
      <div className="page-heading">
        <div className="min-w-0">
          <h1 className="page-title">Apps</h1>
          <p className="page-description">Run and open the local sites Nabu builds inside this workspace.</p>
        </div>
        <div className="flex shrink-0 gap-2">
          <Button variant="secondary" size="icon" aria-label="Refresh local apps" onClick={() => void refresh()} disabled={loading}>
            <RefreshCw className={cn('size-4', loading && 'animate-spin motion-reduce:animate-none')} />
          </Button>
          <Button variant="primary" onClick={() => setEditor('new')}><Plus className="size-4" />Register app</Button>
        </div>
      </div>

      {actionError ? <InlineError message={actionError} /> : null}
      {loading && !data ? <PageLoading label="Loading local apps…" /> : error ? <InlineError message={error} /> : !apps.length ? (
        <EmptyState
          icon={<Globe2 className="size-5" />}
          title="No local apps yet"
          description="When Nabu builds a site in repos, it can register the folder and start command here automatically. You can also add one yourself."
          action={<Button variant="primary" onClick={() => setEditor('new')}><Plus className="size-4" />Register an app</Button>}
        />
      ) : (
        <div className="apps-list">
          {apps.map((app) => (
            <LocalAppCard key={app.id} app={app} action={action} onAction={(kind) => void runAction(app, kind)} onEdit={() => setEditor(app)} onLogs={() => setLogsApp(app)} onDelete={() => setDeleteApp(app)} />
          ))}
        </div>
      )}

      <LocalAppDialog open={editor !== null} app={editor} onOpenChange={(open) => { if (!open) setEditor(null) }} onSaved={async () => { setEditor(null); await refresh() }} />
      <LocalAppLogsDialog app={logsApp} onOpenChange={(open) => { if (!open) setLogsApp(null) }} />
      <ConfirmDialog
        open={deleteApp !== null}
        onOpenChange={(open) => { if (!open) setDeleteApp(null) }}
        title="Delete app registration?"
        description="This removes the launch definition and logs from Nabu. The source folder and its files stay in the workspace."
        details={deleteApp ? <span><strong className="text-ink">{deleteApp.name}</strong><br /><span className="font-mono text-xs">{deleteApp.directory}</span></span> : undefined}
        confirmLabel="Delete registration"
        destructive
        pending={Boolean(deleteApp && action === `delete:${deleteApp.id}`)}
        onConfirm={() => void remove()}
      />
    </div>
  )
}

function LocalAppCard({ app, action, onAction, onEdit, onLogs, onDelete }: { app: LocalApp; action: string | null; onAction: (kind: 'start' | 'stop' | 'restart') => void; onEdit: () => void; onLogs: () => void; onDelete: () => void }) {
  const busy = action?.endsWith(`:${app.id}`) ?? false
  const running = app.status === 'running'
  const statusLabel = running ? app.healthy ? 'Running' : 'Starting' : app.status === 'failed' ? 'Needs attention' : 'Stopped'
  return (
    <article className="local-app-card">
      <div className="local-app-icon" aria-hidden="true"><Globe2 className="size-5" /></div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <h2 className="truncate text-base font-semibold text-ink">{app.name}</h2>
          <span className="inline-flex items-center gap-1.5 text-xs font-medium text-muted">
            <span className={cn('size-2 rounded-full', running ? 'bg-accent shadow-[0_0_10px_rgba(var(--accent),.45)]' : app.status === 'failed' ? 'bg-warning' : 'bg-muted')} />
            {statusLabel}
          </span>
          {app.autoStart ? <span className="text-[11px] font-medium text-muted">Auto-start</span> : null}
        </div>
        {app.description ? <p className="mt-1 line-clamp-2 text-sm leading-relaxed text-muted">{app.description}</p> : null}
        <div className="mt-3 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-muted">
          <span className="min-w-0 truncate font-mono" title={app.directory}>{app.directory}</span>
          <span className="font-mono">:{app.port}</span>
          {running ? <a href={app.url} target="_blank" rel="noreferrer" className="link truncate" title={app.url}>{app.url}</a> : null}
        </div>
        {app.error ? <p className="mt-3 rounded-lg border border-warning/25 bg-warning/5 px-3 py-2 text-xs leading-relaxed text-warning">{app.error}</p> : null}
      </div>
      <div className="local-app-actions">
        {running ? (
          <>
            <Button asChild variant="primary" size="sm"><a href={app.url} target="_blank" rel="noreferrer"><ExternalLink className="size-3.5" />Open</a></Button>
            <Button variant="secondary" size="sm" onClick={() => onAction('stop')} disabled={busy}>{busy ? <LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" /> : <Square className="size-3.5" />}Stop</Button>
            <Button variant="secondary" size="icon" aria-label={`Restart ${app.name}`} onClick={() => onAction('restart')} disabled={busy}><RotateCw className="size-4" /></Button>
          </>
        ) : (
          <Button variant="primary" size="sm" onClick={() => onAction('start')} disabled={busy}>{busy ? <LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" /> : <Play className="size-3.5" />}{app.status === 'failed' ? 'Try again' : 'Start'}</Button>
        )}
        <Button variant="secondary" size="icon" aria-label={`View logs for ${app.name}`} onClick={onLogs}><FileTerminal className="size-4" /></Button>
        <Button variant="ghost" size="icon" aria-label={`Edit ${app.name}`} onClick={onEdit} disabled={running}><Pencil className="size-4" /></Button>
        <Button variant="ghost" size="icon" aria-label={`Delete ${app.name}`} onClick={onDelete}><Trash2 className="size-4" /></Button>
      </div>
    </article>
  )
}

function LocalAppDialog({ open, app, onOpenChange, onSaved }: { open: boolean; app: LocalApp | 'new' | null; onOpenChange: (open: boolean) => void; onSaved: () => Promise<void> }) {
  const seed = useMemo<LocalAppInput>(() => app && app !== 'new' ? { name: app.name, description: app.description, directory: app.directory, command: app.command, port: app.port, healthPath: app.healthPath, autoStart: app.autoStart } : blankInput(), [app])
  const [form, setForm] = useState(seed)
  const [commandText, setCommandText] = useState(formatCommand(seed.command))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => { setForm(seed); setCommandText(formatCommand(seed.command)); setError(null) }, [seed, open])

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError(null)
    try {
      const command = splitCommandLine(commandText)
      if (!command.length) throw new Error('Enter a start command.')
      const input = { ...form, command }
      if (app && app !== 'new') await appsApi.update(app.id, input)
      else await appsApi.create(input)
      await onSaved()
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The app could not be saved.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={app && app !== 'new' ? `Edit ${app.name}` : 'Register local app'}
      description="Connect a site or app folder to a direct start command. Nabu runs it on localhost without evaluating a shell expression."
      className="max-w-2xl"
      footer={<><Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button><Button variant="primary" type="submit" form="local-app-form" disabled={saving}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Globe2 className="size-4" />}{saving ? 'Saving…' : app && app !== 'new' ? 'Save app' : 'Register app'}</Button></>}
    >
      <form id="local-app-form" onSubmit={(event) => void save(event)} className="space-y-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="App name"><Input value={form.name} onChange={(event) => setForm((value) => ({ ...value, name: event.target.value }))} placeholder="Customer portal" required autoFocus /></Field>
          <Field label="Folder" hint="Inside this workspace"><Input value={form.directory} onChange={(event) => setForm((value) => ({ ...value, directory: event.target.value }))} placeholder="repos/customer-portal" required /></Field>
        </div>
        <Field label="Description" hint="Optional"><Textarea value={form.description ?? ''} onChange={(event) => setForm((value) => ({ ...value, description: event.target.value }))} placeholder="What this app is for." autoSizeMin={72} autoSizeMax={140} /></Field>
        <Field label="Start command" hint="Executable and arguments"><Input value={commandText} onChange={(event) => setCommandText(event.target.value)} placeholder="npm run dev -- --host 127.0.0.1 --port 4173" required /><span className="text-xs leading-relaxed text-muted">Quoted arguments are supported. Nabu executes the resulting arguments directly—pipes and redirects are not interpreted.</span></Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Local port"><Input type="number" min={1024} max={65535} value={form.port} onChange={(event) => setForm((value) => ({ ...value, port: Number(event.target.value) }))} required /></Field>
          <Field label="Health path"><Input value={form.healthPath} onChange={(event) => setForm((value) => ({ ...value, healthPath: event.target.value }))} placeholder="/" required /></Field>
        </div>
        <label className="flex items-center justify-between gap-4 rounded-xl border border-line bg-canvas px-4 py-3">
          <span><span className="block text-sm font-semibold text-ink">Start with Nabu</span><span className="mt-0.5 block text-xs leading-relaxed text-muted">Launch this app when the local operator starts.</span></span>
          <Switch checked={form.autoStart} onCheckedChange={(checked) => setForm((value) => ({ ...value, autoStart: checked }))} aria-label="Start this app with Nabu" />
        </label>
        {error ? <InlineError message={error} /> : null}
      </form>
    </Dialog>
  )
}

function LocalAppLogsDialog({ app, onOpenChange }: { app: LocalApp | null; onOpenChange: (open: boolean) => void }) {
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const refresh = useCallback(async () => {
    if (!app) return
    setLoading(true)
    setError(null)
    try { setContent((await appsApi.logs(app.id)).content) } catch (caught) { setError(caught instanceof Error ? caught.message : 'Logs could not be loaded.') } finally { setLoading(false) }
  }, [app])
  useEffect(() => { if (app) void refresh() }, [app, refresh])
  return (
    <Dialog open={app !== null} onOpenChange={onOpenChange} title={app ? `${app.name} logs` : 'App logs'} description="Bounded output from the current or most recent local run." className="max-w-4xl" bodyClassName="p-0 sm:p-0" footer={<Button variant="secondary" onClick={() => void refresh()} disabled={loading}><RefreshCw className={cn('size-4', loading && 'animate-spin motion-reduce:animate-none')} />Refresh</Button>}>
      {error ? <div className="p-5"><InlineError message={error} /></div> : <pre className="local-app-logs" aria-live="polite">{content || (loading ? 'Loading logs…' : 'No output yet.')}</pre>}
    </Dialog>
  )
}

export function splitCommandLine(value: string): string[] {
  const parts: string[] = []
  let current = ''
  let quote = ''
  let escaped = false
  for (const character of value.trim()) {
    if (escaped) { current += character; escaped = false; continue }
    if (character === '\\') { escaped = true; continue }
    if (quote) {
      if (character === quote) quote = ''
      else current += character
      continue
    }
    if (character === '"' || character === "'") { quote = character; continue }
    if (/\s/.test(character)) {
      if (current) { parts.push(current); current = '' }
      continue
    }
    current += character
  }
  if (escaped) current += '\\'
  if (quote) throw new Error('The start command contains an unclosed quote.')
  if (current) parts.push(current)
  return parts
}

function formatCommand(command: string[]): string {
  return command.map((part) => /[\s"'\\]/.test(part) ? JSON.stringify(part) : part).join(' ')
}
