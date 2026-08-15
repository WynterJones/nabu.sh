import { ClientSideRowModelModule, ModuleRegistry, themeQuartz, type ColDef, type ICellRendererParams, type SortChangedEvent } from 'ag-grid-community'
import { AgGridReact } from 'ag-grid-react'
import { ArrowLeft, Braces, ChevronLeft, ChevronRight, Database as DatabaseIcon, Download, ExternalLink, FileJson2, Filter, LoaderCircle, Pencil, Plus, RefreshCw, Search, Trash2, Upload, X } from 'lucide-react'
import { type ChangeEvent, type FormEvent, type MouseEvent, useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { InlineError, PageLoading } from '../components/PageState'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Card, EmptyState } from '../components/ui/Card'
import { Dialog } from '../components/ui/Dialog'
import { Field, Input, Textarea } from '../components/ui/Field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/Select'
import { coerceDatabaseValue, databaseApi, parseDatabaseImport } from '../features/database/api'
import type { CreateDatabaseDataset, DatabaseDataset, DatabaseField, DatabaseFieldType, DatabaseRow } from '../features/database/types'
import { useResource } from '../hooks/useResource'
import { cn, truncate } from '../lib/utils'
import { useNabu } from '../state/NabuContext'

const fieldTypes: DatabaseFieldType[] = ['string', 'integer', 'number', 'boolean', 'datetime', 'json']
type GridSortState = Array<{ id: string; desc: boolean }>
type InspectedRow = { row: DatabaseRow; field: string } | null

ModuleRegistry.registerModules([ClientSideRowModelModule])

const databaseGridTheme = themeQuartz.withParams({
  accentColor: '#36b3a6',
  backgroundColor: '#101012',
  borderColor: '#303034',
  borderRadius: 0,
  chromeBackgroundColor: '#18181b',
  foregroundColor: '#f4f4f5',
  fontFamily: 'Inter, ui-sans-serif, system-ui, sans-serif',
  fontSize: 13,
  headerBackgroundColor: '#18181b',
  headerFontSize: 11,
  headerFontWeight: 650,
  headerTextColor: '#a6a6b0',
  rowHoverColor: 'rgba(54, 179, 166, 0.07)',
  spacing: 6,
  subtleTextColor: '#a6a6b0',
  textColor: '#f4f4f5',
})

export function DatabasePage() {
  const { activeScope } = useNabu()
  const { data, setData, loading, error, refresh } = useResource(databaseApi.listDatasets, activeScope?.id ?? '')
  const [creating, setCreating] = useState(false)
  const datasets = data ?? []

  if (loading && !data) return <PageLoading label="Loading datasets…" />
  return (
    <div className="page-stack max-w-6xl">
      <div className="page-heading"><div><h1 className="page-title">Database</h1><p className="page-description">Structured workspace data for research, market maps, lead lists, and durable operational records.</p></div><div className="flex gap-2"><Button variant="secondary" size="icon" aria-label="Refresh datasets" onClick={() => void refresh()} disabled={loading}><RefreshCw className={cn('size-4', loading && 'animate-spin motion-reduce:animate-none')} /></Button><Button variant="primary" onClick={() => setCreating(true)}><Plus className="size-4" />New dataset</Button></div></div>
      {error ? <InlineError message={error} /> : null}
      {!datasets.length ? <EmptyState icon={<DatabaseIcon className="size-5" />} title="Create your first dataset" description="Build a research set, competitor map, customer directory, or any structured collection." action={<Button variant="primary" onClick={() => setCreating(true)}><Plus className="size-4" />Create dataset</Button>} /> : <div className="database-dataset-list">{datasets.map((dataset) => <DatasetCard key={dataset.id} dataset={dataset} />)}</div>}
      <CreateDatasetDialog open={creating} onOpenChange={setCreating} onCreated={(dataset) => setData((current) => [...(current ?? []), dataset])} />
    </div>
  )
}

