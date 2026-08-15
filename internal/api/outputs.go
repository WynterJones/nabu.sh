package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

// WorkspaceOutput is a user-facing result Nabu produced inside the active
// workspace. Paths are always workspace-relative and safe to pass to the file
// viewer; URLs are retained only for http(s) destinations.
type WorkspaceOutput struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Path        string    `json:"path,omitempty"`
	URL         string    `json:"url,omitempty"`
	FileKind    string    `json:"file_kind,omitempty"`
	MIMEType    string    `json:"mime_type,omitempty"`
	Size        int64     `json:"size,omitempty"`
	Editable    bool      `json:"editable,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	TaskTitle   string    `json:"task_title,omitempty"`
	ScriptRunID string    `json:"script_run_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type WorkspaceOutputs struct {
	Items   []WorkspaceOutput `json:"items"`
	Scripts []domain.Script   `json:"scripts"`
}

type WorkspaceOutputsBackend interface {
	WorkspaceOutputs(context.Context) (WorkspaceOutputs, error)
}

func (s *Server) registerOutputRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/outputs", s.getWorkspaceOutputs)
}

func (s *Server) getWorkspaceOutputs(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.backend.(WorkspaceOutputsBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include workspace outputs.")
		return
	}
	value, err := backend.WorkspaceOutputs(r.Context())
	if value.Items == nil {
		value.Items = []WorkspaceOutput{}
	}
	if value.Scripts == nil {
		value.Scripts = []domain.Script{}
	}
	s.respond(w, value, err)
}
