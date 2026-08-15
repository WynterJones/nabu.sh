// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NabuContext, type NabuState } from '../state/NabuContext'
import { ChatPage } from './ChatPage'

class MockEventSource {
  static instances: MockEventSource[] = []
  onopen: ((event: Event) => void) | null = null
  private listeners = new Map<string, Set<EventListener>>()

  constructor() {
    MockEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: EventListener) {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>()
    listeners.add(listener)
    this.listeners.set(type, listeners)
  }

  removeEventListener(type: string, listener: EventListener) {
    this.listeners.get(type)?.delete(listener)
  }

  emit(type: string, payload: unknown) {
    const event = new MessageEvent(type, { data: JSON.stringify(payload) })
    this.listeners.get(type)?.forEach((listener) => listener(event))
  }

  close() {}
}

const state: NabuState = {
  status: null,
  mission: null,
  tasks: [],
  workspaces: [],
  scopes: [],
  activeScope: { id: 'scope-1', name: 'Acme', path: '/acme', active: true },
  loading: false,
  refreshing: false,
  error: null,
  refresh: async () => undefined,
  switchScope: async () => undefined,
  clearError: () => undefined,
}

beforeEach(() => {
  MockEventSource.instances = []
  vi.stubGlobal('EventSource', MockEventSource)
  HTMLElement.prototype.scrollTo = vi.fn()
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('ChatPage message deletion', () => {
  it('does not let an older history response erase a newer live message', async () => {
    let resolveHistory: (response: Response) => void = () => undefined
    const history = new Promise<Response>((resolve) => { resolveHistory = resolve })
    vi.stubGlobal('fetch', vi.fn((path: string) => path === '/api/chat/status'
      ? Promise.resolve(new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      : history))
    render(<MemoryRouter><NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider></MemoryRouter>)

    await waitFor(() => expect(MockEventSource.instances).toHaveLength(1))
    MockEventSource.instances[0].emit('chat.message', {
      workspace_id: 'scope-1',
      data: { id: '2', workspace_id: 'scope-1', role: 'assistant', content: 'Newest live answer', status: 'complete' },
    })
    expect(await screen.findByText('Newest live answer')).toBeInTheDocument()

    resolveHistory(new Response(JSON.stringify({ messages: [{ id: '1', role: 'user', content: 'Earlier request', status: 'complete' }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Earlier request')).toBeInTheDocument()
    expect(screen.getByText('Newest live answer')).toBeInTheDocument()
  })

  it('catches up durable messages after the event stream reconnects', async () => {
    let latest = [{ id: '1', role: 'user', content: 'Earlier request', status: 'complete' }]
    const fetchMock = vi.fn(async (path: string) => path === '/api/chat/status'
      ? new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      : new Response(JSON.stringify({ messages: latest, has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider></MemoryRouter>)

    expect(await screen.findByText('Earlier request')).toBeInTheDocument()
    latest = [...latest, { id: '2', role: 'assistant', content: 'Recovered after reconnect', status: 'complete' }]
    MockEventSource.instances[0].onopen?.(new Event('open'))

    expect(await screen.findByText('Recovered after reconnect')).toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(([path]) => path === '/api/chat/messages?limit=10')).toHaveLength(2)
  })

  it('focuses the main prompt when Chat opens', async () => {
    vi.stubGlobal('fetch', vi.fn(async (path: string) => path === '/api/chat/status'
      ? new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      : new Response(JSON.stringify({ messages: [], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    render(<MemoryRouter><NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider></MemoryRouter>)

    expect(await screen.findByRole('textbox', { name: 'Message Nabu' })).toHaveFocus()
  })

  it('warns that deleting a root permanently removes every reply', async () => {
    const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method === 'DELETE') return new Response(null, { status: 204 })
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [{ id: 'root-1', role: 'user', content: 'Investigate the launch.', status: 'complete', reply_count: 2 }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider>)

    expect(screen.queryByRole('button', { name: 'Reply in thread' })).not.toBeInTheDocument()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete your message' }))
    expect(await screen.findByRole('heading', { name: 'Delete message and thread?' })).toBeInTheDocument()
    expect(screen.getByText(/permanently delete this message and all 2 replies/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Delete message and thread' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/chat/messages/root-1', expect.objectContaining({ method: 'DELETE' })))
  })

  it('accepts another message while Nabu is working and renders it queued', async () => {
    const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: true, queued: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/chat/messages' && init?.method === 'POST') return new Response(JSON.stringify({ message: { id: 'queued-1', role: 'user', content: 'Look at the repos next.', status: 'queued' } }), { status: 202, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [{ id: 'current-1', role: 'user', content: 'Current request', status: 'processing' }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider>)

    const composer = await screen.findByRole('textbox', { name: 'Message Nabu' })
    fireEvent.change(composer, { target: { value: 'Look at the repos next.' } })
    fireEvent.keyDown(composer, { key: 'Enter', shiftKey: false })
    expect(await screen.findByText('Queued')).toBeInTheDocument()
    expect(screen.getByText('Nabu is replying · new messages will be queued')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/chat/messages', expect.objectContaining({ method: 'POST' }))
  })

  it('explains when a queued message is waiting for Codex instead of claiming Nabu is working', async () => {
    const fetchMock = vi.fn(async (path: string) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: false, queued: 1 }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [{ id: 'queued-1', role: 'user', content: 'Check analytics.', status: 'queued' }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    const waitingState = { ...state, status: { status: 'needs_attention' as const, paused: false, setupComplete: true, name: 'Nabu', codexState: 'rate_limited', retryAt: '2026-08-13T01:39:23Z', activities: [], chatQueued: 1 } }
    render(<NabuContext.Provider value={waitingState}><ChatPage /></NabuContext.Provider>)

    expect(await screen.findByText(/Waiting for Codex · 1 message safely queued/)).toBeInTheDocument()
    expect(screen.queryByText(/Nabu is working/)).not.toBeInTheDocument()
  })

  it('shows one Thinking state on Nabu and no duplicate processing labels', async () => {
    const fetchMock = vi.fn(async (path: string) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: true }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [
        { id: 'active-user', role: 'user', content: 'It is ready now.', status: 'processing' },
        { id: 'active-assistant', role: 'assistant', content: '', status: 'streaming', activity: [{ id: 'activity-1', label: 'Inspecting workspace' }] },
      ], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider></MemoryRouter>)

    expect(await screen.findByText('Thinking')).toBeInTheDocument()
    expect(screen.getAllByText('Thinking')).toHaveLength(1)
    expect(screen.queryByText('Processing')).not.toBeInTheDocument()
    expect(screen.queryByText('Writing')).not.toBeInTheDocument()
    expect(screen.queryByText('Thinking…')).not.toBeInTheDocument()
    expect(screen.queryByText('Inspecting workspace')).not.toBeInTheDocument()
  })

  it('follows the conversation when the Thinking row is added after sending', async () => {
    const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/chat/messages' && init?.method === 'POST') return new Response(JSON.stringify({ message: { id: 'active-user', role: 'user', content: 'Inspect the workspace.', status: 'processing' } }), { status: 202, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider>)

    const composer = await screen.findByRole('textbox', { name: 'Message Nabu' })
    vi.mocked(HTMLElement.prototype.scrollTo).mockClear()
    fireEvent.change(composer, { target: { value: 'Inspect the workspace.' } })
    fireEvent.keyDown(composer, { key: 'Enter', shiftKey: false })

    expect(await screen.findByText('Thinking')).toBeInTheDocument()
    await waitFor(() => expect(HTMLElement.prototype.scrollTo).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'smooth' })))
  })

  it('restores the Thinking row when navigation misses the transient started event', async () => {
    const fetchMock = vi.fn(async (path: string) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: true }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [
        { id: 'recovery-1', role: 'user', content: 'The connection is ready now.', status: 'processing', effect_metadata: { recovery_task_id: 'task-1', recovery_task_title: 'Repair the integration' } },
      ], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider></MemoryRouter>)

    expect(await screen.findByText('Thinking')).toBeInTheDocument()
    expect(screen.getAllByText('Nabu')).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'Open task: Repair the integration' })).toHaveAttribute('href', '/tasks/task-1')
    expect(screen.getByText('The connection is ready now.')).toBeInTheDocument()
    expect(screen.queryByText(/New context:/)).not.toBeInTheDocument()
  })

  it('opens a non-modal thread pane while the main conversation remains interactive', async () => {
    const fetchMock = vi.fn(async (path: string) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/chat/messages/root-1/thread?limit=20') return new Response(JSON.stringify({ root: { id: 'root-1', role: 'assistant', content: 'Root answer', status: 'complete', reply_count: 0 }, messages: [], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [{ id: 'root-1', role: 'assistant', content: 'Root answer', status: 'complete', reply_count: 0 }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider>)

    fireEvent.click(await screen.findByRole('button', { name: 'Reply in thread' }))
    expect(await screen.findByRole('complementary', { name: 'Thread' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'Thread' })).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Message Nabu' })).toBeEnabled()
    expect(screen.getByRole('textbox', { name: 'Reply in thread' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Close thread' }))
    expect(screen.queryByRole('complementary', { name: 'Thread' })).not.toBeInTheDocument()
  })

  it('submits an explicit owner response from an interactive choice card', async () => {
    const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/chat/messages' && init?.method === 'POST') return new Response(JSON.stringify({ message: { id: 'accepted-choice', role: 'user', content: 'Inspect the marketing repository first.', status: 'queued' } }), { status: 202, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [{ id: 'choice-card', role: 'assistant', content: 'Choose a starting point.', status: 'complete', effects: [{ type: 'request_choice', summary: 'Which repository first?', actions: [{ label: 'Marketing site', value: 'Inspect the marketing repository first.', primary: true }, { label: 'Application', value: 'Inspect the application repository first.' }] }] }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider>)

    fireEvent.click(await screen.findByRole('button', { name: 'Marketing site' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/chat/messages', expect.objectContaining({ method: 'POST', body: JSON.stringify({ content: 'Inspect the marketing repository first.' }) })))
  })

  it('upgrades an earlier context-ready question with an approval action', async () => {
    const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === '/api/chat/status') return new Response(JSON.stringify({ working: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/chat/messages' && init?.method === 'POST') return new Response(JSON.stringify({ message: { id: 'approved-context', role: 'user', content: 'I approve this workspace context. Approve and begin the work now.', status: 'queued' } }), { status: 202, headers: { 'Content-Type': 'application/json' } })
      return new Response(JSON.stringify({ messages: [{ id: 'old-context-question', role: 'assistant', content: 'I have enough context to begin once you confirm: should I treat the workspace context as ready?', status: 'complete' }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<NabuContext.Provider value={state}><ChatPage /></NabuContext.Provider>)

    fireEvent.click(await screen.findByRole('button', { name: 'Approve and begin' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/chat/messages', expect.objectContaining({ method: 'POST', body: JSON.stringify({ content: 'I approve this workspace context. Approve and begin the work now.' }) })))
  })
})
