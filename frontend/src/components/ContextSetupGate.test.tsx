// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { NabuContext, type NabuState } from '../state/NabuContext'
import { ContextSetupGate } from './ContextSetupGate'

const state: NabuState = {
  status: null, mission: null, tasks: [], workspaces: [], scopes: [],
  activeScope: { id: 'scope-1', name: 'Acme', path: '/acme', active: true, contextReady: false },
  loading: false, refreshing: false, error: null,
  refresh: async () => undefined, switchScope: async () => undefined, clearError: () => undefined,
}

afterEach(cleanup)

describe('ContextSetupGate', () => {
  it('blocks work surfaces and sends the owner back to Chat while context is incomplete', () => {
    render(<MemoryRouter><NabuContext.Provider value={state}><ContextSetupGate><button type="button">Create task</button></ContextSetupGate></NabuContext.Provider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Set up this workspace with Nabu' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Continue setup in Chat/ })).toHaveAttribute('href', '/chat')
    expect(screen.queryByRole('button', { name: 'Create task' })).not.toBeInTheDocument()
  })
})
