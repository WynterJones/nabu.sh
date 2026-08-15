package operator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/eventbus"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

type fakeExecutor struct {
	result string
	err    error
}

type recordingExecutor struct {
	result   string
	runs     int
	requests []runner.Request
}

func (f *recordingExecutor) Run(ctx context.Context, request runner.Request) (runner.ExecutionResult, error) {
	f.runs++
	f.requests = append(f.requests, request)
	return fakeExecutor{result: f.result}.Run(ctx, request)
}

func (f fakeExecutor) Run(_ context.Context, request runner.Request) (runner.ExecutionResult, error) {
	started := time.Now().UTC()
	if request.OnStart != nil {
		request.OnStart(runner.ProcessStarted{Attempt: 1, PID: 4242, WorkingDirectory: request.WorkingDirectory, Command: []string{"fake-codex"}, StartedAt: started})
	}
	if request.OnOutput != nil {
		request.OnOutput(runner.OutputEvent{Attempt: 1, Stream: runner.OutputStdout, Data: f.result, At: started})
	}
	exit := 0
	return runner.ExecutionResult{
		Attempt: 1, PID: 4242, WorkingDirectory: request.WorkingDirectory, Command: []string{"fake-codex"},
		StartedAt: started, EndedAt: started.Add(time.Millisecond), ExitCode: &exit,
		Status: domain.RunCompleted, Stdout: f.result,
	}, f.err
}

