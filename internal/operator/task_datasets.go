package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

// applyTaskDatasetWrites applies already structurally bounded runner output at
// the final workspace-scoped persistence boundary. Row values are cleared from
// the durable RunResult after application; evidence retains only IDs/counts.
func (o *Operator) applyTaskDatasetWrites(ctx context.Context, task domain.Task, runID string, result *domain.RunResult) error {
	if result == nil || len(result.DatasetWrites) == 0 {
		return nil
	}
	if task.WorkspaceID == "" {
		return fmt.Errorf("dataset writes require an explicit task workspace")
	}
	createdSlugs := make(map[string]struct{})
	var deletions []datasetDeletionTarget
	for index := range result.DatasetWrites {
		if (result.DatasetWrites[index].Operation == domain.DatasetWriteUpsert || result.DatasetWrites[index].Operation == domain.DatasetWriteCreate) && result.DatasetWrites[index].RowsFile != "" {
			rows, err := loadTaskDatasetRowsFile(task, result.DatasetWrites[index].RowsFile)
			if err != nil {
				return fmt.Errorf("dataset write %d: %w", index, err)
			}
			result.DatasetWrites[index].Rows = rows
		}
	}
	// Validate every write against its authoritative workspace before applying
	// any mutation, preventing malformed later writes from causing a partial
	// batch. The operator loop serializes autonomous task application.
	for index := range result.DatasetWrites {
		write := &result.DatasetWrites[index]
		switch write.Operation {
		case domain.DatasetWriteCreate:
			if write.Dataset == nil {
				return fmt.Errorf("dataset write %d omitted dataset metadata", index)
			}
			if _, duplicate := createdSlugs[write.Dataset.Slug]; duplicate {
				return fmt.Errorf("dataset write %d duplicates proposed dataset slug %q", index, write.Dataset.Slug)
			}
			if err := o.store.ValidateNewDatasetWithRowsForWorkspace(ctx, task.WorkspaceID, *write.Dataset, write.Rows); err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			createdSlugs[write.Dataset.Slug] = struct{}{}
		case domain.DatasetWriteUpsert:
			if _, err := o.store.GetDatasetForWorkspace(ctx, task.WorkspaceID, write.DatasetID, false); err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			if err := o.store.ValidateDatasetRowsForWorkspace(ctx, task.WorkspaceID, write.DatasetID, write.Rows, store.DatasetUpsert); err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
		case domain.DatasetWriteUpdate:
			row, err := o.store.GetDatasetRowForWorkspace(ctx, task.WorkspaceID, write.DatasetID, write.RowID)
			if err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			merged := make(map[string]any, len(row.Values))
			for key, value := range row.Values {
				merged[key] = value
			}
			for key, value := range write.Values {
				merged[key] = value
			}
			if err := o.store.ValidateDatasetRowsForWorkspace(ctx, task.WorkspaceID, write.DatasetID, []map[string]any{merged}, store.DatasetInsert); err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
		case domain.DatasetWriteDelete:
			if _, err := o.store.GetDatasetRowForWorkspace(ctx, task.WorkspaceID, write.DatasetID, write.RowID); err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			deletions = append(deletions, datasetDeletionTarget{DatasetID: write.DatasetID, RowID: write.RowID})
		default:
			return fmt.Errorf("dataset write %d has unsupported operation %q", index, write.Operation)
		}
	}
	for index := range result.DatasetWrites {
		write := &result.DatasetWrites[index]
		switch write.Operation {
		case domain.DatasetWriteCreate:
			if write.Dataset == nil {
				return fmt.Errorf("dataset write %d omitted dataset metadata", index)
			}
			requested := *write.Dataset
			requested.WorkspaceID = task.WorkspaceID
			var (
				created domain.Dataset
				written store.DatasetBulkResult
				err     error
			)
			if len(write.Rows) > 0 {
				created, written, err = o.store.CreateDatasetWithRowsForWorkspace(ctx, task.WorkspaceID, requested, write.Rows)
			} else {
				created, err = o.store.CreateDatasetForWorkspace(ctx, task.WorkspaceID, requested)
			}
			if err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			write.DatasetID = created.ID
			write.Dataset = &domain.Dataset{
				ID: created.ID, WorkspaceID: created.WorkspaceID, Name: created.Name, Slug: created.Slug,
				Description: created.Description, Schema: created.Schema, UniqueKey: created.UniqueKey, RowCount: created.RowCount,
			}
			write.Applied, write.Inserted = true, written.Inserted
			write.Rows = nil
			o.emitForWorkspace(ctx, task.WorkspaceID, "dataset.created", created.ID, map[string]any{
				"task_id": task.ID, "run_id": runID, "name": created.Name, "columns": len(created.Schema), "rows": written.Inserted, "source": "task_result",
			})
		case domain.DatasetWriteUpsert:
			dataset, err := o.store.GetDatasetForWorkspace(ctx, task.WorkspaceID, write.DatasetID, false)
			if err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			written, err := o.store.BulkWriteDatasetRowsForWorkspace(ctx, task.WorkspaceID, dataset.ID, write.Rows, store.DatasetUpsert)
			if err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			write.Applied, write.Inserted, write.Updated = true, written.Inserted, written.Updated
			write.Rows = nil
			o.emitForWorkspace(ctx, task.WorkspaceID, "dataset.rows.written", dataset.ID, map[string]any{
				"task_id": task.ID, "run_id": runID, "inserted": written.Inserted, "updated": written.Updated, "source": "task_result",
			})
		case domain.DatasetWriteUpdate:
			row, err := o.store.UpdateDatasetRowForWorkspace(ctx, task.WorkspaceID, write.DatasetID, write.RowID, write.Values)
			if err != nil {
				return fmt.Errorf("dataset write %d: %w", index, translateDatasetError(err))
			}
			write.Applied = true
			write.Values = nil
			o.emitForWorkspace(ctx, task.WorkspaceID, "dataset.row.updated", write.DatasetID, map[string]any{"row_id": row.ID, "task_id": task.ID, "run_id": runID, "source": "task_result"})
		case domain.DatasetWriteDelete:
			// Applied below as one approval after all non-destructive writes.
		default:
			return fmt.Errorf("dataset write %d has unsupported operation %q", index, write.Operation)
		}
	}
	if len(deletions) > 0 {
		approval, err := o.requestDatasetDeletionApproval(ctx, task.WorkspaceID, task.ID, runID, deletions)
		if err != nil {
			return err
		}
		for index := range result.DatasetWrites {
			if result.DatasetWrites[index].Operation == domain.DatasetWriteDelete {
				result.DatasetWrites[index].Applied = false
				result.DatasetWrites[index].Values = nil
			}
		}
		result.Status = "needs_approval"
		message := "Approval " + approval.ID + " is required before deleting the exact dataset row(s)."
		result.ApprovalNeeded = &message
	}
	return nil
}

