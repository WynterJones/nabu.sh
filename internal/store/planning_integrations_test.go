package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestTaskPlannedAtAndWorkspaceIconRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{
		ID: "planning-workspace", Name: "Planning", Path: "/planning", IconPath: "/private/icons/planning.png", Allowed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.IconPath != "/private/icons/planning.png" {
		t.Fatalf("created workspace icon = %q", workspace.IconPath)
	}
	loadedWorkspace, err := s.GetWorkspace(ctx, workspace.ID)
	if err != nil || loadedWorkspace.IconPath != workspace.IconPath {
		t.Fatalf("workspace icon roundtrip = %#v, %v", loadedWorkspace, err)
	}
	loadedWorkspace.IconPath = "/private/icons/revised.png"
	if err := s.UpdateWorkspace(ctx, loadedWorkspace); err != nil {
		t.Fatal(err)
	}
	loadedWorkspace, err = s.GetWorkspace(ctx, workspace.ID)
	if err != nil || loadedWorkspace.IconPath != "/private/icons/revised.png" {
		t.Fatalf("updated workspace icon = %#v, %v", loadedWorkspace, err)
	}
	loadedWorkspace.IconURL = "/api/scopes/" + workspace.ID + "/icon"
	encoded, err := json.Marshal(loadedWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || jsonContainsKey(t, encoded, "icon_path") {
		t.Fatalf("private icon path leaked through JSON: %s", encoded)
	}
	if !jsonContainsKey(t, encoded, "icon_url") {
		t.Fatalf("computed icon URL was omitted from JSON: %s", encoded)
	}

	planned := time.Date(2026, 9, 15, 14, 30, 0, 123, time.UTC)
	task, err := s.CreateTask(ctx, domain.Task{
		ID: "planned-task", WorkspaceID: workspace.ID, Title: "Publish the quarterly guide",
		Status: domain.TaskIdea, Priority: domain.PriorityNormal, PlannedAt: &planned,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetTask(ctx, task.ID)
	if err != nil || loaded.PlannedAt == nil || !loaded.PlannedAt.Equal(planned) {
		t.Fatalf("planned task roundtrip = %#v, %v", loaded, err)
	}
	from, to := planned.Add(-time.Hour), planned.Add(time.Hour)
	listed, err := s.ListTasks(ctx, TaskFilter{WorkspaceID: workspace.ID, PlannedFrom: &from, PlannedTo: &to})
	if err != nil || len(listed) != 1 || listed[0].ID != task.ID {
		t.Fatalf("planned task filter = %#v, %v", listed, err)
	}
	loaded.PlannedAt = nil
	if err := s.UpdateTask(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	unplanned, err := s.ListTasks(ctx, TaskFilter{WorkspaceID: workspace.ID, Unplanned: true})
	if err != nil || len(unplanned) != 1 || unplanned[0].PlannedAt != nil {
		t.Fatalf("unplanned task filter = %#v, %v", unplanned, err)
	}
}

func TestIntegrationRegistryIsWorkspaceScoped(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "integration-w1", Name: "One", Path: "/integration-one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "integration-w2", Name: "Two", Path: "/integration-two"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateIntegration(ctx, domain.Integration{
		WorkspaceID: w1.ID,
		Name:        " Search Console ",
		Provider:    " google ",
		Description: " Generated read-only adapter. ",
		Status:      domain.IntegrationNeedsCredentials,
		Manifest:    json.RawMessage(`{"version":1,"actions":["query"]}`),
		CredentialRequirements: []domain.CredentialRequirement{
			{Name: " client_id ", Description: " OAuth client identifier ", Required: true},
			{Name: "client_secret", Description: "OAuth client secret", Required: true},
		},
		AllowedHosts: []string{"searchconsole.googleapis.com", " searchconsole.googleapis.com ", "oauth2.googleapis.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Search Console" || created.Provider != "google" || len(created.AllowedHosts) != 2 {
		t.Fatalf("normalized integration = %#v", created)
	}
	got, err := s.GetIntegration(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkspaceID != w1.ID || !reflect.DeepEqual(got.AllowedHosts, created.AllowedHosts) ||
		!reflect.DeepEqual(got.CredentialRequirements, created.CredentialRequirements) || string(got.Manifest) != string(created.Manifest) {
		t.Fatalf("integration roundtrip = %#v", got)
	}

	other, err := s.CreateIntegration(ctx, domain.Integration{
		WorkspaceID: w2.ID, Name: "Analytics", Provider: "google", Status: domain.IntegrationDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	activeList, err := s.ListIntegrations(ctx, IntegrationFilter{})
	if err != nil || len(activeList) != 1 || activeList[0].ID != created.ID {
		t.Fatalf("active integration list = %#v, %v", activeList, err)
	}
	if _, err := s.GetIntegration(ctx, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace GetIntegration error = %v, want not found", err)
	}
	filtered, err := s.ListIntegrations(ctx, IntegrationFilter{
		WorkspaceID: w2.ID, Provider: "google", Statuses: []domain.IntegrationStatus{domain.IntegrationDraft},
	})
	if err != nil || len(filtered) != 1 || filtered[0].ID != other.ID {
		t.Fatalf("filtered integrations = %#v, %v", filtered, err)
	}

	verified := time.Date(2026, 8, 12, 19, 0, 0, 0, time.UTC)
	got.Status, got.LastVerifiedAt, got.LastError = domain.IntegrationReady, &verified, ""
	if err := s.UpdateIntegration(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetIntegration(ctx, got.ID)
	if err != nil || got.Status != domain.IntegrationReady || got.LastVerifiedAt == nil || !got.LastVerifiedAt.Equal(verified) {
		t.Fatalf("updated integration = %#v, %v", got, err)
	}
	if err := s.UpdateIntegration(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace UpdateIntegration error = %v, want not found", err)
	}
	if err := s.DeleteIntegration(ctx, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace DeleteIntegration error = %v, want not found", err)
	}
	if err := s.DeleteIntegration(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIntegration(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted integration error = %v, want not found", err)
	}

	if _, err := s.CreateIntegration(ctx, domain.Integration{
		WorkspaceID: w1.ID, Name: "Broken", Provider: "test", Manifest: json.RawMessage(`{"broken"`),
	}); err == nil {
		t.Fatal("invalid integration manifest was accepted")
	}
	if _, err := s.CreateIntegration(ctx, domain.Integration{
		WorkspaceID: w1.ID, Name: "Duplicate credentials", Provider: "test",
		CredentialRequirements: []domain.CredentialRequirement{{Name: "token"}, {Name: "token"}},
	}); err == nil {
		t.Fatal("duplicate credential metadata was accepted")
	}
}

func TestPlansPersistItemsTransactionallyWithoutCreatingLiveTasks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "plan-w1", Name: "One", Path: "/plan-one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "plan-w2", Name: "Two", Path: "/plan-two"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, domain.Task{
		WorkspaceID: w1.ID, Title: "Accepted first step", Status: domain.TaskIdea, Priority: domain.PriorityHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherTask, err := s.CreateTask(ctx, domain.Task{
		WorkspaceID: w2.ID, Title: "Other business task", Status: domain.TaskIdea, Priority: domain.PriorityNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	nextMonth := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	plan, err := s.CreatePlan(ctx, domain.Plan{
		WorkspaceID: w1.ID, Title: "Six-month product launch", Objective: "Sequence work without flooding the live queue.",
		Items: []domain.PlanItem{
			{Kind: domain.PlanItemTask, Title: "Validate the audience", Purpose: "Reduce positioning risk", TaskID: task.ID},
			{Kind: domain.PlanItemMilestone, Title: "Beta launch", PlannedAt: &nextMonth},
			{Kind: domain.PlanItemSchedule, Title: "Monthly review", Cadence: &domain.ScheduleCadence{Expression: "0 9 1 * *"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != domain.PlanProposed || len(plan.Items) != 3 || plan.Items[0].Position != 0 || plan.Items[2].Position != 2 {
		t.Fatalf("created plan = %#v", plan)
	}
	var taskCount int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE workspace_id = ?", w1.ID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 {
		t.Fatalf("plan proposal created live tasks: count = %d", taskCount)
	}
	got, err := s.GetPlan(ctx, plan.ID)
	if err != nil || !reflect.DeepEqual(got, plan) {
		t.Fatalf("plan roundtrip = %#v, want %#v, err %v", got, plan, err)
	}
	if _, err := s.GetPlanForWorkspace(ctx, w2.ID, plan.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace GetPlan error = %v, want not found", err)
	}
	listed, err := s.ListPlans(ctx, PlanFilter{Statuses: []domain.PlanStatus{domain.PlanProposed}})
	if err != nil || len(listed) != 1 || listed[0].ID != plan.ID {
		t.Fatalf("listed plans = %#v, %v", listed, err)
	}

	invalid := got
	invalid.Status = domain.PlanActive
	invalid.Items = []domain.PlanItem{{
		Kind: domain.PlanItemTask, Title: "Cross-scope leak", TaskID: otherTask.ID, Status: domain.PlanItemAccepted,
	}}
	if err := s.UpdatePlan(ctx, invalid); err == nil {
		t.Fatal("cross-workspace plan item link was accepted")
	}
	unchanged, err := s.GetPlan(ctx, plan.ID)
	if err != nil || !reflect.DeepEqual(unchanged, got) {
		t.Fatalf("failed plan update was not atomic: %#v, %v", unchanged, err)
	}

	got.Status = domain.PlanActive
	got.Items = []domain.PlanItem{
		{Kind: domain.PlanItemMilestone, Title: "First milestone", Status: domain.PlanItemAccepted},
		{Kind: domain.PlanItemTask, Title: "Deferred campaign", Status: domain.PlanItemSkipped},
	}
	if err := s.UpdatePlan(ctx, got); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetPlan(ctx, plan.ID)
	if err != nil || updated.Status != domain.PlanActive || len(updated.Items) != 2 ||
		updated.Items[0].Status != domain.PlanItemAccepted || updated.Items[1].Position != 1 {
		t.Fatalf("updated plan = %#v, %v", updated, err)
	}
	if err := s.DeletePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	var itemCount int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM plan_items WHERE plan_id = ?", plan.ID).Scan(&itemCount); err != nil {
		t.Fatal(err)
	}
	if itemCount != 0 {
		t.Fatalf("plan item cascade count = %d", itemCount)
	}
}

func jsonContainsKey(t *testing.T, encoded []byte, key string) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	_, exists := value[key]
	return exists
}
