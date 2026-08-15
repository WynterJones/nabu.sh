package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

type databaseStubBackend struct {
	*stubBackend
	lastFilter store.DatasetRowFilter
	lastBulk   DatasetBulkInput
}

func newDatabaseStubBackend() *databaseStubBackend {
	return &databaseStubBackend{stubBackend: &stubBackend{}}
}

func (b *databaseStubBackend) Datasets(context.Context, store.DatasetFilter) ([]domain.Dataset, error) {
	return []domain.Dataset{{ID: "dataset-1", Name: "Research", Slug: "research"}}, nil
}
func (b *databaseStubBackend) Dataset(context.Context, string, bool) (domain.Dataset, error) {
	return domain.Dataset{ID: "dataset-1", Name: "Research", Slug: "research", Schema: []domain.DatasetColumn{{Name: "name", Type: domain.DatasetString}}}, nil
}
func (b *databaseStubBackend) CreateDataset(_ context.Context, input DatasetCreate) (domain.Dataset, error) {
	return domain.Dataset{ID: "dataset-1", Name: input.Name, Slug: "research", Schema: input.Schema}, nil
}
func (b *databaseStubBackend) UpdateDataset(context.Context, string, DatasetUpdate) (domain.Dataset, error) {
	return domain.Dataset{ID: "dataset-1", Name: "Updated", Slug: "research"}, nil
}
func (b *databaseStubBackend) DeleteDataset(context.Context, string) error { return nil }
func (b *databaseStubBackend) RestoreDataset(context.Context, string) (domain.Dataset, error) {
	return domain.Dataset{ID: "dataset-1", Name: "Research", Slug: "research"}, nil
}
func (b *databaseStubBackend) QueryDatasetRows(_ context.Context, _ string, filter store.DatasetRowFilter) (store.DatasetRowPage, error) {
	b.lastFilter = filter
	return store.DatasetRowPage{Rows: []domain.DatasetRow{{ID: 4, Values: map[string]any{"name": "Alpha"}}}, Total: 1}, nil
}
func (b *databaseStubBackend) BulkDatasetRows(_ context.Context, _ string, input DatasetBulkInput) (store.DatasetBulkResult, error) {
	b.lastBulk = input
	return store.DatasetBulkResult{Inserted: len(input.Rows), Rows: []domain.DatasetRow{}}, nil
}
func (b *databaseStubBackend) UpdateDatasetRow(context.Context, string, int64, DatasetRowUpdate) (domain.DatasetRow, error) {
	return domain.DatasetRow{ID: 4, Values: map[string]any{"name": "Updated"}}, nil
}
func (b *databaseStubBackend) DeleteDatasetRow(context.Context, string, int64) error { return nil }
func (b *databaseStubBackend) ExportDataset(_ context.Context, _ string, format string, output io.Writer) (domain.Dataset, error) {
	if format == "csv" {
		_, _ = io.WriteString(output, "name\nAlpha\n")
	} else {
		_, _ = io.WriteString(output, "[{\"name\":\"Alpha\"}]\n")
	}
	return domain.Dataset{ID: "dataset-1", Name: "Research", Slug: "research"}, nil
}

func TestDatabaseRoutesContracts(t *testing.T) {
	backend := newDatabaseStubBackend()
	handler := New(backend, testAssets(), nil).Handler()

	created := serveDatabaseRequest(handler, http.MethodPost, "/api/database/datasets", `{
"name":"Research","schema":[{"name":"name","type":"string","nullable":false}],"unique_key":["name"]}`)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"dataset"`) {
		t.Fatalf("create dataset response = %d %s", created.Code, created.Body.String())
	}

	bulk := serveDatabaseRequest(handler, http.MethodPost, "/api/database/datasets/dataset-1/rows",
		`{"mode":"upsert","rows":[{"name":"Alpha"}]}`)
	if bulk.Code != http.StatusCreated || backend.lastBulk.Mode != store.DatasetUpsert || len(backend.lastBulk.Rows) != 1 {
		t.Fatalf("bulk response = %d %s, input %#v", bulk.Code, bulk.Body.String(), backend.lastBulk)
	}

	rows := serveDatabaseRequest(handler, http.MethodGet,
		"/api/database/datasets/dataset-1/rows?limit=25&sort=name&direction=desc&q=alp&filter%5Bname%5D=Alpha", "")
	if rows.Code != http.StatusOK || backend.lastFilter.Limit != 25 || backend.lastFilter.Sort != "name" ||
		!backend.lastFilter.Descending || backend.lastFilter.Search != "alp" || backend.lastFilter.Filters["name"] != "Alpha" {
		t.Fatalf("rows response = %d %s, filter %#v", rows.Code, rows.Body.String(), backend.lastFilter)
	}

	patched := serveDatabaseRequest(handler, http.MethodPatch, "/api/database/datasets/dataset-1/rows/4",
		`{"values":{"name":"Updated"}}`)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"row"`) {
		t.Fatalf("patch row response = %d %s", patched.Code, patched.Body.String())
	}

	exported := serveDatabaseRequest(handler, http.MethodGet, "/api/database/datasets/dataset-1/export?format=csv", "")
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		!strings.Contains(exported.Header().Get("Content-Disposition"), "research.csv") || exported.Body.String() != "name\nAlpha\n" {
		t.Fatalf("export response = %d headers=%v body=%q", exported.Code, exported.Header(), exported.Body.String())
	}
}

func TestDatabaseRoutesRejectInvalidBounds(t *testing.T) {
	handler := New(newDatabaseStubBackend(), testAssets(), nil).Handler()
	for _, target := range []string{
		"/api/database/datasets/dataset-1/rows?limit=501",
		"/api/database/datasets/dataset-1/rows?direction=sideways",
		"/api/database/datasets/dataset-1/export?format=sql",
	} {
		response := serveDatabaseRequest(handler, http.MethodGet, target, "")
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s response = %d %s", target, response.Code, response.Body.String())
		}
	}
}

func serveDatabaseRequest(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Host = "127.0.0.1:7777"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	handler.ServeHTTP(response, request)
	return response
}
