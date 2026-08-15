package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state", "nabu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestOpenMigratesConfiguresSQLiteAndSeedsSingletons(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	version, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("schema version = %d, want %d", version, len(migrations))
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("idempotent Migrate: %v", err)
	}

	var journal string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}
	var foreignKeys bool
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if !foreignKeys {
		t.Fatal("foreign keys are disabled")
	}

	settings, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.DisplayName != "Nabu" || settings.ServerAddress != "127.0.0.1:7777" {
		t.Fatalf("unexpected settings defaults: %+v", settings)
	}
	if settings.MaxParallelTasks != 1 {
		t.Fatalf("max parallel tasks = %d, want 1", settings.MaxParallelTasks)
	}
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantPolicy := domain.Policy{Read: "allow", Work: "allow", Publish: "ask", Dangerous: "ask"}
	if policy != wantPolicy {
		t.Fatalf("policy = %+v, want %+v", policy, wantPolicy)
	}

	var tables int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN
('settings','policy','missions','workspaces','tasks','runs','events','artifacts','schedules','reports','messages','approvals')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 12 {
		t.Fatalf("created %d expected tables, want 12", tables)
	}
}

func TestSettingsPolicyAndOrientationState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	last := time.Date(2026, 8, 11, 12, 30, 0, 123, time.UTC)
	want := domain.Settings{
		DisplayName:       "Operator",
		SetupComplete:     true,
		Paused:            true,
		MissionStarted:    true,
		CodexPath:         "/usr/local/bin/codex",
		GitPath:           "/usr/bin/git",
		ServerAddress:     "127.0.0.1:9999",
		OrientationQueued: false,
		LastOrientationAt: &last,
		MaxParallelTasks:  1,
	}
	if err := s.UpdateSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}

	if queued, err := s.RequestOrientation(ctx); err != nil || !queued {
		t.Fatalf("RequestOrientation = %v, %v", queued, err)
	}
	if queued, err := s.RequestOrientation(ctx); err != nil || queued {
		t.Fatalf("duplicate RequestOrientation = %v, %v", queued, err)
	}
	if taken, err := s.ConsumeOrientationRequest(ctx); err != nil || !taken {
		t.Fatalf("ConsumeOrientationRequest = %v, %v", taken, err)
	}
	if taken, err := s.ConsumeOrientationRequest(ctx); err != nil || taken {
		t.Fatalf("duplicate ConsumeOrientationRequest = %v, %v", taken, err)
	}
	completed := last.Add(time.Hour)
	if err := s.CompleteOrientation(ctx, completed); err != nil {
		t.Fatal(err)
	}
	queued, at, err := s.OrientationState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queued || at == nil || !at.Equal(completed) {
		t.Fatalf("OrientationState = %v, %v", queued, at)
	}

	updatedPolicy := domain.Policy{Read: "ask", Work: "ask", Publish: "allow", Dangerous: "deny"}
	if err := s.UpdatePolicy(ctx, updatedPolicy); err != nil {
		t.Fatal(err)
	}
	gotPolicy, err := s.GetPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotPolicy != updatedPolicy {
		t.Fatalf("policy = %+v, want %+v", gotPolicy, updatedPolicy)
	}
}

