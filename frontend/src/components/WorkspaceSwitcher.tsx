import { Check, ChevronsUpDown, LoaderCircle, Settings2 } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useNabu } from '../state/NabuContext'
import { WorkspaceAvatar } from './WorkspaceAvatar'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from './ui/Command'
import { Popover, PopoverContent, PopoverTrigger } from './ui/Popover'
import { cn } from '../lib/utils'
import { useHoverPopover } from '../hooks/useHoverPopover'

export function WorkspaceSwitcher({ onNavigate, compact = false }: { onNavigate?: () => void; compact?: boolean }) {
  const { scopes, activeScope, switchScope } = useNabu()
  const [open, setOpen] = useState(false)
  const [switching, setSwitching] = useState<string | null>(null)
  const navigate = useNavigate()
  const hover = useHoverPopover(setOpen)

  const select = async (id: string) => {
    if (id === activeScope?.id) { setOpen(false); return }
    setSwitching(id)
    try { await switchScope(id); setOpen(false) }
    finally { setSwitching(null) }
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" {...(compact ? hover.triggerProps : {})} className={cn(compact ? 'control-rail-button control-rail-workspace' : 'workspace-switcher')} aria-label={`Switch workspace${activeScope?.name ? ` · ${activeScope.name}` : ''}`} aria-expanded={open} title={activeScope?.name ?? 'Switch workspace'}>
          <WorkspaceAvatar name={activeScope?.name ?? 'Workspace'} iconUrl={activeScope?.iconUrl} className={compact ? 'size-9' : 'size-8'} />
          {!compact ? <><span className="min-w-0 flex-1 truncate text-left text-xs font-semibold text-ink">{activeScope?.name ?? 'Nabu workspace'}</span><ChevronsUpDown className="size-4 shrink-0 text-muted" /></> : null}
        </button>
      </PopoverTrigger>
      <PopoverContent {...(compact ? hover.contentProps : {})} side={compact ? 'right' : 'bottom'} align="start" sideOffset={compact ? 10 : 6} className="w-[min(320px,calc(100vw-2rem))] p-0">
        <Command>
          <CommandInput placeholder="Find a workspace…" />
          <CommandList>
            <CommandEmpty>No workspace found.</CommandEmpty>
            <CommandGroup heading="Workspaces">
              {scopes.map((scope) => <CommandItem key={scope.id} value={`${scope.name} ${scope.path}`} onSelect={() => void select(scope.id)}><WorkspaceAvatar name={scope.name} iconUrl={scope.iconUrl} className="size-7" /><span className="min-w-0 flex-1 truncate font-medium text-ink">{scope.name}</span>{switching === scope.id ? <LoaderCircle className="size-4 animate-spin text-muted motion-reduce:animate-none" /> : activeScope?.id === scope.id ? <Check className="size-4 text-accent" /> : null}</CommandItem>)}
            </CommandGroup>
            <CommandGroup>
              <CommandItem onSelect={() => { setOpen(false); onNavigate?.(); navigate('/settings/workspaces') }}><Settings2 className="size-4 text-muted" /><span>Manage workspaces</span></CommandItem>
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
