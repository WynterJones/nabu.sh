import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, parseRun, parseSetupChecks, parseStatus, parseTask, parseTasks } from './api'

afterEach(() => vi.unstubAllGlobals())

describe('API normalization', () => {
  it('parses the status snapshot contract', () => {
    expect(parseStatus({
      status: 'working',
      display_name: 'Nabu Local',
      setup_complete: true,
      mission_started: true,
      paused: false,
      codex_available: true,
      codex_state: 'available',
      codex_reason: '',
      service_healthy: true,
      disk_free_bytes: 8_589_934_592,
      last_backup_at: '2026-08-12T09:00:00Z',
      active_task_id: 'task-1',
      ready_count: 2,
      needs_attention: 1,
      chat_queued: 1,
      activities: [{ kind: 'chat', label: '1 Chat message queued', status: 'waiting', detail: 'Codex is rate limited.' }],
    })).toMatchObject({
      status: 'working',
      name: 'Nabu Local',
      setupComplete: true,
      missionStarted: true,
      codexAvailable: true,
      activeTaskId: 'task-1',
      readyCount: 2,
      needsAttention: 1,
      serviceHealthy: true,
      diskFreeBytes: 8_589_934_592,
      lastBackupAt: '2026-08-12T09:00:00Z',
      chatQueued: 1,
      activities: [{ kind: 'chat', label: '1 Chat message queued', status: 'waiting', detail: 'Codex is rate limited.' }],
    })
  })

  it('parses plain task arrays and checklist strings', () => {
    const [task] = parseTasks([{
      id: 'task-1',
      title: 'Review acquisition funnel',
      status: 'ready',
      priority: 'high',
      why: 'Supports paid adoption',
      depends_on_task_ids: ['task-research', 'task-data'],
      planned_at: '2026-08-20T14:00:00Z',
      definition_of_done: ['Find the drop-off', 'Document evidence'],
    }])
    expect(task).toMatchObject({ id: 'task-1', status: 'ready', priority: 'high', whyThisMatters: 'Supports paid adoption' })
    expect(task.definitionOfDone).toEqual([
      { label: 'Find the drop-off', complete: false },
      { label: 'Document evidence', complete: false },
    ])
    expect(task.plannedAt).toBe('2026-08-20T14:00:00Z')
    expect(task.dependsOnTaskIds).toEqual(['task-research', 'task-data'])
  })

  it('surfaces a failed verification detail as the task failure reason', () => {
    const task = parseTask({
      id: 'task-failed',
      title: 'Prepare launch',
      status: 'failed',
      result: {
        summary: 'Launch preparation remains blocked.',
        verification: [
          { name: 'Documentation', status: 'passed' },
          { name: 'Site source inventory', status: 'failed', details: 'The workspace contains no site source.' },
        ],
        uncertainties: ['The production domain is unconfirmed.'],
      },
    })
    expect(task.failureReason).toBe('The workspace contains no site source.')
    expect(task.verification[1]).toContain('Site source inventory (failed)')
    expect(task.uncertainties).toEqual(['The production domain is unconfirmed.'])
  })

  it('parses granular Definition of Done outcomes', () => {
    const task = parseTask({
      id: 'task-checklist',
      title: 'Verify the release',
      status: 'failed',
      definition_of_done: [
        { text: 'Build passes', completed: true, details: 'Exit code 0.' },
        { text: 'Browser check passes', completed: false, failed: true, details: 'Browser unavailable.' },
      ],
    })
    expect(task.definitionOfDone).toEqual([
      { label: 'Build passes', complete: true, failed: false, details: 'Exit code 0.' },
      { label: 'Browser check passes', complete: false, failed: true, details: 'Browser unavailable.' },
    ])
  })

  it('parses nested setup checks', () => {
    expect(parseSetupChecks({
      codex: { available: true, path: '/usr/local/bin/codex', version: '1.0' },
      git: { available: false, error: 'Git was not found' },
    })).toEqual([
      { key: 'codex', label: 'Codex CLI', ok: true, detail: '1.0' },
      { key: 'git', label: 'Git', ok: false, detail: 'Git was not found' },
    ])
  })

  it('derives readable activity from persisted run fields', () => {
    const run = parseRun({
      id: 'run-1',
      status: 'completed',
      working_directory: '/workspace',
      started_at: '2026-08-11T10:00:00Z',
      ended_at: '2026-08-11T10:01:00Z',
      raw_output: 'tests passed',
      result: { summary: 'Verified the change.' },
    })
    expect(run.output).toBe('tests passed')
    expect(run.events.map((event) => event.type)).toEqual(['started', 'result'])
  })

  it('serializes prerequisite task ids when creating a task', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      id: 'task-new', title: 'Prepare launch brief', purpose: 'Create the brief.', status: 'idea', priority: 'normal', definition_of_done: ['Brief approved'], depends_on_task_ids: ['task-research'],
    }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await api.createTask({
      title: 'Prepare launch brief',
      purpose: 'Create the brief.',
      priority: 'normal',
      definitionOfDone: ['Brief approved'],
      dependsOnTaskIds: ['task-research'],
    })

    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body))).toMatchObject({
      definition_of_done: ['Brief approved'],
      depends_on_task_ids: ['task-research'],
    })
  })
})
