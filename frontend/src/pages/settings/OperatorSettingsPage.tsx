import { AlertTriangle, Bot, CheckCircle2, Circle, FolderGit2, Gauge, HardDrive, LoaderCircle, Pause, Play, RefreshCw, RotateCw, Server } from 'lucide-react'
import { useState } from 'react'
import { MissionDialog } from '../../components/MissionDialog'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card, SectionHeader } from '../../components/ui/Card'
import { api } from '../../lib/api'
import { settingsApi } from '../../features/settings/api'
import { useResource } from '../../hooks/useResource'
import { useNabu } from '../../state/NabuContext'
import { formatRelativeTime } from '../../lib/utils'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/Select'
import type { CodexModelCatalog, CodexReasoningEffort, OperatorAISettings } from '../../features/settings/types'

export function OperatorSettingsPage() {
  const { status, mission, workspaces, refresh } = useNabu()
  const { data: health, loading, error, refresh: refreshHealth } = useResource(settingsApi.getHealth)
  const [missionOpen, setMissionOpen] = useState(false)
  const [action, setAction] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [restartOpen, setRestartOpen] = useState(false)
  const { data: savedAISettings, loading: aiLoading, error: aiError, refresh: refreshAISettings } = useResource(settingsApi.getOperatorSettings)
  const { data: modelCatalog, loading: modelsLoading, error: modelsError } = useResource(settingsApi.getOperatorModels)
  const [aiDraft, setAIDraft] = useState<OperatorAISettings | null>(null)

  const runAction = async (name: string, callback: () => Promise<unknown>) => {
    setAction(name)
    setActionError(null)
    try {
      await callback()
      await Promise.all([refresh(), refreshHealth()])
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : `${name} could not be completed.`)
    } finally {
      setAction(null)
    }
  }

  if (loading && !health) return <PageLoading label="Loading service health…" />
  const codexTrouble = health?.codexState === 'unavailable' || health?.codexState === 'rate_limited' || status?.codexAvailable === false
  return (
    <div className="settings-content-stack">
      <div><p className="eyebrow">Operator</p><h2 className="settings-title">Mission and service</h2><p className="settings-description">Current local operator identity, workspaces, and recovery controls.</p></div>
      {(error || aiError || actionError) ? <InlineError message={actionError ?? error ?? aiError ?? ''} /> : null}
      {codexTrouble ? <div className="reliability-alert"><AlertTriangle className="size-5 shrink-0 text-warning" /><div className="min-w-0 flex-1"><p className="text-sm font-semibold text-ink">{health?.codexState === 'rate_limited' ? 'Codex is rate limited' : 'Codex is unavailable'}</p><p className="mt-1 text-xs leading-relaxed text-muted">{health?.codexMessage ?? 'The queue is intact. Nabu will retry conservatively while scheduled local scripts continue.'}</p>{health?.retryAt ? <p className="mt-2 text-xs text-warning">Retry {formatRelativeTime(health.retryAt)}</p> : null}</div></div> : null}
      <Card className="p-5 shadow-none"><SectionHeader eyebrow="Active mission" title={mission?.statement || 'No active mission'} action={<Button variant="secondary" size="sm" onClick={() => setMissionOpen(true)}>Edit mission</Button>} />{mission?.context ? <p className="mt-3 line-clamp-3 text-sm leading-relaxed text-muted">{mission.context}</p> : null}<div className="mt-5 flex flex-wrap gap-2"><Button variant="secondary" onClick={() => void runAction(status?.paused ? 'Resume' : 'Pause', () => api.setPaused(!status?.paused))} disabled={action !== null}>{action === 'Pause' || action === 'Resume' ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : status?.paused ? <Play className="size-4" /> : <Pause className="size-4" />}{status?.paused ? 'Resume operator' : 'Pause operator'}</Button></div></Card>
      <AISettingsCard value={aiDraft ?? savedAISettings ?? { codexModel: '', codexReasoningEffort: '', maxParallelTasks: 1 }} onChange={setAIDraft} catalog={modelCatalog} catalogError={modelsError} loading={aiLoading || modelsLoading} saving={action === 'Save AI settings'} onSave={() => void runAction('Save AI settings', async () => { await settingsApi.updateOperatorSettings(aiDraft ?? savedAISettings ?? { codexModel: '', codexReasoningEffort: '', maxParallelTasks: 1 }); setAIDraft(null); await refreshAISettings() })} />
      <Card className="p-5 shadow-none"><SectionHeader eyebrow="Approved access" title={`${workspaces.length} workspace${workspaces.length === 1 ? '' : 's'}`} />{workspaces.length ? <ul className="mt-4 overflow-hidden rounded-lg border border-line">{workspaces.map((workspace) => <li key={workspace.id ?? workspace.path} className="flex min-w-0 items-center gap-3 border-b border-line bg-canvas px-3 py-3 last:border-0"><FolderGit2 className="size-4 shrink-0 text-muted" /><code className="min-w-0 flex-1 truncate text-xs text-ink">{workspace.path}</code><Badge variant="success">Approved</Badge></li>)}</ul> : <p className="mt-4 text-sm text-muted">No approved workspaces.</p>}</Card>
      <Card className="p-5 shadow-none"><SectionHeader eyebrow="Local service" title="Health and recovery" action={<Button variant="ghost" size="icon" aria-label="Refresh health" onClick={() => void refreshHealth()}><RefreshCw className="size-4" /></Button>} /><div className="health-grid"><HealthItem icon={<Bot className="size-4" />} label="Codex" ok={!codexTrouble} value={health?.codexState ?? (status?.codexAvailable ? 'available' : 'unavailable')} /><HealthItem icon={<Server className="size-4" />} label="Service" ok={health?.serviceHealthy} value={health?.serviceHealthy === false ? 'needs attention' : health?.serviceHealthy ? 'healthy' : health?.status ?? 'not reported'} /><HealthItem icon={<HardDrive className="size-4" />} label="Free disk" ok={health?.diskFreeBytes === undefined ? undefined : health.diskFreeBytes >= 2 * 1024 ** 3} value={formatBytes(health?.diskFreeBytes)} /><HealthItem icon={<RotateCw className="size-4" />} label="Latest backup" ok={health?.backupAt ? true : undefined} value={health?.backupAt ? formatRelativeTime(health.backupAt) : 'not reported'} /></div><div className="mt-5 border-t border-line pt-4"><Button variant="danger" onClick={() => setRestartOpen(true)} disabled={action !== null}><RotateCw className="size-4" />Restart local service</Button><p className="mt-2 text-xs text-muted">Running work is recovered from durable state after restart.</p></div></Card>
      <MissionDialog open={missionOpen} onOpenChange={setMissionOpen} />
      <ConfirmDialog open={restartOpen} onOpenChange={setRestartOpen} title="Restart the local service?" description="The UI may disconnect briefly. Durable mission, queue, run, and schedule state will be preserved." confirmLabel="Restart service" destructive pending={action === 'Restart'} onConfirm={() => void runAction('Restart', settingsApi.restartService).then(() => setRestartOpen(false))} />
    </div>
  )
}

