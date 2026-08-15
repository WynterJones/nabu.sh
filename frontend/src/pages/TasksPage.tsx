import { CheckCircle2, CircleDotDashed, ListChecks, Plus, Sparkles } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { CreateTaskDialog } from '../components/CreateTaskDialog'
import { PageLoading } from '../components/PageState'
import { TaskRow } from '../components/TaskRow'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/Select'
import { cn, taskSort } from '../lib/utils'
import { useNabu } from '../state/NabuContext'
import type { Task, TaskStatus } from '../types'

type TaskSectionId = 'doing' | 'needs-you' | 'ready' | 'finished'

const sections: Array<{ id: TaskSectionId; title: string; statuses: TaskStatus[]; icon: typeof ListChecks }> = [
  { id: 'doing', title: 'Doing', statuses: ['running'], icon: CircleDotDashed },
  { id: 'needs-you', title: 'Needs You', statuses: ['needs_approval', 'waiting', 'failed'], icon: Sparkles },
  { id: 'ready', title: 'Ready', statuses: ['ready', 'idea'], icon: ListChecks },
  { id: 'finished', title: 'Finished', statuses: ['completed', 'cancelled'], icon: CheckCircle2 },
]

function sectionFromHash(hash: string): TaskSectionId | undefined {
  const value = hash.replace(/^#/, '')
  return sections.some((section) => section.id === value) ? value as TaskSectionId : undefined
}

export function TasksPage() {
  const { tasks, activeScope, loading } = useNabu()
  const location = useLocation()
  const navigate = useNavigate()
  const [createOpen, setCreateOpen] = useState(false)
  const [selected, setSelected] = useState<TaskSectionId>(() => sectionFromHash(location.hash) ?? 'doing')
  const [finishedLimit, setFinishedLimit] = useState(5)
  const grouped = useMemo(() => Object.fromEntries(sections.map((section) => [section.id, tasks.filter((task) => section.statuses.includes(task.status)).sort(section.id === 'finished' ? finishedTaskSort : taskSort)])) as Record<TaskSectionId, Task[]>, [tasks])
  const selectedSection = sections.find((section) => section.id === selected) ?? sections[0]
  const allSelectedTasks = grouped[selected]
  const visibleTasks = selected === 'finished' ? allSelectedTasks.slice(0, finishedLimit) : allSelectedTasks

  useEffect(() => {
    const requested = sectionFromHash(location.hash)
    if (requested) setSelected(requested)
  }, [location.hash, location.key])

  useEffect(() => { setFinishedLimit(5) }, [activeScope?.id, selected])

  useEffect(() => {
    if (location.hash !== '#needs-you' || loading || selected !== 'needs-you') return
    const frame = window.requestAnimationFrame(() => {
      const section = document.getElementById('needs-you')
      if (!section) return
      const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
      section.scrollIntoView({ behavior: reducedMotion ? 'auto' : 'smooth', block: 'start' })
      section.focus({ preventScroll: true })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [grouped, loading, location.hash, location.key, selected])

  const selectSection = (id: TaskSectionId) => {
    setSelected(id)
    navigate(`/tasks#${id}`, { replace: true })
  }

  if (loading) return <PageLoading label="Loading the task queue…" />

  return (
    <div className="page-stack max-w-6xl">
      <div className="page-heading"><h1 className="page-title">Tasks</h1><Button variant="primary" onClick={() => setCreateOpen(true)}><Plus className="size-4" />Create task</Button></div>
      <div className="tasks-mobile-section-select"><label className="field"><span className="field-label">Task section</span><Select value={selected} onValueChange={(value) => selectSection(value as TaskSectionId)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{sections.map((section) => <SelectItem key={section.id} value={section.id}>{section.title} · {grouped[section.id].length}</SelectItem>)}</SelectContent></Select></label></div>
      <div className="tasks-section-layout">
        <aside className="tasks-section-sidebar"><nav aria-label="Task sections" className="space-y-1">{sections.map(({ id, title, icon: Icon }) => <button key={id} type="button" className={cn('tasks-section-nav-item', selected === id && 'tasks-section-nav-item-active')} aria-current={selected === id ? 'page' : undefined} onClick={() => selectSection(id)}><Icon className="size-4" /><span className="min-w-0 flex-1 text-left">{title}</span><span className="count-pill">{grouped[id].length}</span></button>)}</nav></aside>
        <section id={selected} tabIndex={-1} className="task-board-section min-w-0 scroll-mt-20 outline-none" aria-labelledby={`${selected}-title`}>
          <div className="mb-3 flex items-center gap-2.5 px-1"><selectedSection.icon className="size-4 text-muted" aria-hidden="true" /><h2 id={`${selected}-title`} className="text-base font-semibold text-ink">{selectedSection.title}</h2><span className="count-pill">{allSelectedTasks.length}</span></div>
          {!allSelectedTasks.length ? <EmptySection section={selected} onCreate={() => setCreateOpen(true)} /> : <Card className="overflow-hidden shadow-none">{visibleTasks.map((task) => <TaskRow key={task.id} task={task} />)}{selected === 'finished' && visibleTasks.length < allSelectedTasks.length ? <div className="flex justify-center border-t border-line p-3"><Button variant="secondary" size="sm" onClick={() => setFinishedLimit((current) => current + 15)}>Show 15 more</Button></div> : null}</Card>}
        </section>
      </div>
      <CreateTaskDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  )
}

function finishedTaskSort(a: Task, b: Task): number {
  const timestamp = (task: Task) => new Date(task.completedAt ?? task.updatedAt ?? task.createdAt ?? 0).getTime()
  return timestamp(b) - timestamp(a)
}

function EmptySection({ section, onCreate }: { section: TaskSectionId; onCreate: () => void }) {
  const messages: Record<TaskSectionId, { title: string; description: string }> = {
    doing: { title: 'Nothing is running', description: 'Nabu will pick up the next ready task when capacity is available.' },
    'needs-you': { title: 'Nothing needs you', description: 'Approvals, questions, and failed work will appear here.' },
    ready: { title: 'No work is ready', description: 'Draft a focused task or ask Nabu to orient around the workspace.' },
    finished: { title: 'No finished work', description: 'Completed and closed tasks will collect here.' },
  }
  const message = messages[section]
  return <div className="tasks-empty-section"><ListChecks className="size-4 shrink-0 text-muted" aria-hidden="true" /><div className="min-w-0 flex-1"><h3 className="text-sm font-medium text-ink">{message.title}</h3><p className="mt-1 text-xs leading-relaxed text-muted">{message.description}</p></div>{section === 'ready' ? <Button variant="secondary" size="sm" onClick={onCreate}><Plus className="size-4" />Create task</Button> : null}</div>
}
