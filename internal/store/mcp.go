package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const mcpServerColumns = `id, workspace_id, name, description, transport, command, args_json, url,
auth, enabled, access, required, startup_timeout_seconds, tool_timeout_seconds,
enabled_tools_json, disabled_tools_json, created_at, updated_at`

var mcpHeaderName = regexp.MustCompile(`^[!#$%&'*+.^_\x60|~0-9A-Za-z-]{1,128}$`)

type MCPServerFilter struct {
	WorkspaceID string
	Enabled     *bool
	Limit       int
}

func (s *Store) CreateMCPServer(ctx context.Context, server domain.MCPServer) (domain.MCPServer, error) {
	var err error
	server.WorkspaceID, err = s.defaultWorkspaceID(ctx, server.WorkspaceID)
	if err != nil {
		return domain.MCPServer{}, err
	}
	if server.ID == "" {
		server.ID, err = newID()
		if err != nil {
			return domain.MCPServer{}, err
		}
	}
	server = normalizeMCPServer(server)
	if err := validateMCPServer(server); err != nil {
		return domain.MCPServer{}, err
	}
	now := s.now()
	server.CreatedAt = defaultTime(server.CreatedAt, now)
	server.UpdatedAt = defaultTime(server.UpdatedAt, server.CreatedAt)
	args, _ := json.Marshal(server.Args)
	enabledTools, _ := json.Marshal(server.EnabledTools)
	disabledTools, _ := json.Marshal(server.DisabledTools)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("store: begin create MCP server: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO mcp_servers (`+mcpServerColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, server.ID, server.WorkspaceID,
		server.Name, server.Description, server.Transport, server.Command, args, server.URL, server.Auth, server.Enabled,
		server.Access, server.Required, server.StartupTimeoutSeconds, server.ToolTimeoutSeconds, enabledTools,
		disabledTools, formatTime(server.CreatedAt), formatTime(server.UpdatedAt)); err != nil {
		return domain.MCPServer{}, fmt.Errorf("store: create MCP server: %w", err)
	}
	server.SecretBindings, err = replaceMCPBindingsTx(ctx, tx, server, server.SecretBindings, s.now())
	if err != nil {
		return domain.MCPServer{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MCPServer{}, fmt.Errorf("store: commit MCP server: %w", err)
	}
	return server, nil
}

func (s *Store) GetMCPServer(ctx context.Context, id string) (domain.MCPServer, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.MCPServer{}, err
	}
	return s.GetMCPServerForWorkspace(ctx, workspaceID, id)
}

func (s *Store) GetMCPServerForWorkspace(ctx context.Context, workspaceID, id string) (domain.MCPServer, error) {
	server, err := scanMCPServer(s.db.QueryRowContext(ctx, `SELECT `+mcpServerColumns+`
FROM mcp_servers WHERE id = ? AND workspace_id = ?`, id, workspaceID))
	if err != nil {
		return domain.MCPServer{}, err
	}
	server.SecretBindings, err = listMCPBindings(ctx, s.db, workspaceID, server.ID)
	return server, err
}

