package operator

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestParseCodexModelCatalogFiltersHiddenAndInvalidEfforts(t *testing.T) {
	models, err := parseCodexModelCatalog([]byte(`{"models":[
        {"slug":"gpt-visible","display_name":"Visible","visibility":"list","default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low"},{"effort":"ultra"},{"effort":"invented"}]},
        {"slug":"gpt-hidden","display_name":"Hidden","visibility":"hide","supported_reasoning_levels":[{"effort":"high"}]}
    ]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-visible" || len(models[0].SupportedReasoningEfforts) != 2 || models[0].SupportedReasoningEfforts[1] != "ultra" {
		t.Fatalf("models = %#v", models)
	}
}

func TestCalendarOnlyIncludesActiveWorkspaceAndRange(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	planned := from.Add(48 * time.Hour)
	inside, err := database.CreateTask(ctx, domain.Task{WorkspaceID: workspace.ID, Title: "Planned research", Status: domain.TaskIdea, Priority: domain.PriorityNormal, PlannedAt: &planned})
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateWorkspace(ctx, domain.Workspace{Name: "Other", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateTask(ctx, domain.Task{WorkspaceID: other.ID, Title: "Private other work", Status: domain.TaskIdea, Priority: domain.PriorityNormal, PlannedAt: &planned}); err != nil {
		t.Fatal(err)
	}
	items, err := operator.Calendar(ctx, from, from.Add(7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != inside.ID || !strings.Contains(items[0].Href, inside.ID) {
		t.Fatalf("calendar items = %#v", items)
	}
}

func TestCalendarExpandsRecurringScheduleWithinRange(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	next := from.Add(12 * time.Hour)
	if _, err := database.CreateSchedule(ctx, domain.Schedule{
		WorkspaceID: workspace.ID, Name: "Twice daily", Enabled: true, Kind: domain.ScheduleOrient,
		IntervalSeconds: 12 * 60 * 60, NextRunAt: &next,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := operator.Calendar(ctx, from, from.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var planned int
	for _, item := range items {
		if item.Kind == "schedule" && item.Status == "planned" {
			planned++
		}
	}
	if planned != 3 {
		t.Fatalf("planned schedule occurrences = %d, items = %#v", planned, items)
	}
}

func TestCalendarIncludesDatedPlanMilestonesWithoutDuplicatingLinkedTasks(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	planned := from.Add(72 * time.Hour)
	task, err := database.CreateTask(ctx, domain.Task{WorkspaceID: workspace.ID, Title: "Linked task", Status: domain.TaskIdea, Priority: domain.PriorityNormal, PlannedAt: &planned})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreatePlan(ctx, domain.Plan{
		WorkspaceID: workspace.ID, Title: "Two-week growth plan", Objective: "Reach the next measurable outcome", Status: domain.PlanActive,
		Items: []domain.PlanItem{
			{Kind: domain.PlanItemMilestone, Title: "Review acquisition signal", PlannedAt: &planned, Status: domain.PlanItemAccepted},
			{Kind: domain.PlanItemTask, Title: task.Title, PlannedAt: &planned, TaskID: task.ID, Status: domain.PlanItemAccepted},
		},
	}); err != nil {
		t.Fatal(err)
	}
	items, err := operator.Calendar(ctx, from, from.Add(7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var tasks, milestones int
	for _, item := range items {
		switch item.Kind {
		case "task":
			tasks++
		case "milestone":
			milestones++
			if item.Title != "Review acquisition signal" || item.Status != "planned" {
				t.Fatalf("milestone = %#v", item)
			}
		}
	}
	if tasks != 1 || milestones != 1 {
		t.Fatalf("calendar items = %#v", items)
	}
}

func TestScopeIconRoundTripAndValidation(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := operator.SaveScopeIcon(ctx, workspace.ID, raw, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if updated.IconURL == "" || updated.IconPath == "" {
		t.Fatalf("workspace image view = %#v", updated)
	}
	icon, err := operator.ScopeIcon(ctx, workspace.ID)
	if err != nil || icon.ContentType != "image/png" || icon.ETag == "" {
		t.Fatalf("served image = %#v, %v", icon, err)
	}
	if _, err := operator.SaveScopeIcon(ctx, workspace.ID, []byte("not an image"), "text/plain"); !errors.Is(err, api.ErrInvalid) {
		t.Fatalf("invalid image error = %v", err)
	}
	path := updated.IconPath
	if err := operator.DeleteScopeIcon(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted image stat = %v", err)
	}
	loaded, err := database.GetWorkspace(ctx, workspace.ID)
	if err != nil || loaded.IconPath != "" {
		t.Fatalf("workspace after image deletion = %#v, %v", loaded, err)
	}
}

func TestCreateTaskWithFuturePlanStaysOutOfReadyQueue(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	future := time.Now().UTC().Add(24 * time.Hour)
	task, err := operator.CreateTask(context.Background(), api.TaskCreate{
		Title: "Future research", Purpose: "Research later", Why: "Planned work", Priority: domain.PriorityNormal,
		DefinitionOfDone: []string{"Research saved"}, WorkspaceID: workspace.ID, PlannedAt: &future,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskIdea {
		t.Fatalf("future task status = %s, want idea", task.Status)
	}
	if _, err := database.ClaimNextReadyTaskForWorkspace(context.Background(), workspace.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("future planned claim error = %v, want not found", err)
	}
}
