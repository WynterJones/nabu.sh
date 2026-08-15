package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestTaskDependenciesValidateScopeCyclesAndTransactionalUpdates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "dependency-w1", Name: "One", Path: "/dependency-one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "dependency-w2", Name: "Two", Path: "/dependency-two"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(id, workspaceID string, dependencies ...string) domain.Task {
		t.Helper()
		task, createErr := s.CreateTask(ctx, domain.Task{
			ID: id, WorkspaceID: workspaceID, Title: id, Status: domain.TaskReady,
			Priority: domain.PriorityNormal, DependsOnTaskIDs: dependencies,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return task
	}
	a := create("dependency-a", w1.ID)
	b := create("dependency-b", w1.ID, a.ID)
	c := create("dependency-c", w1.ID, b.ID)
	foreign := create("dependency-foreign", w2.ID)

	got, err := s.GetTask(ctx, c.ID)
	if err != nil || !reflect.DeepEqual(got.DependsOnTaskIDs, []string{b.ID}) {
		t.Fatalf("task dependencies = %#v, %v", got.DependsOnTaskIDs, err)
	}
	listed, err := s.ListTasks(ctx, TaskFilter{WorkspaceID: w1.ID})
	if err != nil || len(listed) != 3 {
		t.Fatalf("list tasks = %#v, %v", listed, err)
	}
	for _, task := range listed {
		if task.ID == b.ID && !reflect.DeepEqual(task.DependsOnTaskIDs, []string{a.ID}) {
			t.Fatalf("listed dependencies = %#v", task.DependsOnTaskIDs)
		}
	}

	if err := s.SetTaskDependenciesForWorkspace(ctx, w1.ID, a.ID, []string{c.ID}); err == nil {
		t.Fatal("transitive dependency cycle was accepted")
	}
	if dependencies, err := s.ListTaskDependenciesForWorkspace(ctx, w1.ID, a.ID); err != nil || len(dependencies) != 0 {
		t.Fatalf("cycle rejection changed dependencies: %#v, %v", dependencies, err)
	}
	for name, dependencies := range map[string][]string{
		"self":      {c.ID},
		"duplicate": {b.ID, b.ID},
		"foreign":   {foreign.ID},
		"missing":   {"missing-task"},
	} {
		if err := s.SetTaskDependenciesForWorkspace(ctx, w1.ID, c.ID, dependencies); err == nil {
			t.Fatalf("%s dependency was accepted", name)
		}
		preserved, getErr := s.ListTaskDependenciesForWorkspace(ctx, w1.ID, c.ID)
		if getErr != nil || !reflect.DeepEqual(preserved, []string{b.ID}) {
			t.Fatalf("%s rejection was not atomic: %#v, %v", name, preserved, getErr)
		}
	}

	if _, err := s.CreateTask(ctx, domain.Task{
		ID: "invalid-dependent", WorkspaceID: w1.ID, Title: "Invalid", Status: domain.TaskReady,
		DependsOnTaskIDs: []string{foreign.ID},
	}); err == nil {
		t.Fatal("cross-workspace create dependency was accepted")
	}
	if _, err := s.GetTask(ctx, "invalid-dependent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid task create was not rolled back: %v", err)
	}

	// Nil preserves dependencies for existing callers; explicit empty clears.
	got, err = s.GetTask(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.Title = "metadata-only update"
	got.DependsOnTaskIDs = nil
	if err := s.UpdateTask(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetTask(ctx, c.ID)
	if err != nil || !reflect.DeepEqual(got.DependsOnTaskIDs, []string{b.ID}) {
		t.Fatalf("nil update did not preserve dependencies: %#v, %v", got, err)
	}
	got.DependsOnTaskIDs = []string{}
	if err := s.UpdateTask(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetTask(ctx, c.ID)
	if err != nil || len(got.DependsOnTaskIDs) != 0 {
		t.Fatalf("empty update did not clear dependencies: %#v, %v", got, err)
	}

	// A workspace move cannot strand a cross-scope dependency.
	got, err = s.GetTask(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.WorkspaceID = w2.ID
	got.DependsOnTaskIDs = nil
	if err := s.UpdateTask(ctx, got); err == nil {
		t.Fatal("workspace move with an existing dependency was accepted")
	}
	got, err = s.GetTask(ctx, b.ID)
	if err != nil || got.WorkspaceID != w1.ID || !reflect.DeepEqual(got.DependsOnTaskIDs, []string{a.ID}) {
		t.Fatalf("failed workspace move was not atomic: %#v, %v", got, err)
	}
}

func TestClaimNextReadyTaskRequiresCompletedPrerequisites(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "claim-dependency-w", Name: "Claims", Path: "/claim-dependencies"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxParallelTasks = 8
	if err := s.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	prerequisites := []domain.Task{
		{ID: "prerequisite-completed", Status: domain.TaskCompleted},
		{ID: "prerequisite-cancelled", Status: domain.TaskCancelled},
		{ID: "prerequisite-failed", Status: domain.TaskFailed},
		{ID: "prerequisite-waiting", Status: domain.TaskWaiting},
		{ID: "prerequisite-approval", Status: domain.TaskNeedsApproval},
	}
	for _, task := range prerequisites {
		task.WorkspaceID, task.Title, task.Priority = workspace.ID, task.ID, domain.PriorityNormal
		if _, err := s.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range prerequisites {
		if _, err := s.CreateTask(ctx, domain.Task{
			ID: "dependent-" + task.ID, WorkspaceID: workspace.ID, Title: "Dependent " + task.ID,
			Status: domain.TaskReady, Priority: domain.PriorityHigh, DependsOnTaskIDs: []string{task.ID},
		}); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := s.ClaimNextReadyTaskForWorkspace(ctx, workspace.ID)
	if err != nil || claimed.ID != "dependent-prerequisite-completed" {
		t.Fatalf("first dependency-aware claim = %#v, %v", claimed, err)
	}
	claimed, err = s.ClaimNextReadyTaskForWorkspace(ctx, workspace.ID)
	if err != nil || claimed.ID != "dependent-prerequisite-cancelled" {
		t.Fatalf("cancelled prerequisite did not release task = %#v, %v", claimed, err)
	}
	if _, err := s.ClaimNextReadyTaskForWorkspace(ctx, workspace.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blocked dependency was claimed: %v", err)
	}
	if err := s.UpdateTaskStatus(ctx, "prerequisite-waiting", domain.TaskCompleted, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimNextReadyTaskForWorkspace(ctx, workspace.ID)
	if err != nil || claimed.ID != "dependent-prerequisite-waiting" {
		t.Fatalf("completed prerequisite did not release task = %#v, %v", claimed, err)
	}
	ready, err := s.ReadyTaskCountForWorkspace(ctx, workspace.ID)
	if err != nil || ready != 2 {
		t.Fatalf("raw ready count = %d, %v; want two blocked tasks", ready, err)
	}
}

func TestRequestTaskRunClearsArtificialDelayAndPrioritizesClaim(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "run-now-w", Name: "Run now", Path: "/run-now"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, domain.Task{
		ID: "ordinary-ready", WorkspaceID: workspace.ID, Title: "Ordinary ready",
		Status: domain.TaskReady, Priority: domain.PriorityHigh,
	}); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(4 * 24 * time.Hour)
	if _, err := s.CreateTask(ctx, domain.Task{
		ID: "requested-low", WorkspaceID: workspace.ID, Title: "Requested low priority",
		Status: domain.TaskIdea, Priority: domain.PriorityLow, PlannedAt: &future,
	}); err != nil {
		t.Fatal(err)
	}
	requested, err := s.RequestTaskRunForWorkspace(ctx, workspace.ID, "requested-low")
	if err != nil {
		t.Fatal(err)
	}
	if requested.Status != domain.TaskReady || requested.PlannedAt != nil {
		t.Fatalf("requested task = %#v, want ready and due now", requested)
	}
	if requested.RunRequestedAt == nil {
		t.Fatal("requested task did not expose its durable queued state")
	}
	claimed, err := s.ClaimNextReadyTaskForWorkspace(ctx, workspace.ID)
	if err != nil || claimed.ID != requested.ID {
		t.Fatalf("claimed task = %#v, %v; want requested task first", claimed, err)
	}
	if _, err := s.RequestTaskRunForWorkspace(ctx, workspace.ID, claimed.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("running task request = %v, want invalid transition", err)
	}
}

func TestMaxParallelTasksBoundsAndGlobalAtomicClaims(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "parallel-w1", Name: "One", Path: "/parallel-one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "parallel-w2", Name: "Two", Path: "/parallel-two"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || settings.MaxParallelTasks != 1 {
		t.Fatalf("default settings = %#v, %v", settings, err)
	}
	for _, invalid := range []int{-1, 9} {
		candidate := settings
		candidate.MaxParallelTasks = invalid
		if err := s.UpdateSettings(ctx, candidate); err == nil {
			t.Fatalf("invalid parallel limit %d was accepted", invalid)
		}
	}
	legacy := settings
	legacy.MaxParallelTasks = 0
	if err := s.UpdateSettings(ctx, legacy); err != nil {
		t.Fatalf("legacy zero parallel limit: %v", err)
	}
	legacy, err = s.GetSettings(ctx)
	if err != nil || legacy.MaxParallelTasks != 1 {
		t.Fatalf("legacy zero normalized settings = %#v, %v", legacy, err)
	}
	settings.MaxParallelTasks = 3
	if err := s.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	for index := range 10 {
		workspaceID := w1.ID
		if index%2 == 1 {
			workspaceID = w2.ID
		}
		if _, err := s.CreateTask(ctx, domain.Task{
			ID: "parallel-task-" + string(rune('a'+index)), WorkspaceID: workspaceID,
			Title: "Parallel", Status: domain.TaskReady, Priority: domain.PriorityNormal,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var claimed atomic.Int32
	var unexpected atomic.Int32
	var wg sync.WaitGroup
	for index := range 20 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			workspaceID := w1.ID
			if index%2 == 1 {
				workspaceID = w2.ID
			}
			_, claimErr := s.ClaimNextReadyTaskForWorkspace(ctx, workspaceID)
			switch {
			case claimErr == nil:
				claimed.Add(1)
			case errors.Is(claimErr, ErrNotFound):
			default:
				unexpected.Add(1)
			}
		}(index)
	}
	wg.Wait()
	if claimed.Load() != 3 || unexpected.Load() != 0 {
		t.Fatalf("parallel claims = %d, unexpected errors = %d", claimed.Load(), unexpected.Load())
	}
	var running int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE status = 'running'`).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 3 {
		t.Fatalf("running tasks = %d, want global limit 3", running)
	}
}

func TestMigration25DefaultsLegacyParallelismAndAddsDependencies(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-parallelism.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE settings(id INTEGER PRIMARY KEY);
INSERT INTO settings(id) VALUES (1);
CREATE TABLE tasks(id TEXT PRIMARY KEY);
INSERT INTO tasks(id) VALUES ('legacy-prerequisite'), ('legacy-dependent');
`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	for version := 1; version < 25; version++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			version, "2026-08-13T12:00:00Z"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	for version := 26; version <= len(migrations); version++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			version, "2026-08-13T12:00:00Z"); err != nil {
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
	var maximum int
	if err := s.db.QueryRowContext(ctx, `SELECT max_parallel_tasks FROM settings WHERE id = 1`).Scan(&maximum); err != nil {
		t.Fatal(err)
	}
	if maximum != 1 {
		t.Fatalf("legacy max parallel tasks = %d, want 1", maximum)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_at)
VALUES ('legacy-dependent', 'legacy-prerequisite', '2026-08-13T12:00:00Z')`); err != nil {
		t.Fatalf("dependency table unusable after migration: %v", err)
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil || version != len(migrations) {
		t.Fatalf("schema version = %d, %v", version, err)
	}
}
