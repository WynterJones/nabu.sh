import { CalendarClock, CalendarDays, ChevronLeft, ChevronRight, Flag, ListTodo, PlayCircle, RefreshCw, Repeat2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { InlineError, PageLoading } from '../components/PageState'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Card, EmptyState } from '../components/ui/Card'
import { calendarApi } from '../features/calendar/api'
import type { CalendarItem, CalendarItemKind } from '../features/calendar/types'
import { settingsApi } from '../features/settings/api'
import type { Schedule } from '../features/settings/types'
import { useResource } from '../hooks/useResource'
import { cn } from '../lib/utils'
import { useNabu } from '../state/NabuContext'

const dayNames = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const monthFormat = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' })
const agendaDateFormat = new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
const timeFormat = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' })

export function calendarRange(month: Date): { start: Date; end: Date; days: Date[] } {
  const first = new Date(month.getFullYear(), month.getMonth(), 1)
  const start = new Date(first)
  start.setDate(first.getDate() - first.getDay())
  start.setHours(0, 0, 0, 0)
  const days = Array.from({ length: 42 }, (_, index) => {
    const day = new Date(start)
    day.setDate(start.getDate() + index)
    return day
  })
  const end = new Date(start)
  end.setDate(start.getDate() + 42)
  return { start, end, days }
}

