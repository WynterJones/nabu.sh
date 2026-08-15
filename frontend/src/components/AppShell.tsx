import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  AlertTriangle,
  CalendarDays,
  CheckSquare2,
  ChevronRight,
  Database,
  FileText,
  Menu,
  MessageSquareText,
  LoaderCircle,
  Pause,
  PackageOpen,
  PanelsTopLeft,
  Play,
  Settings,
  X,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type PropsWithChildren } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { api } from '../lib/api'
import { cn } from '../lib/utils'
import { useNabu } from '../state/NabuContext'
import { Button } from './ui/Button'
import { NabuLogo } from './NabuLogo'
import { OperatorActivityMenu } from './OperatorActivityMenu'
import { WorkspaceSwitcher } from './WorkspaceSwitcher'
import { Popover, PopoverContent, PopoverTrigger } from './ui/Popover'
import { chatApi } from '../features/chat/api'
import { databaseApi } from '../features/database/api'
import { appsApi } from '../features/apps/api'
import { outputsApi } from '../features/outputs/api'
import { reportsApi } from '../features/reports/api'
import { settingsApi } from '../features/settings/api'
import { useResource } from '../hooks/useResource'
import { settingsNavigationSections } from '../pages/settings/navigation'
import { useHoverPopover } from '../hooks/useHoverPopover'

const navigation = [
  { label: 'Chat', to: '/chat', icon: MessageSquareText },
  { label: 'Tasks', to: '/tasks', icon: CheckSquare2 },
  { label: 'Calendar', to: '/calendar', icon: CalendarDays },
  { label: 'Database', to: '/database', icon: Database },
  { label: 'Apps', to: '/apps', icon: PanelsTopLeft },
  { label: 'Outputs', to: '/outputs', icon: PackageOpen },
  { label: 'Reports', to: '/reports', icon: FileText },
]

interface NavigationCues {
  chatWorking: boolean
  chatUnread: boolean
  taskCount: number
  datasetCount: number
  appCount: number
  outputCount: number
  unreadReports: number
}

const countCue = (label: string, cues: NavigationCues) => {
  if (label === 'Tasks') return { count: cues.taskCount, singular: 'open task', plural: 'open tasks' }
  if (label === 'Database') return { count: cues.datasetCount, singular: 'dataset', plural: 'datasets' }
  if (label === 'Apps') return { count: cues.appCount, singular: 'app', plural: 'apps' }
  if (label === 'Outputs') return { count: cues.outputCount, singular: 'output', plural: 'outputs' }
  if (label === 'Reports') return { count: cues.unreadReports, singular: 'unread report', plural: 'unread reports' }
  return null
}

