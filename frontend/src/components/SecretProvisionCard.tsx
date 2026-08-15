import { KeyRound, LoaderCircle, ShieldCheck } from 'lucide-react'
import { FormEvent, useState } from 'react'
import type { EntityRef } from '../features/shared/types'
import { secretsApi } from '../features/secrets/api'
import { InlineError } from './PageState'
import { Button } from './ui/Button'
import { Input } from './ui/Field'

export function SecretProvisionCard({ secret, onReady }: { secret: EntityRef; onReady?: (name: string) => void }) {
  const [value, setValue] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(secret.status === 'configured' || secret.status === 'ready')
  const [error, setError] = useState<string | null>(null)

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!value || saving) return
    setSaving(true); setError(null)
    try {
      await secretsApi.update(secret.id, { value })
      setValue(''); setSaved(true); onReady?.(secret.title)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The secret could not be saved.')
    } finally {
      setSaving(false)
    }
  }

  if (saved) return <div className="secret-chat-saved" role="status"><ShieldCheck className="size-4 text-accent" /><span><strong>{secret.title}</strong> is saved securely. Its value remains hidden.</span></div>
  return <form className="secret-chat-card" onSubmit={(event) => void submit(event)}>
    <span className="secret-chat-icon"><KeyRound className="size-4" /></span>
    <span className="min-w-0 flex-1"><label className="field-label" htmlFor={`chat-secret-${secret.id}`}>{secret.title}</label>{secret.summary ? <span className="mt-0.5 block text-[11px] leading-relaxed text-muted">{secret.summary}</span> : null}<Input id={`chat-secret-${secret.id}`} type="password" value={value} onChange={(event) => setValue(event.target.value)} placeholder="Enter protected value" autoComplete="new-password" className="mt-2 text-base" required />{error ? <span className="mt-2 block"><InlineError message={error} /></span> : null}</span>
    <Button type="submit" variant="primary" size="sm" disabled={!value || saving}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <ShieldCheck className="size-4" />}{saving ? 'Saving…' : 'Save'}</Button>
  </form>
}
