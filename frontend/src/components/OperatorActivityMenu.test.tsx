// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { StatusResponse, Task } from '../types'
import { OperatorActivityMenu } from './OperatorActivityMenu'

const task: Task = {
  id: 'task-1', title: 'Inspect analytics', status: 'running', priority: 'high', definitionOfDone: [],
  verification: [], uncertainties: [], artifacts: [], artifactFiles: [], filesChanged: [],
}

describe('OperatorActivityMenu', () => {
  it('opens a task and Chat activity list from the header status control', () => {
    const status: StatusResponse = {
      status: 'working', paused: false, setupComplete: true, name: 'Nabu', chatQueued: 1,
      activities: [
        { kind: 'task', label: 'Inspect analytics', status: 'running', entityId: 'task-1', detail: 'Autonomous task in progress' },
        { kind: 'chat', label: '1 Chat message queued', status: 'queued' },
      ],
    }
    render(<MemoryRouter><OperatorActivityMenu status={status} tasks={[task]} /></MemoryRouter>)
    fireEvent.click(screen.getByRole('button', { name: 'Open Nabu activity. Working' }))
    expect(screen.getByRole('link', { name: /Inspect analytics/ })).toHaveAttribute('href', '/tasks/task-1')
    expect(screen.getByRole('link', { name: /1 Chat message queued/ })).toHaveAttribute('href', '/chat')
  })

  it('closes and issues a fresh Needs You navigation from the task route', async () => {
    const status: StatusResponse = { status: 'needs_attention', paused: false, setupComplete: true, name: 'Nabu', needsAttention: 2, activities: [] }
    function LocationProbe() {
      const location = useLocation()
      return <span data-testid="location">{location.pathname}{location.hash}:{String(location.state?.attentionRequest ?? '')}</span>
    }
    render(<MemoryRouter initialEntries={['/tasks']}><OperatorActivityMenu status={status} tasks={[]} /><LocationProbe /></MemoryRouter>)

    fireEvent.click(screen.getByRole('button', { name: 'Open Nabu activity. Needs attention' }))
    fireEvent.click(screen.getByRole('button', { name: 'Review 2 items needing follow-up' }))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toMatch(/^\/tasks#needs-you:\d+$/))
    expect(screen.queryByText('Nabu activity')).not.toBeInTheDocument()
  })
})
