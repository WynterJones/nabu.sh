// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Approval } from '../features/approvals/types'
import { ApprovalResolutionDialog } from './ApprovalResolutionDialog'

const approval: Approval = { id: 'approval-1', action: 'Deploy preview', why: 'The verified preview is ready.', status: 'pending', changes: [], evidence: [], artifacts: [] }

afterEach(() => vi.unstubAllGlobals())

describe('ApprovalResolutionDialog', () => {
  it('sends an optional rejection note and returns the resolved approval', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...approval, status: 'rejected', resolution_note: 'Revise the headline.' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const onResolved = vi.fn()
    render(<ApprovalResolutionDialog approval={approval} decision="rejected" open onOpenChange={vi.fn()} onResolved={onResolved} />)
    fireEvent.change(screen.getByLabelText(/Rejection note/), { target: { value: 'Revise the headline.' } })
    fireEvent.click(screen.getByRole('button', { name: 'Reject action' }))
    await waitFor(() => expect(onResolved).toHaveBeenCalledWith(expect.objectContaining({ status: 'rejected', resolutionNote: 'Revise the headline.' })))
    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body))).toEqual({ decision: 'rejected', rejection_note: 'Revise the headline.' })
  })
})
