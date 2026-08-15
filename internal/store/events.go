package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nabu-sh/nabu/internal/domain"
)

const eventColumns = `id, workspace_id, type, entity_id, data, created_at`

// AppendEvent records an immutable event and returns it with its sequence ID.
func (s *Store) AppendEvent(ctx context.Context, event domain.Event) (domain.Event, error) {
	var err error
	event.WorkspaceID, err = s.defaultWorkspaceID(ctx, event.WorkspaceID)
	if err != nil {
		return domain.Event{}, err
	}
	event.CreatedAt = defaultTime(event.CreatedAt, s.now())
	result, err := s.db.ExecContext(ctx, `INSERT INTO events(workspace_id, type, entity_id, data, created_at) VALUES (?, ?, ?, ?, ?)`,
		nullableText(event.WorkspaceID), event.Type, event.EntityID, nullableBytes(event.Data), formatTime(event.CreatedAt))
	if err != nil {
		return domain.Event{}, fmt.Errorf("store: append event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	if err != nil {
		return domain.Event{}, fmt.Errorf("store: event id: %w", err)
	}
	return event, nil
}

func (s *Store) CreateEvent(ctx context.Context, event domain.Event) (domain.Event, error) {
	return s.AppendEvent(ctx, event)
}

func (s *Store) GetEvent(ctx context.Context, id int64) (domain.Event, error) {
	return scanEvent(s.db.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM events WHERE id = ?", id))
}

// ListEvents returns events after afterID in ascending sequence order. A
// non-positive limit uses a conservative default.
func (s *Store) ListEvents(ctx context.Context, afterID int64, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	return s.ListWorkspaceEvents(ctx, workspaceID, afterID, limit)
}

func (s *Store) ListWorkspaceEvents(ctx context.Context, workspaceID string, afterID int64, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE id > ? AND COALESCE(workspace_id, '') = ? ORDER BY id LIMIT ?", afterID, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list events: %w", err)
	}
	defer rows.Close()
	var events []domain.Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list events: %w", err)
	}
	return events, nil
}

// RecentEvents returns the newest events in chronological order, convenient
// for orientation context packets.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	return s.RecentWorkspaceEvents(ctx, workspaceID, limit)
}

func (s *Store) RecentWorkspaceEvents(ctx context.Context, workspaceID string, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE COALESCE(workspace_id, '') = ? ORDER BY id DESC LIMIT ?", workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent events: %w", err)
	}
	defer rows.Close()
	var events []domain.Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: recent events: %w", err)
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}

func (s *Store) DeleteEvent(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete event: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete event result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("event %d: %w", id, ErrNotFound)
	}
	return nil
}

func scanEvent(row rowScanner) (domain.Event, error) {
	var event domain.Event
	var data []byte
	var created string
	var workspaceID sql.NullString
	if err := row.Scan(&event.ID, &workspaceID, &event.Type, &event.EntityID, &data, &created); err != nil {
		return domain.Event{}, fmt.Errorf("store: get event: %w", notFound("event", err))
	}
	event.WorkspaceID = workspaceID.String
	event.Data = data
	var err error
	event.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Event{}, err
	}
	return event, nil
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

var _ rowScanner = (*sql.Rows)(nil)
