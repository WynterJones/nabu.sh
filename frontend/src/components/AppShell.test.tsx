// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NabuContext, type NabuState } from '../state/NabuContext'
import type { Task } from '../types'
import { AppShell } from './AppShell'

const failedTask: Task = {
  id: 'failed-1', title: 'Needs context', status: 'failed', priority: 'normal', definitionOfDone: [], verification: [], uncertainties: [], artifacts: [], artifactFiles: [], filesChanged: [],
}

afterEach(() => { cleanup(); localStorage.clear(); vi.useRealTimers(); vi.unstubAllGlobals() })

describe('AppShell navigation cues', () => {
  it('starts with Chat and shows live task, date, and report cues', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: true }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path.startsWith('/api/chat/messages')) return new Response(JSON.stringify({ messages: [{ id: 'message-2', role: 'assistant', status: 'complete', content: 'Work finished.' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/settings/operator') return new Response(JSON.stringify({ codex_model: 'gpt-5.6-sol', codex_reasoning_effort: 'high' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/database/datasets') return new Response(JSON.stringify([{ id: 'dataset-1', name: 'Research' }, { id: 'dataset-2', name: 'Pages' }]), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/apps') return new Response(JSON.stringify([{ id: 'app-1', name: 'Preview' }]), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/outputs') return new Response(JSON.stringify({ items: [{ id: 'output-1', name: 'Plan', path: 'PLAN.md' }], scripts: [{ id: 'script-1', name: 'Audit' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/reports') return new Response(JSON.stringify([{ id: 'unread-1', title: 'New report', status: 'unread' }, { id: 'read-1', title: 'Read report', status: 'read' }]), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }))
    const state: NabuState = {
      status: null, mission: null, tasks: [failedTask], workspaces: [], scopes: [], activeScope: { id: 'scope-1', name: 'Northstar', path: '/northstar', active: true }, loading: false, refreshing: false, error: null,
      refresh: vi.fn().mockResolvedValue(undefined), switchScope: vi.fn().mockResolvedValue(undefined), clearError: vi.fn(),
    }
    render(<NabuContext.Provider value={state}><MemoryRouter initialEntries={['/tasks']}><AppShell><div>Page</div></AppShell></MemoryRouter></NabuContext.Provider>)

    fireEvent.click(screen.getByRole('button', { name: 'Open main navigation' }))
    const nav = await screen.findByRole('navigation', { name: 'Primary navigation' })
    const links = within(nav).getAllByRole('link')
    expect(screen.getByRole('link', { name: 'Open Chat' })).toHaveAttribute('href', '/chat')
    expect(within(nav).queryByRole('link', { name: 'Chat' })).not.toBeInTheDocument()
    expect(links[0]).toHaveAttribute('href', '/tasks')
    expect(links[1]).toHaveAttribute('href', '/calendar')
    expect(links[3]).toHaveAttribute('href', '/apps')
    expect(links[5]).toHaveAttribute('href', '/reports')
    expect(screen.getByRole('button', { name: 'Open settings' })).toBeInTheDocument()
    expect(await screen.findByLabelText('Codex model: gpt-5.6-sol')).toHaveTextContent('gpt-5.6-sol')
    expect(screen.getByLabelText('Reasoning level: High')).toHaveTextContent('High')
    expect(await screen.findByLabelText('Nabu is responding')).toBeInTheDocument()
    expect(within(nav).getByLabelText('1 open task')).toBeInTheDocument()
    expect(within(nav).getByTitle(/^Today,/)).toBeInTheDocument()
    await waitFor(() => expect(within(nav).getByLabelText('2 datasets')).toBeInTheDocument())
    expect(within(nav).getByLabelText('1 app')).toBeInTheDocument()
    expect(within(nav).getByLabelText('2 outputs')).toBeInTheDocument()
    await waitFor(() => expect(within(nav).getByLabelText('1 unread report')).toBeInTheDocument())
    expect(screen.getByLabelText('New messages from Nabu')).toBeInTheDocument()
    expect(within(nav).queryByText('Navigate')).not.toBeInTheDocument()
  })

  it('opens quick links to settings sections from the sidebar', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    const state: NabuState = {
      status: null, mission: null, tasks: [], workspaces: [], scopes: [], activeScope: { id: 'scope-1', name: 'Northstar', path: '/northstar', active: true }, loading: false, refreshing: false, error: null,
      refresh: vi.fn().mockResolvedValue(undefined), switchScope: vi.fn().mockResolvedValue(undefined), clearError: vi.fn(),
    }
    render(<NabuContext.Provider value={state}><MemoryRouter><AppShell><div>Page</div></AppShell></MemoryRouter></NabuContext.Provider>)
    fireEvent.click(screen.getByRole('button', { name: 'Open settings' }))
    expect(await screen.findByRole('link', { name: 'MCP connectors' })).toHaveAttribute('href', '/settings/mcp')
    expect(screen.getByRole('link', { name: 'Secrets' })).toHaveAttribute('href', '/settings/secrets')
    expect(screen.getByText('General')).toBeInTheDocument()
    expect(screen.getByText('Workspace')).toBeInTheDocument()
  })

  it('keeps the settings flyout open until it is explicitly dismissed', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    const state: NabuState = {
      status: null, mission: null, tasks: [], workspaces: [], scopes: [], activeScope: { id: 'scope-1', name: 'Northstar', path: '/northstar', active: true }, loading: false, refreshing: false, error: null,
      refresh: vi.fn().mockResolvedValue(undefined), switchScope: vi.fn().mockResolvedValue(undefined), clearError: vi.fn(),
    }
    render(<NabuContext.Provider value={state}><MemoryRouter><AppShell><div>Page</div></AppShell></MemoryRouter></NabuContext.Provider>)
    fireEvent.click(screen.getByRole('button', { name: 'Open settings' }))
    expect(await screen.findByRole('link', { name: 'Operator' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Open settings' }))
    await waitFor(() => expect(screen.queryByRole('link', { name: 'Operator' })).not.toBeInTheDocument())
  })

  it('clears a hover-opened rail menu without restoring a lingering trigger focus ring', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() })))
    const state: NabuState = {
      status: null, mission: null, tasks: [], workspaces: [], scopes: [], activeScope: { id: 'scope-1', name: 'Northstar', path: '/northstar', active: true }, loading: false, refreshing: false, error: null,
      refresh: vi.fn().mockResolvedValue(undefined), switchScope: vi.fn().mockResolvedValue(undefined), clearError: vi.fn(),
    }
    render(<NabuContext.Provider value={state}><MemoryRouter><AppShell><div>Page</div></AppShell></MemoryRouter></NabuContext.Provider>)

    const trigger = screen.getByRole('button', { name: 'Open main navigation' })
    fireEvent.pointerEnter(trigger)
    expect(await screen.findByRole('navigation', { name: 'Primary navigation' })).toBeInTheDocument()
    fireEvent.pointerLeave(trigger)

    await waitFor(() => expect(screen.queryByRole('navigation', { name: 'Primary navigation' })).not.toBeInTheDocument())
    expect(document.activeElement).not.toBe(trigger)
  })
})
