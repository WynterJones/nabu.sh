import {
  Check,
  FolderGit2,
  FolderOpen,
  ImagePlus,
  LoaderCircle,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { InlineError } from "../../components/PageState";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card, EmptyState } from "../../components/ui/Card";
import { Dialog } from "../../components/ui/Dialog";
import { Field, Input } from "../../components/ui/Field";
import { scopesApi } from "../../features/scopes/api";
import type { Scope } from "../../features/scopes/types";
import { isAbsoluteWorkspacePath } from "../../lib/utils";
import { useNabu } from "../../state/NabuContext";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/Select";
import { WorkspaceAvatar } from "../../components/WorkspaceAvatar";

const workspaceIconTypes = new Set(["image/png", "image/jpeg", "image/webp"]);
const maximumWorkspaceIconBytes = 2 * 1024 * 1024;

export function WorkspacesSettingsPage() {
  const navigate = useNavigate();
  const { scopes, activeScope, refresh, switchScope } = useNabu();
  const [editing, setEditing] = useState<Scope | null | undefined>(undefined);
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [mission, setMission] = useState("");
  const [context, setContext] = useState("");
  const [mode, setMode] = useState<"create" | "connect">("create");
  const [saving, setSaving] = useState(false);
  const [browsing, setBrowsing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [iconFile, setIconFile] = useState<File | null>(null);
  const [iconPreview, setIconPreview] = useState<string | undefined>();
  const [deleting, setDeleting] = useState<Scope | null>(null);
  const [deleteConfirmation, setDeleteConfirmation] = useState("");
  const [deletePending, setDeletePending] = useState(false);
  const openForm = (scope: Scope | null) => {
    setEditing(scope);
    setName(scope?.name ?? "");
    setPath(scope?.path ?? "");
    setMission("");
    setContext("");
    setMode(scope ? "connect" : "create");
    setIconFile(null);
    setIconPreview(scope?.iconUrl);
    setError(null);
  };
  const chooseIcon = (file?: File) => {
    if (!file) return;
    if (
      !workspaceIconTypes.has(file.type) ||
      file.size > maximumWorkspaceIconBytes
    ) {
      setError("Use a PNG, JPEG, or WebP image no larger than 2 MB.");
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      setIconFile(file);
      setIconPreview(
        typeof reader.result === "string" ? reader.result : undefined,
      );
      setError(null);
    };
    reader.readAsDataURL(file);
  };
  const save = async () => {
    if (
      !name.trim() ||
      !isAbsoluteWorkspacePath(path) ||
      (!editing && !mission.trim()) ||
      saving
    )
      return;
    setSaving(true);
    setError(null);
    try {
      const saved = editing
        ? await scopesApi.update(editing.id, {
            name: name.trim(),
            path: path.trim(),
          })
        : await scopesApi.create({
            name: name.trim(),
            path: path.trim(),
            mode,
            mission: mission.trim(),
            context: context.trim(),
          });
      if (iconFile) await scopesApi.uploadIcon(saved.id, iconFile);
      if (!editing) {
        await switchScope(saved.id);
        navigate("/chat");
      } else await refresh();
      setEditing(undefined);
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : "Workspace could not be saved.",
      );
    } finally {
      setSaving(false);
    }
  };
  const activate = async (scope: Scope) => {
    setSaving(true);
    setError(null);
    try {
      await switchScope(scope.id);
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : "Workspace could not be activated.",
      );
    } finally {
      setSaving(false);
    }
  };
  const removeIcon = async () => {
    if (iconFile) {
      setIconFile(null);
      setIconPreview(editing?.iconUrl);
      return;
    }
    if (!editing?.iconUrl || saving) {
      setIconPreview(undefined);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await scopesApi.deleteIcon(editing.id);
      setIconFile(null);
      setIconPreview(undefined);
      setEditing({ ...editing, iconUrl: undefined });
      await refresh();
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : "Workspace image could not be removed.",
      );
    } finally {
      setSaving(false);
    }
  };
  const browse = async () => {
    if (browsing) return;
    setBrowsing(true);
    setError(null);
    try {
      const selected = await scopesApi.browse();
      if (selected) setPath(selected);
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : "The folder chooser could not be opened.",
      );
    } finally {
      setBrowsing(false);
    }
  };
  const openDelete = (scope: Scope) => {
    setDeleting(scope);
    setDeleteConfirmation("");
    setError(null);
  };
  const removeWorkspace = async () => {
    if (!deleting || deleteConfirmation !== deleting.name || deletePending) return;
    setDeletePending(true);
    setError(null);
    try {
      await scopesApi.delete(deleting.id, deleteConfirmation);
      setDeleting(null);
      setDeleteConfirmation("");
      await refresh();
      window.dispatchEvent(new CustomEvent("nabu:scope-changed"));
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : "Workspace could not be deleted.",
      );
    } finally {
      setDeletePending(false);
    }
  };
  return (
    <div className="settings-content-stack">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p className="eyebrow">Business scope</p>
          <h2 className="settings-title">Workspaces</h2>
          <p className="settings-description">
            Each workspace has its own mission, memory, queue, reports,
            approvals, and schedules.
          </p>
        </div>
        <Button variant="primary" onClick={() => openForm(null)}>
          <Plus className="size-4" />
          Add workspace
        </Button>
      </div>
      {error ? <InlineError message={error} /> : null}
      {scopes.length ? (
        <div className="space-y-2">
          {scopes.map((scope) => (
            <Card
              key={scope.id}
              className="workspace-card-row flex min-w-0 items-center gap-3 p-4 shadow-none"
            >
              <WorkspaceAvatar
                name={scope.name}
                iconUrl={scope.iconUrl}
                className="size-10"
              />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <h3 className="truncate text-sm font-semibold text-ink">
                    {scope.name}
                  </h3>
                  {activeScope?.id === scope.id ? (
                    <Badge variant="success">
                      <Check className="size-3" />
                      Active
                    </Badge>
                  ) : null}
                </div>
                <p className="mt-1 truncate font-mono text-xs text-muted">
                  {scope.path}
                </p>
              </div>
              <div className="workspace-card-actions flex shrink-0 items-center gap-1">
                {activeScope?.id !== scope.id ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => void activate(scope)}
                    disabled={saving}
                  >
                    Switch
                  </Button>
                ) : null}
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Edit ${scope.name}`}
                  onClick={() => openForm(scope)}
                >
                  <Pencil className="size-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className="text-danger"
                  aria-label={`Delete ${scope.name}`}
                  onClick={() => openDelete(scope)}
                  disabled={saving || deletePending}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
            </Card>
          ))}
        </div>
      ) : (
        <EmptyState
          compact
          icon={<FolderGit2 className="size-5" />}
          title="No workspaces configured"
          description="Add a local folder or repository to create a durable business scope."
          action={
            <Button variant="primary" onClick={() => openForm(null)}>
              <Plus className="size-4" />
              Add workspace
            </Button>
          }
        />
      )}
      <Dialog
        open={editing !== undefined}
        onOpenChange={(open) => {
          if (!open && !saving && !browsing) setEditing(undefined);
        }}
        title={editing ? "Edit workspace" : "Add workspace"}
        description="Each workspace is a separate business scope with isolated mission, memory, queue, and reports."
        footer={
          <>
            <Button
              variant="ghost"
              onClick={() => setEditing(undefined)}
              disabled={saving || browsing}
            >
              Cancel
            </Button>
            <Button
              variant="primary"
              onClick={() => void save()}
              disabled={
                !name.trim() ||
                !isAbsoluteWorkspacePath(path) ||
                (!editing && !mission.trim()) ||
                saving ||
                browsing
              }
            >
              {saving ? (
                <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <FolderGit2 className="size-4" />
              )}
              {saving
                ? "Saving…"
                : editing
                  ? "Save workspace"
                  : mode === "create"
                    ? "Create workspace"
                    : "Connect folder"}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div className="workspace-icon-editor">
            <WorkspaceAvatar
              name={name || "Workspace"}
              iconUrl={iconPreview}
              className="size-16"
            />
            <div className="min-w-0">
              <p className="text-sm font-medium text-ink">Workspace image</p>
              <p className="mt-1 text-xs text-muted">
                PNG, JPEG, or WebP · max 2 MB
              </p>
              <div className="mt-3 flex flex-wrap gap-2">
                <Button asChild variant="secondary" size="sm">
                  <label>
                    <ImagePlus className="size-4" />
                    {iconPreview ? "Change image" : "Upload image"}
                    <input
                      type="file"
                      accept="image/png,image/jpeg,image/webp"
                      className="sr-only"
                      onChange={(event) => {
                        chooseIcon(event.target.files?.[0]);
                        event.target.value = "";
                      }}
                    />
                  </label>
                </Button>
                {iconPreview ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-danger"
                    onClick={() => void removeIcon()}
                    disabled={saving}
                  >
                    <Trash2 className="size-4" />
                    {iconFile ? "Remove selection" : "Remove image"}
                  </Button>
                ) : null}
              </div>
            </div>
          </div>
          {!editing ? (
            <Field label="Workspace setup">
              <Select
                value={mode}
                onValueChange={(value: "create" | "connect") => setMode(value)}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="create">
                    Create organized workspace
                  </SelectItem>
                  <SelectItem value="connect">
                    Connect existing folder
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
          ) : null}
          <div className="rounded-lg border border-line bg-canvas p-3 text-xs leading-relaxed text-muted">
            {mode === "create" ? (
              <>
                Nabu creates an organized business workspace here with{" "}
                <span className="text-ink">
                  inbox, documents, media, research, data, repos, reports,
                  deliverables, and archive
                </span>
                .
              </>
            ) : (
              <>
                Nabu connects the established folder or repository and leaves
                its existing layout untouched.
              </>
            )}
          </div>
          <Field label="Workspace name" hint="Required">
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Example workspace"
              autoFocus
            />
          </Field>
          <Field
            label="Absolute path"
            hint="Required"
            error={
              path && !isAbsoluteWorkspacePath(path)
                ? "Use an absolute path beginning with /."
                : undefined
            }
          >
            <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center">
              <Input
                value={path}
                onChange={(event) => setPath(event.target.value)}
                placeholder={
                  mode === "create"
                    ? "/Users/you/Nabu/Wynter"
                    : "/Users/you/Code/project"
                }
                className="min-w-0 flex-1 font-mono text-xs"
                spellCheck={false}
              />
              <Button
                variant="secondary"
                className="h-10 w-full sm:w-auto"
                onClick={() => void browse()}
                disabled={browsing || saving}
              >
                {browsing ? (
                  <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" />
                ) : (
                  <FolderOpen className="size-4" />
                )}
                {browsing ? "Choosing…" : "Choose folder"}
              </Button>
            </div>
          </Field>
          {!editing ? (
            <>
              <Field label="Mission" hint="Required">
                <Input
                  value={mission}
                  onChange={(event) => setMission(event.target.value)}
                  placeholder="Grow qualified traffic and convert it into customers"
                />
              </Field>
              <Field
                label="What already exists"
                hint="Optional · Nabu will continue this in Chat"
              >
                <textarea
                  className="input min-h-24 resize-y py-3"
                  value={context}
                  onChange={(event) => setContext(event.target.value)}
                  placeholder="Product, website, repos, audience, analytics, accounts, constraints…"
                />
              </Field>
            </>
          ) : null}
          {error ? <InlineError message={error} /> : null}
        </div>
      </Dialog>
      <Dialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open && !deletePending) {
            setDeleting(null);
            setDeleteConfirmation("");
          }
        }}
        title="Delete workspace?"
        description="This permanently removes this workspace from Nabu. This action cannot be undone."
        className="max-w-lg"
        footer={
          <>
            <Button
              variant="ghost"
              onClick={() => {
                setDeleting(null);
                setDeleteConfirmation("");
              }}
              disabled={deletePending}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => void removeWorkspace()}
              disabled={
                !deleting ||
                deleteConfirmation !== deleting.name ||
                deletePending
              }
            >
              {deletePending ? (
                <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <Trash2 className="size-4" />
              )}
              {deletePending ? "Deleting…" : "Delete workspace"}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div className="rounded-xl border border-danger/35 bg-danger/5 p-4">
            <p className="text-sm font-semibold text-ink">
              Nabu data will be permanently deleted
            </p>
            <p className="mt-2 text-xs leading-relaxed text-muted">
              Chat history, tasks, runs, reports, datasets, plans, schedules,
              scripts, MCP registrations, memory, and saved credentials for this
              workspace will be removed.
            </p>
          </div>
          <div className="rounded-xl border border-line bg-canvas p-4">
            <div className="flex items-start gap-3">
              <FolderGit2 className="mt-0.5 size-4 shrink-0 text-accent" />
              <div className="min-w-0">
                <p className="text-sm font-medium text-ink">
                  Your folder stays on this computer
                </p>
                <p className="mt-1 break-all font-mono text-[11px] leading-relaxed text-muted">
                  {deleting?.path}
                </p>
              </div>
            </div>
          </div>
          <Field
            label={`Type ${deleting?.name ?? "the workspace name"} to confirm`}
            hint="Required"
          >
            <Input
              value={deleteConfirmation}
              onChange={(event) => setDeleteConfirmation(event.target.value)}
              placeholder={deleting?.name}
              autoComplete="off"
              spellCheck={false}
              autoFocus
            />
          </Field>
          {error ? <InlineError message={error} /> : null}
        </div>
      </Dialog>
    </div>
  );
}
