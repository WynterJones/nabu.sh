package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const memoryUpdateColumns = `id, workspace_id, target, content, source, status, rejection_note, created_at, resolved_at`

type MemoryUpdateFilter struct {
	WorkspaceID string
	Statuses    []domain.MemoryUpdateStatus
	Target      domain.MemoryTarget
	Limit       int
}

func (s *Store) CreateMemoryUpdate(ctx context.Context, update domain.MemoryUpdate) (domain.MemoryUpdate, error) {
	var err error
	update.WorkspaceID, err = s.defaultWorkspaceID(ctx, update.WorkspaceID)
	if err != nil {
		return domain.MemoryUpdate{}, err
	}
	if update.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.MemoryUpdate{}, err
		}
		update.ID = id
	}
	if update.Status == "" {
		update.Status = domain.MemoryProposed
	}
	if update.Status != domain.MemoryProposed {
		return domain.MemoryUpdate{}, fmt.Errorf("create memory update in %q: %w", update.Status, ErrInvalidTransition)
	}
	if update.Target != domain.MemoryDurable && update.Target != domain.MemoryDaily && update.Target != domain.MemorySoul {
		return domain.MemoryUpdate{}, fmt.Errorf("store: invalid memory target %q", update.Target)
	}
	update.CreatedAt = defaultTime(update.CreatedAt, s.now())
	if _, err := s.db.ExecContext(ctx, "INSERT INTO memory_updates ("+memoryUpdateColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		update.ID, nullableText(update.WorkspaceID), update.Target, update.Content, update.Source, update.Status,
		update.RejectionNote, formatTime(update.CreatedAt), nullableTime(update.ResolvedAt)); err != nil {
		return domain.MemoryUpdate{}, fmt.Errorf("store: create memory update: %w", err)
	}
	return update, nil
}

func (s *Store) GetMemoryUpdate(ctx context.Context, id string) (domain.MemoryUpdate, error) {
	return scanMemoryUpdate(s.db.QueryRowContext(ctx, "SELECT "+memoryUpdateColumns+" FROM memory_updates WHERE id = ?", id))
}

func (s *Store) ListMemoryUpdates(ctx context.Context, filter MemoryUpdateFilter) ([]domain.MemoryUpdate, error) {
	query := "SELECT " + memoryUpdateColumns + " FROM memory_updates WHERE 1 = 1"
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
	if filter.Target != "" {
		query += " AND target = ?"
		args = append(args, filter.Target)
	}
	query += " ORDER BY created_at DESC, id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list memory updates: %w", err)
	}
	defer rows.Close()
	var updates []domain.MemoryUpdate
	for rows.Next() {
		update, err := scanMemoryUpdate(rows)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memory updates: %w", err)
	}
	return updates, nil
}

func (s *Store) ResolveMemoryUpdate(ctx context.Context, id string, status domain.MemoryUpdateStatus, rejectionNote string, at time.Time) (domain.MemoryUpdate, error) {
	if status != domain.MemoryApplied && status != domain.MemoryRejected {
		return domain.MemoryUpdate{}, fmt.Errorf("resolve memory update as %q: %w", status, ErrInvalidTransition)
	}
	at = defaultTime(at, s.now())
	row := s.db.QueryRowContext(ctx, `
UPDATE memory_updates
SET status = ?, rejection_note = ?, resolved_at = ?
WHERE id = ? AND status = 'proposed'
RETURNING `+memoryUpdateColumns, status, rejectionNote, formatTime(at), id)
	update, err := scanMemoryUpdate(row)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return domain.MemoryUpdate{}, err
		}
		var existing string
		inspectErr := s.db.QueryRowContext(ctx, "SELECT status FROM memory_updates WHERE id = ?", id).Scan(&existing)
		if inspectErr == sql.ErrNoRows {
			return domain.MemoryUpdate{}, fmt.Errorf("memory update %q: %w", id, ErrNotFound)
		}
		if inspectErr != nil {
			return domain.MemoryUpdate{}, fmt.Errorf("store: inspect memory update: %w", inspectErr)
		}
		return domain.MemoryUpdate{}, fmt.Errorf("memory update %q is %s: %w", id, existing, ErrInvalidTransition)
	}
	return update, nil
}

func scanMemoryUpdate(row rowScanner) (domain.MemoryUpdate, error) {
	var update domain.MemoryUpdate
	var created string
	var resolved sql.NullString
	var workspaceID sql.NullString
	if err := row.Scan(&update.ID, &workspaceID, &update.Target, &update.Content, &update.Source,
		&update.Status, &update.RejectionNote, &created, &resolved); err != nil {
		return domain.MemoryUpdate{}, fmt.Errorf("store: get memory update: %w", notFound("memory update", err))
	}
	update.WorkspaceID = workspaceID.String
	var err error
	update.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.MemoryUpdate{}, err
	}
	update.ResolvedAt, err = parseNullableTime(resolved)
	if err != nil {
		return domain.MemoryUpdate{}, err
	}
	return update, nil
}
