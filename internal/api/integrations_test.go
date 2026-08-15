package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/integrations"
)

func TestIntegrationCredentialEndpointDoesNotReturnOrRetainSecretInput(t *testing.T) {
	backend := &integrationStubBackend{stubBackend: &stubBackend{}}
	handler := New(backend, testAssets(), nil).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/integrations/registry-id/credentials", strings.NewReader(`{"credentials":{"token":"provider-secret"}}`))
	request.Host = "127.0.0.1:7777"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("credential response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "provider-secret") {
		t.Fatalf("credential response exposed secret: %s", response.Body.String())
	}
	for _, value := range backend.received {
		if value != 0 {
			t.Fatalf("credential request buffer was retained after handling: %q", backend.received)
		}
	}
}

func TestIntegrationRoutesReturnComputedViews(t *testing.T) {
	backend := &integrationStubBackend{stubBackend: &stubBackend{}}
	handler := New(backend, testAssets(), nil).Handler()
	for _, path := range []string{"/api/integrations", "/api/integrations/registry-id", "/api/integrations/registry-id/verify"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/verify") {
			method = http.MethodPost
		}
		request := httptest.NewRequest(method, path, nil)
		request.Host = "127.0.0.1:7777"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"configured":true`) {
			t.Fatalf("%s %s response = %d %s", method, path, response.Code, response.Body.String())
		}
	}
}

func TestGeneratedIntegrationRouteAcceptsOnlyValidatedDeclarativeManifest(t *testing.T) {
	backend := &integrationStubBackend{stubBackend: &stubBackend{}}
	handler := New(backend, testAssets(), nil).Handler()
	body := `{"id":"analytics","name":"Analytics","base_url":"https://api.example.com","allowed_hosts":["api.example.com"],"fields":[{"name":"api_key","label":"API key","secret":true,"required":true}],"auth":{"type":"bearer","credential":"api_key"},"operations":[{"id":"read","name":"Read","method":"GET","path":"/v1","kind":"read"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/integrations", strings.NewReader(body))
	request.Host = "127.0.0.1:7777"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || backend.created.ID != "analytics" || backend.created.Fields[0].Name != "api_key" {
		t.Fatalf("response=%d %s created=%#v", response.Code, response.Body.String(), backend.created)
	}
}

type integrationStubBackend struct {
	*stubBackend
	received []byte
	created  integrations.Manifest
}

func (s *integrationStubBackend) Integrations(context.Context) ([]IntegrationView, error) {
	return []IntegrationView{integrationStubView()}, nil
}

func (s *integrationStubBackend) Integration(context.Context, string) (IntegrationView, error) {
	return integrationStubView(), nil
}

func (s *integrationStubBackend) SaveIntegrationCredentials(_ context.Context, _ string, values map[string][]byte) (IntegrationView, error) {
	s.received = values["token"]
	return integrationStubView(), nil
}

func (s *integrationStubBackend) DeleteIntegrationCredential(context.Context, string, string) (IntegrationView, error) {
	return integrationStubView(), nil
}

func (s *integrationStubBackend) VerifyIntegration(context.Context, string) (IntegrationView, error) {
	return integrationStubView(), nil
}

func (s *integrationStubBackend) CreateGeneratedIntegration(_ context.Context, manifest integrations.Manifest) (IntegrationView, error) {
	s.created = manifest
	return integrationStubView(), nil
}

func integrationStubView() IntegrationView {
	return IntegrationView{
		ID: "registry-id", Name: "Provider", Status: domain.IntegrationReady, Available: true, Configured: true,
		Capabilities: []IntegrationCapabilityView{}, RequiredCredentials: []IntegrationCredentialView{}, ConfiguredCredentials: []string{"token"},
	}
}
