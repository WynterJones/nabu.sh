package store

import (
	"context"
	"fmt"
	"time"
)

type RecoveryResult struct {
	TasksInterrupted       int
	RunsInterrupted        int
	ScriptRunsInterrupted  int
	ScheduleClaimsReleased int
	MessagesRequeued       int
}

// RecoverInterrupted repairs state left by a daemon exit. Running tasks return
// to ready so the durable queue can resume, while running runs become interrupted. It is atomic and idempotent. An
// optional timestamp makes restart tests deterministic.
func (s *Store) RecoverInterrupted(ctx context.Context, at ...time.Time) (RecoveryResult, error) {
	if len(at) > 1 {
		return RecoveryResult{}, fmt.Errorf("store: recover interrupted: at most one timestamp may be supplied")
	}
	when := s.now()
	if len(at) == 1 {
		when = defaultTime(at[0], when)
	}
	stamp := formatTime(when)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: begin recovery: %w", err)
	}
	defer tx.Rollback()

	runResult, err := tx.ExecContext(ctx, `
UPDATE runs
SET status = 'interrupted', ended_at = COALESCE(ended_at, ?), pid = 0,
    error = CASE WHEN error = '' THEN 'interrupted by daemon restart' ELSE error END
WHERE status = 'running'`, stamp)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recover runs: %w", err)
	}
	taskResult, err := tx.ExecContext(ctx, `
UPDATE tasks
SET status = 'ready', current_run_id = NULL, updated_at = ?
WHERE status = 'running'`, stamp)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recover tasks: %w", err)
	}
	scriptRunResult, err := tx.ExecContext(ctx, `
UPDATE script_runs
SET status = 'interrupted', ended_at = COALESCE(ended_at, ?), pid = 0,
    error = CASE WHEN error = '' THEN 'interrupted by daemon restart' ELSE error END
WHERE status = 'running'`, stamp)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recover script runs: %w", err)
	}
	scheduleResult, err := tx.ExecContext(ctx, `
UPDATE schedules
SET claim_token = '', claimed_at = NULL, lease_until = NULL, updated_at = ?
WHERE claim_token <> ''`, stamp)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recover schedule claims: %w", err)
	}
	messageResult, err := tx.ExecContext(ctx, `
UPDATE messages SET status = 'queued', updated_at = ?
WHERE role = 'user' AND status = 'processing'`, stamp)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recover chat queue: %w", err)
	}
	runs, err := runResult.RowsAffected()
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recovered run count: %w", err)
	}
	tasks, err := taskResult.RowsAffected()
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recovered task count: %w", err)
	}
	scriptRuns, err := scriptRunResult.RowsAffected()
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recovered script run count: %w", err)
	}
	scheduleClaims, err := scheduleResult.RowsAffected()
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recovered schedule claim count: %w", err)
	}
	messages, err := messageResult.RowsAffected()
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("store: recovered message count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RecoveryResult{}, fmt.Errorf("store: commit recovery: %w", err)
	}
	return RecoveryResult{
		TasksInterrupted: int(tasks), RunsInterrupted: int(runs),
		ScriptRunsInterrupted: int(scriptRuns), ScheduleClaimsReleased: int(scheduleClaims),
		MessagesRequeued: int(messages),
	}, nil
}
