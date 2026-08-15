package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const taskColumns = `id, title, purpose, why, status, priority, definition_of_done,
workspace_id, created_by, parent_task_id, current_run_id, result,
created_at, updated_at, planned_at, run_requested_at, started_at, completed_at`

const MaximumTaskDependencies = 64

type TaskFilter struct {
	Statuses    []domain.TaskStatus
	WorkspaceID string
	PlannedFrom *time.Time
	PlannedTo   *time.Time
	Unplanned   bool
	Limit       int
}

func (s *Store) CreateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	var err error
	task.WorkspaceID, err = s.defaultWorkspaceID(ctx, task.WorkspaceID)
	if err != nil {
		return domain.Task{}, err
	}
	if task.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Task{}, err
		}
		task.ID = id
	}
	if task.Status == "" {
		task.Status = domain.TaskIdea
	}
	if task.Priority == "" {
		task.Priority = domain.PriorityNormal
	}
	now := s.now()
	task.CreatedAt = defaultTime(task.CreatedAt, now)
	task.UpdatedAt = defaultTime(task.UpdatedAt, task.CreatedAt)
	done, result, err := encodeTaskJSON(task)
	if err != nil {
		return domain.Task{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, fmt.Errorf("store: begin create task: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO tasks (`+taskColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Title, task.Purpose, task.Why, task.Status, task.Priority, done,
		nullableText(task.WorkspaceID), task.CreatedBy, nullableText(task.ParentTaskID), nullableText(task.CurrentRunID), result,
		formatTime(task.CreatedAt), formatTime(task.UpdatedAt), nullableTime(task.PlannedAt), nullableTime(task.RunRequestedAt), nullableTime(task.StartedAt), nullableTime(task.CompletedAt),
	); err != nil {
		return domain.Task{}, fmt.Errorf("store: create task: %w", err)
	}
	dependencies, err := s.replaceTaskDependenciesTx(ctx, tx, task.WorkspaceID, task.ID, task.DependsOnTaskIDs)
	if err != nil {
		return domain.Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("store: commit create task: %w", err)
	}
	task.DependsOnTaskIDs = dependencies
	return task, nil
}

func (s *Store) GetTask(ctx context.Context, id string) (domain.Task, error) {
	task, err := scanTask(s.db.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id = ?", id))
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.populateTask(ctx, &task); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func (s *Store) ListTasks(ctx context.Context, filter TaskFilter) ([]domain.Task, error) {
	query := "SELECT " + taskColumns + " FROM tasks"
	var where []string
	var args []any
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	where = append(where, "COALESCE(workspace_id, '') = ?")
	args = append(args, workspaceID)
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, status)
		}
		where = append(where, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.Unplanned {
		where = append(where, "planned_at IS NULL")
	} else {
		if filter.PlannedFrom != nil {
			where = append(where, "planned_at >= ?")
			args = append(args, formatTime(*filter.PlannedFrom))
		}
		if filter.PlannedTo != nil {
			where = append(where, "planned_at <= ?")
			args = append(args, formatTime(*filter.PlannedTo))
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY
CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END,
created_at, id`
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list tasks: %w", err)
	}
	var tasks []domain.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close task rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list tasks: %w", err)
	}
	for i := range tasks {
		if err := s.populateTask(ctx, &tasks[i]); err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

func (s *Store) UpdateTask(ctx context.Context, task domain.Task) error {
	task.UpdatedAt = defaultTime(task.UpdatedAt, s.now())
	done, resultJSON, err := encodeTaskJSON(task)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update task: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE tasks SET title = ?, purpose = ?, why = ?, status = ?, priority = ?, definition_of_done = ?,
		workspace_id = ?, created_by = ?, parent_task_id = ?, current_run_id = ?, result = ?,
		updated_at = ?, planned_at = ?, started_at = ?, completed_at = ?,
		run_requested_at = CASE WHEN ? = 'ready' THEN run_requested_at ELSE NULL END
WHERE id = ?`,
		task.Title, task.Purpose, task.Why, task.Status, task.Priority, done,
		nullableText(task.WorkspaceID), task.CreatedBy, nullableText(task.ParentTaskID), nullableText(task.CurrentRunID), resultJSON,
		formatTime(task.UpdatedAt), nullableTime(task.PlannedAt), nullableTime(task.StartedAt), nullableTime(task.CompletedAt), task.Status, task.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update task: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update task result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task %q: %w", task.ID, ErrNotFound)
	}
	if task.DependsOnTaskIDs != nil {
		if _, err := s.replaceTaskDependenciesTx(ctx, tx, task.WorkspaceID, task.ID, task.DependsOnTaskIDs); err != nil {
			return err
		}
	} else {
		existing, err := listTaskDependencies(ctx, tx, task.WorkspaceID, task.ID)
		if err != nil {
			return err
		}
		if _, err := validateTaskDependenciesTx(ctx, tx, task.WorkspaceID, task.ID, existing); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit update task: %w", err)
	}
	return nil
}

// ListTaskDependencies returns prerequisite task IDs in the active workspace.
func (s *Store) ListTaskDependencies(ctx context.Context, taskID string) ([]string, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	return s.ListTaskDependenciesForWorkspace(ctx, workspaceID, taskID)
}

func (s *Store) ListTaskDependenciesForWorkspace(ctx context.Context, workspaceID, taskID string) ([]string, error) {
	return listTaskDependencies(ctx, s.db, workspaceID, taskID)
}

// SetTaskDependencies atomically replaces a task's prerequisite set in the
// active workspace. Empty clears the set; cross-workspace, self, duplicate,
// and cyclic relationships are rejected without changing existing state.
func (s *Store) SetTaskDependencies(ctx context.Context, taskID string, prerequisiteTaskIDs []string) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.SetTaskDependenciesForWorkspace(ctx, workspaceID, taskID, prerequisiteTaskIDs)
}

func (s *Store) SetTaskDependenciesForWorkspace(ctx context.Context, workspaceID, taskID string, prerequisiteTaskIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin set task dependencies: %w", err)
	}
	defer tx.Rollback()
	if _, err := s.replaceTaskDependenciesTx(ctx, tx, workspaceID, taskID, prerequisiteTaskIDs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit task dependencies: %w", err)
	}
	return nil
}

type taskDependencyQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func listTaskDependencies(ctx context.Context, queryer taskDependencyQueryer, workspaceID, taskID string) ([]string, error) {
	var exists bool
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM tasks WHERE id = ? AND COALESCE(workspace_id, '') = ?)`, taskID, workspaceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("store: inspect task dependencies: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("task %q: %w", taskID, ErrNotFound)
	}
	rows, err := queryer.QueryContext(ctx, `SELECT prerequisite_task_id FROM task_dependencies
WHERE task_id = ? ORDER BY prerequisite_task_id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: list task dependencies: %w", err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan task dependency: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list task dependencies: %w", err)
	}
	return result, nil
}

