package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestBuildTaskPacketIncludesScopePolicyAndResultContract(t *testing.T) {
	t.Parallel()
	packet, err := BuildTaskPacket(TaskPacketRequest{
		Task: domain.Task{
			ID:          "task-1",
			Title:       "Build the landing page",
			Purpose:     "Publish-ready local implementation",
			Why:         "The mission needs qualified traffic",
			Priority:    domain.PriorityHigh,
			WorkspaceID: "workspace-1",
			DefinitionOfDone: []domain.DefinitionItem{
				{Text: "Create the page"},
				{Text: "Build succeeds"},
			},
		},
		Mission: domain.Mission{
			Statement: "Grow qualified traffic",
			Context:   "Focus on high-intent demand.",
		},
		Policy: domain.Policy{
			Read:      "allowed in approved workspace",
			Work:      "allowed locally",
			Publish:   "requires approval",
			Dangerous: "requires approval",
		},
		Workspace: domain.Workspace{
			ID:            "workspace-1",
			Name:          "site",
			Path:          "/work/site",
			DefaultBranch: "main",
			Allowed:       true,
		},
		RelevantContext:  "The existing design system is authoritative.",
		ScriptData:       `[{"script_name":"Analytics summary","result":{"visitors":42}}]`,
		CharacterCharter: "Be calm and candid.",
		Datasets: []domain.Dataset{{
			ID: "dataset-1", Name: "Research", Description: "Verified findings", RowCount: 7,
			Schema: []domain.DatasetColumn{{Name: "source", Type: domain.DatasetString}}, UniqueKey: []string{"source"},
		}},
		Skills:           []string{"frontend conventions"},
		Scripts:          []string{"./scripts/check.sh"},
		RequiredEvidence: []string{"build output", "screenshot"},
	})
	if err != nil {
		t.Fatalf("BuildTaskPacket() error = %v", err)
	}
	for _, expected := range []string{
		"# Task", "Build the landing page", "Grow qualified traffic",
		"Focus on high-intent demand.", "/work/site", "Create the page",
		"Publish: requires approval", "./scripts/check.sh", "build output", "Be calm and candid.",
		"Configured Script Data", `"visitors":42`, "Do not search for or expose credentials",
		`"files_changed"`, `"verification"`, `"approval_needed": null`,
		"Workspace Database", `"id": "dataset-1"`, `"row_count": 7`, `"rows_file"`, "not a shell tool",
	} {
		if !strings.Contains(packet, expected) {
			t.Errorf("packet does not contain %q:\n%s", expected, packet)
		}
	}
}

func TestBuildTaskPacketRejectsUnapprovedOrMismatchedWorkspace(t *testing.T) {
	t.Parallel()
	base := TaskPacketRequest{
		Task:      domain.Task{Title: "Task", Purpose: "Purpose", WorkspaceID: "one"},
		Mission:   domain.Mission{Statement: "Mission"},
		Workspace: domain.Workspace{ID: "one", Path: "/tmp/work"},
	}
	if _, err := BuildTaskPacket(base); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("unapproved workspace error = %v", err)
	}
	base.Workspace.Allowed = true
	base.Workspace.ID = "two"
	if _, err := BuildTaskPacket(base); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched workspace error = %v", err)
	}
}

