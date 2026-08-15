import { BookOpenText, Check, LoaderCircle, Save, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Markdown } from '../../components/Markdown'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card, EmptyState, SectionHeader } from '../../components/ui/Card'
import { Dialog } from '../../components/ui/Dialog'
import { Field, Textarea } from '../../components/ui/Field'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/Tabs'
import { settingsApi } from '../../features/settings/api'
import type { MemoryUpdate } from '../../features/settings/types'
import { useResource } from '../../hooks/useResource'
import { formatRelativeTime } from '../../lib/utils'

export function MemorySettingsPage() {
  const memory = useResource(settingsApi.getMemory)
  const updates = useResource(settingsApi.listMemoryUpdates)
  const [body, setBody] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [rejecting, setRejecting] = useState<MemoryUpdate | null>(null)
  const [note, setNote] = useState('')
  useEffect(() => { if (memory.data) setBody(memory.data.body) }, [memory.data])
  if (memory.loading) return <PageLoading label="Loading durable memory…" />
  const proposedUpdates = (updates.data ?? []).filter((update) => update.status === 'proposed')
  const save = async () => {
    setSaving(true); setError(null)
    try { const updated = await settingsApi.updateMemory(body); memory.setData(updated) }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Memory could not be saved.') }
    finally { setSaving(false) }
  }
  const resolve = async (update: MemoryUpdate, decision: 'applied' | 'rejected', rejectionNote?: string) => {
    setSaving(true); setError(null)
    try { await settingsApi.resolveMemoryUpdate(update.id, decision, rejectionNote); updates.setData((current) => (current ?? []).filter((item) => item.id !== update.id)); if (decision === 'applied') await memory.refresh(); setRejecting(null); setNote('') }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Memory proposal could not be resolved.') }
    finally { setSaving(false) }
  }
  return (
    <div className="settings-content-stack">
      <div className="flex flex-wrap items-end justify-between gap-4"><div><p className="eyebrow">Continuity</p><h2 className="settings-title">Memory</h2><p className="settings-description">Keep stable decisions, terminology, constraints, and lessons concise enough to guide future work.</p></div><Button variant="primary" onClick={() => void save()} disabled={saving || body === (memory.data?.body ?? '')}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Save className="size-4" />}{saving ? 'Saving…' : 'Save memory'}</Button></div>
      {(memory.error || updates.error || error) ? <InlineError message={error ?? memory.error ?? updates.error ?? ''} /> : null}
      {proposedUpdates.length ? <Card className="border-warning/25 p-5 shadow-none"><SectionHeader eyebrow="Proposed updates" title={`${proposedUpdates.length} change${proposedUpdates.length === 1 ? '' : 's'} waiting for review`} /><div className="mt-4 space-y-3">{proposedUpdates.map((update) => <div key={update.id} className="rounded-lg border border-line bg-canvas p-4"><div className="flex flex-wrap items-start justify-between gap-2"><div><h3 className="text-sm font-medium text-ink">{update.summary}</h3>{update.reason ? <p className="mt-1 text-xs leading-relaxed text-muted">{update.reason}</p> : null}</div><Badge variant="warning">{update.status}</Badge></div><div className="mt-3 max-h-48 overflow-y-auto rounded-lg border border-line bg-panel p-3"><Markdown>{update.content}</Markdown></div><div className="mt-3 flex justify-end gap-2"><Button variant="danger" size="sm" onClick={() => setRejecting(update)}><X className="size-3.5" />Reject</Button><Button variant="primary" size="sm" onClick={() => void resolve(update, 'applied')} disabled={saving}><Check className="size-3.5" />Apply</Button></div></div>)}</div></Card> : null}
      <Card className="overflow-hidden shadow-none"><Tabs defaultValue="preview"><div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3"><div><p className="eyebrow">MEMORY.md</p>{memory.data?.updatedAt ? <p className="mt-1 text-[11px] text-muted">Updated {formatRelativeTime(memory.data.updatedAt)}</p> : null}</div><TabsList><TabsTrigger value="preview">Preview</TabsTrigger><TabsTrigger value="edit">Edit</TabsTrigger></TabsList></div><TabsContent value="preview" className="m-0 min-h-[31rem] p-5 sm:p-7">{body.trim() ? <Markdown>{body}</Markdown> : <EmptyState compact icon={<BookOpenText className="size-5" />} title="Memory is empty" description="Add concise, durable context in the editor." />}</TabsContent><TabsContent value="edit" className="m-0 p-4"><Textarea value={body} onChange={(event) => setBody(event.target.value)} aria-label="Durable memory" className="min-h-[28rem] border-0 bg-canvas font-mono text-xs leading-6 focus:outline-none" placeholder="# Durable memory\n\n- Important decisions\n- Stable preferences\n- Recurring constraints" /></TabsContent></Tabs></Card>
      {memory.data?.dailyNotes.length ? <Card className="p-5 shadow-none"><SectionHeader eyebrow="Daily memory" title="Recent operational notes" /><div className="mt-4 divide-y divide-line">{memory.data.dailyNotes.slice(0, 7).map((noteItem) => <div key={noteItem.date} className="grid grid-cols-[100px_minmax(0,1fr)] gap-4 py-3 text-xs"><time className="font-mono text-muted">{noteItem.date}</time><p className="text-pretty leading-relaxed text-ink">{noteItem.summary}</p></div>)}</div></Card> : null}
      <Dialog open={rejecting !== null} onOpenChange={(open) => { if (!open && !saving) { setRejecting(null); setNote('') } }} title="Reject memory update?" description="The proposed context will not be added. Optionally explain what should be corrected." footer={<><Button variant="ghost" onClick={() => setRejecting(null)} disabled={saving}>Cancel</Button><Button variant="danger" onClick={() => rejecting && void resolve(rejecting, 'rejected', note.trim() || undefined)} disabled={saving}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <X className="size-4" />}Reject update</Button></>}><Field label="Rejection note" hint="Optional"><Textarea value={note} onChange={(event) => setNote(event.target.value)} placeholder="This is too broad; preserve only the decision about…" autoFocus /></Field></Dialog>
    </div>
  )
}
