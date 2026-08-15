package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

const scriptColumns = `id, workspace_id, name, path, description, enabled, access, timeout_seconds, created_at, updated_at`
const scriptRunColumns = `id, script_id, schedule_id, status, pid, started_at, ended_at,
exit_code, stdout_path, stderr_path, result, error`

type ScriptFilter struct {
	WorkspaceID string
	Enabled     *bool
	Limit       int
}

type ScriptRunFilter struct {
	ScriptID   string
	ScheduleID string
	Statuses   []domain.ScriptRunStatus
	Limit      int
}

func (s *Store) CreateScript(ctx context.Context, script domain.Script) (domain.Script, error) {
	var err error
	script.WorkspaceID, err = s.defaultWorkspaceID(ctx, script.WorkspaceID)
	if err != nil {
		return domain.Script{}, err
	}
	if script.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Script{}, err
		}
		script.ID = id
	}
	if script.TimeoutSeconds < 0 {
		return domain.Script{}, fmt.Errorf("store: script timeout must not be negative")
	}
	if script.Access == "" {
		script.Access = domain.ScriptAccessRead
	}
	if !validScriptAccess(script.Access) {
		return domain.Script{}, fmt.Errorf("store: invalid script access %q", script.Access)
	}
	now := s.now()
	script.CreatedAt = defaultTime(script.CreatedAt, now)
	script.UpdatedAt = defaultTime(script.UpdatedAt, script.CreatedAt)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Script{}, fmt.Errorf("store: begin create script: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO scripts ("+scriptColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		script.ID, nullableText(script.WorkspaceID), script.Name, script.Path, script.Description, script.Enabled, script.Access,
		script.TimeoutSeconds, formatTime(script.CreatedAt), formatTime(script.UpdatedAt)); err != nil {
		return domain.Script{}, fmt.Errorf("store: create script: %w", err)
	}
	bindings := []domain.ScriptCredentialBinding{}
	if len(script.CredentialBindings) > 0 {
		bindings, err = s.replaceScriptCredentialBindingsTx(ctx, tx, script.WorkspaceID, script.ID, script.CredentialBindings)
		if err != nil {
			return domain.Script{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Script{}, fmt.Errorf("store: commit script: %w", err)
	}
	script.CredentialBindings = bindings
	return script, nil
}

func (s *Store) GetScript(ctx context.Context, id string) (domain.Script, error) {
	script, err := scanScript(s.db.QueryRowContext(ctx, "SELECT "+scriptColumns+" FROM scripts WHERE id = ?", id))
	if err != nil {
		return domain.Script{}, err
	}
	script.CredentialBindings, err = s.ListScriptCredentialBindingsForWorkspace(ctx, script.WorkspaceID, script.ID)
	if err != nil {
		return domain.Script{}, err
	}
	return script, nil
}

func (s *Store) ListScripts(ctx context.Context, filter ScriptFilter) ([]domain.Script, error) {
	query := "SELECT " + scriptColumns + " FROM scripts WHERE 1 = 1"
	var args []any
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query += " AND COALESCE(workspace_id, '') = ?"
	args = append(args, workspaceID)
	if filter.Enabled != nil {
		query += " AND enabled = ?"
		args = append(args, *filter.Enabled)
	}
	query += " ORDER BY name, id"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list scripts: %w", err)
	}
	var scripts []domain.Script
	for rows.Next() {
		script, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		scripts = append(scripts, script)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("store: list scripts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close scripts: %w", err)
	}
	for index := range scripts {
		scripts[index].CredentialBindings, err = s.ListScriptCredentialBindingsForWorkspace(ctx, scripts[index].WorkspaceID, scripts[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return scripts, nil
}

func (s *Store) UpdateScript(ctx context.Context, script domain.Script) error {
	if script.TimeoutSeconds < 0 {
		return fmt.Errorf("store: script timeout must not be negative")
	}
	if script.Access == "" {
		script.Access = domain.ScriptAccessRead
	}
	if !validScriptAccess(script.Access) {
		return fmt.Errorf("store: invalid script access %q", script.Access)
	}
	var err error
	script.WorkspaceID, err = s.defaultWorkspaceID(ctx, script.WorkspaceID)
	if err != nil {
		return err
	}
	script.UpdatedAt = defaultTime(script.UpdatedAt, s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update script: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE scripts SET workspace_id = ?, name = ?, path = ?, description = ?, enabled = ?, access = ?, timeout_seconds = ?, updated_at = ?
WHERE id = ?`, nullableText(script.WorkspaceID), script.Name, script.Path, script.Description, script.Enabled, script.Access,
		script.TimeoutSeconds, formatTime(script.UpdatedAt), script.ID)
	if err != nil {
		return fmt.Errorf("store: update script: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update script result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("script %q: %w", script.ID, ErrNotFound)
	}
	// A nil slice means the caller did not supply binding state. An explicit
	// empty slice clears bindings; a non-empty slice replaces them atomically.
	if script.CredentialBindings != nil {
		if _, err := s.replaceScriptCredentialBindingsTx(ctx, tx, script.WorkspaceID, script.ID, script.CredentialBindings); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit script update: %w", err)
	}
	return nil
}

func (s *Store) DeleteScript(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM scripts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete script: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete script result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("script %q: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Store) CreateScriptRun(ctx context.Context, run domain.ScriptRun) (domain.ScriptRun, error) {
	if run.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.ScriptRun{}, err
		}
		run.ID = id
	}
	if run.Status == "" {
		run.Status = domain.ScriptRunPending
	}
	run.StartedAt = defaultTime(run.StartedAt, s.now())
	resultJSON, err := encodeScriptResult(run.Result)
	if err != nil {
		return domain.ScriptRun{}, err
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO script_runs ("+scriptRunColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		run.ID, run.ScriptID, nullableText(run.ScheduleID), run.Status, run.PID, formatTime(run.StartedAt),
		nullableTime(run.EndedAt), nullableInt(run.ExitCode), run.StdoutPath, run.StderrPath, resultJSON, run.Error); err != nil {
		return domain.ScriptRun{}, fmt.Errorf("store: create script run: %w", err)
	}
	return run, nil
}

func (s *Store) GetScriptRun(ctx context.Context, id string) (domain.ScriptRun, error) {
	return scanScriptRun(s.db.QueryRowContext(ctx, "SELECT "+scriptRunColumns+" FROM script_runs WHERE id = ?", id))
}

func (s *Store) ListScriptRuns(ctx context.Context, filter ScriptRunFilter) ([]domain.ScriptRun, error) {
	query := "SELECT " + scriptRunColumns + " FROM script_runs WHERE 1 = 1"
	var args []any
	if filter.ScriptID != "" {
		query += " AND script_id = ?"
		args = append(args, filter.ScriptID)
	}
	if filter.ScheduleID != "" {
		query += " AND schedule_id = ?"
		args = append(args, filter.ScheduleID)
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, status)
		}
		query += " AND status IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY started_at DESC, id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list script runs: %w", err)
	}
	defer rows.Close()
	var runs []domain.ScriptRun
	for rows.Next() {
		run, err := scanScriptRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list script runs: %w", err)
	}
	return runs, nil
}

func (s *Store) UpdateScriptRun(ctx context.Context, run domain.ScriptRun) error {
	resultJSON, err := encodeScriptResult(run.Result)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE script_runs SET script_id = ?, schedule_id = ?, status = ?, pid = ?, started_at = ?,
    ended_at = ?, exit_code = ?, stdout_path = ?, stderr_path = ?, result = ?, error = ?
WHERE id = ?`, run.ScriptID, nullableText(run.ScheduleID), run.Status, run.PID, formatTime(run.StartedAt),
		nullableTime(run.EndedAt), nullableInt(run.ExitCode), run.StdoutPath, run.StderrPath, resultJSON, run.Error, run.ID)
	if err != nil {
		return fmt.Errorf("store: update script run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update script run result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("script run %q: %w", run.ID, ErrNotFound)
	}
	return nil
}

func (s *Store) DeleteScriptRun(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM script_runs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete script run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete script run result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("script run %q: %w", id, ErrNotFound)
	}
	return nil
}

func scanScript(row rowScanner) (domain.Script, error) {
	var script domain.Script
	var workspaceID sql.NullString
	var created, updated string
	if err := row.Scan(&script.ID, &workspaceID, &script.Name, &script.Path, &script.Description,
		&script.Enabled, &script.Access, &script.TimeoutSeconds, &created, &updated); err != nil {
		return domain.Script{}, fmt.Errorf("store: get script: %w", notFound("script", err))
	}
	script.WorkspaceID = workspaceID.String
	var err error
	script.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Script{}, err
	}
	script.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Script{}, err
	}
	return script, nil
}

func validScriptAccess(access domain.ScriptAccess) bool {
	return access == domain.ScriptAccessRead || access == domain.ScriptAccessWrite
}

func scanScriptRun(row rowScanner) (domain.ScriptRun, error) {
	var run domain.ScriptRun
	var scheduleID sql.NullString
	var started string
	var ended sql.NullString
	var exitCode sql.NullInt64
	var resultJSON []byte
	if err := row.Scan(&run.ID, &run.ScriptID, &scheduleID, &run.Status, &run.PID,
		&started, &ended, &exitCode, &run.StdoutPath, &run.StderrPath, &resultJSON, &run.Error); err != nil {
		return domain.ScriptRun{}, fmt.Errorf("store: get script run: %w", notFound("script run", err))
	}
	run.ScheduleID = scheduleID.String
	var err error
	run.StartedAt, err = parseTime(started)
	if err != nil {
		return domain.ScriptRun{}, err
	}
	run.EndedAt, err = parseNullableTime(ended)
	if err != nil {
		return domain.ScriptRun{}, err
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		run.ExitCode = &code
	}
	if len(resultJSON) > 0 {
		var result domain.ScriptResult
		if err := json.Unmarshal(resultJSON, &result); err != nil {
			return domain.ScriptRun{}, fmt.Errorf("store: decode script result: %w", err)
		}
		run.Result = &result
	}
	return run, nil
}

func encodeScriptResult(result *domain.ScriptResult) (any, error) {
	if result == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("store: encode script result: %w", err)
	}
	return encoded, nil
}
