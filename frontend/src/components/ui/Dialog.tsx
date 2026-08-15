import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { PropsWithChildren, ReactNode } from 'react'
import { cn } from '../../lib/utils'
import { Button } from './Button'

export function Dialog({ open, onOpenChange, title, description, children, footer, className, bodyClassName, headerClassName }: PropsWithChildren<{
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  footer?: ReactNode
  className?: string
  bodyClassName?: string
  headerClassName?: string
}>) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="dialog-overlay" />
        <DialogPrimitive.Content className={cn('dialog-content', className)}>
          <div className={cn('flex items-start justify-between gap-4 border-b border-line px-5 py-4 sm:px-6', headerClassName)}>
            <div className="min-w-0">
              <DialogPrimitive.Title className="text-pretty text-base font-semibold text-ink">{title}</DialogPrimitive.Title>
              {description ? <DialogPrimitive.Description className="mt-1 text-pretty text-sm leading-relaxed text-muted">{description}</DialogPrimitive.Description> : null}
            </div>
            <DialogPrimitive.Close asChild>
              <Button variant="ghost" size="icon" aria-label="Close dialog"><X className="size-4" /></Button>
            </DialogPrimitive.Close>
          </div>
          <div className={cn('min-h-0 overflow-y-auto px-5 py-5 sm:px-6', bodyClassName)}>{children}</div>
          {footer ? <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line px-5 py-4 sm:px-6">{footer}</div> : null}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
