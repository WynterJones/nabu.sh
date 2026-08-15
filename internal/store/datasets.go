package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const (
	MaximumDatasetColumns   = 128
	MaximumDatasetBulkRows  = 1_000
	MaximumDatasetPageSize  = 500
	MaximumDatasetCellBytes = 256 * 1024
)

const datasetColumns = `id, workspace_id, name, slug, description, schema_json, unique_key_json,
row_count, deleted_at, created_at, updated_at`
const datasetRowColumns = `id, values_json, created_at, updated_at`

var datasetIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var datasetSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$|^[a-z0-9]$`)

var (
	ErrDatasetDeleted  = errors.New("store: dataset is deleted")
	ErrDatasetConflict = errors.New("store: dataset conflict")
)

type DatasetFilter struct {
	WorkspaceID    string
	IncludeDeleted bool
	DeletedOnly    bool
	Search         string
	Limit          int
}

type DatasetRowFilter struct {
	WorkspaceID string
	Limit       int
	Cursor      string
	Sort        string
	Descending  bool
	Search      string
	Filters     map[string]string
}

type DatasetRowPage struct {
	Rows       []domain.DatasetRow `json:"rows"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Total      int64               `json:"total"`
}

type DatasetWriteMode string

const (
	DatasetInsert DatasetWriteMode = "insert"
	DatasetUpsert DatasetWriteMode = "upsert"
)

type DatasetBulkResult struct {
	Rows     []domain.DatasetRow `json:"rows"`
	Inserted int                 `json:"inserted"`
	Updated  int                 `json:"updated"`
}

func (s *Store) CreateDataset(ctx context.Context, dataset domain.Dataset) (domain.Dataset, error) {
	return s.CreateDatasetForWorkspace(ctx, dataset.WorkspaceID, dataset)
}

