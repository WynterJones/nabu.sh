// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { EntityReferenceCard } from './EntityReferenceCard'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

describe('EntityReferenceCard', () => {
  it('routes memory updates to durable Memory instead of a task detail', () => {
    render(
      <MemoryRouter>
        <EntityReferenceCard reference={{ id: 'memory-1', type: 'memory', title: 'Memory update', status: 'applied' }} />
      </MemoryRouter>,
    )

    expect(screen.getByRole('link')).toHaveAttribute('href', '/settings/memory')
    expect(screen.getByText('Memory')).toBeInTheDocument()
    expect(screen.getByText('Memory update')).toBeInTheDocument()
  })

  it('keeps genuine task references linked to their task detail', () => {
    render(
      <MemoryRouter>
        <EntityReferenceCard reference={{ id: 'task/1', type: 'task', title: 'Review traffic' }} />
      </MemoryRouter>,
    )

    expect(screen.getByRole('link')).toHaveAttribute('href', '/tasks/task%2F1')
  })

  it('does not turn an unknown entity type into a broken task link', () => {
    render(
      <MemoryRouter>
        <EntityReferenceCard reference={{ id: 'future-1', type: 'future_entity', title: 'Future entity' }} />
      </MemoryRouter>,
    )

    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByText('future entity')).toBeInTheDocument()
  })

  it('renders lifecycle artifacts as distinct stable reference cards', () => {
    render(
      <MemoryRouter>
        <EntityReferenceCard reference={{ id: 'artifact-1', type: 'artifact', title: 'Competitor matrix' }} />
      </MemoryRouter>,
    )

    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByText('Artifact')).toBeInTheDocument()
    expect(screen.getByText('Competitor matrix')).toBeInTheDocument()
  })

  it('routes legacy integration references to the simplified secrets screen', () => {
    render(
      <MemoryRouter>
        <EntityReferenceCard reference={{ id: 'adapter-1', type: 'integration', title: 'Plausible', status: 'ready' }} />
      </MemoryRouter>,
    )

    expect(screen.getByRole('link')).toHaveAttribute('href', '/settings/secrets')
  })

  it('renders context approval as a first-class action card', () => {
    const onMessage = vi.fn()
    render(<MemoryRouter><EntityReferenceCard reference={{ id: 'scope-1', type: 'context_approval', title: 'Workspace context', status: 'pending' }} onMessage={onMessage} /></MemoryRouter>)
    expect(screen.getByRole('button', { name: 'Approve and begin' })).toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    screen.getByRole('button', { name: 'Approve and begin' }).click()
    expect(onMessage).toHaveBeenCalledWith('I approve this workspace context. Approve and begin the work now.')
  })

  it('saves a requested secret directly to the protected endpoint', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ secret: { id: 'deploy-token', name: 'Deploy token', configured: true } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<MemoryRouter><EntityReferenceCard reference={{ id: 'deploy-token', type: 'secret', title: 'Deploy token', status: 'needs_value' }} /></MemoryRouter>)

    fireEvent.change(screen.getByLabelText('Deploy token'), { target: { value: 'protected-value' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/secrets/deploy-token', expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ value: 'protected-value' }) })))
    expect(await screen.findByText(/is saved securely/)).toBeInTheDocument()
    expect(screen.queryByDisplayValue('protected-value')).not.toBeInTheDocument()
  })
})
