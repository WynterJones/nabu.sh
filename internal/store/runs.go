package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

const runColumns = `id, task_id, type, status, pid, working_directory, command,
session_id, attempt, started_at, ended_at, exit_code, stdout_path, stderr_path,
raw_output, result, error`

type RunFilter struct {
	TaskID   string
	Statuses []domain.RunStatus
	Types    []domain.RunType
	Limit    int
}

func (s *Store) CreateRun(ctx context.Context, run domain.Run) (domain.Run, error) {
	if run.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Run{}, err
		}
		run.ID = id
	}
	if run.Type == "" {
		run.Type = domain.RunExecute
	}
	if run.Status == "" {
		run.Status = domain.RunPending
	}
	if run.Attempt == 0 {
		run.Attempt = 1
	}
	run.StartedAt = defaultTime(run.StartedAt, s.now())
	command, result, err := encodeRunJSON(run)
	if err != nil {
		return domain.Run{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO runs (`+runColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, nullableText(run.TaskID), run.Type, run.Status, run.PID, run.WorkingDirectory, command,
		run.SessionID, run.Attempt, formatTime(run.StartedAt), nullableTime(run.EndedAt), nullableInt(run.ExitCode),
		run.StdoutPath, run.StderrPath, run.RawOutput, result, run.Error,
	); err != nil {
		return domain.Run{}, fmt.Errorf("store: create run: %w", err)
	}
	return run, nil
}

func (s *Store) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs WHERE id = ?", id))
}

func (s *Store) ListRuns(ctx context.Context, filter RunFilter) ([]domain.Run, error) {
	query := "SELECT " + runColumns + " FROM runs"
	var where []string
	var args []any
	if filter.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, filter.TaskID)
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, status)
		}
		where = append(where, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(filter.Types) > 0 {
		placeholders := make([]string, len(filter.Types))
		for i, runType := range filter.Types {
			placeholders[i] = "?"
			args = append(args, runType)
		}
		where = append(where, "type IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY started_at DESC, id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list runs: %w", err)
	}
	defer rows.Close()
	var runs []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list runs: %w", err)
	}
	return runs, nil
}

func (s *Store) UpdateRun(ctx context.Context, run domain.Run) error {
	command, resultJSON, err := encodeRunJSON(run)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE runs SET task_id = ?, type = ?, status = ?, pid = ?, working_directory = ?, command = ?,
    session_id = ?, attempt = ?, started_at = ?, ended_at = ?, exit_code = ?, stdout_path = ?,
    stderr_path = ?, raw_output = ?, result = ?, error = ?
WHERE id = ?`,
		nullableText(run.TaskID), run.Type, run.Status, run.PID, run.WorkingDirectory, command,
		run.SessionID, run.Attempt, formatTime(run.StartedAt), nullableTime(run.EndedAt), nullableInt(run.ExitCode),
		run.StdoutPath, run.StderrPath, run.RawOutput, resultJSON, run.Error, run.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update run result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("run %q: %w", run.ID, ErrNotFound)
	}
	return nil
}

func (s *Store) DeleteRun(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM runs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete run result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("run %q: %w", id, ErrNotFound)
	}
	return nil
}

func encodeRunJSON(run domain.Run) ([]byte, any, error) {
	command, err := json.Marshal(run.Command)
	if err != nil {
		return nil, nil, fmt.Errorf("store: encode run command: %w", err)
	}
	var result any
	if run.Result != nil {
		encoded, err := json.Marshal(run.Result)
		if err != nil {
			return nil, nil, fmt.Errorf("store: encode run result: %w", err)
		}
		result = encoded
	}
	return command, result, nil
}

func scanRun(row rowScanner) (domain.Run, error) {
	var run domain.Run
	var taskID sql.NullString
	var command []byte
	var started string
	var ended sql.NullString
	var exitCode sql.NullInt64
	var result sql.NullString
	if err := row.Scan(
		&run.ID, &taskID, &run.Type, &run.Status, &run.PID, &run.WorkingDirectory, &command,
		&run.SessionID, &run.Attempt, &started, &ended, &exitCode, &run.StdoutPath, &run.StderrPath,
		&run.RawOutput, &result, &run.Error,
	); err != nil {
		return domain.Run{}, fmt.Errorf("store: get run: %w", notFound("run", err))
	}
	run.TaskID = taskID.String
	if len(command) > 0 {
		if err := json.Unmarshal(command, &run.Command); err != nil {
			return domain.Run{}, fmt.Errorf("store: decode run command: %w", err)
		}
	}
	if result.Valid && result.String != "" {
		var decoded domain.RunResult
		if err := json.Unmarshal([]byte(result.String), &decoded); err != nil {
			return domain.Run{}, fmt.Errorf("store: decode run result: %w", err)
		}
		run.Result = &decoded
	}
	var err error
	run.StartedAt, err = parseTime(started)
	if err != nil {
		return domain.Run{}, err
	}
	if run.EndedAt, err = parseNullableTime(ended); err != nil {
		return domain.Run{}, err
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		run.ExitCode = &value
	}
	return run, nil
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
