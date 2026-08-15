package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

type DatasetCreate struct {
	Name        string                 `json:"name"`
	Slug        string                 `json:"slug,omitempty"`
	Description string                 `json:"description,omitempty"`
	Schema      []domain.DatasetColumn `json:"schema,omitempty"`
	UniqueKey   []string               `json:"unique_key,omitempty"`
}

type DatasetUpdate struct {
	Name        *string                 `json:"name,omitempty"`
	Slug        *string                 `json:"slug,omitempty"`
	Description *string                 `json:"description,omitempty"`
	Schema      *[]domain.DatasetColumn `json:"schema,omitempty"`
	UniqueKey   *[]string               `json:"unique_key,omitempty"`
}

type DatasetBulkInput struct {
	Rows []map[string]any       `json:"rows"`
	Mode store.DatasetWriteMode `json:"mode,omitempty"`
}

type DatasetRowUpdate struct {
	Values map[string]any `json:"values"`
}

type DatasetBackend interface {
	Datasets(context.Context, store.DatasetFilter) ([]domain.Dataset, error)
	Dataset(context.Context, string, bool) (domain.Dataset, error)
	CreateDataset(context.Context, DatasetCreate) (domain.Dataset, error)
	UpdateDataset(context.Context, string, DatasetUpdate) (domain.Dataset, error)
	DeleteDataset(context.Context, string) error
	RestoreDataset(context.Context, string) (domain.Dataset, error)
	QueryDatasetRows(context.Context, string, store.DatasetRowFilter) (store.DatasetRowPage, error)
	BulkDatasetRows(context.Context, string, DatasetBulkInput) (store.DatasetBulkResult, error)
	UpdateDatasetRow(context.Context, string, int64, DatasetRowUpdate) (domain.DatasetRow, error)
	DeleteDatasetRow(context.Context, string, int64) error
	ExportDataset(context.Context, string, string, io.Writer) (domain.Dataset, error)
}

func (s *Server) registerDatabaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/database/datasets", s.getDatasets)
	mux.HandleFunc("POST /api/database/datasets", s.postDataset)
	mux.HandleFunc("GET /api/database/datasets/{id}", s.getDataset)
	mux.HandleFunc("PATCH /api/database/datasets/{id}", s.patchDataset)
	mux.HandleFunc("DELETE /api/database/datasets/{id}", s.deleteDataset)
	mux.HandleFunc("POST /api/database/datasets/{id}/restore", s.postDatasetRestore)
	mux.HandleFunc("GET /api/database/datasets/{id}/rows", s.getDatasetRows)
	mux.HandleFunc("POST /api/database/datasets/{id}/rows", s.postDatasetRows)
	mux.HandleFunc("PATCH /api/database/datasets/{id}/rows/{row_id}", s.patchDatasetRow)
	mux.HandleFunc("DELETE /api/database/datasets/{id}/rows/{row_id}", s.deleteDatasetRow)
	mux.HandleFunc("GET /api/database/datasets/{id}/export", s.getDatasetExport)
}

func (s *Server) datasetBackend(w http.ResponseWriter) DatasetBackend {
	backend, ok := s.backend.(DatasetBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include workspace datasets.")
		return nil
	}
	return backend
}

func (s *Server) getDatasets(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	includeDeleted, err := queryBool(r, "include_deleted")
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	deletedOnly, err := queryBool(r, "deleted_only")
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	limit, err := queryLimit(r, 0, 1_000)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	value, err := backend.Datasets(r.Context(), store.DatasetFilter{
		IncludeDeleted: includeDeleted, DeletedOnly: deletedOnly, Search: r.URL.Query().Get("q"), Limit: limit,
	})
	s.respond(w, map[string]any{"datasets": value}, err)
}

func (s *Server) postDataset(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	var input DatasetCreate
	if !s.decode(w, r, &input) {
		return
	}
	value, err := backend.CreateDataset(r.Context(), input)
	if err == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"dataset": value})
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) getDataset(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	includeDeleted, err := queryBool(r, "include_deleted")
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	value, err := backend.Dataset(r.Context(), r.PathValue("id"), includeDeleted)
	s.respond(w, map[string]any{"dataset": value}, err)
}

