import { CalendarClock, Clock3, LoaderCircle, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card, EmptyState } from '../../components/ui/Card'
import { Dialog } from '../../components/ui/Dialog'
import { Field, Input } from '../../components/ui/Field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/Select'
import { Switch } from '../../components/ui/Switch'
import { settingsApi } from '../../features/settings/api'
import type { Schedule, ScheduleTrigger } from '../../features/settings/types'
import { useResource } from '../../hooks/useResource'
import { formatRelativeTime } from '../../lib/utils'

interface ScheduleForm { name: string; triggerType: ScheduleTrigger; target: string; cadenceMode: 'interval' | 'cron'; cadence: string; enabled: boolean }
const emptyForm: ScheduleForm = { name: '', triggerType: 'orientation', target: '', cadenceMode: 'interval', cadence: '3600', enabled: true }

function toForm(schedule?: Schedule | null): ScheduleForm {
  if (!schedule) return emptyForm
  const target = typeof schedule.payload.target === 'string' ? schedule.payload.target : typeof schedule.payload.script_id === 'string' ? schedule.payload.script_id : typeof schedule.payload.title === 'string' ? schedule.payload.title : ''
  return { name: schedule.name, triggerType: schedule.triggerType, target, cadenceMode: schedule.cadence.expression ? 'cron' : 'interval', cadence: schedule.cadence.expression ?? String(schedule.cadence.intervalSeconds ?? 3600), enabled: schedule.enabled }
}

function cadenceLabel(schedule: Schedule): string {
  if (schedule.cadence.expression) return schedule.cadence.expression
  const seconds = schedule.cadence.intervalSeconds ?? 0
  if (seconds >= 86400 && seconds % 86400 === 0) return `Every ${seconds / 86400} day${seconds === 86400 ? '' : 's'}`
  if (seconds >= 3600 && seconds % 3600 === 0) return `Every ${seconds / 3600} hour${seconds === 3600 ? '' : 's'}`
  if (seconds >= 60 && seconds % 60 === 0) return `Every ${seconds / 60} minute${seconds === 60 ? '' : 's'}`
  return `Every ${seconds} seconds`
}