func loadTaskDatasetRowsFile(task domain.Task, relative string) ([]map[string]any, error) {
	if task.Workspace == nil || strings.TrimSpace(task.Workspace.Path) == "" {
		return nil, fmt.Errorf("rows_file requires an explicit task workspace")
	}
	root, err := filepath.EvalSymlinks(task.Workspace.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return nil, fmt.Errorf("resolve rows_file: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve rows_file: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("rows_file escapes the approved workspace")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open rows_file: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, runner.MaxRunDatasetRowsFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read rows_file: %w", err)
	}
	if len(content) > runner.MaxRunDatasetRowsFileBytes {
		return nil, fmt.Errorf("rows_file exceeds %d bytes", runner.MaxRunDatasetRowsFileBytes)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.UseNumber()
	var rows []map[string]any
	if err := decoder.Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode rows_file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("rows_file must contain exactly one JSON array")
	}
	if len(rows) == 0 || len(rows) > runner.MaxRunDatasetRowsFileRows {
		return nil, fmt.Errorf("rows_file requires 1-%d rows", runner.MaxRunDatasetRowsFileRows)
	}
	return rows, nil
}

func taskRequiresDatasetRows(task domain.Task) bool {
	parts := []string{task.Title, task.Purpose, task.Why}
	for _, item := range task.DefinitionOfDone {
		parts = append(parts, item.Text)
	}
	text := strings.ToLower(strings.Join(parts, " "))
	if !strings.Contains(text, "dataset") {
		return false
	}
	for _, marker := range []string{"upsert", " rows", "row count", "populate", "queryable", "catalog into", "stored in"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func resultIncludesDatasetRows(result domain.RunResult) bool {
	for _, write := range result.DatasetWrites {
		if (write.Operation == domain.DatasetWriteUpsert || write.Operation == domain.DatasetWriteCreate) && (len(write.Rows) > 0 || write.RowsFile != "") {
			return true
		}
	}
	return false
}

func clearDatasetWriteRows(writes []domain.DatasetWrite) {
	for index := range writes {
		for rowIndex := range writes[index].Rows {
			for key := range writes[index].Rows[rowIndex] {
				delete(writes[index].Rows[rowIndex], key)
			}
		}
		writes[index].Rows = nil
		for key := range writes[index].Values {
			delete(writes[index].Values, key)
		}
		writes[index].Values = nil
	}
}
