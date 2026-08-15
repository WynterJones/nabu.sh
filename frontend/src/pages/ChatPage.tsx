import { ArrowDown, ArrowUp, ArrowUpRight, ListRestart, LoaderCircle, MessageSquare, Sparkles, Trash2, X } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EntityReferenceCard } from '../components/EntityReferenceCard'
import { ChatActionCard } from '../components/ChatActionCard'
import { Markdown } from '../components/Markdown'
import { InlineError, PageLoading } from '../components/PageState'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Textarea } from '../components/ui/Field'
import { chatApi, parseChatActivity, parseChatMessage } from '../features/chat/api'
import type { ChatMessage } from '../features/chat/types'
import { record, stringValue } from '../lib/api/client'
import { cn, formatRelativeTime } from '../lib/utils'
import { useNabu } from '../state/NabuContext'

const starterPrompts = [
  'What should we focus on next?',
  'Summarize what changed today.',
  'Create a task for the highest-impact opportunity.',
]

function eventPayload(event: MessageEvent<string>) {
  const envelope = record(JSON.parse(event.data))
  const data = typeof envelope.data === 'string' ? record(JSON.parse(envelope.data)) : record(envelope.data)
  return { envelope, data }
}

function durableMessageId(id: string) {
  return /^\d+$/.test(id) ? BigInt(id) : null
}

function messageStatusRank(status: ChatMessage['status']) {
  if (status === 'pending') return 0
  if (status === 'queued') return 1
  if (status === 'processing' || status === 'streaming') return 2
  return 3
}

// History requests and live events intentionally overlap so reconnects cannot
// lose messages. Reconcile by durable ID instead of replacing the timeline:
// an older HTTP response must never erase a message that SSE or POST returned
// while that request was in flight.
function reconcileMessages(current: ChatMessage[], incoming: ChatMessage[]) {
  if (!current.length) return incoming
  if (!incoming.length) return current

  const incomingById = new Map(incoming.map((message) => [message.id, message]))
  const merged = current.map((message) => {
    const replacement = incomingById.get(message.id)
    if (!replacement) return message
    return messageStatusRank(replacement.status) >= messageStatusRank(message.status) ? replacement : message
  })
  const known = new Set(merged.map((message) => message.id))

  for (const message of incoming) {
    if (known.has(message.id)) continue
    const id = durableMessageId(message.id)
    const insertion = id === null ? -1 : merged.findIndex((candidate) => {
      const candidateId = durableMessageId(candidate.id)
      return candidateId !== null ? candidateId > id : candidate.id.startsWith('optimistic-') || candidate.id.startsWith('pending-')
    })
    if (insertion < 0) merged.push(message)
    else merged.splice(insertion, 0, message)
    known.add(message.id)
  }
  return merged
}

