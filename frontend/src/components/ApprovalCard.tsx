import { ArrowUpRight, Check, ShieldAlert, X } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { Approval } from '../features/approvals/types'
import { formatRelativeTime } from '../lib/utils'
import { Badge } from './ui/Badge'
import { Button } from './ui/Button'
import { Card } from './ui/Card'

export function ApprovalCard({ approval, onApprove, onReject, resolving = false, compact = false }: {
  approval: Approval
  onApprove?: () => void
  onReject?: () => void
  resolving?: boolean
  compact?: boolean
}) {
  return (
    <Card className="approval-card">
      <div className="flex items-start gap-3">
        <span className="approval-card-icon"><ShieldAlert className="size-4" /></span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0"><p className="eyebrow text-warning">Approval requested</p><h2 className="mt-1 text-pretty text-base font-semibold text-ink">{approval.action}</h2></div>
            <Badge variant="warning">{approval.status}</Badge>
          </div>
          <p className="mt-1.5 max-w-3xl text-pretty text-xs leading-relaxed text-muted">{approval.why || 'Nabu needs permission before taking this action.'}</p>
          {!compact && approval.changes.length ? <ul className="mt-3 space-y-1 text-xs text-muted">{approval.changes.slice(0, 3).map((change) => <li key={change} className="flex items-start gap-2"><span className="mt-1.5 size-1 rounded-full bg-warning" />{change}</li>)}</ul> : null}
          <div className="approval-card-actions">
            <Button asChild variant="secondary" size="sm"><Link to={`/approvals/${encodeURIComponent(approval.id)}`}>Review details<ArrowUpRight className="size-3.5" /></Link></Button>
            {approval.status === 'pending' && onReject ? <Button variant="danger" size="sm" onClick={onReject} disabled={resolving}><X className="size-3.5" />Reject</Button> : null}
            {approval.status === 'pending' && onApprove ? <Button variant="primary" size="sm" onClick={onApprove} disabled={resolving}><Check className="size-3.5" />Approve</Button> : null}
            {approval.createdAt ? <span className="ml-auto text-[11px] text-muted">Requested {formatRelativeTime(approval.createdAt)}</span> : null}
          </div>
        </div>
      </div>
    </Card>
  )
}
