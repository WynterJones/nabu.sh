// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NabuContext, type NabuState } from '../state/NabuContext'
import type { Task } from '../types'
import { TasksPage } from './TasksPage'

function task(id: string, status: Task['status'], priority: Task['priority'] = 'normal'): Task {
  return {
    id,
    title: `Task ${id}`,
    status,
    priority,
    createdAt: `2026-08-${String(Number(id.replace(/\D/g, '')) || 1).padStart(2, '0')}T12:00:00Z`,
    definitionOfDone: [],
    verification: [],
    uncertainties: [],
    artifacts: [],
    artifactFiles: [],
    filesChanged: [],
  }
}

function renderTasks(tasks: Task[], entry = '/tasks') {
  const state: NabuState = {
    status: null,
    mission: null,
    tasks,
    workspaces: [],
    scopes: [],
    activeScope: { id: 'scope-1', name: 'Northstar', path: '/work/northstar', active: true },
    loading: false,
    refreshing: false,
    error: null,
    refresh: vi.fn().mockResolvedValue(undefined),
    switchScope: vi.fn().mockResolvedValue(undefined),
    clearError: vi.fn(),
  }
  render(<NabuContext.Provider value={state}><MemoryRouter initialEntries={[entry]}><Routes><Route path="/tasks" element={<TasksPage />} /></Routes></MemoryRouter></NabuContext.Provider>)
}

describe('TasksPage sections', () => {
  const scrollIntoView = vi.fn()

  beforeEach(() => {
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 1 })
    vi.stubGlobal('cancelAnimationFrame', vi.fn())
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView })
  })

  afterEach(() => {
    cleanup()
    scrollIntoView.mockClear()
    vi.unstubAllGlobals()
  })

  it('opens and focuses Needs You from its deep link', async () => {
    renderTasks([task('1', 'running'), task('2', 'failed', 'high')], '/tasks#needs-you')

    expect(screen.getByRole('heading', { name: 'Needs You' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Task 2' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Task 1' })).not.toBeInTheDocument()
    expect(screen.queryByText('high')).not.toBeInTheDocument()
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalled())
  })

  it('shows five finished tasks first, then reveals fifteen more', () => {
    renderTasks(Array.from({ length: 21 }, (_, index) => task(String(index + 1), 'completed')))

    fireEvent.click(within(screen.getByRole('navigation', { name: 'Task sections' })).getByRole('button', { name: /Finished/ }))
    expect(screen.getAllByRole('link', { name: /Task \d+/ }).map((link) => link.textContent)).toEqual([
      expect.stringContaining('Task 21'),
      expect.stringContaining('Task 20'),
      expect.stringContaining('Task 19'),
      expect.stringContaining('Task 18'),
      expect.stringContaining('Task 17'),
    ])
    fireEvent.click(screen.getByRole('button', { name: 'Show 15 more' }))
    expect(screen.getAllByRole('link', { name: /Task \d+/ })).toHaveLength(20)
    expect(screen.getByRole('button', { name: 'Show 15 more' })).toBeInTheDocument()
  })
})