export function ChatPage() {
  const { activeScope, status } = useNabu()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [nextBeforeId, setNextBeforeId] = useState<string | undefined>()
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [loading, setLoading] = useState(true)
  const [sending, setSending] = useState(false)
  const [queueDepth, setQueueDepth] = useState(0)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [atBottom, setAtBottom] = useState(true)
  const timelineRef = useRef<HTMLDivElement | null>(null)
  const followLatestRef = useRef(true)
  const scopeIdRef = useRef(activeScope?.id)
  const activeMessageId = useRef<string | null>(null)
  const activeMessageRootId = useRef<string | null>(null)
  const optimisticId = useRef(0)
  const historyRequestId = useRef(0)
  const loadedOlderHistory = useRef(false)
  const [threadRoot, setThreadRoot] = useState<ChatMessage | null>(null)
  const [threadReplies, setThreadReplies] = useState<ChatMessage[]>([])
  const [threadHasMore, setThreadHasMore] = useState(false)
  const [threadBeforeId, setThreadBeforeId] = useState<string | undefined>()
  const [threadLoading, setThreadLoading] = useState(false)
  const [threadSending, setThreadSending] = useState(false)
  const [threadContent, setThreadContent] = useState('')
  const threadRequestId = useRef(0)
  const [deleteTarget, setDeleteTarget] = useState<ChatMessage | null>(null)
  const [deleting, setDeleting] = useState(false)
  const visibleMessages = useMemo(() => {
    if (!sending || messages.some((message) => message.role === 'assistant' && isThinking(message))) return messages
    const activeUser = [...messages].reverse().find((message) => message.role === 'user' && message.status === 'processing' && !message.parentMessageId && !message.threadRootId)
    if (!activeUser || activeUser.parentMessageId || activeUser.threadRootId) return messages
    const userIndex = messages.findIndex((message) => message.id === activeUser.id)
    if (messages.slice(userIndex + 1).some((message) => message.role === 'assistant')) return messages
    return [...messages, thinkingMessageFor(activeUser)]
  }, [messages, sending])

  const load = useCallback(async (scopeId?: string) => {
    const requestedScopeId = scopeId ?? scopeIdRef.current
    const requestId = ++historyRequestId.current
    try {
      const [history, queueStatus] = await Promise.all([chatApi.listMessages(), chatApi.getStatus()])
      if (requestedScopeId !== scopeIdRef.current || requestId !== historyRequestId.current) return
      setMessages((current) => reconcileMessages(current, history.messages))
      if (!loadedOlderHistory.current) {
        setHasMore(history.hasMore)
        setNextBeforeId(history.nextBeforeId)
      }
      setSending(queueStatus.working)
      setQueueDepth(queueStatus.queued)
      setError(null)
    } catch (caught) {
      if (requestedScopeId !== scopeIdRef.current || requestId !== historyRequestId.current) return
      setError(caught instanceof Error ? caught.message : 'Conversation history could not be loaded.')
    } finally {
      if (requestedScopeId === scopeIdRef.current && requestId === historyRequestId.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    scopeIdRef.current = activeScope?.id
    historyRequestId.current++
    loadedOlderHistory.current = false
    setMessages([])
    setThreadRoot(null)
    setThreadReplies([])
    setLoading(true)
    void load(activeScope?.id)
  }, [activeScope?.id, load])

  useEffect(() => {
    const events = new EventSource('/api/events')
    const replaceMessage = (raw: Event) => {
      try {
        const { envelope, data } = eventPayload(raw as MessageEvent<string>)
        const workspaceId = stringValue(envelope.workspace_id ?? data.workspace_id)
        if (workspaceId && scopeIdRef.current && workspaceId !== scopeIdRef.current) return
        const message = parseChatMessage(data.message ?? data)
        if (!message.id) return
        if (message.parentMessageId || message.threadRootId) {
          if (threadRoot?.id === (message.threadRootId ?? message.parentMessageId)) setThreadReplies((current) => current.some((item) => item.id === message.id) ? current.map((item) => item.id === message.id ? message : item) : [...current, message])
          return
        }
        setMessages((current) => reconcileMessages(current, [message]))
      } catch {
        // Persisted history remains authoritative if a live event is malformed.
      }
    }
    const started = (raw: Event) => {
      try {
        const { data } = eventPayload(raw as MessageEvent<string>)
        const id = stringValue(data.message_id ?? data.id)
        const rootId = stringValue(data.thread_root_id ?? data.parent_message_id)
        if (!id) return
        setSending(true)
        setQueueDepth((current) => Math.max(0, current - 1))
        activeMessageId.current = id
        activeMessageRootId.current = rootId || null
        if (rootId && threadRoot?.id === rootId) {
          setThreadReplies((current) => current.some((item) => item.id === id) ? current : [...current, { id, role: 'assistant', content: '', status: 'streaming', references: [], effects: [], activity: [], replyCount: 0, threadRootId: rootId }])
          return
        }
        setMessages((current) => current.some((item) => item.id === id) ? current : [...current, { id, role: 'assistant', content: '', status: 'streaming', references: [], effects: [], activity: [], replyCount: 0 }])
      } catch {
        // Ignore malformed transient state.
      }
    }
    const appendDelta = (raw: Event) => {
      try {
        const { data } = eventPayload(raw as MessageEvent<string>)
        const id = stringValue(data.message_id ?? data.id ?? activeMessageId.current)
        const delta = stringValue(data.delta ?? data.content ?? data.text)
        if (!id || !delta) return
        if (activeMessageRootId.current && threadRoot?.id === activeMessageRootId.current) {
          setThreadReplies((current) => current.map((item) => item.id === id ? { ...item, content: `${item.content}${delta}`, status: 'streaming' } : item))
          return
        }
        setMessages((current) => {
          const exists = current.some((item) => item.id === id)
          if (!exists) return [...current, { id, role: 'assistant', content: delta, status: 'streaming', references: [], effects: [], activity: [], replyCount: 0 }]
          return current.map((item) => item.id === id ? { ...item, content: `${item.content}${delta}`, status: 'streaming' } : item)
        })
      } catch {
        // Ignore malformed transient chunks.
      }
    }
    const appendActivity = (raw: Event) => {
      try {
        const { data } = eventPayload(raw as MessageEvent<string>)
        const id = stringValue(data.message_id ?? activeMessageId.current)
        const label = stringValue(data.label ?? data.message ?? data.summary)
        if (!id || !label) return
        const activity = parseChatActivity(data)
        const append = (message: ChatMessage): ChatMessage => {
          if (message.id !== id) return message
          const existing = activity.id ? message.activity.findIndex((item) => item.id === activity.id) : -1
          return { ...message, activity: existing >= 0 ? message.activity.map((item, index) => index === existing ? activity : item) : [...message.activity, activity] }
        }
        const rootId = stringValue(data.thread_root_id ?? data.parent_message_id ?? activeMessageRootId.current)
        if (rootId && threadRoot?.id === rootId) setThreadReplies((current) => current.map(append))
        else setMessages((current) => current.map(append))
      } catch {
        // Activity is transient; malformed events never replace durable chat text.
      }
    }
    const completed = () => {
      const completedMessageId = activeMessageId.current
      const completedRootId = activeMessageRootId.current
      activeMessageId.current = null
      activeMessageRootId.current = null
      setSending(false)
      if (completedRootId && threadRoot?.id === completedRootId) {
        if (completedMessageId) setThreadReplies((current) => current.filter((message) => message.id !== completedMessageId))
        void chatApi.getThread(completedRootId).then((page) => {
          setThreadRoot((current) => page.root ?? current)
          setThreadReplies((current) => reconcileMessages(current, page.messages.filter((item) => item.id !== completedRootId)))
          setThreadHasMore(page.hasMore)
          setThreadBeforeId(page.nextBeforeId)
        }).catch(() => void load())
      } else {
        if (completedMessageId) setMessages((current) => current.filter((message) => message.id !== completedMessageId))
        void load()
      }
    }
    events.onopen = () => void load(scopeIdRef.current)
    events.addEventListener('chat.started', started)
    events.addEventListener('chat.message', replaceMessage)
    events.addEventListener('chat.delta', appendDelta)
    events.addEventListener('chat.activity', appendActivity)
    events.addEventListener('chat.completed', completed)
    return () => {
      events.removeEventListener('chat.started', started)
      events.removeEventListener('chat.message', replaceMessage)
      events.removeEventListener('chat.delta', appendDelta)
      events.removeEventListener('chat.activity', appendActivity)
      events.removeEventListener('chat.completed', completed)
      events.onopen = null
      events.close()
    }
  }, [load, threadRoot?.id])

  useEffect(() => {
    if (!followLatestRef.current) return
    const element = timelineRef.current
    if (!element) return
    window.requestAnimationFrame(() => element.scrollTo({ top: element.scrollHeight, behavior: 'smooth' }))
  }, [visibleMessages])

  useEffect(() => {
    if (!sending || !followLatestRef.current) return
    const element = timelineRef.current
    if (!element) return
    window.requestAnimationFrame(() => element.scrollTo({ top: element.scrollHeight, behavior: 'smooth' }))
  }, [sending])

  const send = async (override?: string) => {
    const value = (override ?? content).trim()
    if (!value || submitting) return
    const optimistic: ChatMessage = {
      id: `optimistic-${++optimisticId.current}`,
      role: 'user',
      content: value,
      status: 'pending',
      references: [], effects: [], activity: [],
      replyCount: 0,
    }
    setMessages((current) => [...current, optimistic])
    setContent('')
    setSubmitting(true)
    followLatestRef.current = true
    setAtBottom(true)
    setError(null)
    try {
      const accepted = await chatApi.sendMessage(value)
      if (accepted.status === 'processing') setSending(true)
      if (accepted.status === 'queued') setQueueDepth((current) => current + 1)
      setMessages((current) => {
        const withoutOptimistic = current.filter((item) => item.id !== optimistic.id)
        return accepted.id ? reconcileMessages(withoutOptimistic, [accepted]) : withoutOptimistic
      })
    } catch (caught) {
      setMessages((current) => current.map((item) => item.id === optimistic.id ? { ...item, status: 'failed', error: 'Message was not sent.' } : item))
      setError(caught instanceof Error ? caught.message : 'Your message could not be sent.')
    } finally {
      setSubmitting(false)
    }
  }

  const continueFromCard = (message: string) => void send(message)

  const handleScroll = () => {
    const element = timelineRef.current
    if (!element) return
    const bottom = element.scrollHeight - element.scrollTop - element.clientHeight < 80
    followLatestRef.current = bottom
    setAtBottom(bottom)
  }

  const loadOlder = async () => {
    if (!nextBeforeId || loadingOlder) return
    setLoadingOlder(true)
    try {
      const page = await chatApi.listMessages(nextBeforeId)
      loadedOlderHistory.current = true
      setMessages((current) => reconcileMessages(current, page.messages))
      setHasMore(page.hasMore)
      setNextBeforeId(page.nextBeforeId)
    }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Older messages could not be loaded.') }
    finally { setLoadingOlder(false) }
  }

  const openThread = async (message: ChatMessage) => {
    const requestId = ++threadRequestId.current
    setThreadRoot(message); setThreadReplies([]); setThreadHasMore(false); setThreadBeforeId(undefined); setThreadLoading(true); setError(null)
    try {
      const page = await chatApi.getThread(message.id)
      if (requestId !== threadRequestId.current) return
      setThreadRoot(page.root ?? message); setThreadReplies(page.messages.filter((item) => item.id !== message.id)); setThreadHasMore(page.hasMore); setThreadBeforeId(page.nextBeforeId)
    } catch (caught) {
      if (requestId === threadRequestId.current) setError(caught instanceof Error ? caught.message : 'Thread could not be loaded.')
    } finally {
      if (requestId === threadRequestId.current) setThreadLoading(false)
    }
  }

  const loadOlderReplies = async () => {
    if (!threadRoot || !threadBeforeId || threadLoading) return
    setThreadLoading(true)
    try { const page = await chatApi.getThread(threadRoot.id, threadBeforeId); setThreadReplies((current) => [...page.messages.filter((item) => item.id !== threadRoot.id), ...current]); setThreadHasMore(page.hasMore); setThreadBeforeId(page.nextBeforeId) }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Older replies could not be loaded.') }
    finally { setThreadLoading(false) }
  }

  const sendReply = async (override?: string) => {
    const value = (override ?? threadContent).trim()
    if (!threadRoot || !value || threadLoading || threadSending) return
    setThreadSending(true); setError(null)
    try { const reply = await chatApi.sendThreadReply(threadRoot.id, value); if (reply.status === 'processing') setSending(true); if (reply.status === 'queued') setQueueDepth((current) => current + 1); setThreadReplies((current) => [...current, reply]); if (!override) setThreadContent(''); setMessages((current) => current.map((item) => item.id === threadRoot.id ? { ...item, replyCount: item.replyCount + 1 } : item)) }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Reply could not be sent.') }
    finally { setThreadSending(false) }
  }

  const closeThread = () => {
    threadRequestId.current++
    setThreadRoot(null)
    setThreadReplies([])
    setThreadContent('')
    setThreadLoading(false)
    setThreadSending(false)
  }

  const deleteMessage = async () => {
    if (!deleteTarget || deleting) return
    const target = deleteTarget
    const rootId = target.threadRootId ?? target.parentMessageId
    setDeleting(true)
    setError(null)
    try {
      await chatApi.deleteMessage(target.id)
      setMessages((current) => current
        .filter((item) => item.id !== target.id)
        .map((item) => rootId && item.id === rootId ? { ...item, replyCount: Math.max(0, item.replyCount - 1) } : item))
      setThreadReplies((current) => current.filter((item) => item.id !== target.id))
      if (threadRoot?.id === target.id) closeThread()
      else if (rootId && threadRoot?.id === rootId) setThreadRoot((current) => current ? { ...current, replyCount: Math.max(0, current.replyCount - 1) } : current)
      setDeleteTarget(null)
      void load(scopeIdRef.current)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The message could not be deleted.')
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className={cn('chat-page', threadRoot && 'chat-page-thread-open')}>
      <section className="chat-primary" aria-label="Conversation with Nabu">
        <div ref={timelineRef} className="chat-timeline" onScroll={handleScroll}>
        {loading ? <PageLoading label="Loading the conversation…" /> : null}
        {!loading && !messages.length ? (
          <div className="chat-welcome">
            <span className="flex size-12 items-center justify-center rounded-xl border border-line bg-panel text-accent"><Sparkles className="size-5" /></span>
            <h2 className="mt-4 text-balance text-lg font-semibold text-ink">What should Nabu move forward?</h2>
            <p className="mt-2 max-w-md text-pretty text-sm leading-relaxed text-muted">Ask for an explanation, change the current focus, create work, or review what has been accomplished.</p>
            <div className="mt-6 grid w-full max-w-xl gap-2 sm:grid-cols-3">
              {starterPrompts.map((prompt) => <button type="button" key={prompt} className="starter-prompt" onClick={() => void send(prompt)}>{prompt}</button>)}
            </div>
          </div>
        ) : null}
        <ol className="chat-message-list" aria-live="polite">
          {hasMore ? <li className="flex justify-center pb-3"><Button variant="secondary" size="sm" onClick={() => void loadOlder()} disabled={loadingOlder}>{loadingOlder ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : null}{loadingOlder ? 'Loading…' : 'Show older messages'}</Button></li> : null}
          {visibleMessages.map((message) => <ChatMessageRow key={message.id} message={message} onOpenThread={() => void openThread(message)} onRequestDelete={setDeleteTarget} onCardMessage={continueFromCard} contextSetupIncomplete={Boolean(activeScope && !activeScope.contextReady)} />)}
        </ol>
        </div>

        {!atBottom ? <Button variant="secondary" size="icon" className="chat-scroll-button" aria-label="Scroll to latest message" onClick={() => { followLatestRef.current = true; setAtBottom(true); timelineRef.current?.scrollTo({ top: timelineRef.current.scrollHeight, behavior: 'smooth' }) }}><ArrowDown className="size-4" /></Button> : null}

        <div className="chat-composer-zone">
          {activeScope && !activeScope.contextReady ? <div className="context-readiness-bar"><Sparkles className="size-4 shrink-0 text-accent" /><div className="min-w-0"><p className="text-xs font-medium text-ink">Context setup in progress</p><p className="mt-0.5 text-[11px] leading-relaxed text-muted">Nabu will gather the missing business, asset, account, and access details—and tell you when it has enough to begin.</p></div></div> : null}
          {error ? <div className="mx-auto mb-2 max-w-3xl"><InlineError message={error} /></div> : null}
          <ChatComposer value={content} onValueChange={setContent} onSend={() => void send()} busy={submitting} placeholder="Ask, steer, or share a stream of thoughts…" label="Message Nabu" sendLabel={sending ? 'Queue message' : 'Send message'} status={sending ? 'Nabu is replying · new messages will be queued' : queueDepth > 0 && status?.codexState && status.codexState !== 'available' ? `Waiting for Codex · ${queueDepth} ${queueDepth === 1 ? 'message' : 'messages'} safely queued${status.retryAt ? ` · retry ${formatRelativeTime(status.retryAt)}` : ''}` : queueDepth > 0 ? `${queueDepth} ${queueDepth === 1 ? 'message' : 'messages'} queued to begin shortly` : 'Enter to send · Shift+Enter for a new line'} autoFocus />
        </div>
      </section>
      {threadRoot ? <aside className="thread-panel" aria-label="Thread">
        <div className="thread-panel-header"><h2 className="text-base font-semibold text-ink">Thread</h2><Button variant="ghost" size="icon" aria-label="Close thread" onClick={closeThread}><X className="size-4" /></Button></div>
        <div className="thread-layout">
          <div className="thread-conversation-scroll">
            <div className="thread-root-section"><ol><ChatMessageRow message={threadRoot} inThread onRequestDelete={setDeleteTarget} onCardMessage={(message) => void sendReply(message)} contextSetupIncomplete={Boolean(activeScope && !activeScope.contextReady)} /></ol></div>
            <div className="thread-replies-label"><span className="thread-replies-badge">{threadRoot.replyCount || threadReplies.length} {threadRoot.replyCount === 1 ? 'reply' : 'replies'}</span></div>
            <div className="thread-replies-scroll">
            {threadHasMore ? <div className="mb-3 flex justify-center"><Button variant="secondary" size="sm" onClick={() => void loadOlderReplies()} disabled={threadLoading}>{threadLoading ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : null}Show older replies</Button></div> : null}
            <ol>{threadLoading && !threadReplies.length ? <li className="thread-loading-row" role="status"><LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /><span>Loading replies…</span></li> : null}{threadReplies.map((reply) => <ChatMessageRow key={reply.id} message={reply} inThread onRequestDelete={setDeleteTarget} onCardMessage={(message) => void sendReply(message)} />)}</ol>
            </div>
          </div>
          {error ? <div className="shrink-0 px-3 pt-3"><InlineError message={error} /></div> : null}
          <div className="thread-composer-zone"><ChatComposer value={threadContent} onValueChange={setThreadContent} onSend={() => void sendReply()} busy={threadLoading || threadSending} placeholder="Reply…" label="Reply in thread" sendLabel={sending ? 'Queue thread reply' : 'Send thread reply'} status={threadSending ? 'Sending reply…' : 'Enter to send · Shift+Enter for a new line'} /></div>
        </div>
      </aside> : null}
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => { if (!open && !deleting) setDeleteTarget(null) }}
        title={deleteTarget?.replyCount ? 'Delete message and thread?' : 'Delete message?'}
        description={deleteTarget?.replyCount ? `This will permanently delete this message and all ${deleteTarget.replyCount} ${deleteTarget.replyCount === 1 ? 'reply' : 'replies'} in its thread. This cannot be undone.` : 'This will permanently delete this message. This cannot be undone.'}
        details={deleteTarget?.content ? <p className="line-clamp-3 whitespace-pre-wrap">{deleteTarget.content}</p> : undefined}
        confirmLabel={deleteTarget?.replyCount ? 'Delete message and thread' : 'Delete message'}
        destructive
        pending={deleting}
        onConfirm={() => void deleteMessage()}
      />
    </div>
  )
}

function ChatComposer({ value, onValueChange, onSend, busy, placeholder, label, sendLabel, status, autoFocus = false }: { value: string; onValueChange: (value: string) => void; onSend: () => void; busy: boolean; placeholder: string; label: string; sendLabel: string; status: string; autoFocus?: boolean }) {
  return (
    <div className="chat-composer" data-busy={busy || undefined}>
      <Textarea value={value} onChange={(event) => onValueChange(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); onSend() } }} placeholder={placeholder} aria-label={label} rows={1} autoSizeMin={44} autoSizeMax={180} className="chat-composer-input" autoFocus={autoFocus} />
      <div className="chat-composer-toolbar">
        <span className="chat-composer-status" aria-live="polite">{status}</span>
        <Button variant="primary" size="icon" className="chat-composer-send" onClick={onSend} disabled={!value.trim() || busy} aria-label={sendLabel}>{busy ? <LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" /> : <ArrowUp className="size-4" />}</Button>
      </div>
    </div>
  )
}

function ChatMessageRow({ message, onOpenThread, onRequestDelete, onCardMessage, contextSetupIncomplete = false, inThread = false }: { message: ChatMessage; onOpenThread?: () => void; onRequestDelete?: (message: ChatMessage) => void; onCardMessage?: (message: string) => void; contextSetupIncomplete?: boolean; inThread?: boolean }) {
  const user = message.role === 'user'
  const thinking = !user && isThinking(message)
  const legacyContextOffer = contextSetupIncomplete && !user && !message.references.some((reference) => reference.type === 'context_approval') && offersContextApproval(message.content)
  const references = legacyContextOffer ? [...message.references, { id: 'current-workspace', type: 'context_approval', title: 'Workspace context', status: 'pending' }] : message.references
  const setupReferences = references.filter((reference) => (reference.type === 'integration' && reference.status !== 'ready') || reference.type === 'secret')
  const entityReferences = references.filter((reference) => !setupReferences.includes(reference))
  const hasBubbleContent = Boolean(thinking || message.recoveryTask || message.content || message.error || message.effects.length || references.length)
  const deletable = !message.id.startsWith('optimistic-') && message.status !== 'pending' && message.status !== 'processing' && message.status !== 'streaming'
  return (
    <li className={cn('chat-message', user && 'chat-message-user', inThread && 'chat-message-thread')}>
      {!user ? <div className={cn('chat-avatar', thinking && 'chat-avatar-working')}><img src="/assets/nabu-owl.png" alt="Nabu" className="size-full object-contain" /></div> : null}
      <article className="min-w-0 flex-1">
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <h2 className="text-xs font-semibold text-ink">{user ? 'You' : 'Nabu'}</h2>
          {message.createdAt ? <span className="text-[11px] text-muted">{formatRelativeTime(message.createdAt)}</span> : null}
          {message.status === 'queued' ? <Badge variant="outline">Queued</Badge> : null}
        </div>
        {hasBubbleContent ? <div className={cn('chat-bubble', user && 'chat-bubble-user', thinking && 'chat-bubble-thinking')}>
          {thinking ? <div className="chat-thinking" role="status" aria-label="Nabu is thinking"><span className="chat-thinking-pulse" aria-hidden="true"><i /><i /><i /></span><span>Thinking</span></div> : null}
          {message.recoveryTask ? <Link className="chat-recovery-card" to={`/tasks/${encodeURIComponent(message.recoveryTask.id)}`} aria-label={`Open task: ${message.recoveryTask.title}`}>
            <span className="chat-recovery-icon"><ListRestart className="size-4" aria-hidden="true" /></span>
            <span className="min-w-0 flex-1"><span className="chat-recovery-label">Continue task</span><strong>{message.recoveryTask.title}</strong></span>
            <ArrowUpRight className="size-4 shrink-0 text-muted" aria-hidden="true" />
          </Link> : null}
          {message.content ? <Markdown className={!user ? 'chat-assistant-markdown' : undefined}>{message.content}</Markdown> : null}
          {message.error ? <p className="mt-2 text-xs text-danger">{message.error}</p> : null}
          {message.effects.some((effect) => effect.type !== 'conversation_only') ? <div className="chat-effects">{message.effects.filter((effect) => effect.type !== 'conversation_only').map((effect, index) => effect.actions?.length
            ? <ChatActionCard key={`${effect.type}-${index}`} title={effect.summary} description={effect.details?.[0]} actions={effect.actions} onAction={(action) => onCardMessage?.(action.value)} />
            : <div key={`${effect.type}-${index}`} className={cn('chat-effect', effect.details?.length && 'chat-effect-detailed')}><CheckEffectIcon /><div className="min-w-0"><p className="chat-effect-summary">{effect.summary}</p>{effect.details?.length ? <ul className="mt-1 list-disc pl-4 text-xs leading-relaxed text-muted">{effect.details.map((detail) => <li key={detail}>{detail}</li>)}</ul> : null}</div></div>)}</div> : null}
          {entityReferences.length ? <div className="mt-3 space-y-1.5">{entityReferences.map((reference) => <EntityReferenceCard key={`${reference.type}-${reference.id}`} reference={reference} compact fluid showStatus={false} onMessage={onCardMessage} />)}</div> : null}
          {setupReferences.length ? <div className="chat-setup-references">{setupReferences.map((reference) => <EntityReferenceCard key={`${reference.type}-${reference.id}`} reference={reference} fluid onMessage={onCardMessage} />)}</div> : null}
          {!user && !inThread && onOpenThread ? <button type="button" className="thread-button" aria-label="Reply in thread" title={message.replyCount ? `Open thread · ${message.replyCount} ${message.replyCount === 1 ? 'reply' : 'replies'}` : 'Reply in thread'} onClick={onOpenThread}><MessageSquare className="size-4" aria-hidden="true" /></button> : null}
        </div> : null}
        {deletable && onRequestDelete ? <button type="button" className="chat-delete-button" aria-label={`Delete ${user ? 'your' : "Nabu's"} message`} title="Delete message" onClick={() => onRequestDelete(message)}><Trash2 className="size-3.5" aria-hidden="true" /></button> : null}
      </article>
    </li>
  )
}

function isThinking(message: ChatMessage) {
  return message.status === 'pending' || message.status === 'processing' || message.status === 'streaming'
}

function thinkingMessageFor(userMessage: ChatMessage): ChatMessage {
  return {
    id: `pending-${userMessage.id}`,
    role: 'assistant',
    content: '',
    status: 'processing',
    references: [],
    effects: [],
    activity: [],
    replyCount: 0,
  }
}

function offersContextApproval(content: string) {
  const normalized = content.toLowerCase()
  return (normalized.includes('enough context') || normalized.includes('context as ready')) &&
    (normalized.includes('confirm') || normalized.includes('should i treat') || normalized.includes('approve'))
}

function CheckEffectIcon() {
  return <span className="chat-effect-icon"><ArrowUp className="size-3.5" /></span>
}
