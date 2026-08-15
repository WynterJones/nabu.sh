package automation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestRunScriptNowPersistsResultArtifactsAndInterestingTask(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	database, scriptsRoot, engine := testEngine(t, nil)
	artifactPath := filepath.Join(scriptsRoot, "finding.json")
	if err := os.WriteFile(artifactPath, []byte(`{"latency_ms":450}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(domain.ScriptResult{
		Status:      "attention",
		Summary:     "Latency increased above the operating threshold.",
		Interesting: true,
		Artifacts: []domain.Artifact{{
			Kind: "json", Name: "Latency sample", Path: artifactPath,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	script := createScript(t, database, scriptsRoot, "site-health", string(result))

	run, err := engine.RunScriptNow(ctx, script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.ScriptRunCompleted || run.Result == nil || !run.Result.Interesting {
		t.Fatalf("unexpected script run: %#v", run)
	}
	stored, err := database.GetScriptRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Result == nil || len(stored.Result.Artifacts) != 1 || stored.Result.Artifacts[0].ScriptRunID != run.ID {
		t.Fatalf("stored result does not link its artifact: %#v", stored.Result)
	}
	artifacts, err := database.ListScriptRunArtifacts(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].ScriptRunID != run.ID {
		t.Fatalf("unexpected artifacts: %#v", artifacts)
	}
	tasks, err := database.ListTasks(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskReady || tasks[0].CreatedBy != "script:"+script.ID {
		t.Fatalf("interesting result did not create one ready task: %#v", tasks)
	}
	if len(tasks[0].Title) > maximumTitleBytes || len(tasks[0].DefinitionOfDone) != 1 {
		t.Fatalf("interesting task was not bounded: %#v", tasks[0])
	}
}

func TestTaskScriptExecutionRejectsWriteCapability(t *testing.T) {
	requireUnix(t)
	database, scriptsRoot, engine := testEngine(t, nil)
	path := filepath.Join(scriptsRoot, "write-capability.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '{\"status\":\"completed\",\"summary\":\"should not run\"}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := database.CreateScript(context.Background(), domain.Script{
		Name: "write capability", Path: filepath.Base(path), Enabled: true, Access: domain.ScriptAccessWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunScriptForTask(context.Background(), script.ID, script.WorkspaceID, t.TempDir()); err == nil || !strings.Contains(err.Error(), "read access") {
		t.Fatalf("write-capable script executed for task: %v", err)
	}
}

func TestTaskScriptExecutionRejectsCrossWorkspaceID(t *testing.T) {
	requireUnix(t)
	database, scriptsRoot, engine := testEngine(t, nil)
	script := createScript(t, database, scriptsRoot, "scoped-browser-qa", `{"status":"completed","summary":"ok"}`)
	if _, err := engine.RunScriptForTask(context.Background(), script.ID, "another-workspace", t.TempDir()); err == nil || !strings.Contains(err.Error(), "requested workspace") {
		t.Fatalf("cross-workspace script executed: %v", err)
	}
}

func TestManualScriptRunsFromItsStoredWorkspace(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	database, scriptsRoot, engine := testEngine(t, nil)
	workspacePath := t.TempDir()
	workspace, err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "manual-script-workspace", Name: "Manual script workspace", Path: workspacePath, Allowed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	script := createWorkingDirectoryScript(t, database, scriptsRoot, workspace.ID, "manual-workspace")
	run, err := engine.RunScriptNow(ctx, script.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if run.Result == nil || run.Result.Summary != expected {
		t.Fatalf("manual script cwd = %#v, want %q", run.Result, expected)
	}
}

func TestScheduledScriptRunsFromItsStoredWorkspace(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	database, scriptsRoot, engine := testEngine(t, func() time.Time { return now })
	workspacePath := t.TempDir()
	workspace, err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "scheduled-script-workspace", Name: "Scheduled script workspace", Path: workspacePath, Allowed: true, ContextReady: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	script := createWorkingDirectoryScript(t, database, scriptsRoot, workspace.ID, "scheduled-workspace")
	schedule := createDueSchedule(t, database, now, domain.Schedule{
		WorkspaceID: workspace.ID, Name: "scheduled workspace verifier", Kind: domain.ScheduleScript, IntervalSeconds: 3600,
		Payload: mustJSON(t, ScriptPayload{ScriptID: script.ID, OnInteresting: InterestingNone}),
	})
	if err := engine.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := database.ListScriptRuns(ctx, store.ScriptRunFilter{ScheduleID: schedule.ID})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Result == nil || runs[0].Result.Summary != expected {
		t.Fatalf("scheduled script runs = %#v, want cwd %q", runs, expected)
	}
}

func TestManualScriptRejectsUnapprovedStoredWorkspace(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	database, scriptsRoot, engine := testEngine(t, nil)
	workspace, err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "unapproved-script-workspace", Name: "Unapproved", Path: t.TempDir(), Allowed: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	script := createWorkingDirectoryScript(t, database, scriptsRoot, workspace.ID, "unapproved-workspace")
	if _, err := engine.RunScriptNow(ctx, script.ID); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("unapproved workspace script executed: %v", err)
	}
}

func TestScriptScheduleDoesNotQueueAIWorkForOrdinaryResult(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	database, scriptsRoot, engine := testEngine(t, func() time.Time { return now })
	script := createScript(t, database, scriptsRoot, "analytics-summary", `{"status":"completed","summary":"No meaningful change.","interesting":false}`)
	payload := mustJSON(t, ScriptPayload{ScriptID: script.ID})
	schedule := createDueSchedule(t, database, now, domain.Schedule{
		Name: "analytics", Kind: domain.ScheduleScript, IntervalSeconds: 3600, Payload: payload,
	})

	if err := engine.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	queued, _, err := database.OrientationState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("ordinary script result unexpectedly queued orientation")
	}
	tasks, err := database.ListTasks(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("ordinary script result unexpectedly created tasks: %#v", tasks)
	}
	runs, err := database.ListScriptRuns(ctx, store.ScriptRunFilter{ScheduleID: schedule.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != domain.ScriptRunCompleted {
		t.Fatalf("scheduled script run not persisted: %#v", runs)
	}
	updated, err := database.GetSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ClaimToken != "" || updated.LastRunAt == nil || updated.NextRunAt == nil || !updated.NextRunAt.After(now) || updated.LastError != "" {
		t.Fatalf("schedule claim was not cleanly advanced: %#v", updated)
	}
}

func TestInterestingScriptCanRequestOrientationWithoutCreatingTask(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	database, scriptsRoot, engine := testEngine(t, func() time.Time { return now })
	script := createScript(t, database, scriptsRoot, "signal", `{"status":"attention","summary":"A meaningful signal arrived.","interesting":true}`)
	createDueSchedule(t, database, now, domain.Schedule{
		Name: "signal", Kind: domain.ScheduleScript, IntervalSeconds: 60,
		Payload: mustJSON(t, ScriptPayload{ScriptID: script.ID, OnInteresting: InterestingOrient}),
	})

	if err := engine.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	queued, _, err := database.OrientationState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("interesting script did not queue orientation")
	}
	tasks, err := database.ListTasks(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("orientation action unexpectedly created a task: %#v", tasks)
	}
}

func TestCancelledScriptStillPersistsTerminalState(t *testing.T) {
	requireUnix(t)
	database, scriptsRoot, engine := testEngine(t, nil)
	path := filepath.Join(scriptsRoot, "slow.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := database.CreateScript(context.Background(), domain.Script{
		Name: "slow", Path: filepath.Base(path), Enabled: true, TimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	run, err := engine.RunScriptNow(ctx, script.ID)
	if err == nil {
		t.Fatal("cancelled script unexpectedly succeeded")
	}
	stored, getErr := database.GetScriptRun(context.Background(), run.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.Status != domain.ScriptRunTimedOut || stored.EndedAt == nil {
		t.Fatalf("cancelled terminal state was not persisted: %#v", stored)
	}
}

func TestTaskAndOrientationSchedulesDispatchDurableRequests(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	database, _, engine := testEngine(t, func() time.Time { return now })
	createDueSchedule(t, database, now, domain.Schedule{
		Name: "weekly review", Kind: domain.ScheduleTask, Expression: "@weekly",
		Payload: mustJSON(t, TaskPayload{
			Title: "Review weekly search performance", Purpose: "Find material changes.",
			DefinitionOfDone: []string{"Record verified findings."}, Priority: domain.PriorityHigh,
		}),
	})
	createDueSchedule(t, database, now, domain.Schedule{
		Name: "daily orientation", Kind: domain.ScheduleOrient, Expression: "@daily",
		Payload: mustJSON(t, OrientPayload{Reason: "Daily mission review"}),
	})

	if err := engine.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, err := database.ListTasks(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Priority != domain.PriorityHigh || !strings.HasPrefix(tasks[0].CreatedBy, "schedule:") {
		t.Fatalf("unexpected scheduled task: %#v", tasks)
	}
	queued, _, err := database.OrientationState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("orientation schedule did not queue durable orientation")
	}
}

func TestScheduleEffectsRemainInTheirWorkspaceScope(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	database, scriptsRoot, engine := testEngine(t, func() time.Time { return now })
	workspaceOne, err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-one", Name: "One", Path: t.TempDir(), Allowed: true, ContextReady: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaceTwo, err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-two", Name: "Two", Path: t.TempDir(), Allowed: true, ContextReady: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetActiveWorkspace(ctx, workspaceTwo.ID); err != nil {
		t.Fatal(err)
	}
	script := createScript(t, database, scriptsRoot, "scoped-signal", `{"status":"attention","summary":"Scoped signal.","interesting":true}`)
	createDueSchedule(t, database, now, domain.Schedule{
		WorkspaceID: workspaceOne.ID, Name: "scoped task", Kind: domain.ScheduleTask, IntervalSeconds: 60,
		Payload: mustJSON(t, TaskPayload{Title: "Scoped task", DefinitionOfDone: []string{"Verify scope."}}),
	})
	createDueSchedule(t, database, now, domain.Schedule{
		WorkspaceID: workspaceOne.ID, Name: "scoped orient", Kind: domain.ScheduleOrient, IntervalSeconds: 60,
	})
	createDueSchedule(t, database, now, domain.Schedule{
		WorkspaceID: workspaceOne.ID, Name: "scoped signal", Kind: domain.ScheduleScript, IntervalSeconds: 60,
		Payload: mustJSON(t, ScriptPayload{ScriptID: script.ID, OnInteresting: InterestingTask}),
	})

	if err := engine.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, err := database.ListTasks(ctx, store.TaskFilter{WorkspaceID: workspaceOne.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("workspace one tasks = %#v", tasks)
	}
	for _, task := range tasks {
		if task.WorkspaceID != workspaceOne.ID {
			t.Fatalf("effect escaped schedule workspace: %#v", task)
		}
	}
	otherTasks, err := database.ListTasks(ctx, store.TaskFilter{WorkspaceID: workspaceTwo.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(otherTasks) != 0 {
		t.Fatalf("inactive schedule effects leaked into active workspace: %#v", otherTasks)
	}
	queued, _, err := database.OrientationStateForWorkspace(ctx, workspaceOne.ID)
	if err != nil || !queued {
		t.Fatalf("workspace one orientation = %v, %v", queued, err)
	}
	settings, err := database.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ActiveWorkspaceID != workspaceTwo.ID || settings.OrientationQueued {
		t.Fatalf("inactive workspace request leaked into active settings: %#v", settings)
	}
}

func TestInvalidSchedulePayloadIsRecordedAndAdvancedWithoutSideEffect(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	database, _, engine := testEngine(t, func() time.Time { return now })
	schedule := createDueSchedule(t, database, now, domain.Schedule{
		Name: "invalid task", Kind: domain.ScheduleTask, IntervalSeconds: 60,
		Payload: json.RawMessage(`{"title":"Unsafe ambiguity","unknown":true}`),
	})

	err := engine.RunDue(ctx)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict payload error, got %v", err)
	}
	updated, getErr := database.GetSchedule(ctx, schedule.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if updated.LastError == "" || updated.NextRunAt == nil || updated.ClaimToken != "" {
		t.Fatalf("failed schedule attempt was not safely finished: %#v", updated)
	}
	tasks, listErr := database.ListTasks(ctx, store.TaskFilter{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(tasks) != 0 {
		t.Fatalf("invalid payload produced a side effect: %#v", tasks)
	}
}

func TestScheduledTaskEffectIsIdempotentPerOccurrence(t *testing.T) {
	ctx := context.Background()
	database, _, engine := testEngine(t, nil)
	payload := TaskPayload{Title: "One occurrence", DefinitionOfDone: []string{"Complete once."}}
	id := stableID("schedule-effect", "schedule-one\x002026-08-12T12:00:00Z")
	first, err := engine.createTask(ctx, payload, "schedule:schedule-one", id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.createTask(ctx, payload, "schedule:schedule-one", id)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent effect IDs differ: %q, %q", first.ID, second.ID)
	}
	tasks, err := database.ListTasks(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("idempotent effect created %d tasks: %#v", len(tasks), tasks)
	}
	if id == stableID("schedule-effect", "schedule-one\x002026-08-13T12:00:00Z") {
		t.Fatal("distinct occurrences produced the same stable ID")
	}
}

func TestExpiredScheduleLeaseIsRecovered(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	claimedAt := time.Now().UTC().Truncate(time.Second)
	now := claimedAt.Add(2 * time.Minute)
	database, scriptsRoot, engine := testEngine(t, func() time.Time { return now })
	script := createScript(t, database, scriptsRoot, "recovered", `{"status":"completed","summary":"Recovered safely."}`)
	schedule := createDueSchedule(t, database, claimedAt, domain.Schedule{
		Name: "recover lease", Kind: domain.ScheduleScript, IntervalSeconds: 60,
		Payload: mustJSON(t, ScriptPayload{ScriptID: script.ID, OnInteresting: InterestingNone}),
	})
	claimed, err := database.ClaimDueSchedule(ctx, claimedAt, time.Second)
	if err != nil || claimed.ClaimToken == "" {
		t.Fatalf("initial claim: %#v, %v", claimed, err)
	}

	if err := engine.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := database.ListScriptRuns(ctx, store.ScriptRunFilter{ScheduleID: schedule.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != domain.ScriptRunCompleted {
		t.Fatalf("expired lease was not recovered exactly once: %#v", runs)
	}
}

func TestStartProcessesDueWorkImmediatelyAndStopsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC().Truncate(time.Second)
	database, _, engine := testEngine(t, func() time.Time { return now })
	createDueSchedule(t, database, now, domain.Schedule{
		Name: "startup orient", Kind: domain.ScheduleOrient, IntervalSeconds: 3600,
	})
	done := make(chan error, 1)
	go func() { done <- engine.Start(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		queued, _, err := database.OrientationState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup pass did not process due schedule")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned an error on clean shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not stop after cancellation")
	}
}

func TestStartRejectsConcurrentInvocation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, engine := testEngine(t, nil)
	done := make(chan error, 1)
	go func() { done <- engine.Start(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		engine.stateMu.Lock()
		started := engine.started
		engine.stateMu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := engine.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("concurrent Start error = %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func testEngine(t *testing.T, now func() time.Time) (*store.Store, string, *Engine) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "nabu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	scriptsRoot := t.TempDir()
	engine, err := New(Options{
		Store: database, ScriptsRoot: scriptsRoot, RunsRoot: t.TempDir(),
		TickInterval: time.Hour, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return database, scriptsRoot, engine
}

func createScript(t *testing.T, database *store.Store, root, name, result string) domain.Script {
	t.Helper()
	path := filepath.Join(root, name+".sh")
	content := "#!/bin/sh\nprintf '%s\\n' '" + result + "'\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := database.CreateScript(context.Background(), domain.Script{
		Name: name, Path: filepath.Base(path), Enabled: true, TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return script
}

func createWorkingDirectoryScript(t *testing.T, database *store.Store, root, workspaceID, name string) domain.Script {
	t.Helper()
	path := filepath.Join(root, name+".sh")
	content := "#!/bin/sh\nprintf '{\"status\":\"completed\",\"summary\":\"%s\"}\\n' \"$PWD\"\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := database.CreateScript(context.Background(), domain.Script{
		WorkspaceID: workspaceID, Name: name, Path: filepath.Base(path), Enabled: true,
		Access: domain.ScriptAccessRead, TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return script
}

func createDueSchedule(t *testing.T, database *store.Store, now time.Time, schedule domain.Schedule) domain.Schedule {
	t.Helper()
	schedule.Enabled = true
	due := now.Add(-time.Minute)
	schedule.NextRunAt = &due
	created, err := database.CreateSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
}
