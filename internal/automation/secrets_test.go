package automation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestRegisteredScriptReceivesScopedSecretAndPersistsOnlyRedaction(t *testing.T) {
	requireUnix(t)
	ctx := context.Background()
	database, err := store.Open(filepath.Join(t.TempDir(), "nabu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	workspace, err := database.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-one", Name: "One", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	record, err := database.CreateSecretRecord(ctx, domain.SecretRecord{WorkspaceID: workspace.ID, Name: "API token", ReferenceKey: "api-token"})
	if err != nil {
		t.Fatal(err)
	}
	backend := credentials.NewMemory()
	secret, _ := credentials.NewSecret([]byte("workspace-one-secret"))
	if err := backend.Put(ctx, credentials.Ref{WorkspaceID: workspace.ID, Integration: domain.SecretCredentialIntegration, Name: record.ReferenceKey}, secret); err != nil {
		t.Fatal(err)
	}
	secret.Destroy()
	scriptsRoot := t.TempDir()
	path := filepath.Join(scriptsRoot, "bound.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$API_TOKEN\" >&2\nprintf '%s\\n' \"$API_TOKEN\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := database.CreateScript(ctx, domain.Script{
		WorkspaceID: workspace.ID, Name: "bound", Path: filepath.Base(path), Enabled: true,
		CredentialBindings: []domain.ScriptCredentialBinding{{Env: "API_TOKEN", SecretRecordID: record.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{Store: database, Credentials: backend, ScriptsRoot: scriptsRoot, RunsRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.RunScriptNow(ctx, script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Result == nil || strings.Contains(run.Result.Summary, "workspace-one-secret") || !strings.Contains(run.Result.Summary, "[REDACTED]") {
		t.Fatalf("script result leaked secret: %#v", run.Result)
	}
	for _, logPath := range []string{run.StdoutPath, run.StderrPath} {
		content, readErr := os.ReadFile(logPath)
		if readErr != nil || strings.Contains(string(content), "workspace-one-secret") || !strings.Contains(string(content), "[REDACTED]") {
			t.Fatalf("script log leaked secret: %q, %v", content, readErr)
		}
	}
}

func TestScriptSecretLookupUsesScriptWorkspaceExactly(t *testing.T) {
	backend := credentials.NewMemory()
	for workspace, value := range map[string]string{"workspace-one": "one-secret", "workspace-two": "two-secret"} {
		secret, _ := credentials.NewSecret([]byte(value))
		if err := backend.Put(context.Background(), credentials.Ref{WorkspaceID: workspace, Integration: domain.SecretCredentialIntegration, Name: "token"}, secret); err != nil {
			t.Fatal(err)
		}
		secret.Destroy()
	}
	engine := &Engine{credentials: backend}
	loaded, err := engine.loadScriptSecrets(context.Background(), domain.Script{
		WorkspaceID: "workspace-one", Name: "scoped",
		CredentialBindings: []domain.ScriptCredentialBinding{{Env: "API_TOKEN", SecretRecordID: "secret-record-one", CredentialIntegration: domain.SecretCredentialIntegration, CredentialName: "token"}},
	}, "workspace-one")
	if err != nil {
		t.Fatal(err)
	}
	defer destroyEnvironmentSecrets(loaded)
	if len(loaded) != 1 || string(loaded[0].Value) != "one-secret" {
		t.Fatalf("cross-scope credential loaded: %#v", loaded)
	}
	if _, err := engine.loadScriptSecrets(context.Background(), domain.Script{
		WorkspaceID: "workspace-one", Name: "scoped",
		CredentialBindings: []domain.ScriptCredentialBinding{{Env: "API_TOKEN", SecretRecordID: "secret-record-one", CredentialIntegration: domain.SecretCredentialIntegration, CredentialName: "token"}},
	}, "workspace-two"); err == nil || !strings.Contains(err.Error(), "schedule workspace") {
		t.Fatalf("cross-scope schedule lookup error = %v", err)
	}
	if _, err := engine.loadScriptSecrets(context.Background(), domain.Script{
		WorkspaceID: "workspace-one", Name: "scoped",
		CredentialBindings: []domain.ScriptCredentialBinding{{Env: "API_TOKEN", SecretRecordID: "secret-record-one", CredentialIntegration: "provider", CredentialName: "token"}},
	}, "workspace-one"); err == nil || !strings.Contains(err.Error(), "invalid namespace") {
		t.Fatalf("unexpected credential namespace accepted: %v", err)
	}
}
