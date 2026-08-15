import { AlertTriangle, LoaderCircle } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from './ui/Button'
import { Dialog } from './ui/Dialog'

export function ConfirmDialog({ open, onOpenChange, title, description, details, confirmLabel, destructive = false, pending = false, onConfirm }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  details?: ReactNode
  confirmLabel: string
  destructive?: boolean
  pending?: boolean
  onConfirm: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={title} description={description} footer={<><Button variant="ghost" onClick={() => onOpenChange(false)} disabled={pending}>Cancel</Button><Button variant={destructive ? 'danger' : 'primary'} onClick={onConfirm} disabled={pending}>{pending ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <AlertTriangle className="size-4" />}{pending ? 'Working…' : confirmLabel}</Button></>}>
      {details ? <div className="rounded-lg border border-line bg-canvas p-3 text-sm leading-relaxed text-muted">{details}</div> : null}
    </Dialog>
  )
}