func (s *Store) replaceTaskDependenciesTx(ctx context.Context, tx *sql.Tx, workspaceID, taskID string, prerequisiteTaskIDs []string) ([]string, error) {
	validated, err := validateTaskDependenciesTx(ctx, tx, workspaceID, taskID, prerequisiteTaskIDs)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_dependencies WHERE task_id = ?`, taskID); err != nil {
		return nil, fmt.Errorf("store: clear task dependencies: %w", err)
	}
	createdAt := formatTime(s.now())
	for _, prerequisiteID := range validated {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_at)
VALUES (?, ?, ?)`, taskID, prerequisiteID, createdAt); err != nil {
			return nil, fmt.Errorf("store: create task dependency: %w", err)
		}
	}
	return validated, nil
}

func validateTaskDependenciesTx(ctx context.Context, tx *sql.Tx, workspaceID, taskID string, prerequisiteTaskIDs []string) ([]string, error) {
	if len(prerequisiteTaskIDs) > MaximumTaskDependencies {
		return nil, fmt.Errorf("store: task dependencies exceed limit of %d", MaximumTaskDependencies)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM tasks WHERE id = ? AND COALESCE(workspace_id, '') = ?)`, taskID, workspaceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("store: inspect dependent task: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("task %q: %w", taskID, ErrNotFound)
	}
	result := make([]string, 0, len(prerequisiteTaskIDs))
	seen := make(map[string]struct{}, len(prerequisiteTaskIDs))
	for _, rawID := range prerequisiteTaskIDs {
		prerequisiteID := strings.TrimSpace(rawID)
		if prerequisiteID == "" {
			return nil, fmt.Errorf("store: task prerequisite ID is required")
		}
		if prerequisiteID == taskID {
			return nil, fmt.Errorf("store: task cannot depend on itself")
		}
		if _, duplicate := seen[prerequisiteID]; duplicate {
			return nil, fmt.Errorf("store: duplicate task prerequisite %q", prerequisiteID)
		}
		seen[prerequisiteID] = struct{}{}
		var prerequisiteExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM tasks WHERE id = ? AND COALESCE(workspace_id, '') = ?)`, prerequisiteID, workspaceID).Scan(&prerequisiteExists); err != nil {
			return nil, fmt.Errorf("store: inspect prerequisite task: %w", err)
		}
		if !prerequisiteExists {
			return nil, fmt.Errorf("prerequisite task %q: %w", prerequisiteID, ErrNotFound)
		}
		var cycle bool
		if err := tx.QueryRowContext(ctx, `
WITH RECURSIVE prerequisites(id) AS (
    SELECT prerequisite_task_id FROM task_dependencies WHERE task_id = ?
    UNION
    SELECT dependency.prerequisite_task_id
    FROM task_dependencies dependency
    JOIN prerequisites current ON dependency.task_id = current.id
)
SELECT EXISTS(SELECT 1 FROM prerequisites WHERE id = ?)`, prerequisiteID, taskID).Scan(&cycle); err != nil {
			return nil, fmt.Errorf("store: inspect task dependency cycle: %w", err)
		}
		if cycle {
			return nil, fmt.Errorf("store: task dependency would create a cycle")
		}
		result = append(result, prerequisiteID)
	}
	sort.Strings(result)
	return result, nil
}

