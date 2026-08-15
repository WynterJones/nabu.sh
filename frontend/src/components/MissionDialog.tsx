import { AlertTriangle, LoaderCircle, Save } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { useNabu } from '../state/NabuContext'
import { InlineError } from './PageState'
import { Button } from './ui/Button'
import { Dialog } from './ui/Dialog'
import { Field, Textarea } from './ui/Field'

export function MissionDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (value: boolean) => void }) {
  const { mission, refresh } = useNabu()
  const [statement, setStatement] = useState(mission?.statement ?? '')
  const [context, setContext] = useState(mission?.context ?? '')
  const [confirming, setConfirming] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (open) {
      setStatement(mission?.statement ?? '')
      setContext(mission?.context ?? '')
      setConfirming(false)
      setError(null)
    }
  }, [mission, open])

  const save = async () => {
    if (!statement.trim() || saving) return
    if (!confirming) {
      setConfirming(true)
      return
    }
    setSaving(true)
    setError(null)
    try {
      await api.updateMission({ statement: statement.trim(), context: context.trim() })
      await refresh()
      onOpenChange(false)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The mission could not be updated.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Edit mission"
      description="The mission is Nabu’s durable north star. Keep it specific and outcome-oriented."
      footer={(
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button>
          <Button variant={confirming ? 'primary' : 'secondary'} onClick={() => void save()} disabled={!statement.trim() || saving}>
            {saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : confirming ? <AlertTriangle className="size-4" /> : <Save className="size-4" />}
            {saving ? 'Saving…' : confirming ? 'Confirm mission change' : 'Review change'}
          </Button>
        </>
      )}
    >
      <div className="space-y-4">
        {confirming ? (
          <div className="flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/10 px-3 py-3 text-sm leading-relaxed text-warning">
            <AlertTriangle className="mt-0.5 size-4 shrink-0" />
            <span>Changing the mission can cause Nabu to reprioritize or replace queued work.</span>
          </div>
        ) : null}
        <Field label="Mission" hint="Required"><Textarea value={statement} onChange={(event) => { setStatement(event.target.value); setConfirming(false) }} maxLength={1200} autoFocus /></Field>
        <Field label="Business context" hint="Optional"><Textarea value={context} onChange={(event) => { setContext(event.target.value); setConfirming(false) }} maxLength={5000} className="min-h-36" /></Field>
        {error ? <InlineError message={error} /> : null}
      </div>
    </Dialog>
  )
}
