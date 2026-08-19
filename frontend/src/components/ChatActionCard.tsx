import { CheckCircle2, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { Button } from './ui/Button'

export interface ChatAction {
  label: string
  value: string
  description?: string
  primary?: boolean
}

// Choosing an option sends the option's value as a chat message, so the
// conversation itself is the durable record of the answer. Matching the
// answered values keeps the card resolved after a remount, instead of
// offering a decision the operator already made.
export function ChatActionCard({ title, description, actions, onAction, answeredValues }: { title: string; description?: string; actions: ChatAction[]; onAction: (action: ChatAction) => void; answeredValues?: ReadonlySet<string> }) {
  const [selected, setSelected] = useState<string | null>(null)
  const answeredValue = selected ?? actions.find((action) => answeredValues?.has(action.value.trim()))?.value ?? null
  const answered = answeredValue ? actions.find((action) => action.value === answeredValue) ?? null : null
  return (
    <section className="chat-action-card" aria-label={title}>
      <span className="chat-action-icon">{answered ? <CheckCircle2 className="size-4" /> : <Sparkles className="size-4" />}</span>
      <div className="min-w-0 flex-1">
        <h3 className="text-[13px] font-semibold leading-5 text-ink">{title}</h3>
        {description ? <p className="mt-1 text-xs leading-relaxed text-muted">{description}</p> : null}
        {answered
          ? <p className="chat-action-resolved"><CheckCircle2 className="size-3.5 shrink-0" aria-hidden="true" /><span>Chosen<span className="sr-only">:</span> <strong>{answered.label}</strong></span></p>
          : <div className="mt-2.5 flex flex-wrap gap-2">
            {actions.map((action) => <div key={action.value} className="chat-action-option"><Button variant={action.primary ? 'primary' : 'secondary'} size="sm" onClick={() => { setSelected(action.value); onAction(action) }}>{action.label}</Button>{action.description ? <span>{action.description}</span> : null}</div>)}
          </div>}
      </div>
    </section>
  )
}
