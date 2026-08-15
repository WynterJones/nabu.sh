import { describe, expect, it, vi } from 'vitest'
import { formatRelativeTime, isAbsoluteWorkspacePath, normalizeOperatorStatus, normalizeTaskStatus, taskSort } from './utils'
import type { Task } from '../types'

describe('workspace path validation', () => {
  it('accepts absolute Mac and Linux paths', () => {
    expect(isAbsoluteWorkspacePath('/Users/nabu/Code/project')).toBe(true)
    expect(isAbsoluteWorkspacePath('/srv/repos/project')).toBe(true)
  })

  it('rejects home shortcuts and relative paths', () => {
    expect(isAbsoluteWorkspacePath('~/Code/project')).toBe(false)
    expect(isAbsoluteWorkspacePath('../project')).toBe(false)
    expect(isAbsoluteWorkspacePath('')).toBe(false)
  })
})

describe('status normalization', () => {
  it('maps API aliases to the durable UI states', () => {
    expect(normalizeTaskStatus('in progress')).toBe('running')
    expect(normalizeTaskStatus('done')).toBe('completed')
    expect(normalizeOperatorStatus('waiting for approval')).toBe('waiting')
    expect(normalizeOperatorStatus('running', true)).toBe('paused')
  })
})

describe('formatRelativeTime', () => {
  it('formats a stable recent timestamp', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-11T12:00:00Z'))
    expect(formatRelativeTime('2026-08-11T11:50:00Z')).toContain('10 minutes ago')
    vi.useRealTimers()
  })
})

describe('taskSort', () => {
  it('orders priority before creation time', () => {
    const task = (id: string, priority: Task['priority'], createdAt: string): Task => ({
      id,
      title: id,
      priority,
      status: 'ready',
      createdAt,
      definitionOfDone: [],
      verification: [],
      uncertainties: [],
      artifacts: [],
      artifactFiles: [],
      filesChanged: [],
    })
    const tasks = [task('low', 'low', '2026-01-01'), task('high', 'high', '2026-08-01'), task('normal', 'normal', '2026-01-01')]
    expect(tasks.sort(taskSort).map((item) => item.id)).toEqual(['high', 'normal', 'low'])
  })
})
