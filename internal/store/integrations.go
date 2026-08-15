package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

const integrationColumns = `id, workspace_id, name, provider, description, status, manifest,
credential_requirements, allowed_hosts, last_verified_at, last_error, created_at, updated_at`

type IntegrationFilter struct {
	WorkspaceID string
	Statuses    []domain.IntegrationStatus
	Provider    string
	Limit       int
}

func (s *Store) CreateIntegration(ctx context.Context, integration domain.Integration) (domain.Integration, error) {
	var err error
	integration.WorkspaceID, err = s.defaultWorkspaceID(ctx, integration.WorkspaceID)
	if err != nil {
		return domain.Integration{}, err
	}
	if integration.WorkspaceID == "" {
		return domain.Integration{}, fmt.Errorf("store: integration requires a workspace")
	}
	if integration.ID == "" {
		integration.ID, err = newID()
		if err != nil {
			return domain.Integration{}, err
		}
	}
	if integration.Status == "" {
		integration.Status = domain.IntegrationDraft
	}
	integration = normalizeIntegration(integration)
	manifest, requirements, hosts, err := encodeIntegration(integration)
	if err != nil {
		return domain.Integration{}, err
	}
	now := s.now()
	integration.CreatedAt = defaultTime(integration.CreatedAt, now)
	integration.UpdatedAt = defaultTime(integration.UpdatedAt, integration.CreatedAt)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO integrations (`+integrationColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		integration.ID, integration.WorkspaceID, integration.Name, integration.Provider, integration.Description,
		integration.Status, manifest, requirements, hosts, nullableTime(integration.LastVerifiedAt),
		integration.LastError, formatTime(integration.CreatedAt), formatTime(integration.UpdatedAt)); err != nil {
		return domain.Integration{}, fmt.Errorf("store: create integration: %w", err)
	}
	return integration, nil
}

// GetIntegration returns an integration only from the active workspace.
func (s *Store) GetIntegration(ctx context.Context, id string) (domain.Integration, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Integration{}, err
	}
	return s.GetIntegrationForWorkspace(ctx, workspaceID, id)
}

func (s *Store) GetIntegrationForWorkspace(ctx context.Context, workspaceID, id string) (domain.Integration, error) {
	return scanIntegration(s.db.QueryRowContext(ctx, `SELECT `+integrationColumns+`
FROM integrations WHERE id = ? AND workspace_id = ?`, id, workspaceID))
}

func (s *Store) ListIntegrations(ctx context.Context, filter IntegrationFilter) ([]domain.Integration, error) {
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + integrationColumns + ` FROM integrations WHERE workspace_id = ?`
	args := []any{workspaceID}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for index, status := range filter.Statuses {
			placeholders[index] = "?"
			args = append(args, status)
		}
		query += " AND status IN (" + strings.Join(placeholders, ",") + ")"
	}
	if filter.Provider != "" {
		query += " AND provider = ?"
		args = append(args, filter.Provider)
	}
	query += " ORDER BY name, id"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list integrations: %w", err)
	}
	defer rows.Close()
	var integrations []domain.Integration
	for rows.Next() {
		integration, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		integrations = append(integrations, integration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list integrations: %w", err)
	}
	return integrations, nil
}

// UpdateIntegration updates an integration only in the active workspace.
func (s *Store) UpdateIntegration(ctx context.Context, integration domain.Integration) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.UpdateIntegrationForWorkspace(ctx, workspaceID, integration)
}

func (s *Store) UpdateIntegrationForWorkspace(ctx context.Context, workspaceID string, integration domain.Integration) error {
	integration.WorkspaceID = workspaceID
	integration = normalizeIntegration(integration)
	manifest, requirements, hosts, err := encodeIntegration(integration)
	if err != nil {
		return err
	}
	integration.UpdatedAt = defaultTime(integration.UpdatedAt, s.now())
	result, err := s.db.ExecContext(ctx, `
UPDATE integrations SET name = ?, provider = ?, description = ?, status = ?, manifest = ?,
    credential_requirements = ?, allowed_hosts = ?, last_verified_at = ?, last_error = ?, updated_at = ?
WHERE id = ? AND workspace_id = ?`, integration.Name, integration.Provider, integration.Description,
		integration.Status, manifest, requirements, hosts, nullableTime(integration.LastVerifiedAt), integration.LastError,
		formatTime(integration.UpdatedAt), integration.ID, workspaceID)
	if err != nil {
		return fmt.Errorf("store: update integration: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update integration result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("integration %q: %w", integration.ID, ErrNotFound)
	}
	return nil
}

// DeleteIntegration deletes an integration only from the active workspace.
func (s *Store) DeleteIntegration(ctx context.Context, id string) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.DeleteIntegrationForWorkspace(ctx, workspaceID, id)
}

func (s *Store) DeleteIntegrationForWorkspace(ctx context.Context, workspaceID, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM integrations WHERE id = ? AND workspace_id = ?", id, workspaceID)
	if err != nil {
		return fmt.Errorf("store: delete integration: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete integration result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("integration %q: %w", id, ErrNotFound)
	}
	return nil
}

func encodeIntegration(integration domain.Integration) ([]byte, []byte, []byte, error) {
	if integration.Name == "" || integration.Provider == "" {
		return nil, nil, nil, fmt.Errorf("store: integration name and provider are required")
	}
	if !validIntegrationStatus(integration.Status) {
		return nil, nil, nil, fmt.Errorf("store: invalid integration status %q", integration.Status)
	}
	manifest := integration.Manifest
	if len(manifest) == 0 {
		manifest = json.RawMessage(`{}`)
	}
	if !json.Valid(manifest) {
		return nil, nil, nil, fmt.Errorf("store: integration manifest is not valid JSON")
	}
	seenRequirements := make(map[string]struct{}, len(integration.CredentialRequirements))
	for _, requirement := range integration.CredentialRequirements {
		name := strings.TrimSpace(requirement.Name)
		if name == "" {
			return nil, nil, nil, fmt.Errorf("store: credential requirement name is required")
		}
		if _, exists := seenRequirements[name]; exists {
			return nil, nil, nil, fmt.Errorf("store: duplicate credential requirement %q", name)
		}
		seenRequirements[name] = struct{}{}
	}
	requirements := integration.CredentialRequirements
	if requirements == nil {
		requirements = []domain.CredentialRequirement{}
	}
	requirementsJSON, err := json.Marshal(requirements)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("store: encode credential requirements: %w", err)
	}
	hostsJSON, err := json.Marshal(integration.AllowedHosts)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("store: encode allowed hosts: %w", err)
	}
	return append([]byte(nil), manifest...), requirementsJSON, hostsJSON, nil
}

func normalizeIntegration(integration domain.Integration) domain.Integration {
	integration.Name = strings.TrimSpace(integration.Name)
	integration.Provider = strings.TrimSpace(integration.Provider)
	integration.Description = strings.TrimSpace(integration.Description)
	integration.LastError = strings.TrimSpace(integration.LastError)
	integration.Manifest = append(json.RawMessage(nil), integration.Manifest...)
	integration.CredentialRequirements = append([]domain.CredentialRequirement(nil), integration.CredentialRequirements...)
	integration.AllowedHosts = uniqueTrimmedStrings(integration.AllowedHosts)
	for index := range integration.CredentialRequirements {
		integration.CredentialRequirements[index].Name = strings.TrimSpace(integration.CredentialRequirements[index].Name)
		integration.CredentialRequirements[index].Label = strings.TrimSpace(integration.CredentialRequirements[index].Label)
		integration.CredentialRequirements[index].Description = strings.TrimSpace(integration.CredentialRequirements[index].Description)
	}
	if integration.CredentialRequirements == nil {
		integration.CredentialRequirements = []domain.CredentialRequirement{}
	}
	if integration.AllowedHosts == nil {
		integration.AllowedHosts = []string{}
	}
	if len(integration.Manifest) == 0 {
		integration.Manifest = json.RawMessage(`{}`)
	}
	return integration
}

func scanIntegration(row rowScanner) (domain.Integration, error) {
	var integration domain.Integration
	var manifest, requirements, hosts []byte
	var verified sql.NullString
	var created, updated string
	if err := row.Scan(&integration.ID, &integration.WorkspaceID, &integration.Name, &integration.Provider,
		&integration.Description, &integration.Status, &manifest, &requirements, &hosts, &verified,
		&integration.LastError, &created, &updated); err != nil {
		return domain.Integration{}, fmt.Errorf("store: get integration: %w", notFound("integration", err))
	}
	integration.Manifest = append(json.RawMessage(nil), manifest...)
	if err := json.Unmarshal(requirements, &integration.CredentialRequirements); err != nil {
		return domain.Integration{}, fmt.Errorf("store: decode credential requirements: %w", err)
	}
	if err := json.Unmarshal(hosts, &integration.AllowedHosts); err != nil {
		return domain.Integration{}, fmt.Errorf("store: decode allowed hosts: %w", err)
	}
	var err error
	integration.LastVerifiedAt, err = parseNullableTime(verified)
	if err != nil {
		return domain.Integration{}, err
	}
	integration.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Integration{}, err
	}
	integration.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Integration{}, err
	}
	return integration, nil
}

func validIntegrationStatus(status domain.IntegrationStatus) bool {
	switch status {
	case domain.IntegrationDraft, domain.IntegrationNeedsCredentials, domain.IntegrationVerifying,
		domain.IntegrationReady, domain.IntegrationFailed, domain.IntegrationDisabled:
		return true
	default:
		return false
	}
}

func uniqueTrimmedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
