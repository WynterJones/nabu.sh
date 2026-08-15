import { FileText, MessageSquareText, Settings } from 'lucide-react'
import { Card, EmptyState } from '../components/ui/Card'
import { useNabu } from '../state/NabuContext'

export function ReportsPage() {
  return <LaterPage eyebrow="Reference library" title="Reports" icon={<FileText className="size-5" />} description="Mission reports, investigations, and other durable outputs will live here in a later phase. Completed task evidence is available now from Tasks." />
}

export function ChatPage() {
  return <LaterPage eyebrow="Steering" title="Chat" icon={<MessageSquareText className="size-5" />} description="A durable steering conversation arrives in the next phase. For now, edit the mission or create a focused task to direct Nabu." />
}

export function SettingsPage() {
  const { status, workspaces } = useNabu()
  return (
    <div className="page-stack max-w-4xl">
      <div><h1 className="page-title">Settings</h1><p className="page-description">A compact view of the current installation. Policy editing arrives with approvals.</p></div>
      <Card className="p-5 shadow-none sm:p-6">
        <dl className="summary-list">
          <div className="summary-row"><dt>Display name</dt><dd>{status?.name ?? 'Nabu'}</dd></div>
          <div className="summary-row"><dt>Codex CLI</dt><dd>{status?.codexAvailable === false ? 'Unavailable' : 'Connected'}</dd></div>
          <div className="summary-row"><dt>Workspaces</dt><dd>{workspaces.length}</dd></div>
          <div className="summary-row"><dt>Version</dt><dd className="font-mono text-xs">{status?.version ?? 'development'}</dd></div>
        </dl>
      </Card>
      <LaterPage eyebrow="Policy and service" title="More settings are coming" icon={<Settings className="size-5" />} description="Approval rules, schedules, and service diagnostics will be configurable in later phases." nested />
    </div>
  )
}

function LaterPage({ eyebrow, title, icon, description, nested = false }: { eyebrow: string; title: string; icon: React.ReactNode; description: string; nested?: boolean }) {
  const content = <EmptyState icon={icon} title="Planned for a later phase" description={description} />
  if (nested) return content
  return <div className="page-stack max-w-4xl"><div><p className="eyebrow">{eyebrow}</p><h1 className="page-title">{title}</h1></div>{content}</div>
}
