package operator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

type mcpAuthCacheEntry struct {
	status    string
	expiresAt time.Time
}

func (o *Operator) BrowserMCPStatus(context.Context) (api.BrowserMCPStatus, error) {
	runtime := discoverBuiltInBrowserRuntime()
	return api.BrowserMCPStatus{
		Name: builtInBrowserMCPName, Available: runtime.Available,
		Provider: "Playwright MCP", Package: builtInBrowserPackage,
		Browser: "Google Chrome", Isolated: true, Reason: runtime.Reason,
	}, nil
}

func (o *Operator) MCPServers(ctx context.Context) ([]domain.MCPServer, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return nil, translateNotFound(err)
	}
	servers, err := o.store.ListMCPServers(ctx, store.MCPServerFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return nil, err
	}
	for index := range servers {
		o.hydrateMCPReadiness(ctx, &servers[index])
	}
	return servers, nil
}

func (o *Operator) MCPServer(ctx context.Context, id string) (domain.MCPServer, error) {
	server, err := o.store.GetMCPServer(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.MCPServer{}, translateNotFound(err)
	}
	o.hydrateMCPReadiness(ctx, &server)
	return server, nil
}

func (o *Operator) CreateMCPServer(ctx context.Context, input api.MCPServerInput) (domain.MCPServer, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return domain.MCPServer{}, translateNotFound(err)
	}
	server := domain.MCPServer{WorkspaceID: workspace.ID, Enabled: true, Access: domain.MCPAccessRead}
	applyMCPInput(&server, input)
	created, err := o.store.CreateMCPServer(ctx, server)
	if err != nil {
		return domain.MCPServer{}, translateNotFound(err)
	}
	o.hydrateMCPReadiness(ctx, &created)
	o.emitForWorkspace(ctx, workspace.ID, "mcp.server.created", created.ID, map[string]string{"name": created.Name})
	return created, nil
}

func (o *Operator) UpdateMCPServer(ctx context.Context, id string, input api.MCPServerInput) (domain.MCPServer, error) {
	server, err := o.store.GetMCPServer(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.MCPServer{}, translateNotFound(err)
	}
	applyMCPInput(&server, input)
	server.UpdatedAt = time.Now().UTC()
	if err := o.store.UpdateMCPServerForWorkspace(ctx, server.WorkspaceID, server); err != nil {
		return domain.MCPServer{}, translateNotFound(err)
	}
	updated, err := o.store.GetMCPServerForWorkspace(ctx, server.WorkspaceID, server.ID)
	if err != nil {
		return domain.MCPServer{}, err
	}
	o.hydrateMCPReadiness(ctx, &updated)
	o.emitForWorkspace(ctx, server.WorkspaceID, "mcp.server.updated", server.ID, map[string]string{"name": server.Name})
	return updated, nil
}

func (o *Operator) DeleteMCPServer(ctx context.Context, id string) error {
	server, err := o.store.GetMCPServer(ctx, strings.TrimSpace(id))
	if err != nil {
		return translateNotFound(err)
	}
	if server.Auth == domain.MCPAuthOAuth {
		name := mcpConfigName(server)
		if config, configErr := mcpConfigValue(server); configErr == nil {
			commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			_ = exec.CommandContext(commandCtx, o.codexCommand(ctx), "-c", "mcp_servers."+name+"="+config, "mcp", "logout", name).Run()
			cancel()
		}
	}
	if err := o.store.DeleteMCPServerForWorkspace(ctx, server.WorkspaceID, server.ID); err != nil {
		return translateNotFound(err)
	}
	o.emitForWorkspace(ctx, server.WorkspaceID, "mcp.server.deleted", server.ID, map[string]string{"name": server.Name})
	return nil
}

func applyMCPInput(server *domain.MCPServer, input api.MCPServerInput) {
	if input.Name != nil {
		server.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		server.Description = redactSecrets(strings.TrimSpace(*input.Description))
	}
	if input.Transport != nil {
		server.Transport = *input.Transport
	}
	if input.Command != nil {
		server.Command = strings.TrimSpace(*input.Command)
	}
	if input.Args != nil {
		server.Args = append([]string(nil), (*input.Args)...)
	}
	if input.URL != nil {
		server.URL = strings.TrimSpace(*input.URL)
	}
	if input.Auth != nil {
		server.Auth = *input.Auth
	}
	if input.Enabled != nil {
		server.Enabled = *input.Enabled
	}
	if input.Access != nil {
		server.Access = *input.Access
	}
	if input.Required != nil {
		server.Required = *input.Required
	}
	if input.StartupTimeoutSeconds != nil {
		server.StartupTimeoutSeconds = *input.StartupTimeoutSeconds
	}
	if input.ToolTimeoutSeconds != nil {
		server.ToolTimeoutSeconds = *input.ToolTimeoutSeconds
	}
	if input.EnabledTools != nil {
		server.EnabledTools = append([]string(nil), (*input.EnabledTools)...)
	}
	if input.DisabledTools != nil {
		server.DisabledTools = append([]string(nil), (*input.DisabledTools)...)
	}
	if input.SecretBindings != nil {
		server.SecretBindings = append([]domain.MCPSecretBinding(nil), (*input.SecretBindings)...)
	}
	if server.Transport == domain.MCPTransportStdio {
		server.URL, server.Auth = "", domain.MCPAuthNone
	}
	if server.Transport == domain.MCPTransportHTTP {
		server.Command, server.Args = "", []string{}
	}
}

