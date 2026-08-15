import { AlertTriangle, ArrowUpRight, CheckCircle2, Clock3, ListChecks, RefreshCw } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApprovalCard } from '../components/ApprovalCard'
import { ApprovalResolutionDialog } from '../components/ApprovalResolutionDialog'
import { PageLoading } from '../components/PageState'
import { PriorityBadge, TaskStatusBadge } from '../components/StatusBadge'
import { Button } from '../components/ui/Button'
import { Card, EmptyState, SectionHeader } from '../components/ui/Card'
import { formatRelativeTime, taskSort } from '../lib/utils'
import { useNabu } from '../state/NabuContext'
import type { Task } from '../types'
import { approvalsApi } from '../features/approvals/api'
import type { Approval } from '../features/approvals/types'
import { useResource } from '../hooks/useResource'
import { settingsApi } from '../features/settings/api'

export function OverviewPage() {
  const { status, tasks, loading, refreshing, refresh } = useNabu()
  const approvals = useResource(() => approvalsApi.list('pending'))
  const health = useResource(settingsApi.getHealth)
  const [resolution, setResolution] = useState<{ approval: Approval; decision: 'approved' | 'rejected' } | null>(null)

  const running = useMemo(() => tasks.find((task) => task.id === status?.activeTaskId) ?? tasks.find((task) => task.status === 'running'), [status?.activeTaskId, tasks])
  const queue = useMemo(() => tasks.filter((task) => task.status === 'ready').sort(taskSort).slice(0, 5), [tasks])
  const needsYou = useMemo(() => tasks.filter((task) => ['needs_approval', 'waiting', 'failed'].includes(task.status)).sort(taskSort), [tasks])
  const completed = useMemo(() => tasks.filter((task) => task.status === 'completed').sort((a, b) => new Date(b.completedAt ?? b.updatedAt ?? 0).getTime() - new Date(a.completedAt ?? a.updatedAt ?? 0).getTime()).slice(0, 4), [tasks])
  const codexTrouble = health.data?.codexState === 'unavailable' || health.data?.codexState === 'rate_limited' || status?.codexAvailable === false
  const hasFailedAttention = needsYou.some((task) => task.status === 'failed')
  const needsDirection = needsYou.some((task) => task.status === 'waiting')
  const needsApproval = needsYou.some((task) => task.status === 'needs_approval')
  const lastOutcomeWasSuccess = completed.length > 0 && queue.length === 0
  const statusArt = hasFailedAttention || codexTrouble || (status?.status === 'needs_attention' && !needsDirection)
    ? '/assets/nabu-status-failed.png'
    : status?.status === 'waiting' || approvals.data?.length || needsApproval
      ? '/assets/nabu-status-awaiting-approval.png'
      : needsDirection
        ? '/assets/nabu-status-asking-question.png'
      : status?.status === 'working' || running
        ? '/assets/nabu-status-active.png'
        : lastOutcomeWasSuccess
          ? '/assets/nabu-status-success.png'
          : '/assets/nabu-status-idle.png'

  if (loading) return <PageLoading />

  return (
    <div className="overview-page page-stack max-w-7xl">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Workspace overview</p>
          <h1 className="page-title">Good to see you.</h1>
        </div>
        <Button variant="secondary" size="sm" onClick={() => void refresh()} disabled={refreshing}>
          <RefreshCw className={`size-4 ${refreshing ? 'animate-spin motion-reduce:animate-none' : ''}`} />Refresh
        </Button>
      </div>

      {codexTrouble ? (
        <Card className="reliability-alert shadow-none"><AlertTriangle className="size-5 shrink-0 text-warning" /><div className="min-w-0"><p className="text-sm font-semibold text-ink">{health.data?.codexState === 'rate_limited' ? 'Codex is rate limited' : 'Codex is unavailable'}</p><p className="mt-1 text-xs leading-relaxed text-muted">{health.data?.codexMessage ?? status?.codexMessage ?? 'The queue is safe. Scheduled local scripts continue while Nabu retries availability conservatively.'}</p>{health.data?.retryAt || status?.retryAt ? <p className="mt-2 text-xs text-warning">Retry {formatRelativeTime(health.data?.retryAt ?? status?.retryAt)}</p> : null}</div></Card>
      ) : null}

      {approvals.data?.[0] ? <ApprovalCard approval={approvals.data[0]} onApprove={() => setResolution({ approval: approvals.data![0], decision: 'approved' })} onReject={() => setResolution({ approval: approvals.data![0], decision: 'rejected' })} compact /> : null}
      {needsYou.length ? <NeedsYou task={needsYou[0]} count={needsYou.length} /> : null}

      <div className="overview-grid">
        <Card className="min-w-0 p-5 shadow-none sm:p-6">
          <div className="now-panel-grid"><div className="min-w-0"><SectionHeader eyebrow="Now" title={running ? running.title : status?.paused ? 'Nabu is paused' : 'Nabu is idle'} action={running ? <TaskStatusBadge status={running.status} /> : undefined} />
          {running ? (
            <>
              <p className="mt-4 text-pretty text-sm leading-relaxed text-muted">{running.purpose ?? running.whyThisMatters ?? 'Nabu is working through this task now.'}</p>
              <div className="mt-5 flex flex-wrap items-center justify-between gap-3">
                <span className="flex items-center gap-1.5 text-xs text-muted"><Clock3 className="size-3.5" />Started {formatRelativeTime(running.startedAt)}</span>
                <Button asChild variant="secondary" size="sm"><Link to={`/tasks/${encodeURIComponent(running.id)}`}>View task<ArrowUpRight className="size-3.5" /></Link></Button>
              </div>
            </>
          ) : (
            <>
              <p className="mt-4 text-sm leading-relaxed text-muted">{status?.paused ? 'Resume when you are ready for Nabu to continue working.' : status?.nextOrientationAt ? `Next orientation ${formatRelativeTime(status.nextOrientationAt)}.` : 'No task is currently running.'}</p>
            </>
          )}</div><img key={statusArt} src={statusArt} alt="" className={`status-art ${status?.paused ? 'status-art-subdued' : ''}`} /></div>
        </Card>

        <Card className="min-w-0 overflow-hidden shadow-none">
          <div className="p-5 pb-3 sm:px-6 sm:pt-6"><SectionHeader eyebrow="Next" title={`${queue.length} task${queue.length === 1 ? '' : 's'} ready`} /></div>
          {queue.length ? (
            <ol className="pb-2">
              {queue.map((task, index) => (
                <li key={task.id}>
                  <Link to={`/tasks/${encodeURIComponent(task.id)}`} className="queue-row">
                    <span className="queue-number">{index + 1}</span>
                    <span className="min-w-0 flex-1 truncate text-sm text-ink">{task.title}</span>
                    <PriorityBadge priority={task.priority} />
                  </Link>
                </li>
              ))}
            </ol>
          ) : <p className="px-5 pb-6 pt-2 text-sm text-muted sm:px-6">The Ready queue is empty.</p>}
        </Card>
      </div>

      <section aria-labelledby="latest-results-title">
        <div className="mb-3 px-1"><p className="eyebrow">Latest results</p><h2 id="latest-results-title" className="mt-1 text-base font-semibold text-ink">Meaningful completed work</h2></div>
        {completed.length ? (
          <ol className="result-list">
            {completed.map((task) => (
              <li key={task.id}>
                <Link to={`/tasks/${encodeURIComponent(task.id)}`} className="result-row">
                  <span className="result-icon"><CheckCircle2 className="size-4" /></span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium text-ink">{task.title}</span>
                    <span className="mt-1 block truncate text-xs text-muted">{task.resultSummary ?? task.output ?? task.purpose ?? 'Completed successfully.'}</span>
                  </span>
                  <time className="shrink-0 text-xs tabular-nums text-muted" dateTime={task.completedAt ?? task.updatedAt}>{formatRelativeTime(task.completedAt ?? task.updatedAt)}</time>
                  <ArrowUpRight className="size-4 shrink-0 text-muted" aria-hidden="true" />
                </Link>
              </li>
            ))}
          </ol>
        ) : <EmptyState compact icon={<ListChecks className="size-5" />} title="No completed work yet" description="Verified results will appear here as Nabu completes tasks." />}
      </section>
      <ApprovalResolutionDialog approval={resolution?.approval ?? null} decision={resolution?.decision ?? 'approved'} open={resolution !== null} onOpenChange={(open) => { if (!open) setResolution(null) }} onResolved={(updated) => { approvals.setData((current) => (current ?? []).filter((approval) => approval.id !== updated.id)); setResolution(null); void refresh() }} />
    </div>
  )
}

function NeedsYou({ task, count }: { task: Task; count: number }) {
  return (
    <Card className="attention-card p-5 shadow-none sm:p-6">
      <div className="flex items-start gap-3.5">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-warning/25 bg-warning/10 text-warning"><AlertTriangle className="size-5" /></span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div><p className="eyebrow text-warning">Needs you{count > 1 ? ` · ${count} items` : ''}</p><h2 className="mt-1 text-pretty text-base font-semibold text-ink">{task.title}</h2></div>
            <TaskStatusBadge status={task.status} />
          </div>
          <p className="mt-2 max-w-3xl text-pretty text-sm leading-relaxed text-muted">{task.error ?? task.resultSummary ?? task.purpose ?? 'Nabu needs a decision or direction before this work can continue.'}</p>
          <Button asChild variant="secondary" size="sm" className="mt-4"><Link to={`/tasks/${encodeURIComponent(task.id)}`}>Review task<ArrowUpRight className="size-3.5" /></Link></Button>
        </div>
      </div>
    </Card>
  )
}
