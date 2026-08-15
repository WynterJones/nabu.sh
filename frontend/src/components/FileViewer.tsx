import { ArrowUp, Check, ChevronRight, Download, File, FileQuestion, Folder, LoaderCircle, Save } from 'lucide-react'
import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useState, type PropsWithChildren } from 'react'
import { InlineError } from './PageState'
import { Button } from './ui/Button'
import { Dialog } from './ui/Dialog'
import { filesApi, type WorkspaceFile } from '../features/files/api'
import { cn } from '../lib/utils'

interface FileViewerState {
  openFile: (path: string) => void
}

const FileViewerContext = createContext<FileViewerState>({ openFile: () => undefined })
const FileCodeEditor = lazy(() => import('./FileCodeEditor'))

export function FileViewerProvider({ children }: PropsWithChildren) {
  const [requestedPath, setRequestedPath] = useState<string | null>(null)
  const [file, setFile] = useState<WorkspaceFile | null>(null)
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const openFile = useCallback((path: string) => {
    const value = path.trim()
    if (!value) return
    setRequestedPath(value)
    setFile(null)
    setContent('')
    setError(null)
    setSaved(false)
    setLoading(true)
    void filesApi.get(value).then((next) => {
      setFile(next)
      setContent(next.content)
    }).catch((caught: unknown) => {
      setError(caught instanceof Error ? caught.message : 'The file could not be opened.')
    }).finally(() => setLoading(false))
  }, [])

  const dirty = Boolean(file?.editable && content !== file.content)
  const close = () => {
    if (dirty && !window.confirm('Discard your unsaved file changes?')) return
    setRequestedPath(null)
    setFile(null)
    setError(null)
  }
  const save = useCallback(async () => {
    if (!file?.editable || saving || !dirty) return
    setSaving(true)
    setSaved(false)
    setError(null)
    try {
      const updated = await filesApi.save(file.path, content)
      setFile(updated)
      setContent(updated.content)
      setSaved(true)
      window.setTimeout(() => setSaved(false), 1800)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The file could not be saved.')
    } finally {
      setSaving(false)
    }
  }, [content, dirty, file, saving])

  useEffect(() => {
    if (!requestedPath) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
        event.preventDefault()
        void save()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [requestedPath, save])

  return (
    <FileViewerContext.Provider value={{ openFile }}>
      {children}
      <Dialog
        open={requestedPath !== null}
        onOpenChange={(open) => { if (!open) close() }}
        title={file?.name ?? 'Open file'}
        description={file?.path ?? requestedPath ?? undefined}
        className="file-viewer-dialog"
        headerClassName="file-viewer-header"
        bodyClassName="file-viewer-body"
        footer={file && file.kind !== 'directory' ? <>
          <div className="mr-auto text-xs text-muted" aria-live="polite">{saved ? <span className="inline-flex items-center gap-1.5 text-accent"><Check className="size-3.5" />Saved</span> : dirty ? 'Unsaved changes' : `${formatBytes(file.size)} · ${file.mimeType}`}</div>
          <Button asChild variant="secondary" size="sm"><a href={filesApi.contentUrl(file.path)} download={file.name}><Download className="size-4" />Download</a></Button>
          {file.editable ? <Button variant="primary" size="sm" onClick={() => void save()} disabled={!dirty || saving}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Save className="size-4" />}{saving ? 'Saving…' : 'Save'}</Button> : null}
        </> : undefined}
      >
        {loading ? <div className="file-viewer-state"><LoaderCircle className="size-5 animate-spin text-accent motion-reduce:animate-none" /><span>Opening…</span></div> : null}
        {error ? <div className="p-4"><InlineError message={error} /></div> : null}
        {!loading && file ? <FilePreview file={file} value={content} onChange={setContent} /> : null}
      </Dialog>
    </FileViewerContext.Provider>
  )
}

export function useFileViewer() {
  return useContext(FileViewerContext)
}

export function FileLink({ path, children, className }: PropsWithChildren<{ path: string; className?: string }>) {
  const { openFile } = useFileViewer()
  return <button type="button" className={cn('file-link', className)} onClick={() => openFile(path)}>{children ?? path}</button>
}

function FilePreview({ file, value, onChange }: { file: WorkspaceFile; value: string; onChange: (value: string) => void }) {
  const source = filesApi.contentUrl(file.path)
  if (file.kind === 'directory') return <DirectoryPreview file={file} />
  if (file.kind === 'text') return <Suspense fallback={<div className="file-viewer-state"><LoaderCircle className="size-5 animate-spin text-accent motion-reduce:animate-none" /><span>Loading editor…</span></div>}><FileCodeEditor name={file.name} value={value} onChange={onChange} /></Suspense>
  if (file.kind === 'image') return <figure className="file-media-frame"><img src={source} alt={file.name} /><figcaption>{file.name}</figcaption></figure>
  if (file.kind === 'video') return <div className="file-media-frame"><video src={source} controls preload="metadata" aria-label={file.name}>Your browser cannot preview this video.</video></div>
  if (file.kind === 'pdf') return <iframe className="file-pdf-frame" src={source} title={`PDF preview: ${file.name}`} />
  return <div className="file-viewer-state"><FileQuestion className="size-6" /><p>This file type cannot be previewed here.</p><Button asChild variant="secondary"><a href={source} download={file.name}><Download className="size-4" />Download file</a></Button></div>
}

function DirectoryPreview({ file }: { file: WorkspaceFile }) {
  const { openFile } = useFileViewer()
  const parentPath = file.path.includes('/') ? file.path.slice(0, file.path.lastIndexOf('/')) : null
  return (
    <div className="file-directory-browser">
      <div className="file-directory-toolbar">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium text-ink">{file.entries.length} {file.entries.length === 1 ? 'item' : 'items'}</p>
          <p className="truncate text-xs text-muted">Folders are listed first</p>
        </div>
        {parentPath ? <Button variant="ghost" size="sm" onClick={() => openFile(parentPath)}><ArrowUp className="size-4" />Up</Button> : null}
      </div>
      {file.entries.length ? (
        <ul className="file-directory-list" aria-label={`Contents of ${file.name}`}>
          {file.entries.map((entry) => (
            <li key={entry.path}>
              <button type="button" className="file-directory-row" onClick={() => openFile(entry.path)}>
                <span className={cn('file-directory-icon', entry.kind === 'directory' && 'file-directory-icon-folder')}>
                  {entry.kind === 'directory' ? <Folder className="size-4" /> : <File className="size-4" />}
                </span>
                <span className="min-w-0 flex-1 truncate text-left text-sm font-medium text-ink">{entry.name}</span>
                <span className="shrink-0 text-xs text-muted">{entry.kind === 'directory' ? 'Folder' : formatBytes(entry.size)}</span>
                <ChevronRight className="size-4 shrink-0 text-muted" />
              </button>
            </li>
          ))}
        </ul>
      ) : <div className="file-viewer-state"><Folder className="size-7" /><p>This folder is empty.</p></div>}
      {file.truncated ? <p className="file-directory-notice">Showing the first 500 items.</p> : null}
    </div>
  )
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}
