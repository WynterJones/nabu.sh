package operator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestApplyTaskLocalAppsRegistersWorkspaceAppIdempotently(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	appDirectory := filepath.Join(workspace.Path, "repos", "toolbox")
	if err := os.MkdirAll(appDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	result := domain.RunResult{LocalApps: []domain.LocalAppRegistration{{
		Name: "Toolbox", Description: "Local utility", Directory: "repos/toolbox",
		Command: []string{"npm", "run", "dev"}, Port: 4173, HealthPath: "/",
	}}}
	if err := service.applyTaskLocalApps(context.Background(), domain.Task{ID: "task-1", WorkspaceID: workspace.ID}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.LocalApps[0].Applied || result.LocalApps[0].ID == "" {
		t.Fatalf("registration evidence = %#v", result.LocalApps[0])
	}
	apps, err := database.ListLocalApps(context.Background(), store.LocalAppFilter{WorkspaceID: workspace.ID})
	if err != nil || len(apps) != 1 || apps[0].Directory != "repos/toolbox" {
		t.Fatalf("apps = %#v, err=%v", apps, err)
	}
	result.LocalApps[0].ID, result.LocalApps[0].Applied = "", false
	if err := service.applyTaskLocalApps(context.Background(), domain.Task{ID: "task-1", WorkspaceID: workspace.ID}, &result); err != nil {
		t.Fatal(err)
	}
	apps, _ = database.ListLocalApps(context.Background(), store.LocalAppFilter{WorkspaceID: workspace.ID})
	if len(apps) != 1 || !result.LocalApps[0].Applied {
		t.Fatalf("idempotent apps = %#v registration=%#v", apps, result.LocalApps[0])
	}
}
