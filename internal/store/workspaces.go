package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/nabu-sh/nabu/internal/domain"
)

const workspaceColumns = `id, name, path, icon_path, default_branch, allowed, mission_started,
context_ready, context_prompted, orientation_queued, last_orientation_at, created_at`

// WorkspaceDeletion describes Nabu-owned filesystem records that became
// unreachable when a workspace was deleted. Callers may use these identifiers
// to remove bounded run directories and managed artifacts after the database
// transaction commits. The connected workspace path is deliberately excluded.
type WorkspaceDeletion struct {
	Workspace         domain.Workspace
	ActiveWorkspaceID string
	RunIDs            []string
	ScriptRunIDs      []string
	ArtifactPaths     []string
}

func (s *Store) CreateWorkspace(ctx context.Context, workspace domain.Workspace) (domain.Workspace, error) {
	if workspace.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Workspace{}, err
		}
		workspace.ID = id
	}
	workspace.CreatedAt = defaultTime(workspace.CreatedAt, s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("store: begin create workspace: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (`+workspaceColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		workspace.ID, workspace.Name, workspace.Path, workspace.IconPath, workspace.DefaultBranch, workspace.Allowed,
		workspace.MissionStarted, workspace.ContextReady, workspace.ContextPrompted, workspace.OrientationQueued, nullableTime(workspace.LastOrientationAt), formatTime(workspace.CreatedAt)); err != nil {
		return domain.Workspace{}, fmt.Errorf("store: create workspace: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO workspace_policies(workspace_id, read, work, publish, dangerous)
SELECT ?, read, work, publish, dangerous FROM policy WHERE id = 1`, workspace.ID); err != nil {
		return domain.Workspace{}, fmt.Errorf("store: create workspace policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE settings SET active_workspace_id = ?, mission_started = ?, orientation_queued = ?, last_orientation_at = ?
WHERE id = 1 AND active_workspace_id IS NULL`, workspace.ID, workspace.MissionStarted,
		workspace.OrientationQueued, nullableTime(workspace.LastOrientationAt)); err != nil {
		return domain.Workspace{}, fmt.Errorf("store: select first workspace: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Workspace{}, fmt.Errorf("store: commit workspace: %w", err)
	}
	return workspace, nil
}

func (s *Store) ActiveWorkspace(ctx context.Context) (domain.Workspace, error) {
	var id sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT active_workspace_id FROM settings WHERE id = 1").Scan(&id); err != nil {
		return domain.Workspace{}, fmt.Errorf("store: get active workspace: %w", err)
	}
	if !id.Valid || id.String == "" {
		return domain.Workspace{}, fmt.Errorf("active workspace: %w", ErrNotFound)
	}
	return s.GetWorkspace(ctx, id.String)
}

func (s *Store) SetActiveWorkspace(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE settings SET active_workspace_id = ?,
    mission_started = (SELECT mission_started FROM workspaces WHERE id = ?),
    orientation_queued = (SELECT orientation_queued FROM workspaces WHERE id = ?),
    last_orientation_at = (SELECT last_orientation_at FROM workspaces WHERE id = ?)
WHERE id = 1 AND EXISTS (SELECT 1 FROM workspaces WHERE id = ?)`, id, id, id, id, id)
	if err != nil {
		return fmt.Errorf("store: set active workspace: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: set active workspace result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("workspace %q: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Store) GetWorkspace(ctx context.Context, id string) (domain.Workspace, error) {
	return scanWorkspace(s.db.QueryRowContext(ctx, "SELECT "+workspaceColumns+" FROM workspaces WHERE id = ?", id))
}

func (s *Store) ListWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+workspaceColumns+" FROM workspaces ORDER BY name, id")
	if err != nil {
		return nil, fmt.Errorf("store: list workspaces: %w", err)
	}
	defer rows.Close()
	var workspaces []domain.Workspace
	for rows.Next() {
		workspace, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list workspaces: %w", err)
	}
	return workspaces, nil
}

func (s *Store) UpdateWorkspace(ctx context.Context, workspace domain.Workspace) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update workspace: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE workspaces SET name = ?, path = ?, icon_path = ?, default_branch = ?, allowed = ?, mission_started = ?,
    context_ready = ?, context_prompted = ?, orientation_queued = ?, last_orientation_at = ? WHERE id = ?`,
		workspace.Name, workspace.Path, workspace.IconPath, workspace.DefaultBranch, workspace.Allowed, workspace.MissionStarted,
		workspace.ContextReady, workspace.ContextPrompted, workspace.OrientationQueued, nullableTime(workspace.LastOrientationAt), workspace.ID)
	if err != nil {
		return fmt.Errorf("store: update workspace: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update workspace result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("workspace %q: %w", workspace.ID, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE settings SET mission_started = ?, orientation_queued = ?, last_orientation_at = ?
WHERE id = 1 AND active_workspace_id = ?`, workspace.MissionStarted, workspace.OrientationQueued,
		nullableTime(workspace.LastOrientationAt), workspace.ID); err != nil {
		return fmt.Errorf("store: sync workspace mission state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit workspace update: %w", err)
	}
	return nil
}

func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	_, err := s.DeleteWorkspaceData(ctx, id)
	return err
}

// DeleteWorkspaceData permanently removes all records owned by one workspace.
// Some phase-one tables predate workspace scoping and use ON DELETE SET NULL;
// those task/run/artifact records are removed explicitly before the workspace
// row so a deletion cannot leave hidden operational history behind.
func (s *Store) DeleteWorkspaceData(ctx context.Context, id string) (WorkspaceDeletion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: begin delete workspace: %w", err)
	}
	defer tx.Rollback()
	workspace, err := scanWorkspace(tx.QueryRowContext(ctx, "SELECT "+workspaceColumns+" FROM workspaces WHERE id = ?", id))
	if err != nil {
		return WorkspaceDeletion{}, err
	}
	open, err := workspaceHasOpenWork(ctx, tx, id)
	if err != nil {
		return WorkspaceDeletion{}, err
	}
	if open {
		return WorkspaceDeletion{}, fmt.Errorf("workspace %q has active work: %w", id, ErrInvalidTransition)
	}
	deletion := WorkspaceDeletion{Workspace: workspace}
	deletion.RunIDs, err = queryStrings(ctx, tx, `
SELECT id FROM runs
WHERE working_directory = ?
   OR task_id IN (SELECT id FROM tasks WHERE workspace_id = ?)
ORDER BY id`, workspace.Path, id)
	if err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: list workspace runs: %w", err)
	}
	deletion.ScriptRunIDs, err = queryStrings(ctx, tx, `
SELECT sr.id FROM script_runs sr
JOIN scripts s ON s.id = sr.script_id
WHERE s.workspace_id = ?
ORDER BY sr.id`, id)
	if err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: list workspace script runs: %w", err)
	}
	deletion.ArtifactPaths, err = queryStrings(ctx, tx, `
SELECT DISTINCT a.path FROM artifacts a
LEFT JOIN tasks direct_task ON direct_task.id = a.task_id
LEFT JOIN runs run ON run.id = a.run_id
LEFT JOIN tasks run_task ON run_task.id = run.task_id
LEFT JOIN script_runs script_run ON script_run.id = a.script_run_id
LEFT JOIN scripts script ON script.id = script_run.script_id
WHERE a.path <> '' AND (
    direct_task.workspace_id = ? OR run_task.workspace_id = ? OR
    run.working_directory = ? OR script.workspace_id = ?
)
ORDER BY a.path`, id, id, workspace.Path, id)
	if err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: list workspace artifacts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM artifacts
WHERE task_id IN (SELECT id FROM tasks WHERE workspace_id = ?)
   OR run_id IN (
       SELECT id FROM runs
       WHERE working_directory = ?
          OR task_id IN (SELECT id FROM tasks WHERE workspace_id = ?)
   )
   OR script_run_id IN (
       SELECT sr.id FROM script_runs sr
       JOIN scripts s ON s.id = sr.script_id
       WHERE s.workspace_id = ?
   )`, id, workspace.Path, id, id); err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: delete workspace artifacts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM runs
WHERE working_directory = ?
   OR task_id IN (SELECT id FROM tasks WHERE workspace_id = ?)`, workspace.Path, id); err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: delete workspace runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM tasks WHERE workspace_id = ?", id); err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: delete workspace tasks: %w", err)
	}
	var wasActive bool
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(active_workspace_id = ?, 0) FROM settings WHERE id = 1", id).Scan(&wasActive); err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: inspect active workspace: %w", err)
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM workspaces WHERE id = ?", id)
	if err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: delete workspace: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: delete workspace result: %w", err)
	}
	if count == 0 {
		return WorkspaceDeletion{}, fmt.Errorf("workspace %q: %w", id, ErrNotFound)
	}
	if wasActive {
		if _, err := tx.ExecContext(ctx, `
UPDATE settings SET
    active_workspace_id = (SELECT id FROM workspaces ORDER BY created_at, id LIMIT 1),
	setup_complete = CASE WHEN EXISTS (SELECT 1 FROM workspaces) THEN setup_complete ELSE 0 END,
    mission_started = COALESCE((SELECT mission_started FROM workspaces ORDER BY created_at, id LIMIT 1), 0),
    orientation_queued = COALESCE((SELECT orientation_queued FROM workspaces ORDER BY created_at, id LIMIT 1), 0),
    last_orientation_at = (SELECT last_orientation_at FROM workspaces ORDER BY created_at, id LIMIT 1)
WHERE id = 1`); err != nil {
			return WorkspaceDeletion{}, fmt.Errorf("store: select workspace after deletion: %w", err)
		}
	}
	var active sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT active_workspace_id FROM settings WHERE id = 1").Scan(&active); err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: read workspace after deletion: %w", err)
	}
	deletion.ActiveWorkspaceID = active.String
	if err := tx.Commit(); err != nil {
		return WorkspaceDeletion{}, fmt.Errorf("store: commit workspace deletion: %w", err)
	}
	return deletion, nil
}

// WorkspaceHasOpenWork reports execution that must finish or be cancelled
// before the workspace can be deleted. Ready/planned work is safe to purge.
func (s *Store) WorkspaceHasOpenWork(ctx context.Context, id string) (bool, error) {
	return workspaceHasOpenWork(ctx, s.db, id)
}

type workspaceQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func workspaceHasOpenWork(ctx context.Context, queryer workspaceQueryRower, id string) (bool, error) {
	var open bool
	err := queryer.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM tasks
    WHERE workspace_id = ? AND status = 'running'
    UNION ALL
    SELECT 1 FROM messages
    WHERE workspace_id = ? AND status IN ('queued', 'processing')
    UNION ALL
    SELECT 1 FROM script_runs sr
    JOIN scripts script ON script.id = sr.script_id
    WHERE script.workspace_id = ? AND sr.status IN ('pending', 'running')
)`, id, id, id).Scan(&open)
	if err != nil {
		return false, fmt.Errorf("store: inspect workspace activity: %w", err)
	}
	return open, nil
}

func queryStrings(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(values)
	return values, nil
}

func scanWorkspace(row rowScanner) (domain.Workspace, error) {
	var workspace domain.Workspace
	var created string
	var lastOrientation sql.NullString
	if err := row.Scan(&workspace.ID, &workspace.Name, &workspace.Path, &workspace.IconPath, &workspace.DefaultBranch,
		&workspace.Allowed, &workspace.MissionStarted, &workspace.ContextReady, &workspace.ContextPrompted, &workspace.OrientationQueued, &lastOrientation, &created); err != nil {
		return domain.Workspace{}, fmt.Errorf("store: get workspace: %w", notFound("workspace", err))
	}
	var err error
	workspace.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Workspace{}, err
	}
	workspace.LastOrientationAt, err = parseNullableTime(lastOrientation)
	if err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
}