function DatasetCard({ dataset }: { dataset: DatabaseDataset }) {
  return <Link to={`/database/${encodeURIComponent(dataset.id)}`} className="database-dataset-card"><span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted"><DatabaseIcon className="size-5" /></span><span className="min-w-0 flex-1"><span className="flex flex-wrap items-center gap-2"><span className="text-sm font-semibold text-ink">{dataset.name}</span><Badge variant="outline">{dataset.rowCount.toLocaleString()} rows</Badge></span><span className="mt-1.5 line-clamp-2 text-xs leading-relaxed text-muted">{dataset.description ?? `${dataset.schema.length} structured fields`}</span><span className="mt-3 flex flex-wrap gap-1.5">{dataset.schema.slice(0, 5).map((field) => <Badge key={field.name}>{field.name}</Badge>)}{dataset.schema.length > 5 ? <Badge>+{dataset.schema.length - 5}</Badge> : null}</span></span><ChevronRight className="size-4 shrink-0 text-muted" /></Link>
}

type DraftField = DatabaseField & { unique: boolean; key: number }
const blankField = (key: number): DraftField => ({ key, name: '', type: 'string', unique: false })

function CreateDatasetDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (open: boolean) => void; onCreated: (dataset: DatabaseDataset) => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [fields, setFields] = useState<DraftField[]>([blankField(1)])
  const [nextKey, setNextKey] = useState(2)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const reset = () => { onOpenChange(false); setName(''); setDescription(''); setFields([blankField(1)]); setNextKey(2); setError(null) }
  const close = () => { if (!saving) reset() }
  const save = async (event: FormEvent) => {
    event.preventDefault()
    const validFields = fields.map((field) => ({ ...field, name: field.name.trim() })).filter((field) => field.name)
    const names = validFields.map((field) => field.name.toLowerCase())
    if (!name.trim() || !validFields.length) return
    if (new Set(names).size !== names.length) { setError('Field names must be unique.'); return }
    setSaving(true); setError(null)
    const input: CreateDatabaseDataset = { name, description, schema: validFields.map(({ name: fieldName, type }) => ({ name: fieldName, type })), uniqueKey: validFields.filter((field) => field.unique).map((field) => field.name) }
    try { const created = await databaseApi.createDataset(input); onCreated(created); reset() }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'The dataset could not be created.') }
    finally { setSaving(false) }
  }
  return <Dialog open={open} onOpenChange={(value) => { if (!value) close() }} title="Create dataset" description="Define a durable structure now. You can add and edit rows here or ask Nabu to work with them in Chat." className="max-w-2xl" footer={<><Button variant="ghost" onClick={close} disabled={saving}>Cancel</Button><Button variant="primary" type="submit" form="create-dataset" disabled={saving || !name.trim() || !fields.some((field) => field.name.trim())}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <DatabaseIcon className="size-4" />}{saving ? 'Creating…' : 'Create dataset'}</Button></>}>
    <form id="create-dataset" onSubmit={(event) => void save(event)} className="space-y-5"><Field label="Dataset name"><Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Market research" required autoFocus /></Field><Field label="Description" hint="Optional"><Textarea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="What this collection tracks and how Nabu should use it." className="min-h-20" /></Field><fieldset><legend className="field-label">Fields</legend><div className="mt-2 space-y-2">{fields.map((field, index) => <div key={field.key} className="database-schema-row"><Input aria-label={`Field ${index + 1} name`} value={field.name} onChange={(event) => setFields((current) => current.map((item) => item.key === field.key ? { ...item, name: event.target.value } : item))} placeholder="Field name" /><Select value={field.type} onValueChange={(value) => setFields((current) => current.map((item) => item.key === field.key ? { ...item, type: value as DatabaseFieldType } : item))}><SelectTrigger aria-label={`Field ${index + 1} type`}><SelectValue /></SelectTrigger><SelectContent>{fieldTypes.map((type) => <SelectItem key={type} value={type}>{type}</SelectItem>)}</SelectContent></Select><label className="flex min-h-10 items-center gap-2 whitespace-nowrap px-2 text-xs text-muted"><input type="checkbox" checked={field.unique} onChange={(event) => setFields((current) => current.map((item) => item.key === field.key ? { ...item, unique: event.target.checked } : item))} className="size-4 accent-[rgb(var(--accent))]" />Unique key</label><Button variant="ghost" size="icon" aria-label={`Remove field ${index + 1}`} onClick={() => setFields((current) => current.filter((item) => item.key !== field.key))} disabled={fields.length === 1}><X className="size-4" /></Button></div>)}</div><Button variant="ghost" size="sm" className="mt-2" onClick={() => { setFields((current) => [...current, blankField(nextKey)]); setNextKey((value) => value + 1) }}><Plus className="size-3.5" />Add field</Button></fieldset>{error ? <InlineError message={error} /> : null}</form>
  </Dialog>
}

