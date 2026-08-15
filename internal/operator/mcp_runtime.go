package operator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

const (
	builtInBrowserMCPName    = "Nabu Browser"
	builtInBrowserConfigName = "nabu_browser"
	builtInBrowserPackage    = "@playwright/mcp@0.0.79"
)

type builtInBrowserRuntime struct {
	Command    string
	Executable string
	Available  bool
	Reason     string
}

// mcpRuntime returns per-run Codex configuration and secret environment
// values. Configuration values contain only metadata and environment variable
// names; credential bytes are owned by the Request until the process starts.
func (o *Operator) mcpRuntime(ctx context.Context, workspaceID string) ([]string, []runner.EnvironmentSecret, error) {
	enabled := true
	servers, err := o.store.ListMCPServers(ctx, store.MCPServerFilter{WorkspaceID: workspaceID, Enabled: &enabled, Limit: 32})
	if err != nil {
		return nil, nil, err
	}
	if len(servers) == 0 {
		return nil, nil, nil
	}
	args := []string{}
	secrets := []runner.EnvironmentSecret{}
	seenEnvironment := map[string]struct{}{}
	destroy := func() {
		for index := range secrets {
			wipeBytes(secrets[index].Value)
			secrets[index].Value = nil
		}
	}
	for _, server := range servers {
		if server.Transport == domain.MCPTransportHTTP && server.Auth == domain.MCPAuthOAuth {
			status, statusErr := o.mcpOAuthStatus(ctx, server)
			if statusErr != nil || status != "logged_in" {
				if server.Required {
					destroy()
					return nil, nil, fmt.Errorf("required MCP connector %q needs OAuth sign-in", server.Name)
				}
				continue
			}
		}
		name := mcpConfigName(server)
		config, configErr := mcpConfigValue(server)
		if configErr != nil {
			destroy()
			return nil, nil, configErr
		}
		serverSecrets := make([]runner.EnvironmentSecret, 0, len(server.SecretBindings))
		serverEnvironment := make([]string, 0, len(server.SecretBindings))
		skipServer := false
		for _, binding := range server.SecretBindings {
			if _, duplicate := seenEnvironment[binding.Env]; duplicate {
				destroy()
				return nil, nil, fmt.Errorf("MCP environment variable %q is bound more than once", binding.Env)
			}
			secret, getErr := o.credentials.Get(ctx, credentials.Ref{
				WorkspaceID: workspaceID, Integration: binding.CredentialIntegration, Name: binding.CredentialName,
			})
			if getErr != nil {
				if errors.Is(getErr, credentials.ErrNotFound) || errors.Is(getErr, credentials.ErrUnsupported) {
					for index := range serverSecrets {
						wipeBytes(serverSecrets[index].Value)
					}
					if server.Required {
						destroy()
						return nil, nil, fmt.Errorf("required MCP connector %q needs its saved secret %q", server.Name, binding.Env)
					}
					skipServer = true
					break
				}
				destroy()
				return nil, nil, getErr
			}
			value, valueErr := secret.Bytes()
			secret.Destroy()
			if valueErr != nil {
				destroy()
				return nil, nil, valueErr
			}
			serverSecrets = append(serverSecrets, runner.EnvironmentSecret{Name: binding.Env, Value: value})
			serverEnvironment = append(serverEnvironment, binding.Env)
		}
		if skipServer {
			continue
		}
		for _, environment := range serverEnvironment {
			seenEnvironment[environment] = struct{}{}
		}
		secrets = append(secrets, serverSecrets...)
		args = append(args, "-c", "mcp_servers."+name+"="+config)
	}
	return args, secrets, nil
}

func (o *Operator) codexRun(ctx context.Context, workspaceID string) ([]string, []runner.EnvironmentSecret, error) {
	return o.codexRunWithBrowser(ctx, workspaceID, "", false)
}

// codexRunWithBrowser adds Nabu's standard browser MCP to a run without
// changing the Codex provider or widening its workspace-write sandbox. The
// connector is optional at process startup so a missing local browser never
// discards otherwise useful implementation or research work.
func (o *Operator) codexRunWithBrowser(ctx context.Context, workspaceID, workspacePath string, includeBrowser bool) ([]string, []runner.EnvironmentSecret, error) {
	base := o.codexArgs(ctx)
	if base == nil {
		base = append([]string(nil), runner.DefaultConfig().Args...)
	}
	mcpArgs, secrets, err := o.mcpRuntime(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if includeBrowser {
		if browserArgs, ok := builtInBrowserMCPArgs(workspacePath); ok {
			mcpArgs = append(mcpArgs, browserArgs...)
		}
	}
	// MCP overrides must precede the stdin prompt marker.
	insertAt := len(base)
	if insertAt > 0 && base[insertAt-1] == "-" {
		insertAt--
	}
	args := append([]string(nil), base[:insertAt]...)
	args = append(args, mcpArgs...)
	args = append(args, base[insertAt:]...)
	return args, secrets, nil
}

func builtInBrowserMCPArgs(workspacePath string) ([]string, bool) {
	runtime := discoverBuiltInBrowserRuntime()
	if !runtime.Available || strings.TrimSpace(workspacePath) == "" || !filepath.IsAbs(workspacePath) {
		return nil, false
	}
	outputDir := filepath.Join(filepath.Clean(workspacePath), "media", "browser")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return nil, false
	}
	config := builtInBrowserMCPConfig(runtime.Command, runtime.Executable, outputDir)
	return []string{"-c", "mcp_servers." + builtInBrowserConfigName + "=" + config}, true
}

