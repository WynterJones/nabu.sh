package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestApplyTaskDatasetWritesCreatesAndUpsertsWithBoundedEvidence(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	result := domain.RunResult{DatasetWrites: []domain.DatasetWrite{{
		Operation: domain.DatasetWriteCreate,
		Dataset:   &domain.Dataset{Name: "Research", Slug: "research", Schema: []domain.DatasetColumn{{Name: "source", Type: domain.DatasetString}}, UniqueKey: []string{"source"}},
	}}}
	if err := operator.applyTaskDatasetWrites(ctx, domain.Task{ID: "task-1", WorkspaceID: workspace.ID}, "run-1", &result); err != nil {
		t.Fatal(err)
	}
	if !result.DatasetWrites[0].Applied || result.DatasetWrites[0].DatasetID == "" || result.DatasetWrites[0].Dataset.RowCount != 0 {
		t.Fatalf("create evidence = %#v", result.DatasetWrites[0])
	}
	datasetID := result.DatasetWrites[0].DatasetID
	result.DatasetWrites = []domain.DatasetWrite{{Operation: domain.DatasetWriteUpsert, DatasetID: datasetID, Rows: []map[string]any{{"source": "primary"}}}}
	if err := operator.applyTaskDatasetWrites(ctx, domain.Task{ID: "task-1", WorkspaceID: workspace.ID}, "run-1", &result); err != nil {
		t.Fatal(err)
	}
	if !result.DatasetWrites[0].Applied || result.DatasetWrites[0].Inserted != 1 || result.DatasetWrites[0].Rows != nil {
		t.Fatalf("upsert evidence retained rows or omitted counts: %#v", result.DatasetWrites[0])
	}
	page, err := database.QueryDatasetRows(ctx, datasetID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("dataset page = %#v, err=%v", page, err)
	}
}

