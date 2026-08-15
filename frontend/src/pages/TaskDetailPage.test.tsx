// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NabuContext, type NabuState } from '../state/NabuContext'
import type { Task } from '../types'
import { TaskDetailPage } from './TaskDetailPage'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const task: Task = {
  id: 'task-1',
  title: 'Audit the onboarding funnel',
  purpose: 'Find the highest-impact conversion issue.',
  status: 'ready',
  priority: 'normal',
  definitionOfDone: [{ label: 'Evidence is documented', complete: false }],
  verification: [],
  uncertainties: [],
  artifacts: [],
  artifactFiles: [],
  filesChanged: [],
}

function renderTask(value: Task, refresh = vi.fn().mockResolvedValue(undefined), allTasks: Task[] = [value]) {
  const state: NabuState = {
    status: null,
    mission: null,
    tasks: allTasks,
    workspaces: [],
    scopes: [],
    activeScope: null,
    loading: false,
    refreshing: false,
    error: null,
    refresh,
    switchScope: vi.fn().mockResolvedValue(undefined),
    clearError: vi.fn(),
  }
  render(
    <NabuContext.Provider value={state}>
      <MemoryRouter initialEntries={[`/tasks/${value.id}`]}>
        <Routes>
          <Route path="/tasks/:id" element={<TaskDetailPage />} />
          <Route path="/tasks" element={<div>Task list</div>} />
        </Routes>
      </MemoryRouter>
    </NabuContext.Provider>,
  )
  return refresh
}

describe('TaskDetailPage deletion', () => {
  it('confirms deletion, refreshes state, and returns to the task list', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(task), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    const refresh = renderTask(task)

    fireEvent.click(screen.getByRole('button', { name: 'Delete task' }))
    expect(screen.getByRole('heading', { name: 'Delete this task?' })).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete task' }).at(-1)!)

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1]).toEqual(['/api/tasks/task-1', expect.objectContaining({ method: 'DELETE' })])
    expect(refresh).toHaveBeenCalled()
    expect(await screen.findByText('Task list')).toBeInTheDocument()
  })

  it('guards deletion while the task is running', () => {
    const running = { ...task, status: 'running' as const }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(running), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    renderTask(running)

    expect(screen.getByRole('button', { name: 'Delete task' })).toBeDisabled()
    expect(screen.getByText('Cancel it before deleting.')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Delete this task?' })).not.toBeInTheDocument()
  })
})

