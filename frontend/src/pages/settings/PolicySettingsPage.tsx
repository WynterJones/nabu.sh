import { LoaderCircle, Save, ShieldCheck } from 'lucide-react'
import { useEffect, useState } from 'react'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/Select'
import { settingsApi } from '../../features/settings/api'
import type { Policy, PolicyDecision } from '../../features/settings/types'
import { useResource } from '../../hooks/useResource'

const categories: Array<{ key: keyof Pick<Policy, 'read' | 'work' | 'publish' | 'dangerous'>; title: string; description: string; examples: string[] }> = [
  { key: 'read', title: 'Read', description: 'Research and inspect without changing external state.', examples: ['Files and repositories', 'Websites and metrics', 'Public web research'] },
  { key: 'work', title: 'Work', description: 'Prepare verified work inside approved local workspaces.', examples: ['Edit files and run tests', 'Create branches and commits', 'Generate reports and drafts'] },
  { key: 'publish', title: 'Publish', description: 'Make changes visible outside this machine.', examples: ['Merge and deploy', 'Publish public content', 'Send external messages'] },
  { key: 'dangerous', title: 'Dangerous', description: 'High-impact, destructive, security, or financial actions.', examples: ['Delete production data', 'Change authentication', 'Spend money or change billing'] },
]

export function PolicySettingsPage() {
  const { data, loading, error } = useResource(settingsApi.getPolicy)
  const [policy, setPolicy] = useState<Policy | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  useEffect(() => { if (data) setPolicy(data) }, [data])
  if (loading || !policy) return <PageLoading label="Loading policy…" />

  const save = async () => {
    setSaving(true); setSaveError(null); setSaved(false)
    try { setPolicy(await settingsApi.updatePolicy(policy)); setSaved(true); window.setTimeout(() => setSaved(false), 1800) }
    catch (caught) { setSaveError(caught instanceof Error ? caught.message : 'Policy could not be saved.') }
    finally { setSaving(false) }
  }

  return (
    <div className="settings-content-stack">
      <div className="flex flex-wrap items-end justify-between gap-4"><div><p className="eyebrow">Autonomy</p><h2 className="settings-title">Policy</h2><p className="settings-description">Four understandable rules govern every task packet and approval boundary.</p></div><Button variant="primary" onClick={() => void save()} disabled={saving}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Save className="size-4" />}{saving ? 'Saving…' : saved ? 'Saved' : 'Save policy'}</Button></div>
      {(error || saveError) ? <InlineError message={saveError ?? error ?? ''} /> : null}
      <div className="space-y-3">{categories.map((category) => <Card key={category.key} className="p-5 shadow-none"><div className="flex items-start gap-3"><span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><ShieldCheck className="size-5" /></span><div className="min-w-0 flex-1"><div className="flex flex-wrap items-start justify-between gap-3"><div><h3 className="text-sm font-semibold text-ink">{category.title}</h3><p className="mt-1 text-pretty text-xs leading-relaxed text-muted">{category.description}</p></div><label className="field w-32 shrink-0"><span className="sr-only">{category.title} policy</span><Select value={policy[category.key]} onValueChange={(value: PolicyDecision) => setPolicy((current) => current ? { ...current, [category.key]: value } : current)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="allow">Allow</SelectItem><SelectItem value="ask">Ask first</SelectItem><SelectItem value="deny">Deny</SelectItem></SelectContent></Select></label></div><div className="mt-3 flex flex-wrap gap-1.5">{category.examples.map((example) => <Badge key={example} variant="outline">{example}</Badge>)}</div></div></div></Card>)}</div>
      <div className="rounded-lg border border-warning/25 bg-warning/5 p-4 text-xs leading-relaxed text-muted"><span className="font-semibold text-warning">Safety invariant:</span> dangerous actions are always held at an approval boundary, even if a worker suggests otherwise.</div>
    </div>
  )
}