function NavigationCue({ label, cues }: { label: string; cues: NavigationCues }) {
  if (label === 'Chat') return <>{cues.chatWorking ? <LoaderCircle className="size-3.5 animate-spin text-accent motion-reduce:animate-none" aria-label="Nabu is responding" /> : null}{cues.chatUnread ? <span className="nav-unread-dot" aria-label="New messages from Nabu" /> : null}</>
  if (label === 'Calendar') {
    const today = new Date()
    const todayLabel = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(today)
    const todayValue = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`
    return <time dateTime={todayValue} className="text-[10px] tabular-nums text-muted" title={`Today, ${todayLabel}`}>{todayLabel}</time>
  }
  const cue = countCue(label, cues)
  return cue && cue.count > 0 ? <span className="count-pill" aria-label={`${cue.count} ${cue.count === 1 ? cue.singular : cue.plural}`}>{cue.count}</span> : null
}

function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <Link to="/chat" className={cn('brand-link', compact ? 'control-rail-brand-link' : 'w-full justify-center')} aria-label="Open Nabu Chat" title="Nabu Chat">
      <NabuLogo variant="wordmark" className={compact ? 'h-10 w-[152px]' : 'h-12 w-[164px]'} />
    </Link>
  )
}

function RailNavigationMenu({ cues }: { cues: NavigationCues }) {
  const [open, setOpen] = useState(false)
  const hover = useHoverPopover(setOpen)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild><button type="button" {...hover.triggerProps} className="control-rail-button" aria-label="Open main navigation" title="Navigation"><Menu className="size-5" /></button></PopoverTrigger>
      <PopoverContent {...hover.contentProps} side="right" align="start" sideOffset={10} className="control-rail-popover w-56">
        <nav aria-label="Primary navigation" className="space-y-1">
          {navigation.filter(({ label }) => label !== 'Chat').map(({ label, to, icon: Icon }) => <NavLink key={to} to={to} end onClick={() => setOpen(false)} className={({ isActive }) => cn('control-rail-menu-item', isActive && 'control-rail-menu-item-active')}><Icon className="size-4" /><span className="min-w-0 flex-1">{label}</span><NavigationCue label={label} cues={cues} /></NavLink>)}
        </nav>
      </PopoverContent>
    </Popover>
  )
}

function RailChatLink({ cues }: { cues: NavigationCues }) {
  return (
    <NavLink to="/chat" className={({ isActive }) => cn('control-rail-button control-rail-chat-link', isActive && 'control-rail-chat-link-active')} aria-label="Open Chat" title="Chat">
      <MessageSquareText className="size-5" />
      <span className="control-rail-chat-cue"><NavigationCue label="Chat" cues={cues} /></span>
    </NavLink>
  )
}

function RailSettingsMenu() {
  const [open, setOpen] = useState(false)
  const hover = useHoverPopover(setOpen)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild><button type="button" {...hover.triggerProps} className="control-rail-button" aria-label="Open settings" title="Settings"><Settings className="size-5" /></button></PopoverTrigger>
      <PopoverContent {...hover.contentProps} side="right" align="start" sideOffset={10} className="control-rail-popover w-56">
        {settingsNavigationSections.map((section) => <section key={section.label} className="control-rail-menu-group"><p className="control-rail-popover-label">{section.label}</p><div className="space-y-1">{section.items.map(({ label, path, icon: Icon, end }) => <NavLink key={path} to={path} end={end} onClick={() => setOpen(false)} className={({ isActive }) => cn('control-rail-menu-item', isActive && 'control-rail-menu-item-active')}><Icon className="size-4" /><span>{label}</span></NavLink>)}</div></section>)}
      </PopoverContent>
    </Popover>
  )
}

function SettingsMenu({ onNavigate, mobile = false }: { onNavigate?: () => void; mobile?: boolean }) {
  const { pathname } = useLocation()
  const [open, setOpen] = useState(false)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const active = pathname.startsWith('/settings')
  const cancelClose = () => {
    if (closeTimer.current) clearTimeout(closeTimer.current)
    closeTimer.current = null
  }
  const openMenu = () => {
    cancelClose()
    setOpen(true)
  }
  const closeMenu = () => {
    cancelClose()
    setOpen(false)
  }
  const scheduleClose = () => {
    cancelClose()
    closeTimer.current = setTimeout(() => setOpen(false), 220)
  }
  useEffect(() => () => cancelClose(), [])
  return (
    <div
      className={cn('settings-menu', mobile && 'settings-menu-mobile')}
      onMouseEnter={() => { if (!mobile) openMenu() }}
      onMouseLeave={() => { if (!mobile) scheduleClose() }}
      onFocus={() => { if (!mobile) openMenu() }}
      onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) closeMenu() }}
      onKeyDown={(event) => { if (event.key === 'Escape') closeMenu() }}
    >
      <div className={cn('settings-menu-root', active && 'nav-item-active')}>
        <NavLink to="/settings" end onClick={onNavigate} className="settings-menu-link">
          <Settings className="size-[17px]" aria-hidden="true" />
          <span className="min-w-0 flex-1">Settings</span>
        </NavLink>
        <button type="button" className="settings-menu-toggle" aria-label="Show settings sections" aria-expanded={open} aria-controls="settings-quick-menu" onClick={() => setOpen((value) => !value)}>
          <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
        </button>
      </div>
      {open ? (
        <div className="settings-flyout-shell">
          <div id="settings-quick-menu" className="settings-flyout" role="menu" aria-label="Settings sections">
            {settingsNavigationSections.map((section) => <div key={section.label} className="settings-flyout-group"><p className="settings-flyout-label">{section.label}</p>{section.items.map(({ label, path, icon: Icon, end }) => (
              <NavLink key={path} to={path} end={end} role="menuitem" onClick={() => { closeMenu(); onNavigate?.() }} className={({ isActive }) => cn('settings-flyout-item', isActive && 'settings-flyout-item-active')}>
                <Icon className="size-4 shrink-0" aria-hidden="true" />
                <span>{label}</span>
              </NavLink>
            ))}</div>)}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function NavItems({ onNavigate, cues, mobile = false }: { onNavigate?: () => void; cues: NavigationCues; mobile?: boolean }) {
  return (
    <nav aria-label="Primary navigation" className="flex flex-1 flex-col gap-1">
      {navigation.map(({ label, to, icon: Icon }) => (
        <NavLink
          key={to}
          to={to}
          end
          onClick={onNavigate}
          className={({ isActive }) => cn('nav-item', isActive && 'nav-item-active')}
        >
          <Icon className="size-[17px]" aria-hidden="true" />
          <span className="min-w-0 flex-1">{label}</span>
          <NavigationCue label={label} cues={cues} />
        </NavLink>
      ))}
      <SettingsMenu onNavigate={onNavigate} mobile={mobile} />
    </nav>
  )
}

function MobileDrawer({ open, onOpenChange, cues }: { open: boolean; onOpenChange: (open: boolean) => void; cues: NavigationCues }) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="drawer-overlay" />
        <DialogPrimitive.Content className="drawer-content">
          <DialogPrimitive.Title className="sr-only">Navigation</DialogPrimitive.Title>
          <div className="sidebar-brand-panel justify-between px-4">
            <Brand />
            <DialogPrimitive.Close asChild>
              <Button variant="ghost" size="icon" aria-label="Close navigation"><X className="size-5" /></Button>
            </DialogPrimitive.Close>
          </div>
          <div className="flex min-h-0 flex-1 flex-col p-3">
            <div className="mb-3"><WorkspaceSwitcher onNavigate={() => onOpenChange(false)} /></div>
            <NavItems onNavigate={() => onOpenChange(false)} cues={cues} mobile />
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

const titleForPath = (pathname: string) => {
  if (pathname === '/') return 'Chat'
  if (pathname.startsWith('/chat')) return 'Chat'
  if (pathname.startsWith('/tasks/')) return 'Task detail'
  if (pathname.startsWith('/tasks')) return 'Tasks'
  if (pathname.startsWith('/calendar')) return 'Calendar'
  if (pathname.startsWith('/database')) return 'Database'
  if (pathname.startsWith('/apps')) return 'Apps'
  if (pathname.startsWith('/outputs')) return 'Outputs'
  if (pathname.startsWith('/runs')) return 'Run activity'
  if (pathname.startsWith('/reports')) return 'Reports'
  if (pathname.startsWith('/settings')) return 'Settings'
  return 'Nabu'
}

function reasoningLabel(value?: string) {
  if (!value) return 'Default'
  if (value === 'xhigh') return 'X-high'
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function AppShell({ children }: PropsWithChildren) {
  const { pathname } = useLocation()
  const { status, tasks, activeScope, refresh, error, clearError } = useNabu()
  const { data: chatStatus, refresh: refreshChatStatus } = useResource(chatApi.getStatus, activeScope?.id ?? '')
  const { data: chatHistory, refresh: refreshChatHistory } = useResource(() => chatApi.listMessages(), activeScope?.id ?? '')
  const { data: datasets, refresh: refreshDatasets } = useResource(databaseApi.listDatasets, activeScope?.id ?? '')
  const { data: apps, refresh: refreshApps } = useResource(appsApi.list, activeScope?.id ?? '')
  const { data: outputs, refresh: refreshOutputs } = useResource(outputsApi.list, activeScope?.id ?? '')
  const { data: reports, refresh: refreshReports } = useResource(reportsApi.list, activeScope?.id ?? '')
  const { data: operatorSettings } = useResource(settingsApi.getOperatorSettings)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [changingPause, setChangingPause] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const taskCount = useMemo(() => tasks.filter((task) => task.status !== 'completed' && task.status !== 'cancelled').length, [tasks])
  const unreadReports = useMemo(() => (reports ?? []).filter((report) => report.status === 'unread').length, [reports])
  const latestAssistantMessage = useMemo(() => (chatHistory?.messages ?? []).filter((message) => message.role === 'assistant' && message.status === 'complete').at(-1)?.id ?? '', [chatHistory])
  const [chatUnread, setChatUnread] = useState(false)

  useEffect(() => {
    if (!activeScope?.id || !latestAssistantMessage) return
    const key = `nabu:chat-seen:${activeScope.id}`
    if (pathname === '/' || pathname.startsWith('/chat')) {
      localStorage.setItem(key, latestAssistantMessage)
      setChatUnread(false)
    } else {
      setChatUnread(localStorage.getItem(key) !== latestAssistantMessage)
    }
  }, [activeScope?.id, latestAssistantMessage, pathname])

  const cues: NavigationCues = {
    chatWorking: Boolean(chatStatus?.working), chatUnread, taskCount,
    datasetCount: datasets?.length ?? 0, appCount: apps?.length ?? 0,
    outputCount: (outputs?.items.length ?? 0) + (outputs?.scripts.length ?? 0), unreadReports,
  }

  useEffect(() => {
    const refreshCues = (event: Event) => {
      const type = (event as CustomEvent<{ type?: string }>).detail?.type ?? ''
      if (type.startsWith('chat.')) { void refreshChatStatus(); void refreshChatHistory() }
      if (type.startsWith('dataset.')) void refreshDatasets()
      if (type.startsWith('app.')) void refreshApps()
      if (type.startsWith('artifact.') || type.startsWith('output.') || type.startsWith('script.')) void refreshOutputs()
      if (type.startsWith('report.')) void refreshReports()
    }
    window.addEventListener('nabu:data-changed', refreshCues)
    return () => window.removeEventListener('nabu:data-changed', refreshCues)
  }, [refreshApps, refreshChatHistory, refreshChatStatus, refreshDatasets, refreshOutputs, refreshReports])

  const togglePaused = async () => {
    if (!status || changingPause) return
    setChangingPause(true)
    setActionError(null)
    try {
      await api.setPaused(!status.paused)
      await refresh()
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'The operator state could not be changed.')
    } finally {
      setChangingPause(false)
    }
  }

  return (
    <div className="app-shell">
      <aside className="desktop-control-rail" aria-label="Application controls">
        <div className="control-rail-brand"><Brand compact /></div>
        <div className="control-rail-actions">
          <WorkspaceSwitcher compact />
          <RailChatLink cues={cues} />
          <RailNavigationMenu cues={cues} />
          <RailSettingsMenu />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="app-header">
          <div className="app-header-left flex min-w-0 items-center gap-3">
            <Button className="mobile-nav-toggle" variant="ghost" size="icon" aria-label="Open navigation" onClick={() => setDrawerOpen(true)}>
              <Menu className="size-5" />
            </Button>
            <h1 className="truncate text-sm font-semibold text-ink">{titleForPath(pathname)}</h1>
          </div>
          <div className="app-header-actions flex shrink-0 items-center gap-2">
            <div className="operator-config-badges" aria-label="Codex configuration">
              <span className="operator-config-badge" aria-label={`Codex model: ${operatorSettings?.codexModel || 'default'}`}>{operatorSettings?.codexModel || 'Codex default'}</span>
              <span className="operator-config-badge" aria-label={`Reasoning level: ${reasoningLabel(operatorSettings?.codexReasoningEffort)}`}>{reasoningLabel(operatorSettings?.codexReasoningEffort)}</span>
            </div>
            {status ? <OperatorActivityMenu status={status} tasks={tasks} /> : null}
            <Button variant="secondary" size="sm" onClick={() => void togglePaused()} disabled={!status || changingPause}>
              {status?.paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
              <span className="header-action-label">{changingPause ? 'Updating…' : status?.paused ? 'Resume' : 'Pause'}</span>
            </Button>
          </div>
        </header>

        {(actionError || error) ? (
          <div className="global-alert" role="alert">
            <AlertTriangle className="size-4 shrink-0 text-warning" />
            <span className="min-w-0 flex-1 truncate">{actionError ?? error}</span>
            <button type="button" className="rounded p-1 text-muted hover:bg-raised hover:text-ink" onClick={() => { setActionError(null); clearError() }} aria-label="Dismiss error"><X className="size-4" /></button>
          </div>
        ) : null}

        <main className="app-main">{children}</main>
      </div>
      <MobileDrawer open={drawerOpen} onOpenChange={setDrawerOpen} cues={cues} />
    </div>
  )
}
