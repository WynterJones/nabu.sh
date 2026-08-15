import { describe, expect, it } from 'vitest'
import { databaseCellText, databaseCellURL } from './DatabasePage'

describe('database cell presentation', () => {
  it('formats structured values without losing their full contents', () => {
    expect(databaseCellText({ tags: ['research', 'verified'] })).toBe('{"tags":["research","verified"]}')
    expect(databaseCellText(true)).toBe('True')
    expect(databaseCellText(null)).toBe('')
  })

  it('only turns safe web URLs into external links', () => {
    expect(databaseCellURL('https://example.com/research?q=nabu')).toBe('https://example.com/research?q=nabu')
    expect(databaseCellURL('http://localhost:3000')).toBe('http://localhost:3000/')
    expect(databaseCellURL('javascript:alert(1)')).toBeNull()
    expect(databaseCellURL('mailto:hello@example.com')).toBeNull()
    expect(databaseCellURL('not a URL')).toBeNull()
  })
})
