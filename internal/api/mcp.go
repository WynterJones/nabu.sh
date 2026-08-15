package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

type MCPServerInput struct {
	Name                  *string                    `json:"name,omitempty"`
	Description           *string                    `json:"description,omitempty"`
	Transport             *domain.MCPTransport       `json:"transport,omitempty"`
	Command               *string                    `json:"command,omitempty"`
	Args                  *[]string                  `json:"args,omitempty"`
	URL                   *string                    `json:"url,omitempty"`
	Auth                  *domain.MCPAuth            `json:"auth,omitempty"`
	Enabled               *bool                      `json:"enabled,omitempty"`
	Access                *domain.MCPAccess          `json:"access,omitempty"`
	Required              *bool                      `json:"required,omitempty"`
	StartupTimeoutSeconds *int64                     `json:"startup_timeout_seconds,omitempty"`
	ToolTimeoutSeconds    *int64                     `json:"tool_timeout_seconds,omitempty"`
	EnabledTools          *[]string                  `json:"enabled_tools,omitempty"`
	DisabledTools         *[]string                  `json:"disabled_tools,omitempty"`
	SecretBindings        *[]domain.MCPSecretBinding `json:"secret_bindings,omitempty"`
}

type MCPBackend interface {
	MCPServers(context.Context) ([]domain.MCPServer, error)
	MCPServer(context.Context, string) (domain.MCPServer, error)
	CreateMCPServer(context.Context, MCPServerInput) (domain.MCPServer, error)
	UpdateMCPServer(context.Context, string, MCPServerInput) (domain.MCPServer, error)
	DeleteMCPServer(context.Context, string) error
	AuthenticateMCPServer(context.Context, string) (string, error)
	MCPServerAuthStatus(context.Context, string) (domain.MCPServer, error)
}

type BrowserMCPStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Provider  string `json:"provider"`
	Package   string `json:"package"`
	Browser   string `json:"browser"`
	Isolated  bool   `json:"isolated"`
	Reason    string `json:"reason,omitempty"`
}

type BrowserMCPBackend interface {
	BrowserMCPStatus(context.Context) (BrowserMCPStatus, error)
}

func (s *Server) registerMCPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mcp/browser", s.getBrowserMCPStatus)
	mux.HandleFunc("GET /api/mcp/servers", s.getMCPServers)
	mux.HandleFunc("POST /api/mcp/servers", s.postMCPServer)
	mux.HandleFunc("GET /api/mcp/servers/{id}", s.getMCPServer)
	mux.HandleFunc("PATCH /api/mcp/servers/{id}", s.patchMCPServer)
	mux.HandleFunc("DELETE /api/mcp/servers/{id}", s.deleteMCPServer)
	mux.HandleFunc("POST /api/mcp/servers/{id}/authenticate", s.authenticateMCPServer)
	mux.HandleFunc("GET /api/mcp/servers/{id}/auth-status", s.getMCPServerAuthStatus)
}

func (s *Server) getBrowserMCPStatus(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.backend.(BrowserMCPBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include built-in browser tools.")
		return
	}
	status, err := backend.BrowserMCPStatus(r.Context())
	s.respond(w, map[string]any{"browser": status}, err)
}

func (s *Server) mcpBackend(w http.ResponseWriter) MCPBackend {
	backend, ok := s.backend.(MCPBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include MCP connectors.")
		return nil
	}
	return backend
}

func (s *Server) getMCPServers(w http.ResponseWriter, r *http.Request) {
	if backend := s.mcpBackend(w); backend != nil {
		values, err := backend.MCPServers(r.Context())
		s.respond(w, map[string]any{"servers": values}, err)
	}
}

func (s *Server) getMCPServer(w http.ResponseWriter, r *http.Request) {
	if backend := s.mcpBackend(w); backend != nil {
		value, err := backend.MCPServer(r.Context(), r.PathValue("id"))
		s.respond(w, map[string]any{"server": value}, err)
	}
}

func (s *Server) postMCPServer(w http.ResponseWriter, r *http.Request) {
	backend := s.mcpBackend(w)
	if backend == nil {
		return
	}
	var input MCPServerInput
	if !s.decode(w, r, &input) {
		return
	}
	if input.Name == nil || input.Transport == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_mcp_server", "A name and transport are required.")
		return
	}
	value, err := backend.CreateMCPServer(r.Context(), input)
	if err == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"server": value})
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) patchMCPServer(w http.ResponseWriter, r *http.Request) {
	backend := s.mcpBackend(w)
	if backend == nil {
		return
	}
	var input MCPServerInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := backend.UpdateMCPServer(r.Context(), r.PathValue("id"), input)
	s.respond(w, map[string]any{"server": value}, err)
}

func (s *Server) deleteMCPServer(w http.ResponseWriter, r *http.Request) {
	if backend := s.mcpBackend(w); backend != nil {
		s.respond(w, map[string]any{"deleted": true}, backend.DeleteMCPServer(r.Context(), r.PathValue("id")))
	}
}

func (s *Server) authenticateMCPServer(w http.ResponseWriter, r *http.Request) {
	if backend := s.mcpBackend(w); backend != nil {
		url, err := backend.AuthenticateMCPServer(r.Context(), r.PathValue("id"))
		s.respond(w, map[string]any{"started": err == nil, "authorization_url": url}, err)
	}
}

func (s *Server) getMCPServerAuthStatus(w http.ResponseWriter, r *http.Request) {
	if backend := s.mcpBackend(w); backend != nil {
		value, err := backend.MCPServerAuthStatus(r.Context(), r.PathValue("id"))
		s.respond(w, map[string]any{"server": value}, err)
	}
}
