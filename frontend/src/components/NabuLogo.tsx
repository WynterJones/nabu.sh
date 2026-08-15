import { cn } from '../lib/utils'

export function NabuLogo({ variant = 'mark', className }: { variant?: 'mark' | 'wordmark'; className?: string }) {
  if (variant === 'wordmark') {
    return (
      <span className={cn('relative block h-12 w-40 shrink-0 overflow-hidden', className)} aria-label="Nabu.sh">
        <img src="/assets/nabu-logo-transparent.png" alt="" className="absolute left-0 top-1/2 w-full -translate-y-1/2 select-none" draggable={false} />
      </span>
    )
  }
  return (
    <span className={cn('relative block size-9 shrink-0 overflow-hidden rounded-lg border border-line bg-raised', className)} aria-hidden="true">
      <img src="/assets/nabu-owl.png" alt="" className="size-full select-none object-cover" draggable={false} />
    </span>
  )
}
