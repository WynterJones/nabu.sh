import { describe, expect, it } from 'vitest'
import { statusMascotSource } from './NabuStatusMascot'
import type { StatusResponse, Task, TaskStatus } from '../types'

const status = (value: StatusResponse['status'], paused = false): StatusResponse => ({
  status: value,
  paused,
  setupComplete: true,
  name: 'Nabu',
})

const task = (value: TaskStatus): Task => ({
  id: value,
  title: value,
  status: value,
  priority: 'normal',
  definitionOfDone: [],
  verification: [],
  uncertainties: [],
  artifacts: [],
  artifactFiles: [],
  filesChanged: [],
})

describe('statusMascotSource', () => {
  it('maps the operator states to the matching Nabu mascot', () => {
    expect(statusMascotSource(status('working'), [])).toContain('active')
    expect(statusMascotSource(status('waiting'), [])).toContain('awaiting-approval')
    expect(statusMascotSource(status('idle'), [task('waiting')])).toContain('asking-question')
    expect(statusMascotSource(status('idle'), [task('failed')])).toContain('asking-question')
    expect(statusMascotSource(status('idle'), [task('completed')])).toContain('success')
    expect(statusMascotSource(status('idle'), [])).toContain('idle')
  })
})
