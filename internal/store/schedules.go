package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const scheduleColumns = `id, workspace_id, name, enabled, kind, expression, interval_seconds, payload,
last_run_at, next_run_at, claim_token, claimed_at, lease_until, last_error, created_at, updated_at`

type ScheduleFilter struct {
	WorkspaceID string
	Enabled     *bool
	Kinds       []domain.ScheduleKind
	Limit       int
}

func (s *Store) CreateSchedule(ctx context.Context, schedule domain.Schedule) (domain.Schedule, error) {
	var err error
	schedule.WorkspaceID, err = s.defaultWorkspaceID(ctx, schedule.WorkspaceID)
	if err != nil {
		return domain.Schedule{}, err
	}
	if schedule.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Schedule{}, err
		}
		schedule.ID = id
	}
	if err := validateSchedule(schedule); err != nil {
		return domain.Schedule{}, err
	}
	now := s.now()
	schedule.CreatedAt = defaultTime(schedule.CreatedAt, now)
	schedule.UpdatedAt = defaultTime(schedule.UpdatedAt, schedule.CreatedAt)
	schedule.Cadence = domain.ScheduleCadence{Expression: schedule.Expression, IntervalSeconds: schedule.IntervalSeconds}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO schedules(id, name, expression, enabled, action, last_run_at, next_run_at, created_at, updated_at,
    kind, interval_seconds, payload, claim_token, claimed_at, lease_until, last_error, workspace_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schedule.ID, schedule.Name, schedule.Expression, schedule.Enabled, schedule.Kind,
		nullableTime(schedule.LastRunAt), nullableTime(schedule.NextRunAt), formatTime(schedule.CreatedAt),
		formatTime(schedule.UpdatedAt), schedule.Kind, schedule.IntervalSeconds, nullableBytes(schedule.Payload),
		schedule.ClaimToken, nullableTime(schedule.ClaimedAt), nullableTime(schedule.LeaseUntil), schedule.LastError,
		nullableText(schedule.WorkspaceID)); err != nil {
		return domain.Schedule{}, fmt.Errorf("store: create schedule: %w", err)
	}
	return schedule, nil
}

func (s *Store) GetSchedule(ctx context.Context, id string) (domain.Schedule, error) {
	return scanSchedule(s.db.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", id))
}

func (s *Store) ListSchedules(ctx context.Context, filter ScheduleFilter) ([]domain.Schedule, error) {
	query := "SELECT " + scheduleColumns + " FROM schedules WHERE 1 = 1"
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
	if len(filter.Kinds) > 0 {
		placeholders := make([]string, len(filter.Kinds))
		for i, kind := range filter.Kinds {
			placeholders[i] = "?"
			args = append(args, kind)
		}
		query += " AND kind IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY enabled DESC, next_run_at, created_at, id"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list schedules: %w", err)
	}
	defer rows.Close()
	var schedules []domain.Schedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list schedules: %w", err)
	}
	return schedules, nil
}

