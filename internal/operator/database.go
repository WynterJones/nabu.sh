package operator

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func (o *Operator) Datasets(ctx context.Context, filter store.DatasetFilter) ([]domain.Dataset, error) {
	filter.WorkspaceID = ""
	return o.store.ListDatasets(ctx, filter)
}

func (o *Operator) Dataset(ctx context.Context, id string, includeDeleted bool) (domain.Dataset, error) {
	dataset, err := o.store.GetDataset(ctx, id, includeDeleted)
	return dataset, translateDatasetError(err)
}

func (o *Operator) CreateDataset(ctx context.Context, input api.DatasetCreate) (domain.Dataset, error) {
	dataset, err := o.store.CreateDataset(ctx, domain.Dataset{
		Name: input.Name, Slug: input.Slug, Description: input.Description,
		Schema: input.Schema, UniqueKey: input.UniqueKey,
	})
	if err != nil {
		return domain.Dataset{}, translateDatasetError(err)
	}
	o.emitForWorkspace(ctx, dataset.WorkspaceID, "dataset.created", dataset.ID, dataset)
	return dataset, nil
}

func (o *Operator) UpdateDataset(ctx context.Context, id string, input api.DatasetUpdate) (domain.Dataset, error) {
	dataset, err := o.store.GetDataset(ctx, id)
	if err != nil {
		return domain.Dataset{}, translateDatasetError(err)
	}
	if input.Name != nil {
		dataset.Name = *input.Name
	}
	if input.Slug != nil {
		dataset.Slug = *input.Slug
	}
	if input.Description != nil {
		dataset.Description = *input.Description
	}
	if input.Schema != nil {
		dataset.Schema = *input.Schema
	}
	if input.UniqueKey != nil {
		dataset.UniqueKey = *input.UniqueKey
	}
	dataset.UpdatedAt = time.Now().UTC()
	if err := o.store.UpdateDataset(ctx, dataset); err != nil {
		return domain.Dataset{}, translateDatasetError(err)
	}
	updated, err := o.store.GetDataset(ctx, id)
	if err != nil {
		return domain.Dataset{}, translateDatasetError(err)
	}
	o.emitForWorkspace(ctx, updated.WorkspaceID, "dataset.updated", updated.ID, updated)
	return updated, nil
}

func (o *Operator) DeleteDataset(ctx context.Context, id string) error {
	dataset, err := o.store.GetDataset(ctx, id)
	if err != nil {
		return translateDatasetError(err)
	}
	if err := o.store.SoftDeleteDataset(ctx, id, time.Now().UTC()); err != nil {
		return translateDatasetError(err)
	}
	o.emitForWorkspace(ctx, dataset.WorkspaceID, "dataset.deleted", dataset.ID, nil)
	return nil
}

func (o *Operator) RestoreDataset(ctx context.Context, id string) (domain.Dataset, error) {
	dataset, err := o.store.RestoreDataset(ctx, id)
	if err != nil {
		return domain.Dataset{}, translateDatasetError(err)
	}
	o.emitForWorkspace(ctx, dataset.WorkspaceID, "dataset.restored", dataset.ID, dataset)
	return dataset, nil
}

func (o *Operator) QueryDatasetRows(ctx context.Context, id string, filter store.DatasetRowFilter) (store.DatasetRowPage, error) {
	filter.WorkspaceID = ""
	page, err := o.store.QueryDatasetRows(ctx, id, filter)
	return page, translateDatasetError(err)
}

func (o *Operator) BulkDatasetRows(ctx context.Context, id string, input api.DatasetBulkInput) (store.DatasetBulkResult, error) {
	dataset, err := o.store.GetDataset(ctx, id)
	if err != nil {
		return store.DatasetBulkResult{}, translateDatasetError(err)
	}
	result, err := o.store.BulkWriteDatasetRows(ctx, id, input.Rows, input.Mode)
	if err != nil {
		return store.DatasetBulkResult{}, translateDatasetError(err)
	}
	o.emitForWorkspace(ctx, dataset.WorkspaceID, "dataset.rows.written", dataset.ID,
		map[string]int{"inserted": result.Inserted, "updated": result.Updated})
	return result, nil
}

func (o *Operator) UpdateDatasetRow(ctx context.Context, id string, rowID int64, input api.DatasetRowUpdate) (domain.DatasetRow, error) {
	dataset, err := o.store.GetDataset(ctx, id)
	if err != nil {
		return domain.DatasetRow{}, translateDatasetError(err)
	}
	row, err := o.store.UpdateDatasetRow(ctx, id, rowID, input.Values)
	if err != nil {
		return domain.DatasetRow{}, translateDatasetError(err)
	}
	o.emitForWorkspace(ctx, dataset.WorkspaceID, "dataset.row.updated", dataset.ID, map[string]int64{"row_id": rowID})
	return row, nil
}

func (o *Operator) DeleteDatasetRow(ctx context.Context, id string, rowID int64) error {
	dataset, err := o.store.GetDataset(ctx, id)
	if err != nil {
		return translateDatasetError(err)
	}
	if err := o.store.DeleteDatasetRow(ctx, id, rowID); err != nil {
		return translateDatasetError(err)
	}
	o.emitForWorkspace(ctx, dataset.WorkspaceID, "dataset.row.deleted", dataset.ID, map[string]int64{"row_id": rowID})
	return nil
}

func (o *Operator) ExportDataset(ctx context.Context, id, format string, output io.Writer) (domain.Dataset, error) {
	dataset, err := o.store.GetDataset(ctx, id)
	if err != nil {
		return domain.Dataset{}, translateDatasetError(err)
	}
	switch format {
	case "json":
		if _, err := io.WriteString(output, "[\n"); err != nil {
			return dataset, err
		}
		first := true
		err = o.store.StreamDatasetRows(ctx, id, store.MaximumDatasetPageSize, func(row domain.DatasetRow) error {
			encoded, err := json.Marshal(row.Values)
			if err != nil {
				return err
			}
			if !first {
				if _, err := io.WriteString(output, ",\n"); err != nil {
					return err
				}
			}
			first = false
			_, err = output.Write(encoded)
			return err
		})
		if err == nil {
			_, err = io.WriteString(output, "\n]\n")
		}
	case "csv":
		writer := csv.NewWriter(output)
		headings := make([]string, len(dataset.Schema))
		for index, column := range dataset.Schema {
			headings[index] = column.Name
		}
		if err := writer.Write(headings); err != nil {
			return dataset, err
		}
		err = o.store.StreamDatasetRows(ctx, id, store.MaximumDatasetPageSize, func(row domain.DatasetRow) error {
			values := make([]string, len(dataset.Schema))
			for index, column := range dataset.Schema {
				values[index] = datasetCSVValue(row.Values[column.Name])
			}
			return writer.Write(values)
		})
		writer.Flush()
		if err == nil {
			err = writer.Error()
		}
	default:
		return domain.Dataset{}, fmt.Errorf("%w: export format must be csv or json", api.ErrInvalid)
	}
	if err != nil {
		return dataset, err
	}
	return dataset, nil
}

func datasetCSVValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}

func translateDatasetError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrDatasetDeleted):
		return api.ErrNotFound
	case errors.Is(err, store.ErrDatasetConflict):
		return fmt.Errorf("%w: %v", api.ErrConflict, err)
	}
	message := err.Error()
	if strings.Contains(message, "UNIQUE constraint failed") {
		return fmt.Errorf("%w: a dataset with that slug or unique row key already exists", api.ErrConflict)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if strings.HasPrefix(message, "store:") {
		return fmt.Errorf("%w: %s", api.ErrInvalid, message)
	}
	return err
}
