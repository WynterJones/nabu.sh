import {
  Archive,
  ArchiveRestore,
  ArrowLeft,
  ArrowUpRight,
  CalendarDays,
  Eye,
  FileArchive,
  FileText,
  Inbox,
  Link2,
  LoaderCircle,
  Trash2,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EntityReferenceCard } from "../components/EntityReferenceCard";
import { FileLink } from "../components/FileViewer";
import { Markdown } from "../components/Markdown";
import { InlineError, PageLoading } from "../components/PageState";
import { Button } from "../components/ui/Button";
import { Card, EmptyState, SectionHeader } from "../components/ui/Card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/Select";
import { reportsApi } from "../features/reports/api";
import type { Report, ReportStatus } from "../features/reports/types";
import { useResource } from "../hooks/useResource";
import { cn, formatRelativeTime, truncate } from "../lib/utils";
import { useNabu } from "../state/NabuContext";

const reportSections: Array<{
  id: ReportStatus;
  title: string;
  icon: typeof Inbox;
}> = [
  { id: "unread", title: "Unread", icon: Inbox },
  { id: "read", title: "Read", icon: Eye },
  { id: "archived", title: "Archived", icon: Archive },
];

export function ReportsPage() {
  const { data, loading, error, refresh } = useResource(reportsApi.list);
  const { activeScope } = useNabu();
  const [selected, setSelected] = useState<ReportStatus>("unread");
  const reports = useMemo(() => data ?? [], [data]);
  const grouped = useMemo(
    () =>
      Object.fromEntries(
        reportSections.map((section) => [
          section.id,
          reports.filter((report) => report.status === section.id),
        ]),
      ) as Record<ReportStatus, Report[]>,
    [reports],
  );
  const visible = grouped[selected];
  if (loading) return <PageLoading label="Loading reports…" />;
  return (
    <div className="page-stack max-w-6xl">
      <div className="page-heading">
        <div>
          <h1 className="page-title">Reports</h1>
          <p className="page-description">
            Meaningful research, investigations, and mission updates that
            persist beyond a run or conversation.
          </p>
        </div>
      </div>
      {error ? <InlineError message={error} /> : null}
      <div className="tasks-mobile-section-select">
        <label className="field">
          <span className="field-label">Report section</span>
          <Select
            value={selected}
            onValueChange={(value) => setSelected(value as ReportStatus)}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {reportSections.map((section) => (
                <SelectItem key={section.id} value={section.id}>
                  {section.title} · {grouped[section.id].length}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </label>
      </div>
      <div className="tasks-section-layout">
        <aside className="tasks-section-sidebar">
          <nav aria-label="Report sections" className="space-y-1">
            {reportSections.map(({ id, title, icon: Icon }) => (
              <button
                key={id}
                type="button"
                className={cn(
                  "tasks-section-nav-item",
                  selected === id && "tasks-section-nav-item-active",
                )}
                aria-current={selected === id ? "page" : undefined}
                onClick={() => setSelected(id)}
              >
                <Icon className="size-4" />
                <span className="min-w-0 flex-1 text-left">{title}</span>
                <span className="count-pill">{grouped[id].length}</span>
              </button>
            ))}
          </nav>
        </aside>
        <section className="min-w-0" aria-labelledby={`reports-${selected}`}>
          <div className="mb-3 flex items-center gap-2.5 px-1">
            <h2
              id={`reports-${selected}`}
              className="text-base font-semibold text-ink"
            >
              {reportSections.find((section) => section.id === selected)?.title}
            </h2>
            <span className="count-pill">{visible.length}</span>
          </div>
          {!visible.length ? (
            <div className="tasks-empty-section">
              <FileText className="size-4 shrink-0 text-muted" />
              <div>
                <h3 className="text-sm font-medium text-ink">
                  No {selected} reports
                </h3>
                <p className="mt-1 text-xs leading-relaxed text-muted">
                  {selected === "unread"
                    ? `New reports for ${activeScope?.name ?? "this workspace"} will appear here.`
                    : selected === "read"
                      ? "Reports you have reviewed will appear here."
                      : "Archived reports remain available until permanently deleted."}
                </p>
              </div>
              {error ? (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => void refresh()}
                >
                  Try again
                </Button>
              ) : null}
            </div>
          ) : (
            <div className="report-list">
              {visible.map((report) => (
                <ReportRow key={report.id} report={report} />
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function ReportRow({ report }: { report: Report }) {
  return (
    <Link
      to={`/reports/${encodeURIComponent(report.id)}`}
      className="report-row"
    >
      <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-canvas text-muted">
        <FileText className="size-5" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="text-sm font-semibold text-ink">{report.title}</span>
        <span className="mt-1.5 line-clamp-2 text-pretty text-xs leading-relaxed text-muted">
          {truncate(report.summary || report.body, 220)}
        </span>
        <span className="mt-2 flex flex-wrap items-center gap-3 text-[11px] text-muted">
          {report.createdAt ? (
            <span className="flex items-center gap-1">
              <CalendarDays className="size-3" />
              {formatRelativeTime(report.createdAt)}
            </span>
          ) : null}
          {report.relatedTasks.length ? (
            <span>
              {report.relatedTasks.length} related task
              {report.relatedTasks.length === 1 ? "" : "s"}
            </span>
          ) : null}
          {report.artifacts.length ? (
            <span>
              {report.artifacts.length} artifact
              {report.artifacts.length === 1 ? "" : "s"}
            </span>
          ) : null}
        </span>
      </span>
      <ArrowUpRight className="size-4 shrink-0 text-muted" />
    </Link>
  );
}

export function ReportDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const {
    data: report,
    setData,
    loading,
    error,
  } = useResource(() => reportsApi.get(id), id);
  const [action, setAction] = useState<ReportStatus | "delete" | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const updateStatus = async (status: ReportStatus) => {
    if (!report || action) return;
    setAction(status);
    setActionError(null);
    try {
      setData(await reportsApi.update(report.id, status));
    } catch (caught) {
      setActionError(
        caught instanceof Error
          ? caught.message
          : "The report could not be updated.",
      );
    } finally {
      setAction(null);
    }
  };
  const remove = async () => {
    if (!report || action) return;
    setAction("delete");
    setActionError(null);
    try {
      await reportsApi.delete(report.id);
      navigate("/reports", { replace: true });
    } catch (caught) {
      setActionError(
        caught instanceof Error
          ? caught.message
          : "The report could not be deleted.",
      );
      setAction(null);
    }
  };
  if (loading) return <PageLoading label="Loading report…" />;
  if (!report)
    return (
      <EmptyState
        title="Report not found"
        description={error ?? "This report may no longer be available."}
        action={
          <Button asChild>
            <Link to="/reports">Back to reports</Link>
          </Button>
        }
      />
    );
  return (
    <div className="page-stack max-w-6xl">
      <div>
        <Button asChild variant="secondary" size="sm">
          <Link to="/reports">
            <ArrowLeft className="size-4" />
            All reports
          </Link>
        </Button>
      </div>
      <div className="page-heading items-start">
        <div className="min-w-0">
          <h1 className="task-detail-title">{report.title}</h1>
          <p className="task-detail-description max-w-3xl">{report.summary}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          {report.status === "unread" ? (
            <Button
              variant="primary"
              onClick={() => void updateStatus("read")}
              disabled={Boolean(action)}
            >
              {action === "read" ? (
                <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <Eye className="size-4" />
              )}
              Mark as read
            </Button>
          ) : null}
          {report.status === "archived" ? (
            <Button
              variant="secondary"
              onClick={() => void updateStatus("read")}
              disabled={Boolean(action)}
            >
              <ArchiveRestore className="size-4" />
              Restore
            </Button>
          ) : (
            <Button
              variant="secondary"
              onClick={() => void updateStatus("archived")}
              disabled={Boolean(action)}
            >
              <Archive className="size-4" />
              Archive
            </Button>
          )}
          <Button
            variant="danger"
            size="icon"
            aria-label="Delete report"
            onClick={() => setDeleteOpen(true)}
            disabled={Boolean(action)}
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      </div>
      {error || actionError ? (
        <InlineError message={actionError ?? error ?? ""} />
      ) : null}
      <div className="report-detail-grid">
        <article className="min-w-0 rounded-xl border border-line bg-panel p-5 shadow-panel sm:p-8">
          <Markdown>{report.body || report.summary}</Markdown>
        </article>
        <aside className="min-w-0 space-y-4">
          {report.createdAt ? (
            <Card className="p-4 shadow-none">
              <SectionHeader title="Details" />
              <dl className="task-detail-meta mt-3">
                <div className="task-detail-meta-row">
                  <dt>Created</dt>
                  <dd>{formatRelativeTime(report.createdAt)}</dd>
                </div>
              </dl>
            </Card>
          ) : null}
          {report.relatedTasks.length ? (
            <Card className="p-4 shadow-none">
              <SectionHeader eyebrow="Related work" title="Tasks" />
              <div className="mt-3 space-y-2">
                {report.relatedTasks.map((reference) => (
                  <EntityReferenceCard
                    key={reference.id}
                    reference={{ ...reference, type: "task" }}
                    compact
                  />
                ))}
              </div>
            </Card>
          ) : null}
          {report.artifacts.length ? (
            <Card className="p-4 shadow-none">
              <SectionHeader eyebrow="Output" title="Artifacts" />
              <div className="mt-3 space-y-2">
                {report.artifacts.map((artifact, index) =>
                  artifact.url ? (
                    <a
                      key={artifact.id ?? `${artifact.name}-${index}`}
                      className="artifact-row"
                      href={artifact.url}
                      target="_blank"
                      rel="noreferrer"
                    >
                      <FileArchive className="size-4 shrink-0 text-muted" />
                      <span className="min-w-0 flex-1 truncate text-xs text-ink">
                        {artifact.name}
                      </span>
                      <Link2 className="size-3.5 text-muted" />
                    </a>
                  ) : (
                    <div
                      key={artifact.id ?? `${artifact.name}-${index}`}
                      className="artifact-row"
                    >
                      <FileArchive className="size-4 shrink-0 text-muted" />
                      <span className="min-w-0 flex-1 truncate text-xs text-ink">
                        {artifact.path ? (
                          <FileLink path={artifact.path}>
                            {artifact.name}
                          </FileLink>
                        ) : (
                          artifact.name
                        )}
                      </span>
                      {artifact.path ? (
                        <FileLink
                          path={artifact.path}
                          className="truncate text-[10px] text-muted"
                        >
                          {artifact.path}
                        </FileLink>
                      ) : null}
                    </div>
                  ),
                )}
              </div>
            </Card>
          ) : null}
        </aside>
      </div>
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={(open) => {
          if (!action) setDeleteOpen(open);
        }}
        title="Delete this report?"
        description="This permanently removes the report from the active workspace. This action cannot be undone."
        details={report.title}
        confirmLabel="Delete report"
        destructive
        pending={action === "delete"}
        onConfirm={() => void remove()}
      />
    </div>
  );
}
