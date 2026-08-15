import { createContext, useContext } from 'react'
import type { Mission, StatusResponse, Task, Workspace } from '../types'
import type { Scope } from '../features/scopes/types'

export interface NabuState {
  status: StatusResponse | null
  mission: Mission | null
  tasks: Task[]
  workspaces: Workspace[]
  scopes: Scope[]
  activeScope: Scope | null
  loading: boolean
  refreshing: boolean
  error: string | null
  refresh: () => Promise<void>
  switchScope: (id: string) => Promise<void>
  clearError: () => void
}

export const NabuContext = createContext<NabuState | null>(null)

export function useNabu(): NabuState {
  const value = useContext(NabuContext)
  if (!value) throw new Error('useNabu must be used inside NabuProvider')
  return value
}
