import { cva, type VariantProps } from 'class-variance-authority'
import type { HTMLAttributes } from 'react'
import { cn } from '../../lib/utils'

export const badgeVariants = cva('inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-2 py-0.5 text-[11px] font-medium leading-4', {
  variants: {
    variant: {
      default: 'border-line bg-raised text-muted',
      success: 'border-accent/25 bg-accent/10 text-accent',
      warning: 'border-warning/30 bg-warning/10 text-warning',
      danger: 'border-danger/30 bg-danger/10 text-danger',
      outline: 'border-line bg-transparent text-muted',
    },
  },
  defaultVariants: { variant: 'default' },
})

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement>, VariantProps<typeof badgeVariants> {}

export function Badge({ variant, className, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />
}
