import { afterEach, describe, expect, it, vi } from 'vitest'
import { chatApi, parseChatActivity, parseChatMessage } from './chat/api'
import { parseApproval } from './approvals/api'
import { parseCodexModelCatalog, parseMemory, parseOperatorSettings, parsePolicy, parseSchedule, parseScript, parseServiceHealth, settingsApi } from './settings/api'
import { scopesApi } from './scopes/api'
import { parseSavedSecret } from './secrets/api'

afterEach(() => vi.unstubAllGlobals())

describe('phase 6–10 API parsers', () => {
  it('normalizes durable chat messages and thread metadata', () => {
    expect(parseChatMessage({
      id: 'm2', role: 'assistant', content: '**Done**', status: 'complete', parent_message_id: 'm1', thread_root_id: 'm1', reply_count: 2,
      effects: [{ type: 'create_task', summary: 'Created the investigation task.' }],
    })).toMatchObject({ id: 'm2', role: 'assistant', parentMessageId: 'm1', threadRootId: 'm1', replyCount: 2, effects: [{ type: 'create_task' }] })
  })

  it('preserves durable queued and processing message states', () => {
    expect(parseChatMessage({ id: 'm3', role: 'user', content: 'Next request', status: 'queued' }).status).toBe('queued')
    expect(parseChatMessage({ id: 'm4', role: 'user', content: 'Current request', status: 'processing' }).status).toBe('processing')
  })

  it('turns task recovery metadata into linked context without exposing the transport prefix', () => {
    expect(parseChatMessage({
      id: 'recovery-1', role: 'user', status: 'processing',
      content: 'Continue failed task: Repair the integration\n\nNew context:\nThe API access is ready now.',
      effect_metadata: { recovery_task_id: 'task-1' },
    })).toMatchObject({
      content: 'The API access is ready now.',
      recoveryTask: { id: 'task-1', title: 'Repair the integration' },
    })
  })

  it('decodes effect metadata returned as a JSON object', () => {
    expect(parseChatMessage({
      id: 42,
      role: 'assistant',
      content: 'I added the task.',
      effect_metadata: {
        effects: [{ type: 'create_task', summary: 'Created a durable task.', entity: { id: 't1', type: 'task', title: 'Audit acquisition' } }],
        references: [{ id: 'r1', type: 'report', title: 'Acquisition findings' }],
      },
    })).toMatchObject({
      id: '42',
      effects: [{ type: 'create_task', entity: { id: 't1' } }],
      references: [{ id: 'r1', type: 'report' }],
    })
  })

  it('normalizes automatic task lifecycle references, including artifact labels', () => {
    expect(parseChatMessage({
      id: 'lifecycle-1', role: 'assistant', content: 'Completed the research task.',
      effect_metadata: { automated_lifecycle: true, references: [
        { id: 'task-1', type: 'task', label: 'Research competitors' },
        { id: 'artifact-1', type: 'artifact', label: 'Competitor matrix' },
      ] },
    }).references).toMatchObject([
      { id: 'task-1', type: 'task', title: 'Research competitors' },
      { id: 'artifact-1', type: 'artifact', title: 'Competitor matrix' },
    ])
  })

  it('decodes bounded chat choice actions', () => {
    expect(parseChatMessage({
      id: 'choice-1', role: 'assistant', content: 'Pick the first workstream.',
      effects: [{ type: 'request_choice', summary: 'Which repository first?', actions: [
        { label: 'Marketing site', value: 'Inspect the marketing repository first.', primary: true },
        { label: 'Application', value: 'Inspect the application repository first.' },
      ] }],
    }).effects[0].actions).toEqual([
      { label: 'Marketing site', value: 'Inspect the marketing repository first.', description: undefined, primary: true },
      { label: 'Application', value: 'Inspect the application repository first.', description: undefined, primary: false },
    ])
  })

  it('normalizes bounded transient chat activity without exposing raw output', () => {
    expect(parseChatActivity({ activity_id: 'a1', type: 'validation', label: 'Validating proposed changes', at: '2026-08-12T12:00:00Z' })).toEqual({
      id: 'a1', type: 'validation', label: 'Validating proposed changes', status: undefined, createdAt: '2026-08-12T12:00:00Z',
    })
  })

  it('requests cursor-paginated top-level chat messages', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ messages: [{ id: 'm1', role: 'user', content: 'Hello' }], has_more: true, next_before_id: 'm0' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const page = await chatApi.listMessages('m2')
    expect(fetchMock).toHaveBeenCalledWith('/api/chat/messages?limit=10&before_id=m2', expect.any(Object))
    expect(page).toMatchObject({ hasMore: true, nextBeforeId: 'm0', messages: [{ id: 'm1', replyCount: 0 }] })
  })

  it('deletes a durable chat message through the message resource', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await chatApi.deleteMessage('message/with spaces')
    expect(fetchMock).toHaveBeenCalledWith('/api/chat/messages/message%2Fwith%20spaces', expect.objectContaining({ method: 'DELETE' }))
  })

  it('normalizes approval evidence and related tasks', () => {
    expect(parseApproval({ id: 'a1', proposed_action: 'Deploy preview', status: 'pending', task: { id: 't1', title: 'Prepare page' }, changes: ['One preview'], verification: [{ name: 'Build', details: 'passed' }] })).toMatchObject({ id: 'a1', action: 'Deploy preview', taskId: 't1', changes: ['One preview'], evidence: ['passed'] })
  })

  it('normalizes policy, schedules, scripts, and memory contracts', () => {
    expect(parsePolicy({ read: 'allow', work: 'allow', publish: 'ask', dangerous: 'deny' })).toMatchObject({ publish: 'ask', dangerous: 'deny' })
    expect(parseSchedule({ id: 's1', name: 'Health', kind: 'script', cadence: { interval_seconds: 3600 }, payload: { script_id: 'site-health' }, enabled: true })).toMatchObject({ triggerType: 'script', cadence: { intervalSeconds: 3600 } })
    expect(parseSchedule({ id: 's2', name: 'Orient', kind: 'orient', expression: '0 9 * * 1-5', enabled: true, last_error: 'Previous dispatch failed.' })).toMatchObject({ triggerType: 'orientation', cadence: { expression: '0 9 * * 1-5' }, lastStatus: 'failed', lastError: 'Previous dispatch failed.' })
    expect(parseScript({ id: 'script-1', name: 'site-health', enabled: true, timeout_seconds: 120, secret_bindings: [{ secret_id: 'secret-1', env_var: 'PLAUSIBLE_TOKEN' }] })).toMatchObject({ enabled: true, timeoutSeconds: 120, secretBindings: [{ secretId: 'secret-1', envVar: 'PLAUSIBLE_TOKEN' }] })
    expect(parseMemory({ content: '# Decisions', daily_notes: [{ date: '2026-08-12', summary: 'Kept the queue small.' }] })).toMatchObject({ body: '# Decisions', dailyNotes: [{ date: '2026-08-12' }] })
  })

  it('keeps saved secret values out of parsed frontend state', () => {
    expect(parseSavedSecret({ id: 'secret-1', name: 'Deploy token', value: 'must-not-render', configured: true, binding_count: 2 })).toEqual({
      id: 'secret-1', name: 'Deploy token', label: undefined, description: undefined, configured: true, createdAt: undefined, updatedAt: undefined, bindingCount: 2,
    })
  })

  it('writes script secret bindings with the provisional snake-case contract', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 'script-1', name: 'Deploy', secret_bindings: [{ secret_id: 'secret-1', env_var: 'DEPLOY_TOKEN' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await settingsApi.updateScript('script-1', { secretBindings: [{ secretId: 'secret-1', envVar: 'DEPLOY_TOKEN' }] })

    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body))).toEqual({ secret_bindings: [{ secret_id: 'secret-1', env_var: 'DEPLOY_TOKEN' }] })
  })

  it('keeps resolved memory history out of the proposal review queue', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify([
      { id: 'done', content: 'Already appended.', status: 'applied' },
      { id: 'pending', content: 'Needs review.', status: 'proposed' },
    ]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(settingsApi.listMemoryUpdates()).resolves.toMatchObject([{ id: 'pending', status: 'proposed' }])
  })

  it('parses the service status snapshot health fields', () => {
    expect(parseServiceHealth({
      status: 'needs_attention',
      codex_available: false,
      codex_state: 'rate_limited',
      codex_reason: 'Retry window is active.',
      codex_retry_at: '2026-08-12T12:00:00Z',
      service_healthy: true,
      disk_free_bytes: 8_589_934_592,
      last_backup_at: '2026-08-12T10:00:00Z',
    })).toEqual({
      status: 'needs_attention',
      codexState: 'rate_limited',
      codexMessage: 'Retry window is active.',
      retryAt: '2026-08-12T12:00:00Z',
      serviceHealthy: true,
      diskFreeBytes: 8_589_934_592,
      backupAt: '2026-08-12T10:00:00Z',
    })
  })

  it('preserves default and all supported Codex reasoning efforts', () => {
    expect(parseOperatorSettings({ codex_model: '', codex_reasoning_effort: '' })).toMatchObject({ codexReasoningEffort: '', maxParallelTasks: 1 })
    for (const effort of ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']) {
      expect(parseOperatorSettings({ codex_reasoning_effort: effort }).codexReasoningEffort).toBe(effort)
    }
    expect(parseOperatorSettings({ max_parallel_tasks: 8 }).maxParallelTasks).toBe(8)
    expect(parseOperatorSettings({ max_parallel_tasks: 12 }).maxParallelTasks).toBe(1)
  })

  it('parses the installed Codex model catalog and effort capabilities', () => {
    expect(parseCodexModelCatalog({ source: 'codex', models: [{ id: 'gpt-5.6-sol', display_name: 'GPT-5.6 Sol', default_reasoning_effort: 'high', supported_reasoning_efforts: ['low', 'high', 'ultra'] }] })).toEqual({ source: 'codex', models: [{ id: 'gpt-5.6-sol', displayName: 'GPT-5.6 Sol', description: undefined, defaultReasoningEffort: 'high', supportedReasoningEfforts: ['low', 'high', 'ultra'] }] })
  })

  it('writes nested cadence and the workspace PATCH/create contracts', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 's1', name: 'Orient', kind: 'orient', cadence: { interval_seconds: 3600 }, enabled: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'w1', name: 'Renamed', path: '/work', active: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'w2', name: 'New', path: '/new', active: false }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await settingsApi.updateSchedule('s1', { cadence: { intervalSeconds: 3600 } })
    await scopesApi.update('w1', { name: 'Renamed' })
    await scopesApi.create({ name: 'New', path: '/new', mode: 'create' })

    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body))).toEqual({ cadence: { interval_seconds: 3600 } })
    expect(fetchMock.mock.calls[1][0]).toBe('/api/scopes/w1')
    expect(fetchMock.mock.calls[1][1]).toMatchObject({ method: 'PATCH' })
    expect(JSON.parse(String(fetchMock.mock.calls[2][1].body))).toEqual({ name: 'New', path: '/new', mode: 'create' })
  })

  it('sends max reasoning effort and retains the returned value', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ codex_model: 'gpt-5', codex_reasoning_effort: 'max', max_parallel_tasks: 2 }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(settingsApi.updateOperatorSettings({ codexModel: ' gpt-5 ', codexReasoningEffort: 'max', maxParallelTasks: 2 })).resolves.toMatchObject({ codexModel: 'gpt-5', codexReasoningEffort: 'max', maxParallelTasks: 2 })
    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body))).toEqual({ codex_model: 'gpt-5', codex_reasoning_effort: 'max', max_parallel_tasks: 2 })
  })
})
