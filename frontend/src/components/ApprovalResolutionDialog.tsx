import { Check, LoaderCircle, ShieldAlert, X } from 'lucide-react'
import { useState } from 'react'
import { approvalsApi } from '../features/approvals/api'
import type { Approval } from '../features/approvals/types'
import { InlineError } from './PageState'
import { Button } from './ui/Button'
import { Dialog } from './ui/Dialog'
import { Field, Textarea } from './ui/Field'

export function ApprovalResolutionDialog({ approval, decision, open, onOpenChange, onResolved }: {
  approval: Approval | null
  decision: 'approved' | 'rejected'
  open: boolean
  onOpenChange: (open: boolean) => void
  onResolved: (approval: Approval) => void
}) {
  const [note, setNote] = useState('')
  const [resolving, setResolving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!approval) return null

  const resolve = async () => {
    setResolving(true)
    setError(null)
    try {
      const updated = await approvalsApi.resolve(approval.id, decision, note.trim() || undefined)
      onResolved(updated)
      setNote('')
      onOpenChange(false)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The approval could not be resolved.')
    } finally {
      setResolving(false)
    }
  }

  const approve = decision === 'approved'
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => { if (!resolving) { onOpenChange(next); if (!next) { setNote(''); setError(null) } } }}
      title={approve ? `Approve ${approval.action}?` : `Reject ${approval.action}?`}
      description={approve ? 'Nabu will resume the related task and perform the proposed action.' : 'Nabu will not take this action. Add direction to help it choose a better next step.'}
      footer={<><Button variant="ghost" onClick={() => onOpenChange(false)} disabled={resolving}>Cancel</Button><Button variant={approve ? 'primary' : 'danger'} onClick={() => void resolve()} disabled={resolving}>{resolving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : approve ? <Check className="size-4" /> : <X className="size-4" />}{resolving ? 'Saving…' : approve ? 'Approve action' : 'Reject action'}</Button></>}
    >
      <div className="space-y-4">
        <div className={`flex items-start gap-3 rounded-lg border p-3 ${approve ? 'border-accent/25 bg-accent/5' : 'border-danger/25 bg-danger/5'}`}>
          <ShieldAlert className={`mt-0.5 size-4 shrink-0 ${approve ? 'text-accent' : 'text-danger'}`} />
          <div><p className="text-sm font-medium text-ink">{approval.action}</p><p className="mt-1 text-xs leading-relaxed text-muted">{approval.why}</p></div>
        </div>
        {!approve ? <Field label="Rejection note" hint="Optional"><Textarea value={note} onChange={(event) => setNote(event.target.value)} placeholder="Explain what Nabu should change or do instead…" autoFocus /></Field> : null}
        {error ? <InlineError message={error} /> : null}
      </div>
    </Dialog>
  )
}
