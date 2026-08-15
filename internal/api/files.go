package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"time"
)

type WorkspaceFileEntry struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type WorkspaceFile struct {
	Path         string               `json:"path"`
	Name         string               `json:"name"`
	Kind         string               `json:"kind"`
	MIMEType     string               `json:"mime_type"`
	Size         int64                `json:"size"`
	Editable     bool                 `json:"editable"`
	Content      string               `json:"content,omitempty"`
	ResolvedPath string               `json:"-"`
	ModifiedAt   time.Time            `json:"modified_at"`
	Entries      []WorkspaceFileEntry `json:"entries,omitempty"`
	Truncated    bool                 `json:"truncated,omitempty"`
}

type WorkspaceFileBackend interface {
	WorkspaceFile(context.Context, string, bool) (WorkspaceFile, error)
	SaveWorkspaceFile(context.Context, string, string) (WorkspaceFile, error)
}

func (s *Server) registerFileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files", s.getWorkspaceFile)
	mux.HandleFunc("PUT /api/files", s.putWorkspaceFile)
	mux.HandleFunc("GET /api/files/content", s.getWorkspaceFileContent)
}

func (s *Server) fileBackend(w http.ResponseWriter) WorkspaceFileBackend {
	backend, ok := s.backend.(WorkspaceFileBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include workspace file viewing.")
		return nil
	}
	return backend
}

func (s *Server) getWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	backend := s.fileBackend(w)
	if backend == nil {
		return
	}
	value, err := backend.WorkspaceFile(r.Context(), r.URL.Query().Get("path"), true)
	s.respond(w, value, err)
}

func (s *Server) putWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	backend := s.fileBackend(w)
	if backend == nil {
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := backend.SaveWorkspaceFile(r.Context(), r.URL.Query().Get("path"), input.Content)
	s.respond(w, value, err)
}

func (s *Server) getWorkspaceFileContent(w http.ResponseWriter, r *http.Request) {
	backend := s.fileBackend(w)
	if backend == nil {
		return
	}
	value, err := backend.WorkspaceFile(r.Context(), r.URL.Query().Get("path"), false)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	if value.Kind == "directory" {
		s.respond(w, nil, errors.Join(ErrInvalid, errors.New("path must identify a regular file")))
		return
	}
	file, err := os.Open(value.ResolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.respond(w, nil, ErrNotFound)
			return
		}
		s.respond(w, nil, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", value.MIMEType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": value.Name}))
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, value.Name, value.ModifiedAt, file)
}
