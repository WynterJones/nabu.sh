package store

import (
	"context"
	"errors"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestLocalAppsAreWorkspaceScopedAndDurable(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	first, err := database.CreateWorkspace(ctx, domain.Workspace{Name: "First", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateWorkspace(ctx, domain.Workspace{Name: "Second", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	created, err := database.CreateLocalApp(ctx, domain.LocalApp{
		WorkspaceID: first.ID, Name: "Website", Description: "Local preview", Directory: "repos/site",
		Command: []string{"npm", "run", "dev"}, Port: 4173, HealthPath: "/health",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || len(created.Command) != 3 {
		t.Fatalf("created local app = %#v", created)
	}
	apps, err := database.ListLocalApps(ctx, LocalAppFilter{WorkspaceID: first.ID})
	if err != nil || len(apps) != 1 || apps[0].ID != created.ID {
		t.Fatalf("first workspace apps = %#v, %v", apps, err)
	}
	if _, err := database.GetLocalAppForWorkspace(ctx, second.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace get = %v", err)
	}
	created.Description = "Updated preview"
	created.Command = []string{"npm", "run", "preview"}
	if err := database.UpdateLocalApp(ctx, created); err != nil {
		t.Fatal(err)
	}
	updated, err := database.GetLocalAppForWorkspace(ctx, first.ID, created.ID)
	if err != nil || updated.Description != "Updated preview" || updated.Command[2] != "preview" {
		t.Fatalf("updated local app = %#v, %v", updated, err)
	}
	if err := database.DeleteLocalAppForWorkspace(ctx, first.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetLocalApp(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted local app = %v", err)
	}
}

func TestLocalAppValidationAndUniqueness(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	workspace, err := database.CreateWorkspace(ctx, domain.Workspace{Name: "Workspace", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	base := domain.LocalApp{WorkspaceID: workspace.ID, Name: "App", Directory: "repos/app", Command: []string{"npm", "run", "dev"}, Port: 4100, HealthPath: "/"}
	if _, err := database.CreateLocalApp(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.ID, base.Name = "", "Duplicate port"
	if _, err := database.CreateLocalApp(ctx, base); err == nil {
		t.Fatal("duplicate workspace port unexpectedly succeeded")
	}
	base.Name, base.Port = "Bad port", 80
	if _, err := database.CreateLocalApp(ctx, base); err == nil {
		t.Fatal("privileged port unexpectedly succeeded")
	}
}
