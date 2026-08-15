package operator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
)

type taskScriptAutomationStub struct {
	run             domain.ScriptRun
	err             error
	calledID        string
	calledWorkspace string
	calledPath      string
	calls           int
}

func (s *taskScriptAutomationStub) RunScriptNow(context.Context, string) (domain.ScriptRun, error) {
	return domain.ScriptRun{}, errors.New("not implemented")
}

func (s *taskScriptAutomationStub) RunScriptForTask(_ context.Context, id, workspaceID, workspacePath string) (domain.ScriptRun, error) {
	s.calls++
	s.calledID, s.calledWorkspace, s.calledPath = id, workspaceID, workspacePath
	return s.run, s.err
}

func TestTaskScriptContextInventoriesAndDefersBrowserVerifier(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	stub := &taskScriptAutomationStub{}
	service.SetAutomation(stub)
	verifier, err := database.CreateScript(context.Background(), domain.Script{
		WorkspaceID: workspace.ID, Name: "Playwright browser QA", Path: "browser-qa.sh",
		Description: "Checks the local UI and captures screenshots.", Enabled: true, Access: domain.ScriptAccessRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateScript(context.Background(), domain.Script{
		WorkspaceID: workspace.ID, Name: "Publish site", Path: "publish.sh", Enabled: true, Access: domain.ScriptAccessWrite,
	}); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "task-browser", Title: "Run Playwright browser QA", Purpose: "Verify the responsive page"}
	plan := service.taskScriptContext(context.Background(), task, workspace)
	if !plan.BrowserQARequired || plan.BrowserVerifier == nil || plan.BrowserVerifier.ID != verifier.ID {
		t.Fatalf("browser plan = %#v", plan)
	}
	if stub.calls != 0 {
		t.Fatalf("browser verifier ran before workspace changes: %d calls", stub.calls)
	}
	inventory := strings.Join(plan.Inventory, "\n")
	if !strings.Contains(inventory, "read-only host browser verifier") || strings.Contains(inventory, "Publish site") {
		t.Fatalf("capability inventory = %q", inventory)
	}
}

func TestTaskScriptContextSelectsReadyBrowserMCP(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	if _, err := database.CreateMCPServer(context.Background(), domain.MCPServer{
		WorkspaceID: workspace.ID, Name: "Chrome DevTools", Description: "Browser inspection and screenshots",
		Transport: domain.MCPTransportHTTP, URL: "https://browser.example.com/mcp", Enabled: true, Access: domain.MCPAccessRead,
	}); err != nil {
		t.Fatal(err)
	}
	plan := service.taskScriptContext(context.Background(), domain.Task{Title: "Run responsive browser QA", Purpose: "Verify the page"}, workspace)
	if plan.BrowserMCPName != "Chrome DevTools" {
		t.Fatalf("browser MCP = %q", plan.BrowserMCPName)
	}
}

func TestBrowserQATaskRecognizesProductAcceptanceCriteria(t *testing.T) {
	tests := []struct {
		name string
		task domain.Task
		want bool
	}{
		{
			name: "current release task wording",
			task: domain.Task{DefinitionOfDone: []domain.DefinitionItem{{Text: "Automated tests verify desktop and mobile behavior, accessibility, responsive overflow, and performance budgets."}}},
			want: true,
		},
		{
			name: "lighthouse",
			task: domain.Task{Purpose: "Run Lighthouse and validate Core Web Vitals."},
			want: true,
		},
		{
			name: "ordinary unit tests",
			task: domain.Task{Purpose: "Run unit tests for the CSV parser."},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := browserQATask(test.task); got != test.want {
				t.Fatalf("browserQATask() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTaskBrowserVerificationMergesHostEvidenceWithExactScope(t *testing.T) {
	service, _, _, workspace := testOperator(t, fakeExecutor{})
	stub := &taskScriptAutomationStub{run: domain.ScriptRun{
		ID: "script-run-1", Status: domain.ScriptRunCompleted,
		Result: &domain.ScriptResult{Status: "completed", Summary: "Chromium checked desktop and mobile.", Artifacts: []domain.Artifact{{Kind: "screenshot", Name: "Mobile", Path: "artifacts/mobile.png"}}},
	}}
	service.SetAutomation(stub)
	verifier := domain.Script{ID: "verifier-1", WorkspaceID: workspace.ID, Name: "Browser QA", Access: domain.ScriptAccessRead}
	plan := taskScriptPlan{BrowserQARequired: true, BrowserVerifier: &verifier, BrowserVerifierName: verifier.Name, WorkspaceID: workspace.ID, WorkspacePath: workspace.Path}
	execution := runner.ExecutionResult{Status: domain.RunCompleted, Stdout: `{
 "status":"completed","summary":"Implemented the page.","files_changed":["page.tsx"],
 "verification":[{"name":"browser check","status":"not_run","details":"delegated"}],"artifacts":[],"uncertainties":[]
}`}
	updated := service.applyTaskBrowserVerification(context.Background(), domain.Task{ID: "task-1"}, execution, plan)
	result, err := runner.ParseRunResult(updated.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || len(result.Verification) != 1 || result.Verification[0].Status != "passed" || !strings.Contains(result.Verification[0].Details, "desktop and mobile") {
		t.Fatalf("merged result = %#v", result)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].ScriptRunID != "script-run-1" {
		t.Fatalf("browser artifacts = %#v", result.Artifacts)
	}
	if stub.calls != 1 || stub.calledID != verifier.ID || stub.calledWorkspace != workspace.ID || stub.calledPath != workspace.Path {
		t.Fatalf("verifier scope = calls:%d id:%q workspace:%q path:%q", stub.calls, stub.calledID, stub.calledWorkspace, stub.calledPath)
	}
}

func TestTaskBrowserVerificationAcceptsPassedMCPEvidenceWithoutHostRun(t *testing.T) {
	service, _, _, workspace := testOperator(t, fakeExecutor{})
	stub := &taskScriptAutomationStub{}
	service.SetAutomation(stub)
	execution := runner.ExecutionResult{Status: domain.RunCompleted, Stdout: `{
 "status":"completed","summary":"Implemented the page.","files_changed":[],
 "verification":[{"name":"Browser MCP QA","status":"passed","details":"Checked desktop and mobile viewports with no overflow."}],"artifacts":[],"uncertainties":[]
}`}
	updated := service.applyTaskBrowserVerification(context.Background(), domain.Task{ID: "task-1"}, execution,
		taskScriptPlan{BrowserQARequired: true, BrowserMCPName: "Chrome DevTools", WorkspaceID: workspace.ID, WorkspacePath: workspace.Path})
	result, err := runner.ParseRunResult(updated.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || len(result.Verification) != 1 || result.Verification[0].Status != "passed" || stub.calls != 0 {
		t.Fatalf("MCP browser result = %#v; host calls = %d", result, stub.calls)
	}
}

func TestTaskBrowserVerificationMissingDefersWhenDurableWorkCompleted(t *testing.T) {
	service, _, _, workspace := testOperator(t, fakeExecutor{})
	execution := runner.ExecutionResult{Status: domain.RunCompleted, Stdout: `{
 "status":"completed","summary":"Implemented the page.","files_changed":["src/page.tsx"],
 "verification":[{"name":"unit tests","status":"passed","details":"ok"}],"artifacts":[],"uncertainties":[]
}`}
	updated := service.applyTaskBrowserVerification(context.Background(), domain.Task{ID: "task-1"}, execution,
		taskScriptPlan{BrowserQARequired: true, WorkspaceID: workspace.ID, WorkspacePath: workspace.Path})
	result, err := runner.ParseRunResult(updated.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || !strings.Contains(result.Summary, "deferred") || len(result.Verification) != 2 || result.Verification[1].Status != "not_run" ||
		!strings.Contains(result.Verification[1].Details, "Settings > MCP connectors") || !strings.Contains(result.Verification[1].Details, "Settings > Scripts") || result.ApprovalNeeded != nil {
		t.Fatalf("missing verifier result = %#v", result)
	}
}

func TestRunTaskWithMissingBrowserVerifierSurfacesGlobalNeedsAttention(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{result: `{
 "status":"completed","summary":"Implemented safe workspace changes.","files_changed":[],
 "verification":[{"name":"unit tests","status":"passed","details":"ok"}],"artifacts":[],"uncertainties":[]
}`})
	task, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Run Playwright browser QA", Purpose: "Verify the responsive page", Status: domain.TaskRunning,
		Priority: domain.PriorityNormal, WorkspaceID: workspace.ID, DefinitionOfDone: []domain.DefinitionItem{{Text: "Browser QA passes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.runTask(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	finished, err := database.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.TaskFailed || finished.Result == nil || !strings.Contains(finished.Result.Summary, "Browser QA needs attention") {
		t.Fatalf("browser task did not fail actionably: %#v", finished)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != domain.GlobalNeedsAttention || status.NeedsAttention == 0 {
		t.Fatalf("global status did not surface attention: %#v", status)
	}
}
