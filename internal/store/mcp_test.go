package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestMCPServersAreMetadataOnlyWorkspaceScopedAndAtomic(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	one, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "mcp-one", Name: "One", Path: "/mcp-one"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "mcp-two", Name: "Two", Path: "/mcp-two"})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.CreateSecretRecord(ctx, domain.SecretRecord{ID: "mcp-token", WorkspaceID: one.ID, Name: "MCP token"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateSecretRecord(ctx, domain.SecretRecord{ID: "foreign-token", WorkspaceID: two.ID, Name: "Foreign token"})
	if err != nil {
		t.Fatal(err)
	}

	created, err := s.CreateMCPServer(ctx, domain.MCPServer{
		ID: "mcp-server", WorkspaceID: one.ID, Name: "Research", Transport: domain.MCPTransportHTTP,
		URL: "https://mcp.example.com/mcp", Enabled: true,
		SecretBindings: []domain.MCPSecretBinding{{SecretRecordID: secret.ID, Env: "MCP_TOKEN", Bearer: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Access != domain.MCPAccessRead || len(created.SecretBindings) != 1 || created.SecretBindings[0].CredentialName != secret.ReferenceKey {
		t.Fatalf("created connector = %#v", created)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "credential_name") || strings.Contains(string(encoded), "credential_integration") {
		t.Fatalf("runtime credential metadata escaped through JSON: %s", encoded)
	}
	if _, err := s.GetMCPServerForWorkspace(ctx, two.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace get error = %v", err)
	}

	changed := created
	changed.Description = "must roll back"
	changed.SecretBindings = []domain.MCPSecretBinding{{SecretRecordID: foreign.ID, Env: "MCP_TOKEN", Bearer: true}}
	if err := s.UpdateMCPServerForWorkspace(ctx, one.ID, changed); err == nil {
		t.Fatal("cross-workspace secret binding was accepted")
	}
	loaded, err := s.GetMCPServerForWorkspace(ctx, one.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Description != "" || loaded.SecretBindings[0].SecretRecordID != secret.ID {
		t.Fatalf("failed update was not atomic: %#v", loaded)
	}
}

func TestMCPServerConnectionValidation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "mcp-validation", Name: "Validation", Path: "/mcp-validation"})
	if err != nil {
		t.Fatal(err)
	}
	for name, server := range map[string]domain.MCPServer{
		"remote plain http": {WorkspaceID: workspace.ID, Name: "Remote", Transport: domain.MCPTransportHTTP, URL: "http://example.com/mcp"},
		"relative command":  {WorkspaceID: workspace.ID, Name: "Local", Transport: domain.MCPTransportStdio, Command: "npx"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateMCPServer(ctx, server); err == nil {
				t.Fatal("unsafe connector was accepted")
			}
		})
	}
	if _, err := s.CreateMCPServer(ctx, domain.MCPServer{WorkspaceID: workspace.ID, Name: "Loopback", Transport: domain.MCPTransportHTTP, URL: "http://127.0.0.1:8765/mcp"}); err != nil {
		t.Fatalf("loopback HTTP should be valid: %v", err)
	}
	oauth, err := s.CreateMCPServer(ctx, domain.MCPServer{WorkspaceID: workspace.ID, Name: "OAuth", Transport: domain.MCPTransportHTTP, URL: "https://mcp.example.com/mcp", Auth: domain.MCPAuthOAuth})
	if err != nil || oauth.Auth != domain.MCPAuthOAuth {
		t.Fatalf("OAuth connector = %#v, %v", oauth, err)
	}
}
