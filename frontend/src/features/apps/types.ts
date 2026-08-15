export type LocalAppStatus = 'stopped' | 'running' | 'failed'

export interface LocalApp {
  id: string
  name: string
  description?: string
  directory: string
  command: string[]
  port: number
  healthPath: string
  autoStart: boolean
  status: LocalAppStatus
  pid?: number
  url: string
  healthy: boolean
  startedAt?: string
  stoppedAt?: string
  exitCode?: number
  error?: string
}

export interface LocalAppInput {
  name: string
  description?: string
  directory: string
  command: string[]
  port: number
  healthPath: string
  autoStart: boolean
}

export interface LocalAppLogs {
  appId: string
  content: string
}