func TestMissionAndWorkspaceCRUD(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	first, err := s.CreateMission(ctx, domain.Mission{ID: "m1", Statement: "First", Active: true, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if first.UpdatedAt.IsZero() {
		t.Fatal("CreateMission did not set UpdatedAt")
	}
	second, err := s.CreateMission(ctx, domain.Mission{ID: "m2", Statement: "Second", Context: "context", Active: true, CreatedAt: t0.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveMission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != second.ID {
		t.Fatalf("active mission = %q, want %q", active.ID, second.ID)
	}
	old, err := s.GetMission(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Active {
		t.Fatal("old active mission was not deactivated")
	}
	old.Statement = "Updated"
	old.Active = true
	old.UpdatedAt = t0.Add(2 * time.Minute)
	if err := s.UpdateMission(ctx, old); err != nil {
		t.Fatal(err)
	}
	active, err = s.ActiveMission(ctx)
	if err != nil || active.ID != first.ID {
		t.Fatalf("active after update = %+v, %v", active, err)
	}

	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{
		ID: "w1", Name: "Nabu", Path: "/tmp/nabu", DefaultBranch: "main", Allowed: true, CreatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace.Name = "Nabu.sh"
	workspace.Allowed = false
	if err := s.UpdateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	gotWorkspace, err := s.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotWorkspace.Name != "Nabu.sh" || gotWorkspace.Allowed {
		t.Fatalf("workspace = %+v", gotWorkspace)
	}
	if _, err := s.CreateTask(ctx, domain.Task{Title: "bad FK", WorkspaceID: "missing"}); err == nil {
		t.Fatal("task with missing workspace did not fail foreign key validation")
	}
	if err := s.DeleteWorkspace(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetWorkspace(ctx, workspace.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetWorkspace after delete = %v", err)
	}
}

func TestDeleteWorkspaceDataCascadesOwnedOperationalState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "delete-w1", Name: "Delete me", Path: "/tmp/delete-w1", Allowed: true, MissionStarted: true})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "keep-w2", Name: "Keep me", Path: "/tmp/keep-w2", Allowed: true, MissionStarted: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveWorkspace(ctx, w1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMission(ctx, domain.Mission{ID: "delete-mission", WorkspaceID: w1.ID, Statement: "Delete", Active: true}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, domain.Task{ID: "delete-task", WorkspaceID: w1.ID, Title: "Delete", Status: domain.TaskCompleted})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, domain.Run{ID: "delete-run", TaskID: task.ID, Type: domain.RunExecute, Status: domain.RunCompleted, WorkingDirectory: w1.Path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArtifact(ctx, domain.Artifact{ID: "delete-artifact", TaskID: task.ID, RunID: run.ID, Kind: "report", Name: "Delete", Path: "/nabu/artifacts/delete.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: w1.ID, Role: domain.MessageUser, Content: "Delete", Status: domain.MessageComplete}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDataset(ctx, domain.Dataset{ID: "delete-dataset", WorkspaceID: w1.ID, Name: "Delete", Slug: "delete", Schema: []domain.DatasetColumn{{Name: "name", Type: domain.DatasetString}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSecretRecord(ctx, domain.SecretRecord{ID: "delete-secret", WorkspaceID: w1.ID, Name: "API key"}); err != nil {
		t.Fatal(err)
	}

	deletion, err := s.DeleteWorkspaceData(ctx, w1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deletion.ActiveWorkspaceID != w2.ID || !reflect.DeepEqual(deletion.RunIDs, []string{run.ID}) || !reflect.DeepEqual(deletion.ArtifactPaths, []string{"/nabu/artifacts/delete.md"}) {
		t.Fatalf("deletion = %#v", deletion)
	}
	for table := range map[string]struct{}{"workspaces": {}, "missions": {}, "tasks": {}, "runs": {}, "artifacts": {}, "messages": {}, "datasets": {}, "secret_records": {}} {
		var count int
		query := "SELECT COUNT(*) FROM " + table + " WHERE id = ?"
		identifier := map[string]any{
			"workspaces": w1.ID, "missions": "delete-mission", "tasks": task.ID, "runs": run.ID,
			"artifacts": "delete-artifact", "datasets": "delete-dataset", "secret_records": "delete-secret",
		}[table]
		if table == "messages" {
			query, identifier = "SELECT COUNT(*) FROM messages WHERE workspace_id = ?", w1.ID
		}
		if err := s.db.QueryRowContext(ctx, query, identifier).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s remaining count = %d, err = %v", table, count, err)
		}
	}
	active, err := s.ActiveWorkspace(ctx)
	if err != nil || active.ID != w2.ID {
		t.Fatalf("active workspace = %#v, %v", active, err)
	}
}

func TestDeleteFinalWorkspaceReturnsSetupToIncomplete(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "only-workspace", Name: "Only", Path: "/tmp/only"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.SetupComplete = true
	if err := s.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteWorkspaceData(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	settings, err = s.GetSettings(ctx)
	if err != nil || settings.SetupComplete || settings.ActiveWorkspaceID != "" {
		t.Fatalf("settings after final workspace delete = %#v, %v", settings, err)
	}
}

func TestTaskQueueRoundTripPriorityAndSingleConcurrency(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "Repo", Path: "/tmp/repo", Allowed: true, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}

	definitions := []domain.DefinitionItem{{Text: "tests pass", Completed: true}}
	result := &domain.RunResult{Status: "completed", Summary: "done", FilesChanged: []string{"a.go"}}
	tasks := []domain.Task{
		{ID: "low", Title: "Low", Status: domain.TaskReady, Priority: domain.PriorityLow, CreatedAt: t0, DefinitionOfDone: definitions},
		{ID: "normal", Title: "Normal", Status: domain.TaskReady, Priority: domain.PriorityNormal, CreatedAt: t0.Add(time.Minute)},
		{ID: "high-new", Title: "High new", Status: domain.TaskReady, Priority: domain.PriorityHigh, CreatedAt: t0.Add(3 * time.Minute), WorkspaceID: workspace.ID, Result: result},
		{ID: "high-old", Title: "High old", Status: domain.TaskReady, Priority: domain.PriorityHigh, CreatedAt: t0.Add(2 * time.Minute)},
	}
	for _, task := range tasks {
		if _, err := s.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	count, err := s.ReadyTaskCount(ctx)
	if err != nil || count != 4 {
		t.Fatalf("ReadyTaskCount = %d, %v", count, err)
	}
	claimed, err := s.ClaimNextReadyTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "high-old" || claimed.Status != domain.TaskRunning || claimed.StartedAt == nil {
		t.Fatalf("claimed = %+v", claimed)
	}
	if _, err := s.ClaimNextReadyTask(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim while running = %v", err)
	}
	if err := s.UpdateTaskStatus(ctx, claimed.ID, domain.TaskCompleted, "", t0.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimNextReadyTask(ctx)
	if err != nil || claimed.ID != "high-new" {
		t.Fatalf("next claim = %q, %v", claimed.ID, err)
	}
	if claimed.Workspace == nil || claimed.Workspace.ID != workspace.ID || !reflect.DeepEqual(claimed.Result, result) {
		t.Fatalf("expanded claimed task = %+v", claimed)
	}

	got, err := s.GetTask(ctx, "low")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.DefinitionOfDone, definitions) {
		t.Fatalf("definition = %#v, want %#v", got.DefinitionOfDone, definitions)
	}
	duplicate, err := s.HasOpenTaskTitle(ctx, "  LOW ")
	if err != nil || !duplicate {
		t.Fatalf("HasOpenTaskTitle = %v, %v", duplicate, err)
	}

	ready, err := s.ListTasks(ctx, TaskFilter{Statuses: []domain.TaskStatus{domain.TaskReady}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 2 || ready[0].ID != "normal" || ready[1].ID != "low" {
		t.Fatalf("ready order = %+v", ready)
	}
}

func TestClaimNextReadyTaskSkipsFuturePlannedWork(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "planned-workspace", Name: "Planned", Path: "/tmp/planned", Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	future := now.Add(24 * time.Hour)
	due := now.Add(-time.Minute)
	if _, err := s.CreateTask(ctx, domain.Task{ID: "future", WorkspaceID: workspace.ID, Title: "Future", Status: domain.TaskReady, Priority: domain.PriorityHigh, PlannedAt: &future}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, domain.Task{ID: "due", WorkspaceID: workspace.ID, Title: "Due", Status: domain.TaskReady, Priority: domain.PriorityNormal, PlannedAt: &due}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextReadyTaskForWorkspace(ctx, workspace.ID)
	if err != nil || claimed.ID != "due" {
		t.Fatalf("claimed task = %#v, %v", claimed, err)
	}
}

func TestDeleteTaskRemovesPendingApprovalAndKeepsResolvedHistory(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{Name: "Delete test", Path: "/tmp/delete-test", Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, domain.Task{Title: "Removable", Status: domain.TaskWaiting, WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.CreateApproval(ctx, domain.Approval{TaskID: task.ID, Status: domain.ApprovalPending, ProposedAction: "Review"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.CreateApproval(ctx, domain.Approval{TaskID: task.ID, Status: domain.ApprovalPending, ProposedAction: "Keep history"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveApproval(ctx, resolved.ID, domain.ApprovalRejected, "No", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetApproval(ctx, pending.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending approval survived task deletion: %v", err)
	}
	kept, err := s.GetApproval(ctx, resolved.ID)
	if err != nil || kept.TaskID != "" {
		t.Fatalf("resolved approval history was not retained and detached: %#v, %v", kept, err)
	}
}

func TestClaimNextReadyTaskIsAtomicAcrossGoroutines(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateTask(ctx, domain.Task{ID: "only", Title: "Only", Status: domain.TaskReady, Priority: domain.PriorityNormal}); err != nil {
		t.Fatal(err)
	}

	var claimed atomic.Int32
	var unexpected atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ClaimNextReadyTask(ctx)
			switch {
			case err == nil:
				claimed.Add(1)
			case errors.Is(err, ErrNotFound):
			default:
				unexpected.Add(1)
			}
		}()
	}
	wg.Wait()
	if claimed.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("claims = %d, unexpected errors = %d", claimed.Load(), unexpected.Load())
	}
}

func TestRunEventAndArtifactRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 8, 11, 10, 0, 0, 321, time.UTC)
	task, err := s.CreateTask(ctx, domain.Task{ID: "t1", Title: "Task", Status: domain.TaskWaiting})
	if err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	ended := t0.Add(time.Minute)
	runResult := &domain.RunResult{
		Status: "completed", Summary: "verified", FilesChanged: []string{"one.go"},
		Verification:  []domain.Verification{{Name: "tests", Status: "passed"}},
		Uncertainties: []string{},
	}
	wantRun := domain.Run{
		ID: "r1", TaskID: task.ID, Type: domain.RunExecute, Status: domain.RunCompleted,
		PID: 123, WorkingDirectory: "/tmp/repo", Command: []string{"codex", "exec"}, SessionID: "session",
		Attempt: 2, StartedAt: t0, EndedAt: &ended, ExitCode: &exitCode,
		StdoutPath: "/tmp/out", StderrPath: "/tmp/err", RawOutput: "raw", Result: runResult,
	}
	if _, err := s.CreateRun(ctx, wantRun); err != nil {
		t.Fatal(err)
	}
	gotRun, err := s.GetRun(ctx, wantRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotRun, wantRun) {
		t.Fatalf("run = %#v, want %#v", gotRun, wantRun)
	}

	eventData := json.RawMessage(`{"summary":"done"}`)
	event, err := s.AppendEvent(ctx, domain.Event{Type: "task.completed", EntityID: task.ID, Data: eventData, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if event.ID == 0 {
		t.Fatal("event sequence ID was not assigned")
	}
	events, err := s.ListEvents(ctx, 0, 10)
	if err != nil || len(events) != 1 || !reflect.DeepEqual(events[0], event) {
		t.Fatalf("events = %#v, %v", events, err)
	}

	wantArtifact := domain.Artifact{
		ID: "a1", TaskID: task.ID, RunID: wantRun.ID, Kind: "git_diff", Name: "Changes",
		Path: "/tmp/diff.patch", URL: "", Metadata: json.RawMessage(`{"files":1}`), CreatedAt: t0,
	}
	if _, err := s.CreateArtifact(ctx, wantArtifact); err != nil {
		t.Fatal(err)
	}
	artifacts, err := s.ListArtifacts(ctx, task.ID, "")
	if err != nil || len(artifacts) != 1 || !reflect.DeepEqual(artifacts[0], wantArtifact) {
		t.Fatalf("artifacts = %#v, %v", artifacts, err)
	}
}

func TestRecoverInterruptedIsAtomicAndIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	if _, err := s.CreateTask(ctx, domain.Task{ID: "t1", Title: "Running", Status: domain.TaskRunning, Priority: domain.PriorityHigh, CurrentRunID: "r1", StartedAt: &t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRun(ctx, domain.Run{ID: "r1", TaskID: "t1", Type: domain.RunExecute, Status: domain.RunRunning, PID: 42, StartedAt: t0}); err != nil {
		t.Fatal(err)
	}
	restarted := t0.Add(time.Hour)
	result, err := s.RecoverInterrupted(ctx, restarted)
	if err != nil {
		t.Fatal(err)
	}
	if result.TasksInterrupted != 1 || result.RunsInterrupted != 1 {
		t.Fatalf("recovery result = %+v", result)
	}
	task, err := s.GetTask(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskReady || task.CurrentRunID != "" {
		t.Fatalf("recovered task = %+v", task)
	}
	run, err := s.GetRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunInterrupted || run.PID != 0 || run.EndedAt == nil || !run.EndedAt.Equal(restarted) {
		t.Fatalf("recovered run = %+v", run)
	}
	result, err = s.RecoverInterrupted(ctx, restarted.Add(time.Hour))
	if err != nil || result != (RecoveryResult{}) {
		t.Fatalf("second recovery = %+v, %v", result, err)
	}
}

func TestDurableChatQueueClaimsFIFOAndRecoversProcessing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{Name: "Queue", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "first", Status: domain.MessageQueued})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "second", Status: domain.MessageQueued})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextQueuedMessage(ctx)
	if err != nil || claimed.ID != first.ID || claimed.Status != domain.MessageProcessing {
		t.Fatalf("first claim = %#v, %v", claimed, err)
	}
	if err := s.DeleteMessage(ctx, claimed.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("processing deletion = %v, want invalid transition", err)
	}
	if queued, err := s.QueuedMessageCount(ctx); err != nil || queued != 1 {
		t.Fatalf("queued count = %d, %v", queued, err)
	}
	if recovered, err := s.RecoverInterrupted(ctx); err != nil || recovered.MessagesRequeued != 1 {
		t.Fatalf("recovered = %+v, %v", recovered, err)
	}
	claimed, err = s.ClaimNextQueuedMessage(ctx)
	if err != nil || claimed.ID != first.ID {
		t.Fatalf("recovered FIFO claim = %#v, %v; second=%d", claimed, err, second.ID)
	}
}

func TestWorkspaceContextReadinessPersists(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{Name: "Context", Path: t.TempDir(), Allowed: true, ContextReady: true, ContextPrompted: true})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.ContextReady || !loaded.ContextPrompted {
		t.Fatalf("workspace context flags = %#v", loaded)
	}
}
