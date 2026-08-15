package operator

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestOperatorDatasetLifecycleAndStreamingExports(t *testing.T) {
	operator, _, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	dataset, err := operator.CreateDataset(ctx, api.DatasetCreate{
		Name: "Research Contacts",
		Schema: []domain.DatasetColumn{
			{Name: "email", Type: domain.DatasetString},
			{Name: "score", Type: domain.DatasetInteger},
			{Name: "details", Type: domain.DatasetJSON, Nullable: true},
		},
		UniqueKey: []string{"email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dataset.WorkspaceID != workspace.ID {
		t.Fatalf("dataset workspace = %q, want %q", dataset.WorkspaceID, workspace.ID)
	}
	written, err := operator.BulkDatasetRows(ctx, dataset.ID, api.DatasetBulkInput{
		Mode: store.DatasetInsert,
		Rows: []map[string]any{
			{"email": "one@example.com", "score": float64(7), "details": map[string]any{"source": "search"}},
			{"email": "two@example.com", "score": float64(3), "details": nil},
		},
	})
	if err != nil || written.Inserted != 2 {
		t.Fatalf("bulk dataset rows = %#v, %v", written, err)
	}
	page, err := operator.QueryDatasetRows(ctx, dataset.ID, store.DatasetRowFilter{Limit: 1})
	if err != nil || len(page.Rows) != 1 || page.NextCursor == "" || page.Total != 2 {
		t.Fatalf("dataset row page = %#v, %v", page, err)
	}
	updated, err := operator.UpdateDatasetRow(ctx, dataset.ID, page.Rows[0].ID, api.DatasetRowUpdate{
		Values: map[string]any{"score": float64(9)},
	})
	if err != nil || updated.Values["score"] != json.Number("9") {
		t.Fatalf("updated dataset row = %#v, %v", updated, err)
	}

	var csvOutput bytes.Buffer
	if _, err := operator.ExportDataset(ctx, dataset.ID, "csv", &csvOutput); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(csvOutput.String())).ReadAll()
	if err != nil || len(records) != 3 || strings.Join(records[0], ",") != "email,score,details" {
		t.Fatalf("CSV export = %#v, %v, raw %q", records, err, csvOutput.String())
	}
	var jsonOutput bytes.Buffer
	if _, err := operator.ExportDataset(ctx, dataset.ID, "json", &jsonOutput); err != nil {
		t.Fatal(err)
	}
	var exported []map[string]any
	if err := json.Unmarshal(jsonOutput.Bytes(), &exported); err != nil || len(exported) != 2 {
		t.Fatalf("JSON export = %#v, %v, raw %q", exported, err, jsonOutput.String())
	}

	if err := operator.DeleteDataset(ctx, dataset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Dataset(ctx, dataset.ID, false); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("deleted operator dataset error = %v, want not found", err)
	}
	restored, err := operator.RestoreDataset(ctx, dataset.ID)
	if err != nil || restored.RowCount != 2 {
		t.Fatalf("restored operator dataset = %#v, %v", restored, err)
	}
}
