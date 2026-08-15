package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestDatasetWorkspaceIsolationPaginationFiltersAndSearch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "data-w1", Name: "Data One", Path: "/data-one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "data-w2", Name: "Data Two", Path: "/data-two"})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := s.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: w1.ID, Name: "Competitor Research", Description: "Structured market observations",
		Schema: []domain.DatasetColumn{
			{Name: "company", Type: domain.DatasetString},
			{Name: "score", Type: domain.DatasetInteger},
			{Name: "active", Type: domain.DatasetBoolean},
			{Name: "notes", Type: domain.DatasetString, Nullable: true},
		},
		UniqueKey: []string{"company"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Slug != "competitor-research" || dataset.RowCount != 0 {
		t.Fatalf("created dataset = %#v", dataset)
	}
	result, err := s.BulkWriteDatasetRows(ctx, dataset.ID, []map[string]any{
		{"company": "Alpha", "score": float64(8), "active": true, "notes": "Strong enterprise motion"},
		{"company": "Bravo", "score": float64(3), "active": false, "notes": "Quiet niche vendor"},
		{"company": "Charlie", "score": float64(6), "active": true, "notes": "Enterprise challenger"},
		{"company": "Delta", "score": float64(4), "active": true, "notes": nil},
		{"company": "Echo", "score": float64(9), "active": false, "notes": "Premium research"},
	}, DatasetInsert)
	if err != nil || result.Inserted != 5 || result.Updated != 0 {
		t.Fatalf("bulk insert = %#v, %v", result, err)
	}
	loaded, err := s.GetDataset(ctx, dataset.ID)
	if err != nil || loaded.RowCount != 5 {
		t.Fatalf("dataset row count = %#v, %v", loaded, err)
	}

	first, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{Limit: 2, Sort: "score", Descending: true})
	if err != nil || len(first.Rows) != 2 || first.Total != 5 || first.NextCursor == "" ||
		first.Rows[0].Values["company"] != "Echo" || first.Rows[1].Values["company"] != "Alpha" {
		t.Fatalf("first sorted page = %#v, %v", first, err)
	}
	second, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{
		Limit: 2, Cursor: first.NextCursor, Sort: "score", Descending: true,
	})
	if err != nil || len(second.Rows) != 2 || second.Rows[0].Values["company"] != "Charlie" || second.Rows[1].Values["company"] != "Delta" {
		t.Fatalf("second sorted page = %#v, %v", second, err)
	}
	third, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{
		Limit: 2, Cursor: second.NextCursor, Sort: "score", Descending: true,
	})
	if err != nil || len(third.Rows) != 1 || third.NextCursor != "" || third.Rows[0].Values["company"] != "Bravo" {
		t.Fatalf("third sorted page = %#v, %v", third, err)
	}
	searched, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{Limit: 10, Search: "enterprise"})
	if err != nil || searched.Total != 2 {
		t.Fatalf("searched page = %#v, %v", searched, err)
	}
	filtered, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{
		Limit: 10, Filters: map[string]string{"active": "true"},
	})
	if err != nil || filtered.Total != 3 {
		t.Fatalf("filtered page = %#v, %v", filtered, err)
	}

	other, err := s.CreateDataset(ctx, domain.Dataset{WorkspaceID: w2.ID, Name: "Private other scope"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDataset(ctx, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope GetDataset error = %v, want not found", err)
	}
	if _, err := s.QueryDatasetRows(ctx, other.ID, DatasetRowFilter{Limit: 10}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope QueryDatasetRows error = %v, want not found", err)
	}
	if _, err := s.BulkWriteDatasetRows(ctx, other.ID, []map[string]any{{"secret": "value"}}, DatasetInsert); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope BulkWriteDatasetRows error = %v, want not found", err)
	}
	list, err := s.ListDatasets(ctx, DatasetFilter{})
	if err != nil || len(list) != 1 || list[0].ID != dataset.ID {
		t.Fatalf("active workspace datasets = %#v, %v", list, err)
	}
}

