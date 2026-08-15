package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

const planColumns = `id, workspace_id, title, objective, status, created_at, updated_at`
const planItemColumns = `id, plan_id, kind, title, purpose, why, planned_at, cadence,
position, task_id, schedule_id, status`

type PlanFilter struct {
	WorkspaceID string
	Statuses    []domain.PlanStatus
	Limit       int
}

func (s *Store) CreatePlan(ctx context.Context, plan domain.Plan) (domain.Plan, error) {
	var err error
	plan.WorkspaceID, err = s.defaultWorkspaceID(ctx, plan.WorkspaceID)
	if err != nil {
		return domain.Plan{}, err
	}
	if plan.WorkspaceID == "" {
		return domain.Plan{}, fmt.Errorf("store: plan requires a workspace")
	}
	if plan.ID == "" {
		plan.ID, err = newID()
		if err != nil {
			return domain.Plan{}, err
		}
	}
	if plan.Status == "" {
		plan.Status = domain.PlanProposed
	}
	if err := validatePlan(plan); err != nil {
		return domain.Plan{}, err
	}
	now := s.now()
	plan.CreatedAt = defaultTime(plan.CreatedAt, now)
	plan.UpdatedAt = defaultTime(plan.UpdatedAt, plan.CreatedAt)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("store: begin create plan: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO plans (`+planColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.WorkspaceID, plan.Title, plan.Objective, plan.Status,
		formatTime(plan.CreatedAt), formatTime(plan.UpdatedAt)); err != nil {
		return domain.Plan{}, fmt.Errorf("store: create plan: %w", err)
	}
	plan.Items, err = insertPlanItems(ctx, tx, plan.WorkspaceID, plan.ID, plan.Items)
	if err != nil {
		return domain.Plan{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Plan{}, fmt.Errorf("store: commit plan: %w", err)
	}
	return plan, nil
}

// GetPlan returns a plan only from the active workspace.
func (s *Store) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Plan{}, err
	}
	return s.GetPlanForWorkspace(ctx, workspaceID, id)
}

func (s *Store) GetPlanForWorkspace(ctx context.Context, workspaceID, id string) (domain.Plan, error) {
	plan, err := scanPlan(s.db.QueryRowContext(ctx, `SELECT `+planColumns+`
FROM plans WHERE id = ? AND workspace_id = ?`, id, workspaceID))
	if err != nil {
		return domain.Plan{}, err
	}
	plan.Items, err = s.listPlanItems(ctx, plan.ID)
	if err != nil {
		return domain.Plan{}, err
	}
	return plan, nil
}

func (s *Store) ListPlans(ctx context.Context, filter PlanFilter) ([]domain.Plan, error) {
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + planColumns + ` FROM plans WHERE workspace_id = ?`
	args := []any{workspaceID}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for index, status := range filter.Statuses {
			placeholders[index] = "?"
			args = append(args, status)
		}
		query += " AND status IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY updated_at DESC, id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list plans: %w", err)
	}
	var plans []domain.Plan
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close plans: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list plans: %w", err)
	}
	for index := range plans {
		plans[index].Items, err = s.listPlanItems(ctx, plans[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return plans, nil
}

// UpdatePlan replaces a plan and its ordered proposal items atomically in the
// active workspace.
func (s *Store) UpdatePlan(ctx context.Context, plan domain.Plan) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.UpdatePlanForWorkspace(ctx, workspaceID, plan)
}

func (s *Store) UpdatePlanForWorkspace(ctx context.Context, workspaceID string, plan domain.Plan) error {
	plan.WorkspaceID = workspaceID
	if err := validatePlan(plan); err != nil {
		return err
	}
	plan.UpdatedAt = defaultTime(plan.UpdatedAt, s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update plan: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE plans SET title = ?, objective = ?, status = ?, updated_at = ?
WHERE id = ? AND workspace_id = ?`, plan.Title, plan.Objective, plan.Status,
		formatTime(plan.UpdatedAt), plan.ID, workspaceID)
	if err != nil {
		return fmt.Errorf("store: update plan: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update plan result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("plan %q: %w", plan.ID, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM plan_items WHERE plan_id = ?", plan.ID); err != nil {
		return fmt.Errorf("store: clear plan items: %w", err)
	}
	if _, err := insertPlanItems(ctx, tx, workspaceID, plan.ID, plan.Items); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit plan update: %w", err)
	}
	return nil
}

// DeletePlan deletes a plan only from the active workspace.
func (s *Store) DeletePlan(ctx context.Context, id string) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.DeletePlanForWorkspace(ctx, workspaceID, id)
}

func (s *Store) DeletePlanForWorkspace(ctx context.Context, workspaceID, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM plans WHERE id = ? AND workspace_id = ?", id, workspaceID)
	if err != nil {
		return fmt.Errorf("store: delete plan: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete plan result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("plan %q: %w", id, ErrNotFound)
	}
	return nil
}

func insertPlanItems(ctx context.Context, tx *sql.Tx, workspaceID, planID string, items []domain.PlanItem) ([]domain.PlanItem, error) {
	items = append([]domain.PlanItem(nil), items...)
	if len(items) > 1 {
		allZero := true
		for _, item := range items {
			if item.Position != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			for index := range items {
				items[index].Position = index
			}
		}
	}
	seenPositions := make(map[int]struct{}, len(items))
	for index := range items {
		item := &items[index]
		item.PlanID = planID
		if item.ID == "" {
			id, err := newID()
			if err != nil {
				return nil, err
			}
			item.ID = id
		}
		if item.Status == "" {
			item.Status = domain.PlanItemProposed
		}
		if err := validatePlanItem(*item); err != nil {
			return nil, err
		}
		if _, duplicate := seenPositions[item.Position]; duplicate {
			return nil, fmt.Errorf("store: duplicate plan item position %d", item.Position)
		}
		seenPositions[item.Position] = struct{}{}
		if err := validatePlanItemLinks(ctx, tx, workspaceID, *item); err != nil {
			return nil, err
		}
		var cadence any
		if item.Cadence != nil {
			encoded, err := json.Marshal(item.Cadence)
			if err != nil {
				return nil, fmt.Errorf("store: encode plan item cadence: %w", err)
			}
			cadence = encoded
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO plan_items (`+planItemColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, planID, item.Kind, item.Title,
			item.Purpose, item.Why, nullableTime(item.PlannedAt), cadence, item.Position,
			nullableText(item.TaskID), nullableText(item.ScheduleID), item.Status); err != nil {
			return nil, fmt.Errorf("store: create plan item: %w", err)
		}
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Position < items[right].Position })
	if items == nil {
		items = []domain.PlanItem{}
	}
	return items, nil
}

