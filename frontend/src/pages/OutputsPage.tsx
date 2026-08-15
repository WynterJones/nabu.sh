import {
  ArrowUpRight,
  FileArchive,
  FileCode2,
  FileText,
  Globe2,
  Image as ImageIcon,
  LoaderCircle,
  PackageOpen,
  Play,
  Video,
  Wrench,
} from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useFileViewer } from '../components/FileViewer'
import { InlineError, PageLoading } from '../components/PageState'
import { Button } from '../components/ui/Button'
import { Card, EmptyState } from '../components/ui/Card'
import { outputsApi } from '../features/outputs/api'
import type { WorkspaceOutput } from '../features/outputs/types'
import { settingsApi } from '../features/settings/api'
import type { ScriptEntry } from '../features/settings/types'
import { useResource } from '../hooks/useResource'
import { formatRelativeTime } from '../lib/utils'
import { useNabu } from '../state/NabuContext'

export function OutputsPage() {
  const { activeScope } = useNabu()
  const { data, loading, error, refresh } = useResource(outputsApi.list, activeScope?.id ?? '')
  const [running, setRunning] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const sites = useMemo(() => (data?.items ?? []).filter((item) => item.url), [data?.items])
  const files = useMemo(() => (data?.items ?? []).filter((item) => item.path), [data?.items])

  useEffect(() => {
    const handleChange = (event: Event) => {
      const type = (event as CustomEvent<{ type?: string }>).detail?.type ?? ''
      if (type.startsWith('output.') || type.startsWith('task.') || type.startsWith('script.')) void refresh()
    }
    window.addEventListener('nabu:data-changed', handleChange)
    return () => window.removeEventListener('nabu:data-changed', handleChange)
  }, [refresh])

  const run = async (script: ScriptEntry) => {
    if (running) return
    setRunning(script.id)
    setActionError(null)
    try {
      await settingsApi.runScript(script.id)
      await refresh()
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'The tool could not be run.')
    } finally {
      setRunning(null)
    }
  }

  if (loading && !data) return <PageLoading label="Loading outputs…" />
  const scripts = data?.scripts ?? []
  const empty = !sites.length && !files.length && !scripts.length
  return (
    <div className="page-stack max-w-6xl">
      <div className="page-heading items-end">
        <div><h1 className="page-title">Outputs</h1><p className="page-description">Open what Nabu made and run reusable workspace tools without hunting through folders.</p></div>
        <Button asChild variant="secondary"><Link to="/chat">Ask Nabu to build something</Link></Button>
      </div>
      {(error || actionError) ? <InlineError message={actionError ?? error ?? ''} /> : null}
      {empty ? <EmptyState icon={<PackageOpen className="size-5" />} title="Nothing has been published here yet" description="When Nabu finishes a site, document, media file, export, or reusable tool, it will appear here automatically." action={<Button asChild variant="primary"><Link to="/chat">Open Chat</Link></Button>} /> : null}
      {sites.length ? <OutputSection title="Apps & sites" description="Live destinations and previews ready to open." icon={<Globe2 className="size-4" />}><div className="grid gap-3 md:grid-cols-2">{sites.map((item) => <SiteOutputCard key={`site-${item.id}`} item={item} />)}</div></OutputSection> : null}
      {files.length ? <OutputSection title="Files & documents" description="Preview, edit, or download durable workspace files." icon={<FileArchive className="size-4" />}><div className="divide-y divide-line overflow-hidden rounded-xl border border-line bg-panel">{files.map((item) => <FileOutputRow key={`file-${item.id}`} item={item} />)}</div></OutputSection> : null}
      {scripts.length ? <OutputSection title="Tools" description="Reusable checks and actions Nabu can run again." icon={<Wrench className="size-4" />}><div className="grid gap-3 md:grid-cols-2">{scripts.map((script) => <ToolCard key={script.id} script={script} running={running === script.id} onRun={() => void run(script)} />)}</div></OutputSection> : null}
    </div>
  )
}

