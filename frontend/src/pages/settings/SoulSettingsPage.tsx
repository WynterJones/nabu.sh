import { Feather, LoaderCircle, Save } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Markdown } from '../../components/Markdown'
import { InlineError, PageLoading } from '../../components/PageState'
import { Button } from '../../components/ui/Button'
import { Card, EmptyState } from '../../components/ui/Card'
import { Textarea } from '../../components/ui/Field'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/Tabs'
import { settingsApi } from '../../features/settings/api'
import { useResource } from '../../hooks/useResource'
import { formatRelativeTime } from '../../lib/utils'

export function SoulSettingsPage() {
  const soul = useResource(settingsApi.getSoul)
  const [body, setBody] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { if (soul.data) setBody(soul.data.body) }, [soul.data])
  if (soul.loading) return <PageLoading label="Loading Nabu’s character…" />

  const save = async () => {
    setSaving(true)
    setError(null)
    try { soul.setData(await settingsApi.updateSoul(body)) }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'SOUL.md could not be saved.') }
    finally { setSaving(false) }
  }

  return (
    <div className="settings-content-stack">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div><p className="eyebrow">Character charter</p><h2 className="settings-title">Soul</h2><p className="settings-description">Shape Nabu’s voice, working style, aspirations, and learned principles. Mission, policy, approvals, and your instructions always take priority.</p></div>
        <Button variant="primary" onClick={() => void save()} disabled={saving || body === (soul.data?.body ?? '')}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Save className="size-4" />}{saving ? 'Saving…' : 'Save soul'}</Button>
      </div>
      {(soul.error || error) ? <InlineError message={error ?? soul.error ?? ''} /> : null}
      <Card className="overflow-hidden shadow-none"><Tabs defaultValue="preview"><div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3"><div><p className="eyebrow">SOUL.md</p>{soul.data?.updatedAt ? <p className="mt-1 text-[11px] text-muted">Updated {formatRelativeTime(soul.data.updatedAt)}</p> : null}</div><TabsList><TabsTrigger value="preview">Preview</TabsTrigger><TabsTrigger value="edit">Edit</TabsTrigger></TabsList></div><TabsContent value="preview" className="m-0 min-h-[31rem] p-5 sm:p-7">{body.trim() ? <Markdown>{body}</Markdown> : <EmptyState compact icon={<Feather className="size-5" />} title="Soul is empty" description="Add a transparent character charter for Nabu." />}</TabsContent><TabsContent value="edit" className="m-0 p-4"><Textarea value={body} onChange={(event) => setBody(event.target.value)} aria-label="Nabu character charter" className="min-h-[28rem] border-0 bg-canvas font-mono text-xs leading-6 focus:outline-none" placeholder="# Soul\n\n## Character\n\n- Calm, candid, and curious" /></TabsContent></Tabs></Card>
      <p className="text-pretty text-xs leading-relaxed text-muted">Nabu may add short, non-sensitive reflections from real collaboration. SOUL.md cannot grant access, weaken policy, store secrets, or override owner direction.</p>
    </div>
  )
}
