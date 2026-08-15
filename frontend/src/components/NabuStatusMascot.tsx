import type { StatusResponse, Task } from '../types'
import { cn } from '../lib/utils'

export function statusMascotSource(status: StatusResponse, tasks: Task[]) {
  if (tasks.some((task) => task.status === 'failed') || status.status === 'needs_attention') {
    return '/assets/nabu-status-asking-question.png'
  }
  if (status.status === 'waiting' || tasks.some((task) => task.status === 'needs_approval')) {
    return '/assets/nabu-status-awaiting-approval.png'
  }
  if (tasks.some((task) => task.status === 'waiting')) {
    return '/assets/nabu-status-asking-question.png'
  }
  if (status.status === 'working' || tasks.some((task) => task.status === 'running')) {
    return '/assets/nabu-status-active.png'
  }
  if (!status.paused && tasks.some((task) => task.status === 'completed') && !tasks.some((task) => task.status === 'ready')) {
    return '/assets/nabu-status-success.png'
  }
  return '/assets/nabu-status-idle.png'
}

export function NabuStatusMascot({ status, tasks }: { status: StatusResponse; tasks: Task[] }) {
  const source = statusMascotSource(status, tasks)
  return <img key={source} src={source} alt="" aria-hidden="true" className={cn('operator-mascot', status.paused && 'operator-mascot-subdued')} />
}
