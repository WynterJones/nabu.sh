import type { EntityRef } from '../shared/types'

export interface ChatEffectAction {
  label: string
  value: string
  description?: string
  primary?: boolean
}

export interface ChatEffect {
  type: string
  summary: string
  entity?: EntityRef
  details?: string[]
  actions?: ChatEffectAction[]
}

export interface ChatActivity {
  id?: string
  type: string
  label: string
  status?: string
  createdAt?: string
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'system'
  content: string
  status: 'pending' | 'queued' | 'processing' | 'streaming' | 'complete' | 'failed'
  createdAt?: string
  references: EntityRef[]
  effects: ChatEffect[]
  activity: ChatActivity[]
  error?: string
  parentMessageId?: string
  threadRootId?: string
  replyCount: number
  recoveryTask?: {
    id: string
    title: string
  }
}

export interface ChatResponse {
  message: ChatMessage
}

export interface ChatPageResult {
  messages: ChatMessage[]
  hasMore: boolean
  nextBeforeId?: string
  root?: ChatMessage
}