export function DatabaseDatasetPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: dataset, loading: datasetLoading, error: datasetError, refresh: refreshDataset } = useResource(() => databaseApi.getDataset(id), id)
  const [sorting, setSorting] = useState<GridSortState>([])
  const [searchDraft, setSearchDraft] = useState('')
  const [search, setSearch] = useState('')
  const [filterField, setFilterField] = useState('')
  const [filterDraft, setFilterDraft] = useState('')
  const [filterValue, setFilterValue] = useState('')
  const [cursors, setCursors] = useState<string[]>([])
  const [editor, setEditor] = useState<DatabaseRow | 'new' | null>(null)
  const [deleteRow, setDeleteRow] = useState<DatabaseRow | null>(null)
  const [deleteDatasetOpen, setDeleteDatasetOpen] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const sort = sorting[0]
  const dependency = JSON.stringify([id, cursors.at(-1), sort?.id, sort?.desc, search, filterField, filterValue])
  const { data: rowsPage, loading: rowsLoading, error: rowsError, refresh: refreshRows } = useResource(() => databaseApi.listRows(id, { limit: 50, cursor: cursors.at(-1), sort: sort?.id, direction: sort?.desc ? 'desc' : 'asc', q: search, filter: filterField && filterValue ? { field: filterField, value: filterValue } : undefined }), dependency)
  const rows = rowsPage?.rows ?? []

  const updateSorting = useCallback((next: GridSortState) => {
    setSorting((current) => current[0]?.id === next[0]?.id && current[0]?.desc === next[0]?.desc ? current : next)
    setCursors([])
  }, [])
  const refreshAll = async () => { await Promise.all([refreshRows(), refreshDataset()]) }
  const confirmDeleteRow = async () => {
    if (!deleteRow || deleting) return
    setDeleting(true); setActionError(null)
    try { await databaseApi.deleteRow(id, deleteRow.id); setDeleteRow(null); await refreshAll() }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'The row could not be deleted.') }
    finally { setDeleting(false) }
  }
  const confirmDeleteDataset = async () => {
    if (!dataset || deleting) return
    setDeleting(true); setActionError(null)
    try { await databaseApi.deleteDataset(dataset.id); navigate('/database') }
    catch (caught) { setActionError(caught instanceof Error ? caught.message : 'The dataset could not be deleted.') }
    finally { setDeleting(false) }
  }

  if (datasetLoading && !dataset) return <PageLoading label="Loading dataset…" />
  if (!dataset) return <div className="page-stack max-w-6xl"><EmptyState icon={<DatabaseIcon className="size-5" />} title="Dataset not found" description={datasetError ?? 'This dataset may have been removed.'} action={<Button asChild><Link to="/database">Back to datasets</Link></Button>} /></div>
  return <div className="page-stack max-w-[1600px]">
    <div><Button asChild variant="secondary" size="sm"><Link to="/database"><ArrowLeft className="size-4" />All datasets</Link></Button></div>
    <div className="page-heading items-start"><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><h1 className="page-title">{dataset.name}</h1><Badge variant="outline">{dataset.rowCount.toLocaleString()} rows</Badge></div>{dataset.description ? <p className="page-description">{dataset.description}</p> : null}</div><div className="flex flex-wrap justify-end gap-2"><Button variant="secondary" size="sm" asChild><a href={databaseApi.exportUrl(dataset.id, 'csv')} download><Download className="size-4" />CSV</a></Button><Button variant="secondary" size="sm" onClick={() => setImportOpen(true)}><Upload className="size-4" />Import JSON</Button><Button variant="primary" size="sm" onClick={() => setEditor('new')}><Plus className="size-4" />Add row</Button><Button variant="ghost" size="icon" aria-label="Delete dataset" onClick={() => setDeleteDatasetOpen(true)}><Trash2 className="size-4" /></Button></div></div>
    {datasetError || rowsError || actionError ? <InlineError message={actionError ?? rowsError ?? datasetError ?? ''} /> : null}
    <Card className="overflow-hidden shadow-none">
      <form className="database-toolbar" onSubmit={(event) => { event.preventDefault(); setSearch(searchDraft); setFilterValue(filterDraft); setCursors([]) }}><div className="relative min-w-[180px] flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" /><Input value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} placeholder="Search all fields…" aria-label="Search dataset" className="pl-9" /></div><Select value={filterField || 'all'} onValueChange={(value) => { setFilterField(value === 'all' ? '' : value); if (value === 'all') setFilterDraft('') }}><SelectTrigger className="w-full sm:w-44" aria-label="Filter field"><Filter className="size-3.5 text-muted" /><SelectValue placeholder="Filter field" /></SelectTrigger><SelectContent><SelectItem value="all">All fields</SelectItem>{dataset.schema.map((field) => <SelectItem key={field.name} value={field.name}>{field.name}</SelectItem>)}</SelectContent></Select>{filterField ? <Input value={filterDraft} onChange={(event) => setFilterDraft(event.target.value)} placeholder="Exact value" aria-label={`Filter ${filterField}`} className="w-full sm:w-40" /> : null}<Button type="submit" variant="secondary" size="sm">Apply</Button>{search || filterValue ? <Button variant="ghost" size="sm" onClick={() => { setSearchDraft(''); setSearch(''); setFilterDraft(''); setFilterValue(''); setFilterField(''); setCursors([]) }}><X className="size-3.5" />Clear</Button> : null}<Button variant="ghost" size="icon" aria-label="Refresh rows" onClick={() => void refreshAll()} disabled={rowsLoading}><RefreshCw className={cn('size-4', rowsLoading && 'animate-spin motion-reduce:animate-none')} /></Button></form>
      {rowsLoading && !rowsPage ? <PageLoading label="Loading rows…" /> : !rows.length ? <div className="p-3 sm:p-4"><EmptyState compact icon={<Braces className="size-5" />} title={search || filterValue ? 'No matching rows' : 'This dataset has no rows'} description={search || filterValue ? 'Adjust the search or exact-match filter.' : 'Add a row here, import a JSON array, or ask Nabu in Chat to collect structured research.'} action={!search && !filterValue ? <Button variant="primary" onClick={() => setEditor('new')}><Plus className="size-4" />Add first row</Button> : undefined} /></div> : <DatabaseGrid dataset={dataset} rows={rows} sorting={sorting} onSortingChange={updateSorting} onEdit={setEditor} onDelete={setDeleteRow} />}
      {rows.length ? <div className="database-pagination"><p className="text-xs text-muted">{rowsPage?.total !== undefined ? `${rowsPage.total.toLocaleString()} total rows` : `${rows.length} rows on this page`}</p><div className="flex items-center gap-2"><Button variant="secondary" size="sm" disabled={!cursors.length || rowsLoading} onClick={() => setCursors((current) => current.slice(0, -1))}><ChevronLeft className="size-4" />Previous</Button><span className="min-w-14 text-center text-xs tabular-nums text-muted">Page {cursors.length + 1}</span><Button variant="secondary" size="sm" disabled={!rowsPage?.nextCursor || rowsLoading} onClick={() => rowsPage?.nextCursor && setCursors((current) => [...current, rowsPage.nextCursor!])}>Next<ChevronRight className="size-4" /></Button></div></div> : null}
    </Card>
    <RowEditorDialog dataset={dataset} row={editor} onOpenChange={(open) => { if (!open) setEditor(null) }} onSaved={() => void refreshAll()} />
    <ImportRowsDialog dataset={dataset} open={importOpen} onOpenChange={setImportOpen} onImported={() => void refreshAll()} />
    <ConfirmDialog open={deleteRow !== null} onOpenChange={(open) => { if (!open && !deleting) setDeleteRow(null) }} title="Delete row?" description="This row will be permanently removed from the dataset. This cannot be undone." details={deleteRow ? <code className="break-all text-xs">{deleteRow.id}</code> : undefined} confirmLabel="Delete row" destructive pending={deleting} onConfirm={() => void confirmDeleteRow()} />
    <ConfirmDialog open={deleteDatasetOpen} onOpenChange={(open) => { if (!deleting) setDeleteDatasetOpen(open) }} title="Move dataset to trash?" description="The dataset and its rows will disappear from normal workspace views, but remain recoverable." details={dataset.name} confirmLabel="Move to trash" destructive pending={deleting} onConfirm={() => void confirmDeleteDataset()} />
  </div>
}

