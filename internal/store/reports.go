package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

const reportColumns = `id, workspace_id, kind, status, title, summary, body, path, created_at, updated_at`

type ReportFilter struct {
	WorkspaceID string
	TaskID      string
	Kind        string
	Statuses    []domain.ReportStatus
	Limit       int
}

func (s *Store) CreateReport(ctx context.Context, report domain.Report) (domain.Report, error) {
	var err error
	if report.WorkspaceID == "" && len(report.RelatedTaskIDs) > 0 {
		var workspaceID sql.NullString
		if err := s.db.QueryRowContext(ctx, "SELECT workspace_id FROM tasks WHERE id = ?", report.RelatedTaskIDs[0]).Scan(&workspaceID); err != nil {
			return domain.Report{}, fmt.Errorf("store: get report task scope: %w", notFound("task", err))
		}
		report.WorkspaceID = workspaceID.String
	}
	report.WorkspaceID, err = s.defaultWorkspaceID(ctx, report.WorkspaceID)
	if err != nil {
		return domain.Report{}, err
	}
	if report.ID == "" {
		id, err := newID()
		if err != nil {
			return domain.Report{}, err
		}
		report.ID = id
	}
	now := s.now()
	report.CreatedAt = defaultTime(report.CreatedAt, now)
	report.UpdatedAt = defaultTime(report.UpdatedAt, report.CreatedAt)
	if report.Status == "" {
		report.Status = domain.ReportUnread
	}
	if !validReportStatus(report.Status) {
		return domain.Report{}, fmt.Errorf("store: invalid report status %q", report.Status)
	}
	report.RelatedTaskIDs = uniqueStrings(report.RelatedTaskIDs)
	report.ArtifactIDs = reportArtifactIDs(report)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Report{}, fmt.Errorf("store: begin create report: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO reports(id, task_id, title, summary, path, created_at, kind, body, updated_at, workspace_id, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, report.ID, firstNullable(report.RelatedTaskIDs),
		report.Title, report.Summary, report.Path, formatTime(report.CreatedAt), report.Kind,
		report.Body, formatTime(report.UpdatedAt), nullableText(report.WorkspaceID), report.Status); err != nil {
		return domain.Report{}, fmt.Errorf("store: create report: %w", err)
	}
	if err := replaceReportRelations(ctx, tx, report); err != nil {
		return domain.Report{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Report{}, fmt.Errorf("store: commit report: %w", err)
	}
	return report, nil
}

func (s *Store) GetReport(ctx context.Context, id string) (domain.Report, error) {
	report, err := scanReport(s.db.QueryRowContext(ctx, "SELECT "+reportColumns+" FROM reports WHERE id = ?", id))
	if err != nil {
		return domain.Report{}, err
	}
	if err := s.populateReportRelations(ctx, &report); err != nil {
		return domain.Report{}, err
	}
	return report, nil
}

func (s *Store) ListReports(ctx context.Context, filter ReportFilter) ([]domain.Report, error) {
	query := "SELECT DISTINCT " + prefixedColumns("r", reportColumns) + " FROM reports r"
	var args []any
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if filter.TaskID != "" {
		query += " JOIN report_tasks rt ON rt.report_id = r.id WHERE rt.task_id = ?"
		args = append(args, filter.TaskID)
	} else {
		query += " WHERE 1 = 1"
	}
	query += " AND COALESCE(r.workspace_id, '') = ?"
	args = append(args, workspaceID)
	if filter.Kind != "" {
		query += " AND r.kind = ?"
		args = append(args, filter.Kind)
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for index, status := range filter.Statuses {
			if !validReportStatus(status) {
				return nil, fmt.Errorf("store: invalid report status %q", status)
			}
			placeholders[index] = "?"
			args = append(args, status)
		}
		query += " AND r.status IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY r.created_at DESC, r.id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list reports: %w", err)
	}
	var reports []domain.Report
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		reports = append(reports, report)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close reports: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list reports: %w", err)
	}
	for i := range reports {
		if err := s.populateReportRelations(ctx, &reports[i]); err != nil {
			return nil, err
		}
	}
	return reports, nil
}