func TestBuildTaskPacketDelegatesBrowserQAToRegisteredHostVerifier(t *testing.T) {
	t.Parallel()
	packet, err := BuildTaskPacket(TaskPacketRequest{
		Task:              domain.Task{Title: "Verify the site in Playwright", Purpose: "Finish browser QA", WorkspaceID: "one"},
		Mission:           domain.Mission{Statement: "Ship a reliable site"},
		Workspace:         domain.Workspace{ID: "one", Name: "Site", Path: "/work/site", Allowed: true},
		Scripts:           []string{"Browser QA (read-only host browser verifier, id verifier-1)"},
		BrowserQARequired: true, BrowserVerifier: "Browser QA",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Registered Host Capabilities", "not inside Codex", "Browser Verification",
		"Keep the default workspace-write sandbox", "bootstrap_check_in", "Nabu will execute", "Do not invoke that verifier yourself",
	} {
		if !strings.Contains(packet, expected) {
			t.Errorf("browser QA packet missing %q:\n%s", expected, packet)
		}
	}
}

func TestBuildTaskPacketUsesReadyBrowserMCPBeforeHostFallback(t *testing.T) {
	t.Parallel()
	packet, err := BuildTaskPacket(TaskPacketRequest{
		Task:              domain.Task{Title: "Verify the responsive site", Purpose: "Run browser QA", WorkspaceID: "one"},
		Mission:           domain.Mission{Statement: "Ship a reliable site"},
		Workspace:         domain.Workspace{ID: "one", Path: "/work/site", Allowed: true},
		BrowserQARequired: true, BrowserMCP: "Chrome DevTools", BrowserVerifier: "Browser QA fallback",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ready browser MCP", "Chrome DevTools", "Use its browser tools", "observable results", "host-side fallback"} {
		if !strings.Contains(packet, expected) {
			t.Errorf("browser MCP packet missing %q:\n%s", expected, packet)
		}
	}
}

func TestBuildTaskPacketDefersMissingBrowserVerifierWithoutBlockingDurableWork(t *testing.T) {
	t.Parallel()
	packet, err := BuildTaskPacket(TaskPacketRequest{
		Task:              domain.Task{Title: "Run browser QA", Purpose: "Verify the page", WorkspaceID: "one"},
		Mission:           domain.Mission{Statement: "Ship a reliable site"},
		Workspace:         domain.Workspace{ID: "one", Path: "/work/site", Allowed: true},
		BrowserQARequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"No ready browser MCP", "status `completed`", "Browser QA as `not_run`", "without blocking dependent work", "sole meaningful outcome"} {
		if !strings.Contains(packet, expected) {
			t.Errorf("missing-verifier packet missing %q:\n%s", expected, packet)
		}
	}
}

func TestGenerateOrientationPacketIncludesDurableStateAndContract(t *testing.T) {
	t.Parallel()
	packet, err := GenerateOrientationPacket(OrientationPacketRequest{
		Mission: domain.Mission{
			Statement: "Grow product adoption",
			Context:   "Prioritize self-serve acquisition.",
		},
		BusinessContext: "The product serves B2B marketers.",
		DurableMemory:   "The owner prefers weekly summaries.", CharacterCharter: "Ask direct questions.",
		RecentCompleted: []domain.Task{{ID: "done-1", Title: "Ship page", Status: domain.TaskCompleted}},
		RecentFailures:  []domain.Task{{ID: "failed-1", Title: "Run import", Status: domain.TaskFailed}},
		CurrentQueue:    []domain.Task{{ID: "ready-1", Title: "Analyze funnel", Status: domain.TaskReady}},
		RecentEvents: []domain.Event{{
			ID:   42,
			Type: "user.steered",
			Data: json.RawMessage(`{"message":"focus on onboarding"}`),
		}},
		Workspaces: []domain.Workspace{
			{ID: "allowed", Name: "site", Path: "/work/site", Allowed: true},
			{ID: "denied", Name: "private", Path: "/private", Allowed: false},
		},
	})
	if err != nil {
		t.Fatalf("GenerateOrientationPacket() error = %v", err)
	}
	for _, expected := range []string{
		"Grow product adoption", "The product serves B2B marketers.", "The owner prefers weekly summaries.", "Ask direct questions.",
		"Recent Completed Work", "Ship page", "Recent Failures", "Run import",
		"Current Queue", "Analyze funnel", "Recent Meaningful Events", "user.steered",
		`"tasks"`, `"priority_updates"`, `"no_work_needed"`, "at most 3",
	} {
		if !strings.Contains(packet, expected) {
			t.Errorf("packet does not contain %q:\n%s", expected, packet)
		}
	}
	if strings.Contains(packet, "/private") {
		t.Fatalf("packet leaked unapproved workspace:\n%s", packet)
	}
}

func TestGenerateIdleStewardPacketExplainsBoundedProactiveReview(t *testing.T) {
	t.Parallel()
	packet, err := GenerateOrientationPacket(OrientationPacketRequest{
		Mission:          domain.Mission{Statement: "Grow useful adoption"},
		Workspaces:       []domain.Workspace{{ID: "workspace", Name: "Workspace", Path: "/work", Allowed: true}},
		IdleSteward:      true,
		StewardshipState: json.RawMessage(`{"workspace_inventory":{"empty":false},"schedules":[],"datasets":[{"id":"data-1","name":"Research"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"15 continuous idle minutes", "Create 1-3 tasks only", "Do not manufacture busywork", "Current Tools, Calendar, and Workspace State", "Research"} {
		if !strings.Contains(packet, expected) {
			t.Errorf("idle steward packet missing %q:\n%s", expected, packet)
		}
	}
}
