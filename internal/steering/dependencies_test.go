package steering

import (
	"errors"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestTaskDependenciesRequireAuthoritativeSameWorkspaceAcyclicIDs(t *testing.T) {
	state := ValidationState{
		Workspaces: []domain.Workspace{{ID: "workspace-one", Allowed: true}},
		Tasks: []domain.Task{
			{ID: "foundation", WorkspaceID: "workspace-one", Title: "Foundation", Status: domain.TaskReady},
			{ID: "dependent", WorkspaceID: "workspace-one", Title: "Dependent", Status: domain.TaskReady, DependsOnTaskIDs: []string{"foundation"}},
			{ID: "foreign", WorkspaceID: "workspace-two", Title: "Foreign", Status: domain.TaskReady},
		},
	}
	valid := `{"assistant_response":"Queued the next step.","effects":[{"type":"create_task","task":{"title":"After foundation","purpose":"Build on the prerequisite","priority":"normal","definition_of_done":["Done"],"workspace_id":"workspace-one","depends_on_task_ids":["foundation"]}}]}`
	if _, err := ParseResult(valid, state); err != nil {
		t.Fatalf("valid dependency rejected: %v", err)
	}
	for name, raw := range map[string]string{
		"invented": `{"assistant_response":"x","effects":[{"type":"create_task","task":{"title":"x","purpose":"x","priority":"normal","definition_of_done":["x"],"depends_on_task_ids":["invented"]}}]}`,
		"foreign":  `{"assistant_response":"x","effects":[{"type":"create_task","task":{"title":"x","purpose":"x","priority":"normal","definition_of_done":["x"],"workspace_id":"workspace-one","depends_on_task_ids":["foreign"]}}]}`,
		"cycle":    `{"assistant_response":"x","effects":[{"type":"update_task","task_id":"foundation","task":{"depends_on_task_ids":["dependent"]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseResult(raw, state); err == nil || !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("unsafe dependency accepted: %v", err)
			}
		})
	}
}

func TestBuildRepairPacketContainsValidationErrorAndContract(t *testing.T) {
	packet := BuildRepairPacket("original", `{"bad":true}`, errors.New("unknown task ID"))
	for _, expected := range []string{"previous response was rejected before any effects were applied", "unknown task ID", "assistant_response", "Do not loosen approval boundaries"} {
		if !containsText(packet, expected) {
			t.Fatalf("repair packet missing %q: %s", expected, packet)
		}
	}
}

func containsText(value, expected string) bool {
	for index := 0; index+len(expected) <= len(value); index++ {
		if value[index:index+len(expected)] == expected {
			return true
		}
	}
	return false
}
