package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// IdleStewardState is the durable, per-workspace throttle for proactive idle
// reviews. Keeping this outside settings avoids coupling independent business
// workspaces and prevents daemon restarts from causing duplicate Codex calls.
type IdleStewardState struct {
	WorkspaceID string
	EmptyChecks int
	LastRunAt   *time.Time
	NextRunAt   *time.Time
}

// RecordIdleCheck records a genuinely idle operator cycle. The first idle
// observation starts a durable minimum-idle window. It returns true only once
// that real-time window has elapsed; additional wake signals cannot accelerate
// it. A provisional lease prevents another process/restart from claiming the
// same review while it is running.
func (s *Store) RecordIdleCheck(ctx context.Context, workspaceID string, now time.Time, minimumIdle, lease time.Duration) (bool, error) {
	if workspaceID == "" {
		return false, fmt.Errorf("store: idle steward requires a workspace")
	}
	if minimumIdle <= 0 {
		return false, fmt.Errorf("store: idle steward minimum idle duration must be positive")
	}
	if now.IsZero() {
		now = s.now()
	}
	now = now.UTC()
	if lease <= 0 {
		lease = 30 * time.Minute
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: begin idle check: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO idle_steward_state(workspace_id, empty_checks)
VALUES (?, 0) ON CONFLICT(workspace_id) DO NOTHING`, workspaceID); err != nil {
		return false, fmt.Errorf("store: initialize idle steward: %w", err)
	}

	var checks int
	var next sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT empty_checks, next_run_at FROM idle_steward_state WHERE workspace_id = ?`, workspaceID).Scan(&checks, &next); err != nil {
		return false, fmt.Errorf("store: read idle steward: %w", err)
	}
	var nextAt time.Time
	if next.Valid {
		nextAt, err = parseTime(next.String)
		if err != nil {
			return false, err
		}
		if now.Before(nextAt) {
			if err := tx.Commit(); err != nil {
				return false, fmt.Errorf("store: commit idle cooldown check: %w", err)
			}
			return false, nil
		}
	}

	// A positive marker means this row represents an accumulated idle window,
	// while zero with next_run_at represents a post-review cooldown. When a
	// cooldown expires, begin a fresh full window rather than reviewing at once.
	due := checks > 0 && next.Valid && !now.Before(nextAt)
	if due {
		checks = 0
		nextAt = now.Add(lease)
	} else {
		checks = 1
		nextAt = now.Add(minimumIdle)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE idle_steward_state SET empty_checks = ?, next_run_at = ? WHERE workspace_id = ?`, checks, formatTime(nextAt), workspaceID); err != nil {
		return false, fmt.Errorf("store: record idle check: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: commit idle check: %w", err)
	}
	return due, nil
}

// ResetIdleChecks clears an in-progress idle window when real work appears. A
// post-review cooldown uses a zero marker and is intentionally preserved.
func (s *Store) ResetIdleChecks(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO idle_steward_state(workspace_id, empty_checks)
VALUES (?, 0)
ON CONFLICT(workspace_id) DO UPDATE SET empty_checks = 0, next_run_at = NULL
WHERE idle_steward_state.empty_checks <> 0`, workspaceID)
	if err != nil {
		return fmt.Errorf("store: reset idle checks: %w", err)
	}
	return nil
}

// CompleteIdleSteward records the outcome boundary and next eligible review.
func (s *Store) CompleteIdleSteward(ctx context.Context, workspaceID string, ranAt, nextRunAt time.Time) error {
	if workspaceID == "" {
		return fmt.Errorf("store: idle steward requires a workspace")
	}
	if ranAt.IsZero() {
		ranAt = s.now()
	}
	if nextRunAt.IsZero() || nextRunAt.Before(ranAt) {
		return fmt.Errorf("store: idle steward next run must follow the completed run")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO idle_steward_state(workspace_id, empty_checks, last_run_at, next_run_at)
VALUES (?, 0, ?, ?)
ON CONFLICT(workspace_id) DO UPDATE SET
    empty_checks = 0,
    last_run_at = excluded.last_run_at,
    next_run_at = excluded.next_run_at`, workspaceID, formatTime(ranAt), formatTime(nextRunAt))
	if err != nil {
		return fmt.Errorf("store: complete idle steward: %w", err)
	}
	return nil
}

func (s *Store) GetIdleStewardState(ctx context.Context, workspaceID string) (IdleStewardState, error) {
	var state IdleStewardState
	var last, next sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT workspace_id, empty_checks, last_run_at, next_run_at
FROM idle_steward_state WHERE workspace_id = ?`, workspaceID).Scan(&state.WorkspaceID, &state.EmptyChecks, &last, &next)
	if err != nil {
		return IdleStewardState{}, notFound("idle steward state", err)
	}
	if state.LastRunAt, err = parseNullableTime(last); err != nil {
		return IdleStewardState{}, err
	}
	if state.NextRunAt, err = parseNullableTime(next); err != nil {
		return IdleStewardState{}, err
	}
	return state, nil
}
