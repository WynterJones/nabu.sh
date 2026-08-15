import { AlertTriangle, ArrowLeft, Ban, Check, Circle, CircleX, FileCode2, LoaderCircle, MessageSquareText, Play, RotateCcw, Send, TerminalSquare, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { InlineError, PageLoading } from '../components/PageState'
import { TaskStatusBadge } from '../components/StatusBadge'
import { Button } from '../components/ui/Button'
import { Card, EmptyState, SectionHeader } from '../components/ui/Card'
import { api } from '../lib/api'
import { cn, formatRelativeTime } from '../lib/utils'
import { useNabu } from '../state/NabuContext'
import type { Task, TaskPriority, TaskStatus } from '../types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { FileLink } from '../components/FileViewer'
import { Textarea } from '../components/ui/Field'
import { TaskDependencyPicker } from '../components/TaskDependencyPicker'

export function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { tasks, refresh } = useNabu()
  const cachedTask = useMemo(() => tasks.find((item) => item.id === id), [id, tasks])
  const [task, setTask] = useState<Task | null>(cachedTask ?? null)
  const [loading, setLoading] = useState(!cachedTask)
  const [updating, setUpdating] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [closeOpen, setCloseOpen] = useState(false)
  const [closing, setClosing] = useState(false)
  const [recoveryNote, setRecoveryNote] = useState('')
  const [recovering, setRecovering] = useState(false)
  const [runningNow, setRunningNow] = useState(false)
  const [runQueued, setRunQueued] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    let alive = true
    setLoading(!cachedTask)
    api.getTask(id).then((result) => {
      if (alive) setTask(result)
    }).catch((caught: unknown) => {
      if (alive) setError(caught instanceof Error ? caught.message : 'The task could not be loaded.')
    }).finally(() => {
      if (alive) setLoading(false)
    })
    return () => { alive = false }
  }, [cachedTask, id])

  const update = async (values: Record<string, unknown>) => {
    if (!id || updating) return
    setUpdating(true)
    setError(null)
    try {
      const nextTask = await api.updateTask(id, values)
      setTask(nextTask)
      await refresh()
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The task could not be updated.')
    } finally {
      setUpdating(false)
    }
  }

  const remove = async () => {
    if (!id || !task || task.status === 'running' || deleting) return
    setDeleting(true)
    setError(null)
    try {
      await api.deleteTask(id)
      await refresh()
      navigate('/tasks', { replace: true })
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The task could not be deleted.')
      setDeleteOpen(false)
    } finally {
      setDeleting(false)
    }
  }

  const continueWithNabu = async () => {
    if (!id || !task || recovering) return
    setRecovering(true)
    setError(null)
    try {
      await api.recoverTask(id, recoveryNote.trim())
      navigate('/chat', { state: { recoveryTaskId: id } })
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Nabu could not start the recovery conversation.')
    } finally {
      setRecovering(false)
    }
  }

  const closeTask = async () => {
    if (!id || !task || task.status !== 'failed' || closing) return
    setClosing(true)
    setError(null)
    try {
      const nextTask = await api.updateTask(id, { status: 'cancelled' })
      setTask(nextTask)
      setCloseOpen(false)
      await refresh()
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The task could not be closed.')
    } finally {
      setClosing(false)
    }
  }

  const runNow = async () => {
    if (!id || !task || runningNow || runQueued || task.runRequestedAt || !['idea', 'ready'].includes(task.status)) return
    setRunningNow(true)
    setError(null)
    try {
      const nextTask = await api.runTask(id)
      setTask(nextTask)
      setRunQueued(true)
      await refresh()
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The task could not be queued.')
    } finally {
      setRunningNow(false)
    }
  }

  if (loading) return <PageLoading label="Loading task detail…" />
  if (!task) return <EmptyState title="Task not found" description={error ?? 'This task may have been removed.'} action={<Button variant="secondary" onClick={() => navigate('/tasks')}>Back to tasks</Button>} />

  const canCancel: TaskStatus[] = ['idea', 'ready', 'running', 'waiting', 'needs_approval']
  const runIsQueued = runQueued || Boolean(task.runRequestedAt)
  return (
    <div className="page-stack max-w-5xl">
      <div className="flex items-center justify-between gap-3">
        <Button asChild variant="secondary" size="sm"><Link to="/tasks"><ArrowLeft className="size-4" />All tasks</Link></Button>
        <div className="flex items-center gap-2">
          {task.status === 'failed' || task.status === 'cancelled' ? (
            <Button variant="secondary" size="sm" onClick={() => void update({ status: 'ready' })} disabled={updating}>
              <RotateCcw className="size-4" />Retry
            </Button>
          ) : null}
          {task.status === 'idea' || task.status === 'ready' ? (
            <Button variant="primary" size="sm" onClick={() => void runNow()} disabled={updating || runningNow || runIsQueued}>
              {runningNow ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Play className="size-4" />}
              {runningNow ? 'Queueing…' : runIsQueued ? 'Queued' : 'Run now'}
            </Button>
          ) : null}
          {canCancel.includes(task.status) ? (
            <Button variant="danger" size="sm" onClick={() => void update({ status: 'cancelled' })} disabled={updating}>
              {updating ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Ban className="size-4" />}Cancel
            </Button>
          ) : null}
        </div>
      </div>

      <div className="page-heading items-start">
        <div className="min-w-0">
          <h1 className="task-detail-title text-balance">{task.title}</h1>
          {task.purpose ? <p className="task-detail-description max-w-4xl">{task.purpose}</p> : null}
        </div>
      </div>

      {error ? <InlineError message={error} /> : null}

      {task.status === 'failed' ? (
        <Card className="task-followup-card p-5 shadow-none sm:p-6">
          <div className="flex items-start gap-3">
            <span className="task-followup-icon">
              <AlertTriangle className="size-5" />
            </span>
            <div className="min-w-0 flex-1">
              <h2 className="text-lg font-semibold text-ink">This task needs another step</h2>
              <div className="task-followup-section">
                <h3>What happened</h3>
                <p>{task.failureReason ?? task.resultSummary ?? task.error ?? 'The run stopped before every requested outcome could be verified.'}</p>
              </div>
              {task.uncertainties.length ? (
                <div className="task-followup-section">
                  <h3>Still needed</h3>
                  <ul className="mt-2 space-y-1.5 text-sm text-muted">
                  {task.uncertainties.slice(0, 3).map((item) => <li key={item} className="flex gap-2"><span aria-hidden="true">•</span><span>{item}</span></li>)}
                  </ul>
                </div>
              ) : null}
              {(task.resultSummary || task.verification.length || task.filesChanged.length || task.artifacts.length || task.runId) ? (
                <div className="task-followup-preserved">
                  <Check className="mt-0.5 size-4 shrink-0 text-accent" />
                  <p><strong>Work is preserved.</strong> {[
                    task.resultSummary ? 'result summary' : '',
                    task.verification.length ? `${task.verification.length} verification ${task.verification.length === 1 ? 'entry' : 'entries'}` : '',
                    task.filesChanged.length ? `${task.filesChanged.length} changed ${task.filesChanged.length === 1 ? 'file' : 'files'}` : '',
                    task.artifacts.length ? `${task.artifacts.length} ${task.artifacts.length === 1 ? 'artifact' : 'artifacts'}` : '',
                    task.runId ? 'run evidence' : '',
                  ].filter(Boolean).join(', ')} remain available below.</p>
                </div>
              ) : null}
              <div className="task-recovery">
                <div className="flex items-start gap-3">
                  <span className="task-recovery-icon"><MessageSquareText className="size-4" /></span>
                  <div className="min-w-0">
                    <h3 className="text-sm font-semibold text-ink">Continue this task with Nabu</h3>
                  </div>
                </div>
                <Textarea
                  value={recoveryNote}
                  onChange={(event) => setRecoveryNote(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey) && !event.nativeEvent.isComposing) {
                      event.preventDefault()
                      void continueWithNabu()
                    }
                  }}
                  className="task-recovery-input"
                  rows={2}
                  maxLength={16000}
                  aria-label="New context for Nabu"
                  placeholder="Add new context, constraints, or the next step…"
                />
                <div className="task-recovery-actions">
                  <div className="task-recovery-secondary-actions">
                    {task.runId ? <Button asChild variant="secondary"><Link to={`/runs/${encodeURIComponent(task.runId)}`}><TerminalSquare className="size-4" />Run evidence</Link></Button> : null}
                    <Button variant="secondary" onClick={() => setCloseOpen(true)} disabled={closing}>
                      <CircleX className="size-4" />Close task
                    </Button>
                  </div>
                  <Button variant="primary" onClick={() => void continueWithNabu()} disabled={recovering}>
                    {recovering ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Send className="size-4" />}
                    {recovering ? 'Sending…' : 'Continue in Chat'}
                  </Button>
                </div>
              </div>
            </div>
          </div>
        </Card>
      ) : null}

      <div className="detail-grid">
        <div className="min-w-0 space-y-4">
          <Card className="p-5 shadow-none sm:p-6">
            <SectionHeader title="Why this matters" />
            <p className="mt-4 text-pretty text-sm leading-relaxed text-muted">{task.whyThisMatters ?? 'No mission rationale was recorded for this task.'}</p>
          </Card>
          {task.dependsOnTaskIds?.length ? (
            <Card className="p-5 shadow-none sm:p-6">
              <SectionHeader
                title="Prerequisites"
                action={<TaskDependencyPicker tasks={tasks} selectedIds={task.dependsOnTaskIds} excludeTaskId={task.id} disabled={updating} onChange={(dependsOnTaskIds) => void update({ depends_on_task_ids: dependsOnTaskIds })} />}
              />
              <p className="mt-2 text-xs leading-relaxed text-muted">Nabu starts this task after every prerequisite is completed or closed.</p>
              <ul className="task-prerequisite-list">
                {task.dependsOnTaskIds.map((dependencyId) => {
                  const dependency = tasks.find((item) => item.id === dependencyId)
                  return (
                    <li key={dependencyId}>
                      <Link to={`/tasks/${encodeURIComponent(dependencyId)}`} className="task-prerequisite-row">
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium text-ink">{dependency?.title ?? 'Unavailable task'}</span>
                          {!dependency ? <code className="mt-0.5 block truncate text-[10px] text-muted">{dependencyId}</code> : null}
                        </span>
                        {dependency ? <TaskStatusBadge status={dependency.status} /> : null}
                      </Link>
                    </li>
                  )
                })}
              </ul>
            </Card>
          ) : null}
          <Card className="p-5 shadow-none sm:p-6">
            <SectionHeader title="Definition of done" />
            {task.definitionOfDone.length ? (
              <ul className="task-checklist mt-4">
                {task.definitionOfDone.map((item, index) => (
                  <li key={`${item.label}-${index}`} className={cn('task-checklist-item', item.failed && 'task-checklist-item-failed')} title={item.details}>
                    {item.complete ? <Check className="size-4 shrink-0 text-accent" /> : item.failed ? <CircleX className="size-4 shrink-0 text-danger" /> : <Circle className={cn('size-4 shrink-0 text-muted', task.status === 'running' && 'task-checklist-pending')} />}
                    <span className={item.complete ? 'text-muted line-through' : item.failed ? 'text-danger/85' : 'text-ink'}>{item.label}</span>
                  </li>
                ))}
              </ul>
            ) : <p className="mt-4 text-sm text-muted">No explicit completion criteria were recorded.</p>}
          </Card>
          {task.status !== 'failed' && (task.resultSummary || task.output || task.error) ? (
            <Card className="p-5 shadow-none sm:p-6">
              <SectionHeader title={task.error ? 'Task failed' : 'Latest result'} />
              <p className={`mt-4 whitespace-pre-wrap text-sm leading-relaxed ${task.error ? 'text-danger' : 'text-muted'}`}>{task.error ?? task.resultSummary ?? task.output}</p>
            </Card>
          ) : null}
          {(task.verification.length || task.filesChanged.length || task.artifacts.length) ? (
            <Card className="p-5 shadow-none sm:p-6">
              <SectionHeader title="Output and verification" />
              <EvidenceSection icon={<Check className="size-4" />} title="Verification" values={task.verification} />
              <EvidenceSection icon={<FileCode2 className="size-4" />} title="Files changed" values={task.filesChanged} mono files />
              <EvidenceSection icon={<TerminalSquare className="size-4" />} title="Artifacts" values={task.artifacts} mono filePaths={new Map(task.artifactFiles.map((artifact) => [artifact.name, artifact.path]))} />
            </Card>
          ) : null}
        </div>
        <Card className="task-detail-sidebar h-fit p-5 shadow-none">
          <SectionHeader title="Details" />
          <dl className="task-detail-meta mt-4">
            <Detail label="Status"><TaskStatusBadge status={task.status} /></Detail>
            <Detail label="Priority">
              <select className="task-priority-select" value={task.priority} onChange={(event) => void update({ priority: event.target.value as TaskPriority })} disabled={updating} aria-label="Task priority">
                <option value="high">High</option><option value="normal">Normal</option><option value="low">Low</option>
              </select>
            </Detail>
            <Detail label="Workspace"><span className="truncate" title={task.workspace ?? undefined}>{workspaceName(task.workspace)}</span></Detail>
            <Detail label="Created by">{task.createdBy ?? 'Nabu'}</Detail>
            <Detail label="Created">{formatRelativeTime(task.createdAt)}</Detail>
            {task.plannedAt ? <Detail label="Planned">{formatRelativeTime(task.plannedAt)}</Detail> : null}
            {task.startedAt ? <Detail label="Started">{formatRelativeTime(task.startedAt)}</Detail> : null}
          </dl>
          {task.runId ? <Button asChild variant="secondary" className="mt-5 w-full"><Link to={`/runs/${encodeURIComponent(task.runId)}`}><TerminalSquare className="size-4" />View run activity</Link></Button> : null}
          <div className="mt-5 border-t border-line/35 pt-4">
            <Button variant="danger" className="w-full" onClick={() => { if (task.status !== 'running') setDeleteOpen(true) }} disabled={task.status === 'running' || updating || deleting}>
              <Trash2 className="size-4" />Delete task
            </Button>
            <p className="mt-2 text-[11px] leading-relaxed text-muted">{task.status === 'running' ? 'Cancel it before deleting.' : 'Permanent and cannot be undone.'}</p>
          </div>
        </Card>
      </div>
      <ConfirmDialog
        open={closeOpen}
        onOpenChange={(open) => { if (!closing) setCloseOpen(open) }}
        title="Close this task?"
        description="This removes the task from Needs You without claiming its unfinished outcomes were completed. Its report, files, run evidence, and follow-up details remain available."
        details={<><span className="font-medium text-ink">{task.title}</span><span className="mt-1 block text-xs text-muted">You can reopen it later with Retry.</span></>}
        confirmLabel="Close task"
        pending={closing}
        onConfirm={() => void closeTask()}
      />
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={(open) => { if (!deleting) setDeleteOpen(open) }}
        title="Delete this task?"
        description="This permanently removes the task from the active workspace. This action cannot be undone."
        details={<span className="font-medium text-ink">{task.title}</span>}
        confirmLabel="Delete task"
        destructive
        pending={deleting}
        onConfirm={() => void remove()}
      />
    </div>
  )
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return <div className="task-detail-meta-row"><dt>{label}</dt><dd>{children}</dd></div>
}

function workspaceName(workspace?: string) {
  if (!workspace) return 'None'
  const parts = workspace.replace(/[\\/]+$/, '').split(/[\\/]/)
  return parts.at(-1) || workspace
}

function EvidenceSection({ icon, title, values, mono = false, files = false, filePaths = new Map<string, string>() }: { icon: React.ReactNode; title: string; values: string[]; mono?: boolean; files?: boolean; filePaths?: Map<string, string> }) {
  if (!values.length) return null
  return (
    <div className="mt-5">
      <h3 className="flex items-center gap-2 text-xs font-semibold text-muted">{icon}{title}</h3>
      <ul className="evidence-list">{values.map((value, index) => {
        const path = files ? value : filePaths.get(value) ?? [...filePaths.entries()].find(([name]) => value.startsWith(`${name}:`))?.[1]
        return <li key={`${value}-${index}`} className={`evidence-row ${mono ? 'font-mono' : ''}`}>{path ? <FileLink path={path}>{value}</FileLink> : value}</li>
      })}</ul>
    </div>
  )
}
