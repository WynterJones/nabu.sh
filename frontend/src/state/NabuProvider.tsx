import { useCallback, useEffect, useRef, useState, type PropsWithChildren } from 'react'
import { api } from '../lib/api'
import type { Mission, StatusResponse, Task, Workspace } from '../types'
import { NabuContext } from './NabuContext'
import { scopesApi } from '../features/scopes/api'
import type { Scope } from '../features/scopes/types'

const LIVE_EVENTS = [
  'status.changed',
  'task.created',
  'task.updated',
  'task.started',
  'task.completed',
  'task.failed',
  'task.cancelled',
  'task.deleted',
  'run.completed',
  'approval.created',
  'approval.resolved',
  'report.created',
  'report.updated',
  'report.deleted',
  'chat.message',
  'chat.started',
  'chat.completed',
  'chat.failed',
  'orientation.completed',
  'context.ready',
  'context.requested',
  'scope.changed',
  'workspace.deleted',
]

export function NabuProvider({ children }: PropsWithChildren) {
  const [status, setStatus] = useState<StatusResponse | null>(null)
  const [mission, setMission] = useState<Mission | null>(null)
  const [tasks, setTasks] = useState<Task[]>([])
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [scopes, setScopes] = useState<Scope[]>([])
  const [activeScope, setActiveScope] = useState<Scope | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const refreshTimer = useRef<number | null>(null)

  const refresh = useCallback(async () => {
    setRefreshing(true)
    try {
      const nextStatus = await api.getStatus()
      setStatus(nextStatus)
      if (nextStatus.setupComplete) {
        const [nextMission, nextTasks, nextWorkspaces, nextScopes, nextActiveScope] = await Promise.all([
          api.getMission(),
          api.getTasks(),
          api.getWorkspaces(),
          scopesApi.list().catch(() => []),
          scopesApi.active().catch(() => null),
        ])
        setMission(nextMission)
        setTasks(nextTasks)
        setWorkspaces(nextWorkspaces)
        setScopes(nextScopes)
        setActiveScope(nextActiveScope ?? nextScopes.find((scope) => scope.active) ?? null)
      }
      setError(null)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Nabu could not be reached.')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  const switchScope = useCallback(async (id: string) => {
    const selected = await scopesApi.setActive(id)
    setActiveScope(selected)
    setScopes((current) => current.map((scope) => ({ ...scope, active: scope.id === selected.id })))
    await refresh()
    window.dispatchEvent(new CustomEvent('nabu:scope-changed', { detail: { scopeId: selected.id } }))
  }, [refresh])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    const events = new EventSource('/api/events')
    const scheduleRefresh = (event?: Event) => {
      if (refreshTimer.current !== null) window.clearTimeout(refreshTimer.current)
      refreshTimer.current = window.setTimeout(() => void refresh(), 120)
      if (event?.type === 'scope.changed') window.dispatchEvent(new CustomEvent('nabu:scope-changed'))
      window.dispatchEvent(new CustomEvent('nabu:data-changed', { detail: { type: event?.type ?? 'message' } }))
    }
    events.onmessage = scheduleRefresh
    events.onopen = scheduleRefresh
    LIVE_EVENTS.forEach((type) => events.addEventListener(type, scheduleRefresh))
    return () => {
      if (refreshTimer.current !== null) window.clearTimeout(refreshTimer.current)
      LIVE_EVENTS.forEach((type) => events.removeEventListener(type, scheduleRefresh))
      events.close()
    }
  }, [refresh])

  return (
    <NabuContext.Provider value={{
      status,
      mission,
      tasks,
      workspaces,
      scopes,
      activeScope,
      loading,
      refreshing,
      error,
      refresh,
      switchScope,
      clearError: () => setError(null),
    }}>
      {children}
    </NabuContext.Provider>
  )
}
