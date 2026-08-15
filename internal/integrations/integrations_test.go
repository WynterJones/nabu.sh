package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nabu-sh/nabu/internal/credentials"
)

func TestParseManifestStrictValidation(t *testing.T) {
	valid := `{
  "id":"analytics","name":"Analytics","base_url":"https://api.example.com",
  "allowed_hosts":["api.example.com"],
  "auth":{"type":"bearer","credential":"token"},
  "operations":[
    {"id":"summary","name":"Summary","method":"GET","path":"/v1/summary","kind":"read"},
    {"id":"publish","name":"Publish","method":"POST","path":"/v1/publish","kind":"write","request_template":{"enabled":true}}
  ]
}`
	manifest, err := ParseManifest([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "analytics" || len(manifest.Operations) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}

	invalid := []string{
		strings.Replace(valid, `"base_url":"https://api.example.com"`, `"base_url":"http://api.example.com"`, 1),
		strings.Replace(valid, `"allowed_hosts":["api.example.com"]`, `"allowed_hosts":["*.example.com"]`, 1),
		strings.Replace(valid, `"path":"/v1/summary"`, `"path":"/../admin"`, 1),
		strings.Replace(valid, `"method":"GET","path":"/v1/summary","kind":"read"`, `"method":"POST","path":"/v1/summary","kind":"read"`, 1),
		strings.Replace(valid, `"type":"bearer"`, `"type":"oauth2"`, 1),
		strings.Replace(valid, `"id":"analytics"`, `"id":"analytics","command":"sh -c evil"`, 1),
		valid + `{}`,
	}
	for index, raw := range invalid {
		if _, err := ParseManifest([]byte(raw)); err == nil {
			t.Errorf("invalid manifest %d accepted", index)
		}
	}
}

func TestBearerExecutionUsesScopedCredentialAndRedactsResponse(t *testing.T) {
	backend := credentials.NewMemory()
	secret, _ := credentials.NewSecret([]byte("provider-token"))
	defer secret.Destroy()
	ref := credentials.Ref{WorkspaceID: "workspace-one", Integration: "registry-record-123", Name: "token"}
	if err := backend.Put(context.Background(), ref, secret); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/v1/check" || request.Method != http.MethodGet {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer provider-token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"echo":"provider-token","ok":true}`)
	}))
	defer server.Close()
	manifest := localManifest(t, server.URL, Auth{Type: AuthBearer, Credential: "token"}, Operation{
		ID: "check", Name: "Check", Method: "GET", Path: "/v1/check", Kind: OperationRead,
	})
	service, err := NewService(backend, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), manifest, ExecuteRequest{
		WorkspaceID: "workspace-one", IntegrationID: "registry-record-123", OperationID: "check",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusOK || strings.Contains(string(result.Body), "provider-token") || !strings.Contains(string(result.Body), "[REDACTED]") {
		t.Fatalf("result = %#v", result)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestGeneratedFieldsRenderIntoBoundedPathAndQuery(t *testing.T) {
	backend := credentials.NewMemory()
	for name, value := range map[string]string{"api_key": "secret-key", "site_id": "example.com", "project": "project alpha"} {
		secret, _ := credentials.NewSecret([]byte(value))
		if err := backend.Put(context.Background(), credentials.Ref{WorkspaceID: "workspace", Integration: "adapter", Name: name}, secret); err != nil {
			t.Fatal(err)
		}
		secret.Destroy()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/v1/projects/project%20alpha/stats" || request.URL.Query().Get("site_id") != "example.com" {
			t.Errorf("request URL = %s", request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer secret-key" {
			t.Errorf("authorization header was not applied")
		}
		_, _ = io.WriteString(w, `{"site":"example.com","ok":true}`)
	}))
	defer server.Close()
	base := localManifest(t, server.URL, Auth{Type: AuthBearer, Credential: "api_key"}, Operation{
		ID: "read", Name: "Read stats", Method: "GET", Path: "/v1", Kind: OperationRead,
	})
	manifest := base
	manifest.Fields = []Field{
		{Name: "api_key", Label: "API key", Secret: true, Required: true},
		{Name: "site_id", Label: "Site domain", Required: true},
		{Name: "project", Label: "Project", Required: true},
	}
	manifest.Operations = []Operation{{
		ID: "read", Name: "Read stats", Method: "GET", Path: "/v1/projects/{{project}}/stats", Kind: OperationRead,
		Query: map[string]string{"site_id": "{{site_id}}"},
	}}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(backend, server.Client())
	result, err := service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", IntegrationID: "adapter", OperationID: "read"})
	if err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestGeneratedAPIKeyFieldSupportsCustomHeaderAndPOSTQueryBody(t *testing.T) {
	backend := credentials.NewMemory()
	for name, value := range map[string]string{"api_key": "secret-key", "site_id": "example.com"} {
		secret, _ := credentials.NewSecret([]byte(value))
		if err := backend.Put(context.Background(), credentials.Ref{WorkspaceID: "workspace", Integration: "adapter", Name: name}, secret); err != nil {
			t.Fatal(err)
		}
		secret.Destroy()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if request.Method != http.MethodPost || request.Header.Get("X-API-Key") != "secret-key" || string(body) != `{"metrics":["visitors"],"site_id":"example.com"}` {
			t.Errorf("request method=%s header=%q body=%s", request.Method, request.Header.Get("X-API-Key"), body)
		}
		_, _ = io.WriteString(w, `{"echo":"secret-key","ok":true}`)
	}))
	defer server.Close()
	host := strings.Split(strings.TrimPrefix(server.URL, "http://"), ":")[0]
	manifest := Manifest{
		ID: "provider", Name: "Provider", BaseURL: server.URL, AllowedHosts: []string{host}, AllowLocalhost: true,
		Fields: []Field{{Name: "api_key", Label: "API key", Secret: true, Required: true}, {Name: "site_id", Label: "Site domain", Required: true}},
		Operations: []Operation{{
			ID: "query", Name: "Query stats", Method: "POST", Path: "/api/query", Kind: OperationRead,
			Headers: map[string]string{"X-API-Key": "{{api_key}}"}, RequestTemplate: json.RawMessage(`{"site_id":"{{site_id}}","metrics":["visitors"]}`),
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(backend, server.Client())
	result, err := service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", IntegrationID: "adapter", OperationID: "query"})
	if err != nil || strings.Contains(string(result.Body), "secret-key") || !strings.Contains(string(result.Body), "[REDACTED]") {
		t.Fatalf("result=%s err=%v", result.Body, err)
	}
}

func TestGeneratedManifestSupportsSecretBindingsButRejectsPathLeaksAndUnknownFields(t *testing.T) {
	base := Manifest{
		ID: "provider", Name: "Provider", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Auth:       Auth{Type: AuthBearer, Credential: "api_key"},
		Fields:     []Field{{Name: "api_key", Label: "API key", Secret: true, Required: true}},
		Operations: []Operation{{ID: "read", Name: "Read", Method: "GET", Path: "/v1", Kind: OperationRead}},
	}
	for _, operation := range []Operation{
		{ID: "secret-path", Name: "Secret in path", Method: "GET", Path: "/v1/{{api_key}}", Kind: OperationRead},
		{ID: "unknown", Name: "Unknown field", Method: "GET", Path: "/v1/{{account_id}}", Kind: OperationRead},
	} {
		manifest := base
		manifest.Operations = []Operation{operation}
		if err := manifest.Validate(); err == nil {
			t.Fatalf("unsafe operation accepted: %#v", operation)
		}
	}
	allowed := base
	allowed.Operations = []Operation{{ID: "query", Name: "API key query", Method: "GET", Path: "/v1", Kind: OperationRead, Query: map[string]string{"key": "{{api_key}}"}}}
	if err := allowed.Validate(); err != nil {
		t.Fatalf("bounded secret query binding rejected: %v", err)
	}
}

func TestExecutionRequiresCredentialRegistryID(t *testing.T) {
	manifest := Manifest{
		ID: "provider", Name: "Provider", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Operations: []Operation{{ID: "read", Name: "Read", Method: "GET", Path: "/v1", Kind: OperationRead}},
	}
	service, err := NewService(credentials.NewMemory(), panicDoer{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", OperationID: "read"})
	if err == nil || !strings.Contains(err.Error(), "registry ID") {
		t.Fatalf("missing integration registry ID error = %v", err)
	}
}

func TestWriteRequiresApprovalAndUsesFixedJSONTemplate(t *testing.T) {
	backend := credentials.NewMemory()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(request.Body)
		if string(body) != `{"enabled":true}` || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("body=%q content-type=%q", body, request.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	manifest := localManifest(t, server.URL, Auth{}, Operation{
		ID: "enable", Name: "Enable", Method: "POST", Path: "/v1/enable", Kind: OperationWrite,
		RequestTemplate: json.RawMessage(`{"enabled":true}`),
	})
	service, _ := NewService(backend, server.Client())
	_, err := service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", IntegrationID: "provider", OperationID: "enable"})
	if !errors.Is(err, ErrApprovalRequired) || requests.Load() != 0 {
		t.Fatalf("unapproved write err=%v requests=%d", err, requests.Load())
	}
	result, err := service.Execute(context.Background(), manifest, ExecuteRequest{
		WorkspaceID: "workspace", IntegrationID: "provider", OperationID: "enable", Approved: true,
	})
	if err != nil || result.StatusCode != http.StatusNoContent || requests.Load() != 1 {
		t.Fatalf("approved write result=%#v err=%v requests=%d", result, err, requests.Load())
	}
}

func TestVerifyReadOnlyRejectsWriteOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	manifest := localManifest(t, server.URL, Auth{}, Operation{
		ID: "mutate", Name: "Mutate", Method: "DELETE", Path: "/v1/value", Kind: OperationWrite,
	})
	service, _ := NewService(credentials.NewMemory(), server.Client())
	_, err := service.VerifyReadOnly(context.Background(), manifest, "workspace", "provider", "mutate")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("VerifyReadOnly error = %v", err)
	}
}

func TestBasicAndHeaderAuthAreBounded(t *testing.T) {
	for _, test := range []struct {
		name       string
		auth       Auth
		secrets    map[string]string
		wantHeader string
		wantValue  string
	}{
		{name: "header", auth: Auth{Type: AuthHeader, Header: "X-API-Key", Credential: "api-key"}, secrets: map[string]string{"api-key": "header-secret"}, wantHeader: "X-API-Key", wantValue: "header-secret"},
		{name: "basic", auth: Auth{Type: AuthBasic, UsernameCredential: "username", PasswordCredential: "password"}, secrets: map[string]string{"username": "owner", "password": "password-secret"}, wantHeader: "Authorization", wantValue: "Basic b3duZXI6cGFzc3dvcmQtc2VjcmV0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := credentials.NewMemory()
			for name, value := range test.secrets {
				secret, _ := credentials.NewSecret([]byte(value))
				if err := backend.Put(context.Background(), credentials.Ref{WorkspaceID: "workspace", Integration: "provider", Name: name}, secret); err != nil {
					t.Fatal(err)
				}
				secret.Destroy()
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if got := request.Header.Get(test.wantHeader); got != test.wantValue {
					t.Errorf("header = %q", got)
				}
				fmt.Fprint(w, test.wantValue)
			}))
			defer server.Close()
			manifest := localManifest(t, server.URL, test.auth, Operation{ID: "read", Name: "Read", Method: "GET", Path: "/v1", Kind: OperationRead})
			service, _ := NewService(backend, server.Client())
			result, err := service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", IntegrationID: "provider", OperationID: "read"})
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range test.secrets {
				if strings.Contains(string(result.Body), secret) {
					t.Fatalf("response leaked secret: %q", result.Body)
				}
			}
		})
	}
}

