import { CheckCircle2, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { Button } from './ui/Button'

export interface ChatAction {
  label: string
  value: string
  description?: string
  primary?: boolean
}

export function ChatActionCard({ title, description, actions, onAction }: { title: string; description?: string; actions: ChatAction[]; onAction: (action: ChatAction) => void }) {
  const [selected, setSelected] = useState<string | null>(null)
  return (
    <section className="chat-action-card" aria-label={title}>
      <span className="chat-action-icon">{selected ? <CheckCircle2 className="size-4" /> : <Sparkles className="size-4" />}</span>
      <div className="min-w-0 flex-1">
        <h3 className="text-[13px] font-semibold leading-5 text-ink">{title}</h3>
        {description ? <p className="mt-1 text-xs leading-relaxed text-muted">{description}</p> : null}
        <div className="mt-2.5 flex flex-wrap gap-2">
          {actions.map((action) => <div key={action.value} className="chat-action-option"><Button variant={action.primary ? 'primary' : 'secondary'} size="sm" disabled={selected !== null} onClick={() => { setSelected(action.value); onAction(action) }}>{selected === action.value ? 'Selected' : action.label}</Button>{action.description ? <span>{action.description}</span> : null}</div>)}
        </div>
      </div>
    </section>
  )
}