func (s *Store) UpdateReport(ctx context.Context, report domain.Report) error {
	if report.Status != "" && !validReportStatus(report.Status) {
		return fmt.Errorf("store: invalid report status %q", report.Status)
	}
	report.UpdatedAt = defaultTime(report.UpdatedAt, s.now())
	report.RelatedTaskIDs = uniqueStrings(report.RelatedTaskIDs)
	report.ArtifactIDs = reportArtifactIDs(report)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update report: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE reports SET task_id = ?, workspace_id = ?, kind = ?, status = COALESCE(NULLIF(?, ''), status),
    title = ?, summary = ?, body = ?, path = ?, updated_at = ?
WHERE id = ?`, firstNullable(report.RelatedTaskIDs), nullableText(report.WorkspaceID), report.Kind, report.Status,
		report.Title, report.Summary, report.Body, report.Path, formatTime(report.UpdatedAt), report.ID)
	if err != nil {
		return fmt.Errorf("store: update report: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update report result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("report %q: %w", report.ID, ErrNotFound)
	}
	if err := replaceReportRelations(ctx, tx, report); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit report update: %w", err)
	}
	return nil
}

// UpdateReportStatus changes lifecycle state only for a report in the active
// workspace.
func (s *Store) UpdateReportStatus(ctx context.Context, id string, status domain.ReportStatus) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.UpdateReportStatusForWorkspace(ctx, workspaceID, id, status)
}

func (s *Store) UpdateReportStatusForWorkspace(ctx context.Context, workspaceID, id string, status domain.ReportStatus) error {
	if !validReportStatus(status) {
		return fmt.Errorf("store: invalid report status %q", status)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE reports SET status = ?, updated_at = ? WHERE id = ? AND COALESCE(workspace_id, '') = ?`,
		status, formatTime(s.now()), id, workspaceID)
	if err != nil {
		return fmt.Errorf("store: update report status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update report status result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("report %q: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Store) DeleteReport(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM reports WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete report: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete report result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("report %q: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Store) LinkReportTask(ctx context.Context, reportID, taskID string) error {
	if _, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO report_tasks(report_id, task_id) VALUES (?, ?)", reportID, taskID); err != nil {
		return fmt.Errorf("store: link report task: %w", err)
	}
	return nil
}

func (s *Store) LinkReportArtifact(ctx context.Context, reportID, artifactID string) error {
	if _, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO report_artifacts(report_id, artifact_id) VALUES (?, ?)", reportID, artifactID); err != nil {
		return fmt.Errorf("store: link report artifact: %w", err)
	}
	return nil
}

func replaceReportRelations(ctx context.Context, tx *sql.Tx, report domain.Report) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM report_tasks WHERE report_id = ?", report.ID); err != nil {
		return fmt.Errorf("store: clear report tasks: %w", err)
	}
	for _, taskID := range report.RelatedTaskIDs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO report_tasks(report_id, task_id) VALUES (?, ?)", report.ID, taskID); err != nil {
			return fmt.Errorf("store: link report task %q: %w", taskID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM report_artifacts WHERE report_id = ?", report.ID); err != nil {
		return fmt.Errorf("store: clear report artifacts: %w", err)
	}
	for _, artifactID := range report.ArtifactIDs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO report_artifacts(report_id, artifact_id) VALUES (?, ?)", report.ID, artifactID); err != nil {
			return fmt.Errorf("store: link report artifact %q: %w", artifactID, err)
		}
	}
	return nil
}

func (s *Store) populateReportRelations(ctx context.Context, report *domain.Report) error {
	rows, err := s.db.QueryContext(ctx, "SELECT task_id FROM report_tasks WHERE report_id = ? ORDER BY task_id", report.ID)
	if err != nil {
		return fmt.Errorf("store: get report tasks: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: scan report task: %w", err)
		}
		report.RelatedTaskIDs = append(report.RelatedTaskIDs, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("store: close report tasks: %w", err)
	}

	rows, err = s.db.QueryContext(ctx, `
SELECT `+prefixedColumns("a", artifactColumns)+`
FROM artifacts a JOIN report_artifacts ra ON ra.artifact_id = a.id
WHERE ra.report_id = ? ORDER BY a.created_at, a.id`, report.ID)
	if err != nil {
		return fmt.Errorf("store: get report artifacts: %w", err)
	}
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		report.ArtifactIDs = append(report.ArtifactIDs, artifact.ID)
		report.Artifacts = append(report.Artifacts, artifact)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("store: close report artifacts: %w", err)
	}
	return nil
}

func scanReport(row rowScanner) (domain.Report, error) {
	var report domain.Report
	var created string
	var updated sql.NullString
	var workspaceID sql.NullString
	if err := row.Scan(&report.ID, &workspaceID, &report.Kind, &report.Status, &report.Title, &report.Summary,
		&report.Body, &report.Path, &created, &updated); err != nil {
		return domain.Report{}, fmt.Errorf("store: get report: %w", notFound("report", err))
	}
	report.WorkspaceID = workspaceID.String
	var err error
	report.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Report{}, err
	}
	if updated.Valid {
		report.UpdatedAt, err = parseTime(updated.String)
	} else {
		report.UpdatedAt = report.CreatedAt
	}
	if err != nil {
		return domain.Report{}, err
	}
	return report, nil
}

func validReportStatus(status domain.ReportStatus) bool {
	return status == domain.ReportUnread || status == domain.ReportRead || status == domain.ReportArchived
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func reportArtifactIDs(report domain.Report) []string {
	values := append([]string(nil), report.ArtifactIDs...)
	for _, artifact := range report.Artifacts {
		values = append(values, artifact.ID)
	}
	return uniqueStrings(values)
}

func firstNullable(values []string) any {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func prefixedColumns(prefix, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}