func (o *Operator) hydrateMCPReadiness(ctx context.Context, server *domain.MCPServer) {
	server.Ready = server.Enabled
	server.AuthStatus = "not_required"
	server.MissingSecrets = []string{}
	for _, binding := range server.SecretBindings {
		secret, err := o.credentials.Get(ctx, credentials.Ref{
			WorkspaceID: server.WorkspaceID, Integration: binding.CredentialIntegration, Name: binding.CredentialName,
		})
		if err == nil {
			secret.Destroy()
			continue
		}
		server.Ready = false
		server.MissingSecrets = append(server.MissingSecrets, binding.Env)
	}
	if !server.Enabled {
		server.Ready = false
		server.AuthStatus = "disabled"
		return
	}
	if server.Transport == domain.MCPTransportHTTP && server.Auth == domain.MCPAuthOAuth {
		status, err := o.mcpOAuthStatus(ctx, *server)
		server.AuthStatus = status
		if err != nil || status != "logged_in" {
			server.Ready = false
		}
	} else if server.Auth == domain.MCPAuthSecret {
		server.AuthStatus = "secret"
	}
}

func (o *Operator) AuthenticateMCPServer(ctx context.Context, id string) (string, error) {
	server, err := o.MCPServer(ctx, id)
	if err != nil {
		return "", err
	}
	if server.Transport != domain.MCPTransportHTTP || server.Auth != domain.MCPAuthOAuth {
		return "", fmt.Errorf("%w: this connector is not configured for OAuth sign-in", api.ErrInvalid)
	}
	o.mu.Lock()
	if o.lifecycleContext == nil {
		o.mu.Unlock()
		return "", fmt.Errorf("%w: Nabu must be running to open MCP sign-in", api.ErrUnavailable)
	}
	loginCtx := o.lifecycleContext
	o.mu.Unlock()
	name := mcpConfigName(server)
	config, err := mcpConfigValue(server)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(loginCtx, o.codexCommand(ctx), "-c", "mcp_servers."+name+"="+config, "mcp", "login", name)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("%w: Codex could not prepare MCP sign-in", api.ErrUnavailable)
	}
	var stderr cappedWriter
	stderr.limit = 4096
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("%w: Codex could not start MCP sign-in", api.ErrUnavailable)
	}
	authorizationURL := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 64*1024)
		found := false
		for scanner.Scan() {
			if found {
				continue
			}
			for _, field := range strings.Fields(scanner.Text()) {
				candidate := strings.TrimSpace(field)
				if strings.HasPrefix(candidate, "https://") {
					found = true
					authorizationURL <- candidate
					break
				}
			}
		}
		waitErr := command.Wait()
		if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
			o.logger.Warn("MCP OAuth sign-in ended without a connection", "server_id", server.ID, "error", waitErr)
		}
		o.mcpAuthMu.Lock()
		delete(o.mcpAuthCache, server.ID)
		o.mcpAuthMu.Unlock()
		o.emitForWorkspace(context.Background(), server.WorkspaceID, "mcp.server.auth_changed", server.ID, map[string]string{"name": server.Name})
		done <- waitErr
	}()
	select {
	case url := <-authorizationURL:
		return url, nil
	case waitErr := <-done:
		if waitErr == nil {
			return "", nil
		}
		return "", fmt.Errorf("%w: Codex could not begin MCP sign-in", api.ErrUnavailable)
	case <-time.After(8 * time.Second):
		return "", fmt.Errorf("%w: MCP sign-in did not provide an authorization URL", api.ErrUnavailable)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (o *Operator) MCPServerAuthStatus(ctx context.Context, id string) (domain.MCPServer, error) {
	server, err := o.MCPServer(ctx, id)
	if err != nil {
		return domain.MCPServer{}, err
	}
	return server, nil
}

func (o *Operator) mcpOAuthStatus(ctx context.Context, server domain.MCPServer) (string, error) {
	o.mcpAuthMu.Lock()
	if cached, ok := o.mcpAuthCache[server.ID]; ok && time.Now().Before(cached.expiresAt) {
		o.mcpAuthMu.Unlock()
		return cached.status, nil
	}
	o.mcpAuthMu.Unlock()
	name := mcpConfigName(server)
	config, err := mcpConfigValue(server)
	if err != nil {
		return "error", err
	}
	commandCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, o.codexCommand(ctx), "-c", "mcp_servers."+name+"="+config, "mcp", "list", "--json")
	var stdout, stderr cappedWriter
	stdout.limit, stderr.limit = 64*1024, 4096
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return "error", fmt.Errorf("inspect MCP OAuth status: %w", err)
	}
	var items []struct {
		Name       string `json:"name"`
		AuthStatus string `json:"auth_status"`
	}
	if err := json.NewDecoder(bytes.NewReader(stdout.Bytes())).Decode(&items); err != nil {
		return "error", fmt.Errorf("decode MCP OAuth status: %w", err)
	}
	for _, item := range items {
		if item.Name == name {
			status := normalizeMCPOAuthStatus(item.AuthStatus)
			o.mcpAuthMu.Lock()
			o.mcpAuthCache[server.ID] = mcpAuthCacheEntry{status: status, expiresAt: time.Now().Add(2 * time.Second)}
			o.mcpAuthMu.Unlock()
			return status, nil
		}
	}
	return "not_logged_in", nil
}

// Codex has emitted both "logged_in" and the enum-derived "o_auth" value for
// a completed OAuth login across CLI versions. Nabu exposes one stable status
// to its API so readiness and the Settings UI do not depend on that spelling.
func normalizeMCPOAuthStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "logged_in", "authenticated", "oauth", "o_auth":
		return "logged_in"
	case "not_authenticated":
		return "not_logged_in"
	default:
		return status
	}
}

var _ api.MCPBackend = (*Operator)(nil)
