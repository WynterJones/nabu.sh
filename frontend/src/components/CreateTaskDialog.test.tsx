// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NabuContext, type NabuState } from '../state/NabuContext'
import { CreateTaskDialog } from './CreateTaskDialog'

afterEach(() => vi.unstubAllGlobals())

const state: NabuState = {
  status: null,
  mission: null,
  tasks: [],
  workspaces: [],
  scopes: [{ id: 'scope-1', name: 'Wynter.ai', path: '/workspace', active: true }],
  activeScope: { id: 'scope-1', name: 'Wynter.ai', path: '/workspace', active: true },
  loading: false,
  refreshing: false,
  error: null,
  refresh: vi.fn().mockResolvedValue(undefined),
  switchScope: vi.fn().mockResolvedValue(undefined),
  clearError: vi.fn(),
}

describe('CreateTaskDialog', () => {
  it('turns plain-language intent into an editable task draft', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      title: 'Investigate signup conversion', purpose: 'Find the source of the conversion drop.', why: 'Paid adoption depends on signup health.', priority: 'high', definition_of_done: ['Root cause documented', 'Recommendation verified'],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<NabuContext.Provider value={state}><CreateTaskDialog open onOpenChange={vi.fn()} /></NabuContext.Provider>)
    fireEvent.change(screen.getByLabelText(/What should Nabu work on/), { target: { value: 'Find out why signup conversion dropped.' } })
    fireEvent.click(screen.getByRole('button', { name: 'Draft task' }))
    await waitFor(() => expect(screen.getByDisplayValue('Investigate signup conversion')).toBeInTheDocument())
    expect(screen.getByText('Review drafted task')).toBeInTheDocument()
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument()
    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body))).toEqual({ request: 'Find out why signup conversion dropped.', priority: 'normal' })
  })
})
