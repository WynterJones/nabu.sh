package operator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/eventbus"
	"github.com/nabu-sh/nabu/internal/integrations"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestIntegrationLifecycleCredentialsVerifyAndIngestion(t *testing.T) {
	ctx := context.Background()
	backend := credentials.NewMemory()
	database, operator := integrationTestOperator(t, backend, nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"records":[{"value":1}]}`)
	}))
	defer server.Close()
	client := server.Client()
	sink := &recordingSink{}
	if err := operator.ConfigureIntegrations(backend, client, sink); err != nil {
		t.Fatal(err)
	}
	manifest := integrationManifest(t, server.URL, integrations.Auth{Type: integrations.AuthBearer, Credential: "token"}, integrations.Operation{
		ID: "read", Name: "Read data", Method: "GET", Path: "/", Kind: integrations.OperationRead,
	})
	created, err := operator.CreateGeneratedIntegration(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if created.Configured || len(created.RequiredCredentials) != 1 || created.RequiredCredentials[0].Configured {
		t.Fatalf("new integration view = %#v", created)
	}
	values := map[string][]byte{"token": []byte("secret-token")}
	configured, err := operator.SaveIntegrationCredentials(ctx, created.ID, values)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 || !configured.Configured || configured.RequiredCredentials[0].Configured != true {
		t.Fatalf("configured integration = %#v values=%#v", configured, values)
	}
	verified, err := operator.VerifyIntegration(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != domain.IntegrationReady || verified.VerifiedAt == nil || sink.calls != 1 || strings.Contains(sink.body, "secret-token") {
		t.Fatalf("verified=%#v sink=%#v", verified, sink)
	}
	stored, err := database.GetIntegration(ctx, created.ID)
	if err != nil || stored.Status != domain.IntegrationReady || stored.LastVerifiedAt == nil || strings.Contains(string(stored.Manifest), "secret-token") {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	events, err := database.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
		if strings.Contains(string(event.Data), "secret-token") {
			t.Fatalf("secret leaked into event: %#v", event)
		}
	}
	if !containsString(types, "integration.created") || !containsString(types, "integration.credentials_updated") || !containsString(types, "integration.ready") {
		t.Fatalf("events = %v", types)
	}
	deleted, err := operator.DeleteIntegrationCredential(ctx, created.ID, "token")
	if err != nil || deleted.Status != domain.IntegrationNeedsCredentials || deleted.Configured {
		t.Fatalf("deleted=%#v err=%v", deleted, err)
	}
}

func TestGeneratedIntegrationPublishesDynamicConnectionFieldSchema(t *testing.T) {
	ctx := context.Background()
	_, operator := integrationTestOperator(t, credentials.NewMemory(), noopDoer{}, nil)
	manifest := integrations.Manifest{
		ID: "analytics", Name: "Analytics", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Auth: integrations.Auth{Type: integrations.AuthBearer, Credential: "api_key"},
		Fields: []integrations.Field{
			{Name: "api_key", Label: "Stats API key", Description: "Create this in the provider account.", Secret: true, Required: true},
			{Name: "site_id", Label: "Site domain", Description: "The domain configured at the provider.", Required: true},
		},
		Operations: []integrations.Operation{{ID: "read", Name: "Read stats", Method: "GET", Path: "/v1/stats", Query: map[string]string{"site_id": "{{site_id}}"}, Kind: integrations.OperationRead}},
	}
	created, err := operator.CreateGeneratedIntegration(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.RequiredCredentials) != 2 || !created.RequiredCredentials[0].Secret || created.RequiredCredentials[1].Secret || created.RequiredCredentials[1].Label != "Site domain" {
		t.Fatalf("connection fields = %#v", created.RequiredCredentials)
	}
	again, err := operator.CreateGeneratedIntegration(ctx, manifest)
	if err != nil || again.ID != created.ID {
		t.Fatalf("duplicate adapter was not idempotent: first=%s again=%#v err=%v", created.ID, again, err)
	}
}

func TestTaskDoesNotInvokeLegacyClosedIntegration(t *testing.T) {
	ctx := context.Background()
	executor := &integrationPromptExecutor{}
	operator, database, _, workspace := testOperator(t, executor)
	backend := credentials.NewMemory()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer vault-only-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"results":{"visitors":42,"pageviews":85}}`)
	}))
	defer server.Close()
	if err := operator.ConfigureIntegrations(backend, server.Client(), nil); err != nil {
		t.Fatal(err)
	}
	manifest := integrationManifest(t, server.URL, integrations.Auth{Type: integrations.AuthBearer, Credential: "api_key"}, integrations.Operation{
		ID: "traffic-summary", Name: "Read traffic summary", Method: "GET", Path: "/", Kind: integrations.OperationRead,
	})
	manifest.ID, manifest.Name = "plausible-stats", "Plausible Analytics"
	created, err := operator.CreateGeneratedIntegration(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.SaveIntegrationCredentials(ctx, created.ID, map[string][]byte{"api_key": []byte("vault-only-token")}); err != nil {
		t.Fatal(err)
	}
	if ready, err := operator.VerifyIntegration(ctx, created.ID); err != nil || ready.Status != domain.IntegrationReady {
		t.Fatalf("VerifyIntegration() = %#v, %v", ready, err)
	}
	task, err := database.CreateTask(ctx, domain.Task{
		Title: "Prepare Plausible traffic report", Purpose: "Use Plausible Analytics for the latest traffic summary",
		Why: "Traffic is the mission metric", Status: domain.TaskReady, Priority: domain.PriorityHigh,
		WorkspaceID: workspace.ID, DefinitionOfDone: []domain.DefinitionItem{{Text: "Report visitor and pageview values"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.runTask(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(executor.prompt, "Configured Integration Data") || strings.Contains(executor.prompt, `"visitors": 42`) {
		t.Fatalf("task prompt still included a closed integration result: %s", executor.prompt)
	}
	if requests != 1 {
		t.Fatalf("legacy integration was invoked during task execution; requests = %d", requests)
	}
	if strings.Contains(executor.prompt, "vault-only-token") {
		t.Fatalf("task prompt exposed credential: %s", executor.prompt)
	}
}

type integrationPromptExecutor struct{ prompt string }

func (executor *integrationPromptExecutor) Run(ctx context.Context, request runner.Request) (runner.ExecutionResult, error) {
	executor.prompt = request.Prompt
	return fakeExecutor{result: `{"status":"completed","summary":"Reported traffic.","files_changed":[],"verification":[{"name":"provider response","status":"passed","details":"Visitors and pageviews were present."}],"artifacts":[],"uncertainties":[],"approval_needed":null}`}.Run(ctx, request)
}

func TestIntegrationCredentialStorageFailureIsActionableAndSafe(t *testing.T) {
	ctx := context.Background()
	backend := failingCredentialBackend{cause: errors.New("private backend diagnostics")}
	_, operator := integrationTestOperator(t, backend, noopDoer{}, nil)
	created, err := operator.CreateGeneratedIntegration(ctx, integrations.Manifest{
		ID: "analytics", Name: "Analytics", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Auth:       integrations.Auth{Type: integrations.AuthBearer, Credential: "token"},
		Operations: []integrations.Operation{{ID: "read", Name: "Read", Method: "GET", Path: "/", Kind: integrations.OperationRead}},
	})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string][]byte{"token": []byte("never-return-this")}
	_, err = operator.SaveIntegrationCredentials(ctx, created.ID, values)
	if !errors.Is(err, api.ErrUnavailable) || !strings.Contains(err.Error(), "system credential vault") {
		t.Fatalf("credential storage error = %v", err)
	}
	if strings.Contains(err.Error(), "private backend diagnostics") || strings.Contains(err.Error(), "never-return-this") {
		t.Fatalf("credential storage error exposed private detail: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("credential values were retained: %#v", values)
	}
}

func TestIntegrationCrossWorkspaceIdentifiersAreNotVisible(t *testing.T) {
	ctx := context.Background()
	backend := credentials.NewMemory()
	database, operator := integrationTestOperator(t, backend, noopDoer{}, nil)
	manifest := integrations.Manifest{
		ID: "public", Name: "Public", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Operations: []integrations.Operation{{ID: "read", Name: "Read", Method: "GET", Path: "/", Kind: integrations.OperationRead}},
	}
	created, err := operator.CreateGeneratedIntegration(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateWorkspace(ctx, domain.Workspace{ID: "other-workspace", Name: "Other", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetActiveWorkspace(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Integration(ctx, created.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("cross-workspace Integration error = %v", err)
	}
	if _, err := operator.SaveIntegrationCredentials(ctx, created.ID, map[string][]byte{"token": []byte("hidden")}); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("cross-workspace credential error = %v", err)
	}
}

func TestWriteOnlyIntegrationCannotBeVerification(t *testing.T) {
	ctx := context.Background()
	backend := credentials.NewMemory()
	_, operator := integrationTestOperator(t, backend, panicDoer{}, nil)
	manifest := integrations.Manifest{
		ID: "writer", Name: "Writer", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Operations: []integrations.Operation{{ID: "write", Name: "Write", Method: "POST", Path: "/", Kind: integrations.OperationWrite, RequestTemplate: json.RawMessage(`{"enabled":true}`)}},
	}
	created, err := operator.CreateGeneratedIntegration(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.VerifyIntegration(ctx, created.ID); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("write verification error = %v", err)
	}
}

func TestStoredManifestIsRevalidatedOnEveryAccess(t *testing.T) {
	ctx := context.Background()
	backend := credentials.NewMemory()
	database, operator := integrationTestOperator(t, backend, nil, nil)
	created, err := database.CreateIntegration(ctx, domain.Integration{
		Name: "Unsafe", Provider: "unsafe", Status: domain.IntegrationDraft,
		Manifest: json.RawMessage(`{"base_url":"http://169.254.169.254","command":"curl metadata"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Integration(ctx, created.ID); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid stored manifest access error = %v", err)
	}
}

func integrationTestOperator(t *testing.T, backend credentials.Backend, client integrations.HTTPDoer, sink IntegrationResponseSink) (*store.Store, *Operator) {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(filepath.Join(root, "nabu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	workspace, err := database.CreateWorkspace(context.Background(), domain.Workspace{ID: "workspace", Name: "Workspace", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := config.Ensure(filepath.Join(root, "home"))
	if err != nil {
		t.Fatal(err)
	}
	operator := NewWithIntegrations(database, nil, paths, eventbus.New(), nil, backend, client, sink)
	if active, err := database.ActiveWorkspace(context.Background()); err != nil || active.ID != workspace.ID {
		t.Fatalf("active workspace = %#v, %v", active, err)
	}
	return database, operator
}

func integrationManifest(t *testing.T, serverURL string, auth integrations.Auth, operation integrations.Operation) integrations.Manifest {
	t.Helper()
	host := strings.Split(strings.TrimPrefix(serverURL, "http://"), ":")[0]
	manifest := integrations.Manifest{
		ID: "provider", Name: "Provider", BaseURL: serverURL, AllowedHosts: []string{host}, AllowLocalhost: true,
		Auth: auth, Operations: []integrations.Operation{operation},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	return manifest
}

type recordingSink struct {
	calls int
	body  string
}

func (s *recordingSink) IngestIntegrationResponse(_ context.Context, _ string, _ domain.Integration, _ integrations.Operation, result integrations.Result) error {
	s.calls++
	s.body = string(result.Body)
	return nil
}

type noopDoer struct{}

func (noopDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

type panicDoer struct{}

func (panicDoer) Do(*http.Request) (*http.Response, error) { panic("HTTP must not be reached") }

type failingCredentialBackend struct{ cause error }

func (f failingCredentialBackend) Put(context.Context, credentials.Ref, *credentials.Secret) error {
	return f.cause
}
func (failingCredentialBackend) Get(context.Context, credentials.Ref) (*credentials.Secret, error) {
	return nil, credentials.ErrNotFound
}
func (failingCredentialBackend) Delete(context.Context, credentials.Ref) error {
	return credentials.ErrNotFound
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