export function databaseCellText(value: unknown): string {
  if (value == null) return ''
  if (typeof value === 'boolean') return value ? 'True' : 'False'
  if (typeof value === 'object') {
    try { return JSON.stringify(value) }
    catch { return String(value) }
  }
  return String(value)
}

export function databaseCellURL(value: unknown): string | null {
  if (typeof value !== 'string') return null
  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.toString() : null
  } catch {
    return null
  }
}

function DatabaseCellValue({ value, full = false }: { value: unknown; full?: boolean }) {
  if (value == null || value === '') return <span className="text-muted/60">—</span>
  const text = databaseCellText(value)
  const url = databaseCellURL(value)
  if (url) {
    return <a href={url} target="_blank" rel="noopener noreferrer" className={cn('database-cell-link', !full && 'database-cell-single-line')} title={text} onClick={(event) => event.stopPropagation()}>{full ? text : truncate(text, 120)}<ExternalLink className="size-3 shrink-0" /></a>
  }
  if (typeof value === 'object') return full ? <pre className="database-record-json">{JSON.stringify(value, null, 2)}</pre> : <code className="database-cell-single-line text-[11px]">{truncate(text, 120)}</code>
  return <span className={cn(!full && 'database-cell-single-line')} title={full ? undefined : text}>{full ? text : truncate(text, 140)}</span>
}

