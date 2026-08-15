// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { Task } from '../types'
import { TaskRow } from './TaskRow'

describe('TaskRow', () => {
  it('keeps grouped task rows concise without repeating path or status', () => {
    const task: Task = {
      id: 'task-1', title: 'Audit production observability', status: 'failed', priority: 'high',
      workspace: '/Users/example/workspaces/business', createdAt: new Date().toISOString(), definitionOfDone: [],
      verification: [], uncertainties: [], artifacts: [], artifactFiles: [], filesChanged: [],
    }
    render(<MemoryRouter><TaskRow task={task} /></MemoryRouter>)
    expect(screen.getByRole('heading', { name: task.title })).toBeInTheDocument()
    expect(screen.queryByText('high')).not.toBeInTheDocument()
    expect(screen.queryByText(task.workspace!)).not.toBeInTheDocument()
    expect(screen.queryByText('Needs follow-up')).not.toBeInTheDocument()
  })
})