func (s *Store) CreateDatasetForWorkspace(ctx context.Context, workspaceID string, dataset domain.Dataset) (domain.Dataset, error) {
	dataset, schemaJSON, uniqueJSON, err := s.prepareNewDataset(ctx, workspaceID, dataset)
	if err != nil {
		return domain.Dataset{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO datasets (`+datasetColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`, dataset.ID, dataset.WorkspaceID, dataset.Name,
		dataset.Slug, dataset.Description, schemaJSON, uniqueJSON,
		formatTime(dataset.CreatedAt), formatTime(dataset.UpdatedAt)); err != nil {
		return domain.Dataset{}, fmt.Errorf("store: create dataset: %w", err)
	}
	return dataset, nil
}

// CreateDatasetWithRows atomically creates and populates a dataset. It is the
// persistence boundary for task results that cannot reference a not-yet-created
// dataset ID. Rows use insert semantics because the dataset is new.
func (s *Store) CreateDatasetWithRows(ctx context.Context, dataset domain.Dataset, rows []map[string]any) (domain.Dataset, DatasetBulkResult, error) {
	return s.CreateDatasetWithRowsForWorkspace(ctx, dataset.WorkspaceID, dataset, rows)
}

func (s *Store) CreateDatasetWithRowsForWorkspace(ctx context.Context, workspaceID string, dataset domain.Dataset, rows []map[string]any) (domain.Dataset, DatasetBulkResult, error) {
	if len(rows) == 0 || len(rows) > MaximumDatasetBulkRows {
		return domain.Dataset{}, DatasetBulkResult{}, fmt.Errorf("store: dataset create with rows requires 1-%d rows", MaximumDatasetBulkRows)
	}
	if len(dataset.Schema) == 0 {
		inferred, err := inferDatasetSchema(rows)
		if err != nil {
			return domain.Dataset{}, DatasetBulkResult{}, err
		}
		dataset.Schema = inferred
	}
	dataset, schemaJSON, uniqueJSON, err := s.prepareNewDataset(ctx, workspaceID, dataset)
	if err != nil {
		return domain.Dataset{}, DatasetBulkResult{}, err
	}
	type preparedDatasetRow struct {
		values      []byte
		search      string
		fingerprint string
	}
	prepared := make([]preparedDatasetRow, 0, len(rows))
	seenFingerprints := make(map[string]struct{}, len(rows))
	for _, values := range rows {
		normalized, err := validateDatasetRow(dataset.Schema, values, false)
		if err != nil {
			return domain.Dataset{}, DatasetBulkResult{}, err
		}
		encoded, search, fingerprint, err := encodeDatasetRow(dataset, normalized)
		if err != nil {
			return domain.Dataset{}, DatasetBulkResult{}, err
		}
		if fingerprint != "" {
			if _, duplicate := seenFingerprints[fingerprint]; duplicate {
				return domain.Dataset{}, DatasetBulkResult{}, fmt.Errorf("%w: duplicate dataset unique key", ErrDatasetConflict)
			}
			seenFingerprints[fingerprint] = struct{}{}
		}
		prepared = append(prepared, preparedDatasetRow{values: encoded, search: search, fingerprint: fingerprint})
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Dataset{}, DatasetBulkResult{}, fmt.Errorf("store: begin create dataset with rows: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO datasets (`+datasetColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`, dataset.ID, dataset.WorkspaceID, dataset.Name,
		dataset.Slug, dataset.Description, schemaJSON, uniqueJSON,
		formatTime(dataset.CreatedAt), formatTime(dataset.UpdatedAt)); err != nil {
		return domain.Dataset{}, DatasetBulkResult{}, fmt.Errorf("store: create dataset: %w", err)
	}
	result := DatasetBulkResult{Rows: make([]domain.DatasetRow, 0, len(prepared)), Inserted: len(prepared)}
	for _, row := range prepared {
		created, err := scanDatasetRow(tx.QueryRowContext(ctx, `
INSERT INTO dataset_rows(dataset_id, values_json, search_text, unique_fingerprint, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING `+datasetRowColumns, dataset.ID, row.values, row.search,
			row.fingerprint, formatTime(dataset.CreatedAt), formatTime(dataset.UpdatedAt)))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return domain.Dataset{}, DatasetBulkResult{}, fmt.Errorf("%w: duplicate dataset unique key", ErrDatasetConflict)
			}
			return domain.Dataset{}, DatasetBulkResult{}, err
		}
		result.Rows = append(result.Rows, created)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE datasets SET row_count = ? WHERE id = ?`, len(prepared), dataset.ID); err != nil {
		return domain.Dataset{}, DatasetBulkResult{}, fmt.Errorf("store: initialize dataset row count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Dataset{}, DatasetBulkResult{}, fmt.Errorf("store: commit dataset with rows: %w", err)
	}
	dataset.RowCount = int64(len(prepared))
	return dataset, result, nil
}

func (s *Store) prepareNewDataset(ctx context.Context, workspaceID string, dataset domain.Dataset) (domain.Dataset, []byte, []byte, error) {
	var err error
	if workspaceID == "" {
		workspaceID = dataset.WorkspaceID
	}
	dataset.WorkspaceID, err = s.defaultWorkspaceID(ctx, workspaceID)
	if err != nil {
		return domain.Dataset{}, nil, nil, err
	}
	if dataset.WorkspaceID == "" {
		return domain.Dataset{}, nil, nil, fmt.Errorf("store: dataset requires a workspace")
	}
	if dataset.ID == "" {
		dataset.ID, err = newID()
		if err != nil {
			return domain.Dataset{}, nil, nil, err
		}
	}
	dataset.RowCount = 0
	dataset.DeletedAt = nil
	dataset.Name = strings.TrimSpace(dataset.Name)
	if dataset.Slug == "" {
		dataset.Slug = slugifyDataset(dataset.Name)
	}
	dataset.Slug = strings.ToLower(strings.TrimSpace(dataset.Slug))
	dataset.Description = strings.TrimSpace(dataset.Description)
	if err := validateDatasetMetadata(dataset); err != nil {
		return domain.Dataset{}, nil, nil, err
	}
	schemaJSON, uniqueJSON, err := encodeDatasetMetadata(dataset)
	if err != nil {
		return domain.Dataset{}, nil, nil, err
	}
	now := s.now()
	dataset.CreatedAt = defaultTime(dataset.CreatedAt, now)
	dataset.UpdatedAt = defaultTime(dataset.UpdatedAt, dataset.CreatedAt)
	dataset.Schema = nonNilDatasetSchema(dataset.Schema)
	dataset.UniqueKey = nonNilStrings(dataset.UniqueKey)
	return dataset, schemaJSON, uniqueJSON, nil
}

func (s *Store) GetDataset(ctx context.Context, id string, includeDeleted ...bool) (domain.Dataset, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Dataset{}, err
	}
	return s.GetDatasetForWorkspace(ctx, workspaceID, id, len(includeDeleted) > 0 && includeDeleted[0])
}

func (s *Store) GetDatasetForWorkspace(ctx context.Context, workspaceID, id string, includeDeleted bool) (domain.Dataset, error) {
	query := `SELECT ` + datasetColumns + ` FROM datasets WHERE id = ? AND workspace_id = ?`
	if !includeDeleted {
		query += " AND deleted_at IS NULL"
	}
	return scanDataset(s.db.QueryRowContext(ctx, query, id, workspaceID))
}

func (s *Store) ListDatasets(ctx context.Context, filter DatasetFilter) ([]domain.Dataset, error) {
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + datasetColumns + ` FROM datasets WHERE workspace_id = ?`
	args := []any{workspaceID}
	if filter.DeletedOnly {
		query += " AND deleted_at IS NOT NULL"
	} else if !filter.IncludeDeleted {
		query += " AND deleted_at IS NULL"
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		query += " AND (lower(name) LIKE ? ESCAPE '\\' OR lower(description) LIKE ? ESCAPE '\\')"
		pattern := "%" + escapeLike(strings.ToLower(search)) + "%"
		args = append(args, pattern, pattern)
	}
	query += " ORDER BY updated_at DESC, id DESC"
	if filter.Limit > 0 {
		if filter.Limit > 1_000 {
			filter.Limit = 1_000
		}
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list datasets: %w", err)
	}
	defer rows.Close()
	var datasets []domain.Dataset
	for rows.Next() {
		dataset, err := scanDataset(rows)
		if err != nil {
			return nil, err
		}
		datasets = append(datasets, dataset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list datasets: %w", err)
	}
	if datasets == nil {
		datasets = []domain.Dataset{}
	}
	return datasets, nil
}

// UpdateDataset permits metadata edits and safe additive schema evolution.
// Existing column types cannot change and columns cannot be removed while
// rows exist, preventing silent reinterpretation or data loss.
func (s *Store) UpdateDataset(ctx context.Context, dataset domain.Dataset) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.UpdateDatasetForWorkspace(ctx, workspaceID, dataset)
}

func (s *Store) UpdateDatasetForWorkspace(ctx context.Context, workspaceID string, dataset domain.Dataset) error {
	current, err := s.GetDatasetForWorkspace(ctx, workspaceID, dataset.ID, false)
	if err != nil {
		return err
	}
	dataset.WorkspaceID = workspaceID
	dataset.Name = strings.TrimSpace(dataset.Name)
	dataset.Slug = strings.ToLower(strings.TrimSpace(dataset.Slug))
	dataset.Description = strings.TrimSpace(dataset.Description)
	if err := validateDatasetMetadata(dataset); err != nil {
		return err
	}
	if err := validateAdditiveDatasetSchema(current, dataset); err != nil {
		return err
	}
	schemaJSON, uniqueJSON, err := encodeDatasetMetadata(dataset)
	if err != nil {
		return err
	}
	dataset.UpdatedAt = defaultTime(dataset.UpdatedAt, s.now())
	result, err := s.db.ExecContext(ctx, `UPDATE datasets
SET name = ?, slug = ?, description = ?, schema_json = ?, unique_key_json = ?, updated_at = ?
WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, dataset.Name, dataset.Slug,
		dataset.Description, schemaJSON, uniqueJSON, formatTime(dataset.UpdatedAt), dataset.ID, workspaceID)
	if err != nil {
		return fmt.Errorf("store: update dataset: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update dataset result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("dataset %q: %w", dataset.ID, ErrNotFound)
	}
	return nil
}

func (s *Store) SoftDeleteDataset(ctx context.Context, id string, at time.Time) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.SoftDeleteDatasetForWorkspace(ctx, workspaceID, id, at)
}

func (s *Store) SoftDeleteDatasetForWorkspace(ctx context.Context, workspaceID, id string, at time.Time) error {
	at = defaultTime(at, s.now())
	result, err := s.db.ExecContext(ctx, `UPDATE datasets SET deleted_at = ?, updated_at = ?
WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, formatTime(at), formatTime(at), id, workspaceID)
	if err != nil {
		return fmt.Errorf("store: delete dataset: %w", err)
	}
	return expectDatasetMutation(result, id)
}

func (s *Store) RestoreDataset(ctx context.Context, id string) (domain.Dataset, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Dataset{}, err
	}
	return s.RestoreDatasetForWorkspace(ctx, workspaceID, id)
}

func (s *Store) RestoreDatasetForWorkspace(ctx context.Context, workspaceID, id string) (domain.Dataset, error) {
	now := s.now()
	result, err := s.db.ExecContext(ctx, `UPDATE datasets SET deleted_at = NULL, updated_at = ?
WHERE id = ? AND workspace_id = ? AND deleted_at IS NOT NULL`, formatTime(now), id, workspaceID)
	if err != nil {
		return domain.Dataset{}, fmt.Errorf("store: restore dataset: %w", err)
	}
	if err := expectDatasetMutation(result, id); err != nil {
		return domain.Dataset{}, err
	}
	return s.GetDatasetForWorkspace(ctx, workspaceID, id, false)
}

// PurgeDataset is intentionally explicit and scoped; API deletion uses the
// recoverable soft-delete path and does not expose purge.
func (s *Store) PurgeDataset(ctx context.Context, workspaceID, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM datasets
WHERE id = ? AND workspace_id = ? AND deleted_at IS NOT NULL`, id, workspaceID)
	if err != nil {
		return fmt.Errorf("store: purge dataset: %w", err)
	}
	return expectDatasetMutation(result, id)
}

func (s *Store) BulkWriteDatasetRows(ctx context.Context, datasetID string, rows []map[string]any, mode DatasetWriteMode) (DatasetBulkResult, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return DatasetBulkResult{}, err
	}
	return s.BulkWriteDatasetRowsForWorkspace(ctx, workspaceID, datasetID, rows, mode)
}

