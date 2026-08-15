package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type outputsStubBackend struct{ *stubBackend }

func (o outputsStubBackend) WorkspaceOutputs(context.Context) (WorkspaceOutputs, error) {
	return WorkspaceOutputs{Items: []WorkspaceOutput{{
		ID: "artifact-1", Kind: "document", Name: "Launch plan", Path: "documents/launch.md",
		FileKind: "text", Editable: true, CreatedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
	}}}, nil
}

func TestWorkspaceOutputsRoute(t *testing.T) {
	handler := New(outputsStubBackend{stubBackend: &stubBackend{}}, testAssets(), nil).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/outputs", nil)
	request.Host = "127.0.0.1:7777"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"path":"documents/launch.md"`) || !strings.Contains(response.Body.String(), `"scripts":[]`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