// This is the exact autonomous-task boundary: rows_file is decoded into 100
// typed maps before reaching the store. The store must commit all 100 or none,
// and its denormalized list count must stay equal to the physical row count.
func TestDatasetHundredRowUpsertIsAtomicAndListCountIsAccurate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{
		ID: "hundred-row-workspace", Name: "Hundred rows", Path: "/hundred-rows",
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := s.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID,
		Name:        "File-loaded research",
		Schema: []domain.DatasetColumn{
			{Name: "key", Type: domain.DatasetString},
			{Name: "score", Type: domain.DatasetInteger},
		},
		UniqueKey: []string{"key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 100)
	for index := range rows {
		// json.Number matches the decoder used for rows_file payloads.
		rows[index] = map[string]any{
			"key": fmt.Sprintf("row-%03d", index), "score": json.Number(fmt.Sprintf("%d", index)),
		}
	}
	result, err := s.BulkWriteDatasetRowsForWorkspace(ctx, workspace.ID, dataset.ID, rows, DatasetUpsert)
	if err != nil || result.Inserted != 100 || result.Updated != 0 || len(result.Rows) != 100 {
		t.Fatalf("100-row upsert = %#v, %v", result, err)
	}
	assertDatasetCounts := func(want int64) {
		t.Helper()
		listed, listErr := s.ListDatasets(ctx, DatasetFilter{WorkspaceID: workspace.ID})
		if listErr != nil || len(listed) != 1 || listed[0].RowCount != want {
			t.Fatalf("dataset list count = %#v, %v; want %d", listed, listErr, want)
		}
		var physical int64
		if countErr := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dataset_rows WHERE dataset_id = ?`, dataset.ID).Scan(&physical); countErr != nil {
			t.Fatal(countErr)
		}
		if physical != want {
			t.Fatalf("physical row count = %d, want %d", physical, want)
		}
	}
	assertDatasetCounts(100)

	invalid := make([]map[string]any, len(rows))
	for index, row := range rows {
		invalid[index] = map[string]any{"key": row["key"], "score": json.Number("999")}
	}
	invalid[99]["score"] = "not-an-integer"
	if _, err := s.BulkWriteDatasetRowsForWorkspace(ctx, workspace.ID, dataset.ID, invalid, DatasetUpsert); err == nil {
		t.Fatal("invalid final row did not roll back the 99 preceding updates")
	}
	assertDatasetCounts(100)
	page, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{
		WorkspaceID: workspace.ID, Limit: 10, Filters: map[string]string{"key": "row-000"},
	})
	if err != nil || page.Total != 1 || page.Rows[0].Values["score"] != json.Number("0") {
		t.Fatalf("failed batch changed an earlier row = %#v, %v", page, err)
	}

	mixed := make([]map[string]any, 100)
	for index := range mixed {
		keyIndex := index
		if index >= 50 {
			keyIndex += 50
		}
		mixed[index] = map[string]any{
			"key": fmt.Sprintf("row-%03d", keyIndex), "score": json.Number(fmt.Sprintf("%d", keyIndex+1)),
		}
	}
	result, err = s.BulkWriteDatasetRowsForWorkspace(ctx, workspace.ID, dataset.ID, mixed, DatasetUpsert)
	if err != nil || result.Inserted != 50 || result.Updated != 50 {
		t.Fatalf("mixed 100-row upsert = %#v, %v", result, err)
	}
	assertDatasetCounts(150)
}

func TestCreateDatasetWithRowsForWorkspaceIsAtomic(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{
		ID: "atomic-create-workspace", Name: "Atomic create", Path: "/atomic-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 100)
	for index := range rows {
		rows[index] = map[string]any{
			"key": fmt.Sprintf("item-%03d", index), "score": json.Number(fmt.Sprintf("%d", index)),
		}
	}
	dataset, result, err := s.CreateDatasetWithRowsForWorkspace(ctx, workspace.ID, domain.Dataset{
		ID: "atomic-created-dataset", Name: "Atomic research", Slug: "atomic-research",
		Schema: []domain.DatasetColumn{
			{Name: "key", Type: domain.DatasetString},
			{Name: "score", Type: domain.DatasetInteger},
		},
		UniqueKey: []string{"key"}, RowCount: 999,
	}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.WorkspaceID != workspace.ID || dataset.RowCount != 100 || result.Inserted != 100 || result.Updated != 0 || len(result.Rows) != 100 {
		t.Fatalf("atomic create result = dataset %#v, rows %#v", dataset, result)
	}
	listed, err := s.ListDatasets(ctx, DatasetFilter{WorkspaceID: workspace.ID})
	if err != nil || len(listed) != 1 || listed[0].ID != dataset.ID || listed[0].RowCount != 100 {
		t.Fatalf("atomic dataset list = %#v, %v", listed, err)
	}
	page, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 100})
	if err != nil || page.Total != 100 || len(page.Rows) != 100 {
		t.Fatalf("atomic dataset rows = %#v, %v", page, err)
	}

	invalidRows := append([]map[string]any(nil), rows...)
	invalidRows[len(invalidRows)-1] = map[string]any{"key": "item-invalid", "score": "not-an-integer"}
	if _, _, err := s.CreateDatasetWithRowsForWorkspace(ctx, workspace.ID, domain.Dataset{
		ID: "atomic-invalid-dataset", Name: "Must roll back", Slug: "must-roll-back",
		Schema: []domain.DatasetColumn{
			{Name: "key", Type: domain.DatasetString},
			{Name: "score", Type: domain.DatasetInteger},
		},
		UniqueKey: []string{"key"},
	}, invalidRows); err == nil {
		t.Fatal("invalid create-with-rows succeeded")
	}
	if _, err := s.GetDatasetForWorkspace(ctx, workspace.ID, "atomic-invalid-dataset", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid batch retained dataset metadata: %v", err)
	}

	inferred, inferredRows, err := s.CreateDatasetWithRowsForWorkspace(ctx, workspace.ID, domain.Dataset{
		ID: "atomic-inferred-dataset", Name: "Inferred atomically", Slug: "inferred-atomically",
	}, []map[string]any{{"name": "First", "rank": json.Number("1")}, {"name": "Second", "rank": json.Number("2")}})
	if err != nil || inferred.RowCount != 2 || len(inferred.Schema) != 2 || inferredRows.Inserted != 2 {
		t.Fatalf("inferred atomic dataset = %#v, %#v, %v", inferred, inferredRows, err)
	}
}

func TestDatasetTypeValidationBulkAtomicityUpsertAndSchemaEvolution(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "typed-data", Name: "Typed", Path: "/typed-data"})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := s.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Leads",
		Schema: []domain.DatasetColumn{
			{Name: "email", Type: domain.DatasetString},
			{Name: "visits", Type: domain.DatasetInteger},
			{Name: "qualified", Type: domain.DatasetBoolean},
			{Name: "seen_at", Type: domain.DatasetDatetime},
			{Name: "metadata", Type: domain.DatasetJSON, Nullable: true},
		},
		UniqueKey: []string{"email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]any{
		"email": "owner@example.com", "visits": float64(3), "qualified": true,
		"seen_at": "2026-08-12T12:00:00-04:00", "metadata": map[string]any{"source": "research"},
	}
	invalid := map[string]any{
		"email": "broken@example.com", "visits": float64(1.5), "qualified": true,
		"seen_at": "not-a-date", "metadata": nil,
	}
	if _, err := s.BulkWriteDatasetRows(ctx, dataset.ID, []map[string]any{valid, invalid}, DatasetInsert); err == nil {
		t.Fatal("invalid typed bulk write was accepted")
	}
	loaded, err := s.GetDataset(ctx, dataset.ID)
	if err != nil || loaded.RowCount != 0 {
		t.Fatalf("failed bulk write was not atomic: %#v, %v", loaded, err)
	}
	page, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("failed bulk rows persisted: %#v, %v", page, err)
	}

	inserted, err := s.BulkWriteDatasetRows(ctx, dataset.ID, []map[string]any{valid}, DatasetInsert)
	if err != nil || inserted.Inserted != 1 {
		t.Fatalf("valid insert = %#v, %v", inserted, err)
	}
	replacement := map[string]any{
		"email": "owner@example.com", "visits": float64(8), "qualified": false,
		"seen_at": "2026-08-13T12:00:00Z", "metadata": map[string]any{"source": "import"},
	}
	upserted, err := s.BulkWriteDatasetRows(ctx, dataset.ID, []map[string]any{
		replacement,
		{"email": "new@example.com", "visits": float64(1), "qualified": true, "seen_at": "2026-08-14T12:00:00Z", "metadata": nil},
	}, DatasetUpsert)
	if err != nil || upserted.Inserted != 1 || upserted.Updated != 1 {
		t.Fatalf("upsert = %#v, %v", upserted, err)
	}
	loaded, err = s.GetDataset(ctx, dataset.ID)
	if err != nil || loaded.RowCount != 2 {
		t.Fatalf("row count after upsert = %#v, %v", loaded, err)
	}
	page, err = s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{Limit: 10, Filters: map[string]string{"email": "owner@example.com"}})
	if err != nil || page.Total != 1 || page.Rows[0].Values["visits"] != json.Number("8") {
		t.Fatalf("upserted row = %#v, %v", page, err)
	}

	loaded.Schema = append(loaded.Schema, domain.DatasetColumn{Name: "campaign", Type: domain.DatasetString, Nullable: true})
	if err := s.UpdateDataset(ctx, loaded); err != nil {
		t.Fatalf("additive schema update: %v", err)
	}
	changed := loaded
	changed.Schema[0].Type = domain.DatasetNumber
	if err := s.UpdateDataset(ctx, changed); err == nil {
		t.Fatal("existing dataset column type change was accepted")
	}
	removed := loaded
	removed.Schema = removed.Schema[:len(removed.Schema)-1]
	if err := s.UpdateDataset(ctx, removed); err == nil {
		t.Fatal("dataset column removal was accepted")
	}

	inferred, err := s.CreateDataset(ctx, domain.Dataset{WorkspaceID: workspace.ID, Name: "Inferred"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BulkWriteDatasetRows(ctx, inferred.ID, []map[string]any{
		{"name": "First", "rank": float64(1)}, {"name": "Second", "extra": true},
	}, DatasetInsert); err != nil {
		t.Fatal(err)
	}
	inferred, err = s.GetDataset(ctx, inferred.ID)
	if err != nil || len(inferred.Schema) != 3 {
		t.Fatalf("inferred schema = %#v, %v", inferred, err)
	}
	for _, column := range inferred.Schema {
		if (column.Name == "rank" || column.Name == "extra") && !column.Nullable {
			t.Fatalf("sparse inferred column is not nullable: %#v", column)
		}
	}
}

func TestDatasetSoftDeleteRestoreAndExplicitPurge(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "trash-data", Name: "Trash", Path: "/trash-data"})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := s.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Recoverable", Schema: []domain.DatasetColumn{{Name: "value", Type: domain.DatasetString}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.BulkWriteDatasetRows(ctx, dataset.ID, []map[string]any{{"value": "keep me"}}, DatasetInsert)
	if err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	if err := s.SoftDeleteDataset(ctx, dataset.ID, deletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDataset(ctx, dataset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft-deleted GetDataset error = %v, want not found", err)
	}
	trash, err := s.ListDatasets(ctx, DatasetFilter{DeletedOnly: true})
	if err != nil || len(trash) != 1 || trash[0].DeletedAt == nil || !trash[0].DeletedAt.Equal(deletedAt) {
		t.Fatalf("trash datasets = %#v, %v", trash, err)
	}
	if _, err := s.BulkWriteDatasetRows(ctx, dataset.ID, []map[string]any{{"value": "must fail"}}, DatasetInsert); !errors.Is(err, ErrNotFound) {
		t.Fatalf("write to deleted dataset error = %v, want not found", err)
	}
	restored, err := s.RestoreDataset(ctx, dataset.ID)
	if err != nil || restored.DeletedAt != nil || restored.RowCount != 1 {
		t.Fatalf("restored dataset = %#v, %v", restored, err)
	}
	page, err := s.QueryDatasetRows(ctx, dataset.ID, DatasetRowFilter{Limit: 10})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ID != rows.Rows[0].ID {
		t.Fatalf("restored dataset rows = %#v, %v", page, err)
	}
	if err := s.PurgeDataset(ctx, workspace.ID, dataset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active dataset purge error = %v, want not found", err)
	}
	if err := s.SoftDeleteDataset(ctx, dataset.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeDataset(ctx, workspace.ID, dataset.ID); err != nil {
		t.Fatal(err)
	}
	var rowCount int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dataset_rows WHERE dataset_id = ?", dataset.ID).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 0 {
		t.Fatalf("purged dataset retained %d rows", rowCount)
	}
}

func TestDatasetJSONSchemaRoundTrip(t *testing.T) {
	column := domain.DatasetColumn{Name: "payload", Type: domain.DatasetJSON, Nullable: true}
	encoded, err := json.Marshal(column)
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.DatasetColumn
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded, column) {
		t.Fatalf("dataset column roundtrip = %#v, %v", decoded, err)
	}
}
