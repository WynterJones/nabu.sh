import { describe, expect, it } from 'vitest'
import { parseWorkspaceOutputs } from './api'

describe('outputs API', () => {
  it('parses workspace files, sites, and runnable tools', () => {
    const value = parseWorkspaceOutputs({
      items: [{ id: 'artifact-1', kind: 'document', name: 'Plan', path: 'documents/plan.md', file_kind: 'text', mime_type: 'text/markdown', size: 120, editable: true, task_id: 'task-1', task_title: 'Create plan' }],
      scripts: [{ id: 'script-1', name: 'Check site', enabled: true, access: 'read', secret_bindings: [] }],
    })
    expect(value.items).toEqual([expect.objectContaining({ id: 'artifact-1', path: 'documents/plan.md', fileKind: 'text', editable: true, taskId: 'task-1' })])
    expect(value.scripts).toEqual([expect.objectContaining({ id: 'script-1', name: 'Check site', enabled: true })])
  })
})
