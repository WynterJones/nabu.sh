// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Task } from '../types'
import { TaskDependencyPicker } from './TaskDependencyPicker'

const task = (id: string, title: string): Task => ({
  id,
  title,
  status: 'ready',
  priority: 'normal',
  definitionOfDone: [],
  verification: [],
  uncertainties: [],
  artifacts: [],
  artifactFiles: [],
  filesChanged: [],
})

describe('TaskDependencyPicker', () => {
  it('selects prerequisites and never offers the current task', () => {
    const onChange = vi.fn()
    render(<TaskDependencyPicker tasks={[task('task-current', 'Current task'), task('task-research', 'Gather evidence')]} selectedIds={[]} excludeTaskId="task-current" onChange={onChange} />)

    fireEvent.click(screen.getByRole('button', { name: 'No prerequisites' }))
    expect(screen.queryByRole('checkbox', { name: /Current task/ })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('checkbox', { name: /Gather evidence/ }))

    expect(onChange).toHaveBeenCalledWith(['task-research'])
  })
})