describe('TaskDetailPage completion criteria', () => {
  it('distinguishes verified, failed, and pending outcomes', async () => {
    const outcomeTask: Task = {
      ...task,
      status: 'running',
      definitionOfDone: [
        { label: 'Build passes', complete: true, details: 'Exit code 0.' },
        { label: 'Browser check passes', complete: false, failed: true, details: 'Browser unavailable.' },
        { label: 'Publish notes', complete: false },
      ],
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(outcomeTask), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    renderTask(outcomeTask)

    expect((await screen.findByText('Build passes')).closest('li')).not.toHaveClass('task-checklist-item-failed')
    expect(screen.getByText('Browser check passes').closest('li')).toHaveClass('task-checklist-item-failed')
    expect(screen.getByText('Browser check passes').closest('li')).toHaveAttribute('title', 'Browser unavailable.')
    expect(screen.getByText('Publish notes').closest('li')).not.toHaveClass('task-checklist-item-failed')
  })
})

describe('TaskDetailPage immediate execution', () => {
  it('queues a ready task to run now', async () => {
    const queued = { ...task, planned_at: null }
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(task), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ task: queued }), { status: 202, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const refresh = renderTask(task)

    fireEvent.click(await screen.findByRole('button', { name: 'Run now' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1]).toEqual(['/api/tasks/task-1/run', expect.objectContaining({ method: 'POST' })])
    expect(refresh).toHaveBeenCalled()
    expect(await screen.findByRole('button', { name: 'Queued' })).toBeDisabled()
  })

  it('preserves the queued state after the task page reloads', async () => {
    const queued: Task = { ...task, runRequestedAt: '2026-08-14T15:00:00Z' }
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...queued, run_requested_at: queued.runRequestedAt }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    renderTask(queued)

    expect(await screen.findByRole('button', { name: 'Queued' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Queued' }))
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe('TaskDetailPage prerequisites', () => {
  it('shows linked prerequisite state and excludes the task itself from editing', async () => {
    const prerequisite: Task = { ...task, id: 'task-research', title: 'Gather customer evidence', status: 'completed' }
    const dependent: Task = { ...task, dependsOnTaskIds: [prerequisite.id] }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...dependent, depends_on_task_ids: dependent.dependsOnTaskIds }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    renderTask(dependent, vi.fn().mockResolvedValue(undefined), [dependent, prerequisite])

    expect(await screen.findByRole('heading', { name: 'Prerequisites' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Gather customer evidence/ })).toHaveAttribute('href', '/tasks/task-research')
    fireEvent.click(screen.getByRole('button', { name: '1 prerequisite' }))
    expect(screen.getByRole('checkbox', { name: /Gather customer evidence/ })).toBeChecked()
    expect(screen.queryByRole('checkbox', { name: /Audit the onboarding funnel/ })).not.toBeInTheDocument()
  })
})

describe('TaskDetailPage recovery', () => {
  it('queues a task-scoped recovery turn and opens Chat', async () => {
    const failed: Task = {
      ...task,
      status: 'failed',
      failureReason: 'Railway runtime settings were not available.',
      uncertainties: ['Sentry project mappings remain unverified.'],
      runId: 'run-1',
    }
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(failed), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ message: { id: 42, status: 'queued' } }), { status: 202, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(
      <NabuContext.Provider value={{
        status: null, mission: null, tasks: [failed], workspaces: [], scopes: [], activeScope: null,
        loading: false, refreshing: false, error: null, refresh: vi.fn().mockResolvedValue(undefined),
        switchScope: vi.fn().mockResolvedValue(undefined), clearError: vi.fn(),
      }}>
        <MemoryRouter initialEntries={[`/tasks/${failed.id}`]}>
          <Routes>
            <Route path="/tasks/:id" element={<TaskDetailPage />} />
            <Route path="/chat" element={<div>Durable chat</div>} />
          </Routes>
        </MemoryRouter>
      </NabuContext.Provider>,
    )

    expect(await screen.findByRole('heading', { name: 'This task needs another step' })).toBeInTheDocument()
    expect(screen.getByText('What happened')).toBeInTheDocument()
    expect(screen.getByText('Still needed')).toBeInTheDocument()
    fireEvent.change(await screen.findByLabelText('New context for Nabu'), { target: { value: 'Railway is connected now. Preserve the existing report.' } })
    fireEvent.click(screen.getByRole('button', { name: 'Continue in Chat' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1]).toEqual(['/api/tasks/task-1/recover', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ note: 'Railway is connected now. Preserve the existing report.' }),
    })])
    expect(await screen.findByText('Durable chat')).toBeInTheDocument()
  })

  it('closes a failed task while preserving it outside Needs You', async () => {
    const failed: Task = {
      ...task,
      status: 'failed',
      failureReason: 'The provider could not answer every requested site.',
      runId: 'run-1',
    }
    const closed = { ...failed, status: 'cancelled' as const }
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(failed), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(closed), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const refresh = renderTask(failed)

    fireEvent.click(await screen.findByRole('button', { name: 'Close task' }))
    expect(screen.getByRole('heading', { name: 'Close this task?' })).toBeInTheDocument()
    expect(screen.getByText(/report, files, run evidence, and follow-up details remain available/i)).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: 'Close task' }).at(-1)!)

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1]).toEqual(['/api/tasks/task-1', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ status: 'cancelled' }),
    })])
    expect(refresh).toHaveBeenCalled()
    expect((await screen.findAllByText('Cancelled')).length).toBeGreaterThan(0)
    expect(screen.queryByRole('heading', { name: 'Continue this task with Nabu' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })
})