// UpdateSchedule changes configuration and due times without disturbing an
// in-flight durable claim.
func (s *Store) UpdateSchedule(ctx context.Context, schedule domain.Schedule) error {
	if err := validateSchedule(schedule); err != nil {
		return err
	}
	schedule.UpdatedAt = defaultTime(schedule.UpdatedAt, s.now())
	result, err := s.db.ExecContext(ctx, `
UPDATE schedules SET workspace_id = ?, name = ?, expression = ?, enabled = ?, action = ?, kind = ?,
    interval_seconds = ?, payload = ?, last_run_at = ?, next_run_at = ?, updated_at = ?
WHERE id = ?`, nullableText(schedule.WorkspaceID), schedule.Name, schedule.Expression, schedule.Enabled, schedule.Kind,
		schedule.Kind, schedule.IntervalSeconds, nullableBytes(schedule.Payload), nullableTime(schedule.LastRunAt),
		nullableTime(schedule.NextRunAt), formatTime(schedule.UpdatedAt), schedule.ID)
	if err != nil {
		return fmt.Errorf("store: update schedule: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update schedule result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("schedule %q: %w", schedule.ID, ErrNotFound)
	}
	return nil
}

// ClaimDueSchedule atomically leases the oldest due enabled schedule. Expired
// leases are eligible immediately, which makes claims self-recovering after a
// daemon restart.
func (s *Store) ClaimDueSchedule(ctx context.Context, now time.Time, lease time.Duration) (domain.Schedule, error) {
	now = defaultTime(now, s.now())
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	token, err := newID()
	if err != nil {
		return domain.Schedule{}, err
	}
	leaseUntil := now.Add(lease)
	row := s.db.QueryRowContext(ctx, `
UPDATE schedules
SET claim_token = ?, claimed_at = ?, lease_until = ?, updated_at = ?
WHERE id = (
    SELECT id FROM schedules
    WHERE enabled = 1
      AND next_run_at IS NOT NULL
      AND next_run_at <= ?
	  AND (workspace_id IS NULL OR EXISTS (
	      SELECT 1 FROM workspaces w WHERE w.id = schedules.workspace_id AND w.context_ready = 1
	  ))
      AND (claim_token = '' OR lease_until IS NULL OR lease_until <= ?)
    ORDER BY next_run_at, created_at, id
    LIMIT 1
)
AND enabled = 1
AND (claim_token = '' OR lease_until IS NULL OR lease_until <= ?)
RETURNING `+scheduleColumns,
		token, formatTime(now), formatTime(leaseUntil), formatTime(now),
		formatTime(now), formatTime(now), formatTime(now))
	return scanSchedule(row)
}

// FinishScheduleClaim marks an execution attempt and releases its lease. The
// token prevents a stale worker from finishing a claim that has been recovered
// by another daemon.
func (s *Store) FinishScheduleClaim(ctx context.Context, id, token string, nextRunAt *time.Time, lastError string, at time.Time) error {
	at = defaultTime(at, s.now())
	result, err := s.db.ExecContext(ctx, `
UPDATE schedules
SET last_run_at = ?, next_run_at = ?, claim_token = '', claimed_at = NULL,
    lease_until = NULL, last_error = ?, updated_at = ?
WHERE id = ? AND claim_token = ?`, formatTime(at), nullableTime(nextRunAt), lastError,
		formatTime(at), id, token)
	if err != nil {
		return fmt.Errorf("store: finish schedule claim: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: finish schedule claim result: %w", err)
	}
	if count == 0 {
		return s.scheduleClaimError(ctx, id)
	}
	return nil
}

// ReleaseScheduleClaim abandons a claim without recording a completed run.
func (s *Store) ReleaseScheduleClaim(ctx context.Context, id, token string, nextRunAt *time.Time, lastError string) error {
	now := s.now()
	result, err := s.db.ExecContext(ctx, `
UPDATE schedules
SET next_run_at = ?, claim_token = '', claimed_at = NULL, lease_until = NULL,
    last_error = ?, updated_at = ?
WHERE id = ? AND claim_token = ?`, nullableTime(nextRunAt), lastError, formatTime(now), id, token)
	if err != nil {
		return fmt.Errorf("store: release schedule claim: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: release schedule claim result: %w", err)
	}
	if count == 0 {
		return s.scheduleClaimError(ctx, id)
	}
	return nil
}

func (s *Store) ReleaseExpiredScheduleClaims(ctx context.Context, now time.Time) (int, error) {
	now = defaultTime(now, s.now())
	result, err := s.db.ExecContext(ctx, `
UPDATE schedules
SET claim_token = '', claimed_at = NULL, lease_until = NULL, updated_at = ?
WHERE claim_token <> '' AND lease_until IS NOT NULL AND lease_until <= ?`, formatTime(now), formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("store: release expired schedule claims: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: released schedule claim count: %w", err)
	}
	return int(count), nil
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM schedules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete schedule: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete schedule result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("schedule %q: %w", id, ErrNotFound)
	}
	return nil
}

func validateSchedule(schedule domain.Schedule) error {
	switch schedule.Kind {
	case domain.ScheduleScript, domain.ScheduleTask, domain.ScheduleOrient:
	default:
		return fmt.Errorf("store: invalid schedule kind %q", schedule.Kind)
	}
	if schedule.IntervalSeconds < 0 {
		return fmt.Errorf("store: schedule interval must not be negative")
	}
	if schedule.IntervalSeconds == 0 && strings.TrimSpace(schedule.Expression) == "" {
		return fmt.Errorf("store: schedule requires an interval or expression")
	}
	return nil
}

func (s *Store) scheduleClaimError(ctx context.Context, id string) error {
	var token string
	err := s.db.QueryRowContext(ctx, "SELECT claim_token FROM schedules WHERE id = ?", id).Scan(&token)
	if err == sql.ErrNoRows {
		return fmt.Errorf("schedule %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: inspect schedule claim: %w", err)
	}
	return fmt.Errorf("schedule %q has another claim: %w", id, ErrClaimLost)
}

func scanSchedule(row rowScanner) (domain.Schedule, error) {
	var schedule domain.Schedule
	var payload []byte
	var lastRun, nextRun, claimed, lease sql.NullString
	var created, updated string
	var workspaceID sql.NullString
	if err := row.Scan(&schedule.ID, &workspaceID, &schedule.Name, &schedule.Enabled, &schedule.Kind,
		&schedule.Expression, &schedule.IntervalSeconds, &payload, &lastRun, &nextRun,
		&schedule.ClaimToken, &claimed, &lease, &schedule.LastError, &created, &updated); err != nil {
		return domain.Schedule{}, fmt.Errorf("store: get schedule: %w", notFound("schedule", err))
	}
	schedule.WorkspaceID = workspaceID.String
	schedule.Payload = payload
	var err error
	schedule.LastRunAt, err = parseNullableTime(lastRun)
	if err != nil {
		return domain.Schedule{}, err
	}
	schedule.NextRunAt, err = parseNullableTime(nextRun)
	if err != nil {
		return domain.Schedule{}, err
	}
	schedule.ClaimedAt, err = parseNullableTime(claimed)
	if err != nil {
		return domain.Schedule{}, err
	}
	schedule.LeaseUntil, err = parseNullableTime(lease)
	if err != nil {
		return domain.Schedule{}, err
	}
	schedule.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Schedule{}, err
	}
	schedule.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Schedule{}, err
	}
	schedule.Cadence = domain.ScheduleCadence{Expression: schedule.Expression, IntervalSeconds: schedule.IntervalSeconds}
	return schedule, nil
}
