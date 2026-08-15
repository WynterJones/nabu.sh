import { CheckSquare2, Clock3, ExternalLink, MessageSquareText, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import type { OperatorActivity, StatusResponse, Task } from '../types'
import { formatRelativeTime, operatorStatusLabel } from '../lib/utils'
import { Badge } from './ui/Badge'
import { Popover, PopoverContent, PopoverTrigger } from './ui/Popover'
import { NabuStatusMascot } from './NabuStatusMascot'
import { OperatorStatusBadge } from './StatusBadge'

function activityTarget(activity: OperatorActivity) {
  if (activity.kind === 'task' && activity.entityId) return `/tasks/${encodeURIComponent(activity.entityId)}`
  if (activity.kind === 'run' && activity.entityId) return `/runs/${encodeURIComponent(activity.entityId)}`
  if (activity.kind === 'chat') return '/chat'
  return '/'
}

function ActivityIcon({ kind }: { kind: string }) {
  if (kind === 'task') return <CheckSquare2 className="size-3" />
  if (kind === 'chat') return <MessageSquareText className="size-3" />
  return <Sparkles className="size-3" />
}

function ActivityRow({ activity, onNavigate }: { activity: OperatorActivity; onNavigate: () => void }) {
  const waiting = activity.status === 'waiting'
  return (
    <Link to={activityTarget(activity)} className="operator-activity-row" onClick={onNavigate}>
      <span className="operator-activity-icon"><ActivityIcon kind={activity.kind} /></span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-xs font-semibold text-ink">{activity.label}</span>
          <Badge variant={waiting ? 'warning' : activity.status === 'running' ? 'success' : 'outline'}>{waiting ? 'Waiting' : activity.status === 'running' ? 'Running' : activity.status === 'queued' ? 'Queued' : activity.status}</Badge>
        </span>
        {activity.detail ? <span className="mt-1 line-clamp-2 text-[11px] leading-relaxed text-muted">{activity.detail}</span> : null}
      </span>
      <ExternalLink className="mt-0.5 size-3.5 shrink-0 text-muted" aria-hidden="true" />
    </Link>
  )
}

export function OperatorActivityMenu({ status, tasks }: { status: StatusResponse; tasks: Task[] }) {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const statusLabel = operatorStatusLabel(status.status)
  const activities = status.activities ?? []
  const queuedWaiting = Boolean(status.chatQueued && status.codexState && status.codexState !== 'available')
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="operator-activity-trigger" aria-label={`Open Nabu activity. ${statusLabel}`}>
          <NabuStatusMascot status={status} tasks={tasks} />
          <OperatorStatusBadge status={status.status} />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" sideOffset={10} className="operator-activity-popover">
        <div className="border-b border-line px-3 py-2.5">
          <p className="text-xs font-semibold text-ink">Nabu activity</p>
          <p className="mt-0.5 text-[11px] text-muted">{statusLabel}</p>
        </div>
        {queuedWaiting ? (
          <div className="operator-activity-health" role="status">
            <Clock3 className="mt-0.5 size-4 shrink-0 text-warning" />
            <span><strong>Waiting for Codex</strong>{status.retryAt ? ` · retry ${formatRelativeTime(status.retryAt)}` : ''}</span>
          </div>
        ) : null}
        <div className="space-y-1 p-1.5">
          {activities.length ? activities.map((activity, index) => <ActivityRow key={`${activity.kind}-${activity.entityId ?? index}-${activity.status}`} activity={activity} onNavigate={() => setOpen(false)} />) : (
            <div className="px-3 py-4 text-center">
              <p className="text-xs font-medium text-ink">No work is running</p>
            </div>
          )}
        </div>
        {status.needsAttention ? <button type="button" className="operator-attention-link w-full text-left" onClick={() => { setOpen(false); navigate('/tasks#needs-you', { state: { attentionRequest: Date.now() } }) }}>Review {status.needsAttention} item{status.needsAttention === 1 ? '' : 's'} needing follow-up <ExternalLink className="size-3.5" /></button> : null}
      </PopoverContent>
    </Popover>
  )
}
