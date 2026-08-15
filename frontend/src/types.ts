export type OperatorStatus = 'working' | 'idle' | 'waiting' | 'paused' | 'needs_attention'
export type TaskStatus =
  | 'idea'
  | 'ready'
  | 'running'
  | 'waiting'
  | 'needs_approval'
  | 'completed'
  | 'failed'
  | 'cancelled'
export type TaskPriority = 'high' | 'normal' | 'low'

export interface OperatorActivity {
  kind: 'task' | 'run' | 'chat' | 'operator' | string
  label: string
  status: 'running' | 'queued' | 'waiting' | string
  entityId?: string
  detail?: string
}

export interface StatusResponse {
  status: OperatorStatus
  setupComplete: boolean
  paused: boolean
  name: string
  version?: string
  nextOrientationAt?: string
  activeTaskId?: string
  message?: string
  missionStarted?: boolean
  codexAvailable?: boolean
  readyCount?: number
  needsAttention?: number
  codexState?: string
  codexMessage?: string
  retryAt?: string
  serviceHealthy?: boolean
  diskFreeBytes?: number
  lastBackupAt?: string
  contextReady?: boolean
  activities?: OperatorActivity[]
  chatQueued?: number
}

export interface Mission {
  id?: string
  statement: string
  context?: string
  active?: boolean
  updatedAt?: string
}

export interface Workspace {
  id?: string
  path: string
  writable?: boolean
  git?: boolean
  valid?: boolean
  error?: string
}

export interface TaskChecklistItem {
  label: string
  complete: boolean
  failed?: boolean
  details?: string
}

export interface Task {
  id: string
  title: string
  purpose?: string
  whyThisMatters?: string
  status: TaskStatus
  priority: TaskPriority
  workspace?: string
  createdAt?: string
  updatedAt?: string
  startedAt?: string
  completedAt?: string
  plannedAt?: string
  runRequestedAt?: string
  createdBy?: string
  runId?: string
  dependsOnTaskIds?: string[]
  definitionOfDone: TaskChecklistItem[]
  output?: string
  resultSummary?: string
  error?: string
  failureReason?: string
  verification: string[]
  uncertainties: string[]
  artifacts: string[]
  artifactFiles: Array<{ name: string; path: string }>
  filesChanged: string[]
}

export interface RunEvent {
  id?: string
  type: string
  message: string
  at?: string
  stream?: 'stdout' | 'stderr' | string
}

export interface Run {
  id: string
  taskId?: string
  taskTitle?: string
  type?: string
  status: string
  startedAt?: string
  endedAt?: string
  exitCode?: number
  cwd?: string
  output: string
  stderr?: string
  events: RunEvent[]
  resultSummary?: string
}

export interface SetupCheck {
  key: string
  label: string
  ok: boolean
  detail?: string
}

export interface SetupPayload {
  name: string
  mission: string
  context: string
  workspaces: string[]
  autonomy: {
    research: boolean
    editWorkspaces: boolean
    runLocal: boolean
    createGitChanges: boolean
    createDrafts: boolean
  }
}