function OutputSection({ title, description, icon, children }: { title: string; description: string; icon: ReactNode; children: ReactNode }) {
  return <section><div className="mb-3 flex items-start gap-3"><span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-raised text-muted">{icon}</span><div className="min-w-0"><h2 className="text-base font-semibold text-ink">{title}</h2><p className="mt-0.5 text-xs leading-relaxed text-muted">{description}</p></div></div>{children}</section>
}

function SiteOutputCard({ item }: { item: WorkspaceOutput }) {
  const { openFile } = useFileViewer()
  const host = friendlyHost(item.url)
  return <Card className="flex min-w-0 flex-col gap-4 p-4 shadow-none sm:p-5"><div className="flex min-w-0 items-start gap-3"><span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-accent/20 bg-accent/5 text-accent"><Globe2 className="size-5" /></span><div className="min-w-0 flex-1"><h3 className="truncate text-sm font-semibold text-ink">{item.name}</h3><p className="mt-1 truncate text-xs text-muted">{host}</p>{item.taskTitle ? <SourceLink item={item} /> : null}</div></div><div className="flex flex-wrap gap-2"><Button asChild variant="primary" size="sm"><a href={item.url} target="_blank" rel="noreferrer">Open<ArrowUpRight className="size-3.5" /></a></Button>{item.path ? <Button variant="secondary" size="sm" onClick={() => openFile(item.path!)}>View source</Button> : null}</div></Card>
}

function FileOutputRow({ item }: { item: WorkspaceOutput }) {
  const { openFile } = useFileViewer()
  return <button type="button" className="group flex min-h-16 w-full min-w-0 items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-raised/55 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent" onClick={() => item.path && openFile(item.path)}><span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted group-hover:text-ink">{fileIcon(item)}</span><span className="min-w-0 flex-1"><span className="block truncate text-sm font-semibold text-ink">{item.name}</span><span className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted"><span>{fileLabel(item)}</span>{item.size ? <span>{formatBytes(item.size)}</span> : null}{item.createdAt ? <span>{formatRelativeTime(item.createdAt)}</span> : null}{item.taskTitle ? <span className="truncate">From {item.taskTitle}</span> : null}</span></span><span className="shrink-0 text-xs font-semibold text-link">Open</span></button>
}

function ToolCard({ script, running, onRun }: { script: ScriptEntry; running: boolean; onRun: () => void }) {
  return <Card className="flex min-w-0 items-start gap-3 p-4 shadow-none"><span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><FileCode2 className="size-5" /></span><div className="min-w-0 flex-1"><h3 className="truncate text-sm font-semibold text-ink">{script.name}</h3><p className="mt-1 line-clamp-2 text-xs leading-relaxed text-muted">{script.description || script.lastSummary || 'Reusable workspace tool.'}</p></div><Button variant="secondary" size="sm" disabled={!script.enabled || running} onClick={onRun}>{running ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Play className="size-4" />}{running ? 'Running' : 'Run'}</Button></Card>
}

function SourceLink({ item }: { item: WorkspaceOutput }) {
  if (!item.taskId || !item.taskTitle) return null
  return <Link to={`/tasks/${encodeURIComponent(item.taskId)}`} className="mt-2 inline-flex max-w-full items-center gap-1 truncate text-[11px] font-medium text-link hover:underline">{item.taskTitle}<ArrowUpRight className="size-3 shrink-0" /></Link>
}

function friendlyHost(value?: string) {
  if (!value) return 'Web output'
  try { return new URL(value).host }
  catch { return value }
}

function fileIcon(item: WorkspaceOutput) {
  if (item.fileKind === 'image') return <ImageIcon className="size-4.5" />
  if (item.fileKind === 'video') return <Video className="size-4.5" />
  if (item.fileKind === 'text' || item.fileKind === 'pdf') return <FileText className="size-4.5" />
  return <FileArchive className="size-4.5" />
}

function fileLabel(item: WorkspaceOutput) {
  if (item.fileKind === 'image') return 'Image'
  if (item.fileKind === 'video') return 'Video'
  if (item.fileKind === 'pdf') return 'PDF'
  if (item.editable) return 'Editable file'
  return item.mimeType || 'File'
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}
