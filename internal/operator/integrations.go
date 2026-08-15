package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/integrations"
	"github.com/nabu-sh/nabu/internal/store"
)

const maximumIntegrationError = 2 * 1024
const maximumTaskIntegrationContext = 256 * 1024

type taskIntegrationResult struct {
	IntegrationID   string          `json:"integration_id"`
	IntegrationName string          `json:"integration_name"`
	OperationID     string          `json:"operation_id"`
	OperationName   string          `json:"operation_name"`
	StatusCode      int             `json:"status_code,omitempty"`
	Response        json.RawMessage `json:"response,omitempty"`
	Error           string          `json:"error,omitempty"`
}

// taskIntegrationContext executes only ready, read-only integrations that are
// explicitly named by the task. Credential material stays inside the service;
// only the bounded, redacted provider response enters the task packet.
func (o *Operator) taskIntegrationContext(ctx context.Context, task domain.Task, workspaceID string) string {
	haystack := strings.ToLower(strings.Join([]string{task.Title, task.Purpose, task.Why, definitionText(task.DefinitionOfDone)}, "\n"))
	items, err := o.store.ListIntegrations(ctx, store.IntegrationFilter{WorkspaceID: workspaceID, Statuses: []domain.IntegrationStatus{domain.IntegrationReady}, Limit: 32})
	if err != nil {
		return ""
	}
	_, service, _ := o.integrationDependencies()
	if service == nil {
		return ""
	}
	results := make([]taskIntegrationResult, 0, 4)
	for _, item := range items {
		manifest, parseErr := integrations.ParseManifest(item.Manifest)
		if parseErr != nil || !integrationMentioned(haystack, item, manifest) {
			continue
		}
		for _, operation := range manifest.Operations {
			if operation.Kind != integrations.OperationRead || len(results) >= 4 {
				continue
			}
			result, executeErr := service.Execute(ctx, manifest, integrations.ExecuteRequest{
				WorkspaceID: workspaceID, IntegrationID: item.ID, OperationID: operation.ID,
			})
			entry := taskIntegrationResult{
				IntegrationID: item.ID, IntegrationName: item.Name,
				OperationID: operation.ID, OperationName: operation.Name, StatusCode: result.StatusCode,
			}
			if executeErr != nil {
				entry.Error = boundedIntegrationError(executeErr)
			} else if len(result.Body) > 0 {
				if json.Valid(result.Body) {
					entry.Response = append(json.RawMessage(nil), result.Body...)
				} else {
					encoded, _ := json.Marshal(string(result.Body))
					entry.Response = encoded
				}
			}
			result.Destroy()
			results = append(results, entry)
			o.emitForWorkspace(ctx, workspaceID, "integration.read_completed", item.ID, map[string]any{
				"task_id": task.ID, "operation_id": operation.ID, "status_code": entry.StatusCode, "error": entry.Error,
			})
		}
	}
	if len(results) == 0 {
		return ""
	}
	for {
		encoded, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return ""
		}
		if len(encoded) <= maximumTaskIntegrationContext {
			return string(encoded)
		}
		removedResponse := false
		for index := len(results) - 1; index >= 0; index-- {
			if len(results[index].Response) == 0 {
				continue
			}
			results[index].Response = nil
			results[index].Error = "Provider response omitted because it exceeded Nabu's task context limit."
			removedResponse = true
			break
		}
		if !removedResponse {
			return ""
		}
	}
}

func integrationMentioned(haystack string, item domain.Integration, manifest integrations.Manifest) bool {
	candidates := []string{item.Name, item.Provider, manifest.Name, manifest.ID}
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if len(candidate) >= 3 && strings.Contains(haystack, candidate) {
			return true
		}
	}
	return false
}

func definitionText(items []domain.DefinitionItem) string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, item.Text)
	}
	return strings.Join(values, "\n")
}

// IntegrationResponseSink is the explicit hook for a later dataset ingestion
// pipeline. The operator never persists provider bodies by default.
type IntegrationResponseSink interface {
	IngestIntegrationResponse(context.Context, string, domain.Integration, integrations.Operation, integrations.Result) error
}