func TestTransportErrorsNeverIncludeProviderErrorOrSecret(t *testing.T) {
	backend := credentials.NewMemory()
	secret, _ := credentials.NewSecret([]byte("provider-token"))
	defer secret.Destroy()
	_ = backend.Put(context.Background(), credentials.Ref{WorkspaceID: "workspace", Integration: "provider", Name: "token"}, secret)
	service, _ := NewService(backend, failingDoer{})
	manifest := Manifest{
		ID: "provider", Name: "Provider", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Auth:       Auth{Type: AuthBearer, Credential: "token"},
		Operations: []Operation{{ID: "read", Name: "Read", Method: "GET", Path: "/v1", Kind: OperationRead}},
	}
	_, err := service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", IntegrationID: "provider", OperationID: "read"})
	if err == nil || strings.Contains(err.Error(), "provider-token") || strings.Contains(err.Error(), "transport-internal") {
		t.Fatalf("transport error = %v", err)
	}
}

func TestDefaultClientRestrictsAndExecutesExplicitLocalhost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	manifest := localManifest(t, server.URL, Auth{}, Operation{ID: "read", Name: "Read", Method: "GET", Path: "/", Kind: OperationRead})
	service, _ := NewService(credentials.NewMemory(), nil)
	result, err := service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", IntegrationID: "provider", OperationID: "read"})
	if err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("restricted localhost result=%#v err=%v", result, err)
	}
}