func (s *Store) BulkWriteDatasetRowsForWorkspace(ctx context.Context, workspaceID, datasetID string, rows []map[string]any, mode DatasetWriteMode) (DatasetBulkResult, error) {
	if len(rows) == 0 || len(rows) > MaximumDatasetBulkRows {
		return DatasetBulkResult{}, fmt.Errorf("store: dataset bulk write requires 1-%d rows", MaximumDatasetBulkRows)
	}
	if mode == "" {
		mode = DatasetInsert
	}
	if mode != DatasetInsert && mode != DatasetUpsert {
		return DatasetBulkResult{}, fmt.Errorf("store: invalid dataset write mode %q", mode)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DatasetBulkResult{}, fmt.Errorf("store: begin dataset bulk write: %w", err)
	}
	defer tx.Rollback()
	dataset, err := getDatasetTx(ctx, tx, workspaceID, datasetID, false)
	if err != nil {
		return DatasetBulkResult{}, err
	}
	if len(dataset.Schema) == 0 {
		dataset.Schema, err = inferDatasetSchema(rows)
		if err != nil {
			return DatasetBulkResult{}, err
		}
		schemaJSON, uniqueJSON, _ := encodeDatasetMetadata(dataset)
		if _, err := tx.ExecContext(ctx, `UPDATE datasets SET schema_json = ?, unique_key_json = ?, updated_at = ?
WHERE id = ? AND workspace_id = ?`, schemaJSON, uniqueJSON, formatTime(s.now()), dataset.ID, workspaceID); err != nil {
			return DatasetBulkResult{}, fmt.Errorf("store: persist inferred dataset schema: %w", err)
		}
	}
	if mode == DatasetUpsert && len(dataset.UniqueKey) == 0 {
		return DatasetBulkResult{}, fmt.Errorf("store: dataset upsert requires a unique key")
	}
	result := DatasetBulkResult{Rows: make([]domain.DatasetRow, 0, len(rows))}
	for _, values := range rows {
		normalized, err := validateDatasetRow(dataset.Schema, values, false)
		if err != nil {
			return DatasetBulkResult{}, err
		}
		encoded, search, fingerprint, err := encodeDatasetRow(dataset, normalized)
		if err != nil {
			return DatasetBulkResult{}, err
		}
		now := s.now()
		var row domain.DatasetRow
		if mode == DatasetUpsert {
			var existed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM dataset_rows WHERE dataset_id = ? AND unique_fingerprint = ?)`, dataset.ID, fingerprint).Scan(&existed); err != nil {
				return DatasetBulkResult{}, fmt.Errorf("store: inspect dataset upsert: %w", err)
			}
			row, err = scanDatasetRow(tx.QueryRowContext(ctx, `
INSERT INTO dataset_rows(dataset_id, values_json, search_text, unique_fingerprint, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(dataset_id, unique_fingerprint) WHERE unique_fingerprint <> ''
DO UPDATE SET values_json = excluded.values_json, search_text = excluded.search_text, updated_at = excluded.updated_at
RETURNING `+datasetRowColumns, dataset.ID, encoded, search, fingerprint, formatTime(now), formatTime(now)))
			if err == nil {
				if existed {
					result.Updated++
				} else {
					result.Inserted++
				}
			}
		} else {
			row, err = scanDatasetRow(tx.QueryRowContext(ctx, `
INSERT INTO dataset_rows(dataset_id, values_json, search_text, unique_fingerprint, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING `+datasetRowColumns,
				dataset.ID, encoded, search, fingerprint, formatTime(now), formatTime(now)))
			if err == nil {
				result.Inserted++
			}
		}
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return DatasetBulkResult{}, fmt.Errorf("%w: duplicate dataset unique key", ErrDatasetConflict)
			}
			return DatasetBulkResult{}, err
		}
		result.Rows = append(result.Rows, row)
	}
	if result.Inserted > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE datasets SET row_count = row_count + ?, updated_at = ?
WHERE id = ?`, result.Inserted, formatTime(s.now()), dataset.ID); err != nil {
			return DatasetBulkResult{}, fmt.Errorf("store: update dataset row count: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return DatasetBulkResult{}, fmt.Errorf("store: commit dataset bulk write: %w", err)
	}
	return result, nil
}

// ValidateDatasetRowsForWorkspace performs the same schema, unique-key, and
// encoded-size validation as a bulk write without mutating any row.
func (s *Store) ValidateDatasetRowsForWorkspace(ctx context.Context, workspaceID, datasetID string, rows []map[string]any, mode DatasetWriteMode) error {
	if len(rows) == 0 || len(rows) > MaximumDatasetBulkRows {
		return fmt.Errorf("store: dataset bulk write requires 1-%d rows", MaximumDatasetBulkRows)
	}
	if mode != DatasetInsert && mode != DatasetUpsert {
		return fmt.Errorf("store: invalid dataset write mode %q", mode)
	}
	dataset, err := s.GetDatasetForWorkspace(ctx, workspaceID, datasetID, false)
	if err != nil {
		return err
	}
	if len(dataset.Schema) == 0 {
		return fmt.Errorf("store: dataset schema is required before validated autonomous writes")
	}
	if mode == DatasetUpsert && len(dataset.UniqueKey) == 0 {
		return fmt.Errorf("store: dataset upsert requires a unique key")
	}
	for _, values := range rows {
		normalized, err := validateDatasetRow(dataset.Schema, values, false)
		if err != nil {
			return err
		}
		if _, _, _, err := encodeDatasetRow(dataset, normalized); err != nil {
			return err
		}
	}
	return nil
}

// ValidateNewDatasetForWorkspace applies the exact metadata validation and
// checks the workspace slug constraint without creating a record.
func (s *Store) ValidateNewDatasetForWorkspace(ctx context.Context, workspaceID string, dataset domain.Dataset) error {
	dataset.WorkspaceID = workspaceID
	dataset.Name = strings.TrimSpace(dataset.Name)
	if dataset.Slug == "" {
		dataset.Slug = slugifyDataset(dataset.Name)
	}
	dataset.Slug = strings.ToLower(strings.TrimSpace(dataset.Slug))
	dataset.Description = strings.TrimSpace(dataset.Description)
	if err := validateDatasetMetadata(dataset); err != nil {
		return err
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM datasets WHERE workspace_id = ? AND slug = ? AND deleted_at IS NULL)`, workspaceID, dataset.Slug).Scan(&exists); err != nil {
		return fmt.Errorf("store: inspect dataset slug: %w", err)
	}
	if exists {
		return fmt.Errorf("%w: dataset slug %q already exists", ErrDatasetConflict, dataset.Slug)
	}
	return nil
}

// ValidateNewDatasetWithRowsForWorkspace applies the same metadata, row, and
// unique-key checks as the atomic create boundary without mutating storage.
func (s *Store) ValidateNewDatasetWithRowsForWorkspace(ctx context.Context, workspaceID string, dataset domain.Dataset, rows []map[string]any) error {
	if len(rows) == 0 {
		return s.ValidateNewDatasetForWorkspace(ctx, workspaceID, dataset)
	}
	if len(rows) > MaximumDatasetBulkRows {
		return fmt.Errorf("store: dataset create with rows requires 1-%d rows", MaximumDatasetBulkRows)
	}
	if len(dataset.Schema) == 0 {
		inferred, err := inferDatasetSchema(rows)
		if err != nil {
			return err
		}
		dataset.Schema = inferred
	}
	if err := s.ValidateNewDatasetForWorkspace(ctx, workspaceID, dataset); err != nil {
		return err
	}
	seenFingerprints := make(map[string]struct{}, len(rows))
	for _, values := range rows {
		normalized, err := validateDatasetRow(dataset.Schema, values, false)
		if err != nil {
			return err
		}
		_, _, fingerprint, err := encodeDatasetRow(dataset, normalized)
		if err != nil {
			return err
		}
		if fingerprint != "" {
			if _, duplicate := seenFingerprints[fingerprint]; duplicate {
				return fmt.Errorf("%w: duplicate dataset unique key", ErrDatasetConflict)
			}
			seenFingerprints[fingerprint] = struct{}{}
		}
	}
	return nil
}

func (s *Store) UpdateDatasetRow(ctx context.Context, datasetID string, rowID int64, values map[string]any) (domain.DatasetRow, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.DatasetRow{}, err
	}
	return s.UpdateDatasetRowForWorkspace(ctx, workspaceID, datasetID, rowID, values)
}

func (s *Store) UpdateDatasetRowForWorkspace(ctx context.Context, workspaceID, datasetID string, rowID int64, values map[string]any) (domain.DatasetRow, error) {
	if len(values) == 0 {
		return domain.DatasetRow{}, fmt.Errorf("store: dataset row update requires at least one value")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DatasetRow{}, fmt.Errorf("store: begin dataset row update: %w", err)
	}
	defer tx.Rollback()
	dataset, err := getDatasetTx(ctx, tx, workspaceID, datasetID, false)
	if err != nil {
		return domain.DatasetRow{}, err
	}
	current, err := scanDatasetRow(tx.QueryRowContext(ctx, `SELECT `+datasetRowColumns+`
FROM dataset_rows WHERE id = ? AND dataset_id = ?`, rowID, datasetID))
	if err != nil {
		return domain.DatasetRow{}, err
	}
	for key, value := range values {
		current.Values[key] = value
	}
	normalized, err := validateDatasetRow(dataset.Schema, current.Values, false)
	if err != nil {
		return domain.DatasetRow{}, err
	}
	encoded, search, fingerprint, err := encodeDatasetRow(dataset, normalized)
	if err != nil {
		return domain.DatasetRow{}, err
	}
	now := s.now()
	updated, err := scanDatasetRow(tx.QueryRowContext(ctx, `UPDATE dataset_rows
SET values_json = ?, search_text = ?, unique_fingerprint = ?, updated_at = ?
WHERE id = ? AND dataset_id = ? RETURNING `+datasetRowColumns,
		encoded, search, fingerprint, formatTime(now), rowID, datasetID))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return domain.DatasetRow{}, fmt.Errorf("%w: duplicate dataset unique key", ErrDatasetConflict)
		}
		return domain.DatasetRow{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE datasets SET updated_at = ? WHERE id = ?", formatTime(now), datasetID); err != nil {
		return domain.DatasetRow{}, fmt.Errorf("store: touch dataset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.DatasetRow{}, fmt.Errorf("store: commit dataset row update: %w", err)
	}
	return updated, nil
}

func (s *Store) DeleteDatasetRow(ctx context.Context, datasetID string, rowID int64) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.DeleteDatasetRowForWorkspace(ctx, workspaceID, datasetID, rowID)
}

func (s *Store) GetDatasetRowForWorkspace(ctx context.Context, workspaceID, datasetID string, rowID int64) (domain.DatasetRow, error) {
	if _, err := s.GetDatasetForWorkspace(ctx, workspaceID, datasetID, false); err != nil {
		return domain.DatasetRow{}, err
	}
	return scanDatasetRow(s.db.QueryRowContext(ctx, `SELECT `+datasetRowColumns+`
FROM dataset_rows WHERE id = ? AND dataset_id = ?`, rowID, datasetID))
}

func (s *Store) DeleteDatasetRowForWorkspace(ctx context.Context, workspaceID, datasetID string, rowID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin delete dataset row: %w", err)
	}
	defer tx.Rollback()
	if _, err := getDatasetTx(ctx, tx, workspaceID, datasetID, false); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM dataset_rows WHERE id = ? AND dataset_id = ?", rowID, datasetID)
	if err != nil {
		return fmt.Errorf("store: delete dataset row: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete dataset row result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("dataset row %d: %w", rowID, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE datasets SET row_count = row_count - 1, updated_at = ? WHERE id = ?`,
		formatTime(s.now()), datasetID); err != nil {
		return fmt.Errorf("store: update dataset row count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit delete dataset row: %w", err)
	}
	return nil
}

func (s *Store) QueryDatasetRows(ctx context.Context, datasetID string, filter DatasetRowFilter) (DatasetRowPage, error) {
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return DatasetRowPage{}, err
	}
	dataset, err := s.GetDatasetForWorkspace(ctx, workspaceID, datasetID, false)
	if err != nil {
		return DatasetRowPage{}, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > MaximumDatasetPageSize {
		return DatasetRowPage{}, fmt.Errorf("store: dataset page limit exceeds %d", MaximumDatasetPageSize)
	}
	if len(filter.Search) > 8*1024 {
		return DatasetRowPage{}, fmt.Errorf("store: dataset search exceeds 8 KiB")
	}
	if len(filter.Filters) > MaximumDatasetColumns {
		return DatasetRowPage{}, fmt.Errorf("store: too many dataset filters")
	}
	sortColumn, err := datasetSortColumn(dataset.Schema, filter.Sort)
	if err != nil {
		return DatasetRowPage{}, err
	}
	query := `SELECT ` + datasetRowColumns + ` FROM dataset_rows WHERE dataset_id = ?`
	countQuery := `SELECT COUNT(*) FROM dataset_rows WHERE dataset_id = ?`
	args := []any{datasetID}
	countArgs := []any{datasetID}
	if search := strings.TrimSpace(filter.Search); search != "" {
		clause := " AND search_text LIKE ? ESCAPE '\\'"
		pattern := "%" + escapeLike(strings.ToLower(search)) + "%"
		query += clause
		countQuery += clause
		args = append(args, pattern)
		countArgs = append(countArgs, pattern)
	}
	filterKeys := make([]string, 0, len(filter.Filters))
	for key := range filter.Filters {
		filterKeys = append(filterKeys, key)
	}
	sort.Strings(filterKeys)
	for _, key := range filterKeys {
		column, ok := findDatasetColumn(dataset.Schema, key)
		if !ok {
			return DatasetRowPage{}, fmt.Errorf("store: unknown dataset filter column %q", key)
		}
		rawValue := filter.Filters[key]
		value, err := parseDatasetFilterValue(column, rawValue)
		if err != nil {
			return DatasetRowPage{}, err
		}
		clause := " AND json_extract(values_json, '$." + key + "') = ?"
		if rawValue == "null" && column.Nullable {
			clause = " AND json_extract(values_json, '$." + key + "') IS NULL"
		} else if column.Type == domain.DatasetJSON {
			clause = " AND json(json_extract(values_json, '$." + key + "')) = json(?)"
		}
		query += clause
		countQuery += clause
		if !strings.Contains(clause, " IS NULL") {
			args = append(args, value)
			countArgs = append(countArgs, value)
		}
	}
	cursorOffset, err := decodeDatasetCursor(filter.Cursor)
	if err != nil {
		return DatasetRowPage{}, err
	}
	direction := "ASC"
	if filter.Descending {
		direction = "DESC"
	}
	query += " ORDER BY " + sortColumn + " " + direction + ", id " + direction + " LIMIT ? OFFSET ?"
	args = append(args, filter.Limit+1, cursorOffset)
	var total int64
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return DatasetRowPage{}, fmt.Errorf("store: count dataset rows: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return DatasetRowPage{}, fmt.Errorf("store: query dataset rows: %w", err)
	}
	defer rows.Close()
	page := DatasetRowPage{Rows: []domain.DatasetRow{}, Total: total}
	for rows.Next() {
		row, err := scanDatasetRow(rows)
		if err != nil {
			return DatasetRowPage{}, err
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return DatasetRowPage{}, fmt.Errorf("store: query dataset rows: %w", err)
	}
	if len(page.Rows) > filter.Limit {
		page.Rows = page.Rows[:filter.Limit]
		page.NextCursor = encodeDatasetCursor(cursorOffset + int64(filter.Limit))
	}
	return page, nil
}

func (s *Store) StreamDatasetRows(ctx context.Context, datasetID string, batchSize int, consume func(domain.DatasetRow) error) error {
	if consume == nil {
		return fmt.Errorf("store: dataset row consumer is required")
	}
	if batchSize <= 0 || batchSize > MaximumDatasetPageSize {
		batchSize = MaximumDatasetPageSize
	}
	cursor := ""
	for {
		page, err := s.QueryDatasetRows(ctx, datasetID, DatasetRowFilter{Limit: batchSize, Cursor: cursor})
		if err != nil {
			return err
		}
		for _, row := range page.Rows {
			if err := consume(row); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

func getDatasetTx(ctx context.Context, tx *sql.Tx, workspaceID, id string, includeDeleted bool) (domain.Dataset, error) {
	query := `SELECT ` + datasetColumns + ` FROM datasets WHERE id = ? AND workspace_id = ?`
	if !includeDeleted {
		query += " AND deleted_at IS NULL"
	}
	return scanDataset(tx.QueryRowContext(ctx, query, id, workspaceID))
}

func scanDataset(row rowScanner) (domain.Dataset, error) {
	var dataset domain.Dataset
	var schemaJSON, uniqueJSON []byte
	var deleted sql.NullString
	var created, updated string
	if err := row.Scan(&dataset.ID, &dataset.WorkspaceID, &dataset.Name, &dataset.Slug, &dataset.Description,
		&schemaJSON, &uniqueJSON, &dataset.RowCount, &deleted, &created, &updated); err != nil {
		return domain.Dataset{}, fmt.Errorf("store: get dataset: %w", notFound("dataset", err))
	}
	if err := json.Unmarshal(schemaJSON, &dataset.Schema); err != nil {
		return domain.Dataset{}, fmt.Errorf("store: decode dataset schema: %w", err)
	}
	if err := json.Unmarshal(uniqueJSON, &dataset.UniqueKey); err != nil {
		return domain.Dataset{}, fmt.Errorf("store: decode dataset unique key: %w", err)
	}
	dataset.Schema = nonNilDatasetSchema(dataset.Schema)
	dataset.UniqueKey = nonNilStrings(dataset.UniqueKey)
	var err error
	dataset.DeletedAt, err = parseNullableTime(deleted)
	if err != nil {
		return domain.Dataset{}, err
	}
	dataset.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Dataset{}, err
	}
	dataset.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Dataset{}, err
	}
	return dataset, nil
}

func scanDatasetRow(row rowScanner) (domain.DatasetRow, error) {
	var datasetRow domain.DatasetRow
	var valuesJSON []byte
	var created, updated string
	if err := row.Scan(&datasetRow.ID, &valuesJSON, &created, &updated); err != nil {
		return domain.DatasetRow{}, fmt.Errorf("store: get dataset row: %w", notFound("dataset row", err))
	}
	decoder := json.NewDecoder(strings.NewReader(string(valuesJSON)))
	decoder.UseNumber()
	if err := decoder.Decode(&datasetRow.Values); err != nil {
		return domain.DatasetRow{}, fmt.Errorf("store: decode dataset row: %w", err)
	}
	var err error
	datasetRow.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.DatasetRow{}, err
	}
	datasetRow.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.DatasetRow{}, err
	}
	return datasetRow, nil
}

func validateDatasetMetadata(dataset domain.Dataset) error {
	if dataset.Name == "" || len(dataset.Name) > 160 {
		return fmt.Errorf("store: dataset name is required and must not exceed 160 bytes")
	}
	if !datasetSlug.MatchString(dataset.Slug) {
		return fmt.Errorf("store: invalid dataset slug %q", dataset.Slug)
	}
	if len(dataset.Description) > 8*1024 {
		return fmt.Errorf("store: dataset description exceeds 8 KiB")
	}
	if len(dataset.Schema) > MaximumDatasetColumns {
		return fmt.Errorf("store: dataset schema exceeds %d columns", MaximumDatasetColumns)
	}
	seen := make(map[string]domain.DatasetColumnType, len(dataset.Schema))
	for _, column := range dataset.Schema {
		if !datasetIdentifier.MatchString(column.Name) {
			return fmt.Errorf("store: invalid dataset column name %q", column.Name)
		}
		if !validDatasetColumnType(column.Type) {
			return fmt.Errorf("store: invalid type %q for dataset column %q", column.Type, column.Name)
		}
		if _, exists := seen[column.Name]; exists {
			return fmt.Errorf("store: duplicate dataset column %q", column.Name)
		}
		if len(column.Description) > 2*1024 {
			return fmt.Errorf("store: dataset column %q description exceeds 2 KiB", column.Name)
		}
		seen[column.Name] = column.Type
	}
	uniqueKeys := make(map[string]struct{}, len(dataset.UniqueKey))
	for _, key := range dataset.UniqueKey {
		if _, exists := seen[key]; !exists {
			return fmt.Errorf("store: unique key column %q is not in the dataset schema", key)
		}
		if _, duplicate := uniqueKeys[key]; duplicate {
			return fmt.Errorf("store: duplicate unique key column %q", key)
		}
		uniqueKeys[key] = struct{}{}
	}
	return nil
}

func validateAdditiveDatasetSchema(current, updated domain.Dataset) error {
	if current.RowCount == 0 {
		return nil
	}
	if len(updated.Schema) < len(current.Schema) {
		return fmt.Errorf("store: dataset columns cannot be removed while rows exist")
	}
	updatedTypes := make(map[string]domain.DatasetColumn, len(updated.Schema))
	for _, column := range updated.Schema {
		updatedTypes[column.Name] = column
	}
	for index, column := range current.Schema {
		candidate, exists := updatedTypes[column.Name]
		if !exists {
			return fmt.Errorf("store: dataset column %q cannot be removed while rows exist", column.Name)
		}
		if candidate.Type != column.Type {
			return fmt.Errorf("store: dataset column %q type cannot change", column.Name)
		}
		if candidate.Nullable != column.Nullable {
			return fmt.Errorf("store: dataset column %q nullability cannot change while rows exist", column.Name)
		}
		if updated.Schema[index].Name != column.Name {
			return fmt.Errorf("store: existing dataset column order cannot change")
		}
	}
	for _, column := range updated.Schema[len(current.Schema):] {
		if !column.Nullable {
			return fmt.Errorf("store: added dataset column %q must be nullable", column.Name)
		}
	}
	if !equalStringSlices(current.UniqueKey, updated.UniqueKey) {
		return fmt.Errorf("store: dataset unique key cannot change while rows exist")
	}
	return nil
}

func encodeDatasetMetadata(dataset domain.Dataset) ([]byte, []byte, error) {
	schema, err := json.Marshal(nonNilDatasetSchema(dataset.Schema))
	if err != nil {
		return nil, nil, fmt.Errorf("store: encode dataset schema: %w", err)
	}
	unique, err := json.Marshal(nonNilStrings(dataset.UniqueKey))
	if err != nil {
		return nil, nil, fmt.Errorf("store: encode dataset unique key: %w", err)
	}
	return schema, unique, nil
}

func inferDatasetSchema(rows []map[string]any) ([]domain.DatasetColumn, error) {
	types := make(map[string]domain.DatasetColumnType)
	nullable := make(map[string]bool)
	appearances := make(map[string]int)
	for _, row := range rows {
		for key, value := range row {
			appearances[key]++
			if !datasetIdentifier.MatchString(key) {
				return nil, fmt.Errorf("store: invalid dataset column name %q", key)
			}
			inferred, isNull, err := inferDatasetValueType(value)
			if err != nil {
				return nil, fmt.Errorf("store: infer dataset column %q: %w", key, err)
			}
			if isNull {
				nullable[key] = true
				continue
			}
			if current, exists := types[key]; exists {
				merged, ok := mergeDatasetTypes(current, inferred)
				if !ok {
					return nil, fmt.Errorf("store: inconsistent inferred values for column %q", key)
				}
				types[key] = merged
			} else {
				types[key] = inferred
			}
		}
	}
	keys := make([]string, 0, len(types))
	for key := range types {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 || len(keys) > MaximumDatasetColumns {
		return nil, fmt.Errorf("store: inferred dataset requires 1-%d columns", MaximumDatasetColumns)
	}
	schema := make([]domain.DatasetColumn, 0, len(keys))
	for _, key := range keys {
		schema = append(schema, domain.DatasetColumn{Name: key, Type: types[key], Nullable: nullable[key] || appearances[key] < len(rows)})
	}
	return schema, nil
}

func validateDatasetRow(schema []domain.DatasetColumn, values map[string]any, partial bool) (map[string]any, error) {
	if values == nil {
		return nil, fmt.Errorf("store: dataset row values are required")
	}
	columns := make(map[string]domain.DatasetColumn, len(schema))
	for _, column := range schema {
		columns[column.Name] = column
	}
	for key := range values {
		if _, exists := columns[key]; !exists {
			return nil, fmt.Errorf("store: unknown dataset column %q", key)
		}
	}
	if len(values) > MaximumDatasetColumns {
		return nil, fmt.Errorf("store: dataset row exceeds %d columns", MaximumDatasetColumns)
	}
	normalized := make(map[string]any, len(schema))
	for _, column := range schema {
		value, exists := values[column.Name]
		if !exists {
			if partial {
				continue
			}
			if !column.Nullable {
				return nil, fmt.Errorf("store: dataset column %q is required", column.Name)
			}
			normalized[column.Name] = nil
			continue
		}
		value, err := normalizeDatasetValue(column, value)
		if err != nil {
			return nil, err
		}
		normalized[column.Name] = value
	}
	return normalized, nil
}

func normalizeDatasetValue(column domain.DatasetColumn, value any) (any, error) {
	if value == nil {
		if column.Nullable {
			return nil, nil
		}
		return nil, fmt.Errorf("store: dataset column %q cannot be null", column.Name)
	}
	switch column.Type {
	case domain.DatasetString:
		text, ok := value.(string)
		if !ok || len(text) > MaximumDatasetCellBytes {
			return nil, fmt.Errorf("store: dataset column %q requires a bounded string", column.Name)
		}
		return text, nil
	case domain.DatasetInteger:
		number, ok := jsonNumber(value)
		if !ok || math.Trunc(number) != number || number < math.MinInt64 || number > math.MaxInt64 {
			return nil, fmt.Errorf("store: dataset column %q requires an integer", column.Name)
		}
		return int64(number), nil
	case domain.DatasetNumber:
		number, ok := jsonNumber(value)
		if !ok || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, fmt.Errorf("store: dataset column %q requires a finite number", column.Name)
		}
		return number, nil
	case domain.DatasetBoolean:
		boolean, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("store: dataset column %q requires a boolean", column.Name)
		}
		return boolean, nil
	case domain.DatasetDatetime:
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("store: dataset column %q requires an RFC3339 datetime", column.Name)
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil, fmt.Errorf("store: dataset column %q requires an RFC3339 datetime", column.Name)
		}
		return formatTime(parsed), nil
	case domain.DatasetJSON:
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > MaximumDatasetCellBytes {
			return nil, fmt.Errorf("store: dataset column %q requires bounded JSON", column.Name)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("store: unsupported dataset column type %q", column.Type)
	}
}

func encodeDatasetRow(dataset domain.Dataset, values map[string]any) ([]byte, string, string, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, "", "", fmt.Errorf("store: encode dataset row: %w", err)
	}
	if len(encoded) > MaximumDatasetColumns*MaximumDatasetCellBytes {
		return nil, "", "", fmt.Errorf("store: dataset row is too large")
	}
	parts := make([]string, 0, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		switch typed := value.(type) {
		case string:
			parts = append(parts, strings.ToLower(typed))
		case nil:
		default:
			text, _ := json.Marshal(typed)
			parts = append(parts, strings.ToLower(string(text)))
		}
	}
	fingerprint := ""
	if len(dataset.UniqueKey) > 0 {
		unique := make([]any, 0, len(dataset.UniqueKey))
		for _, key := range dataset.UniqueKey {
			value, exists := values[key]
			if !exists || value == nil {
				return nil, "", "", fmt.Errorf("store: dataset unique key column %q is required", key)
			}
			unique = append(unique, value)
		}
		uniqueJSON, _ := json.Marshal(unique)
		sum := sha256.Sum256(uniqueJSON)
		fingerprint = hex.EncodeToString(sum[:])
	}
	return encoded, strings.Join(parts, "\n"), fingerprint, nil
}

