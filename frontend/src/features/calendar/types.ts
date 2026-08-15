export type CalendarItemKind = 'task' | 'schedule' | 'run' | 'milestone'

export interface CalendarItem {
  id: string
  kind: CalendarItemKind
  title: string
  status: string
  startsAt: string
  endedAt?: string
  recurring: boolean
  href?: string
}

export interface CalendarRange {
  from: Date
  to: Date
}
