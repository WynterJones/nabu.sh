import { apiRequest, booleanValue, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import type { CalendarItem, CalendarItemKind, CalendarRange } from './types'

function internalHref(value: unknown): string | undefined {
  const href = optionalString(value)
  return href?.startsWith('/') && !href.startsWith('//') ? href : undefined
}

export function parseCalendarItem(raw: unknown): CalendarItem {
  const item = record(raw)
  const rawKind = stringValue(item.kind ?? item.type, 'task').toLowerCase()
  const kind: CalendarItemKind = rawKind === 'schedule' || rawKind === 'run' || rawKind === 'milestone' ? rawKind : 'task'
  const id = stringValue(item.id ?? item.entity_id)
  return {
    id,
    kind,
    title: stringValue(item.title ?? item.name, 'Untitled item'),
    status: stringValue(item.status, kind === 'schedule' ? 'planned' : 'ready'),
    startsAt: stringValue(item.starts_at ?? item.startsAt ?? item.planned_at ?? item.scheduled_at),
    endedAt: optionalString(item.ended_at ?? item.endedAt),
    recurring: booleanValue(item.recurring),
    href: internalHref(item.href) ?? (id ? kind === 'task' ? `/tasks/${encodeURIComponent(id)}` : kind === 'run' ? `/runs/${encodeURIComponent(id)}` : kind === 'schedule' ? '/settings/schedules' : '/calendar' : undefined),
  }
}

export const calendarApi = {
  list: ({ from, to }: CalendarRange) => {
    const query = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() })
    return apiRequest<unknown>(`/api/calendar?${query}`).then((raw) => list(extractValue(raw, 'items', 'calendar')).map(parseCalendarItem).filter((item) => item.id && !Number.isNaN(new Date(item.startsAt).getTime())))
  },
}
