package operator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestProductScopesCreateConnectAndKeepWorkspaceDataIsolated(t *testing.T) {
	operator, database, paths, original := testOperator(t, fakeExecutor{})
	ctx := context.Background()

	if _, err := operator.CreateScope(ctx, api.ScopeCreate{
		Name: "Duplicate", Path: original.Path, Mode: "create",
	}); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("duplicate scope error = %v, want conflict", err)
	}
	if _, err := os.Stat(filepath.Join(original.Path, "inbox")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("duplicate create mutated the existing workspace: %v", err)
	}

	createdPath := filepath.Join(paths.Root, "business-created")
	created, err := operator.CreateScope(ctx, api.ScopeCreate{
		Name: "Created business", Path: createdPath, Mode: "create",
		Mission: "Grow a focused product.", Context: "A bounded test business.",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range config.WorkspaceLayoutDirectories() {
		info, statErr := os.Stat(filepath.Join(createdPath, directory))
		if statErr != nil || !info.IsDir() {
			t.Fatalf("created workspace is missing %s: %v", directory, statErr)
		}
	}
	mission, err := database.GetMissionForWorkspace(ctx, created.ID)
	if err != nil || mission.Statement != "Grow a focused product." {
		t.Fatalf("created workspace mission = %#v, %v", mission, err)
	}

	connectedPath := filepath.Join(paths.Root, "established-repository")
	if err := os.MkdirAll(connectedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(connectedPath, "README.md"), []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	connected, err := operator.CreateScope(ctx, api.ScopeCreate{
		Name: "Connected business", Path: connectedPath, Mode: "connect", Mission: "Improve the established business.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(connectedPath, "inbox")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("connect mode added the managed layout: %v", err)
	}

	if _, err := operator.SetActiveScope(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	createdWorkspace, err := database.GetWorkspace(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if createdWorkspace.ContextReady || !createdWorkspace.ContextPrompted {
		t.Fatalf("new active workspace context state = ready:%t prompted:%t, want false/true", createdWorkspace.ContextReady, createdWorkspace.ContextPrompted)
	}
	onboarding, err := database.ListMessages(ctx, store.MessageFilter{WorkspaceID: created.ID, Role: domain.MessageAssistant})
	if err != nil {
		t.Fatal(err)
	}
	if len(onboarding) != 1 || !strings.Contains(onboarding[0].Content, "Are we starting fresh") {
		t.Fatalf("new workspace onboarding messages = %#v", onboarding)
	}
	customPolicy := domain.Policy{Read: "allow", Work: "ask", Publish: "deny", Dangerous: "deny"}
	if _, err := operator.UpdatePolicy(ctx, customPolicy); err != nil {
		t.Fatal(err)
	}
	report, err := database.CreateReport(ctx, domain.Report{
		WorkspaceID: created.ID, Title: "Scoped report", Summary: "Only for one business", Body: "Evidence.",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := operator.SetActiveScope(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	policy, err := operator.Policy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Work != "allow" || policy.Publish != "ask" {
		t.Fatalf("policy leaked between workspaces: %#v", policy)
	}
	if _, err := operator.Report(ctx, report.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("cross-workspace report error = %v, want not found", err)
	}
	reports, err := operator.Reports(ctx)
	if err != nil || len(reports) != 0 {
		t.Fatalf("active workspace reports = %#v, %v", reports, err)
	}

	active, err := operator.SetActiveScope(ctx, connected.ID)
	if err != nil || !active.Active {
		t.Fatalf("set active scope = %#v, %v", active, err)
	}
	scopes, err := operator.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	activeCount := 0
	for _, scope := range scopes {
		if scope.Active {
			activeCount++
			if scope.ID != connected.ID {
				t.Fatalf("wrong active scope: %#v", scope)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("active scope count = %d, want 1", activeCount)
	}
}

func TestDeleteScopeRequiresExactConfirmationAndPreservesConnectedFolder(t *testing.T) {
	operator, database, paths, original := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	connectedPath := filepath.Join(paths.Root, "connected-delete-test")
	if err := os.MkdirAll(connectedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(connectedPath, "KEEP-ME.txt")
	if err := os.WriteFile(marker, []byte("owner data"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := operator.CreateScope(ctx, api.ScopeCreate{
		Name: "Delete this scope", Path: connectedPath, Mode: "connect", Mission: "Exercise safe deletion.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.SetActiveScope(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AppendMessage(ctx, domain.Message{WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "history", Status: domain.MessageComplete}); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.DeleteScope(ctx, workspace.ID, api.ScopeDelete{Confirmation: "wrong"}); !errors.Is(err, api.ErrInvalid) {
		t.Fatalf("wrong confirmation error = %v", err)
	}
	result, err := operator.DeleteScope(ctx, workspace.ID, api.ScopeDelete{Confirmation: workspace.Name})
	if err != nil {
		t.Fatal(err)
	}
	if !result.FolderPreserved || result.ActiveWorkspaceID != original.ID {
		t.Fatalf("delete result = %#v", result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("connected workspace folder was modified: %v", err)
	}
	if _, err := database.GetWorkspace(ctx, workspace.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted workspace = %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Scopes, workspace.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace context directory remains: %v", err)
	}
}

func TestDeleteScopeRejectsOpenChat(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	if _, err := database.AppendMessage(ctx, domain.Message{WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "queued", Status: domain.MessageQueued}); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.DeleteScope(ctx, workspace.ID, api.ScopeDelete{Confirmation: workspace.Name}); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("delete with open Chat error = %v", err)
	}
}

func TestProductSchedulesScriptsApprovalsAndMemory(t *testing.T) {
	operator, database, paths, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()

	scriptName, scriptPath, description := "Health check", "site-health", "Checks the configured site."
	timeout := int64(120)
	script, err := operator.CreateScript(ctx, api.ScriptInput{
		Name: &scriptName, Path: &scriptPath, Description: &description, TimeoutSeconds: &timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if script.WorkspaceID != workspace.ID || script.Path != "site-health" || !script.Enabled {
		t.Fatalf("created script = %#v", script)
	}

	scheduleName := "Hourly health"
	scheduleKind := domain.ScheduleScript
	interval := int64(3600)
	schedule, err := operator.CreateSchedule(ctx, api.ScheduleInput{
		Name: &scheduleName, Kind: &scheduleKind,
		Cadence: &api.ScheduleCadenceInput{IntervalSeconds: &interval},
		Payload: json.RawMessage(`{"target":"Health check"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if schedule.WorkspaceID != workspace.ID || schedule.Cadence.IntervalSeconds != interval || schedule.NextRunAt == nil {
		t.Fatalf("created schedule = %#v", schedule)
	}
	var payload map[string]any
	if err := json.Unmarshal(schedule.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["script_id"] != script.ID {
		t.Fatalf("normalized schedule payload = %#v", payload)
	}
	expression := "0 * * * *"
	if _, err := operator.UpdateSchedule(ctx, schedule.ID, api.ScheduleInput{
		Expression: &expression, Cadence: &api.ScheduleCadenceInput{IntervalSeconds: &interval},
	}); !errors.Is(err, api.ErrInvalid) {
		t.Fatalf("ambiguous cadence error = %v, want invalid", err)
	}
	disabled := false
	schedule, err = operator.UpdateSchedule(ctx, schedule.ID, api.ScheduleInput{Enabled: &disabled})
	if err != nil || schedule.Enabled {
		t.Fatalf("disabled schedule = %#v, %v", schedule, err)
	}

	approval, err := database.CreateApproval(ctx, domain.Approval{
		WorkspaceID: workspace.ID, ProposedAction: "Publish a release", Why: "Owner review is required",
		ProposedChange: "Publish version 1.",
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err = operator.ResolveApproval(ctx, approval.ID, domain.ApprovalApproved, "")
	if err != nil || approval.Status != domain.ApprovalApproved || approval.ResolvedAt == nil {
		t.Fatalf("resolved approval = %#v, %v", approval, err)
	}
	if _, err := operator.ResolveApproval(ctx, approval.ID, domain.ApprovalRejected, "late"); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("second approval resolution error = %v, want conflict", err)
	}

	view, err := operator.UpdateMemory(ctx, "A durable owner preference.")
	if err != nil || !strings.Contains(view.Content, "A durable owner preference.") {
		t.Fatalf("updated memory = %#v, %v", view, err)
	}
	proposal, err := database.CreateMemoryUpdate(ctx, domain.MemoryUpdate{
		WorkspaceID: workspace.ID, Target: domain.MemoryDurable, Content: "A verified product fact.", Source: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err = operator.ResolveMemoryUpdate(ctx, proposal.ID, domain.MemoryApplied, "")
	if err != nil || proposal.Status != domain.MemoryApplied {
		t.Fatalf("resolved memory update = %#v, %v", proposal, err)
	}
	// A retried identical decision is idempotent and must not append twice.
	if retried, retryErr := operator.ResolveMemoryUpdate(ctx, proposal.ID, domain.MemoryApplied, ""); retryErr != nil || retried.Status != domain.MemoryApplied {
		t.Fatalf("retried memory update = %#v, %v", retried, retryErr)
	}
	pendingMemory, err := operator.MemoryUpdates(ctx)
	if err != nil || len(pendingMemory) != 0 {
		t.Fatalf("pending memory after resolution = %#v, %v", pendingMemory, err)
	}
	view, err = operator.Memory(ctx)
	if err != nil || !strings.Contains(view.Content, "A verified product fact.") {
		t.Fatalf("memory after proposal = %#v, %v", view, err)
	}
	if strings.Count(view.Content, "A verified product fact.") != 1 {
		t.Fatalf("retried resolution duplicated memory: %q", view.Content)
	}
	soul, err := operator.UpdateSoul(ctx, "# Soul\n\nPrefer one direct question at a time.")
	if err != nil || !strings.Contains(soul.Content, "one direct question") {
		t.Fatalf("updated soul = %#v, %v", soul, err)
	}
	soulProposal, err := database.CreateMemoryUpdate(ctx, domain.MemoryUpdate{
		WorkspaceID: workspace.ID, Target: domain.MemorySoul, Content: "Be candid about uncertainty.", Source: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.ResolveMemoryUpdate(ctx, soulProposal.ID, domain.MemoryApplied, ""); err != nil {
		t.Fatal(err)
	}
	soul, err = operator.Soul(ctx)
	if err != nil || !strings.Contains(soul.Content, "Be candid about uncertainty.") {
		t.Fatalf("soul after reflection = %#v, %v", soul, err)
	}

	otherPath := filepath.Join(paths.Root, "other-scope")
	other, err := operator.CreateScope(ctx, api.ScopeCreate{Name: "Other", Path: otherPath, Mode: "create", Mission: "Test the other workspace."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.SetActiveScope(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if scripts, err := operator.Scripts(ctx); err != nil || len(scripts) != 0 {
		t.Fatalf("scripts leaked across workspaces: %#v, %v", scripts, err)
	}
	if schedules, err := operator.Schedules(ctx); err != nil || len(schedules) != 0 {
		t.Fatalf("schedules leaked across workspaces: %#v, %v", schedules, err)
	}
	if _, err := operator.Script(ctx, script.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("cross-workspace script error = %v, want not found", err)
	}
}

func TestRestartServiceRequiresExternalController(t *testing.T) {
	operator, _, _, _ := testOperator(t, fakeExecutor{})
	if err := operator.RestartService(context.Background()); !errors.Is(err, api.ErrUnavailable) {
		t.Fatalf("RestartService error = %v, want unavailable", err)
	}
}

func TestChatThreadExtraItemCursorHasNoGap(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	root, err := database.AppendMessage(ctx, domain.Message{
		WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "root",
	})
	if err != nil {
		t.Fatal(err)
	}
	var replyIDs []int64
	for _, content := range []string{"one", "two", "three", "four"} {
		reply, appendErr := database.AppendMessage(ctx, domain.Message{
			WorkspaceID: workspace.ID, ParentMessageID: &root.ID,
			Role: domain.MessageAssistant, Content: content,
		})
		if appendErr != nil {
			t.Fatal(appendErr)
		}
		replyIDs = append(replyIDs, reply.ID)
	}

	newer, err := operator.ChatMessages(ctx, api.ChatPageRequest{ThreadRootID: root.ID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !newer.HasMore || len(newer.Messages) != 3 || newer.Messages[1].ID != replyIDs[2] ||
		newer.Messages[2].ID != replyIDs[3] || newer.NextBeforeID != replyIDs[2] {
		t.Fatalf("newer thread page = %#v", newer)
	}
	older, err := operator.ChatMessages(ctx, api.ChatPageRequest{
		ThreadRootID: root.ID, BeforeID: newer.NextBeforeID, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if older.HasMore || len(older.Messages) != 3 || older.Messages[1].ID != replyIDs[0] || older.Messages[2].ID != replyIDs[1] {
		t.Fatalf("older thread page = %#v", older)
	}
}
