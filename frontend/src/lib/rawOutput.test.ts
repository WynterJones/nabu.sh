import { describe, expect, it } from 'vitest'
import { formatRawOutput } from './rawOutput'

describe('formatRawOutput', () => {
  it('formats adjacent JSON events as a readable stream', () => {
    const formatted = formatRawOutput('{"type":"item.started","item":{"text":"a }{ value"}}{"type":"item.completed"}')
    expect(formatted).toMatchObject({ structured: true, entries: 2 })
    expect(formatted.text).toContain('\n  "type": "item.started"')
    expect(formatted.text).toContain('\n\n{\n  "type": "item.completed"')
  })

  it('preserves unstructured terminal output exactly', () => {
    const output = 'Build complete\nwarning: check this file'
    expect(formatRawOutput(output)).toEqual({ text: output, structured: false, entries: 0 })
  })
})