// ConfigureIntegrations replaces platform dependencies for tests or a future
// audited transport. Call during startup, before serving requests.
func (o *Operator) ConfigureIntegrations(backend credentials.Backend, client integrations.HTTPDoer, sink IntegrationResponseSink) error {
	service, err := integrations.NewService(backend, client)
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.credentials = backend
	o.integrations = service
	o.integrationSink = sink
	o.mu.Unlock()
	return nil
}

func (o *Operator) integrationDependencies() (credentials.Backend, *integrations.Service, IntegrationResponseSink) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.credentials, o.integrations, o.integrationSink
}

func (o *Operator) Integrations(ctx context.Context) ([]api.IntegrationView, error) {
	active, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return nil, translateNotFound(err)
	}
	items, err := o.store.ListIntegrations(ctx, store.IntegrationFilter{WorkspaceID: active.ID})
	if err != nil {
		return nil, err
	}
	result := make([]api.IntegrationView, 0, len(items))
	for _, item := range items {
		view, viewErr := o.integrationView(ctx, item)
		if viewErr != nil {
			return nil, viewErr
		}
		result = append(result, view)
	}
	return result, nil
}

func (o *Operator) Integration(ctx context.Context, id string) (api.IntegrationView, error) {
	item, manifest, err := o.activeIntegration(ctx, id)
	if err != nil {
		return api.IntegrationView{}, err
	}
	return o.integrationViewWithManifest(ctx, item, manifest)
}

// CreateGeneratedIntegration is safe for chat orchestration: it accepts only
// the validated declarative manifest type and cannot create a shell adapter.
func (o *Operator) CreateGeneratedIntegration(ctx context.Context, manifest integrations.Manifest) (api.IntegrationView, error) {
	active, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return api.IntegrationView{}, translateNotFound(err)
	}
	return o.createGeneratedIntegrationForWorkspace(ctx, active.ID, manifest)
}

