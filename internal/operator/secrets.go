package operator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func (o *Operator) Secrets(ctx context.Context) ([]api.SecretView, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return nil, translateNotFound(err)
	}
	records, err := o.store.ListSecretRecords(ctx, store.SecretRecordFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return nil, err
	}
	views := make([]api.SecretView, 0, len(records))
	for _, record := range records {
		view, viewErr := o.secretView(ctx, record)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	return views, nil
}

func (o *Operator) Secret(ctx context.Context, id string) (api.SecretView, error) {
	record, err := o.store.GetSecretRecord(ctx, strings.TrimSpace(id))
	if err != nil {
		return api.SecretView{}, translateNotFound(err)
	}
	return o.secretView(ctx, record)
}

func (o *Operator) CreateSecret(ctx context.Context, input api.SecretCreate, value []byte) (api.SecretView, error) {
	defer wipeBytes(value)
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return api.SecretView{}, translateNotFound(err)
	}
	record, err := o.createSecretRecordForWorkspace(ctx, workspace.ID, input)
	if err != nil {
		return api.SecretView{}, err
	}
	if len(value) == 0 {
		_ = o.store.DeleteSecretRecordForWorkspace(context.WithoutCancel(ctx), workspace.ID, record.ID)
		return api.SecretView{}, fmt.Errorf("%w: secret value is required", api.ErrInvalid)
	}
	if err := o.putSecretValue(ctx, record, value); err != nil {
		_ = o.store.DeleteSecretRecordForWorkspace(context.WithoutCancel(ctx), workspace.ID, record.ID)
		return api.SecretView{}, err
	}
	view, err := o.secretView(ctx, record)
	if err != nil {
		return api.SecretView{}, err
	}
	o.emitForWorkspace(ctx, workspace.ID, "secret.configured", record.ID, map[string]string{"name": record.Name})
	return view, nil
}

// createSecretRecordForWorkspace is used by Chat to create a metadata-only
// protected setup card. The owner supplies the value later through /api/secrets.
func (o *Operator) createSecretRecordForWorkspace(ctx context.Context, workspaceID string, input api.SecretCreate) (domain.SecretRecord, error) {
	record := domain.SecretRecord{
		WorkspaceID: workspaceID,
		Name:        strings.TrimSpace(input.Name), Label: strings.TrimSpace(input.Label),
		Description: redactSecrets(strings.TrimSpace(input.Description)),
	}
	if record.Name == "" || len(record.Name) > 128 || len(record.Label) > 256 || len(record.Description) > 4096 {
		return domain.SecretRecord{}, fmt.Errorf("%w: secret metadata is invalid or too long", api.ErrInvalid)
	}
	existing, err := o.store.ListSecretRecords(ctx, store.SecretRecordFilter{WorkspaceID: workspaceID, Search: record.Name, Limit: 32})
	if err != nil {
		return domain.SecretRecord{}, err
	}
	for _, candidate := range existing {
		if strings.EqualFold(candidate.Name, record.Name) {
			return candidate, nil
		}
	}
	created, err := o.store.CreateSecretRecord(ctx, record)
	if err != nil {
		return domain.SecretRecord{}, err
	}
	o.emitForWorkspace(ctx, workspaceID, "secret.created", created.ID, map[string]string{"name": created.Name, "label": created.Label})
	return created, nil
}

func (o *Operator) UpdateSecret(ctx context.Context, id string, input api.SecretUpdate, value []byte) (api.SecretView, error) {
	defer wipeBytes(value)
	record, err := o.store.GetSecretRecord(ctx, strings.TrimSpace(id))
	if err != nil {
		return api.SecretView{}, translateNotFound(err)
	}
	if input.Name != nil {
		record.Name = strings.TrimSpace(*input.Name)
	}
	if input.Label != nil {
		record.Label = strings.TrimSpace(*input.Label)
	}
	if input.Description != nil {
		record.Description = redactSecrets(strings.TrimSpace(*input.Description))
	}
	record.UpdatedAt = time.Now().UTC()
	if input.Name != nil || input.Label != nil || input.Description != nil {
		if err := o.store.UpdateSecretRecordForWorkspace(ctx, record.WorkspaceID, record); err != nil {
			return api.SecretView{}, translateNotFound(err)
		}
	}
	if len(value) > 0 {
		if err := o.putSecretValue(ctx, record, value); err != nil {
			return api.SecretView{}, err
		}
		o.emitForWorkspace(ctx, record.WorkspaceID, "secret.configured", record.ID, map[string]string{"name": record.Name})
	} else {
		o.emitForWorkspace(ctx, record.WorkspaceID, "secret.updated", record.ID, map[string]string{"name": record.Name})
	}
	updated, err := o.store.GetSecretRecordForWorkspace(ctx, record.WorkspaceID, record.ID)
	if err != nil {
		return api.SecretView{}, err
	}
	return o.secretView(ctx, updated)
}

func (o *Operator) DeleteSecret(ctx context.Context, id string) error {
	record, err := o.store.GetSecretRecord(ctx, strings.TrimSpace(id))
	if err != nil {
		return translateNotFound(err)
	}
	if err := o.store.DeleteSecretRecordForWorkspace(ctx, record.WorkspaceID, record.ID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key") {
			return fmt.Errorf("%w: remove this secret from its registered scripts before deleting it", api.ErrConflict)
		}
		return translateNotFound(err)
	}
	backend, _, _ := o.integrationDependencies()
	if backend != nil {
		if err := backend.Delete(ctx, secretRef(record)); err != nil && !errors.Is(err, credentials.ErrNotFound) && !errors.Is(err, credentials.ErrUnsupported) {
			o.logger.Warn("orphaned credential could not be removed", "secret_id", record.ID, "error", "credential backend unavailable")
		}
	}
	o.emitForWorkspace(ctx, record.WorkspaceID, "secret.deleted", record.ID, map[string]string{"name": record.Name})
	return nil
}

func (o *Operator) secretView(ctx context.Context, record domain.SecretRecord) (api.SecretView, error) {
	configured, err := o.secretConfigured(ctx, record)
	if err != nil {
		return api.SecretView{}, err
	}
	return api.SecretView{
		ID: record.ID, WorkspaceID: record.WorkspaceID, Name: record.Name, Label: record.Label,
		Description: record.Description, Configured: configured, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, nil
}

func (o *Operator) secretConfigured(ctx context.Context, record domain.SecretRecord) (bool, error) {
	backend, _, _ := o.integrationDependencies()
	if backend == nil {
		return false, nil
	}
	secret, err := backend.Get(ctx, secretRef(record))
	if err == nil {
		secret.Destroy()
		return true, nil
	}
	if errors.Is(err, credentials.ErrNotFound) || errors.Is(err, credentials.ErrUnsupported) {
		return false, nil
	}
	return false, translateCredentialError(err)
}

func (o *Operator) putSecretValue(ctx context.Context, record domain.SecretRecord, value []byte) error {
	backend, _, _ := o.integrationDependencies()
	if backend == nil {
		return api.ErrUnavailable
	}
	secret, err := credentials.NewSecret(value)
	if err != nil {
		return fmt.Errorf("%w: secret value must contain 1 to 65536 bytes", api.ErrInvalid)
	}
	defer secret.Destroy()
	if err := backend.Put(ctx, secretRef(record), secret); err != nil {
		return translateCredentialError(err)
	}
	return nil
}

func secretRef(record domain.SecretRecord) credentials.Ref {
	return credentials.Ref{WorkspaceID: record.WorkspaceID, Integration: domain.SecretCredentialIntegration, Name: record.ReferenceKey}
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ api.SecretBackend = (*Operator)(nil)
