// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { IntegrationsSettingsPage } from './IntegrationsSettingsPage'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('SecretsSettingsPage', () => {
  it('presents saved values as script environment inputs without a closed integration catalogue', async () => {
    const fetchMock = vi.fn(async () => json([]))
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><IntegrationsSettingsPage /></MemoryRouter>)

    expect(await screen.findByRole('heading', { name: 'Secrets' })).toBeInTheDocument()
    expect(screen.getByText(/Secrets become environment variables/)).toBeInTheDocument()
    expect(screen.queryByText('Tested integrations')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Ask Nabu to build a script/ })).toHaveAttribute('href', '/chat')
  })

  it('saves a reusable secret without ever rendering its value', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/secrets' && init?.method === 'POST') return json({ id: 'secret-1', name: 'Deploy token', description: 'Production deploy access', value: 'must-not-render' })
      if (url === '/api/secrets') return json([])
      return json({}, 404)
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><IntegrationsSettingsPage /></MemoryRouter>)

    fireEvent.click((await screen.findAllByRole('button', { name: 'Add secret' }))[0])
    fireEvent.change(await screen.findByLabelText(/Name/), { target: { value: 'Deploy token' } })
    fireEvent.change(screen.getByLabelText(/Description/), { target: { value: 'Production deploy access' } })
    fireEvent.change(screen.getByLabelText(/Secret value/), { target: { value: 'super-secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save secret' }))

    expect(await screen.findByText('Deploy token')).toBeInTheDocument()
    expect(screen.getByText('Value hidden')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('super-secret')).not.toBeInTheDocument()
    expect(screen.queryByText('must-not-render')).not.toBeInTheDocument()
  })
})