// UpdateTaskStatus records a state transition and manages its lifecycle
// timestamps. A zero at value uses the store clock.
func (s *Store) UpdateTaskStatus(ctx context.Context, id string, status domain.TaskStatus, currentRunID string, at time.Time) error {
	at = defaultTime(at, s.now())
	started := "started_at"
	completed := "completed_at"
	args := []any{status, nullableText(currentRunID), formatTime(at)}
	switch status {
	case domain.TaskRunning:
		started = "COALESCE(started_at, ?)"
		args = append(args, formatTime(at))
	case domain.TaskCompleted, domain.TaskFailed, domain.TaskCancelled:
		completed = "?"
		args = append(args, formatTime(at))
	}
	query := `UPDATE tasks SET status = ?, current_run_id = ?, updated_at = ?, started_at = ` + started + `, completed_at = ` + completed + `, run_requested_at = CASE WHEN ? = 'ready' THEN run_requested_at ELSE NULL END WHERE id = ?`
	args = append(args, status, id)
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: update task status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update task status result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	return nil
}

// ClaimNextReadyTask atomically moves the oldest highest-priority ready task
// to running. It returns ErrNotFound when the queue has no due task whose
// prerequisites are resolved, or the configured global concurrency limit has
// already been reached. A cancelled prerequisite is resolved because closing
// it explicitly waives that branch of work; failed or waiting prerequisites
// still block their dependents.
func (s *Store) ClaimNextReadyTask(ctx context.Context) (domain.Task, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	return s.ClaimNextReadyTaskForWorkspace(ctx, workspaceID)
}

func (s *Store) ClaimNextReadyTaskForWorkspace(ctx context.Context, workspaceID string) (domain.Task, error) {
	now := s.now()
	row := s.db.QueryRowContext(ctx, `
UPDATE tasks
SET status = 'running', started_at = COALESCE(started_at, ?), updated_at = ?, run_requested_at = NULL
WHERE id = (
	    SELECT candidate.id FROM tasks candidate
	    WHERE candidate.status = 'ready'
	      AND COALESCE(candidate.workspace_id, '') = ?
	      AND (candidate.planned_at IS NULL OR candidate.planned_at <= ?)
	      AND (SELECT COUNT(*) FROM tasks running WHERE running.status = 'running') <
	          (SELECT max_parallel_tasks FROM settings WHERE id = 1)
	      AND NOT EXISTS (
	          SELECT 1
	          FROM task_dependencies dependency
	          JOIN tasks prerequisite ON prerequisite.id = dependency.prerequisite_task_id
	          WHERE dependency.task_id = candidate.id
	            AND prerequisite.status NOT IN ('completed', 'cancelled')
	      )
	ORDER BY CASE WHEN candidate.run_requested_at IS NULL THEN 1 ELSE 0 END,
	         candidate.run_requested_at,
	         CASE candidate.priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END,
	         candidate.created_at, candidate.id
    LIMIT 1
)
AND status = 'ready'
RETURNING `+taskColumns, formatTime(now), formatTime(now), workspaceID, formatTime(now))
	task, err := scanTask(row)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.populateTask(ctx, &task); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

// RequestTaskRun makes an idea or ready task due immediately and promotes it
// ahead of ordinary queued work. Real prerequisites and the global worker cap
// remain authoritative when the task is claimed.
func (s *Store) RequestTaskRun(ctx context.Context, id string) (domain.Task, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	return s.RequestTaskRunForWorkspace(ctx, workspaceID, id)
}

func (s *Store) RequestTaskRunForWorkspace(ctx context.Context, workspaceID, id string) (domain.Task, error) {
	now := s.now()
	row := s.db.QueryRowContext(ctx, `
UPDATE tasks
SET status = 'ready', planned_at = NULL, run_requested_at = ?, updated_at = ?
WHERE id = ?
  AND COALESCE(workspace_id, '') = ?
  AND status IN ('idea', 'ready')
RETURNING `+taskColumns, formatTime(now), formatTime(now), id, workspaceID)
	task, err := scanTask(row)
	if err == nil {
		if err := s.populateTask(ctx, &task); err != nil {
			return domain.Task{}, err
		}
		return task, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.Task{}, err
	}
	var status domain.TaskStatus
	lookupErr := s.db.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = ? AND COALESCE(workspace_id, '') = ?`, id, workspaceID).Scan(&status)
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return domain.Task{}, fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	if lookupErr != nil {
		return domain.Task{}, fmt.Errorf("store: inspect task run request: %w", lookupErr)
	}
	return domain.Task{}, fmt.Errorf("task %q is %s: %w", id, status, ErrInvalidTransition)
}

func (s *Store) ReadyTaskCount(ctx context.Context) (int, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return 0, err
	}
	return s.ReadyTaskCountForWorkspace(ctx, workspaceID)
}

func (s *Store) ReadyTaskCountForWorkspace(ctx context.Context, workspaceID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE status = 'ready' AND COALESCE(workspace_id, '') = ?", workspaceID).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count ready tasks: %w", err)
	}
	return count, nil
}

// HasOpenTaskTitle supports orientation deduplication. Title matching ignores
// surrounding whitespace and ASCII case; terminal tasks are excluded.
func (s *Store) HasOpenTaskTitle(ctx context.Context, title string) (bool, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return false, err
	}
	var exists bool
	err = s.db.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM tasks
    WHERE lower(trim(title)) = lower(trim(?))
      AND COALESCE(workspace_id, '') = ?
      AND status NOT IN ('completed', 'failed', 'cancelled')
)`, title, workspaceID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: check duplicate task title: %w", err)
	}
	return exists, nil
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin delete task: %w", err)
	}
	defer tx.Rollback()
	// A pending approval is actionable only while its task exists. Historical
	// resolved approvals are retained and detached by the foreign key.
	if _, err := tx.ExecContext(ctx, "DELETE FROM approvals WHERE task_id = ? AND status = 'pending'", id); err != nil {
		return fmt.Errorf("store: delete pending task approvals: %w", err)
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete task: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete task result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit delete task: %w", err)
	}
	return nil
}