func (o *Operator) createGeneratedIntegrationForWorkspace(ctx context.Context, workspaceID string, manifest integrations.Manifest) (api.IntegrationView, error) {
	if err := manifest.Validate(); err != nil {
		return api.IntegrationView{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	if strings.TrimSpace(workspaceID) == "" {
		return api.IntegrationView{}, api.ErrNotFound
	}
	existing, err := o.store.ListIntegrations(ctx, store.IntegrationFilter{WorkspaceID: workspaceID, Provider: manifest.ID, Limit: 1})
	if err != nil {
		return api.IntegrationView{}, err
	}
	if len(existing) > 0 {
		return o.integrationView(ctx, existing[0])
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return api.IntegrationView{}, err
	}
	requirements := credentialRequirements(manifest)
	status := domain.IntegrationDraft
	if len(requirements) > 0 {
		status = domain.IntegrationNeedsCredentials
	}
	item, err := o.store.CreateIntegration(ctx, domain.Integration{
		WorkspaceID: workspaceID,
		Name:        manifest.Name, Provider: manifest.ID, Description: manifest.Description,
		Status: status, Manifest: encoded, CredentialRequirements: requirements,
		AllowedHosts: append([]string(nil), manifest.AllowedHosts...),
	})
	if err != nil {
		return api.IntegrationView{}, err
	}
	o.emitForWorkspace(ctx, workspaceID, "integration.created", item.ID, map[string]any{
		"provider": item.Provider, "operations": len(manifest.Operations), "credential_names": credentialNames(requirements),
	})
	return o.integrationViewWithManifest(ctx, item, manifest)
}

func (o *Operator) SaveIntegrationCredentials(ctx context.Context, id string, values map[string][]byte) (api.IntegrationView, error) {
	defer wipeCredentialMap(values)
	item, manifest, err := o.activeIntegration(ctx, id)
	if err != nil {
		return api.IntegrationView{}, err
	}
	backend, _, _ := o.integrationDependencies()
	if backend == nil {
		return api.IntegrationView{}, api.ErrUnavailable
	}
	required := credentialRequirements(manifest)
	expected := make(map[string]struct{}, len(required))
	for _, requirement := range required {
		expected[requirement.Name] = struct{}{}
	}
	if len(values) == 0 {
		return api.IntegrationView{}, fmt.Errorf("%w: at least one credential is required", api.ErrInvalid)
	}
	for name, value := range values {
		if _, ok := expected[name]; !ok {
			return api.IntegrationView{}, fmt.Errorf("%w: credential %q is not declared by this integration", api.ErrInvalid, name)
		}
		if len(value) > 64*1024 {
			return api.IntegrationView{}, fmt.Errorf("%w: credential %q is too large", api.ErrInvalid, name)
		}
	}
	configured, err := o.configuredCredentials(ctx, item, required)
	if err != nil && !errors.Is(err, credentials.ErrUnsupported) {
		return api.IntegrationView{}, err
	}
	if errors.Is(err, credentials.ErrUnsupported) {
		return api.IntegrationView{}, fmt.Errorf("%w: secure credential storage is unavailable", api.ErrUnavailable)
	}
	written := make([]string, 0, len(values))
	for name, value := range values {
		if len(value) == 0 {
			if configured[name] {
				continue
			}
			return api.IntegrationView{}, fmt.Errorf("%w: credential %q cannot be empty", api.ErrInvalid, name)
		}
		secret, createErr := credentials.NewSecret(value)
		if createErr != nil {
			return api.IntegrationView{}, fmt.Errorf("%w: invalid credential %q", api.ErrInvalid, name)
		}
		putErr := backend.Put(ctx, credentialRef(item, name), secret)
		secret.Destroy()
		if putErr != nil {
			return api.IntegrationView{}, translateCredentialError(putErr)
		}
		written = append(written, name)
	}
	item.Status = domain.IntegrationDraft
	item.LastVerifiedAt = nil
	item.LastError = ""
	if err := o.store.UpdateIntegrationForWorkspace(ctx, item.WorkspaceID, item); err != nil {
		return api.IntegrationView{}, err
	}
	sort.Strings(written)
	o.emitForWorkspace(ctx, item.WorkspaceID, "integration.credentials_updated", item.ID, map[string]any{"credential_names": written})
	return o.integrationViewWithManifest(ctx, item, manifest)
}

func (o *Operator) DeleteIntegrationCredential(ctx context.Context, id, name string) (api.IntegrationView, error) {
	item, manifest, err := o.activeIntegration(ctx, id)
	if err != nil {
		return api.IntegrationView{}, err
	}
	if !containsCredential(credentialRequirements(manifest), name) {
		return api.IntegrationView{}, api.ErrNotFound
	}
	backend, _, _ := o.integrationDependencies()
	if backend == nil {
		return api.IntegrationView{}, api.ErrUnavailable
	}
	if err := backend.Delete(ctx, credentialRef(item, name)); err != nil && !errors.Is(err, credentials.ErrNotFound) {
		return api.IntegrationView{}, translateCredentialError(err)
	}
	item.Status = domain.IntegrationNeedsCredentials
	item.LastVerifiedAt = nil
	item.LastError = ""
	if err := o.store.UpdateIntegrationForWorkspace(ctx, item.WorkspaceID, item); err != nil {
		return api.IntegrationView{}, err
	}
	o.emitForWorkspace(ctx, item.WorkspaceID, "integration.credential_deleted", item.ID, map[string]string{"credential_name": name})
	return o.integrationViewWithManifest(ctx, item, manifest)
}

func (o *Operator) VerifyIntegration(ctx context.Context, id string) (api.IntegrationView, error) {
	item, manifest, err := o.activeIntegration(ctx, id)
	if err != nil {
		return api.IntegrationView{}, err
	}
	requirements := credentialRequirements(manifest)
	configured, configuredErr := o.configuredCredentials(ctx, item, requirements)
	if configuredErr != nil {
		return api.IntegrationView{}, translateCredentialError(configuredErr)
	}
	for _, requirement := range requirements {
		if requirement.Required && !configured[requirement.Name] {
			item.Status = domain.IntegrationNeedsCredentials
			item.LastVerifiedAt = nil
			item.LastError = "Required credentials are not configured."
			_ = o.store.UpdateIntegrationForWorkspace(ctx, item.WorkspaceID, item)
			return o.integrationViewWithManifest(ctx, item, manifest)
		}
	}
	operation, ok := verificationOperation(manifest)
	if !ok {
		return api.IntegrationView{}, fmt.Errorf("%w: integration has no read-only verification operation", api.ErrInvalid)
	}
	_, service, sink := o.integrationDependencies()
	if service == nil {
		return api.IntegrationView{}, api.ErrUnavailable
	}
	item.Status = domain.IntegrationVerifying
	item.LastError = ""
	if err := o.store.UpdateIntegrationForWorkspace(ctx, item.WorkspaceID, item); err != nil {
		return api.IntegrationView{}, err
	}
	o.emitForWorkspace(ctx, item.WorkspaceID, "integration.verifying", item.ID, map[string]string{"operation_id": operation.ID})
	result, verifyErr := service.VerifyReadOnly(ctx, manifest, item.WorkspaceID, item.ID, operation.ID)
	defer result.Destroy()
	now := time.Now().UTC()
	if verifyErr != nil {
		item.Status = domain.IntegrationFailed
		item.LastVerifiedAt = &now
		item.LastError = boundedIntegrationError(verifyErr)
		if err := o.store.UpdateIntegrationForWorkspace(context.WithoutCancel(ctx), item.WorkspaceID, item); err != nil {
			return api.IntegrationView{}, err
		}
		o.emitForWorkspace(context.WithoutCancel(ctx), item.WorkspaceID, "integration.verification_failed", item.ID, map[string]any{
			"operation_id": operation.ID, "error": item.LastError,
		})
		return o.integrationViewWithManifest(context.WithoutCancel(ctx), item, manifest)
	}
	item.Status = domain.IntegrationReady
	item.LastVerifiedAt = &now
	item.LastError = ""
	if err := o.store.UpdateIntegrationForWorkspace(context.WithoutCancel(ctx), item.WorkspaceID, item); err != nil {
		return api.IntegrationView{}, err
	}
	o.emitForWorkspace(context.WithoutCancel(ctx), item.WorkspaceID, "integration.ready", item.ID, map[string]any{
		"operation_id": operation.ID, "status_code": result.StatusCode,
	})
	if sink != nil {
		if sinkErr := sink.IngestIntegrationResponse(ctx, item.WorkspaceID, item, operation, result); sinkErr != nil {
			o.emitForWorkspace(context.WithoutCancel(ctx), item.WorkspaceID, "integration.ingestion_failed", item.ID, map[string]string{"operation_id": operation.ID})
		}
	}
	return o.integrationViewWithManifest(context.WithoutCancel(ctx), item, manifest)
}

func (o *Operator) activeIntegration(ctx context.Context, id string) (domain.Integration, integrations.Manifest, error) {
	if strings.TrimSpace(id) == "" {
		return domain.Integration{}, integrations.Manifest{}, api.ErrNotFound
	}
	item, err := o.store.GetIntegration(ctx, id)
	if err != nil {
		return domain.Integration{}, integrations.Manifest{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, item.WorkspaceID); err != nil {
		return domain.Integration{}, integrations.Manifest{}, err
	}
	manifest, err := integrations.ParseManifest(item.Manifest)
	if err != nil {
		return domain.Integration{}, integrations.Manifest{}, fmt.Errorf("stored integration manifest %q is invalid: %w", item.ID, err)
	}
	if !sameStrings(item.AllowedHosts, manifest.AllowedHosts) {
		return domain.Integration{}, integrations.Manifest{}, fmt.Errorf("stored integration host metadata does not match its manifest")
	}
	return item, manifest, nil
}

func (o *Operator) integrationView(ctx context.Context, item domain.Integration) (api.IntegrationView, error) {
	manifest, err := integrations.ParseManifest(item.Manifest)
	if err != nil {
		return api.IntegrationView{}, fmt.Errorf("stored integration manifest %q is invalid: %w", item.ID, err)
	}
	return o.integrationViewWithManifest(ctx, item, manifest)
}

func (o *Operator) integrationViewWithManifest(ctx context.Context, item domain.Integration, manifest integrations.Manifest) (api.IntegrationView, error) {
	requirements := credentialRequirements(manifest)
	configured, err := o.configuredCredentials(ctx, item, requirements)
	available := true
	if errors.Is(err, credentials.ErrUnsupported) {
		available = false
		err = nil
	}
	if err != nil {
		return api.IntegrationView{}, err
	}
	view := api.IntegrationView{
		ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name, Description: item.Description,
		Status: item.Status, Available: available, Configured: available, Error: item.LastError, VerifiedAt: item.LastVerifiedAt,
		Capabilities:        make([]api.IntegrationCapabilityView, 0, len(manifest.Operations)),
		RequiredCredentials: make([]api.IntegrationCredentialView, 0, len(requirements)),
	}
	for _, operation := range manifest.Operations {
		view.Capabilities = append(view.Capabilities, api.IntegrationCapabilityView{ID: operation.ID, Name: operation.Name, Kind: string(operation.Kind)})
	}
	for _, requirement := range requirements {
		isConfigured := configured[requirement.Name]
		if requirement.Required {
			view.Configured = view.Configured && isConfigured
		}
		view.RequiredCredentials = append(view.RequiredCredentials, api.IntegrationCredentialView{
			Name: requirement.Name, Label: requirement.Label, Description: requirement.Description,
			Secret: requirement.Secret, Required: requirement.Required, Configured: isConfigured,
		})
		if isConfigured {
			view.ConfiguredCredentials = append(view.ConfiguredCredentials, requirement.Name)
		}
	}
	if len(requirements) == 0 {
		view.Configured = available
	}
	return view, nil
}

func (o *Operator) configuredCredentials(ctx context.Context, item domain.Integration, requirements []domain.CredentialRequirement) (map[string]bool, error) {
	backend, _, _ := o.integrationDependencies()
	if backend == nil {
		return nil, credentials.ErrUnsupported
	}
	result := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		secret, err := backend.Get(ctx, credentialRef(item, requirement.Name))
		switch {
		case err == nil:
			result[requirement.Name] = true
			secret.Destroy()
		case errors.Is(err, credentials.ErrNotFound):
			result[requirement.Name] = false
		case errors.Is(err, credentials.ErrUnsupported):
			return result, err
		default:
			return nil, err
		}
	}
	return result, nil
}

func credentialRequirements(manifest integrations.Manifest) []domain.CredentialRequirement {
	fields := manifest.RequiredFields()
	result := make([]domain.CredentialRequirement, 0, len(fields))
	for _, field := range fields {
		result = append(result, domain.CredentialRequirement{
			Name: field.Name, Label: field.Label, Description: field.Description,
			Secret: field.Secret, Required: field.Required,
		})
	}
	return result
}

func credentialNames(requirements []domain.CredentialRequirement) []string {
	result := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		result = append(result, requirement.Name)
	}
	return result
}

func credentialRef(item domain.Integration, name string) credentials.Ref {
	return credentials.Ref{WorkspaceID: item.WorkspaceID, Integration: item.ID, Name: name}
}

func verificationOperation(manifest integrations.Manifest) (integrations.Operation, bool) {
	for _, operation := range manifest.Operations {
		if operation.Kind == integrations.OperationRead {
			return operation, true
		}
	}
	return integrations.Operation{}, false
}

func containsCredential(requirements []domain.CredentialRequirement, name string) bool {
	for _, requirement := range requirements {
		if requirement.Name == name {
			return true
		}
	}
	return false
}

func translateCredentialError(err error) error {
	if errors.Is(err, credentials.ErrUnsupported) {
		return fmt.Errorf("%w: secure credential storage is unavailable on this machine", api.ErrUnavailable)
	}
	if errors.Is(err, credentials.ErrNotFound) {
		return api.ErrNotFound
	}
	return fmt.Errorf("%w: the system credential vault could not save this connection; unlock it, allow access if prompted, and try again", api.ErrUnavailable)
}

func boundedIntegrationError(err error) string {
	value := strings.TrimSpace(err.Error())
	if len(value) > maximumIntegrationError {
		value = value[:maximumIntegrationError]
	}
	return redactSecrets(value)
}

func wipeCredentialMap(values map[string][]byte) {
	for name, value := range values {
		for index := range value {
			value[index] = 0
		}
		delete(values, name)
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	copyLeft := append([]string(nil), left...)
	copyRight := append([]string(nil), right...)
	sort.Strings(copyLeft)
	sort.Strings(copyRight)
	for index := range copyLeft {
		if copyLeft[index] != copyRight[index] {
			return false
		}
	}
	return true
}

var _ api.IntegrationBackend = (*Operator)(nil)
