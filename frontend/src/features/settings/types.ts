export type PolicyDecision = 'allow' | 'ask' | 'deny'

export interface Policy {
  read: PolicyDecision
  work: PolicyDecision
  publish: PolicyDecision
  dangerous: PolicyDecision
  updatedAt?: string
}

export type ScheduleTrigger = 'script' | 'task' | 'orientation'

export interface Schedule {
  id: string
  name: string
  triggerType: ScheduleTrigger
  payload: Record<string, unknown>
  cadence: { expression?: string; intervalSeconds?: number }
  enabled: boolean
  nextRunAt?: string
  lastRunAt?: string
  lastStatus?: string
  lastError?: string
}

export interface ScriptEntry {
  id: string
  name: string
  description?: string
  path?: string
  status: string
  enabled: boolean
  access: 'read' | 'write'
  timeoutSeconds?: number
  lastRunAt?: string
  lastSummary?: string
  interesting?: boolean
  secretBindings: Array<{ secretId: string; envVar: string }>
}

export interface MemoryDocument {
  body: string
  summary?: string
  updatedAt?: string
  dailyNotes: Array<{ date: string; summary: string }>
}

export interface SoulDocument {
  body: string
  updatedAt?: string
}

export interface MemoryUpdate {
  id: string
  summary: string
  content: string
  reason?: string
  status: string
  createdAt?: string
}

export interface ServiceHealth {
  status: string
  codexState: 'available' | 'unavailable' | 'rate_limited' | string
  codexMessage?: string
  retryAt?: string
  serviceHealthy?: boolean
  diskFreeBytes?: number
  backupAt?: string
}

export type CodexReasoningEffort = '' | 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max' | 'ultra'

export interface OperatorAISettings {
  codexModel: string
  codexReasoningEffort: CodexReasoningEffort
  maxParallelTasks: number
}

export interface CodexModelOption {
  id: string
  displayName: string
  description?: string
  defaultReasoningEffort: CodexReasoningEffort
  supportedReasoningEfforts: CodexReasoningEffort[]
}

export interface CodexModelCatalog {
  models: CodexModelOption[]
  source: 'codex' | 'fallback'
}