func datasetSortColumn(schema []domain.DatasetColumn, value string) (string, error) {
	switch value {
	case "", "id":
		return "id", nil
	case "created_at":
		return "created_at", nil
	case "updated_at":
		return "updated_at", nil
	}
	if _, exists := findDatasetColumn(schema, value); !exists {
		return "", fmt.Errorf("store: unknown dataset sort column %q", value)
	}
	return "json_extract(values_json, '$." + value + "')", nil
}

func parseDatasetFilterValue(column domain.DatasetColumn, raw string) (any, error) {
	if raw == "null" && column.Nullable {
		return nil, nil
	}
	switch column.Type {
	case domain.DatasetString, domain.DatasetDatetime:
		value, err := normalizeDatasetValue(column, raw)
		return value, err
	case domain.DatasetInteger:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("store: invalid integer filter for %q", column.Name)
		}
		return value, nil
	case domain.DatasetNumber:
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("store: invalid number filter for %q", column.Name)
		}
		return value, nil
	case domain.DatasetBoolean:
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("store: invalid boolean filter for %q", column.Name)
		}
		return value, nil
	case domain.DatasetJSON:
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("store: invalid JSON filter for %q", column.Name)
		}
		encoded, _ := json.Marshal(value)
		return string(encoded), nil
	default:
		return nil, fmt.Errorf("store: invalid filter column %q", column.Name)
	}
}

