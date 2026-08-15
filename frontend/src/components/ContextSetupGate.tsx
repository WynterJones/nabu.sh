import { ArrowRight, Sparkles } from 'lucide-react'
import type { PropsWithChildren } from 'react'
import { Link } from 'react-router-dom'
import { useNabu } from '../state/NabuContext'
import { Button } from './ui/Button'

export function ContextSetupGate({ children }: PropsWithChildren) {
  const { activeScope } = useNabu()
  const blocked = Boolean(activeScope && !activeScope.contextReady)
  if (!blocked) return children

  return (
    <div className="context-gate">
      <div ref={(node) => { if (node) node.setAttribute('inert', '') }} className="context-gate-content" aria-hidden="true">{children}</div>
      <div className="context-gate-overlay">
        <section className="context-gate-card" aria-labelledby="context-gate-title">
          <span className="context-gate-icon"><Sparkles className="size-9" /></span>
          <p className="eyebrow text-warning">Workspace setup</p>
          <h1 id="context-gate-title" className="context-gate-title">Set up this workspace with Nabu</h1>
          <p className="context-gate-description">Share what exists, what success looks like, and any important constraints. Nabu will tell you when it has enough context to begin.</p>
          <Button variant="primary" className="mt-7" asChild><Link to="/chat">Continue setup in Chat<ArrowRight className="size-4" /></Link></Button>
        </section>
      </div>
    </div>
  )
}