function AISettingsCard({ value, onChange, onSave, saving, loading, catalog, catalogError }: { value: OperatorAISettings; onChange: (value: OperatorAISettings) => void; onSave: () => void; saving: boolean; loading: boolean; catalog: CodexModelCatalog | null; catalogError: string | null }) {
  const selectedModel = catalog?.models.find((model) => model.id === value.codexModel)
  const unavailableCurrent = Boolean(value.codexModel && !selectedModel)
  const baseEfforts: CodexReasoningEffort[] = selectedModel?.supportedReasoningEfforts.length ? selectedModel.supportedReasoningEfforts : ['none', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
  const efforts = ['' as CodexReasoningEffort, ...baseEfforts]
  if (value.codexReasoningEffort && !efforts.includes(value.codexReasoningEffort)) efforts.splice(1, 0, value.codexReasoningEffort)
  return (
    <Card className="p-5 shadow-none">
      <SectionHeader eyebrow="Codex" title="Model and autonomy" />
      <p className="mt-2 text-xs leading-relaxed text-muted">Applies to tasks, orientation, Chat, approvals, and task drafting.</p>
      <div className="mt-5 form-grid">
        <label className="field">
          <span className="field-label">Model</span>
          <Select value={value.codexModel || '__default__'} onValueChange={(next) => onChange({ ...value, codexModel: next === '__default__' ? '' : next })} disabled={loading}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="__default__">Codex default</SelectItem>
              {unavailableCurrent ? <SelectItem value={value.codexModel}>Current · unavailable · {value.codexModel}</SelectItem> : null}
              {catalog?.models.map((model) => <SelectItem key={model.id} value={model.id}>{model.displayName}</SelectItem>)}
            </SelectContent>
          </Select>
          {selectedModel?.description ? <span className="text-xs leading-relaxed text-muted">{selectedModel.description}</span> : catalogError ? <span className="text-xs text-warning">Model catalog unavailable; current setting is preserved.</span> : catalog?.source === 'fallback' ? <span className="text-xs text-muted">Showing the built-in fallback catalog.</span> : null}
        </label>
        <label className="field">
          <span className="field-label">Reasoning effort</span>
          <Select value={value.codexReasoningEffort || 'default'} onValueChange={(next) => onChange({ ...value, codexReasoningEffort: next === 'default' ? '' : next as OperatorAISettings['codexReasoningEffort'] })} disabled={loading}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>{efforts.map((effort) => <SelectItem key={effort || 'default'} value={effort || 'default'}>{effortLabel(effort)}{effort && effort === selectedModel?.defaultReasoningEffort ? ' · model default' : ''}</SelectItem>)}</SelectContent>
          </Select>
          <span className="text-xs leading-relaxed text-muted">Available efforts follow the selected model.</span>
        </label>
      </div>
      <div className="operator-concurrency-setting">
        <span className="operator-concurrency-icon"><Gauge className="size-4" /></span>
        <div className="min-w-0 flex-1">
          <label className="field-label" htmlFor="max-parallel-tasks">Max parallel tasks</label>
          <p className="mt-1 text-xs leading-relaxed text-muted">How many independent ready tasks Nabu may run at once. Two is recommended for normal use.</p>
          <p className="mt-2 flex items-start gap-1.5 text-xs leading-relaxed text-warning"><AlertTriangle className="mt-0.5 size-3.5 shrink-0" />Parallel work can use Codex capacity faster. Dependencies still wait for their prerequisites.</p>
        </div>
        <Select value={String(value.maxParallelTasks || 1)} onValueChange={(next) => onChange({ ...value, maxParallelTasks: Number(next) })} disabled={loading}>
          <SelectTrigger id="max-parallel-tasks" className="w-28" aria-label="Maximum parallel tasks"><SelectValue /></SelectTrigger>
          <SelectContent>{Array.from({ length: 8 }, (_, index) => index + 1).map((count) => <SelectItem key={count} value={String(count)}>{count}{count === 1 ? ' task' : ' tasks'}{count === 2 ? ' · recommended' : ''}</SelectItem>)}</SelectContent>
        </Select>
      </div>
      <div className="mt-4 flex justify-end"><Button variant="primary" onClick={onSave} disabled={loading || saving}>{saving || loading ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Bot className="size-4" />}{loading ? 'Loading settings…' : 'Save AI settings'}</Button></div>
    </Card>
  )
}

function effortLabel(effort: CodexReasoningEffort): string {
  if (!effort) return 'Codex default'
  if (effort === 'xhigh') return 'Extra high'
  if (effort === 'minimal') return 'Minimal (legacy)'
  return effort[0].toUpperCase() + effort.slice(1)
}

function HealthItem({ icon, label, value, ok }: { icon: React.ReactNode; label: string; value: string; ok?: boolean }) {
  return <div className="rounded-lg border border-line bg-canvas p-3"><div className="flex items-center justify-between gap-2"><span className="flex items-center gap-2 text-xs text-muted">{icon}{label}</span>{ok === true ? <CheckCircle2 className="size-3.5 text-accent" /> : ok === false ? <AlertTriangle className="size-3.5 text-warning" /> : <Circle className="size-3.5 text-muted" />}</div><p className="mt-2 truncate text-xs capitalize text-ink">{value.replaceAll('_', ' ')}</p></div>
}

function formatBytes(value?: number): string {
  if (value === undefined) return 'not reported'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit++ }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}
