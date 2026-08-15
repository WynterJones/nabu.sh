// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Markdown } from './Markdown'

describe('Markdown', () => {
  it('renders accessible code blocks and copies their contents', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    render(<Markdown>{'```ts\nconst mission = "focused"\n```'}</Markdown>)
    expect(screen.getByText('const mission = "focused"')).toBeInTheDocument()
    expect(screen.getByRole('code').closest('pre')).toHaveAttribute('tabindex', '0')
    fireEvent.click(screen.getByRole('button', { name: 'Copy code block' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('const mission = "focused"'))
    expect(screen.getByText('Copied')).toBeInTheDocument()
  })

  it('renders redacted values as an accessible privacy badge outside code', () => {
    render(<Markdown>{'The key is [REDACTED], while `[REDACTED]` is sample code.'}</Markdown>)
    expect(screen.getByRole('note', { name: 'Sensitive value redacted' })).toBeInTheDocument()
    expect(screen.getByText('[REDACTED]')).toHaveClass('markdown-inline-code')
  })

  it('keeps report tables horizontally scrollable and truncates long cells', () => {
    render(<Markdown>{'| Repository | Fingerprint | Severity |\n| --- | --- | --- |\n| example/repository | edf687f759ec9765bd5db185dbc615c80af77d6e7e19386fc42934e7a80307af | High |'}</Markdown>)
    const scroller = screen.getByRole('table').parentElement
    expect(scroller).toHaveClass('markdown-table-scroll')
    expect(scroller).toHaveAttribute('tabindex', '0')
    expect(screen.getByTitle('edf687f759ec9765bd5db185dbc615c80af77d6e7e19386fc42934e7a80307af')).toHaveClass('markdown-table-cell')
  })

  it('pretty prints and highlights standalone JSON reports', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const source = '{"generatedAt":"2026-08-13T23:26:24Z","results":[{"band":"small","rows":10000,"passed":true}]}'
    const formatted = JSON.stringify(JSON.parse(source), null, 2)
    const { container } = render(<Markdown>{source}</Markdown>)
    const block = container.querySelector('.markdown-json')
    expect(block).toHaveAttribute('aria-label', 'Formatted JSON')
    expect(block?.querySelector('pre')).toHaveAttribute('tabindex', '0')
    expect(block?.querySelector('pre')?.textContent).toBe(formatted)
    expect(block?.querySelector('.json-token-key')).toHaveTextContent('"generatedAt"')
    expect(block?.querySelector('.json-token-number')).toHaveTextContent('10000')
    fireEvent.click(screen.getByRole('button', { name: 'Copy JSON' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(formatted))
  })
})
