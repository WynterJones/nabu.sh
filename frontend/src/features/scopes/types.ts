export interface Scope {
  id: string
  name: string
  path: string
  active: boolean
  createdAt?: string
  updatedAt?: string
  iconUrl?: string
  contextReady?: boolean
}
