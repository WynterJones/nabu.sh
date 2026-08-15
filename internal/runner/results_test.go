package runner

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestParseRunResultFromProseAndCodeFence(t *testing.T) {
	t.Parallel()
	raw := "Work finished.\n```json\n" + `{
  "status": "SUCCESS",
  "summary": "  Created and verified the page.  ",
  "files_changed": [" page.tsx ", "page.tsx"],
  "verification": [{"name":" build ","status":"PASSED","details":" ok "}],
  "artifacts": [],
  "uncertainties": null,
  "approval_needed": ""
}` + "\n```\nThat is all."
	result, err := ParseRunResult(raw)
	if err != nil {
		t.Fatalf("ParseRunResult() error = %v", err)
	}
	if result.Status != "completed" || result.Summary != "Created and verified the page." {
		t.Fatalf("result = %#v", result)
	}
	if len(result.FilesChanged) != 1 || result.FilesChanged[0] != "page.tsx" {
		t.Fatalf("files changed = %#v", result.FilesChanged)
	}
	if len(result.Uncertainties) != 0 || result.Uncertainties == nil {
		t.Fatalf("uncertainties = %#v, want non-nil empty", result.Uncertainties)
	}
	if result.ApprovalNeeded != nil {
		t.Fatalf("approval needed = %v, want nil", result.ApprovalNeeded)
	}
	if result.Verification[0].Status != "passed" || result.Verification[0].Name != "build" {
		t.Fatalf("verification = %#v", result.Verification)
	}
}

