import { apiRequest, extractValue, list, optionalString, record, stringValue } from '../../lib/api/client'
import { parseEntityRef } from '../shared/parsers'
import { booleanValue } from '../../lib/api/client'
import type { ChatActivity, ChatEffect, ChatMessage, ChatPageResult } from './types'

export function parseChatActivity(raw: unknown): ChatActivity {
  const item = record(raw)
  return {
    id: optionalString(item.id ?? item.activity_id),
    type: stringValue(item.type ?? item.kind, 'activity'),
    label: stringValue(item.label ?? item.message ?? item.summary, 'Nabu is working'),
    status: optionalString(item.status),
    createdAt: optionalString(item.created_at ?? item.createdAt ?? item.at),
  }
}

function parseEffect(raw: unknown): ChatEffect {
  const item = record(raw)
  const entity = record(item.entity ?? item.reference)
  const detailsValue = item.details ?? item.changes
  const details = Array.isArray(detailsValue)
    ? detailsValue.map((entry) => stringValue(entry)).filter(Boolean)
    : typeof detailsValue === 'string'
      ? [detailsValue]
      : Object.entries(record(detailsValue)).map(([key, value]) => `${key.replaceAll('_', ' ')}: ${stringValue(value)}`).filter((entry) => !entry.endsWith(': '))
  const actions = list(item.actions).map((rawAction) => {
    const action = record(rawAction)
    return {
      label: stringValue(action.label),
      value: stringValue(action.value),
      description: optionalString(action.description),
      primary: booleanValue(action.primary),
    }
  }).filter((action) => action.label && action.value)
  return {
    type: stringValue(item.type ?? item.effect, 'state_changed'),
    summary: stringValue(item.summary ?? item.message ?? item.description, 'Nabu updated durable state.'),
    entity: Object.keys(entity).length ? parseEntityRef(entity) : undefined,
    details: details.length ? details : undefined,
    actions: actions.length ? actions : undefined,
  }
}

function effectMetadata(value: unknown): Record<string, unknown> {
  if (typeof value === 'string') {
    try { return record(JSON.parse(value)) }
    catch { return {} }
  }
  return record(value)
}

function recoveryContext(content: string, metadata: Record<string, unknown>) {
  const id = optionalString(metadata.recovery_task_id ?? metadata.recoveryTaskId)
  let title = optionalString(metadata.recovery_task_title ?? metadata.recoveryTaskTitle)
  let visibleContent = content
  const legacy = content.match(/^Continue (?:failed|waiting) task:\s*([^\n]+)(?:\n\n(?:New context:\s*\n?)?([\s\S]*))?$/i)
  if (legacy) {
    title ||= legacy[1]?.trim()
    visibleContent = legacy[2]?.trim() || 'Help me resolve the recorded blocker and continue this task.'
  }
  return {
    content: visibleContent,
    recoveryTask: id && title ? { id, title } : undefined,
  }
}

export function parseChatMessage(raw: unknown): ChatMessage {
  const item = record(raw)
  const metadata = effectMetadata(item.effect_metadata ?? item.effectMetadata)
  const role = stringValue(item.role ?? item.author, 'assistant').toLowerCase()
  const status = stringValue(item.status, 'complete').toLowerCase()
  const recovery = recoveryContext(stringValue(item.content ?? item.text ?? item.body), metadata)
  return {
    id: stringValue(item.id ?? item.message_id),
    role: role === 'user' || role === 'system' ? role : 'assistant',
    content: recovery.content,
    status: status === 'pending' || status === 'queued' || status === 'processing' || status === 'streaming' || status === 'failed' ? status : 'complete',
    createdAt: optionalString(item.created_at ?? item.createdAt ?? item.at),
    references: list(item.references ?? item.entities ?? metadata.references).map(parseEntityRef).filter((entry) => entry.id),
    effects: list(item.effects ?? item.state_changes ?? metadata.effects).map(parseEffect),
    activity: list(item.activity ?? item.activities).map(parseChatActivity),
    error: optionalString(item.error),
    parentMessageId: optionalString(item.parent_message_id ?? item.parentMessageId),
    threadRootId: optionalString(item.thread_root_id ?? item.threadRootId),
    replyCount: typeof item.reply_count === 'number' ? item.reply_count : 0,
    recoveryTask: recovery.recoveryTask,
  }
}

function parsePage(raw: unknown): ChatPageResult {
  const body = record(raw)
  const data = record(body.data)
  const source = Object.keys(data).length ? data : body
  const rootValue = source.root
  return {
    messages: list(source.messages ?? source.items).map(parseChatMessage),
    hasMore: booleanValue(source.has_more ?? source.hasMore),
    nextBeforeId: optionalString(source.next_before_id ?? source.nextBeforeId),
    root: rootValue ? parseChatMessage(rootValue) : undefined,
  }
}

export const chatApi = {
  listMessages: (beforeId?: string) => apiRequest<unknown>(`/api/chat/messages?limit=10${beforeId ? `&before_id=${encodeURIComponent(beforeId)}` : ''}`, { cache: 'no-store' }).then(parsePage),
  sendMessage: (content: string) => apiRequest<unknown>('/api/chat/messages', { method: 'POST', body: JSON.stringify({ content }) }).then((raw) => parseChatMessage(extractValue(raw, 'message'))),
  deleteMessage: (id: string) => apiRequest<void>(`/api/chat/messages/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  getThread: (id: string, beforeId?: string) => apiRequest<unknown>(`/api/chat/messages/${encodeURIComponent(id)}/thread?limit=20${beforeId ? `&before_id=${encodeURIComponent(beforeId)}` : ''}`).then(parsePage),
  sendThreadReply: (id: string, content: string) => apiRequest<unknown>(`/api/chat/messages/${encodeURIComponent(id)}/thread`, { method: 'POST', body: JSON.stringify({ content }) }).then((raw) => parseChatMessage(extractValue(raw, 'message'))),
  getStatus: () => apiRequest<unknown>('/api/chat/status', { cache: 'no-store' }).then((raw) => {
    const body = record(raw)
    const status = record(extractValue(raw, 'status'))
    return {
      working: booleanValue(status.working ?? body.working),
      queued: typeof (status.queued ?? body.queued) === 'number' ? Number(status.queued ?? body.queued) : 0,
    }
  }),
}
