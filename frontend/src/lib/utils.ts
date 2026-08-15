import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'
import type { OperatorStatus, Task, TaskPriority, TaskStatus } from '../types'

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}

export function normalizeTaskStatus(value: unknown): TaskStatus {
  const status = String(value ?? 'idea').trim().toLowerCase().replace(/[ -]+/g, '_')
  const aliases: Record<string, TaskStatus> = {
    pending: 'ready',
    queued: 'ready',
    in_progress: 'running',
    active: 'running',
    blocked: 'waiting',
    needs_you: 'needs_approval',
    approval: 'needs_approval',
    complete: 'completed',
    done: 'completed',
    error: 'failed',
    canceled: 'cancelled',
  }
  const normalized = aliases[status] ?? status
  const valid: TaskStatus[] = ['idea', 'ready', 'running', 'waiting', 'needs_approval', 'completed', 'failed', 'cancelled']
  return valid.includes(normalized as TaskStatus) ? (normalized as TaskStatus) : 'idea'
}

export function normalizePriority(value: unknown): TaskPriority {
  const priority = String(value ?? 'normal').toLowerCase()
  return priority === 'high' || priority === 'low' ? priority : 'normal'
}

export function normalizeOperatorStatus(value: unknown, paused = false): OperatorStatus {
  if (paused) return 'paused'
  const status = String(value ?? 'idle').trim().toLowerCase().replace(/[ -]+/g, '_')
  const aliases: Record<string, OperatorStatus> = {
    active: 'working',
    running: 'working',
    waiting_for_approval: 'waiting',
    needs_approval: 'waiting',
    attention: 'needs_attention',
  }
  const normalized = aliases[status] ?? status
  const valid: OperatorStatus[] = ['working', 'idle', 'waiting', 'paused', 'needs_attention']
  return valid.includes(normalized as OperatorStatus) ? (normalized as OperatorStatus) : 'idle'
}

export function operatorStatusLabel(status: OperatorStatus): string {
  return {
    working: 'Working',
    idle: 'Idle',
    waiting: 'Waiting for approval',
    paused: 'Paused',
    needs_attention: 'Needs attention',
  }[status]
}

export function taskStatusLabel(status: TaskStatus): string {
  return {
    idea: 'Idea',
    ready: 'Ready',
    running: 'Running',
    waiting: 'Waiting',
    needs_approval: 'Needs approval',
    completed: 'Completed',
    failed: 'Needs follow-up',
    cancelled: 'Cancelled',
  }[status]
}

export function isAbsoluteWorkspacePath(path: string): boolean {
  const trimmed = path.trim()
  return trimmed.startsWith('/') && !trimmed.includes('\0')
}

export function formatRelativeTime(value?: string): string {
  if (!value) return 'Just now'
  const timestamp = new Date(value).getTime()
  if (Number.isNaN(timestamp)) return 'Unknown time'
  const seconds = Math.round((timestamp - Date.now()) / 1000)
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  const ranges: Array<[number, Intl.RelativeTimeFormatUnit]> = [
    [60, 'second'],
    [60, 'minute'],
    [24, 'hour'],
    [7, 'day'],
    [4.345, 'week'],
    [12, 'month'],
    [Number.POSITIVE_INFINITY, 'year'],
  ]
  let duration = seconds
  for (const [range, unit] of ranges) {
    if (Math.abs(duration) < range) return formatter.format(Math.round(duration), unit)
    duration /= range
  }
  return formatter.format(Math.round(duration), 'year')
}

export function taskSort(a: Task, b: Task): number {
  const priorityWeight: Record<TaskPriority, number> = { high: 0, normal: 1, low: 2 }
  const priorityDelta = priorityWeight[a.priority] - priorityWeight[b.priority]
  if (priorityDelta !== 0) return priorityDelta
  return new Date(a.createdAt ?? 0).getTime() - new Date(b.createdAt ?? 0).getTime()
}

export function truncate(value: string, length = 120): string {
  if (value.length <= length) return value
  return `${value.slice(0, Math.max(0, length - 1)).trimEnd()}…`
}