function DatabaseGrid({ dataset, rows, sorting, onSortingChange, onEdit, onDelete }: { dataset: DatabaseDataset; rows: DatabaseRow[]; sorting: GridSortState; onSortingChange: (sorting: GridSortState) => void; onEdit: (row: DatabaseRow) => void; onDelete: (row: DatabaseRow) => void }) {
  const [inspected, setInspected] = useState<InspectedRow>(null)
  const columns = useMemo<ColDef<DatabaseRow>[]>(() => [
    ...dataset.schema.map((field): ColDef<DatabaseRow> => ({
      colId: field.name,
      field: `values.${field.name}`,
      headerName: field.name,
      minWidth: 150,
      width: 190,
      maxWidth: 420,
      resizable: true,
      sortable: true,
      sort: sorting[0]?.id === field.name ? (sorting[0].desc ? 'desc' : 'asc') : null,
      valueGetter: ({ data }) => data?.values[field.name],
      cellRenderer: ({ value }: ICellRendererParams<DatabaseRow, unknown>) => <DatabaseCellValue value={value} />,
    })),
    {
      colId: 'actions',
      headerName: '',
      pinned: 'right',
      lockPosition: 'right',
      sortable: false,
      resizable: false,
      suppressMovable: true,
      width: 92,
      minWidth: 92,
      maxWidth: 92,
      cellRenderer: ({ data }: ICellRendererParams<DatabaseRow>) => data ? <div className="database-grid-actions"><Button variant="ghost" size="icon" aria-label={`Edit row ${data.id}`} onClick={(event) => { event.stopPropagation(); onEdit(data) }}><Pencil className="size-3.5" /></Button><Button variant="ghost" size="icon" className="text-danger" aria-label={`Delete row ${data.id}`} onClick={(event) => { event.stopPropagation(); onDelete(data) }}><Trash2 className="size-3.5" /></Button></div> : null,
    },
  ], [dataset.schema, onDelete, onEdit, sorting])

  const handleSort = useCallback((event: SortChangedEvent<DatabaseRow>) => {
    const selected = event.api.getColumnState().find((column) => column.sort === 'asc' || column.sort === 'desc')
    const next = selected ? [{ id: selected.colId, desc: selected.sort === 'desc' }] : []
    if (next[0]?.id !== sorting[0]?.id || next[0]?.desc !== sorting[0]?.desc) onSortingChange(next)
  }, [onSortingChange, sorting])
  const inspectFromCell = useCallback((event: { data?: DatabaseRow; colDef: ColDef<DatabaseRow>; event?: Event | null }) => {
    if (!event.data || event.colDef.colId === 'actions') return
    const target = event.event?.target
    if (target instanceof Element && target.closest('a,button')) return
    setInspected({ row: event.data, field: event.colDef.colId ?? '' })
  }, [])
  const preventRowAction = (event: MouseEvent) => event.stopPropagation()

  return <>
    <div className="database-ag-grid" aria-label={`${dataset.name} rows`}>
      <AgGridReact<DatabaseRow>
        theme={databaseGridTheme}
        rowData={rows}
        columnDefs={columns}
        defaultColDef={{ suppressHeaderMenuButton: true }}
        getRowId={({ data }) => data.id}
        rowHeight={48}
        headerHeight={42}
        animateRows={false}
        ensureDomOrder
        suppressCellFocus={false}
        suppressMultiSort
        onCellClicked={inspectFromCell}
        onSortChanged={handleSort}
      />
    </div>
    <div className="database-mobile-list">{rows.map((row) => <article key={row.id} role="button" tabIndex={0} className="database-mobile-row" onClick={() => setInspected({ row, field: dataset.schema[0]?.name ?? '' })} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setInspected({ row, field: dataset.schema[0]?.name ?? '' }) } }}><dl>{dataset.schema.slice(0, 5).map((field) => <div key={field.name}><dt>{field.name}</dt><dd><DatabaseCellValue value={row.values[field.name]} /></dd></div>)}</dl><div className="mt-3 flex justify-end gap-1" onClick={preventRowAction}><Button variant="ghost" size="sm" onClick={() => onEdit(row)}><Pencil className="size-3.5" />Edit</Button><Button variant="ghost" size="sm" className="text-danger" onClick={() => onDelete(row)}><Trash2 className="size-3.5" />Delete</Button></div></article>)}</div>
    <DatabaseRecordDrawer dataset={dataset} inspected={inspected} onClose={() => setInspected(null)} onEdit={(row) => { setInspected(null); onEdit(row) }} />
  </>
}