func inferDatasetValueType(value any) (domain.DatasetColumnType, bool, error) {
	switch typed := value.(type) {
	case nil:
		return "", true, nil
	case string:
		if len(typed) > MaximumDatasetCellBytes {
			return "", false, fmt.Errorf("string exceeds %d bytes", MaximumDatasetCellBytes)
		}
		return domain.DatasetString, false, nil
	case bool:
		return domain.DatasetBoolean, false, nil
	case float64:
		if math.Trunc(typed) == typed {
			return domain.DatasetInteger, false, nil
		}
		return domain.DatasetNumber, false, nil
	case json.Number:
		if _, err := typed.Int64(); err == nil {
			return domain.DatasetInteger, false, nil
		}
		if _, err := typed.Float64(); err == nil {
			return domain.DatasetNumber, false, nil
		}
		return "", false, fmt.Errorf("invalid number")
	case map[string]any, []any:
		return domain.DatasetJSON, false, nil
	default:
		return "", false, fmt.Errorf("unsupported value type %T", value)
	}
}

func mergeDatasetTypes(left, right domain.DatasetColumnType) (domain.DatasetColumnType, bool) {
	if left == right {
		return left, true
	}
	if (left == domain.DatasetInteger && right == domain.DatasetNumber) || (left == domain.DatasetNumber && right == domain.DatasetInteger) {
		return domain.DatasetNumber, true
	}
	return "", false
}