func (s *Store) ListMCPServers(ctx context.Context, filter MCPServerFilter) ([]domain.MCPServer, error) {
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + mcpServerColumns + ` FROM mcp_servers WHERE workspace_id = ?`
	args := []any{workspaceID}
	if filter.Enabled != nil {
		query += ` AND enabled = ?`
		args = append(args, *filter.Enabled)
	}
	query += ` ORDER BY name, id`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list MCP servers: %w", err)
	}
	servers := []domain.MCPServer{}
	for rows.Next() {
		server, scanErr := scanMCPServer(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		servers = append(servers, server)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("store: list MCP servers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close MCP server rows: %w", err)
	}
	// The store intentionally uses one SQLite connection. Finish the outer
	// cursor before hydrating child bindings to avoid waiting on ourselves.
	for index := range servers {
		servers[index].SecretBindings, err = listMCPBindings(ctx, s.db, workspaceID, servers[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return servers, nil
}

func (s *Store) UpdateMCPServer(ctx context.Context, server domain.MCPServer) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.UpdateMCPServerForWorkspace(ctx, workspaceID, server)
}

func (s *Store) UpdateMCPServerForWorkspace(ctx context.Context, workspaceID string, server domain.MCPServer) error {
	server.WorkspaceID = workspaceID
	server = normalizeMCPServer(server)
	if err := validateMCPServer(server); err != nil {
		return err
	}
	server.UpdatedAt = defaultTime(server.UpdatedAt, s.now())
	args, _ := json.Marshal(server.Args)
	enabledTools, _ := json.Marshal(server.EnabledTools)
	disabledTools, _ := json.Marshal(server.DisabledTools)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update MCP server: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE mcp_servers SET name = ?, description = ?, transport = ?, command = ?,
args_json = ?, url = ?, auth = ?, enabled = ?, access = ?, required = ?, startup_timeout_seconds = ?,
tool_timeout_seconds = ?, enabled_tools_json = ?, disabled_tools_json = ?, updated_at = ?
WHERE id = ? AND workspace_id = ?`, server.Name, server.Description, server.Transport, server.Command, args,
		server.URL, server.Auth, server.Enabled, server.Access, server.Required, server.StartupTimeoutSeconds, server.ToolTimeoutSeconds,
		enabledTools, disabledTools, formatTime(server.UpdatedAt), server.ID, workspaceID)
	if err != nil {
		return fmt.Errorf("store: update MCP server: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return notFound("MCP server", sql.ErrNoRows)
	}
	if _, err := replaceMCPBindingsTx(ctx, tx, server, server.SecretBindings, s.now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit MCP server: %w", err)
	}
	return nil
}

func (s *Store) DeleteMCPServer(ctx context.Context, id string) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	return s.DeleteMCPServerForWorkspace(ctx, workspaceID, id)
}

func (s *Store) DeleteMCPServerForWorkspace(ctx context.Context, workspaceID, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = ? AND workspace_id = ?`, id, workspaceID)
	if err != nil {
		return fmt.Errorf("store: delete MCP server: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return notFound("MCP server", sql.ErrNoRows)
	}
	return nil
}

func normalizeMCPServer(server domain.MCPServer) domain.MCPServer {
	server.ID = strings.TrimSpace(server.ID)
	server.WorkspaceID = strings.TrimSpace(server.WorkspaceID)
	server.Name = strings.TrimSpace(server.Name)
	server.Description = strings.TrimSpace(server.Description)
	server.Command = strings.TrimSpace(server.Command)
	server.URL = strings.TrimSpace(server.URL)
	if server.Auth == "" {
		server.Auth = domain.MCPAuthNone
	}
	if server.Transport == domain.MCPTransportHTTP && server.Auth == domain.MCPAuthNone && len(server.SecretBindings) > 0 {
		server.Auth = domain.MCPAuthSecret
	}
	if server.Access == "" {
		server.Access = domain.MCPAccessRead
	}
	if server.StartupTimeoutSeconds <= 0 {
		server.StartupTimeoutSeconds = 10
	}
	if server.ToolTimeoutSeconds <= 0 {
		server.ToolTimeoutSeconds = 60
	}
	if server.Args == nil {
		server.Args = []string{}
	}
	if server.EnabledTools == nil {
		server.EnabledTools = []string{}
	}
	if server.DisabledTools == nil {
		server.DisabledTools = []string{}
	}
	if server.SecretBindings == nil {
		server.SecretBindings = []domain.MCPSecretBinding{}
	}
	return server
}

func validateMCPServer(server domain.MCPServer) error {
	if server.ID == "" || server.WorkspaceID == "" || server.Name == "" {
		return fmt.Errorf("store: MCP server ID, workspace, and name are required")
	}
	if len(server.Name) > 128 || len(server.Description) > 4096 || len(server.Args) > 64 ||
		len(server.EnabledTools) > 128 || len(server.DisabledTools) > 128 {
		return fmt.Errorf("store: MCP server metadata exceeds its limit")
	}
	if server.Access != domain.MCPAccessRead && server.Access != domain.MCPAccessFull {
		return fmt.Errorf("store: invalid MCP access %q", server.Access)
	}
	if server.StartupTimeoutSeconds < 1 || server.StartupTimeoutSeconds > 120 || server.ToolTimeoutSeconds < 1 || server.ToolTimeoutSeconds > 600 {
		return fmt.Errorf("store: MCP timeout is outside the supported range")
	}
	switch server.Transport {
	case domain.MCPTransportStdio:
		if server.Command == "" || strings.ContainsRune(server.Command, 0) || len(server.Command) > 2048 || server.URL != "" {
			return fmt.Errorf("store: stdio MCP servers require a command and no URL")
		}
		if runtime.GOOS != "windows" && !strings.HasPrefix(server.Command, "/") {
			return fmt.Errorf("store: stdio MCP commands must use an absolute executable path")
		}
		if server.Auth != domain.MCPAuthNone {
			return fmt.Errorf("store: stdio MCP servers do not support HTTP authentication")
		}
	case domain.MCPTransportHTTP:
		if server.URL == "" || server.Command != "" || len(server.Args) != 0 {
			return fmt.Errorf("store: HTTP MCP servers require a URL and no command")
		}
		parsed, err := url.Parse(server.URL)
		if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" {
			return fmt.Errorf("store: MCP server URL is invalid")
		}
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackMCPHost(parsed.Hostname())) {
			return fmt.Errorf("store: remote MCP servers require HTTPS; HTTP is allowed only on loopback")
		}
		if server.Auth != domain.MCPAuthNone && server.Auth != domain.MCPAuthOAuth && server.Auth != domain.MCPAuthSecret {
			return fmt.Errorf("store: invalid MCP authentication %q", server.Auth)
		}
		if server.Auth == domain.MCPAuthSecret && len(server.SecretBindings) == 0 {
			return fmt.Errorf("store: secret-authenticated MCP servers require a secret binding")
		}
	default:
		return fmt.Errorf("store: invalid MCP transport %q", server.Transport)
	}
	for _, values := range [][]string{server.Args, server.EnabledTools, server.DisabledTools} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || len(value) > 2048 {
				return fmt.Errorf("store: MCP argument or tool name is invalid")
			}
		}
	}
	return nil
}

func isLoopbackMCPHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func replaceMCPBindingsTx(ctx context.Context, tx *sql.Tx, server domain.MCPServer, bindings []domain.MCPSecretBinding, now time.Time) ([]domain.MCPSecretBinding, error) {
	if len(bindings) > 32 {
		return nil, fmt.Errorf("store: MCP secret bindings exceed limit of 32")
	}
	type validated struct {
		binding   domain.MCPSecretBinding
		reference string
	}
	items := make([]validated, 0, len(bindings))
	seenEnv, seenHeader := map[string]struct{}{}, map[string]struct{}{}
	bearerCount := 0
	for _, input := range bindings {
		binding := input
		binding.SecretRecordID = strings.TrimSpace(binding.SecretRecordID)
		binding.Env = strings.TrimSpace(binding.Env)
		binding.Header = strings.TrimSpace(binding.Header)
		if !validScriptCredentialEnvironment(binding.Env) || binding.SecretRecordID == "" {
			return nil, fmt.Errorf("store: invalid MCP secret binding")
		}
		if _, duplicate := seenEnv[binding.Env]; duplicate {
			return nil, fmt.Errorf("store: duplicate MCP environment variable %q", binding.Env)
		}
		seenEnv[binding.Env] = struct{}{}
		if server.Transport == domain.MCPTransportStdio && (binding.Bearer || binding.Header != "") {
			return nil, fmt.Errorf("store: stdio MCP bindings use environment variables only")
		}
		if binding.Bearer {
			bearerCount++
			if bearerCount > 1 || binding.Header != "" {
				return nil, fmt.Errorf("store: HTTP MCP servers accept one bearer binding")
			}
		} else if server.Transport == domain.MCPTransportHTTP {
			if !mcpHeaderName.MatchString(binding.Header) || strings.EqualFold(binding.Header, "Authorization") {
				return nil, fmt.Errorf("store: invalid MCP HTTP header %q", binding.Header)
			}
			key := strings.ToLower(binding.Header)
			if _, duplicate := seenHeader[key]; duplicate {
				return nil, fmt.Errorf("store: duplicate MCP HTTP header %q", binding.Header)
			}
			seenHeader[key] = struct{}{}
		}
		var reference string
		if err := tx.QueryRowContext(ctx, `SELECT reference_key FROM secret_records WHERE id = ? AND workspace_id = ?`,
			binding.SecretRecordID, server.WorkspaceID).Scan(&reference); err != nil {
			return nil, fmt.Errorf("store: bind MCP secret: %w", notFound("secret record", err))
		}
		items = append(items, validated{binding: binding, reference: reference})
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_secret_bindings WHERE server_id = ?`, server.ID); err != nil {
		return nil, fmt.Errorf("store: clear MCP secret bindings: %w", err)
	}
	stamp := formatTime(now)
	result := make([]domain.MCPSecretBinding, 0, len(items))
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mcp_secret_bindings
(server_id, env_var, secret_record_id, header_name, bearer, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			server.ID, item.binding.Env, item.binding.SecretRecordID, item.binding.Header, item.binding.Bearer, stamp, stamp); err != nil {
			return nil, fmt.Errorf("store: create MCP secret binding: %w", err)
		}
		item.binding.CredentialIntegration = domain.SecretCredentialIntegration
		item.binding.CredentialName = item.reference
		result = append(result, item.binding)
	}
	return result, nil
}

func listMCPBindings(ctx context.Context, queryer contextQueryer, workspaceID, serverID string) ([]domain.MCPSecretBinding, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT binding.secret_record_id, binding.env_var, binding.header_name,
binding.bearer, record.reference_key FROM mcp_secret_bindings binding
JOIN secret_records record ON record.id = binding.secret_record_id
WHERE binding.server_id = ? AND record.workspace_id = ? ORDER BY binding.env_var`, serverID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("store: list MCP secret bindings: %w", err)
	}
	defer rows.Close()
	result := []domain.MCPSecretBinding{}
	for rows.Next() {
		var binding domain.MCPSecretBinding
		if err := rows.Scan(&binding.SecretRecordID, &binding.Env, &binding.Header, &binding.Bearer, &binding.CredentialName); err != nil {
			return nil, fmt.Errorf("store: scan MCP secret binding: %w", err)
		}
		binding.CredentialIntegration = domain.SecretCredentialIntegration
		result = append(result, binding)
	}
	return result, rows.Err()
}