func TestApplyTaskDatasetWritesLoadsBoundedWorkspaceRowsFile(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	dataset, err := database.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Research", Slug: "research-file", UniqueKey: []string{"source"},
		Schema: []domain.DatasetColumn{{Name: "source", Type: domain.DatasetString}, {Name: "score", Type: domain.DatasetInteger}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace.Path, "research"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "research", "rows.json"), []byte(`[{"source":"primary","score":9}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "task-1", WorkspaceID: workspace.ID, Workspace: &workspace}
	result := domain.RunResult{DatasetWrites: []domain.DatasetWrite{{Operation: domain.DatasetWriteUpsert, DatasetID: dataset.ID, RowsFile: "research/rows.json"}}}
	if err := operator.applyTaskDatasetWrites(ctx, task, "run-1", &result); err != nil {
		t.Fatal(err)
	}
	if !result.DatasetWrites[0].Applied || result.DatasetWrites[0].Inserted != 1 || result.DatasetWrites[0].Rows != nil {
		t.Fatalf("file write evidence = %#v", result.DatasetWrites[0])
	}
	page, err := database.QueryDatasetRows(ctx, dataset.ID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("dataset page = %#v, err=%v", page, err)
	}
}

func TestApplyTaskDatasetWritesLoadsLargeBoundedWorkspaceRowsFile(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	dataset, err := database.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Large research", Slug: "large-research", UniqueKey: []string{"source"},
		Schema: []domain.DatasetColumn{{Name: "source", Type: domain.DatasetString}, {Name: "notes", Type: domain.DatasetString}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace.Path, "research"), 0o700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"source": "primary", "notes": strings.Repeat("verified research evidence ", 7_000)},
		{"source": "secondary", "notes": strings.Repeat("verified research evidence ", 7_000)},
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= runner.MaxRunDatasetWriteBytes || len(payload) > runner.MaxRunDatasetRowsFileBytes {
		t.Fatalf("test payload size = %d", len(payload))
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "research", "large-rows.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "task-large", WorkspaceID: workspace.ID, Workspace: &workspace}
	result := domain.RunResult{DatasetWrites: []domain.DatasetWrite{{Operation: domain.DatasetWriteUpsert, DatasetID: dataset.ID, RowsFile: "research/large-rows.json"}}}
	if err := operator.applyTaskDatasetWrites(ctx, task, "run-large", &result); err != nil {
		t.Fatal(err)
	}
	if !result.DatasetWrites[0].Applied || result.DatasetWrites[0].Inserted != 2 || result.DatasetWrites[0].Rows != nil {
		t.Fatalf("large file evidence = %#v", result.DatasetWrites[0])
	}
	page, err := database.QueryDatasetRows(ctx, dataset.ID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil || page.Total != 2 {
		t.Fatalf("large dataset page = %#v, err=%v", page, err)
	}
}

func TestApplyTaskDatasetWritesAtomicallyCreatesFromWorkspaceRowsFile(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(workspace.Path, "research"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "research", "new-rows.json"), []byte(`[{"source":"primary","score":9}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "task-1", WorkspaceID: workspace.ID, Workspace: &workspace}
	result := domain.RunResult{DatasetWrites: []domain.DatasetWrite{{
		Operation: domain.DatasetWriteCreate, RowsFile: "research/new-rows.json",
		Dataset: &domain.Dataset{Name: "New research", Slug: "new-research", UniqueKey: []string{"source"}, Schema: []domain.DatasetColumn{{Name: "source", Type: domain.DatasetString}, {Name: "score", Type: domain.DatasetInteger}}},
	}}}
	if err := operator.applyTaskDatasetWrites(ctx, task, "run-1", &result); err != nil {
		t.Fatal(err)
	}
	evidence := result.DatasetWrites[0]
	if !evidence.Applied || evidence.Inserted != 1 || evidence.DatasetID == "" || evidence.Rows != nil {
		t.Fatalf("atomic create evidence = %#v", evidence)
	}
	created, err := database.GetDatasetForWorkspace(ctx, workspace.ID, evidence.DatasetID, false)
	if err != nil || created.RowCount != 1 {
		t.Fatalf("created dataset = %#v, err=%v", created, err)
	}
}

func TestLoadTaskDatasetRowsFileRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`[{"source":"private"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "rows.json")); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{Path: root}
	if _, err := loadTaskDatasetRowsFile(domain.Task{Workspace: &workspace}, "rows.json"); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestTaskDatasetCompletionGuardRequiresStructuredUpsert(t *testing.T) {
	task := domain.Task{Title: "Reconcile catalog into dataset", Purpose: "Upsert all 100 rows into the dataset", DefinitionOfDone: []domain.DefinitionItem{{Text: "Dataset row count is verified"}}}
	if !taskRequiresDatasetRows(task) {
		t.Fatal("dataset requirement was not detected")
	}
	if resultIncludesDatasetRows(domain.RunResult{}) {
		t.Fatal("empty result satisfied dataset requirement")
	}
	if !resultIncludesDatasetRows(domain.RunResult{DatasetWrites: []domain.DatasetWrite{{Operation: domain.DatasetWriteUpsert, DatasetID: "dataset-1", RowsFile: "research/rows.json"}}}) {
		t.Fatal("rows_file upsert was not detected")
	}
}

func TestApplyTaskDatasetWritesRejectsCrossWorkspaceID(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	other, err := database.CreateWorkspace(ctx, domain.Workspace{ID: "other-workspace", Name: "Other", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := database.CreateDataset(ctx, domain.Dataset{WorkspaceID: other.ID, Name: "Private", Slug: "private", Schema: []domain.DatasetColumn{{Name: "key", Type: domain.DatasetString}}, UniqueKey: []string{"key"}})
	if err != nil {
		t.Fatal(err)
	}
	result := domain.RunResult{DatasetWrites: []domain.DatasetWrite{{Operation: domain.DatasetWriteUpsert, DatasetID: foreign.ID, Rows: []map[string]any{{"key": "hidden"}}}}}
	err = operator.applyTaskDatasetWrites(ctx, domain.Task{ID: "task-1", WorkspaceID: workspace.ID}, "run-1", &result)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-workspace dataset write error = %v", err)
	}
}

func TestApplyTaskDatasetWritesPreflightsEntireBatch(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	dataset, err := database.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Metrics", Slug: "metrics", UniqueKey: []string{"key"},
		Schema: []domain.DatasetColumn{{Name: "key", Type: domain.DatasetString}, {Name: "value", Type: domain.DatasetInteger}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := domain.RunResult{DatasetWrites: []domain.DatasetWrite{
		{Operation: domain.DatasetWriteUpsert, DatasetID: dataset.ID, Rows: []map[string]any{{"key": "safe", "value": 1}}},
		{Operation: domain.DatasetWriteUpsert, DatasetID: dataset.ID, Rows: []map[string]any{{"key": "bad", "value": "not-an-integer"}}},
	}}
	if err := operator.applyTaskDatasetWrites(ctx, domain.Task{ID: "task-1", WorkspaceID: workspace.ID}, "run-1", &result); err == nil {
		t.Fatal("malformed later write was accepted")
	}
	page, err := database.QueryDatasetRows(ctx, dataset.ID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("preflight allowed a partial write: %#v, err=%v", page, err)
	}
}

func TestAutonomousTaskAppliesVerifiedDatasetWriteAndPersistsOnlyEvidence(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	dataset, err := database.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Findings", Slug: "findings", UniqueKey: []string{"source"},
		Schema: []domain.DatasetColumn{{Name: "source", Type: domain.DatasetString}, {Name: "score", Type: domain.DatasetInteger}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operator.runner = fakeExecutor{result: fmt.Sprintf(`{
  "status":"completed","summary":"Captured the verified finding.","files_changed":[],
  "verification":[{"name":"schema validation","status":"passed","details":"source and score matched the declared schema"}],
  "artifacts":[],"uncertainties":[],"approval_needed":null,
  "dataset_writes":[{"operation":"upsert_rows","dataset_id":%q,"rows":[{"source":"primary","score":9}]}]
}`, dataset.ID)}
	task, err := database.CreateTask(ctx, domain.Task{
		WorkspaceID: workspace.ID, Title: "Store finding", Purpose: "Persist structured evidence", Why: "Build the dataset",
		Status: domain.TaskRunning, Priority: domain.PriorityNormal, CreatedBy: "orientation",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "Finding is stored"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.runTask(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	completed, err := database.GetTask(ctx, task.ID)
	if err != nil || completed.Status != domain.TaskCompleted || completed.Result == nil || len(completed.Result.DatasetWrites) != 1 {
		t.Fatalf("completed task = %#v, err=%v", completed, err)
	}
	evidence := completed.Result.DatasetWrites[0]
	if !evidence.Applied || evidence.Inserted != 1 || evidence.Rows != nil {
		t.Fatalf("durable dataset evidence = %#v", evidence)
	}
	page, err := database.QueryDatasetRows(ctx, dataset.ID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("dataset page = %#v, err=%v", page, err)
	}
	events, err := database.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "dataset.rows.written" && strings.Contains(string(event.Data), `"source":"task_result"`) && strings.Contains(string(event.Data), `"run_id"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("dataset write evidence event missing: %#v", events)
	}
}
