// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { NabuContext, type NabuState } from '../../state/NabuContext'
import { WorkspacesSettingsPage } from './WorkspacesSettingsPage'

const state: NabuState = {
  status: null,
  mission: null,
  tasks: [],
  workspaces: [],
  scopes: [{ id: 'scope-1', name: 'Existing', path: '/work/existing', active: true }],
  activeScope: { id: 'scope-1', name: 'Existing', path: '/work/existing', active: true },
  loading: false,
  refreshing: false,
  error: null,
  refresh: async () => undefined,
  switchScope: async () => undefined,
  clearError: () => undefined,
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('WorkspacesSettingsPage dialog', () => {
  it('keeps the same dialog and input focused while typing', () => {
    render(<MemoryRouter><NabuContext.Provider value={state}><WorkspacesSettingsPage /></NabuContext.Provider></MemoryRouter>)
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace' }))
    const dialog = screen.getByRole('dialog')
    const input = screen.getByRole('textbox', { name: /^Workspace name/ })
    input.focus()
    fireEvent.change(input, { target: { value: 'Nabu Test Lab' } })
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(screen.getByRole('textbox', { name: /^Workspace name/ })).toBe(input)
    expect(input).toHaveFocus()
    expect(input).toHaveValue('Nabu Test Lab')
  })

  it('uses the native folder chooser without resetting the form', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ path: '/Users/test/Nabu Lab/' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><NabuContext.Provider value={state}><WorkspacesSettingsPage /></NabuContext.Provider></MemoryRouter>)
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace' }))
    fireEvent.change(screen.getByRole('textbox', { name: /^Workspace name/ }), { target: { value: 'Nabu Lab' } })
    fireEvent.click(screen.getByRole('button', { name: 'Choose folder' }))
    await waitFor(() => expect(screen.getByRole('textbox', { name: /^Absolute path/ })).toHaveValue('/Users/test/Nabu Lab/'))
    expect(screen.getByRole('textbox', { name: /^Workspace name/ })).toHaveValue('Nabu Lab')
    expect(fetchMock).toHaveBeenCalledWith('/api/setup/browse', expect.objectContaining({ method: 'POST' }))
  })

  it('requires the exact workspace name before deleting Nabu data', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ deleted_workspace_id: 'scope-1', folder_preserved: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><NabuContext.Provider value={state}><WorkspacesSettingsPage /></NabuContext.Provider></MemoryRouter>)

    fireEvent.click(screen.getByRole('button', { name: 'Delete Existing' }))
    expect(screen.getByText('Your folder stays on this computer')).toBeInTheDocument()
    const remove = screen.getByRole('button', { name: 'Delete workspace' })
    expect(remove).toBeDisabled()

    fireEvent.change(screen.getByRole('textbox', { name: /^Type Existing to confirm/ }), { target: { value: 'Existing' } })
    expect(remove).toBeEnabled()
    fireEvent.click(remove)

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/scopes/scope-1', expect.objectContaining({
      method: 'DELETE',
      body: JSON.stringify({ confirmation: 'Existing' }),
    })))
  })
})