func TestParseRunResultFromCodexJSONLAgentMessage(t *testing.T) {
	t.Parallel()
	finalText := "Here is the required result:\n```json\n" + `{
  "status":"needs-approval",
  "summary":"Prepared the deploy.",
  "files_changed":[],
  "verification":[],
  "artifacts":[],
  "uncertainties":[],
  "approval_needed":"Deploy production"
}` + "\n```"
	event, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "agent_message",
			"text": finalText,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"thread.started","thread_id":"thread-1"}` + "\n" + string(event) + "\n" + `{"type":"turn.completed"}`
	result, err := ParseRunResult(raw)
	if err != nil {
		t.Fatalf("ParseRunResult() error = %v\nraw: %s", err, raw)
	}
	if result.Status != "needs_approval" || result.ApprovalNeeded == nil || *result.ApprovalNeeded != "Deploy production" {
		t.Fatalf("result = %#v", result)
	}
}

func TestParseRunResultRejectsMissingOrInvalidContract(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"plain prose only",
		`{"status":"mystery","summary":"Nope"}`,
		`{"status":"completed","summary":"   "}`,
	} {
		if _, err := ParseRunResult(raw); err == nil {
			t.Errorf("ParseRunResult(%q) error = nil", raw)
		}
	}
}

func TestParseRunResultAcceptsBoundedLocalAppRegistration(t *testing.T) {
	raw := `{"status":"completed","summary":"Built the app","files_changed":["repos/toolbox/package.json"],"verification":[{"name":"build","status":"passed"}],"artifacts":[],"uncertainties":[],"local_apps":[{"name":"Toolbox","directory":"repos/toolbox","command":["npm","run","dev"],"port":4173,"health_path":"/"}]}`
	result, err := ParseRunResult(raw)
	if err != nil || len(result.LocalApps) != 1 || result.LocalApps[0].Directory != "repos/toolbox" || result.LocalApps[0].Applied {
		t.Fatalf("result = %#v, err=%v", result, err)
	}
}

func TestParseRunResultPreservesSpecificLocalAppValidationErrorFromJSONL(t *testing.T) {
	t.Parallel()
	result := `{"status":"completed","summary":"Built the app","files_changed":["package.json"],"verification":[{"name":"build","status":"passed"}],"artifacts":[],"uncertainties":[],"local_apps":[{"name":"Root app","directory":".","command":["npm","run","dev"],"port":4173,"health_path":"/"}]}`
	event, err := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": result}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseRunResult(string(event) + "\n" + `{"type":"turn.completed","usage":{"input_tokens":1}}`)
	if err == nil || !strings.Contains(err.Error(), "repos/<app-folder>") {
		t.Fatalf("ParseRunResult() error = %v, want repos folder explanation", err)
	}
}

func TestParseRunResultNormalizesDefinitionOutcomes(t *testing.T) {
	t.Parallel()
	raw := `{"status":"failed","summary":"One criterion remains blocked.","definition_of_done":[{"text":" Build passes ","status":"PASSED","details":" exit 0 "},{"text":"Browser check passes","status":"FAILED","details":"browser unavailable"}]}`
	result, err := ParseRunResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DefinitionDone) != 2 || result.DefinitionDone[0].Text != "Build passes" || result.DefinitionDone[0].Status != "passed" || result.DefinitionDone[0].Details != "exit 0" || result.DefinitionDone[1].Status != "failed" {
		t.Fatalf("definition outcomes = %#v", result.DefinitionDone)
	}
	if _, err := ParseRunResult(`{"status":"failed","summary":"Invalid outcome.","definition_of_done":[{"text":"Check","status":"unknown"}]}`); err == nil {
		t.Fatal("invalid definition outcome status was accepted")
	}
}

func TestParseRunResultRejectsLocalAppsOutsideRepos(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{".", "app", "repos", "repos/../app"} {
		raw := `{"status":"completed","summary":"Built","local_apps":[{"name":"App","directory":` + strconv.Quote(directory) + `,"command":["npm","run","dev"],"port":4173}]}`
		if _, err := ParseRunResult(raw); err == nil || !strings.Contains(err.Error(), "repos/<app-folder>") {
			t.Errorf("ParseRunResult(%q) error = %v", directory, err)
		}
	}
}

func TestParseOrientationResultSanitizesTasksAndPriorityUpdates(t *testing.T) {
	t.Parallel()
	queue := []domain.Task{
		{ID: "q1", Title: "Audit Analytics!", Priority: domain.PriorityNormal, Status: domain.TaskReady},
		{ID: "q2", Title: "Fix signup", Priority: domain.PriorityHigh, Status: domain.TaskReady},
	}
	raw := `Orientation follows:
{
  "summary": "Focus on acquisition gaps.",
  "tasks": [
    {"title":"audit analytics", "purpose":"Duplicate", "why":"same", "priority":"high", "definition_of_done":[]},
    {"title":"Create SEO page", "purpose":"Capture demand", "why":"Traffic", "priority":"HIGH", "definition_of_done":[{"text":"Build passes","completed":true}]},
    {"title":"Create SEO-page!", "purpose":"Duplicate proposal", "why":"Traffic", "priority":"low", "definition_of_done":[]},
    {"title":"Improve onboarding", "purpose":"Activation", "why":"Adoption", "priority":"normal", "definition_of_done":[]},
    {"title":"Review pricing", "purpose":"Conversion", "why":"Revenue", "priority":"low", "definition_of_done":[]},
    {"title":"This fourth task is dropped", "purpose":"Backlog", "why":"No", "priority":"high", "definition_of_done":[]}
  ],
  "priority_updates": [
    {"task_id":"q1", "priority":"high"},
    {"task_id":"q1", "priority":"low"},
    {"task_id":"q2", "priority":"HIGH"},
    {"task_id":"missing", "priority":"low"}
  ],
  "no_work_needed": false
}
Done.`
	result, err := ParseOrientationResult(raw, queue)
	if err != nil {
		t.Fatalf("ParseOrientationResult() error = %v", err)
	}
	if len(result.Tasks) != MaxOrientationTasks {
		t.Fatalf("task count = %d, want %d: %#v", len(result.Tasks), MaxOrientationTasks, result.Tasks)
	}
	for _, duplicate := range []string{"audit analytics", "Create SEO-page!", "This fourth task is dropped"} {
		for _, task := range result.Tasks {
			if task.Title == duplicate {
				t.Fatalf("duplicate/capped task %q was retained: %#v", duplicate, result.Tasks)
			}
		}
	}
	if result.Tasks[0].Priority != domain.PriorityHigh || result.Tasks[0].DefinitionOfDone[0].Completed {
		t.Fatalf("first task was not normalized: %#v", result.Tasks[0])
	}
	if len(result.PriorityUpdates) != 1 || result.PriorityUpdates[0].TaskID != "q1" || result.PriorityUpdates[0].Priority != domain.PriorityHigh {
		t.Fatalf("priority updates = %#v", result.PriorityUpdates)
	}
}

func TestSanitizeOrientationResultAllowsNoWork(t *testing.T) {
	t.Parallel()
	result, err := SanitizeOrientationResult(domain.OrientationResult{
		Summary:      "The current queue is sufficient.",
		NoWorkNeeded: true,
		Tasks: []domain.OrientationTask{{
			Title:    "Should not be created",
			Purpose:  "Conflicts with no-work",
			Priority: domain.PriorityHigh,
		}},
	}, nil)
	if err != nil {
		t.Fatalf("SanitizeOrientationResult() error = %v", err)
	}
	if !result.NoWorkNeeded || len(result.Tasks) != 0 || result.Tasks == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestSanitizeOrientationResultRejectsInvalidPriority(t *testing.T) {
	t.Parallel()
	_, err := SanitizeOrientationResult(domain.OrientationResult{
		Summary: "Has an unsafe priority.",
		Tasks: []domain.OrientationTask{{
			Title:    "Do everything",
			Purpose:  "Unbounded",
			Priority: "urgent",
		}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid priority") {
		t.Fatalf("SanitizeOrientationResult() error = %v", err)
	}
}
