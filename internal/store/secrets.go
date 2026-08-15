package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
)

const secretRecordColumns = `id, workspace_id, name, label, description, reference_key, created_at, updated_at`

var safeEnvironmentVariable = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

var reservedEnvironmentVariables = map[string]struct{}{
	"BASHOPTS": {}, "BASH_ENV": {}, "CDPATH": {}, "ENV": {}, "GLOBIGNORE": {},
	"HOME": {}, "IFS": {}, "NODE_OPTIONS": {}, "OLDPWD": {}, "PATH": {},
	"PERL5LIB": {}, "PERL5OPT": {}, "PROMPT_COMMAND": {}, "PS4": {}, "PWD": {},
	"PYTHONHOME": {}, "PYTHONPATH": {}, "RUBYLIB": {}, "RUBYOPT": {}, "SHELL": {},
	"SHELLOPTS": {},
}

type SecretRecordFilter struct {
	WorkspaceID string
	Search      string
	Limit       int
}

func (s *Store) CreateSecretRecord(ctx context.Context, record domain.SecretRecord) (domain.SecretRecord, error) {
	var err error
	record.WorkspaceID, err = s.defaultWorkspaceID(ctx, record.WorkspaceID)
	if err != nil {
		return domain.SecretRecord{}, err
	}
	if record.WorkspaceID == "" {
		return domain.SecretRecord{}, fmt.Errorf("store: secret record requires a workspace")
	}
	if record.ID == "" {
		record.ID, err = newID()
		if err != nil {
			return domain.SecretRecord{}, err
		}
	}
	record = normalizeSecretRecord(record)
	if record.ReferenceKey == "" {
		record.ReferenceKey = record.ID
	}
	if err := validateSecretRecord(record); err != nil {
		return domain.SecretRecord{}, err
	}
	now := s.now()
	record.CreatedAt = defaultTime(record.CreatedAt, now)
	record.UpdatedAt = defaultTime(record.UpdatedAt, record.CreatedAt)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO secret_records (`+secretRecordColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, record.ID, record.WorkspaceID, record.Name, record.Label,
		record.Description, record.ReferenceKey, formatTime(record.CreatedAt), formatTime(record.UpdatedAt)); err != nil {
		return domain.SecretRecord{}, fmt.Errorf("store: create secret record: %w", err)
	}
	return record, nil
}

// GetSecretRecord returns a record only from the active workspace.
func (s *Store) GetSecretRecord(ctx context.Context, id string) (domain.SecretRecord, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.SecretRecord{}, err
	}
	return s.GetSecretRecordForWorkspace(ctx, workspaceID, id)
}

func (s *Store) GetSecretRecordForWorkspace(ctx context.Context, workspaceID, id string) (domain.SecretRecord, error) {
	return scanSecretRecord(s.db.QueryRowContext(ctx, `SELECT `+secretRecordColumns+`
FROM secret_records WHERE id = ? AND workspace_id = ?`, id, workspaceID))
}

func (s *Store) GetSecretRecordByReferenceKeyForWorkspace(ctx context.Context, workspaceID, referenceKey string) (domain.SecretRecord, error) {
	return scanSecretRecord(s.db.QueryRowContext(ctx, `SELECT `+secretRecordColumns+`
FROM secret_records WHERE reference_key = ? AND workspace_id = ?`, referenceKey, workspaceID))
}

func (s *Store) ListSecretRecords(ctx context.Context, filter SecretRecordFilter) ([]domain.SecretRecord, error) {
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + secretRecordColumns + ` FROM secret_records WHERE workspace_id = ?`
	args := []any{workspaceID}
	if search := strings.ToLower(strings.TrimSpace(filter.Search)); search != "" {
		pattern := "%" + escapeLike(search) + "%"
		query += ` AND (lower(name) LIKE ? ESCAPE '\' OR lower(label) LIKE ? ESCAPE '\' OR lower(description) LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern, pattern)
	}
	query += " ORDER BY name, id"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list secret records: %w", err)
	}
	defer rows.Close()
	records := []domain.SecretRecord{}
	for rows.Next() {
		record, err := scanSecretRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list secret records: %w", err)
	}
	return records, nil
}

// UpdateSecretRecord updates mutable metadata in the active workspace.
// ReferenceKey is immutable because changing it would orphan the vault value.
func (s *Store) UpdateSecretRecord(ctx context.Context, record domain.SecretRecord) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.UpdateSecretRecordForWorkspace(ctx, workspaceID, record)
}

