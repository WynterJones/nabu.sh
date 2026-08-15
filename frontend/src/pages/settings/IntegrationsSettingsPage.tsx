import { FileCode2, KeyRound, LoaderCircle, MessageSquareText, Pencil, Plus, RefreshCw, ShieldCheck, Trash2 } from 'lucide-react'
import { FormEvent, useState } from 'react'
import { Link } from 'react-router-dom'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card, EmptyState, SectionHeader } from '../../components/ui/Card'
import { Dialog } from '../../components/ui/Dialog'
import { Field, Input, Textarea } from '../../components/ui/Field'
import { secretsApi } from '../../features/secrets/api'
import type { SavedSecret } from '../../features/secrets/types'
import { useResource } from '../../hooks/useResource'
import { formatRelativeTime } from '../../lib/utils'

export function IntegrationsSettingsPage() {
  return (
    <div className="settings-content-stack">
      <div>
        <h2 className="settings-title">Secrets</h2>
        <p className="settings-description">Store API keys and tokens once, then expose them only to the managed scripts that need them.</p>
      </div>
      <div className="credential-safety">
        <ShieldCheck className="size-5 shrink-0 text-accent" />
        <div>
          <p className="text-sm font-medium text-ink">Secret values stay out of Chat</p>
          <p className="mt-1 text-xs leading-relaxed text-muted">Values are write-only after saving. Nabu can bind a saved secret to a script environment variable without seeing or repeating the value.</p>
        </div>
      </div>
      <SecretsManager />
      <Card className="p-5 shadow-none">
        <h3 className="text-sm font-semibold text-ink">Secrets become environment variables</h3>
        <p className="mt-2 max-w-2xl text-xs leading-relaxed text-muted">Ask Nabu to build a script for any API. It will create the local script, bind only the saved values it needs to names such as <code>API_TOKEN</code>, and return bounded redacted results.</p>
        <div className="mt-4 flex flex-wrap gap-2">
          <Button asChild variant="secondary" size="sm"><Link to="/chat"><MessageSquareText className="size-4" />Ask Nabu to build a script</Link></Button>
          <Button asChild variant="secondary" size="sm"><Link to="/settings/scripts"><FileCode2 className="size-4" />Manage scripts</Link></Button>
        </div>
      </Card>
    </div>
  )
}

function SecretsManager() {
  const { data, setData, loading, error, refresh } = useResource(secretsApi.list)
  const secrets = data ?? []
  const [editing, setEditing] = useState<SavedSecret | null | undefined>(undefined)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [value, setValue] = useState('')
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<SavedSecret | null>(null)

  const openSecret = (secret: SavedSecret | null) => {
    setEditing(secret)
    setName(secret?.name ?? '')
    setDescription(secret?.description ?? '')
    setValue('')
    setActionError(null)
  }
  const closeSecret = () => {
    if (saving) return
    setEditing(undefined)
    setName('')
    setDescription('')
    setValue('')
    setActionError(null)
  }
  const saveSecret = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!name.trim() || (!editing && !value) || saving) return
    setSaving(true)
    setActionError(null)
    try {
      const saved = await secretsApi.save({ id: editing?.id, name, description, value: value || undefined })
      setData((current) => editing ? (current ?? []).map((item) => item.id === saved.id ? saved : item) : [...(current ?? []), saved])
      setValue('')
      setEditing(undefined)
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'The secret could not be saved.')
    } finally {
      setSaving(false)
    }
  }
  const removeSecret = async () => {
    if (!deleting || saving) return
    setSaving(true)
    setActionError(null)
    try {
      await secretsApi.delete(deleting.id)
      setData((current) => (current ?? []).filter((item) => item.id !== deleting.id))
      setDeleting(null)
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'The secret could not be deleted.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="space-y-3">
      <SectionHeader title="Saved secrets" />
      <p className="-mt-1 text-xs leading-relaxed text-muted">Each saved value can be bound to one or more script environment variables. Names and bindings are visible; values are not.</p>
      <div className="flex items-center justify-end gap-1 pt-1"><Button variant="ghost" size="icon" aria-label="Refresh saved secrets" onClick={() => void refresh()} disabled={loading}><RefreshCw className={loading ? 'size-4 animate-spin motion-reduce:animate-none' : 'size-4'} /></Button><Button variant="primary" size="sm" onClick={() => openSecret(null)}><Plus className="size-4" />Add secret</Button></div>
      {error || actionError ? <InlineError message={actionError ?? error ?? ''} /> : null}
      {loading && !data ? <PageLoading label="Loading saved secrets…" /> : !secrets.length ? (
        <EmptyState compact icon={<KeyRound className="size-5" />} title="No saved secrets" description="Add an API key or token, then ask Nabu to build a script that uses it through an environment variable." action={<Button variant="primary" onClick={() => openSecret(null)}><Plus className="size-4" />Add secret</Button>} />
      ) : (
        <Card className="overflow-hidden shadow-none">{secrets.map((secret) => <div key={secret.id} className="secret-row"><span className="integration-icon"><KeyRound className="size-4" /></span><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-semibold text-ink">{secret.name}</h3><Badge variant="outline">Value hidden</Badge>{secret.bindingCount ? <Badge>{secret.bindingCount} {secret.bindingCount === 1 ? 'binding' : 'bindings'}</Badge> : null}</div>{secret.description ? <p className="mt-1 line-clamp-2 text-xs leading-relaxed text-muted">{secret.description}</p> : null}{secret.updatedAt ? <p className="mt-1 text-[11px] text-muted">Updated {formatRelativeTime(secret.updatedAt)}</p> : null}</div><Button variant="ghost" size="icon" aria-label={`Edit ${secret.name}`} onClick={() => openSecret(secret)}><Pencil className="size-4" /></Button><Button variant="ghost" size="icon" aria-label={`Delete ${secret.name}`} onClick={() => setDeleting(secret)}><Trash2 className="size-4" /></Button></div>)}</Card>
      )}
      <Dialog open={editing !== undefined} onOpenChange={(open) => { if (!open) closeSecret() }} title={editing ? `Edit ${editing.name}` : 'Add saved secret'} description="The value is write-only. After saving, it cannot be viewed or copied back out of Nabu." footer={<><Button variant="ghost" onClick={closeSecret} disabled={saving}>Cancel</Button><Button variant="primary" type="submit" form="saved-secret-form" disabled={saving || !name.trim() || (!editing && !value)}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <KeyRound className="size-4" />}{saving ? 'Saving…' : 'Save secret'}</Button></>}>
        <form id="saved-secret-form" onSubmit={(event) => void saveSecret(event)} className="space-y-4"><Field label="Name" hint="Required"><Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Analytics API key" autoFocus /></Field><Field label="Description" hint="Optional"><Textarea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Where this value is used and who owns it." className="min-h-20" /></Field><Field label={editing ? 'Replacement value' : 'Secret value'} hint={editing ? 'Leave blank to keep the saved value' : 'Required'}><Input type="password" value={value} onChange={(event) => setValue(event.target.value)} placeholder={editing ? 'Enter only to replace' : 'Enter secret value'} autoComplete="new-password" className="text-base" required={!editing} /></Field>{actionError ? <InlineError message={actionError} /> : null}</form>
      </Dialog>
      <ConfirmDialog open={deleting !== null} onOpenChange={(open) => { if (!open && !saving) setDeleting(null) }} title="Delete saved secret?" description="This permanently removes the stored value. Scripts using it may stop working until their bindings are updated." details={deleting?.name} confirmLabel="Delete secret" destructive pending={saving} onConfirm={() => void removeSecret()} />
    </section>
  )
}
