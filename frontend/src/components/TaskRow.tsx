import { ArrowUpRight, CalendarClock, Clock3 } from 'lucide-react'
import { Link } from 'react-router-dom'
import { formatRelativeTime, taskStatusLabel } from '../lib/utils'
import type { Task } from '../types'

export function TaskRow({ task }: { task: Task }) {
  const timestamp = task.completedAt ?? task.startedAt ?? task.createdAt
  return (
    <Link to={`/tasks/${encodeURIComponent(task.id)}`} className="task-row" aria-label={`Open ${task.title}, ${taskStatusLabel(task.status)}`}>
      <div className="min-w-0 flex-1">
        <h3 className="truncate text-sm font-medium text-ink">{task.title}</h3>
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
          <span className="flex items-center gap-1.5"><Clock3 className="size-3" />{formatRelativeTime(timestamp)}</span>
          {task.plannedAt ? <span className="flex items-center gap-1.5"><CalendarClock className="size-3" />Planned {formatRelativeTime(task.plannedAt)}</span> : null}
        </div>
      </div>
      <ArrowUpRight className="size-4 shrink-0 text-muted" aria-hidden="true" />
    </Link>
  )
}