func (s *Server) patchDataset(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	var input DatasetUpdate
	if !s.decode(w, r, &input) {
		return
	}
	if input.Name == nil && input.Slug == nil && input.Description == nil && input.Schema == nil && input.UniqueKey == nil {
		writeError(w, http.StatusBadRequest, "invalid_dataset", "At least one dataset field is required.")
		return
	}
	value, err := backend.UpdateDataset(r.Context(), r.PathValue("id"), input)
	s.respond(w, map[string]any{"dataset": value}, err)
}

func (s *Server) deleteDataset(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	if err := backend.DeleteDataset(r.Context(), r.PathValue("id")); err != nil {
		s.respond(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) postDatasetRestore(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	value, err := backend.RestoreDataset(r.Context(), r.PathValue("id"))
	s.respond(w, map[string]any{"dataset": value}, err)
}

func (s *Server) getDatasetRows(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	limit, err := queryLimit(r, 100, store.MaximumDatasetPageSize)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	direction := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction")))
	if direction != "" && direction != "asc" && direction != "desc" {
		s.respond(w, nil, fmt.Errorf("%w: direction must be asc or desc", ErrInvalid))
		return
	}
	filters := make(map[string]string)
	for key, values := range r.URL.Query() {
		if strings.HasPrefix(key, "filter[") && strings.HasSuffix(key, "]") && len(values) == 1 {
			name := strings.TrimSuffix(strings.TrimPrefix(key, "filter["), "]")
			if name == "" {
				s.respond(w, nil, fmt.Errorf("%w: filter column cannot be empty", ErrInvalid))
				return
			}
			filters[name] = values[0]
		}
	}
	value, err := backend.QueryDatasetRows(r.Context(), r.PathValue("id"), store.DatasetRowFilter{
		Limit: limit, Cursor: r.URL.Query().Get("cursor"), Sort: r.URL.Query().Get("sort"),
		Descending: direction == "desc", Search: r.URL.Query().Get("q"), Filters: filters,
	})
	s.respond(w, value, err)
}

func (s *Server) postDatasetRows(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	var input DatasetBulkInput
	if !s.decodeDataset(w, r, &input) {
		return
	}
	value, err := backend.BulkDatasetRows(r.Context(), r.PathValue("id"), input)
	if err == nil {
		writeJSON(w, http.StatusCreated, value)
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) decodeDataset(w http.ResponseWriter, r *http.Request, target any) bool {
	// Keep local imports large enough for useful research batches while avoiding
	// a single request allocating hundreds of megabytes before row validation.
	r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must be valid bounded JSON: "+err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func (s *Server) patchDatasetRow(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	rowID, err := datasetRowID(r)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	var input DatasetRowUpdate
	if !s.decode(w, r, &input) {
		return
	}
	value, err := backend.UpdateDatasetRow(r.Context(), r.PathValue("id"), rowID, input)
	s.respond(w, map[string]any{"row": value}, err)
}

func (s *Server) deleteDatasetRow(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	rowID, err := datasetRowID(r)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	if err := backend.DeleteDatasetRow(r.Context(), r.PathValue("id"), rowID); err != nil {
		s.respond(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getDatasetExport(w http.ResponseWriter, r *http.Request) {
	backend := s.datasetBackend(w)
	if backend == nil {
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format != "csv" && format != "json" {
		s.respond(w, nil, fmt.Errorf("%w: export format must be csv or json", ErrInvalid))
		return
	}
	dataset, err := backend.Dataset(r.Context(), r.PathValue("id"), false)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	filename := dataset.Slug + "." + format
	contentType := "application/json; charset=utf-8"
	if format == "csv" {
		contentType = "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	// Export streams directly and uses bounded store pages. Once streaming has
	// begun, a late client/write error terminates the response rather than
	// attempting to append a JSON error document to exported data.
	if _, err := backend.ExportDataset(r.Context(), dataset.ID, format, w); err != nil {
		return
	}
}

func datasetRowID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("row_id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w: invalid dataset row ID", ErrInvalid)
	}
	return id, nil
}

func queryBool(r *http.Request, key string) (bool, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%w: %s must be true or false", ErrInvalid, key)
	}
	return parsed, nil
}

func queryLimit(r *http.Request, fallback, maximum int) (int, error) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, maximum)
	}
	return parsed, nil
}