function DatabaseRecordDrawer({ dataset, inspected, onClose, onEdit }: { dataset: DatabaseDataset; inspected: InspectedRow; onClose: () => void; onEdit: (row: DatabaseRow) => void }) {
  const row = inspected?.row
  return <Dialog open={Boolean(row)} onOpenChange={(open) => { if (!open) onClose() }} title="Row details" description={row ? `Record ${row.id}` : undefined} className="database-record-drawer" bodyClassName="database-record-body" footer={row ? <Button variant="primary" onClick={() => onEdit(row)}><Pencil className="size-4" />Edit row</Button> : undefined}>
    {row ? <div className="database-record-fields">{dataset.schema.map((field) => <section key={field.name} className={cn('database-record-field', inspected?.field === field.name && 'database-record-field-active')}><h3>{field.name}</h3><div><DatabaseCellValue value={row.values[field.name]} full /></div></section>)}</div> : null}
  </Dialog>
}

function initialValues(dataset: DatabaseDataset, row: DatabaseRow | 'new'): Record<string, string> {
  return Object.fromEntries(dataset.schema.map((field) => { const value = row === 'new' ? '' : row.values[field.name]; return [field.name, value == null ? '' : typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value)] }))
}

function RowEditorDialog({ dataset, row, onOpenChange, onSaved }: { dataset: DatabaseDataset; row: DatabaseRow | 'new' | null; onOpenChange: (open: boolean) => void; onSaved: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({})
  const [loadedKey, setLoadedKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    const key = row === 'new' ? 'new' : row?.id ?? ''
    if (!row || key === loadedKey) return
    setLoadedKey(key)
    setValues(initialValues(dataset, row))
    setError(null)
  }, [dataset, loadedKey, row])
  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (!row || saving) return
    const next: Record<string, unknown> = {}
    try { for (const field of dataset.schema) next[field.name] = coerceDatabaseValue(values[field.name] ?? '', field.type) }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'One of the values is invalid.'); return }
    setSaving(true); setError(null)
    try { if (row === 'new') await databaseApi.createRows(dataset.id, [next]); else await databaseApi.updateRow(dataset.id, row.id, next); onOpenChange(false); onSaved() }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'The row could not be saved.') }
    finally { setSaving(false) }
  }
  return <Dialog open={row !== null} onOpenChange={(open) => { if (!open && !saving) onOpenChange(false) }} title={row === 'new' ? 'Add row' : 'Edit row'} description={`Values follow the ${dataset.name} schema.`} className="database-row-dialog" footer={<><Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button><Button variant="primary" type="submit" form="database-row-editor" disabled={saving}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : null}{saving ? 'Saving…' : 'Save row'}</Button></>}>
    {row ? <form id="database-row-editor" onSubmit={(event) => void save(event)} className="space-y-4">{dataset.schema.map((field) => <DatabaseValueField key={field.name} field={field} value={values[field.name] ?? ''} onChange={(value) => setValues((current) => ({ ...current, [field.name]: value }))} />)}{error ? <InlineError message={error} /> : null}</form> : null}
  </Dialog>
}

