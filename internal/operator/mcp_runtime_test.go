package operator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
)

func TestBuiltInBrowserMCPConfigIsPinnedIsolatedAndSandboxed(t *testing.T) {
	config := builtInBrowserMCPConfig("/absolute/npx", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/approved/media/browser")
	for _, expected := range []string{
		builtInBrowserPackage, `command="/absolute/npx"`, `"--browser","chrome"`,
		`"--isolated"`, `"--snapshot-boxes"`, `"--viewport-size","1280x800"`,
		`"--output-dir","/approved/media/browser"`, `default_tools_approval_mode="approve"`,
	} {
		if !strings.Contains(config, expected) {
			t.Errorf("browser MCP config missing %q: %s", expected, config)
		}
	}
	if strings.Contains(config, "--no-sandbox") || strings.Contains(config, "--allow-unrestricted-file-access") {
		t.Fatalf("browser MCP config weakens isolation: %s", config)
	}
}

func TestCodexRunWithBrowserInsertsConfigBeforePromptMarker(t *testing.T) {
	service, _, _, workspace := testOperator(t, fakeExecutor{})
	args, secrets, err := service.codexRunWithBrowser(context.Background(), workspace.ID, workspace.Path, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 || len(args) < 3 || args[len(args)-1] != "-" {
		t.Fatalf("browser runtime args=%v secrets=%v", args, secrets)
	}
	joined := strings.Join(args, " ")
	if discoverBuiltInBrowserRuntime().Available {
		if !strings.Contains(joined, "mcp_servers."+builtInBrowserConfigName) || !strings.Contains(joined, builtInBrowserPackage) || !strings.Contains(joined, filepath.Join(workspace.Path, "media", "browser")) {
			t.Fatalf("browser MCP was not inserted: %q", joined)
		}
	}
}

func TestMCPRuntimeUsesVaultSecretWithoutPuttingValueInArguments(t *testing.T) {
	ctx := context.Background()
	service, _, _, workspace := testOperator(t, fakeExecutor{})
	backend := credentials.NewMemory()
	if err := service.ConfigureIntegrations(backend, nil, nil); err != nil {
		t.Fatal(err)
	}
	secret, err := service.CreateSecret(ctx, api.SecretCreate{Name: "mcp_token"}, []byte("vault-only-token"))
	if err != nil {
		t.Fatal(err)
	}
	name, transport, url := "Research MCP", domain.MCPTransportHTTP, "https://mcp.example.com/mcp"
	bindings := []domain.MCPSecretBinding{{SecretRecordID: secret.ID, Env: "MCP_TOKEN", Bearer: true}}
	if _, err := service.CreateMCPServer(ctx, api.MCPServerInput{Name: &name, Transport: &transport, URL: &url, SecretBindings: &bindings}); err != nil {
		t.Fatal(err)
	}

	args, environment, err := service.mcpRuntime(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "vault-only-token") || !strings.Contains(joined, "bearer_token_env_var") || !strings.Contains(joined, "MCP_TOKEN") {
		t.Fatalf("unsafe or incomplete MCP arguments: %q", joined)
	}
	if len(environment) != 1 || string(environment[0].Value) != "vault-only-token" {
		t.Fatalf("runtime environment = %#v", environment)
	}
	wipeBytes(environment[0].Value)
}

func TestOptionalMCPWithMissingSecretIsSkippedButRequiredFails(t *testing.T) {
	ctx := context.Background()
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	if err := service.ConfigureIntegrations(credentials.NewMemory(), nil, nil); err != nil {
		t.Fatal(err)
	}
	record, err := database.CreateSecretRecord(ctx, domain.SecretRecord{WorkspaceID: workspace.ID, Name: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := database.CreateMCPServer(ctx, domain.MCPServer{WorkspaceID: workspace.ID, Name: "Optional", Transport: domain.MCPTransportHTTP, URL: "https://mcp.example.com/mcp", Enabled: true, SecretBindings: []domain.MCPSecretBinding{{SecretRecordID: record.ID, Env: "MCP_TOKEN", Bearer: true}}})
	if err != nil {
		t.Fatal(err)
	}
	args, environment, err := service.mcpRuntime(ctx, workspace.ID)
	if err != nil || len(args) != 0 || len(environment) != 0 {
		t.Fatalf("optional connector was not skipped: args=%v env=%v err=%v", args, environment, err)
	}
	server.Required = true
	if err := database.UpdateMCPServerForWorkspace(ctx, workspace.ID, server); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.mcpRuntime(ctx, workspace.ID); err == nil || !strings.Contains(err.Error(), "required MCP connector") {
		t.Fatalf("required missing connector error = %v", err)
	}
}

func TestOAuthMCPConfigUsesCodexOAuthWithoutSecretArguments(t *testing.T) {
	server := domain.MCPServer{ID: "oauth-server", Name: "Sentry", Transport: domain.MCPTransportHTTP, URL: "https://mcp.sentry.dev/mcp/example", Auth: domain.MCPAuthOAuth, Access: domain.MCPAccessRead, Enabled: true, StartupTimeoutSeconds: 10, ToolTimeoutSeconds: 60}
	value, err := mcpConfigValue(server)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, `auth="oauth"`) || strings.Contains(strings.ToLower(value), "token") {
		t.Fatalf("OAuth MCP config = %q", value)
	}
}