func (s *Store) UpdateSecretRecordForWorkspace(ctx context.Context, workspaceID string, record domain.SecretRecord) error {
	record.WorkspaceID = workspaceID
	record = normalizeSecretRecord(record)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update secret record: %w", err)
	}
	defer tx.Rollback()
	var referenceKey string
	if err := tx.QueryRowContext(ctx, `SELECT reference_key FROM secret_records WHERE id = ? AND workspace_id = ?`,
		record.ID, workspaceID).Scan(&referenceKey); err != nil {
		return fmt.Errorf("store: get secret record: %w", notFound("secret record", err))
	}
	if record.ReferenceKey != "" && record.ReferenceKey != referenceKey {
		return fmt.Errorf("store: secret record reference key is immutable")
	}
	record.ReferenceKey = referenceKey
	if err := validateSecretRecord(record); err != nil {
		return err
	}
	record.UpdatedAt = defaultTime(record.UpdatedAt, s.now())
	if _, err := tx.ExecContext(ctx, `UPDATE secret_records
SET name = ?, label = ?, description = ?, updated_at = ? WHERE id = ? AND workspace_id = ?`,
		record.Name, record.Label, record.Description, formatTime(record.UpdatedAt), record.ID, workspaceID); err != nil {
		return fmt.Errorf("store: update secret record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit secret record: %w", err)
	}
	return nil
}

func (s *Store) DeleteSecretRecord(ctx context.Context, id string) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.DeleteSecretRecordForWorkspace(ctx, workspaceID, id)
}

