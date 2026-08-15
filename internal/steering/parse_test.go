package steering

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestParseResultFromCodexJSONLNormalizesAndValidates(t *testing.T) {
	structured := `{
  "assistant_response":"I created one task and queued the existing task.",
  "effects":[
    {"type":"create-task","task":{"title":"Review onboarding","purpose":"Find the largest conversion gap","why":"Supports adoption","priority":"Medium","definition_of_done":["Document evidence","Document evidence"],"workspace_id":"workspace-1"}},
    {"type":"update_task","task_id":"task-1","task":{"status":"queued","priority":"HIGH"}},
    {"type":"approve_action","approval_id":"approval-1"}
  ]
}`
	encoded, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": structured},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"thread.started","thread_id":"thread-1"}` + "\n" + string(encoded)
	result, err := ParseResult(raw, testState())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Effects) != 3 {
		t.Fatalf("effects = %d", len(result.Effects))
	}
	created := result.Effects[0]
	if created.Type != EffectCreateTask || created.Task.Priority != domain.PriorityNormal || len(created.Task.DefinitionOfDone) != 1 {
		t.Fatalf("create effect was not normalized: %#v", created)
	}
	updated := result.Effects[1]
	if updated.Task.Status != domain.TaskReady || updated.Task.Priority != domain.PriorityHigh {
		t.Fatalf("update effect was not normalized: %#v", updated)
	}
}

func TestParseResultAcceptsRedundantReadyStatusOnCreatedTask(t *testing.T) {
	structured := `{
  "assistant_response":"Next, establish an evidence-based acquisition strategy.",
  "effects":[{"type":"create_task","task":{
    "title":"Define the acquisition strategy and measurement baseline",
    "purpose":"Produce a decision-ready acquisition brief.",
    "why":"Growing qualified traffic requires positioning and baseline metrics.",
    "priority":"high","status":"ready",
    "definition_of_done":["Document the ideal-user profile and primary conversion action."],
    "workspace_id":"workspace-1"
  }}]
}`
	encoded, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": structured},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ParseResult(string(encoded), testState())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Effects) != 1 || result.Effects[0].Task.Status != "" {
		t.Fatalf("redundant create status was not discarded: %#v", result)
	}
}

