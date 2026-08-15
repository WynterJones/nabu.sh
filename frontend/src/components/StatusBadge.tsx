import { Circle } from 'lucide-react'
import type { OperatorStatus, TaskPriority, TaskStatus } from '../types'
import { cn, operatorStatusLabel, taskStatusLabel } from '../lib/utils'

export function OperatorStatusBadge({ status }: { status: OperatorStatus }) {
  const label = `Nabu is ${operatorStatusLabel(status).toLowerCase()}`
  return (
    <span className={cn('operator-status-dot', `status-${status}`)} aria-label={label} title={label}>
      <Circle className="size-2.5 fill-current" aria-hidden="true" />
    </span>
  )
}

export function TaskStatusBadge({ status }: { status: TaskStatus }) {
  return <span className={cn('task-badge', `task-${status}`)}>{taskStatusLabel(status)}</span>
}

export function PriorityBadge({ priority }: { priority: TaskPriority }) {
  return <span className={cn('priority-badge', `priority-${priority}`)}>{priority}</span>
}