func (s *Store) DeleteSecretRecordForWorkspace(ctx context.Context, workspaceID, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM secret_records WHERE id = ? AND workspace_id = ?`, id, workspaceID)
	if err != nil {
		return fmt.Errorf("store: delete secret record: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete secret record result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("secret record %q: %w", id, ErrNotFound)
	}
	return nil
}

func normalizeSecretRecord(record domain.SecretRecord) domain.SecretRecord {
	record.ID = strings.TrimSpace(record.ID)
	record.WorkspaceID = strings.TrimSpace(record.WorkspaceID)
	record.Name = strings.TrimSpace(record.Name)
	record.Label = strings.TrimSpace(record.Label)
	record.Description = strings.TrimSpace(record.Description)
	record.ReferenceKey = strings.TrimSpace(record.ReferenceKey)
	if record.Label == "" {
		record.Label = record.Name
	}
	return record
}

func validateSecretRecord(record domain.SecretRecord) error {
	if record.ID == "" || record.Name == "" {
		return fmt.Errorf("store: secret record ID and name are required")
	}
	if len(record.Name) > 128 || len(record.Label) > 256 || len(record.Description) > 4096 {
		return fmt.Errorf("store: secret record metadata exceeds size limit")
	}
	ref := credentials.Ref{WorkspaceID: record.WorkspaceID, Integration: domain.SecretCredentialIntegration, Name: record.ReferenceKey}
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("store: invalid secret record reference: %w", err)
	}
	return nil
}

func scanSecretRecord(row rowScanner) (domain.SecretRecord, error) {
	var record domain.SecretRecord
	var created, updated string
	if err := row.Scan(&record.ID, &record.WorkspaceID, &record.Name, &record.Label,
		&record.Description, &record.ReferenceKey, &created, &updated); err != nil {
		return domain.SecretRecord{}, fmt.Errorf("store: get secret record: %w", notFound("secret record", err))
	}
	var err error
	record.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.SecretRecord{}, err
	}
	record.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.SecretRecord{}, err
	}
	return record, nil
}

func validScriptCredentialEnvironment(env string) bool {
	if !safeEnvironmentVariable.MatchString(env) {
		return false
	}
	if _, reserved := reservedEnvironmentVariables[env]; reserved {
		return false
	}
	return !strings.HasPrefix(env, "LD_") && !strings.HasPrefix(env, "DYLD_") &&
		!strings.HasPrefix(env, "BASH_FUNC_") && !strings.HasPrefix(env, "GIT_CONFIG_") &&
		!strings.HasSuffix(env, "_ASKPASS")
}

// ListScriptCredentialBindings returns bindings for a script in the active
// workspace. Runtime credential references are hydrated from immutable record
// metadata; no credential value is read here.
func (s *Store) ListScriptCredentialBindings(ctx context.Context, scriptID string) ([]domain.ScriptCredentialBinding, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	return s.ListScriptCredentialBindingsForWorkspace(ctx, workspaceID, scriptID)
}

func (s *Store) ListScriptCredentialBindingsForWorkspace(ctx context.Context, workspaceID, scriptID string) ([]domain.ScriptCredentialBinding, error) {
	return listScriptCredentialBindings(ctx, s.db, workspaceID, scriptID)
}

// SetScriptCredentialBindings atomically replaces all bindings for a script in
// the active workspace. Passing an empty or nil slice explicitly clears them.
func (s *Store) SetScriptCredentialBindings(ctx context.Context, scriptID string, bindings []domain.ScriptCredentialBinding) ([]domain.ScriptCredentialBinding, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	return s.SetScriptCredentialBindingsForWorkspace(ctx, workspaceID, scriptID, bindings)
}

func (s *Store) SetScriptCredentialBindingsForWorkspace(ctx context.Context, workspaceID, scriptID string, bindings []domain.ScriptCredentialBinding) ([]domain.ScriptCredentialBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin set script credential bindings: %w", err)
	}
	defer tx.Rollback()
	result, err := s.replaceScriptCredentialBindingsTx(ctx, tx, workspaceID, scriptID, bindings)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit script credential bindings: %w", err)
	}
	return result, nil
}

func (s *Store) replaceScriptCredentialBindingsTx(ctx context.Context, tx *sql.Tx, workspaceID, scriptID string, bindings []domain.ScriptCredentialBinding) ([]domain.ScriptCredentialBinding, error) {
	if workspaceID == "" || scriptID == "" {
		return nil, fmt.Errorf("store: script credential bindings require a workspace and script")
	}
	if len(bindings) > 64 {
		return nil, fmt.Errorf("store: script credential bindings exceed limit of 64")
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM scripts WHERE id = ? AND workspace_id = ?`, scriptID, workspaceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("store: bind script credentials: %w", notFound("script", err))
	}

	type validatedBinding struct {
		env          string
		secretID     string
		referenceKey string
	}
	validated := make([]validatedBinding, 0, len(bindings))
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		env := strings.TrimSpace(binding.Env)
		secretID := strings.TrimSpace(binding.SecretRecordID)
		if !validScriptCredentialEnvironment(env) {
			return nil, fmt.Errorf("store: unsafe script credential environment variable %q", env)
		}
		if secretID == "" {
			return nil, fmt.Errorf("store: script credential binding %q requires a secret record", env)
		}
		if _, duplicate := seen[env]; duplicate {
			return nil, fmt.Errorf("store: duplicate script credential environment variable %q", env)
		}
		seen[env] = struct{}{}
		var referenceKey string
		if err := tx.QueryRowContext(ctx, `SELECT reference_key FROM secret_records
WHERE id = ? AND workspace_id = ?`, secretID, workspaceID).Scan(&referenceKey); err != nil {
			return nil, fmt.Errorf("store: bind %q: %w", env, notFound("secret record", err))
		}
		validated = append(validated, validatedBinding{env: env, secretID: secretID, referenceKey: referenceKey})
	}

	// Validation deliberately completes before deletion so an invalid request
	// cannot partially replace the prior binding set.
	if _, err := tx.ExecContext(ctx, `DELETE FROM script_credential_bindings WHERE script_id = ?`, scriptID); err != nil {
		return nil, fmt.Errorf("store: clear script credential bindings: %w", err)
	}
	now := formatTime(s.now())
	result := make([]domain.ScriptCredentialBinding, 0, len(validated))
	for _, binding := range validated {
		if _, err := tx.ExecContext(ctx, `INSERT INTO script_credential_bindings
(script_id, env, secret_record_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			scriptID, binding.env, binding.secretID, now, now); err != nil {
			return nil, fmt.Errorf("store: create script credential binding %q: %w", binding.env, err)
		}
		result = append(result, domain.ScriptCredentialBinding{
			Env:                   binding.env,
			SecretRecordID:        binding.secretID,
			CredentialIntegration: domain.SecretCredentialIntegration,
			CredentialName:        binding.referenceKey,
		})
	}
	return result, nil
}

type contextQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func listScriptCredentialBindings(ctx context.Context, queryer contextQueryer, workspaceID, scriptID string) ([]domain.ScriptCredentialBinding, error) {
	var exists int
	if err := queryer.QueryRowContext(ctx, `SELECT 1 FROM scripts WHERE id = ? AND COALESCE(workspace_id, '') = ?`, scriptID, workspaceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("store: list script credential bindings: %w", notFound("script", err))
	}
	rows, err := queryer.QueryContext(ctx, `
SELECT binding.env, binding.secret_record_id, record.reference_key
FROM script_credential_bindings binding
JOIN secret_records record ON record.id = binding.secret_record_id
WHERE binding.script_id = ? AND record.workspace_id = ?
ORDER BY binding.env`, scriptID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("store: list script credential bindings: %w", err)
	}
	defer rows.Close()
	result := []domain.ScriptCredentialBinding{}
	for rows.Next() {
		var binding domain.ScriptCredentialBinding
		if err := rows.Scan(&binding.Env, &binding.SecretRecordID, &binding.CredentialName); err != nil {
			return nil, fmt.Errorf("store: scan script credential binding: %w", err)
		}
		binding.CredentialIntegration = domain.SecretCredentialIntegration
		result = append(result, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list script credential bindings: %w", err)
	}
	return result, nil
}
