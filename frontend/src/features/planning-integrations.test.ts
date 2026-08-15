import { afterEach, describe, expect, it, vi } from 'vitest'
import { calendarApi, parseCalendarItem } from './calendar/api'
import { coerceDatabaseValue, databaseApi, parseDatabaseDataset, parseDatabaseImport, parseDatabaseRowsPage } from './database/api'
import { remoteAccessApi } from './remote-access/api'
import { scopesApi } from './scopes/api'
import { upcomingReadyTasks } from '../pages/CalendarPage'

afterEach(() => vi.unstubAllGlobals())

describe('planning API contracts', () => {
  it('parses the private Tailscale access snapshot', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ installed: true, connected: true, version: '1.94.2', machine_name: 'Nabu Mac', dns_name: 'nabu.example.ts.net', tailnet_name: 'example.ts.net', private_url: 'https://nabu.example.ts.net', serve_configured: true, funnel_configured: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(remoteAccessApi.tailscale()).resolves.toMatchObject({ connected: true, dnsName: 'nabu.example.ts.net', privateUrl: 'https://nabu.example.ts.net', serveConfigured: true, funnelConfigured: false })
  })

  it('starts the guided Tailscale setup and returns admin consent when required', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: { installed: true, connected: true, serve_configured: false }, authorization_url: 'https://login.tailscale.com/f/serve?node=abc' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(remoteAccessApi.enableTailscale()).resolves.toMatchObject({ authorizationUrl: 'https://login.tailscale.com/f/serve?node=abc', status: { connected: true, serveConfigured: false } })
    expect(fetchMock).toHaveBeenCalledWith('/api/remote-access/tailscale/serve', expect.objectContaining({ method: 'POST' }))
  })

  it('normalizes calendar items and generates internal fallback links', () => {
    expect(parseCalendarItem({ id: 'task-1', kind: 'task', title: 'Prepare launch', status: 'ready', starts_at: '2026-08-20T14:00:00Z' })).toMatchObject({ id: 'task-1', kind: 'task', startsAt: '2026-08-20T14:00:00Z', href: '/tasks/task-1' })
    expect(parseCalendarItem({ id: 'schedule-1', kind: 'schedule', name: 'Daily orientation', starts_at: '2026-08-21T09:00:00Z', recurring: true, href: 'https://unsafe.example' })).toMatchObject({ kind: 'schedule', recurring: true, href: '/settings/schedules' })
    expect(parseCalendarItem({ id: 'milestone-1', kind: 'milestone', name: 'Review activation', starts_at: '2026-08-22T09:00:00Z' })).toMatchObject({ kind: 'milestone', title: 'Review activation', href: '/calendar' })
  })

  it('requests the visible calendar range', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [{ id: 'run-1', kind: 'run', title: 'Completed run', status: 'completed', starts_at: '2026-08-01T10:00:00Z' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const items = await calendarApi.list({ from: new Date('2026-08-01T00:00:00Z'), to: new Date('2026-09-01T00:00:00Z') })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/calendar?from=2026-08-01T00%3A00%3A00.000Z&to=2026-09-01T00%3A00%3A00.000Z')
    expect(items[0]).toMatchObject({ id: 'run-1', href: '/runs/run-1' })
  })

  it('shows future ready tasks and schedules in the upcoming list', () => {
    const items = [
      parseCalendarItem({ id: 'ready', kind: 'task', title: 'Ready task', status: 'ready', starts_at: '2026-08-20T14:00:00Z' }),
      parseCalendarItem({ id: 'running', kind: 'task', title: 'Running task', status: 'running', starts_at: '2026-08-20T15:00:00Z' }),
      parseCalendarItem({ id: 'schedule', kind: 'schedule', title: 'Schedule', status: 'ready', starts_at: '2026-08-20T16:00:00Z' }),
      parseCalendarItem({ id: 'milestone', kind: 'milestone', title: 'Review milestone', status: 'planned', starts_at: '2026-08-20T17:00:00Z' }),
      parseCalendarItem({ id: 'past', kind: 'task', title: 'Past task', status: 'ready', starts_at: '2026-08-19T14:00:00Z' }),
    ]
    expect(upcomingReadyTasks(items, new Date('2026-08-20T12:00:00Z')).map((item) => item.id)).toEqual(['ready', 'schedule', 'milestone'])
  })

  it('uploads workspace icons as multipart data without forcing JSON headers', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 'w1', name: 'Acme', path: '/acme', icon_url: '/api/scopes/w1/icon' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const image = new File(['image'], 'acme.png', { type: 'image/png' })
    await scopesApi.uploadIcon('w1', image)
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.body).toBeInstanceOf(FormData)
    expect(new Headers(init.headers).get('Content-Type')).toBeNull()
  })
})

describe('database API contracts', () => {
  it('normalizes dataset schemas and row envelopes', () => {
    expect(parseDatabaseDataset({ id: 'd1', name: 'Launches', slug: 'launches', schema: [{ name: 'votes', type: 'integer' }], unique_key: ['votes'], row_count: 120 })).toMatchObject({ id: 'd1', schema: [{ name: 'votes', type: 'integer' }], uniqueKey: ['votes'], rowCount: 120 })
    expect(parseDatabaseRowsPage({ rows: [{ id: 'r1', values: { votes: 42 } }], total: 1, next_cursor: 'opaque' })).toMatchObject({ rows: [{ id: 'r1', values: { votes: 42 } }], total: 1, nextCursor: 'opaque' })
  })

  it('encodes cursor, server sort, search, and field filters', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ rows: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await databaseApi.listRows('data/set', { limit: 25, cursor: 'next page', sort: 'votes', direction: 'desc', q: 'launch', filter: { field: 'category', value: 'AI tools' } })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/database/datasets/data%2Fset/rows?limit=25&cursor=next+page&sort=votes&direction=desc&q=launch&filter%5Bcategory%5D=AI+tools')
  })

  it('validates imports and coerces schema values without losing types', () => {
    expect(parseDatabaseImport('[{"name":"Nabu"}]')).toEqual([{ name: 'Nabu' }])
    expect(coerceDatabaseValue('42', 'integer')).toBe(42)
    expect(coerceDatabaseValue('{"active":true}', 'json')).toEqual({ active: true })
    expect(() => parseDatabaseImport('{}')).toThrow(/JSON array/)
  })
})
