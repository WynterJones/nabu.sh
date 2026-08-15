import {
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  Code2,
  FolderGit2,
  FolderOpen,
  LoaderCircle,
  LockKeyhole,
  Plus,
  Rocket,
  ShieldCheck,
  Sparkles,
  Trash2,
  XCircle,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { api } from '../lib/api'
import { cn, isAbsoluteWorkspacePath } from '../lib/utils'
import { useNabu } from '../state/NabuContext'
import type { SetupCheck, SetupPayload } from '../types'
import { InlineError } from '../components/PageState'
import { NabuLogo } from '../components/NabuLogo'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { Field, Input, Textarea } from '../components/ui/Field'

const steps = ['Name', 'Mission', 'Context', 'Workspaces', 'Autonomy', 'Start']

const initialPayload: SetupPayload = {
  name: 'Nabu',
  mission: '',
  context: '',
  workspaces: [''],
  autonomy: {
    research: true,
    editWorkspaces: true,
    runLocal: true,
    createGitChanges: true,
    createDrafts: true,
  },
}

const autonomousActions: Array<{ key: keyof SetupPayload['autonomy']; label: string; description: string }> = [
  { key: 'research', label: 'Research and read', description: 'Inspect approved sources and the public web.' },
  { key: 'editWorkspaces', label: 'Local workspace work', description: 'Edit approved files, run tests, and prepare local Git changes and drafts.' },
]

const approvalActions = ['Merge protected branches', 'Deploy production', 'Publish publicly', 'Send external messages', 'Delete important data', 'Spend money']

export function SetupPage() {
  const { refresh } = useNabu()
  const [step, setStep] = useState(0)
  const [form, setForm] = useState(initialPayload)
  const [checks, setChecks] = useState<SetupCheck[]>([])
  const [checking, setChecking] = useState(false)
  const [browsingIndex, setBrowsingIndex] = useState<number | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const workspaceErrors = useMemo(() => form.workspaces.map((path) => {
    if (!path.trim()) return 'Enter a folder path.'
    if (!isAbsoluteWorkspacePath(path)) return 'Use an absolute path beginning with /.'
    return null
  }), [form.workspaces])

  const stepValid = [
    form.name.trim().length > 0,
    form.mission.trim().length >= 8,
    true,
    form.workspaces.length > 0 && workspaceErrors.every((item) => item === null),
    Object.values(form.autonomy).some(Boolean),
    true,
  ][step]

  const setField = <K extends keyof SetupPayload>(key: K, value: SetupPayload[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }

  const next = async () => {
    if (!stepValid) return
    setError(null)
    if (step === 3) {
      setChecking(true)
      try {
        const result = await api.checkSetup(form.workspaces.map((path) => path.trim()))
        setChecks(result)
        if (result.some((check) => !check.ok)) {
          setError('Resolve the failed system checks before continuing.')
          return
        }
      } catch (caught) {
        setError(caught instanceof Error ? caught.message : 'Setup checks could not be completed.')
        return
      } finally {
        setChecking(false)
      }
    }
    setStep((current) => Math.min(current + 1, steps.length - 1))
  }

  const browse = async (index: number) => {
    setBrowsingIndex(index)
    setError(null)
    try {
      const path = await api.browseWorkspace()
      const next = [...form.workspaces]
      next[index] = path
      setField('workspaces', next)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The folder picker could not be opened.')
    } finally {
      setBrowsingIndex(null)
    }
  }

  const start = async () => {
    setSubmitting(true)
    setError(null)
    try {
      await api.completeSetup({
        ...form,
        name: form.name.trim(),
        mission: form.mission.trim(),
        context: form.context.trim(),
        workspaces: form.workspaces.map((path) => path.trim()),
      })
      await api.startMission()
      await refresh()
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Nabu could not finish setup.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="setup-shell">
      <div className="setup-frame">
        <header className="flex items-center justify-between gap-4 px-5 py-5 sm:px-8">
          <NabuLogo variant="wordmark" className="h-10 w-32" />
          <span className="text-xs text-muted">Local setup</span>
        </header>

        <ol className="setup-progress" aria-label="Setup progress">
          {steps.map((label, index) => (
            <li key={label} className={cn('setup-progress-item', index === step && 'setup-progress-active', index < step && 'setup-progress-complete')} aria-current={index === step ? 'step' : undefined}>
              <span className="setup-progress-dot">{index < step ? <Check className="size-3" /> : index + 1}</span>
              <span className="setup-progress-label">{label}</span>
            </li>
          ))}
        </ol>

        <Card className="setup-card">
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-7 sm:px-8 sm:py-8">
            {step === 0 ? (
              <SetupSection icon={<Sparkles className="size-5" />} eyebrow="Step 1 of 6" title="Name your operator" description="This is the name you’ll see throughout the app. You can change it later.">
                <Field label="Display name" hint="Required">
                  <Input value={form.name} onChange={(event) => setField('name', event.target.value)} autoFocus maxLength={48} autoComplete="off" />
                </Field>
              </SetupSection>
            ) : null}

            {step === 1 ? (
              <SetupSection icon={<Rocket className="size-5" />} eyebrow="Step 2 of 6" title="Define one mission" description="What should Nabu be responsible for accomplishing? Keep it concrete enough to guide daily work.">
                <Field label="Mission" hint="Required">
                  <Textarea value={form.mission} onChange={(event) => setField('mission', event.target.value)} placeholder="Grow qualified traffic, users, and paid adoption for…" autoFocus maxLength={1200} />
                </Field>
              </SetupSection>
            ) : null}

            {step === 2 ? (
              <SetupSection icon={<Code2 className="size-5" />} eyebrow="Step 3 of 6" title="Add durable context" description="Briefly describe the business, products, audience, priorities, and important constraints.">
                <Field label="Business context" hint="Optional">
                  <Textarea value={form.context} onChange={(event) => setField('context', event.target.value)} placeholder="We build… Our primary audience is… Avoid…" autoFocus maxLength={5000} className="min-h-36" />
                </Field>
              </SetupSection>
            ) : null}

            {step === 3 ? (
              <SetupSection icon={<FolderGit2 className="size-5" />} eyebrow="Step 4 of 6" title="Approve workspaces" description="Nabu may only read or change files in these folders. Enter absolute Mac or Linux paths.">
                <div className="space-y-3">
                  {form.workspaces.map((path, index) => (
                    <Field key={index} label={`Workspace ${index + 1}`} error={path.length > 0 ? workspaceErrors[index] ?? undefined : undefined}>
                      <div className="flex items-center gap-2">
                        <Input
                          value={path}
                          onChange={(event) => {
                            const next = [...form.workspaces]
                            next[index] = event.target.value
                            setField('workspaces', next)
                          }}
                          placeholder="/Users/you/Code/project"
                          className="min-w-0 flex-1 font-mono text-xs"
                          autoFocus={index === 0}
                          spellCheck={false}
                        />
                        <Button variant="secondary" size="icon" aria-label={`Browse for workspace ${index + 1}`} onClick={() => void browse(index)} disabled={browsingIndex !== null}>
                          {browsingIndex === index ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <FolderOpen className="size-4" />}
                        </Button>
                        {form.workspaces.length > 1 ? (
                          <Button variant="ghost" size="icon" aria-label={`Remove workspace ${index + 1}`} onClick={() => setField('workspaces', form.workspaces.filter((_, itemIndex) => itemIndex !== index))}>
                            <Trash2 className="size-4" />
                          </Button>
                        ) : null}
                      </div>
                    </Field>
                  ))}
                  <Button variant="secondary" size="sm" onClick={() => setField('workspaces', [...form.workspaces, ''])}>
                    <Plus className="size-4" />Add workspace
                  </Button>
                </div>
                {checks.length ? <CheckList checks={checks} /> : null}
              </SetupSection>
            ) : null}

            {step === 4 ? (
              <SetupSection icon={<ShieldCheck className="size-5" />} eyebrow="Step 5 of 6" title="Choose autonomy" description="Local preparation can run automatically. External or high-impact actions always require your approval.">
                <div className="permission-list">
                  {autonomousActions.map((item) => {
                    const enabled = form.autonomy[item.key]
                    return (
                      <div key={item.key} className="permission-row">
                        <div className="min-w-0">
                          <p className="text-sm font-medium text-ink">{item.label}</p>
                          <p className="mt-0.5 text-pretty text-xs leading-relaxed text-muted">{item.description}</p>
                        </div>
                        <button
                          type="button"
                          role="switch"
                          aria-checked={enabled}
                          aria-label={`${item.label}: ${enabled ? 'allow' : 'ask'}`}
                          className={cn('switch', enabled && 'switch-on')}
                          onClick={() => setForm((current) => ({
                            ...current,
                            autonomy: item.key === 'editWorkspaces'
                              ? { ...current.autonomy, editWorkspaces: !enabled, runLocal: !enabled, createGitChanges: !enabled, createDrafts: !enabled }
                              : { ...current.autonomy, [item.key]: !enabled },
                          }))}
                        >
                          <span className="switch-thumb" />
                        </button>
                      </div>
                    )
                  })}
                </div>
                <div className="mt-6 rounded-lg border border-warning/25 bg-warning/5 p-4">
                  <div className="flex items-center gap-2 text-sm font-medium text-warning"><LockKeyhole className="size-4" />Always ask first</div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    {approvalActions.map((action) => <span key={action} className="rounded-md border border-line bg-canvas px-2 py-1 text-xs text-muted">{action}</span>)}
                  </div>
                </div>
              </SetupSection>
            ) : null}

            {step === 5 ? (
              <SetupSection icon={<CheckCircle2 className="size-5" />} eyebrow="Step 6 of 6" title={`${form.name.trim() || 'Nabu'} is ready`} description="Review the essentials, then start the mission. Nabu will orient and build a small, useful queue.">
                <dl className="summary-list">
                  <div className="summary-row">
                    <dt>Mission</dt>
                    <dd className="text-pretty">{form.mission}</dd>
                  </div>
                  <div className="summary-row">
                    <dt>Codex</dt>
                    <dd>{checks.find((check) => check.key === 'codex')?.ok ? 'Connected' : 'Checked'}</dd>
                  </div>
                  <div className="summary-row">
                    <dt>Workspaces</dt>
                    <dd>{form.workspaces.length} approved</dd>
                  </div>
                  <div className="summary-row">
                    <dt>Autonomy</dt>
                    <dd>Local work allowed; external changes require approval</dd>
                  </div>
                </dl>
              </SetupSection>
            ) : null}
            {error ? <div className="mx-auto mt-5 max-w-xl"><InlineError message={error} /></div> : null}
          </div>

          <div className="flex items-center justify-between gap-3 border-t border-line px-5 py-4 sm:px-8">
            <Button variant="secondary" onClick={() => { setError(null); setStep((current) => Math.max(0, current - 1)) }} disabled={step === 0 || submitting}>
              <ArrowLeft className="size-4" />Back
            </Button>
            {step < steps.length - 1 ? (
              <Button variant="primary" onClick={() => void next()} disabled={!stepValid || checking}>
                {checking ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : null}
                {checking ? 'Checking…' : 'Continue'}
                {!checking ? <ArrowRight className="size-4" /> : null}
              </Button>
            ) : (
              <Button variant="primary" onClick={() => void start()} disabled={submitting}>
                {submitting ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Rocket className="size-4" />}
                {submitting ? 'Starting…' : 'Start mission'}
              </Button>
            )}
          </div>
        </Card>
      </div>
    </main>
  )
}

function SetupSection({ icon, eyebrow, title, description, children }: { icon: React.ReactNode; eyebrow: string; title: string; description: string; children: React.ReactNode }) {
  return (
    <section className="mx-auto max-w-xl">
      <div className="flex items-start gap-3.5">
        <div className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-accent/20 bg-accent/10 text-accent">{icon}</div>
        <div className="min-w-0">
          <p className="eyebrow">{eyebrow}</p>
          <h1 className="mt-1 text-balance text-xl font-semibold leading-tight tracking-[-0.025em] text-ink sm:text-2xl">{title}</h1>
          <p className="mt-2 text-pretty text-sm leading-relaxed text-muted">{description}</p>
        </div>
      </div>
      <div className="mt-7">{children}</div>
    </section>
  )
}

function CheckList({ checks }: { checks: SetupCheck[] }) {
  return (
    <div className="mt-5 rounded-lg border border-line bg-canvas p-3" aria-live="polite">
      <p className="eyebrow mb-2">System checks</p>
      {checks.map((check) => (
        <div key={check.key} className="flex items-center gap-2 py-1.5 text-xs">
          {check.ok ? <CheckCircle2 className="size-4 shrink-0 text-accent" /> : <XCircle className="size-4 shrink-0 text-danger" />}
          <span className="text-ink">{check.label}</span>
          <span className="ml-auto max-w-[55%] truncate text-muted">{check.detail ?? (check.ok ? 'Ready' : 'Unavailable')}</span>
        </div>
      ))}
    </div>
  )
}