export function SchedulesSettingsPage() {
  const { data, setData, loading, error } = useResource(settingsApi.listSchedules)
  const schedules = data ?? []
  const [editing, setEditing] = useState<Schedule | null | undefined>(undefined)
  const [form, setForm] = useState<ScheduleForm>(emptyForm)
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<Schedule | null>(null)
  const interval = Number(form.cadence)
  const cadenceValid = Boolean(form.cadence.trim()) && (form.cadenceMode === 'cron' || (Number.isInteger(interval) && interval >= 60))
  const targetValid = form.triggerType === 'orientation' || Boolean(form.target.trim())

  const openForm = (schedule: Schedule | null) => { setEditing(schedule); setForm(toForm(schedule)); setActionError(null) }
  const save = async () => {
    if (!form.name.trim() || !cadenceValid || !targetValid) return
    setSaving(true); setActionError(null)
    const value: Omit<Schedule, 'id'> = {
      name: form.name.trim(), triggerType: form.triggerType,
      payload: form.target.trim() ? { target: form.target.trim() } : {},
      cadence: form.cadenceMode === 'cron' ? { expression: form.cadence.trim() } : { intervalSeconds: Number(form.cadence) },
      enabled: form.enabled,
    }
    try {
      const updated = editing ? await settingsApi.updateSchedule(editing.id, value) : await settingsApi.createSchedule(value)
      setData((current) => editing ? (current ?? []).map((item) => item.id === updated.id ? updated : item) : [...(current ?? []), updated])
      setEditing(undefined)
    } catch (caught) { setActionError(caught instanceof Error ? caught.message : 'Schedule could not be saved.') }
    finally { setSaving(false) }
  }
  const toggle = async (schedule: Schedule, enabled: boolean) => {
    try { const updated = await settingsApi.updateSchedule(schedule.id, { enabled }); setData((current) => (current ?? []).map((item) => item.id === updated.id ? updated : item)) }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'Schedule could not be updated.') }
  }
  const remove = async () => {
    if (!deleting) return
    setSaving(true)
    try { await settingsApi.deleteSchedule(deleting.id); setData((current) => (current ?? []).filter((item) => item.id !== deleting.id)); setDeleting(null) }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'Schedule could not be deleted.') }
    finally { setSaving(false) }
  }

  if (loading) return <PageLoading label="Loading schedules…" />
  return (
    <div className="settings-content-stack">
      <div className="flex flex-wrap items-end justify-between gap-4"><div><p className="eyebrow">Deterministic timing</p><h2 className="settings-title">Schedules</h2><p className="settings-description">Run scripts, create bounded tasks, or orient without a constant AI heartbeat.</p></div><Button variant="primary" onClick={() => openForm(null)}><Plus className="size-4" />New schedule</Button></div>
      {(error || actionError) ? <InlineError message={actionError ?? error ?? ''} /> : null}
      {!schedules.length ? <EmptyState compact icon={<CalendarClock className="size-5" />} title="No schedules configured" description="Add a schedule for repeatable checks or periodic orientation." action={<Button variant="primary" onClick={() => openForm(null)}><Plus className="size-4" />New schedule</Button>} /> : <div className="space-y-2">{schedules.map((schedule) => <Card key={schedule.id} className="schedule-row shadow-none"><span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><CalendarClock className="size-4" /></span><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-medium text-ink">{schedule.name}</h3><Badge>{schedule.triggerType}</Badge>{schedule.lastStatus ? <Badge variant={schedule.lastStatus === 'failed' ? 'danger' : 'success'}>{schedule.lastStatus}</Badge> : null}</div><p className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted"><span className="flex items-center gap-1"><Clock3 className="size-3" />{cadenceLabel(schedule)}</span>{schedule.nextRunAt && schedule.enabled ? <span>Next {formatRelativeTime(schedule.nextRunAt)}</span> : null}</p>{schedule.lastError ? <p className="mt-1 truncate text-xs text-danger" title={schedule.lastError}>{schedule.lastError}</p> : null}</div><Switch checked={schedule.enabled} onCheckedChange={(checked) => void toggle(schedule, checked)} aria-label={`${schedule.enabled ? 'Disable' : 'Enable'} ${schedule.name}`} /><Button variant="ghost" size="icon" aria-label={`Edit ${schedule.name}`} onClick={() => openForm(schedule)}><Pencil className="size-4" /></Button><Button variant="ghost" size="icon" aria-label={`Delete ${schedule.name}`} onClick={() => setDeleting(schedule)}><Trash2 className="size-4" /></Button></Card>)}</div>}
      <Dialog open={editing !== undefined} onOpenChange={(open) => { if (!open && !saving) setEditing(undefined) }} title={editing ? 'Edit schedule' : 'Create schedule'} description="Schedules are durable and resume after the local service restarts." footer={<><Button variant="ghost" onClick={() => setEditing(undefined)} disabled={saving}>Cancel</Button><Button variant="primary" onClick={() => void save()} disabled={!form.name.trim() || !cadenceValid || !targetValid || saving}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <CalendarClock className="size-4" />}{saving ? 'Saving…' : 'Save schedule'}</Button></>}>
        <div className="space-y-4"><Field label="Name" hint="Required"><Input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} placeholder="Hourly site health" autoFocus /></Field><div className="form-grid"><Field label="Action"><Select value={form.triggerType} onValueChange={(value: ScheduleTrigger) => setForm((current) => ({ ...current, triggerType: value }))}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="script">Run script</SelectItem><SelectItem value="task">Create task</SelectItem><SelectItem value="orientation">Orient</SelectItem></SelectContent></Select></Field><Field label="Cadence type"><Select value={form.cadenceMode} onValueChange={(value: 'interval' | 'cron') => setForm((current) => ({ ...current, cadenceMode: value }))}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="interval">Interval</SelectItem><SelectItem value="cron">Cron expression</SelectItem></SelectContent></Select></Field></div>{form.triggerType !== 'orientation' ? <Field label={form.triggerType === 'script' ? 'Script ID or name' : 'Task title'} hint="Required" error={!targetValid ? 'Choose the script or name the task this schedule should create.' : undefined}><Input value={form.target} onChange={(event) => setForm((current) => ({ ...current, target: event.target.value }))} placeholder={form.triggerType === 'script' ? 'site-health' : 'Weekly search review'} /></Field> : null}<Field label={form.cadenceMode === 'cron' ? 'Cron expression' : 'Interval in seconds'} hint="Required" error={form.cadence && !cadenceValid ? 'Use a whole-number interval of at least 60 seconds.' : undefined}><Input value={form.cadence} type={form.cadenceMode === 'interval' ? 'number' : 'text'} min={form.cadenceMode === 'interval' ? 60 : undefined} step={form.cadenceMode === 'interval' ? 1 : undefined} onChange={(event) => setForm((current) => ({ ...current, cadence: event.target.value }))} placeholder={form.cadenceMode === 'cron' ? '0 9 * * 1-5' : '3600'} /></Field><div className="permission-row rounded-lg border"><div><p className="text-sm font-medium text-ink">Enabled</p><p className="mt-0.5 text-xs text-muted">Run this schedule when it is due.</p></div><Switch checked={form.enabled} onCheckedChange={(enabled) => setForm((current) => ({ ...current, enabled }))} /></div>{actionError ? <InlineError message={actionError} /> : null}</div>
      </Dialog>
      <ConfirmDialog open={deleting !== null} onOpenChange={(open) => { if (!open) setDeleting(null) }} title="Delete schedule?" description="This stops future runs. Existing task, report, and script history remains available." details={deleting?.name} confirmLabel="Delete schedule" destructive pending={saving} onConfirm={() => void remove()} />
    </div>
  )
}
