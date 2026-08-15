package operator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestWorkspaceOutputsReturnsOnlyUsableActiveWorkspaceArtifacts(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	path := filepath.Join(workspace.Path, "documents", "launch-plan.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Launch plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateTask(context.Background(), domain.Task{
		WorkspaceID: workspace.ID, Title: "Prepare launch plan", Status: domain.TaskCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := database.CreateArtifact(context.Background(), domain.Artifact{
		TaskID: task.ID, Kind: "site", Name: "Launch preview", Path: "documents/launch-plan.md", URL: "https://example.com/preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateArtifact(context.Background(), domain.Artifact{
		TaskID: task.ID, Kind: "file", Name: "Missing", Path: "documents/missing.md",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateArtifact(context.Background(), domain.Artifact{
		TaskID: task.ID, Kind: "url", Name: "Unsafe", URL: "file:///etc/passwd",
	}); err != nil {
		t.Fatal(err)
	}

	view, err := service.WorkspaceOutputs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 1 {
		t.Fatalf("outputs = %#v", view.Items)
	}
	item := view.Items[0]
	if item.ID != artifact.ID || item.Path != "documents/launch-plan.md" || item.URL != "https://example.com/preview" ||
		item.FileKind != "text" || !item.Editable || item.TaskTitle != task.Title {
		t.Fatalf("output = %#v", item)
	}
}