function DatabaseValueField({ field, value, onChange }: { field: DatabaseField; value: string; onChange: (value: string) => void }) {
  if (field.type === 'boolean') return <Field label={field.name} hint="boolean"><Select value={value || 'false'} onValueChange={onChange}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="true">True</SelectItem><SelectItem value="false">False</SelectItem></SelectContent></Select></Field>
  if (field.type === 'json') return <Field label={field.name} hint="JSON"><Textarea value={value} onChange={(event) => onChange(event.target.value)} placeholder="{}" className="font-mono text-xs" /></Field>
  return <Field label={field.name} hint={field.type}><Input type={field.type === 'number' || field.type === 'integer' ? 'number' : field.type === 'datetime' ? 'datetime-local' : 'text'} step={field.type === 'integer' ? '1' : field.type === 'number' ? 'any' : undefined} value={value} onChange={(event) => onChange(event.target.value)} /></Field>
}

function ImportRowsDialog({ dataset, open, onOpenChange, onImported }: { dataset: DatabaseDataset; open: boolean; onOpenChange: (open: boolean) => void; onImported: () => void }) {
  const [content, setContent] = useState('')
  const [mode, setMode] = useState<'insert' | 'upsert'>('insert')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const reset = () => { onOpenChange(false); setContent(''); setError(null) }
  const close = () => { if (!saving) reset() }
  const chooseFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return
    if (file.size > 5 * 1024 * 1024) { setError('Choose a JSON file smaller than 5 MB.'); return }
    setContent(await file.text()); setError(null); event.target.value = ''
  }
  const save = async (event: FormEvent) => {
    event.preventDefault(); setError(null)
    let rows: Array<Record<string, unknown>>
    try { rows = parseDatabaseImport(content) }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'The JSON could not be read.'); return }
    setSaving(true)
    try { await databaseApi.createRows(dataset.id, rows, mode); reset(); onImported() }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'The rows could not be imported.') }
    finally { setSaving(false) }
  }
  return <Dialog open={open} onOpenChange={(value) => { if (!value) close() }} title="Import JSON rows" description="Import a JSON array of row objects. CSV import is not enabled by the local service yet." className="max-w-2xl" footer={<><Button variant="ghost" onClick={close} disabled={saving}>Cancel</Button><Button variant="primary" type="submit" form="database-import" disabled={saving || !content.trim()}>{saving ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <Upload className="size-4" />}{saving ? 'Importing…' : 'Import rows'}</Button></>}>
    <form id="database-import" onSubmit={(event) => void save(event)} className="space-y-4"><label className="flex cursor-pointer items-center justify-center gap-2 rounded-lg border border-dashed border-line bg-canvas px-4 py-4 text-sm text-muted outline-none hover:border-[#48484f] hover:text-ink focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent"><FileJson2 className="size-4" />Choose JSON file<input type="file" accept="application/json,.json" onChange={(event) => void chooseFile(event)} className="sr-only" /></label><Field label="JSON rows"><Textarea value={content} onChange={(event) => setContent(event.target.value)} placeholder={'[\n  { "name": "Example" }\n]'} className="min-h-48 font-mono text-xs" /></Field><Field label="Write mode"><Select value={mode} onValueChange={(value) => setMode(value as 'insert' | 'upsert')}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="insert">Insert new rows</SelectItem><SelectItem value="upsert" disabled={!dataset.uniqueKey.length}>Upsert by unique key{dataset.uniqueKey.length ? '' : ' (no unique key)'}</SelectItem></SelectContent></Select></Field>{dataset.uniqueKey.length ? <p className="text-xs text-muted">Upsert key: {dataset.uniqueKey.join(', ')}</p> : null}{error ? <InlineError message={error} /> : null}</form>
  </Dialog>
}
