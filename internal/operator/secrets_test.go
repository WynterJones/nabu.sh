package operator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/steering"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestSecretLifecycleAndScriptBindingStayMetadataOnly(t *testing.T) {
	ctx := context.Background()
	service, database, paths, workspace := testOperator(t, fakeExecutor{})
	backend := credentials.NewMemory()
	if err := service.ConfigureIntegrations(backend, nil, nil); err != nil {
		t.Fatal(err)
	}
	value := []byte("provider-secret")
	created, err := service.CreateSecret(ctx, api.SecretCreate{Name: "analytics_token", Label: "Analytics token"}, value)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Configured || strings.Contains(strings.ToLower(created.Description), "provider-secret") {
		t.Fatalf("secret view = %#v", created)
	}
	for _, item := range value {
		if item != 0 {
			t.Fatalf("caller secret buffer was not destroyed: %q", value)
		}
	}
	record, err := database.GetSecretRecordForWorkspace(ctx, workspace.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := backend.Get(ctx, secretRef(record))
	if err != nil {
		t.Fatal(err)
	}
	storedValue, err := stored.Bytes()
	stored.Destroy()
	if err != nil || string(storedValue) != "provider-secret" {
		t.Fatalf("vault value unavailable: %q %v", storedValue, err)
	}
	wipeBytes(storedValue)

	scriptPath := filepath.Join(paths.Scripts, "analytics-summary.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '%s\\n' '{\"status\":\"completed\",\"summary\":\"ok\",\"interesting\":false}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	name, path, access := "Analytics summary", "analytics-summary.sh", domain.ScriptAccessRead
	bindings := []domain.ScriptCredentialBinding{{Env: "ANALYTICS_TOKEN", SecretRecordID: created.ID}}
	script, err := service.CreateScript(ctx, api.ScriptInput{Name: &name, Path: &path, Access: &access, SecretBindings: &bindings})
	if err != nil {
		t.Fatal(err)
	}
	if len(script.CredentialBindings) != 1 || script.CredentialBindings[0].CredentialName == "" {
		t.Fatalf("script binding = %#v", script.CredentialBindings)
	}
	encoded := string(mustMarshalJSON(t, script))
	if strings.Contains(encoded, "credential_name") || strings.Contains(encoded, "credential_integration") {
		t.Fatalf("runtime credential reference exposed through JSON: %s", encoded)
	}
}

func TestChatCreatesManagedScriptWithoutEmbeddingSecret(t *testing.T) {
	ctx := context.Background()
	service, database, paths, workspace := testOperator(t, fakeExecutor{})
	backend := credentials.NewMemory()
	if err := service.ConfigureIntegrations(backend, nil, nil); err != nil {
		t.Fatal(err)
	}
	secret, err := service.CreateSecret(ctx, api.SecretCreate{Name: "provider_token"}, []byte("vault-only"))
	if err != nil {
		t.Fatal(err)
	}
	effect := steering.Effect{Type: steering.EffectCreateScript, Script: &steering.ScriptChange{
		Name: "Provider summary", Path: "provider-summary.sh", Content: "#!/bin/sh\nprintf '%s\\n' '{\"status\":\"completed\",\"summary\":\"ok\",\"interesting\":false}'",
		Access: "read", TimeoutSeconds: 60, SecretBindings: []steering.ScriptSecretBinding{{SecretID: secret.ID, EnvVar: "PROVIDER_TOKEN"}},
	}}
	view, err := service.applyChatEffect(ctx, effect, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity == nil || view.Entity.Type != "script" {
		t.Fatalf("chat view = %#v", view)
	}
	retried, err := service.applyChatEffect(ctx, effect, workspace)
	if err != nil || retried.Entity == nil || retried.Entity.ID != view.Entity.ID {
		t.Fatalf("exact script retry was not idempotent: first=%#v retry=%#v err=%v", view, retried, err)
	}
	content, err := os.ReadFile(filepath.Join(paths.Scripts, "provider-summary.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "vault-only") {
		t.Fatal("managed script embedded the secret value")
	}
	scripts, err := database.ListScripts(ctx, store.ScriptFilter{WorkspaceID: workspace.ID})
	if err != nil || len(scripts) == 0 || len(scripts[len(scripts)-1].CredentialBindings) != 1 {
		t.Fatalf("scripts=%#v err=%v", scripts, err)
	}
}

func TestChatPersistsCreateScriptEffectAfterApplyingIt(t *testing.T) {
	ctx := context.Background()
	service, database, paths, workspace := testOperator(t, fakeExecutor{})
	backend := credentials.NewMemory()
	if err := service.ConfigureIntegrations(backend, nil, nil); err != nil {
		t.Fatal(err)
	}
	secret, err := service.CreateSecret(ctx, api.SecretCreate{Name: "analytics_token", Label: "Analytics token"}, []byte("vault-only"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(steering.Result{
		AssistantResponse: "I created a reusable analytics script with the saved key bound as ANALYTICS_TOKEN.",
		Effects: []steering.Effect{{Type: steering.EffectCreateScript, Script: &steering.ScriptChange{
			Name: "Analytics summary", Path: "analytics-summary.sh", Description: "Reads a bounded analytics summary.",
			Content: "#!/bin/sh\nprintf '%s\\n' '{\"status\":\"completed\",\"summary\":\"ok\",\"interesting\":false}'\n",
			Access:  "read", TimeoutSeconds: 60, SecretBindings: []steering.ScriptSecretBinding{{SecretID: secret.ID, EnvVar: "ANALYTICS_TOKEN"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.runner = fakeExecutor{result: string(result)}
	service.Start(ctx)
	if _, err := service.SendChat(ctx, api.ChatSend{Content: "Use my saved analytics key to create the reusable read script."}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, service)
	messages, err := database.ListMessages(ctx, store.MessageFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Status != domain.MessageComplete || !strings.Contains(messages[1].Content, "reusable analytics script") {
		t.Fatalf("chat messages = %#v", messages)
	}
	if _, err := os.Stat(filepath.Join(paths.Scripts, "analytics-summary.sh")); err != nil {
		t.Fatalf("managed script was not created: %v", err)
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
