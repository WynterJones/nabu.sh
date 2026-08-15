import { Blocks, BookOpenText, Bot, CalendarClock, Feather, FileCode2, FolderGit2, KeyRound, Network, ShieldCheck } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

export interface SettingsNavigationItem {
  label: string
  path: string
  icon: LucideIcon
  end?: boolean
}

export interface SettingsNavigationSection {
  label: string
  items: SettingsNavigationItem[]
}

export const settingsNavigationSections: SettingsNavigationSection[] = [
  {
    label: 'General',
    items: [
      { label: 'Operator', path: '/settings', icon: Bot, end: true },
      { label: 'Workspaces', path: '/settings/workspaces', icon: FolderGit2 },
      { label: 'Remote access', path: '/settings/remote-access', icon: Network },
    ],
  },
  {
    label: 'Workspace',
    items: [
      { label: 'Policy', path: '/settings/policy', icon: ShieldCheck },
      { label: 'Schedules', path: '/settings/schedules', icon: CalendarClock },
      { label: 'Scripts', path: '/settings/scripts', icon: FileCode2 },
      { label: 'Memory', path: '/settings/memory', icon: BookOpenText },
      { label: 'Soul', path: '/settings/soul', icon: Feather },
      { label: 'Secrets', path: '/settings/secrets', icon: KeyRound },
      { label: 'MCP connectors', path: '/settings/mcp', icon: Blocks },
    ],
  },
]

export const settingsNavigation = settingsNavigationSections.flatMap((section) => section.items)
