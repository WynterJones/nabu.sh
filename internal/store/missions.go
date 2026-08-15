package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nabu-sh/nabu/internal/domain"
)

const missionColumns = `id, workspace_id, statement, context, active, created_at, updated_at`

func (s *Store) CreateMission(ctx context.Context, mission domain.Mission) (domain.Mission, error) {
	var err error
	mission.WorkspaceID, err = s.defaultWorkspaceID(ctx, mission.WorkspaceID)
	if err != nil {
		return domain.Mission{}, err
	}
	if mission.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Mission{}, err
		}
		mission.ID = id
	}
	now := s.now()
	mission.CreatedAt = defaultTime(mission.CreatedAt, now)
	mission.UpdatedAt = defaultTime(mission.UpdatedAt, mission.CreatedAt)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("store: begin create mission: %w", err)
	}
	defer tx.Rollback()
	if mission.Active {
		if _, err := tx.ExecContext(ctx, "UPDATE missions SET active = 0, updated_at = ? WHERE active = 1 AND COALESCE(workspace_id, '') = ?", formatTime(now), mission.WorkspaceID); err != nil {
			return domain.Mission{}, fmt.Errorf("store: deactivate mission: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO missions (`+missionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		mission.ID, nullableText(mission.WorkspaceID), mission.Statement, mission.Context, mission.Active,
		formatTime(mission.CreatedAt), formatTime(mission.UpdatedAt),
	); err != nil {
		return domain.Mission{}, fmt.Errorf("store: create mission: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Mission{}, fmt.Errorf("store: commit mission: %w", err)
	}
	return mission, nil
}

func (s *Store) GetMission(ctx context.Context, id string) (domain.Mission, error) {
	return scanMission(s.db.QueryRowContext(ctx, "SELECT "+missionColumns+" FROM missions WHERE id = ?", id))
}

func (s *Store) ActiveMission(ctx context.Context) (domain.Mission, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Mission{}, err
	}
	return s.GetMissionForWorkspace(ctx, workspaceID)
}

func (s *Store) GetMissionForWorkspace(ctx context.Context, workspaceID string) (domain.Mission, error) {
	return scanMission(s.db.QueryRowContext(ctx, `SELECT `+missionColumns+`
FROM missions WHERE active = 1 AND COALESCE(workspace_id, '') = ?`, workspaceID))
}

func (s *Store) ListMissions(ctx context.Context) ([]domain.Mission, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+missionColumns+" FROM missions ORDER BY created_at DESC, id DESC")
	if err != nil {
		return nil, fmt.Errorf("store: list missions: %w", err)
	}
	defer rows.Close()
	var missions []domain.Mission
	for rows.Next() {
		mission, err := scanMission(rows)
		if err != nil {
			return nil, err
		}
		missions = append(missions, mission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list missions: %w", err)
	}
	return missions, nil
}

func (s *Store) ListMissionsForWorkspace(ctx context.Context, workspaceID string) ([]domain.Mission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+missionColumns+`
FROM missions WHERE COALESCE(workspace_id, '') = ? ORDER BY created_at DESC, id DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("store: list workspace missions: %w", err)
	}
	defer rows.Close()
	var missions []domain.Mission
	for rows.Next() {
		mission, err := scanMission(rows)
		if err != nil {
			return nil, err
		}
		missions = append(missions, mission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list workspace missions: %w", err)
	}
	return missions, nil
}

func (s *Store) UpdateMission(ctx context.Context, mission domain.Mission) error {
	var err error
	mission.WorkspaceID, err = s.defaultWorkspaceID(ctx, mission.WorkspaceID)
	if err != nil {
		return err
	}
	mission.UpdatedAt = defaultTime(mission.UpdatedAt, s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update mission: %w", err)
	}
	defer tx.Rollback()
	if mission.Active {
		if _, err := tx.ExecContext(ctx, "UPDATE missions SET active = 0, updated_at = ? WHERE active = 1 AND id <> ? AND COALESCE(workspace_id, '') = ?", formatTime(mission.UpdatedAt), mission.ID, mission.WorkspaceID); err != nil {
			return fmt.Errorf("store: deactivate mission: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE missions SET workspace_id = ?, statement = ?, context = ?, active = ?, updated_at = ? WHERE id = ?`,
		nullableText(mission.WorkspaceID), mission.Statement, mission.Context, mission.Active, formatTime(mission.UpdatedAt), mission.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update mission: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update mission result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("mission %q: %w", mission.ID, ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit mission: %w", err)
	}
	return nil
}

func (s *Store) SetActiveMission(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin activate mission: %w", err)
	}
	defer tx.Rollback()
	now := formatTime(s.now())
	var workspaceID sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT workspace_id FROM missions WHERE id = ?", id).Scan(&workspaceID); err != nil {
		return fmt.Errorf("store: find mission scope: %w", notFound("mission", err))
	}
	if _, err := tx.ExecContext(ctx, "UPDATE missions SET active = 0, updated_at = ? WHERE active = 1 AND id <> ? AND COALESCE(workspace_id, '') = ?", now, id, workspaceID.String); err != nil {
		return fmt.Errorf("store: deactivate mission: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE missions SET active = 1, updated_at = ? WHERE id = ?", now, id)
	if err != nil {
		return fmt.Errorf("store: activate mission: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: activate mission result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("mission %q: %w", id, ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit active mission: %w", err)
	}
	return nil
}

func (s *Store) DeleteMission(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM missions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete mission: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete mission result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("mission %q: %w", id, ErrNotFound)
	}
	return nil
}

func scanMission(row rowScanner) (domain.Mission, error) {
	var mission domain.Mission
	var created, updated string
	var workspaceID sql.NullString
	if err := row.Scan(&mission.ID, &workspaceID, &mission.Statement, &mission.Context, &mission.Active, &created, &updated); err != nil {
		if err = notFound("mission", err); err != nil {
			return domain.Mission{}, fmt.Errorf("store: get mission: %w", err)
		}
	}
	mission.WorkspaceID = workspaceID.String
	var err error
	mission.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Mission{}, err
	}
	mission.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Mission{}, err
	}
	return mission, nil
}

var _ rowScanner = (*sql.Row)(nil)
