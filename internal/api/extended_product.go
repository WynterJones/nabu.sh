package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const maximumWorkspaceIconBytes = 2 * 1024 * 1024

type CalendarItem struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	StartsAt  time.Time  `json:"starts_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Recurring bool       `json:"recurring"`
	Href      string     `json:"href,omitempty"`
}

type CodexModelOption struct {
	ID                        string   `json:"id"`
	DisplayName               string   `json:"display_name"`
	Description               string   `json:"description,omitempty"`
	DefaultReasoningEffort    string   `json:"default_reasoning_effort,omitempty"`
	SupportedReasoningEfforts []string `json:"supported_reasoning_efforts"`
}

type CodexModelCatalog struct {
	Models []CodexModelOption `json:"models"`
	Source string             `json:"source"`
}

type ScopeIcon struct {
	Content     []byte
	ContentType string
	ETag        string
}

// ExtendedProductBackend keeps additive product surfaces independent from the
// phase 6-10 interface, which is also implemented by focused test backends.
type ExtendedProductBackend interface {
	Calendar(context.Context, time.Time, time.Time) ([]CalendarItem, error)
	CodexModels(context.Context) (CodexModelCatalog, error)
	SaveScopeIcon(context.Context, string, []byte, string) (domain.Workspace, error)
	DeleteScopeIcon(context.Context, string) error
	ScopeIcon(context.Context, string) (ScopeIcon, error)
}

func (s *Server) registerExtendedProductRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/calendar", s.getCalendar)
	mux.HandleFunc("GET /api/settings/operator/models", s.getCodexModels)
	mux.HandleFunc("GET /api/scopes/{id}/icon", s.getScopeIcon)
	mux.HandleFunc("POST /api/scopes/{id}/icon", s.postScopeIcon)
	mux.HandleFunc("DELETE /api/scopes/{id}/icon", s.deleteScopeIcon)
}

func (s *Server) extendedProduct(w http.ResponseWriter) ExtendedProductBackend {
	backend, ok := s.backend.(ExtendedProductBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include the requested feature.")
		return nil
	}
	return backend
}

func (s *Server) getCalendar(w http.ResponseWriter, r *http.Request) {
	backend := s.extendedProduct(w)
	if backend == nil {
		return
	}
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		s.respond(w, nil, fmt.Errorf("%w: from must be an RFC3339 timestamp", ErrInvalid))
		return
	}
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil || !to.After(from) || to.Sub(from) > 370*24*time.Hour {
		s.respond(w, nil, fmt.Errorf("%w: to must be after from and the range cannot exceed 370 days", ErrInvalid))
		return
	}
	items, err := backend.Calendar(r.Context(), from.UTC(), to.UTC())
	if items == nil {
		items = []CalendarItem{}
	}
	s.respond(w, map[string]any{"items": items}, err)
}

func (s *Server) getCodexModels(w http.ResponseWriter, r *http.Request) {
	if backend := s.extendedProduct(w); backend != nil {
		value, err := backend.CodexModels(r.Context())
		s.respond(w, value, err)
	}
}

func (s *Server) postScopeIcon(w http.ResponseWriter, r *http.Request) {
	backend := s.extendedProduct(w)
	if backend == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumWorkspaceIconBytes+64*1024)
	if err := r.ParseMultipartForm(maximumWorkspaceIconBytes + 64*1024); err != nil {
		s.respond(w, nil, fmt.Errorf("%w: workspace image must be a multipart upload no larger than 2 MB", ErrInvalid))
		return
	}
	file, _, err := r.FormFile("icon")
	if err != nil {
		s.respond(w, nil, fmt.Errorf("%w: multipart field icon is required", ErrInvalid))
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maximumWorkspaceIconBytes+1))
	if err != nil || len(content) == 0 || len(content) > maximumWorkspaceIconBytes {
		s.respond(w, nil, fmt.Errorf("%w: workspace image must be between 1 byte and 2 MB", ErrInvalid))
		return
	}
	contentType := http.DetectContentType(content)
	workspace, err := backend.SaveScopeIcon(r.Context(), r.PathValue("id"), content, contentType)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"workspace": workspace})
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) getScopeIcon(w http.ResponseWriter, r *http.Request) {
	backend := s.extendedProduct(w)
	if backend == nil {
		return
	}
	icon, err := backend.ScopeIcon(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	if match := r.Header.Get("If-None-Match"); icon.ETag != "" && match == icon.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", icon.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(icon.Content)))
	w.Header().Set("Cache-Control", "private, max-age=300")
	if icon.ETag != "" {
		w.Header().Set("ETag", icon.ETag)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(icon.Content)
}

func (s *Server) deleteScopeIcon(w http.ResponseWriter, r *http.Request) {
	backend := s.extendedProduct(w)
	if backend == nil {
		return
	}
	if err := backend.DeleteScopeIcon(r.Context(), strings.TrimSpace(r.PathValue("id"))); err != nil {
		s.respond(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
