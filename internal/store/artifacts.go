package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nabu-sh/nabu/internal/domain"
)

const artifactColumns = `id, task_id, run_id, script_run_id, kind, name, path, url, metadata, created_at`

func (s *Store) CreateArtifact(ctx context.Context, artifact domain.Artifact) (domain.Artifact, error) {
	if artifact.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Artifact{}, err
		}
		artifact.ID = id
	}
	artifact.CreatedAt = defaultTime(artifact.CreatedAt, s.now())
	if _, err := s.db.ExecContext(ctx, `INSERT INTO artifacts (`+artifactColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.ID, nullableText(artifact.TaskID), nullableText(artifact.RunID), nullableText(artifact.ScriptRunID), artifact.Kind,
		artifact.Name, artifact.Path, artifact.URL, nullableBytes(artifact.Metadata), formatTime(artifact.CreatedAt)); err != nil {
		return domain.Artifact{}, fmt.Errorf("store: create artifact: %w", err)
	}
	return artifact, nil
}

func (s *Store) GetArtifact(ctx context.Context, id string) (domain.Artifact, error) {
	return scanArtifact(s.db.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", id))
}

func (s *Store) ListArtifacts(ctx context.Context, taskID, runID string) ([]domain.Artifact, error) {
	query := "SELECT " + artifactColumns + " FROM artifacts"
	var args []any
	switch {
	case taskID != "" && runID != "":
		query += " WHERE task_id = ? AND run_id = ?"
		args = append(args, taskID, runID)
	case taskID != "":
		query += " WHERE task_id = ?"
		args = append(args, taskID)
	case runID != "":
		query += " WHERE run_id = ?"
		args = append(args, runID)
	}
	query += " ORDER BY created_at, id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list artifacts: %w", err)
	}
	defer rows.Close()
	var artifacts []domain.Artifact
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list artifacts: %w", err)
	}
	return artifacts, nil
}

func (s *Store) ListScriptRunArtifacts(ctx context.Context, scriptRunID string) ([]domain.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE script_run_id = ? ORDER BY created_at, id", scriptRunID)
	if err != nil {
		return nil, fmt.Errorf("store: list script run artifacts: %w", err)
	}
	defer rows.Close()
	var artifacts []domain.Artifact
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list script run artifacts: %w", err)
	}
	return artifacts, nil
}

func (s *Store) UpdateArtifact(ctx context.Context, artifact domain.Artifact) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE artifacts SET task_id = ?, run_id = ?, script_run_id = ?, kind = ?, name = ?, path = ?, url = ?, metadata = ? WHERE id = ?`,
		nullableText(artifact.TaskID), nullableText(artifact.RunID), nullableText(artifact.ScriptRunID), artifact.Kind, artifact.Name,
		artifact.Path, artifact.URL, nullableBytes(artifact.Metadata), artifact.ID)
	if err != nil {
		return fmt.Errorf("store: update artifact: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update artifact result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("artifact %q: %w", artifact.ID, ErrNotFound)
	}
	return nil
}

func (s *Store) DeleteArtifact(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM artifacts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete artifact: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete artifact result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("artifact %q: %w", id, ErrNotFound)
	}
	return nil
}

func scanArtifact(row rowScanner) (domain.Artifact, error) {
	var artifact domain.Artifact
	var taskID, runID, scriptRunID sql.NullString
	var metadata []byte
	var created string
	if err := row.Scan(&artifact.ID, &taskID, &runID, &scriptRunID, &artifact.Kind, &artifact.Name,
		&artifact.Path, &artifact.URL, &metadata, &created); err != nil {
		return domain.Artifact{}, fmt.Errorf("store: get artifact: %w", notFound("artifact", err))
	}
	artifact.TaskID = taskID.String
	artifact.RunID = runID.String
	artifact.ScriptRunID = scriptRunID.String
	artifact.Metadata = metadata
	var err error
	artifact.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}
