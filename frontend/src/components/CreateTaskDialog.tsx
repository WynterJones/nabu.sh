import { ArrowLeft, LoaderCircle, Plus, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { api } from '../lib/api'
import { useNabu } from '../state/NabuContext'
import type { TaskPriority } from '../types'
import { InlineError } from './PageState'
import { Button } from './ui/Button'
import { Dialog } from './ui/Dialog'
import { Field, Input, Textarea } from './ui/Field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './ui/Select'
import { TaskDependencyPicker } from './TaskDependencyPicker'

interface DraftForm {
  title: string
  purpose: string
  why: string
  priority: TaskPriority
  definition: string
  dependsOnTaskIds: string[]
}

export function CreateTaskDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (value: boolean) => void }) {
  const { activeScope, tasks, refresh } = useNabu()
  const [request, setRequest] = useState('')
  const [priority, setPriority] = useState<TaskPriority>('normal')
  const [draft, setDraft] = useState<DraftForm | null>(null)
  const [drafting, setDrafting] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reset = () => { setRequest(''); setPriority('normal'); setDraft(null); setError(null) }
  const close = (value: boolean) => { if (!value && !drafting && !submitting) reset(); onOpenChange(value) }
  const createDraft = async () => {
    if (!request.trim() || drafting) return
    setDrafting(true); setError(null)
    try {
      const generated = await api.draftTask(request.trim(), priority)
      setDraft({ title: generated.title, purpose: generated.purpose, why: generated.why, priority: generated.priority, definition: generated.definitionOfDone.join('\n'), dependsOnTaskIds: [] })
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'Nabu could not draft this task.') }
    finally { setDrafting(false) }
  }
  const submit = async () => {
    if (!draft || submitting) return
    const definition = draft.definition.split('\n').map((line) => line.replace(/^[-*]\s*/, '').trim()).filter(Boolean)
    if (!draft.title.trim() || !draft.purpose.trim() || !definition.length) return
    setSubmitting(true); setError(null)
    try {
      await api.createTask({ title: draft.title.trim(), purpose: draft.purpose.trim(), whyThisMatters: draft.why.trim() || undefined, priority: draft.priority, definitionOfDone: definition, dependsOnTaskIds: draft.dependsOnTaskIds })
      await refresh(); close(false)
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'The task could not be created.') }
    finally { setSubmitting(false) }
  }

  const footer = draft ? <><Button variant="ghost" onClick={() => { setDraft(null); setError(null) }} disabled={submitting}><ArrowLeft className="size-4" />Back</Button><Button variant="primary" onClick={() => void submit()} disabled={!draft.title.trim() || !draft.purpose.trim() || !draft.definition.trim() || submitting}>{submitting ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Plus className="size-4" />}{submitting ? 'Creating…' : 'Create task'}</Button></> : <><Button variant="ghost" onClick={() => close(false)} disabled={drafting}>Cancel</Button><Button variant="primary" onClick={() => void createDraft()} disabled={!request.trim() || drafting}>{drafting ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Sparkles className="size-4" />}{drafting ? 'Drafting…' : 'Draft task'}</Button></>

  return (
    <Dialog open={open} onOpenChange={close} title={draft ? 'Review drafted task' : 'Create a task'} description={draft ? 'Nabu drafted a bounded task from your intent. Review the durable details before adding it to the queue.' : `Describe the outcome in plain language. Nabu will draft a focused task for ${activeScope?.name ?? 'the active workspace'}.`} footer={footer}>
      {!draft ? (
        <div className="space-y-5">
          <Field label="What should Nabu work on?" hint="Required"><Textarea value={request} onChange={(event) => setRequest(event.target.value)} placeholder="Figure out why signup conversion dropped after the last release, verify the likely cause, and recommend the next action." autoFocus className="min-h-44 text-base leading-relaxed" maxLength={4000} /></Field>
          <Field label="Priority" hint="Optional"><Select value={priority} onValueChange={(value: TaskPriority) => setPriority(value)}><SelectTrigger className="w-36"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="high">High</SelectItem><SelectItem value="normal">Normal</SelectItem><SelectItem value="low">Low</SelectItem></SelectContent></Select></Field>
          {error ? <InlineError message={error} /> : null}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="rounded-lg border border-accent/20 bg-accent/5 p-3 text-xs leading-relaxed text-muted"><span className="font-semibold text-accent">Drafted by Nabu:</span> edit anything below. The task is not queued until you create it.</div>
          <Field label="Title" hint="Required"><Input value={draft.title} onChange={(event) => setDraft((current) => current ? { ...current, title: event.target.value } : current)} autoFocus /></Field>
          <Field label="Purpose" hint="Required"><Textarea value={draft.purpose} onChange={(event) => setDraft((current) => current ? { ...current, purpose: event.target.value } : current)} /></Field>
          <Field label="Why this matters"><Textarea value={draft.why} onChange={(event) => setDraft((current) => current ? { ...current, why: event.target.value } : current)} /></Field>
          <div className="form-grid"><Field label="Priority"><Select value={draft.priority} onValueChange={(value: TaskPriority) => setDraft((current) => current ? { ...current, priority: value } : current)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="high">High</SelectItem><SelectItem value="normal">Normal</SelectItem><SelectItem value="low">Low</SelectItem></SelectContent></Select></Field><Field label="Definition of done" hint="One per line"><Textarea value={draft.definition} onChange={(event) => setDraft((current) => current ? { ...current, definition: event.target.value } : current)} /></Field></div>
          <div className="field">
            <span className="flex items-center justify-between gap-3"><span className="field-label">Prerequisite tasks</span><span className="text-xs text-muted">Optional</span></span>
            <TaskDependencyPicker tasks={tasks} selectedIds={draft.dependsOnTaskIds} onChange={(dependsOnTaskIds) => setDraft((current) => current ? { ...current, dependsOnTaskIds } : current)} />
            <span className="text-xs leading-relaxed text-muted">Nabu waits until every selected task is completed or closed. Only independent work runs in parallel.</span>
          </div>
          {error ? <InlineError message={error} /> : null}
        </div>
      )}
    </Dialog>
  )
}
