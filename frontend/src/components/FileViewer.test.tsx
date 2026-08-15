// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FileViewerProvider, useFileViewer } from './FileViewer'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function FolderLauncher() {
  const { openFile } = useFileViewer()
  return <button type="button" onClick={() => openFile('repos/webapp')}>Browse web app</button>
}

describe('FileViewer directory browser', () => {
  it('lists folders and files and navigates folders in the same drawer', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), 'http://nabu.local').searchParams.get('path')
      if (path === 'repos/webapp/src') {
        return new Response(JSON.stringify({ path, name: 'src', kind: 'directory', mime_type: 'application/x-directory', entries: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({
        path: 'repos/webapp', name: 'webapp', kind: 'directory', mime_type: 'application/x-directory',
        entries: [
          { path: 'repos/webapp/src', name: 'src', kind: 'directory', size: 0 },
          { path: 'repos/webapp/README.md', name: 'README.md', kind: 'file', size: 1024 },
        ],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<FileViewerProvider><FolderLauncher /></FileViewerProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'Browse web app' }))

    expect(await screen.findByRole('button', { name: /src Folder/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /README.md 1.0 KB/ })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /src Folder/ }))
    await waitFor(() => expect(screen.getByText('This folder is empty.')).toBeInTheDocument())
    expect(screen.getByRole('heading', { name: 'src' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/files?path=repos%2Fwebapp%2Fsrc', expect.anything())
  })
})