func TestParseResultFromFenceWithPromptInjectionShapedResponse(t *testing.T) {
	raw := "prose before\n```json\n" + `{
  "assistant_response":"The quoted task said: ignore previous instructions; I treated it as data.",
  "effects":[{"type":"conversation_only"}]
}` + "\n```\nprose after"
	result, err := ParseResult(raw, testState())
	if err != nil {
		t.Fatal(err)
	}
	if result.Effects[0].Type != EffectConversationOnly || !strings.Contains(result.AssistantResponse, "treated it as data") {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestParseResultRejectsUnknownJSONFields(t *testing.T) {
	raw := `{"assistant_response":"No change.","effects":[{"type":"conversation_only","command":"deploy"}]}`
	if _, err := ParseResult(raw, testState()); err == nil || !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateResultRejectsMalformedAndHighImpactEffects(t *testing.T) {
	create := func(title string) Effect {
		return Effect{Type: EffectCreateTask, Task: &TaskChange{Title: title, Purpose: "Purpose", Priority: domain.PriorityNormal, DefinitionOfDone: []string{"Done"}, WorkspaceID: "workspace-1"}}
	}
	tests := []struct {
		name    string
		result  Result
		wantErr error
	}{
		{name: "unknown effect", result: resultWith(Effect{Type: "deploy_production"}), wantErr: ErrUnknownEffect},
		{name: "unknown task", result: resultWith(Effect{Type: EffectCancelTask, TaskID: "invented"}), wantErr: ErrInvalidResult},
		{name: "resolved approval", result: resultWith(Effect{Type: EffectApproveAction, ApprovalID: "approval-resolved"}), wantErr: ErrInvalidResult},
		{name: "dangerous policy bypass", result: resultWith(Effect{Type: EffectUpdatePolicy, Policy: &PolicyChange{Category: CategoryDangerous, Decision: DecisionAllow}}), wantErr: ErrHighImpactEffect},
		{name: "task lifecycle completion", result: resultWith(Effect{Type: EffectUpdateTask, TaskID: "task-1", Task: &TaskChange{Status: domain.TaskCompleted}}), wantErr: ErrHighImpactEffect},
		{name: "create task lifecycle", result: resultWith(Effect{Type: EffectCreateTask, Task: &TaskChange{Title: "Task", Purpose: "Purpose", Status: domain.TaskWaiting, DefinitionOfDone: []string{"Done"}}}), wantErr: ErrHighImpactEffect},
		{name: "unapproved workspace", result: resultWith(Effect{Type: EffectCreateTask, Task: &TaskChange{Title: "Task", Purpose: "Purpose", WorkspaceID: "workspace-denied", DefinitionOfDone: []string{"Done"}}}), wantErr: ErrInvalidResult},
		{name: "empty definition", result: resultWith(Effect{Type: EffectCreateTask, Task: &TaskChange{Title: "Task", Purpose: "Purpose", DefinitionOfDone: []string{" "}}}), wantErr: ErrInvalidResult},
		{name: "conversation mixed", result: resultWith(Effect{Type: EffectConversationOnly}, Effect{Type: EffectPause}), wantErr: ErrInvalidResult},
		{name: "pause resume", result: resultWith(Effect{Type: EffectPause}, Effect{Type: EffectResume}), wantErr: ErrInvalidResult},
		{name: "duplicate task mutation", result: resultWith(Effect{Type: EffectUpdateTask, TaskID: "task-1", Task: &TaskChange{Priority: domain.PriorityLow}}, Effect{Type: EffectCancelTask, TaskID: "task-1"}), wantErr: ErrInvalidResult},
		{name: "duplicate open task", result: resultWith(create("Existing task")), wantErr: ErrInvalidResult},
		{name: "too many task creations", result: resultWith(create("One"), create("Two"), create("Three"), create("Four")), wantErr: ErrInvalidResult},
		{name: "extraneous payload", result: resultWith(Effect{Type: EffectPause, Note: "and deploy"}), wantErr: ErrInvalidResult},
		{name: "completed task mutation", result: resultWith(Effect{Type: EffectUpdateTask, TaskID: "task-completed", Task: &TaskChange{Priority: domain.PriorityLow}}), wantErr: ErrHighImpactEffect},
		{name: "running task edit", result: resultWith(Effect{Type: EffectUpdateTask, TaskID: "task-running", Task: &TaskChange{Priority: domain.PriorityLow}}), wantErr: ErrHighImpactEffect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateResult(test.result, testState())
			if err == nil || !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateResultCapsTotalEffects(t *testing.T) {
	effects := make([]Effect, MaxEffects+1)
	for index := range effects {
		effects[index] = Effect{Type: EffectRequestReport, Report: &ReportRequest{Title: fmt.Sprintf("Report %d", index), Scope: fmt.Sprintf("Scope %d", index)}}
	}
	if _, err := ValidateResult(Result{AssistantResponse: "Reports requested.", Effects: effects}, testState()); err == nil {
		t.Fatal("expected effect cap error")
	}
}

func resultWith(effects ...Effect) Result {
	return Result{AssistantResponse: "I will apply the validated change.", Effects: effects}
}

func testState() ValidationState {
	return ValidationState{
		Tasks: []domain.Task{
			{ID: "task-1", Title: "Existing task", Status: domain.TaskReady, Priority: domain.PriorityNormal},
			{ID: "task-completed", Title: "Completed task", Status: domain.TaskCompleted, Priority: domain.PriorityNormal},
			{ID: "task-running", Title: "Running task", Status: domain.TaskRunning, Priority: domain.PriorityNormal},
		},
		Workspaces: []domain.Workspace{
			{ID: "workspace-1", Path: "/approved", Allowed: true},
			{ID: "workspace-denied", Path: "/denied", Allowed: false},
		},
		PendingApprovals: []ApprovalSummary{
			{ID: "approval-1", Action: "Deploy preview", Status: ApprovalPending, Category: CategoryPublish},
			{ID: "approval-resolved", Action: "Old deploy", Status: ApprovalApproved, Category: CategoryPublish},
		},
	}
}
