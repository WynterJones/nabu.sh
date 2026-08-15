package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const approvalColumns = `id, workspace_id, task_id, run_id, status, action, reason, proposed_change,
evidence, evidence_metadata, rejection_note, created_at, updated_at, expires_at, resolved_at`

type ApprovalFilter struct {
	WorkspaceID string
	Statuses    []domain.ApprovalStatus
	TaskID      string
	RunID       string
	Limit       int
}

// CreateApproval records a pending approval and atomically pauses a related
// non-terminal task at the approval boundary.
func (s *Store) CreateApproval(ctx context.Context, approval domain.Approval) (domain.Approval, error) {
	var err error
	if approval.WorkspaceID == "" && approval.TaskID != "" {
		var workspaceID sql.NullString
		if err := s.db.QueryRowContext(ctx, "SELECT workspace_id FROM tasks WHERE id = ?", approval.TaskID).Scan(&workspaceID); err != nil {
			return domain.Approval{}, fmt.Errorf("store: get approval task scope: %w", notFound("task", err))
		}
		approval.WorkspaceID = workspaceID.String
	}
	approval.WorkspaceID, err = s.defaultWorkspaceID(ctx, approval.WorkspaceID)
	if err != nil {
		return domain.Approval{}, err
	}
	if approval.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Approval{}, err
		}
		approval.ID = id
	}
	if approval.Status == "" {
		approval.Status = domain.ApprovalPending
	}
	if approval.Status != domain.ApprovalPending {
		return domain.Approval{}, fmt.Errorf("create approval in %q: %w", approval.Status, ErrInvalidTransition)
	}
	now := s.now()
	approval.CreatedAt = defaultTime(approval.CreatedAt, now)
	approval.UpdatedAt = defaultTime(approval.UpdatedAt, approval.CreatedAt)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Approval{}, fmt.Errorf("store: begin create approval: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO approvals (`+approvalColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		approval.ID, nullableText(approval.WorkspaceID), nullableText(approval.TaskID), nullableText(approval.RunID), approval.Status,
		approval.ProposedAction, approval.Why, approval.ProposedChange, approval.Evidence,
		nullableBytes(approval.EvidenceMetadata), approval.RejectionNote, formatTime(approval.CreatedAt),
		formatTime(approval.UpdatedAt), nullableTime(approval.ExpiresAt), nullableTime(approval.ResolvedAt)); err != nil {
		return domain.Approval{}, fmt.Errorf("store: create approval: %w", err)
	}
	if approval.TaskID != "" {
		if _, err := tx.ExecContext(ctx, `
UPDATE tasks SET status = 'needs_approval', updated_at = ?
WHERE id = ? AND status NOT IN ('completed','failed','cancelled')`,
			formatTime(approval.CreatedAt), approval.TaskID); err != nil {
			return domain.Approval{}, fmt.Errorf("store: pause task for approval: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Approval{}, fmt.Errorf("store: commit approval: %w", err)
	}
	return approval, nil
}

func (s *Store) GetApproval(ctx context.Context, id string) (domain.Approval, error) {
	return scanApproval(s.db.QueryRowContext(ctx, "SELECT "+approvalColumns+" FROM approvals WHERE id = ?", id))
}

func (s *Store) ListApprovals(ctx context.Context, filter ApprovalFilter) ([]domain.Approval, error) {
	query := "SELECT " + approvalColumns + " FROM approvals WHERE 1 = 1"
	var args []any
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query += " AND COALESCE(workspace_id, '') = ?"
	args = append(args, workspaceID)
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, status)
		}
		query += " AND status IN (" + strings.Join(placeholders, ",") + ")"
	}
	if filter.TaskID != "" {
		query += " AND task_id = ?"
		args = append(args, filter.TaskID)
	}
	if filter.RunID != "" {
		query += " AND run_id = ?"
		args = append(args, filter.RunID)
	}
	query += " ORDER BY created_at DESC, id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list approvals: %w", err)
	}
	defer rows.Close()
	var approvals []domain.Approval
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list approvals: %w", err)
	}
	return approvals, nil
}

// UpdateApproval edits the proposal details while it remains pending.
func (s *Store) UpdateApproval(ctx context.Context, approval domain.Approval) error {
	approval.UpdatedAt = defaultTime(approval.UpdatedAt, s.now())
	result, err := s.db.ExecContext(ctx, `
UPDATE approvals SET action = ?, reason = ?, proposed_change = ?, evidence = ?,
    evidence_metadata = ?, expires_at = ?, updated_at = ?
WHERE id = ? AND status = 'pending'`, approval.ProposedAction, approval.Why,
		approval.ProposedChange, approval.Evidence, nullableBytes(approval.EvidenceMetadata),
		nullableTime(approval.ExpiresAt), formatTime(approval.UpdatedAt), approval.ID)
	if err != nil {
		return fmt.Errorf("store: update approval: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update approval result: %w", err)
	}
	if count == 0 {
		return s.approvalMutationError(ctx, approval.ID)
	}
	return nil
}

