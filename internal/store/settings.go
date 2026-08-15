package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const settingsColumns = `display_name, setup_complete, paused, mission_started,
codex_path, git_path, server_address, orientation_queued, last_orientation_at, active_workspace_id, last_backup_at,
codex_model, codex_reasoning_effort, max_parallel_tasks`

const (
	DefaultMaxParallelTasks = 1
	MaximumParallelTasks    = 8
)

// GetSettings returns the singleton application settings row.
func (s *Store) GetSettings(ctx context.Context) (domain.Settings, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+settingsColumns+" FROM settings WHERE id = 1")
	return scanSettings(row)
}

// UpdateSettings replaces the singleton application settings row.
func (s *Store) UpdateSettings(ctx context.Context, settings domain.Settings) error {
	if settings.MaxParallelTasks == 0 {
		settings.MaxParallelTasks = DefaultMaxParallelTasks
	}
	if settings.MaxParallelTasks < DefaultMaxParallelTasks || settings.MaxParallelTasks > MaximumParallelTasks {
		return fmt.Errorf("store: max parallel tasks must be between %d and %d", DefaultMaxParallelTasks, MaximumParallelTasks)
	}
	var last any
	if settings.LastOrientationAt != nil {
		last = formatTime(*settings.LastOrientationAt)
	}
	var lastBackup any
	if settings.LastBackupAt != nil {
		lastBackup = formatTime(*settings.LastBackupAt)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update settings: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE settings SET display_name = ?, setup_complete = ?, paused = ?, mission_started = ?,
    codex_path = ?, git_path = ?, server_address = ?, orientation_queued = ?, last_orientation_at = ?, active_workspace_id = ?, last_backup_at = ?,
    codex_model = ?, codex_reasoning_effort = ?, max_parallel_tasks = ?
WHERE id = 1`,
		settings.DisplayName, settings.SetupComplete, settings.Paused, settings.MissionStarted,
		settings.CodexPath, settings.GitPath, settings.ServerAddress, settings.OrientationQueued, last,
		nullableText(settings.ActiveWorkspaceID), lastBackup, settings.CodexModel, settings.CodexReasoningEffort, settings.MaxParallelTasks,
	)
	if err != nil {
		return fmt.Errorf("store: update settings: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update settings result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("settings: %w", ErrNotFound)
	}
	if settings.ActiveWorkspaceID != "" {
		if _, err := tx.ExecContext(ctx, `
UPDATE workspaces SET mission_started = ?, orientation_queued = ?, last_orientation_at = ? WHERE id = ?`,
			settings.MissionStarted, settings.OrientationQueued, last, settings.ActiveWorkspaceID); err != nil {
			return fmt.Errorf("store: sync active workspace mission state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit settings: %w", err)
	}
	return nil
}

// RequestOrientation durably queues an orientation run. The returned value is
// true only when this call changed the state from not queued to queued.
func (s *Store) RequestOrientation(ctx context.Context) (bool, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return false, err
	}
	if workspaceID == "" {
		result, err := s.db.ExecContext(ctx, "UPDATE settings SET orientation_queued = 1 WHERE id = 1 AND orientation_queued = 0")
		if err != nil {
			return false, fmt.Errorf("store: request orientation: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("store: request orientation result: %w", err)
		}
		return count == 1, nil
	}
	return s.RequestOrientationForWorkspace(ctx, workspaceID)
}

func (s *Store) RequestOrientationForWorkspace(ctx context.Context, workspaceID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: begin request orientation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE workspaces SET orientation_queued = 1 WHERE id = ? AND orientation_queued = 0`, workspaceID)
	if err != nil {
		return false, fmt.Errorf("store: request workspace orientation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: request orientation result: %w", err)
	}
	if count == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = ?)", workspaceID).Scan(&exists); err != nil {
			return false, fmt.Errorf("store: inspect orientation workspace: %w", err)
		}
		if !exists {
			return false, fmt.Errorf("workspace %q: %w", workspaceID, ErrNotFound)
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE settings SET orientation_queued = 1 WHERE id = 1 AND active_workspace_id = ?`, workspaceID); err != nil {
		return false, fmt.Errorf("store: sync orientation request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: commit orientation request: %w", err)
	}
	return count == 1, nil
}

// QueueOrientation is an alias for RequestOrientation.
func (s *Store) QueueOrientation(ctx context.Context) (bool, error) {
	return s.RequestOrientation(ctx)
}

// ConsumeOrientationRequest atomically takes one queued orientation request.
// It prevents multiple worker loops from starting duplicate orientation runs.
func (s *Store) ConsumeOrientationRequest(ctx context.Context) (bool, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return false, err
	}
	if workspaceID == "" {
		result, err := s.db.ExecContext(ctx, "UPDATE settings SET orientation_queued = 0 WHERE id = 1 AND orientation_queued = 1")
		if err != nil {
			return false, fmt.Errorf("store: consume orientation request: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("store: consume orientation result: %w", err)
		}
		return count == 1, nil
	}
	return s.ConsumeOrientationRequestForWorkspace(ctx, workspaceID)
}

func (s *Store) ConsumeOrientationRequestForWorkspace(ctx context.Context, workspaceID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: begin consume orientation request: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE workspaces SET orientation_queued = 0 WHERE id = ? AND orientation_queued = 1`, workspaceID)
	if err != nil {
		return false, fmt.Errorf("store: consume workspace orientation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: consume orientation result: %w", err)
	}
	if count == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = ?)", workspaceID).Scan(&exists); err != nil {
			return false, fmt.Errorf("store: inspect orientation workspace: %w", err)
		}
		if !exists {
			return false, fmt.Errorf("workspace %q: %w", workspaceID, ErrNotFound)
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE settings SET orientation_queued = 0 WHERE id = 1 AND active_workspace_id = ?`, workspaceID); err != nil {
		return false, fmt.Errorf("store: sync consumed orientation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: commit consumed orientation: %w", err)
	}
	return count == 1, nil
}

// CompleteOrientation records a successful orientation and clears any queued
// request. A zero timestamp uses the store clock.
func (s *Store) CompleteOrientation(ctx context.Context, at time.Time) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	if workspaceID != "" {
		return s.CompleteOrientationForWorkspace(ctx, workspaceID, at)
	}
	at = defaultTime(at, s.now())
	if _, err := s.db.ExecContext(ctx, `
UPDATE settings SET orientation_queued = 0, last_orientation_at = ? WHERE id = 1`, formatTime(at)); err != nil {
		return fmt.Errorf("store: complete orientation: %w", err)
	}
	return nil
}

func (s *Store) CompleteOrientationForWorkspace(ctx context.Context, workspaceID string, at time.Time) error {
	at = defaultTime(at, s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin complete orientation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE workspaces SET orientation_queued = 0, last_orientation_at = ? WHERE id = ?`, formatTime(at), workspaceID)
	if err != nil {
		return fmt.Errorf("store: complete workspace orientation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: complete orientation result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("workspace %q: %w", workspaceID, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE settings SET orientation_queued = 0, last_orientation_at = ?
WHERE id = 1 AND active_workspace_id = ?`, formatTime(at), workspaceID); err != nil {
		return fmt.Errorf("store: sync completed orientation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit completed orientation: %w", err)
	}
	return nil
}

// OrientationState returns whether an orientation is queued and the last
// successful orientation time.
func (s *Store) OrientationState(ctx context.Context) (bool, *time.Time, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return false, nil, err
	}
	if workspaceID != "" {
		return s.OrientationStateForWorkspace(ctx, workspaceID)
	}
	var queued bool
	var last sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT orientation_queued, last_orientation_at FROM settings WHERE id = 1").Scan(&queued, &last); err != nil {
		return false, nil, fmt.Errorf("store: orientation state: %w", err)
	}
	if !last.Valid {
		return queued, nil, nil
	}
	parsed, err := parseTime(last.String)
	if err != nil {
		return false, nil, err
	}
	return queued, &parsed, nil
}

func (s *Store) OrientationStateForWorkspace(ctx context.Context, workspaceID string) (bool, *time.Time, error) {
	var queued bool
	var last sql.NullString
	if err := s.db.QueryRowContext(ctx, `
SELECT orientation_queued, last_orientation_at FROM workspaces WHERE id = ?`, workspaceID).Scan(&queued, &last); err != nil {
		return false, nil, fmt.Errorf("store: workspace orientation state: %w", notFound("workspace", err))
	}
	if !last.Valid {
		return queued, nil, nil
	}
	parsed, err := parseTime(last.String)
	if err != nil {
		return false, nil, err
	}
	return queued, &parsed, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSettings(row rowScanner) (domain.Settings, error) {
	var settings domain.Settings
	var last, activeWorkspaceID, lastBackup sql.NullString
	if err := row.Scan(
		&settings.DisplayName, &settings.SetupComplete, &settings.Paused, &settings.MissionStarted,
		&settings.CodexPath, &settings.GitPath, &settings.ServerAddress, &settings.OrientationQueued, &last, &activeWorkspaceID, &lastBackup,
		&settings.CodexModel, &settings.CodexReasoningEffort, &settings.MaxParallelTasks,
	); err != nil {
		return domain.Settings{}, fmt.Errorf("store: get settings: %w", notFound("settings", err))
	}
	if last.Valid {
		parsed, err := parseTime(last.String)
		if err != nil {
			return domain.Settings{}, err
		}
		settings.LastOrientationAt = &parsed
	}
	settings.ActiveWorkspaceID = activeWorkspaceID.String
	if lastBackup.Valid {
		parsed, err := parseTime(lastBackup.String)
		if err != nil {
			return domain.Settings{}, err
		}
		settings.LastBackupAt = &parsed
	}
	return settings, nil
}
