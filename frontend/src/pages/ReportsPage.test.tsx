// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NabuContext, type NabuState } from '../state/NabuContext'
import { ReportDetailPage, ReportsPage } from './ReportsPage'

const state: NabuState = {
  status: null, mission: null, tasks: [], workspaces: [], scopes: [],
  activeScope: { id: 'scope-1', name: 'Northstar', path: '/northstar', active: true },
  loading: false, refreshing: false, error: null,
  refresh: vi.fn().mockResolvedValue(undefined), switchScope: vi.fn().mockResolvedValue(undefined), clearError: vi.fn(),
}

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

describe('Reports sections and lifecycle', () => {
  it('shows one settings-style section at a time', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json([
      { id: 'new', title: 'New findings', summary: 'Unread', status: 'unread' },
      { id: 'seen', title: 'Reviewed findings', summary: 'Read', status: 'read' },
      { id: 'old', title: 'Archived findings', summary: 'Archived', status: 'archived' },
    ])))
    render(<NabuContext.Provider value={state}><MemoryRouter><ReportsPage /></MemoryRouter></NabuContext.Provider>)

    expect(await screen.findByText('New findings')).toBeInTheDocument()
    expect(screen.queryByText('Reviewed findings')).not.toBeInTheDocument()
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Report sections' })).getByRole('button', { name: /Read/ }))
    expect(screen.getByText('Reviewed findings')).toBeInTheDocument()
    expect(screen.queryByText('New findings')).not.toBeInTheDocument()
  })

  it('marks unread reports read and permanently deletes through confirmation', async () => {
    const report = { id: 'report-1', title: 'Launch findings', summary: 'Summary', body: 'Durable body', status: 'unread', created_at: '2026-08-12T10:00:00Z' }
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'DELETE') return new Response(null, { status: 204 })
      if (init?.method === 'PATCH') return json({ ...report, status: 'read', read_at: '2026-08-12T12:00:00Z' })
      return json(report)
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter initialEntries={['/reports/report-1']}><Routes><Route path="/reports/:id" element={<ReportDetailPage />} /><Route path="/reports" element={<div>Report library</div>} /></Routes></MemoryRouter>)

    fireEvent.click(await screen.findByRole('button', { name: 'Mark as read' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/reports/report-1', expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ status: 'read' }) })))
    expect(screen.queryByRole('button', { name: 'Mark as read' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Delete report' }))
    expect(screen.getByRole('heading', { name: 'Delete this report?' })).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete report' }).at(-1)!)
    expect(await screen.findByText('Report library')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/reports/report-1', expect.objectContaining({ method: 'DELETE' }))
  })
})
