package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

type mcpStubBackend struct {
	*stubBackend
	received MCPServerInput
	deleted  string
}

func (s *mcpStubBackend) MCPServers(context.Context) ([]domain.MCPServer, error) {
	return []domain.MCPServer{{ID: "mcp-1", Name: "Research", Transport: domain.MCPTransportHTTP, URL: "https://mcp.example.com/mcp", Ready: true}}, nil
}
func (s *mcpStubBackend) MCPServer(context.Context, string) (domain.MCPServer, error) {
	return domain.MCPServer{ID: "mcp-1", Name: "Research", Transport: domain.MCPTransportHTTP}, nil
}
func (s *mcpStubBackend) CreateMCPServer(_ context.Context, input MCPServerInput) (domain.MCPServer, error) {
	s.received = input
	return domain.MCPServer{ID: "mcp-1", Name: *input.Name, Transport: *input.Transport}, nil
}
func (s *mcpStubBackend) UpdateMCPServer(_ context.Context, _ string, input MCPServerInput) (domain.MCPServer, error) {
	s.received = input
	return domain.MCPServer{ID: "mcp-1", Name: "Research", Transport: domain.MCPTransportHTTP}, nil
}
func (s *mcpStubBackend) DeleteMCPServer(_ context.Context, id string) error {
	s.deleted = id
	return nil
}
func (s *mcpStubBackend) AuthenticateMCPServer(context.Context, string) (string, error) {
	return "https://auth.example.com/connect", nil
}
func (s *mcpStubBackend) MCPServerAuthStatus(context.Context, string) (domain.MCPServer, error) {
	return domain.MCPServer{ID: "mcp-1", Name: "Research", Transport: domain.MCPTransportHTTP, Auth: domain.MCPAuthOAuth, AuthStatus: "logged_in", Ready: true}, nil
}
func (s *mcpStubBackend) BrowserMCPStatus(context.Context) (BrowserMCPStatus, error) {
	return BrowserMCPStatus{Name: "Nabu Browser", Available: true, Provider: "Playwright MCP", Package: "@playwright/mcp@0.0.79", Browser: "Google Chrome", Isolated: true}, nil
}

func TestMCPServerEndpointsUseTypedMetadataOnlyContract(t *testing.T) {
	backend := &mcpStubBackend{stubBackend: &stubBackend{}}
	handler := New(backend, testAssets(), nil).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/mcp/servers", strings.NewReader(`{"name":"Research","transport":"http","url":"https://mcp.example.com/mcp","secret_bindings":[{"secret_id":"secret-1","env_var":"MCP_TOKEN","bearer":true}]}`))
	request.Host = "127.0.0.1:7777"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || backend.received.URL == nil || *backend.received.URL != "https://mcp.example.com/mcp" || backend.received.SecretBindings == nil || len(*backend.received.SecretBindings) != 1 {
		t.Fatalf("MCP create response=%d %s input=%#v", response.Code, response.Body.String(), backend.received)
	}

	deleted := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/mcp/servers/mcp-1", nil)
	deleteRequest.Host = "127.0.0.1:7777"
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusOK || backend.deleted != "mcp-1" {
		t.Fatalf("delete = %d %q", deleted.Code, backend.deleted)
	}

	authenticated := httptest.NewRecorder()
	authRequest := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/mcp-1/authenticate", nil)
	authRequest.Host = "127.0.0.1:7777"
	handler.ServeHTTP(authenticated, authRequest)
	if authenticated.Code != http.StatusOK || !strings.Contains(authenticated.Body.String(), "https://auth.example.com/connect") {
		t.Fatalf("authenticate = %d %s", authenticated.Code, authenticated.Body.String())
	}
}

func TestBrowserMCPStatusEndpoint(t *testing.T) {
	backend := &mcpStubBackend{stubBackend: &stubBackend{}}
	handler := New(backend, testAssets(), nil).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/mcp/browser", nil)
	request.Host = "127.0.0.1:7777"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":true`) || !strings.Contains(response.Body.String(), "Playwright MCP") {
		t.Fatalf("browser MCP status = %d %s", response.Code, response.Body.String())
	}
}