func TestCredentialControlCharactersAreRejectedBeforeNetwork(t *testing.T) {
	backend := credentials.NewMemory()
	secret, _ := credentials.NewSecret([]byte("value\r\nX-Injected: true"))
	defer secret.Destroy()
	_ = backend.Put(context.Background(), credentials.Ref{WorkspaceID: "workspace", Integration: "provider", Name: "token"}, secret)
	service, _ := NewService(backend, panicDoer{})
	manifest := Manifest{
		ID: "provider", Name: "Provider", BaseURL: "https://api.example.com", AllowedHosts: []string{"api.example.com"},
		Auth:       Auth{Type: AuthBearer, Credential: "token"},
		Operations: []Operation{{ID: "read", Name: "Read", Method: "GET", Path: "/v1", Kind: OperationRead}},
	}
	if _, err := service.Execute(context.Background(), manifest, ExecuteRequest{WorkspaceID: "workspace", IntegrationID: "provider", OperationID: "read"}); err == nil || !strings.Contains(err.Error(), "HTTP header") {
		t.Fatalf("control-character credential error = %v", err)
	}
}

func localManifest(t *testing.T, serverURL string, auth Auth, operation Operation) Manifest {
	t.Helper()
	host := strings.TrimPrefix(serverURL, "http://")
	host = strings.Split(host, ":")[0]
	manifest := Manifest{
		ID: "provider", Name: "Provider", BaseURL: serverURL, AllowedHosts: []string{host},
		AllowLocalhost: true, Auth: auth, Operations: []Operation{operation},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	return manifest
}

type failingDoer struct{}

func (failingDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport-internal provider-token")
}

type panicDoer struct{}

func (panicDoer) Do(*http.Request) (*http.Response, error) {
	panic("network must not be reached")
}