func TestRunTaskPersistsNormalizedEvidence(t *testing.T) {
	operator, database, paths, workspace := testOperator(t, fakeExecutor{result: `{
  "status":"completed","summary":"Verified the requested change.","files_changed":["README.md"],
  "verification":[{"name":"test","status":"passed","details":"exit 0"}],
  "artifacts":[],"uncertainties":[],"approval_needed":null
}`})
	if err := os.WriteFile(filepath.Join(workspace.Path, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Verify work", Purpose: "Prove the run lifecycle", Why: "Evidence matters", Status: domain.TaskRunning,
		Priority: domain.PriorityHigh, WorkspaceID: workspace.ID, CreatedBy: "user",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "Verification passes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.runTask(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	completed, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.TaskCompleted || completed.Result == nil {
		t.Fatalf("task was not completed with a result: %#v", completed)
	}
	if completed.Result.Summary != "Verified the requested change." {
		t.Fatalf("unexpected summary: %q", completed.Result.Summary)
	}
	if completed.CurrentRunID == "" {
		t.Fatal("task did not retain its run relationship")
	}
	run, err := database.GetRun(context.Background(), completed.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.PID != 4242 || run.Status != domain.RunCompleted || run.StdoutPath == "" {
		t.Fatalf("run metadata was not persisted: %#v", run)
	}
	if !strings.HasPrefix(run.StdoutPath, paths.Runs) {
		t.Fatalf("stdout escaped the run directory: %s", run.StdoutPath)
	}
}

func TestStatusDistinguishesQueuedChatFromActiveWork(t *testing.T) {
	service, _, _, _ := testOperator(t, fakeExecutor{})
	if _, err := service.SendChat(context.Background(), api.ChatSend{Content: "Review the analytics plan"}); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().UTC().Add(time.Minute)
	service.mu.Lock()
	service.codexState = "rate_limited"
	service.codexReason = "Codex is rate limited. Nabu kept the queue intact."
	service.codexRetryAt = &retryAt
	service.mu.Unlock()

	snapshot, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status == domain.GlobalWorking {
		t.Fatalf("queued chat was reported as active work: %#v", snapshot)
	}
	if snapshot.Status != domain.GlobalNeedsAttention || snapshot.ChatQueued != 1 {
		t.Fatalf("queued chat status = %#v", snapshot)
	}
	if len(snapshot.Activities) != 1 || snapshot.Activities[0].Kind != "chat" || snapshot.Activities[0].Status != "waiting" {
		t.Fatalf("queued chat activity = %#v", snapshot.Activities)
	}
}

func TestCompletedReportArtifactBecomesDurableReport(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: `{
  "status":"completed","summary":"Summarized verified traffic.","files_changed":["reports/traffic.md"],
  "verification":[{"name":"provider response","status":"passed","details":"Traffic values were present."}],
  "artifacts":[{"kind":"report","name":"Traffic summary","path":"reports/traffic.md"}],
  "uncertainties":[],"approval_needed":null
}`})
	if err := os.MkdirAll(filepath.Join(workspace.Path, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "reports", "traffic.md"), []byte("# Traffic\n\n42 visitors.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Prepare report: Traffic summary", Purpose: "Create a durable report", Status: domain.TaskReady,
		Priority: domain.PriorityHigh, WorkspaceID: workspace.ID, CreatedBy: "chat",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "The report is linked from Nabu"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.runTask(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	reports, err := database.ListReports(context.Background(), store.ReportFilter{WorkspaceID: workspace.ID, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Title != "Traffic summary" || reports[0].Path != "reports/traffic.md" {
		t.Fatalf("durable reports = %#v", reports)
	}
	if !strings.Contains(reports[0].Body, "42 visitors") || reports[0].Summary != "Summarized verified traffic." {
		t.Fatalf("durable report content = %#v", reports[0])
	}
	if len(reports[0].Artifacts) != 1 || reports[0].Artifacts[0].Kind != "report" {
		t.Fatalf("durable report artifacts = %#v", reports[0].Artifacts)
	}
	created, err := operator.ReconcileReports(context.Background())
	if err != nil || created != 0 {
		t.Fatalf("idempotent reconciliation = %d, %v", created, err)
	}
}

func TestReconcileReportsBackfillsCompletedTask(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	if err := os.MkdirAll(filepath.Join(workspace.Path, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "reports", "existing.md"), []byte("# Existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Prepare report: Existing findings", Purpose: "Preserve the findings", Status: domain.TaskCompleted,
		Priority: domain.PriorityNormal, WorkspaceID: workspace.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateArtifact(context.Background(), domain.Artifact{
		TaskID: task.ID, Kind: "report", Name: "Existing findings", Path: "reports/existing.md",
	}); err != nil {
		t.Fatal(err)
	}
	created, err := operator.ReconcileReports(context.Background())
	if err != nil || created != 1 {
		t.Fatalf("reconciliation = %d, %v", created, err)
	}
	created, err = operator.ReconcileReports(context.Background())
	if err != nil || created != 0 {
		t.Fatalf("second reconciliation = %d, %v", created, err)
	}
}

func TestDeleteTaskRejectsRunningAndRemovesTerminalTask(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	running, err := database.CreateTask(ctx, domain.Task{
		Title: "Active work", Purpose: "Keep it", Status: domain.TaskRunning,
		Priority: domain.PriorityNormal, WorkspaceID: workspace.ID, CreatedBy: "user",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "Finished"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.DeleteTask(ctx, running.ID); err == nil {
		t.Fatal("running task deletion should require cancellation first")
	}
	completed, err := database.CreateTask(ctx, domain.Task{
		Title: "Old work", Purpose: "Remove it", Status: domain.TaskCompleted,
		Priority: domain.PriorityNormal, WorkspaceID: workspace.ID, CreatedBy: "user",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "Finished"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.DeleteTask(ctx, completed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetTask(ctx, completed.ID); err == nil {
		t.Fatal("deleted task is still present")
	}
}

func TestOrientationCapsAndDeduplicatesReadyTasks(t *testing.T) {
	result := `{
  "summary":"Selected a bounded next set.",
  "tasks":[
    {"title":"First task","purpose":"One","why":"Mission","priority":"high","definition_of_done":[{"text":"Done","completed":false}]},
    {"title":"Second task","purpose":"Two","why":"Mission","priority":"normal","definition_of_done":[{"text":"Done","completed":false}]},
    {"title":"Second task","purpose":"Duplicate","why":"Mission","priority":"low","definition_of_done":[]},
    {"title":"Third task","purpose":"Three","why":"Mission","priority":"low","definition_of_done":[{"text":"Done","completed":false}]},
    {"title":"Fourth task","purpose":"Four","why":"Mission","priority":"low","definition_of_done":[]}
  ],
  "priority_updates":[],"no_work_needed":false
}`
	operator, database, _, _ := testOperator(t, fakeExecutor{result: result})
	if err := operator.runOrientation(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks, err := database.ListTasks(context.Background(), store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != runner.MaxOrientationTasks {
		t.Fatalf("expected %d bounded tasks, got %d", runner.MaxOrientationTasks, len(tasks))
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		if seen[task.Title] {
			t.Fatalf("duplicate task survived orientation: %s", task.Title)
		}
		seen[task.Title] = true
		if task.CreatedBy != "orientation" || task.Why == "" {
			t.Fatalf("task rationale was not durable: %#v", task)
		}
	}
}

func TestIdleStewardRunsAfterMinimumIdleDurationAndPersistsCooldown(t *testing.T) {
	executor := &recordingExecutor{result: `{
  "summary":"The mission is intentionally waiting for its scheduled review.",
  "tasks":[],"priority_updates":[],"no_work_needed":true
}`}
	operator, database, _, workspace := testOperator(t, executor)
	ctx := context.Background()
	if err := database.CompleteOrientation(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-idleMinimumDuration - time.Second)
	if due, err := database.RecordIdleCheck(ctx, workspace.ID, start, idleMinimumDuration, idleReviewLease); err != nil || due {
		t.Fatalf("seed idle window = due %t, error %v", due, err)
	}
	if executor.runs != 0 {
		t.Fatalf("Codex ran before minimum idle duration: %d", executor.runs)
	}
	if err := operator.workOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if executor.runs != 1 || len(executor.requests) != 1 {
		t.Fatalf("idle steward runs = %d, requests = %d", executor.runs, len(executor.requests))
	}
	if !strings.Contains(executor.requests[0].Prompt, "periodic idle stewardship review") {
		t.Fatalf("idle prompt was not stewardship-specific:\n%s", executor.requests[0].Prompt)
	}
	state, err := database.GetIdleStewardState(ctx, workspace.ID)
	if err != nil || state.LastRunAt == nil || state.NextRunAt == nil || state.NextRunAt.Sub(*state.LastRunAt) != idleNoWorkCooldown {
		t.Fatalf("idle steward state = %#v, error %v", state, err)
	}
	for check := 0; check < 50; check++ {
		if err := operator.workOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if executor.runs != 1 {
		t.Fatalf("idle steward ignored cooldown; runs = %d", executor.runs)
	}
}

func TestCompletionRequiresPassedVerification(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: `{
  "status":"completed","summary":"Claimed completion despite a failing check.","files_changed":[],
  "verification":[{"name":"test suite","status":"failed","details":"exit 1"}],
  "artifacts":[],"uncertainties":[],"approval_needed":null
}`})
	task, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Reject weak evidence", Purpose: "Do not accept a failing test", Status: domain.TaskRunning,
		Priority: domain.PriorityNormal, WorkspaceID: workspace.ID, CreatedBy: "user",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "Tests pass"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.runTask(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	failed, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != domain.TaskFailed || failed.DefinitionOfDone[0].Completed {
		t.Fatalf("failing evidence was accepted: %#v", failed)
	}
}

func TestApplyDefinitionOutcomesTracksPassedFailedAndPendingCriteria(t *testing.T) {
	items := []domain.DefinitionItem{
		{Text: "Build passes"},
		{Text: "Browser check passes"},
		{Text: "Publish notes"},
	}
	applyDefinitionOutcomes(items, []domain.DefinitionOutcome{
		{Text: "Build passes", Status: "passed", Details: "Exit code 0."},
		{Text: "Browser check passes", Status: "failed", Details: "Browser unavailable."},
	}, domain.TaskFailed)
	if !items[0].Completed || items[0].Failed || items[0].Details != "Exit code 0." {
		t.Fatalf("passed criterion = %#v", items[0])
	}
	if items[1].Completed || !items[1].Failed || items[1].Details != "Browser unavailable." {
		t.Fatalf("failed criterion = %#v", items[1])
	}
	if items[2].Completed || !items[2].Failed {
		t.Fatalf("unmet criterion = %#v", items[2])
	}
}

func TestVerificationAllowsDocumentedOutOfScopeCheckAlongsidePassedEvidence(t *testing.T) {
	items := []domain.Verification{
		{Name: "Repository checkout", Status: "passed", Details: "All requested repositories are present and clean."},
		{Name: "Application test suites", Status: "not_run", Details: "Application execution was outside this inventory task."},
	}
	if !verificationPassed(items) {
		t.Fatal("documented not_run evidence rejected despite a passing required check")
	}
	if verificationPassed([]domain.Verification{{Name: "Tests", Status: "not_run", Details: "No applicable test."}}) {
		t.Fatal("not_run evidence alone was accepted as completion evidence")
	}
}

func TestResumeRequeuesTaskInterruptedByPause(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	task, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Paused work", Purpose: "Resume later", Status: domain.TaskWaiting, Priority: domain.PriorityNormal,
		WorkspaceID: workspace.ID, CreatedBy: "user", CurrentRunID: "run-before-pause",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "Finish"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := database.GetSettings(context.Background())
	settings.Paused = true
	if err := database.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.SetPaused(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	resumed, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != domain.TaskReady || resumed.CurrentRunID != "" {
		t.Fatalf("paused task was not requeued: %#v", resumed)
	}
}

func testOperator(t *testing.T, executor Executor) (*Operator, *store.Store, config.Paths, domain.Workspace) {
	t.Helper()
	paths, err := config.Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(paths.Root, "approved")
	if err := os.MkdirAll(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := database.CreateWorkspace(context.Background(), domain.Workspace{
		Name: "approved", Path: workspacePath, Allowed: true, MissionStarted: true,
		ContextReady: true, ContextPrompted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateMission(context.Background(), domain.Mission{
		WorkspaceID: workspace.ID, Statement: "Advance the test mission", Context: "Keep it bounded", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.SetupComplete, settings.MissionStarted = true, true
	if err := database.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	service := New(database, executor, paths, eventbus.New(), nil)
	t.Cleanup(func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Stop(stopContext)
		_ = database.Close()
	})
	return service, database, paths, workspace
}
