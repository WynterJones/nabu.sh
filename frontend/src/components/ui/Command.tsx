import { Command as CommandPrimitive } from 'cmdk'
import { Search } from 'lucide-react'
import { forwardRef } from 'react'
import { cn } from '../../lib/utils'

export const Command = forwardRef<React.ElementRef<typeof CommandPrimitive>, React.ComponentPropsWithoutRef<typeof CommandPrimitive>>(
  function Command({ className, ...props }, ref) { return <CommandPrimitive ref={ref} className={cn('flex size-full flex-col overflow-hidden rounded-lg bg-panel text-ink', className)} {...props} /> },
)
export function CommandInput(props: React.ComponentPropsWithoutRef<typeof CommandPrimitive.Input>) { return <div className="flex items-center gap-2 border-b border-line px-3"><Search className="size-4 shrink-0 text-muted" /><CommandPrimitive.Input className="h-10 min-w-0 flex-1 bg-transparent text-sm text-ink outline-none placeholder:text-muted" {...props} /></div> }
export const CommandList = forwardRef<React.ElementRef<typeof CommandPrimitive.List>, React.ComponentPropsWithoutRef<typeof CommandPrimitive.List>>(
  function CommandList({ className, ...props }, ref) { return <CommandPrimitive.List ref={ref} className={cn('max-h-72 overflow-y-auto overflow-x-hidden p-1', className)} {...props} /> },
)
export const CommandEmpty = (props: React.ComponentPropsWithoutRef<typeof CommandPrimitive.Empty>) => <CommandPrimitive.Empty className="py-6 text-center text-xs text-muted" {...props} />
export const CommandGroup = forwardRef<React.ElementRef<typeof CommandPrimitive.Group>, React.ComponentPropsWithoutRef<typeof CommandPrimitive.Group>>(
  function CommandGroup({ className, ...props }, ref) { return <CommandPrimitive.Group ref={ref} className={cn('overflow-hidden text-ink [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-2 [&_[cmdk-group-heading]]:text-[10px] [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-wider [&_[cmdk-group-heading]]:text-muted', className)} {...props} /> },
)
export const CommandItem = forwardRef<React.ElementRef<typeof CommandPrimitive.Item>, React.ComponentPropsWithoutRef<typeof CommandPrimitive.Item>>(
  function CommandItem({ className, ...props }, ref) { return <CommandPrimitive.Item ref={ref} className={cn('relative flex cursor-default select-none items-center gap-2 rounded-lg px-2 py-2 text-xs outline-none data-[disabled=true]:pointer-events-none data-[selected=true]:bg-raised data-[disabled=true]:opacity-50', className)} {...props} /> },
)