func scanMCPServer(row rowScanner) (domain.MCPServer, error) {
	var server domain.MCPServer
	var args, enabledTools, disabledTools []byte
	var created, updated string
	if err := row.Scan(&server.ID, &server.WorkspaceID, &server.Name, &server.Description, &server.Transport,
		&server.Command, &args, &server.URL, &server.Auth, &server.Enabled, &server.Access, &server.Required,
		&server.StartupTimeoutSeconds, &server.ToolTimeoutSeconds, &enabledTools, &disabledTools, &created, &updated); err != nil {
		return domain.MCPServer{}, fmt.Errorf("store: get MCP server: %w", notFound("MCP server", err))
	}
	if err := json.Unmarshal(args, &server.Args); err != nil {
		return domain.MCPServer{}, fmt.Errorf("store: decode MCP args: %w", err)
	}
	if err := json.Unmarshal(enabledTools, &server.EnabledTools); err != nil {
		return domain.MCPServer{}, fmt.Errorf("store: decode MCP enabled tools: %w", err)
	}
	if err := json.Unmarshal(disabledTools, &server.DisabledTools); err != nil {
		return domain.MCPServer{}, fmt.Errorf("store: decode MCP disabled tools: %w", err)
	}
	var err error
	if server.CreatedAt, err = parseTime(created); err != nil {
		return domain.MCPServer{}, err
	}
	if server.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.MCPServer{}, err
	}
	return normalizeMCPServer(server), nil
}
