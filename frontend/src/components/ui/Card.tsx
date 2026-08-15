import type { HTMLAttributes, PropsWithChildren, ReactNode } from 'react'
import { cn } from '../../lib/utils'

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('glass-surface rounded-xl border border-line', className)} {...props} />
}

export function SectionHeader({ eyebrow, title, action, className }: { eyebrow?: string; title: string; action?: ReactNode; className?: string }) {
  return (
    <div className={cn('flex min-w-0 items-start justify-between gap-4', className)}>
      <div className="min-w-0">
        {eyebrow ? <p className="eyebrow">{eyebrow}</p> : null}
        <h2 className="mt-1 text-pretty text-base font-semibold leading-snug text-ink">{title}</h2>
      </div>
      {action}
    </div>
  )
}

export function EmptyState({ icon, title, description, action, compact = false }: PropsWithChildren<{ icon?: ReactNode; title: string; description: string; action?: ReactNode; compact?: boolean }>) {
  return (
    <div className={cn('glass-surface flex flex-col items-center justify-center rounded-xl border border-dashed border-line px-5 text-center', compact ? 'py-8' : 'min-h-64 py-12')}>
      {icon ? <div className="mb-4 flex size-10 items-center justify-center rounded-lg border border-line bg-raised text-muted">{icon}</div> : null}
      <h2 className="text-pretty text-sm font-semibold text-ink">{title}</h2>
      <p className="mt-1.5 max-w-sm text-pretty text-sm leading-relaxed text-muted">{description}</p>
      {action ? <div className="mt-5">{action}</div> : null}
    </div>
  )
}
