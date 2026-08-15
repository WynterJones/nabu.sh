package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type secretStubBackend struct {
	*stubBackend
	received []byte
}

func (s *secretStubBackend) Secrets(context.Context) ([]SecretView, error) {
	return []SecretView{{ID: "secret-1", Name: "analytics_token", Configured: true}}, nil
}
func (s *secretStubBackend) Secret(context.Context, string) (SecretView, error) {
	return SecretView{ID: "secret-1", Name: "analytics_token", Configured: true}, nil
}
func (s *secretStubBackend) CreateSecret(_ context.Context, input SecretCreate, value []byte) (SecretView, error) {
	s.received = value
	return SecretView{ID: "secret-1", Name: input.Name, Configured: true}, nil
}
func (s *secretStubBackend) UpdateSecret(_ context.Context, id string, _ SecretUpdate, value []byte) (SecretView, error) {
	s.received = value
	return SecretView{ID: id, Name: "analytics_token", Configured: true}, nil
}
func (s *secretStubBackend) DeleteSecret(context.Context, string) error { return nil }

func TestSecretEndpointNeverReturnsOrRetainsValue(t *testing.T) {
	backend := &secretStubBackend{stubBackend: &stubBackend{}}
	handler := New(backend, testAssets(), nil).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/secrets", strings.NewReader(`{"name":"analytics_token","value":"provider-secret"}`))
	request.Host = "127.0.0.1:7777"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"configured":true`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "provider-secret") {
		t.Fatalf("response exposed secret: %s", response.Body.String())
	}
	for _, value := range backend.received {
		if value != 0 {
			t.Fatalf("request buffer retained a secret: %q", backend.received)
		}
	}
}
