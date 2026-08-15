import * as ScrollAreaPrimitive from '@radix-ui/react-scroll-area'
import { forwardRef } from 'react'
import { cn } from '../../lib/utils'

export const ScrollArea = forwardRef<React.ElementRef<typeof ScrollAreaPrimitive.Root>, React.ComponentPropsWithoutRef<typeof ScrollAreaPrimitive.Root>>(
  function ScrollArea({ className, children, ...props }, ref) {
    return (
      <ScrollAreaPrimitive.Root ref={ref} className={cn('relative overflow-hidden', className)} {...props}>
        <ScrollAreaPrimitive.Viewport className="size-full rounded-[inherit]">{children}</ScrollAreaPrimitive.Viewport>
        <ScrollBar />
        <ScrollAreaPrimitive.Corner />
      </ScrollAreaPrimitive.Root>
    )
  },
)

export const ScrollBar = forwardRef<React.ElementRef<typeof ScrollAreaPrimitive.Scrollbar>, React.ComponentPropsWithoutRef<typeof ScrollAreaPrimitive.Scrollbar>>(
  function ScrollBar({ className, orientation = 'vertical', ...props }, ref) {
    return (
      <ScrollAreaPrimitive.Scrollbar ref={ref} orientation={orientation} className={cn('flex touch-none select-none p-px transition-colors', orientation === 'vertical' ? 'h-full w-2.5 border-l border-l-transparent' : 'h-2.5 flex-col border-t border-t-transparent', className)} {...props}>
        <ScrollAreaPrimitive.Thumb className="relative flex-1 rounded-full bg-[#3a3a40]" />
      </ScrollAreaPrimitive.Scrollbar>
    )
  },
)