func validatePlanItemLinks(ctx context.Context, tx *sql.Tx, workspaceID string, item domain.PlanItem) error {
	for _, link := range []struct {
		id, table, label string
	}{
		{item.TaskID, "tasks", "task"},
		{item.ScheduleID, "schedules", "schedule"},
	} {
		if link.id == "" {
			continue
		}
		var exists bool
		query := "SELECT EXISTS(SELECT 1 FROM " + link.table + " WHERE id = ? AND COALESCE(workspace_id, '') = ?)"
		if err := tx.QueryRowContext(ctx, query, link.id, workspaceID).Scan(&exists); err != nil {
			return fmt.Errorf("store: validate plan %s: %w", link.label, err)
		}
		if !exists {
			return fmt.Errorf("store: plan item %s %q is not in workspace %q", link.label, link.id, workspaceID)
		}
	}
	return nil
}

func (s *Store) listPlanItems(ctx context.Context, planID string) ([]domain.PlanItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+planItemColumns+`
FROM plan_items WHERE plan_id = ? ORDER BY position, id`, planID)
	if err != nil {
		return nil, fmt.Errorf("store: list plan items: %w", err)
	}
	defer rows.Close()
	var items []domain.PlanItem
	for rows.Next() {
		item, err := scanPlanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list plan items: %w", err)
	}
	if items == nil {
		items = []domain.PlanItem{}
	}
	return items, nil
}

func validatePlan(plan domain.Plan) error {
	if strings.TrimSpace(plan.Title) == "" {
		return fmt.Errorf("store: plan title is required")
	}
	switch plan.Status {
	case domain.PlanProposed, domain.PlanActive, domain.PlanCompleted, domain.PlanArchived:
		return nil
	default:
		return fmt.Errorf("store: invalid plan status %q", plan.Status)
	}
}

func validatePlanItem(item domain.PlanItem) error {
	if strings.TrimSpace(item.Title) == "" {
		return fmt.Errorf("store: plan item title is required")
	}
	if item.Position < 0 {
		return fmt.Errorf("store: plan item position must not be negative")
	}
	switch item.Kind {
	case domain.PlanItemTask, domain.PlanItemSchedule, domain.PlanItemMilestone:
	default:
		return fmt.Errorf("store: invalid plan item kind %q", item.Kind)
	}
	switch item.Status {
	case domain.PlanItemProposed, domain.PlanItemAccepted, domain.PlanItemSkipped:
	default:
		return fmt.Errorf("store: invalid plan item status %q", item.Status)
	}
	return nil
}

func scanPlan(row rowScanner) (domain.Plan, error) {
	var plan domain.Plan
	var created, updated string
	if err := row.Scan(&plan.ID, &plan.WorkspaceID, &plan.Title, &plan.Objective, &plan.Status,
		&created, &updated); err != nil {
		return domain.Plan{}, fmt.Errorf("store: get plan: %w", notFound("plan", err))
	}
	var err error
	plan.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Plan{}, err
	}
	plan.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Plan{}, err
	}
	return plan, nil
}

func scanPlanItem(row rowScanner) (domain.PlanItem, error) {
	var item domain.PlanItem
	var planned sql.NullString
	var cadence []byte
	var taskID, scheduleID sql.NullString
	if err := row.Scan(&item.ID, &item.PlanID, &item.Kind, &item.Title, &item.Purpose, &item.Why,
		&planned, &cadence, &item.Position, &taskID, &scheduleID, &item.Status); err != nil {
		return domain.PlanItem{}, fmt.Errorf("store: get plan item: %w", err)
	}
	item.TaskID = taskID.String
	item.ScheduleID = scheduleID.String
	var err error
	item.PlannedAt, err = parseNullableTime(planned)
	if err != nil {
		return domain.PlanItem{}, err
	}
	if len(cadence) > 0 {
		var decoded domain.ScheduleCadence
		if err := json.Unmarshal(cadence, &decoded); err != nil {
			return domain.PlanItem{}, fmt.Errorf("store: decode plan item cadence: %w", err)
		}
		item.Cadence = &decoded
	}
	return item, nil
}
