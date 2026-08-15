// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

afterEach(cleanup)

const css = readFileSync(resolve(process.cwd(), 'src/styles.css'), 'utf8')

describe('app scroll surfaces', () => {
  for (const width of [360, 768, 1280]) {
    it(`keeps the shared Task, Report, Database, and Settings page scroller native at ${width}px`, () => {
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
      render(<main data-testid="main-scroll" className="app-main"><div className="page-stack min-w-0">Scrollable page</div></main>)

      expect(screen.getByTestId('main-scroll')).toHaveClass('app-main')
      expect(css).toMatch(/\.app-main\s*\{\s*@apply[^;]*min-h-0[^;]*min-w-0[^;]*overflow-y-auto[^;]*overflow-x-hidden/)
      expect(css).toMatch(/\.page-stack\s*\{\s*@apply[^;]*w-full[^;]*min-w-0/)
    })
  }

  it('themes the actual app scroller with standards and a WebKit fallback', () => {
    expect(css).toContain('.app-main,')
    expect(css).toMatch(/scrollbar-color:\s*var\(--scroll-thumb\)\s+var\(--scroll-track\)/)
    expect(css).toMatch(/\.app-main::-webkit-scrollbar/)
    expect(css).toMatch(/scrollbar-gutter:\s*stable/)
    expect(css).not.toMatch(/\*\s*\{[^}]*scrollbar-width/s)
  })

  it('keeps representative page grids shrinkable across the target breakpoints', () => {
    expect(css).toMatch(/\.detail-grid\s*\{[^}]*grid-cols-1[^}]*lg:grid-cols-\[minmax\(0,1fr\)_280px\]/)
    expect(css).toMatch(/\.report-detail-grid\s*\{[^}]*grid-cols-1[^}]*xl:grid-cols-\[minmax\(0,1fr\)_300px\]/)
    expect(css).toMatch(/\.settings-layout\s*\{[^}]*grid-cols-1[^}]*md:grid-cols-\[180px_minmax\(0,1fr\)\]/)
    expect(css).toMatch(/\.database-ag-grid\s*\{[^}]*min-w-0[^}]*md:block/)
    expect(css).toContain('.database-ag-grid .ag-body-viewport,')
    expect(css).toMatch(/\.database-record-drawer\s*\{[^}]*w-\[min\(50vw,720px\)\]/)
    expect(css).toMatch(/\.database-mobile-list\s*\{[^}]*md:hidden/)
  })
})
