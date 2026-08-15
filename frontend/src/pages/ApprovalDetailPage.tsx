import { ArrowLeft, Check, CheckCircle2, Clock3, FileCheck2, ShieldAlert, X } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApprovalResolutionDialog } from '../components/ApprovalResolutionDialog'
import { Markdown } from '../components/Markdown'
import { InlineError, PageLoading } from '../components/PageState'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Card, EmptyState, SectionHeader } from '../components/ui/Card'
import { approvalsApi } from '../features/approvals/api'
import type { Approval } from '../features/approvals/types'
import { useResource } from '../hooks/useResource'
import { formatRelativeTime } from '../lib/utils'
import { FileLink } from '../components/FileViewer'

export function ApprovalDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const { data: loaded, setData, loading, error } = useResource(() => approvalsApi.get(id), id)
  const [dialog, setDialog] = useState<'approved' | 'rejected' | null>(null)
  const approval = loaded as Approval | null
  if (loading) return <PageLoading label="Loading approval…" />
  if (!approval) return <EmptyState title="Approval not found" description={error ?? 'This approval may no longer be available.'} action={<Button asChild><Link to="/chat">Back to Chat</Link></Button>} />

  return (
    <div className="page-stack max-w-5xl">
      <div><Button asChild variant="secondary" size="sm"><Link to="/chat"><ArrowLeft className="size-4" />Chat</Link></Button></div>
      <div className="page-heading items-start">
        <div className="min-w-0"><div className="mb-3 flex flex-wrap items-center gap-2"><Badge variant={approval.status === 'pending' ? 'warning' : approval.status === 'approved' ? 'success' : approval.status === 'rejected' ? 'danger' : 'default'}>{approval.status}</Badge></div><h1 className="page-title text-balance">{approval.action}</h1><p className="page-description max-w-3xl">{approval.why}</p></div>
        {approval.status === 'pending' ? <div className="flex items-center gap-2"><Button variant="danger" onClick={() => setDialog('rejected')}><X className="size-4" />Reject</Button><Button variant="primary" onClick={() => setDialog('approved')}><Check className="size-4" />Approve</Button></div> : null}
      </div>
      {error ? <InlineError message={error} /> : null}

      <div className="detail-grid">
        <div className="min-w-0 space-y-4">
          <Card className="p-5 shadow-none sm:p-6"><SectionHeader eyebrow="Proposed change" title="What will happen" />{approval.changes.length ? <ul className="mt-4 space-y-2.5">{approval.changes.map((change) => <li key={change} className="flex items-start gap-2.5 text-sm leading-relaxed text-muted"><CheckCircle2 className="mt-0.5 size-4 shrink-0 text-warning" />{change}</li>)}</ul> : <p className="mt-4 text-sm text-muted">No structured change list was provided.</p>}</Card>
          <Card className="p-5 shadow-none sm:p-6"><SectionHeader eyebrow="Evidence" title="Verification and preview" />{approval.evidence.length ? <div className="mt-4 space-y-2">{approval.evidence.map((evidence) => <div key={evidence} className="flex items-start gap-2.5 rounded-lg border border-line bg-canvas p-3 text-sm text-muted"><FileCheck2 className="mt-0.5 size-4 shrink-0 text-accent" /><Markdown>{evidence}</Markdown></div>)}</div> : <p className="mt-4 text-sm text-muted">No additional evidence was attached.</p>}</Card>
          {approval.artifacts.length ? <Card className="p-5 shadow-none sm:p-6"><SectionHeader eyebrow="Artifacts" title="Reviewable output" /><div className="mt-4 grid gap-2 sm:grid-cols-2">{approval.artifacts.map((artifact, index) => artifact.url ? <a key={artifact.id ?? `${artifact.name}-${index}`} href={artifact.url} className="entity-reference" target="_blank" rel="noreferrer"><span className="flex size-8 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><FileCheck2 className="size-4" /></span><span className="min-w-0 flex-1"><span className="eyebrow">{artifact.kind}</span><span className="mt-1 block truncate text-sm text-ink">{artifact.name}</span></span></a> : <div key={artifact.id ?? `${artifact.name}-${index}`} className="entity-reference"><span className="flex size-8 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><FileCheck2 className="size-4" /></span><span className="min-w-0 flex-1"><span className="eyebrow">{artifact.kind}</span><span className="mt-1 block truncate text-sm text-ink">{artifact.path ? <FileLink path={artifact.path}>{artifact.name}</FileLink> : artifact.name}</span></span></div>)}</div></Card> : null}
        </div>
        <Card className="h-fit p-5 shadow-none"><SectionHeader eyebrow="Request details" title="Context" /><dl className="mt-4 space-y-3 text-sm"><Detail label="Status">{approval.status}</Detail><Detail label="Related task">{approval.taskId ? <Link className="text-accent hover:underline" to={`/tasks/${encodeURIComponent(approval.taskId)}`}>{approval.taskTitle ?? 'View task'}</Link> : 'None'}</Detail><Detail label="Requested">{formatRelativeTime(approval.createdAt)}</Detail>{approval.resolvedAt ? <Detail label="Resolved">{formatRelativeTime(approval.resolvedAt)}</Detail> : null}</dl>{approval.resolutionNote ? <div className="mt-4 rounded-lg border border-line bg-canvas p-3"><p className="eyebrow">Resolution note</p><p className="mt-2 text-sm leading-relaxed text-muted">{approval.resolutionNote}</p></div> : null}</Card>
      </div>
      <ApprovalResolutionDialog approval={approval} decision={dialog ?? 'approved'} open={dialog !== null} onOpenChange={(open) => { if (!open) setDialog(null) }} onResolved={setData} />
    </div>
  )
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return <div className="flex items-start justify-between gap-4 border-b border-line/70 pb-3 last:border-0 last:pb-0"><dt className="flex items-center gap-1.5 text-muted">{label === 'Requested' ? <Clock3 className="size-3.5" /> : label === 'Status' ? <ShieldAlert className="size-3.5" /> : null}{label}</dt><dd className="min-w-0 text-right capitalize text-ink">{children}</dd></div>
}
