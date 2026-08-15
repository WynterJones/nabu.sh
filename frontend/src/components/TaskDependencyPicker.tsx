import { Check, ChevronDown, ListTree, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import type { Task } from '../types'
import { cn } from '../lib/utils'
import { Button } from './ui/Button'
import { Input } from './ui/Field'
import { Popover, PopoverContent, PopoverTrigger } from './ui/Popover'

interface TaskDependencyPickerProps {
  tasks: Task[]
  selectedIds: string[]
  onChange: (ids: string[]) => void
  excludeTaskId?: string
  disabled?: boolean
}

export function TaskDependencyPicker({ tasks, selectedIds, onChange, excludeTaskId, disabled = false }: TaskDependencyPickerProps) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const normalizedQuery = query.trim().toLowerCase()
  const available = useMemo(() => tasks.filter((task) => (
    task.id !== excludeTaskId
    && (task.status !== 'cancelled' || selectedIds.includes(task.id))
    && (!normalizedQuery || `${task.title} ${task.status}`.toLowerCase().includes(normalizedQuery))
  )), [excludeTaskId, normalizedQuery, selectedIds, tasks])
  const eligibleCount = tasks.filter((task) => task.id !== excludeTaskId && task.status !== 'cancelled').length
  const label = selectedIds.length
    ? `${selectedIds.length} prerequisite${selectedIds.length === 1 ? '' : 's'}`
    : 'No prerequisites'

  const toggle = (id: string) => {
    if (disabled || id === excludeTaskId) return
    onChange(selectedIds.includes(id) ? selectedIds.filter((item) => item !== id) : [...selectedIds, id])
  }

  return (
    <Popover open={open} onOpenChange={(next) => { setOpen(next); if (!next) setQuery('') }}>
      <PopoverTrigger asChild>
        <Button variant="secondary" className="task-dependency-trigger" disabled={disabled || eligibleCount === 0}>
          <ListTree className="size-4 text-muted" />
          <span className="min-w-0 flex-1 truncate text-left">{label}</span>
          <ChevronDown className="size-4 text-muted" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="task-dependency-popover">
        <div className="task-dependency-search">
          <Search className="size-4 shrink-0 text-muted" aria-hidden="true" />
          <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find a task…" aria-label="Find a prerequisite task" className="h-9 border-0 bg-transparent px-0 shadow-none focus-visible:outline-none" />
        </div>
        <div className="task-dependency-options" role="group" aria-label="Prerequisite tasks">
          {available.length ? available.map((task) => {
            const selected = selectedIds.includes(task.id)
            return (
              <button
                key={task.id}
                type="button"
                role="checkbox"
                aria-checked={selected}
                className={cn('task-dependency-option', selected && 'task-dependency-option-selected')}
                onClick={() => toggle(task.id)}
                disabled={disabled}
              >
                <span className="task-dependency-check" aria-hidden="true">{selected ? <Check className="size-3.5" /> : null}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs font-medium text-ink">{task.title}</span>
                  <span className="mt-0.5 block text-[10px] capitalize text-muted">{task.status.replaceAll('_', ' ')}</span>
                </span>
              </button>
            )
          }) : <p className="px-3 py-6 text-center text-xs text-muted">{normalizedQuery ? 'No matching tasks.' : 'No other tasks are available.'}</p>}
        </div>
        <div className="flex items-center justify-between gap-2 border-t border-line px-2 py-2">
          <Button variant="ghost" size="sm" onClick={() => onChange([])} disabled={disabled || !selectedIds.length}>Clear</Button>
          <Button variant="secondary" size="sm" onClick={() => setOpen(false)}>Done</Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
