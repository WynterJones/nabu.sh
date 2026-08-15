import {
  ArrowUpRight,
  BookOpenText,
  CalendarClock,
  CheckSquare2,
  Database,
  Feather,
  FileArchive,
  FileText,
  FolderGit2,
  KeyRound,
  ListChecks,
  PanelsTopLeft,
  ShieldAlert,
  ShieldCheck,
  Target,
  TerminalSquare,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import type { EntityRef } from '../features/shared/types'
import { cn } from '../lib/utils'
import { ChatActionCard } from './ChatActionCard'
import { SecretProvisionCard } from './SecretProvisionCard'

interface ReferenceConfig {
  icon: typeof FileText
  label: string
  href: (reference: EntityRef) => string
}

const detailHref = (path: string) => (reference: EntityRef) =>
  `${path}/${encodeURIComponent(reference.id)}`

const config: Record<string, ReferenceConfig> = {
  task: { icon: CheckSquare2, label: 'Task', href: detailHref('/tasks') },
  report: { icon: FileText, label: 'Report', href: detailHref('/reports') },
  approval: { icon: ShieldAlert, label: 'Approval', href: detailHref('/approvals') },
  run: { icon: TerminalSquare, label: 'Run', href: detailHref('/runs') },
  memory: { icon: BookOpenText, label: 'Memory', href: () => '/settings/memory' },
  soul: { icon: Feather, label: 'Soul', href: () => '/settings/soul' },
  context: { icon: FolderGit2, label: 'Context', href: () => '/settings/memory' },
  mission: { icon: Target, label: 'Mission', href: () => '/settings/workspaces' },
  policy: { icon: ShieldCheck, label: 'Policy', href: () => '/settings/policy' },
  plan: { icon: ListChecks, label: 'Plan', href: () => '/calendar' },
  schedule: { icon: CalendarClock, label: 'Schedule', href: () => '/settings/schedules' },
  integration: { icon: KeyRound, label: 'Secret access', href: () => '/settings/secrets' },
  dataset: { icon: Database, label: 'Dataset', href: detailHref('/database') },
  app: { icon: PanelsTopLeft, label: 'App', href: () => '/apps' },
}

const staticConfig: Record<string, Pick<ReferenceConfig, 'icon' | 'label'>> = {
  artifact: { icon: FileArchive, label: 'Artifact' },
}

export function EntityReferenceCard({ reference, compact = false, fluid = false, showStatus = true, onMessage }: { reference: EntityRef; compact?: boolean; fluid?: boolean; showStatus?: boolean; onMessage?: (message: string) => void }) {
  if (reference.type === 'secret') return <SecretProvisionCard secret={reference} onReady={(name) => onMessage?.(`${name} is now saved securely. Continue with the work that needed it.`)} />
  if (reference.type === 'context_approval') return <ChatActionCard title="Context ready for approval" description="Nabu believes it has enough mission and workspace context to begin autonomous work." actions={[{ label: 'Approve and begin', value: 'I approve this workspace context. Approve and begin the work now.', primary: true }]} onAction={(action) => onMessage?.(action.value)} />
  const selected = config[reference.type]
  const staticReference = staticConfig[reference.type]
  const cardClassName = cn('entity-reference', compact && 'entity-reference-compact', fluid && 'entity-reference-fluid')
  if (!selected) {
    const StaticIcon = staticReference?.icon ?? FileText
    return (
      <div className={cardClassName}>
        <span className="entity-reference-icon"><StaticIcon className="size-4" /></span>
        <span className="min-w-0">
          <span className="entity-reference-header"><span className="entity-reference-label">{staticReference?.label ?? reference.type.replaceAll('_', ' ')}</span>{showStatus && reference.status ? <span className="entity-reference-status">{reference.status.replaceAll('_', ' ')}</span> : null}</span>
          <span className="entity-reference-title">{reference.title}</span>
          {!compact && reference.summary ? <span className="mt-1 line-clamp-2 block text-xs leading-relaxed text-muted">{reference.summary}</span> : null}
        </span>
        <span aria-hidden="true" />
      </div>
    )
  }
  const Icon = selected.icon
  return (
    <Link to={selected.href(reference)} className={cardClassName}>
      <span className="entity-reference-icon"><Icon className="size-4" /></span>
      <span className="min-w-0">
        <span className="entity-reference-header"><span className="entity-reference-label">{selected.label}</span>{showStatus && reference.status ? <span className="entity-reference-status">{reference.status.replaceAll('_', ' ')}</span> : null}</span>
        <span className="entity-reference-title">{reference.title}</span>
        {!compact && reference.summary ? <span className="mt-1 line-clamp-2 block text-xs leading-relaxed text-muted">{reference.summary}</span> : null}
      </span>
      <ArrowUpRight className="entity-reference-arrow" />
    </Link>
  )
}