func encodeTaskJSON(task domain.Task) ([]byte, any, error) {
	done, err := json.Marshal(task.DefinitionOfDone)
	if err != nil {
		return nil, nil, fmt.Errorf("store: encode task definition: %w", err)
	}
	var result any
	if task.Result != nil {
		encoded, err := json.Marshal(task.Result)
		if err != nil {
			return nil, nil, fmt.Errorf("store: encode task result: %w", err)
		}
		result = encoded
	}
	return done, result, nil
}

func scanTask(row rowScanner) (domain.Task, error) {
	var task domain.Task
	var done []byte
	var workspaceID, parentID, currentRunID, result sql.NullString
	var created, updated string
	var planned, runRequested, started, completed sql.NullString
	if err := row.Scan(
		&task.ID, &task.Title, &task.Purpose, &task.Why, &task.Status, &task.Priority, &done,
		&workspaceID, &task.CreatedBy, &parentID, &currentRunID, &result,
		&created, &updated, &planned, &runRequested, &started, &completed,
	); err != nil {
		return domain.Task{}, fmt.Errorf("store: get task: %w", notFound("task", err))
	}
	task.WorkspaceID = workspaceID.String
	task.ParentTaskID = parentID.String
	task.CurrentRunID = currentRunID.String
	if len(done) > 0 {
		if err := json.Unmarshal(done, &task.DefinitionOfDone); err != nil {
			return domain.Task{}, fmt.Errorf("store: decode task definition: %w", err)
		}
	}
	if result.Valid && result.String != "" {
		var decoded domain.RunResult
		if err := json.Unmarshal([]byte(result.String), &decoded); err != nil {
			return domain.Task{}, fmt.Errorf("store: decode task result: %w", err)
		}
		task.Result = &decoded
	}
	var err error
	task.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Task{}, err
	}
	task.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Task{}, err
	}
	if task.PlannedAt, err = parseNullableTime(planned); err != nil {
		return domain.Task{}, err
	}
	if task.RunRequestedAt, err = parseNullableTime(runRequested); err != nil {
		return domain.Task{}, err
	}
	if task.StartedAt, err = parseNullableTime(started); err != nil {
		return domain.Task{}, err
	}
	if task.CompletedAt, err = parseNullableTime(completed); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func (s *Store) populateTask(ctx context.Context, task *domain.Task) error {
	if task.WorkspaceID != "" {
		workspace, err := s.GetWorkspace(ctx, task.WorkspaceID)
		if !errors.Is(err, ErrNotFound) {
			if err != nil {
				return err
			}
			task.Workspace = &workspace
		}
	}
	dependencies, err := s.ListTaskDependenciesForWorkspace(ctx, task.WorkspaceID, task.ID)
	if err != nil {
		return err
	}
	task.DependsOnTaskIDs = dependencies
	return nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
