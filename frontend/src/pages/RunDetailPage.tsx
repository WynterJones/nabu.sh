import { Activity, ArrowLeft, Check, Clipboard, FileTerminal, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { InlineError, PageLoading } from '../components/PageState'
import { Button } from '../components/ui/Button'
import { Card, EmptyState } from '../components/ui/Card'
import { api } from '../lib/api'
import { formatRawOutput } from '../lib/rawOutput'
import { formatRelativeTime } from '../lib/utils'
import type { Run } from '../types'

export function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [run, setRun] = useState<Run | null>(null)
  const [tab, setTab] = useState<'activity' | 'output'>('activity')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const load = useCallback(async () => {
    if (!id) return
    try {
      setRun(await api.getRun(id))
      setError(null)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The run could not be loaded.')
    } finally {
      setLoading(false)
    }
  }, [id])

  useEffect(() => { void load() }, [load])
  useEffect(() => {
    const events = new EventSource('/api/events')
    const appendOutput = (rawEvent: Event) => {
      const event = rawEvent as MessageEvent<string>
      try {
        const envelope = JSON.parse(event.data) as { entity_id?: string; data?: unknown; created_at?: string }
        const data = typeof envelope.data === 'string' ? JSON.parse(envelope.data) as Record<string, unknown> : envelope.data as Record<string, unknown> | undefined
        const runId = String(data?.run_id ?? envelope.entity_id ?? '')
        if (runId !== id) return
        const content = String(data?.data ?? '')
        if (!content) return
        const stream = String(data?.stream ?? 'stdout')
        const at = typeof data?.at === 'string' ? data.at : envelope.created_at
        setRun((current) => current ? {
          ...current,
          output: stream === 'stderr' ? current.output : `${current.output}${content}`,
          stderr: stream === 'stderr' ? `${current.stderr ?? ''}${content}` : current.stderr,
          events: [...current.events, { type: 'output', message: content, stream, at }],
        } : current)
      } catch {
        // Ignore malformed live events; the persisted run remains authoritative.
      }
    }
    const complete = () => void load()
    events.addEventListener('run.output', appendOutput)
    events.addEventListener('run.completed', complete)
    return () => {
      events.removeEventListener('run.output', appendOutput)
      events.removeEventListener('run.completed', complete)
      events.close()
    }
  }, [id, load])

  const copy = async () => {
    if (!run) return
    await navigator.clipboard.writeText([run.output, run.stderr].filter(Boolean).join('\n'))
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1600)
  }

  if (loading) return <PageLoading label="Loading run activity…" />
  if (!run) return <EmptyState title="Run not found" description={error ?? 'This run is unavailable.'} action={<Button asChild variant="secondary"><Link to="/tasks">Back to tasks</Link></Button>} />

  const output = [run.output, run.stderr].filter(Boolean).join('\n')
  return (
    <div className="page-stack max-w-6xl">
      <div><Button asChild variant="secondary" size="sm"><Link to={run.taskId ? `/tasks/${encodeURIComponent(run.taskId)}` : '/tasks'}><ArrowLeft className="size-4" />Back to task</Link></Button></div>
      <div className="page-heading">
        <div className="min-w-0">
          <div className="mb-2 flex flex-wrap items-center gap-2"><span className="task-badge capitalize">{run.status.replaceAll('_', ' ')}</span>{run.type ? <span className="priority-badge">{run.type}</span> : null}</div>
          <h1 className="page-title text-balance">{run.taskTitle ?? `Run ${run.id}`}</h1>
          <p className="page-description font-mono text-xs">{run.id}{run.cwd ? ` · ${run.cwd}` : ''}</p>
        </div>
        <Button variant="secondary" size="sm" onClick={() => void load()}><RefreshCw className="size-4" />Refresh</Button>
      </div>
      {error ? <InlineError message={error} /> : null}
      <div className="run-meta">
        <Meta label="Started" value={formatRelativeTime(run.startedAt)} />
        <Meta label="Finished" value={run.endedAt ? formatRelativeTime(run.endedAt) : 'Still running'} />
        <Meta label="Exit code" value={run.exitCode === undefined ? '—' : String(run.exitCode)} mono />
        <Meta label="Events" value={String(run.events.length)} mono />
      </div>
      {run.resultSummary ? <Card className="p-4 text-sm leading-relaxed text-muted shadow-none"><span className="mr-2 font-medium text-ink">Result:</span>{run.resultSummary}</Card> : null}
      <Card className="flex min-h-[30rem] min-w-0 flex-col overflow-hidden shadow-none">
        <div className="flex items-center justify-between gap-3 border-b border-line bg-raised/45 px-3 py-2">
          <div className="flex items-center gap-1" role="tablist" aria-label="Run views">
            <button type="button" role="tab" aria-selected={tab === 'activity'} className={`run-tab ${tab === 'activity' ? 'run-tab-active' : ''}`} onClick={() => setTab('activity')}><Activity className="size-3.5" />Activity</button>
            <button type="button" role="tab" aria-selected={tab === 'output'} className={`run-tab ${tab === 'output' ? 'run-tab-active' : ''}`} onClick={() => setTab('output')}><FileTerminal className="size-3.5" />Raw output</button>
          </div>
          {tab === 'output' && output ? <Button variant="ghost" size="sm" onClick={() => void copy()}>{copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}{copied ? 'Copied' : 'Copy'}</Button> : null}
        </div>
        {tab === 'activity' ? (
          run.events.length ? <ol className="min-h-0 flex-1 overflow-y-auto p-3 sm:p-4">{run.events.map((event, index) => (
            <li key={event.id ?? `${event.type}-${index}`} className="activity-row">
              <span className="activity-dot" />
              <div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-x-2 gap-y-1"><span className="text-xs font-medium capitalize text-ink">{event.type.replaceAll(/[._-]/g, ' ')}</span>{event.at ? <span className="text-[11px] text-muted">{formatRelativeTime(event.at)}</span> : null}</div><ActivityMessage content={event.message} /></div>
            </li>
          ))}</ol> : <EmptyState compact title="No activity events yet" description="Human-readable run events will appear here as Nabu receives them." />
        ) : (
          output ? <div className="run-output-surface">{run.output ? <RawStream label="stdout" content={run.output} /> : null}{run.stderr ? <RawStream label="stderr" content={run.stderr} danger /> : null}</div> : <div className="run-empty-output"><EmptyState compact title="No raw output" description="This run has not emitted output yet." /></div>
        )}
      </Card>
    </div>
  )
}

function ActivityMessage({ content }: { content: string }) {
  const formatted = formatRawOutput(content)
  return formatted.structured
    ? <pre className="activity-json">{formatted.text}</pre>
    : <p className="mt-1 whitespace-pre-wrap break-words text-xs leading-relaxed text-muted">{content}</p>
}

function RawStream({ label, content, danger = false }: { label: string; content: string; danger?: boolean }) {
  const formatted = formatRawOutput(content)
  return <section className="run-output-section"><header className="run-output-header"><span className={danger ? 'text-danger' : 'text-accent'}>{label}</span><span>{formatted.structured ? `${formatted.entries.toLocaleString()} JSON ${formatted.entries === 1 ? 'entry' : 'entries'}` : `${content.split('\n').length.toLocaleString()} lines`}</span></header><pre className="run-output">{formatted.text}</pre></section>
}

function Meta({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="rounded-lg border border-line bg-panel px-3 py-3"><p className="eyebrow">{label}</p><p className={`mt-1 text-sm text-ink ${mono ? 'font-mono tabular-nums' : ''}`}>{value}</p></div>
}
