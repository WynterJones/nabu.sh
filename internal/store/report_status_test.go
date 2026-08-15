package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestReportStatusLifecycleFilteringAndWorkspaceIsolation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "report-status-w1", Name: "One", Path: "/report-status-one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "report-status-w2", Name: "Two", Path: "/report-status-two"}); err != nil {
		t.Fatal(err)
	}

	clock := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	unread, err := s.CreateReport(ctx, domain.Report{
		ID: "status-unread", WorkspaceID: "report-status-w1", Title: "New report",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unread.Status != domain.ReportUnread {
		t.Fatalf("default report status = %q, want unread", unread.Status)
	}
	read, err := s.CreateReport(ctx, domain.Report{
		ID: "status-read", WorkspaceID: "report-status-w1", Title: "Read report", Status: domain.ReportRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReport(ctx, domain.Report{
		ID: "status-archived-other", WorkspaceID: "report-status-w2", Title: "Other report", Status: domain.ReportArchived,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReport(ctx, domain.Report{
		ID: "status-invalid", WorkspaceID: "report-status-w1", Title: "Invalid", Status: "deleted",
	}); err == nil {
		t.Fatal("invalid report status was accepted")
	}

	list, err := s.ListReports(ctx, ReportFilter{
		WorkspaceID: "report-status-w1", Statuses: []domain.ReportStatus{domain.ReportUnread},
	})
	if err != nil || len(list) != 1 || list[0].ID != unread.ID {
		t.Fatalf("unread reports = %#v, %v", list, err)
	}
	list, err = s.ListReports(ctx, ReportFilter{
		WorkspaceID: "report-status-w1", Statuses: []domain.ReportStatus{domain.ReportRead, domain.ReportArchived},
	})
	if err != nil || len(list) != 1 || list[0].ID != read.ID {
		t.Fatalf("read/archive reports = %#v, %v", list, err)
	}
	if _, err := s.ListReports(ctx, ReportFilter{WorkspaceID: "report-status-w1", Statuses: []domain.ReportStatus{"unknown"}}); err == nil {
		t.Fatal("invalid report status filter was accepted")
	}

	clock = clock.Add(time.Hour)
	if err := s.UpdateReportStatus(ctx, unread.ID, domain.ReportRead); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetReport(ctx, unread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.ReportRead || !updated.UpdatedAt.Equal(clock) {
		t.Fatalf("read transition = %#v", updated)
	}
	clock = clock.Add(time.Hour)
	if err := s.UpdateReportStatusForWorkspace(ctx, "report-status-w1", unread.ID, domain.ReportArchived); err != nil {
		t.Fatal(err)
	}
	updated, err = s.GetReport(ctx, unread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.ReportArchived {
		t.Fatalf("archive transition status = %q", updated.Status)
	}

	// Existing callers that omit the new field do not accidentally unarchive a
	// report during a content-only update.
	updated.Status = ""
	updated.Title = "Archived, edited title"
	clock = clock.Add(time.Hour)
	updated.UpdatedAt = time.Time{}
	if err := s.UpdateReport(ctx, updated); err != nil {
		t.Fatal(err)
	}
	updated, err = s.GetReport(ctx, unread.ID)
	if err != nil || updated.Status != domain.ReportArchived || updated.Title != "Archived, edited title" {
		t.Fatalf("content update changed lifecycle = %#v, %v", updated, err)
	}

	if err := s.UpdateReportStatusForWorkspace(ctx, "report-status-w1", unread.ID, "deleted"); err == nil {
		t.Fatal("invalid status transition was accepted")
	}
	if err := s.SetActiveWorkspace(ctx, "report-status-w2"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateReportStatus(ctx, unread.ID, domain.ReportRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace status update error = %v", err)
	}
	other, err := s.ListReports(ctx, ReportFilter{Statuses: []domain.ReportStatus{domain.ReportArchived}})
	if err != nil || len(other) != 1 || other[0].ID != "status-archived-other" {
		t.Fatalf("active workspace archived reports = %#v, %v", other, err)
	}
}

func TestMigration21DefaultsLegacyReportsToUnread(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE reports (
    id TEXT PRIMARY KEY,
    task_id TEXT,
    workspace_id TEXT,
    kind TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT
);
INSERT INTO reports(id, title, created_at, updated_at)
VALUES ('legacy-report', 'Legacy', '2026-08-12T12:00:00Z', '2026-08-12T12:00:00Z');
`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	for version := 1; version <= 20; version++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			version, "2026-08-12T12:00:00Z"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	// Isolate migration 21 even as later additive migrations are introduced.
	for version := 22; version <= len(migrations); version++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			version, "2026-08-12T12:00:00Z"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	version, err := s.SchemaVersion(ctx)
	if err != nil || version != len(migrations) {
		t.Fatalf("schema version = %d, %v", version, err)
	}
	var status domain.ReportStatus
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM reports WHERE id = 'legacy-report'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != domain.ReportUnread {
		t.Fatalf("legacy report status = %q, want unread", status)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO reports(id, title, created_at)
VALUES ('post-migration-default', 'Default', '2026-08-12T13:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM reports WHERE id = 'post-migration-default'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != domain.ReportUnread {
		t.Fatalf("post-migration default = %q, want unread", status)
	}
}
