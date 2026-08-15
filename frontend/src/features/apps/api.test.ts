import { afterEach, describe, expect, it, vi } from 'vitest'
import { splitCommandLine } from '../../pages/AppsPage'
import { appsApi, parseLocalApp } from './api'

afterEach(() => vi.unstubAllGlobals())

describe('local apps API', () => {
  it('normalizes runtime state and direct argv', () => {
    expect(parseLocalApp({ id: 'app-1', name: 'Toolbox', directory: 'repos/toolbox', command: ['npm', 'run', 'dev'], port: 4173, health_path: '/health', auto_start: true, status: 'running', healthy: true, url: 'http://127.0.0.1:4173' })).toMatchObject({
      id: 'app-1', name: 'Toolbox', command: ['npm', 'run', 'dev'], healthPath: '/health', autoStart: true, status: 'running', healthy: true,
    })
  })

  it('sends an argv vector rather than a shell expression', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 'app-1', name: 'Toolbox', directory: 'repos/toolbox', command: ['npm', 'run', 'dev'], port: 4173, status: 'stopped' }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await appsApi.create({ name: 'Toolbox', directory: 'repos/toolbox', command: ['npm', 'run', 'dev'], port: 4173, healthPath: '/', autoStart: false })
    expect(JSON.parse(String((fetchMock.mock.calls[0][1] as RequestInit).body))).toMatchObject({ command: ['npm', 'run', 'dev'], health_path: '/', auto_start: false })
  })

  it('parses quoted command arguments without executing syntax', () => {
    expect(splitCommandLine('npm run dev -- --host "127.0.0.1" --title "Helpful tools"')).toEqual(['npm', 'run', 'dev', '--', '--host', '127.0.0.1', '--title', 'Helpful tools'])
    expect(splitCommandLine('sh -c "echo hello | tee output"')).toEqual(['sh', '-c', 'echo hello | tee output'])
    expect(() => splitCommandLine('npm "unfinished')).toThrow(/unclosed quote/)
  })
})