export function calendarDateKey(value: Date | string): string {
  const date = value instanceof Date ? value : new Date(value)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

export function upcomingReadyTasks(items: CalendarItem[], now: Date): CalendarItem[] {
  return items.filter((item) => ((item.kind === 'task' && item.status === 'ready') || item.kind === 'schedule' || item.kind === 'milestone') && new Date(item.startsAt).getTime() > now.getTime()).sort((a, b) => new Date(a.startsAt).getTime() - new Date(b.startsAt).getTime()).slice(0, 12)
}

function scheduleCadence(schedule: Schedule): string {
  if (schedule.cadence.intervalSeconds) {
    const seconds = schedule.cadence.intervalSeconds
    if (seconds % 86400 === 0) return `Every ${seconds / 86400}d`
    if (seconds % 3600 === 0) return `Every ${seconds / 3600}h`
    return `Every ${Math.round(seconds / 60)}m`
  }
  return schedule.cadence.expression || 'Recurring'
}

function ItemIcon({ kind, className }: { kind: CalendarItemKind; className: string }) {
  if (kind === 'schedule') return <CalendarClock className={className} />
  if (kind === 'run') return <PlayCircle className={className} />
  if (kind === 'milestone') return <Flag className={className} />
  return <ListTodo className={className} />
}

export function CalendarPage() {
  const { activeScope } = useNabu()
  const [today] = useState(() => new Date())
  const [month, setMonth] = useState(() => new Date(today.getFullYear(), today.getMonth(), 1))
  const range = useMemo(() => calendarRange(month), [month])
  const rangeKey = `${range.start.toISOString()}-${range.end.toISOString()}-${activeScope?.id ?? ''}`
  const { data, loading, error, refresh } = useResource(() => calendarApi.list({ from: range.start, to: range.end }), rangeKey)
  const schedules = useResource(settingsApi.listSchedules, activeScope?.id ?? '')
  const items = useMemo(() => data ?? [], [data])
  const itemsByDay = useMemo(() => {
    const grouped = new Map<string, CalendarItem[]>()
    for (const item of items) {
      const key = calendarDateKey(item.startsAt)
      grouped.set(key, [...(grouped.get(key) ?? []), item])
    }
    for (const dayItems of grouped.values()) dayItems.sort((a, b) => new Date(a.startsAt).getTime() - new Date(b.startsAt).getTime())
    return grouped
  }, [items])
  const upcoming = useMemo(() => upcomingReadyTasks(items, today), [items, today])

  const moveMonth = (offset: number) => setMonth((current) => new Date(current.getFullYear(), current.getMonth() + offset, 1))
  const currentMonth = month.getFullYear() === today.getFullYear() && month.getMonth() === today.getMonth()

  return (
    <div className="page-stack max-w-7xl">
      <div className="page-heading">
        <div><h1 className="page-title">Calendar</h1><p className="page-description">Planned work, recurring schedules, and completed runs in one timeline.</p></div>
        <Button variant="secondary" size="sm" onClick={() => void refresh()} disabled={loading}><RefreshCw className={cn('size-4', loading && 'animate-spin motion-reduce:animate-none')} />Refresh</Button>
      </div>
      {error ? <InlineError message={error} /> : null}
      <div className="calendar-layout">
        <Card className="min-w-0 overflow-hidden shadow-none">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3 sm:px-5">
            <h2 className="text-base font-semibold text-ink" aria-live="polite">{monthFormat.format(month)}</h2>
            <div className="flex items-center gap-1">
              <Button variant="ghost" size="icon" aria-label="Previous month" onClick={() => moveMonth(-1)}><ChevronLeft className="size-4" /></Button>
              <Button variant="secondary" size="sm" onClick={() => setMonth(new Date(today.getFullYear(), today.getMonth(), 1))} disabled={currentMonth}>Today</Button>
              <Button variant="ghost" size="icon" aria-label="Next month" onClick={() => moveMonth(1)}><ChevronRight className="size-4" /></Button>
            </div>
          </div>
          {loading && !data ? <div className="min-h-[34rem]"><PageLoading label="Loading calendar…" /></div> : <MonthGrid days={range.days} month={month} today={today} itemsByDay={itemsByDay} />}
        </Card>

        <section className="min-w-0" aria-labelledby="upcoming-title">
          <Card className="p-4 shadow-none sm:p-5">
            <h2 id="upcoming-title" className="text-base font-semibold text-ink">Upcoming</h2>
            {upcoming.length ? <ol className="mt-4 space-y-1">{upcoming.map((item) => <AgendaItem key={`${item.kind}-${item.id}-${item.startsAt}`} item={item} />)}</ol> : <div className="py-8 text-center"><CalendarDays className="mx-auto size-5 text-muted" /><p className="mt-3 text-sm font-medium text-ink">Nothing planned</p><p className="mt-1 text-xs text-muted">Future ready tasks and schedule occurrences will appear here.</p></div>}
          </Card>
          {(schedules.data?.length ?? 0) > 0 ? <Card className="mt-4 p-4 shadow-none sm:p-5"><div className="flex items-center justify-between gap-3"><h2 className="text-base font-semibold text-ink">Schedules</h2><Button asChild variant="ghost" size="sm"><Link to="/settings/schedules">Manage</Link></Button></div><div className="mt-3 space-y-1.5">{schedules.data?.map((schedule) => <Link key={schedule.id} to="/settings/schedules" className="agenda-item"><span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><Repeat2 className="size-4" /></span><span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium text-ink">{schedule.name}</span><span className="mt-0.5 block truncate text-[11px] text-muted">{scheduleCadence(schedule)}</span></span><Badge variant={schedule.enabled ? 'success' : 'outline'}>{schedule.enabled ? 'On' : 'Off'}</Badge></Link>)}</div></Card> : null}
          {!items.length && !loading ? <EmptyState compact icon={<CalendarDays className="size-5" />} title="No calendar activity" description="Plan a task or create a schedule to establish the first timeline item." /> : null}
        </section>
      </div>
    </div>
  )
}

function MonthGrid({ days, month, today, itemsByDay }: { days: Date[]; month: Date; today: Date; itemsByDay: Map<string, CalendarItem[]> }) {
  return (
    <div className="calendar-grid" role="grid" aria-label={monthFormat.format(month)}>
      {dayNames.map((day) => <div key={day} className="calendar-weekday" role="columnheader">{day}</div>)}
      {days.map((day) => {
        const key = calendarDateKey(day)
        const dayItems = itemsByDay.get(key) ?? []
        const outside = day.getMonth() !== month.getMonth()
        const isToday = key === calendarDateKey(today)
        return <div key={key} className={cn('calendar-day', outside && 'calendar-day-outside')} role="gridcell" aria-label={`${agendaDateFormat.format(day)}, ${dayItems.length} items`}><div className="flex items-center justify-between gap-1"><time dateTime={key} className={cn('calendar-date', isToday && 'calendar-date-today')}>{day.getDate()}</time>{dayItems.length ? <span className="calendar-day-count">{dayItems.length}</span> : null}</div><div className="calendar-day-items">{dayItems.slice(0, 3).map((item) => <CalendarPill key={`${item.kind}-${item.id}`} item={item} />)}{dayItems.length > 3 ? <span className="calendar-more">+{dayItems.length - 3} more</span> : null}</div></div>
      })}
    </div>
  )
}

function CalendarPill({ item }: { item: CalendarItem }) {
  const content = <><ItemIcon kind={item.kind} className="size-3 shrink-0" /><span className="calendar-item-label">{item.title}</span>{item.recurring ? <Repeat2 className="size-2.5 shrink-0" /> : null}</>
  return item.href ? <Link className={cn('calendar-item', `calendar-item-${item.kind}`)} to={item.href} title={item.title} aria-label={`${item.title}, ${item.kind}, ${item.status}`}>{content}</Link> : <span className={cn('calendar-item', `calendar-item-${item.kind}`)} title={item.title}>{content}</span>
}

function AgendaItem({ item }: { item: CalendarItem }) {
  const startsAt = new Date(item.startsAt)
  const content = <><span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><ItemIcon kind={item.kind} className="size-4" /></span><span className="min-w-0 flex-1"><span className="block text-[11px] font-medium text-muted">{agendaDateFormat.format(startsAt)} · {timeFormat.format(startsAt)}</span><span className="mt-0.5 block text-pretty text-sm font-medium text-ink">{item.title}</span></span></>
  return <li>{item.href ? <Link to={item.href} className="agenda-item">{content}</Link> : <div className="agenda-item">{content}</div>}</li>
}