func validDatasetColumnType(value domain.DatasetColumnType) bool {
	switch value {
	case domain.DatasetString, domain.DatasetInteger, domain.DatasetNumber, domain.DatasetBoolean,
		domain.DatasetDatetime, domain.DatasetJSON:
		return true
	default:
		return false
	}
}

func findDatasetColumn(schema []domain.DatasetColumn, name string) (domain.DatasetColumn, bool) {
	for _, column := range schema {
		if column.Name == name {
			return column, true
		}
	}
	return domain.DatasetColumn{}, false
}

func jsonNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		if typed > 1<<53 {
			return 0, false
		}
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func expectDatasetMutation(result sql.Result, id string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: dataset mutation result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("dataset %q: %w", id, ErrNotFound)
	}
	return nil
}

func slugifyDataset(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	dash := false
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			builder.WriteRune(char)
			dash = false
		} else if builder.Len() > 0 && !dash {
			builder.WriteByte('-')
			dash = true
		}
		if builder.Len() >= 64 {
			break
		}
	}
	return strings.Trim(builder.String(), "-")
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}

func encodeDatasetCursor(offset int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(offset, 10)))
}

func decodeDatasetCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, fmt.Errorf("store: invalid dataset cursor")
	}
	offset, err := strconv.ParseInt(string(decoded), 10, 64)
	if err != nil || offset < 1 {
		return 0, fmt.Errorf("store: invalid dataset cursor")
	}
	return offset, nil
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func nonNilDatasetSchema(value []domain.DatasetColumn) []domain.DatasetColumn {
	if value == nil {
		return []domain.DatasetColumn{}
	}
	return value
}

func nonNilStrings(value []string) []string {
	if value == nil {
		return []string{}
	}
	return value
}