func builtInBrowserMCPConfig(command, executable, outputDir string) string {
	args := []string{
		"-y", builtInBrowserPackage,
		"--browser", "chrome",
		"--executable-path", executable,
		"--isolated",
		"--viewport-size", "1280x800",
		"--snapshot-boxes",
		"--output-dir", outputDir,
		"--output-max-size", "33554432",
	}
	return "{" + strings.Join([]string{
		"enabled=true",
		"required=false",
		"startup_timeout_sec=60",
		"tool_timeout_sec=120",
		"default_tools_approval_mode=" + tomlString("approve"),
		"command=" + tomlString(command),
		"args=" + tomlStrings(args),
	}, ",") + "}"
}

func discoverBuiltInBrowserRuntime() builtInBrowserRuntime {
	command := firstExecutable("npx", []string{
		"/opt/homebrew/bin/npx",
		"/usr/local/bin/npx",
		filepath.Join(userHomeDir(), ".volta", "bin", "npx"),
	})
	if command == "" {
		matches, _ := filepath.Glob(filepath.Join(userHomeDir(), ".nvm", "versions", "node", "*", "bin", "npx"))
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		command = firstExecutable("", matches)
	}
	if command == "" {
		return builtInBrowserRuntime{Reason: "Node.js npx is not installed or is not discoverable by the Nabu service."}
	}
	browser := firstExecutable("google-chrome", []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
	})
	if browser == "" {
		return builtInBrowserRuntime{Command: command, Reason: "Google Chrome or Chromium is not installed."}
	}
	return builtInBrowserRuntime{Command: command, Executable: browser, Available: true}
}

func firstExecutable(name string, candidates []string) string {
	if name != "" {
		if path, err := exec.LookPath(name); err == nil && filepath.IsAbs(path) {
			return path
		}
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

func userHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func mcpConfigName(server domain.MCPServer) string {
	return "nabu_" + strings.ReplaceAll(server.ID, "-", "_")
}

func mcpConfigValue(server domain.MCPServer) (string, error) {
	fields := []string{
		"enabled=true",
		"required=" + strconv.FormatBool(server.Required),
		"startup_timeout_sec=" + strconv.FormatInt(server.StartupTimeoutSeconds, 10),
		"tool_timeout_sec=" + strconv.FormatInt(server.ToolTimeoutSeconds, 10),
		// Read mode is the conservative default: tools marked read-only run,
		// while write-capable MCP tools remain approval-bound.
		"default_tools_approval_mode=" + tomlString(map[domain.MCPAccess]string{
			domain.MCPAccessRead: "writes", domain.MCPAccessFull: "approve",
		}[server.Access]),
	}
	if server.Transport == domain.MCPTransportStdio {
		fields = append(fields, "command="+tomlString(server.Command), "args="+tomlStrings(server.Args))
		vars := make([]string, 0, len(server.SecretBindings))
		for _, binding := range server.SecretBindings {
			vars = append(vars, binding.Env)
		}
		sort.Strings(vars)
		if len(vars) > 0 {
			fields = append(fields, "env_vars="+tomlStrings(vars))
		}
	} else {
		fields = append(fields, "url="+tomlString(server.URL))
		if server.Auth == domain.MCPAuthOAuth {
			fields = append(fields, "auth="+tomlString("oauth"))
		}
		headers := map[string]string{}
		for _, binding := range server.SecretBindings {
			if binding.Bearer {
				fields = append(fields, "bearer_token_env_var="+tomlString(binding.Env))
			} else {
				headers[binding.Header] = binding.Env
			}
		}
		if len(headers) > 0 {
			fields = append(fields, "env_http_headers="+tomlStringMap(headers))
		}
	}
	if len(server.EnabledTools) > 0 {
		fields = append(fields, "enabled_tools="+tomlStrings(server.EnabledTools))
	}
	if len(server.DisabledTools) > 0 {
		fields = append(fields, "disabled_tools="+tomlStrings(server.DisabledTools))
	}
	return "{" + strings.Join(fields, ",") + "}", nil
}

func tomlString(value string) string {
	return strconv.Quote(value)
}

func tomlStrings(values []string) string {
	items := make([]string, len(values))
	for index, value := range values {
		items[index] = tomlString(value)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func tomlStringMap(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]string, 0, len(keys))
	for _, key := range keys {
		items = append(items, tomlString(key)+"="+tomlString(values[key]))
	}
	return "{" + strings.Join(items, ",") + "}"
}