// ResolveApproval performs a pending-only decision and the related task state
// transition in one transaction. Approved tasks return to ready; rejected and
// expired tasks become waiting for revised direction.
func (s *Store) ResolveApproval(ctx context.Context, id string, status domain.ApprovalStatus, rejectionNote string, at time.Time) (domain.Approval, error) {
	if status != domain.ApprovalApproved && status != domain.ApprovalRejected && status != domain.ApprovalExpired {
		return domain.Approval{}, fmt.Errorf("resolve approval as %q: %w", status, ErrInvalidTransition)
	}
	at = defaultTime(at, s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Approval{}, fmt.Errorf("store: begin resolve approval: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
UPDATE approvals SET status = ?, rejection_note = ?, updated_at = ?, resolved_at = ?
WHERE id = ? AND status = 'pending'`, status, rejectionNote, formatTime(at), formatTime(at), id)
	if err != nil {
		return domain.Approval{}, fmt.Errorf("store: resolve approval: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return domain.Approval{}, fmt.Errorf("store: resolve approval result: %w", err)
	}
	if count == 0 {
		return domain.Approval{}, approvalMutationErrorTx(ctx, tx, id)
	}
	approval, err := scanApproval(tx.QueryRowContext(ctx, "SELECT "+approvalColumns+" FROM approvals WHERE id = ?", id))
	if err != nil {
		return domain.Approval{}, err
	}
	if approval.TaskID != "" {
		taskStatus := domain.TaskWaiting
		if status == domain.ApprovalApproved {
			taskStatus = domain.TaskReady
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE tasks SET status = ?, current_run_id = NULL, updated_at = ?
WHERE id = ? AND status = 'needs_approval'`, taskStatus, formatTime(at), approval.TaskID); err != nil {
			return domain.Approval{}, fmt.Errorf("store: resume task after approval: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Approval{}, fmt.Errorf("store: commit approval resolution: %w", err)
	}
	return approval, nil
}

// ExpireApprovals resolves all pending approvals whose deadline has passed.
func (s *Store) ExpireApprovals(ctx context.Context, now time.Time) (int, error) {
	now = defaultTime(now, s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin expire approvals: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT id, task_id FROM approvals
WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at <= ?`, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("store: find expired approvals: %w", err)
	}
	type expired struct{ id, taskID string }
	var pending []expired
	for rows.Next() {
		var item expired
		var taskID sql.NullString
		if err := rows.Scan(&item.id, &taskID); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("store: scan expired approval: %w", err)
		}
		item.taskID = taskID.String
		pending = append(pending, item)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("store: close expired approvals: %w", err)
	}
	for _, item := range pending {
		if _, err := tx.ExecContext(ctx, `
UPDATE approvals SET status = 'expired', updated_at = ?, resolved_at = ?
WHERE id = ? AND status = 'pending'`, formatTime(now), formatTime(now), item.id); err != nil {
			return 0, fmt.Errorf("store: expire approval: %w", err)
		}
		if item.taskID != "" {
			if _, err := tx.ExecContext(ctx, `
UPDATE tasks SET status = 'waiting', current_run_id = NULL, updated_at = ?
WHERE id = ? AND status = 'needs_approval'`, formatTime(now), item.taskID); err != nil {
				return 0, fmt.Errorf("store: wait task after expiration: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit expired approvals: %w", err)
	}
	return len(pending), nil
}

func (s *Store) DeleteApproval(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM approvals WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete approval: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete approval result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("approval %q: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Store) approvalMutationError(ctx context.Context, id string) error {
	return approvalMutationErrorTx(ctx, s.db, id)
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func approvalMutationErrorTx(ctx context.Context, queryer queryRower, id string) error {
	var status string
	err := queryer.QueryRowContext(ctx, "SELECT status FROM approvals WHERE id = ?", id).Scan(&status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("approval %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: inspect approval: %w", err)
	}
	return fmt.Errorf("approval %q is %s: %w", id, status, ErrInvalidTransition)
}

func scanApproval(row rowScanner) (domain.Approval, error) {
	var approval domain.Approval
	var taskID, runID sql.NullString
	var metadata []byte
	var created string
	var updated, expires, resolved sql.NullString
	var workspaceID sql.NullString
	if err := row.Scan(&approval.ID, &workspaceID, &taskID, &runID, &approval.Status,
		&approval.ProposedAction, &approval.Why, &approval.ProposedChange,
		&approval.Evidence, &metadata, &approval.RejectionNote,
		&created, &updated, &expires, &resolved); err != nil {
		return domain.Approval{}, fmt.Errorf("store: get approval: %w", notFound("approval", err))
	}
	approval.WorkspaceID = workspaceID.String
	approval.TaskID = taskID.String
	approval.RunID = runID.String
	approval.EvidenceMetadata = metadata
	var err error
	approval.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Approval{}, err
	}
	if updated.Valid {
		approval.UpdatedAt, err = parseTime(updated.String)
	} else {
		approval.UpdatedAt = approval.CreatedAt
	}
	if err != nil {
		return domain.Approval{}, err
	}
	if approval.ExpiresAt, err = parseNullableTime(expires); err != nil {
		return domain.Approval{}, err
	}
	if approval.ResolvedAt, err = parseNullableTime(resolved); err != nil {
		return domain.Approval{}, err
	}
	return approval, nil
}
