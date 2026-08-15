package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

type LocalAppInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Directory   string   `json:"directory"`
	Command     []string `json:"command"`
	Port        int      `json:"port"`
	HealthPath  string   `json:"health_path,omitempty"`
	AutoStart   bool     `json:"auto_start"`
}

type LocalAppUpdate struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Directory   *string   `json:"directory,omitempty"`
	Command     *[]string `json:"command,omitempty"`
	Port        *int      `json:"port,omitempty"`
	HealthPath  *string   `json:"health_path,omitempty"`
	AutoStart   *bool     `json:"auto_start,omitempty"`
}

type LocalAppView struct {
	domain.LocalApp
	Status    string     `json:"status"`
	PID       int        `json:"pid,omitempty"`
	URL       string     `json:"url"`
	Healthy   bool       `json:"healthy"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type LocalAppLogs struct {
	AppID   string `json:"app_id"`
	Content string `json:"content"`
}

type LocalAppsBackend interface {
	LocalApps(context.Context) ([]LocalAppView, error)
	LocalApp(context.Context, string) (LocalAppView, error)
	CreateLocalApp(context.Context, LocalAppInput) (LocalAppView, error)
	UpdateLocalApp(context.Context, string, LocalAppUpdate) (LocalAppView, error)
	DeleteLocalApp(context.Context, string) error
	StartLocalApp(context.Context, string) (LocalAppView, error)
	StopLocalApp(context.Context, string) (LocalAppView, error)
	RestartLocalApp(context.Context, string) (LocalAppView, error)
	LocalAppLogs(context.Context, string) (LocalAppLogs, error)
}

func (s *Server) registerLocalAppRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/apps", s.getLocalApps)
	mux.HandleFunc("POST /api/apps", s.postLocalApp)
	mux.HandleFunc("GET /api/apps/{id}", s.getLocalApp)
	mux.HandleFunc("PATCH /api/apps/{id}", s.patchLocalApp)
	mux.HandleFunc("DELETE /api/apps/{id}", s.deleteLocalApp)
	mux.HandleFunc("POST /api/apps/{id}/start", s.postStartLocalApp)
	mux.HandleFunc("POST /api/apps/{id}/stop", s.postStopLocalApp)
	mux.HandleFunc("POST /api/apps/{id}/restart", s.postRestartLocalApp)
	mux.HandleFunc("GET /api/apps/{id}/logs", s.getLocalAppLogs)
}

func (s *Server) localAppsBackend(w http.ResponseWriter) (LocalAppsBackend, bool) {
	backend, ok := s.backend.(LocalAppsBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include local applications.")
	}
	return backend, ok
}

func (s *Server) getLocalApps(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.localAppsBackend(w)
	if !ok {
		return
	}
	apps, err := backend.LocalApps(r.Context())
	if apps == nil {
		apps = []LocalAppView{}
	}
	s.respond(w, map[string]any{"apps": apps}, err)
}

func (s *Server) getLocalApp(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.localAppsBackend(w)
	if !ok {
		return
	}
	value, err := backend.LocalApp(r.Context(), r.PathValue("id"))
	s.respond(w, value, err)
}

func (s *Server) postLocalApp(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.localAppsBackend(w)
	if !ok {
		return
	}
	var input LocalAppInput
	if !s.decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Directory) == "" || len(input.Command) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_app", "Name, folder, and start command are required.")
		return
	}
	value, err := backend.CreateLocalApp(r.Context(), input)
	if err == nil {
		writeJSON(w, http.StatusCreated, value)
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) patchLocalApp(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.localAppsBackend(w)
	if !ok {
		return
	}
	var input LocalAppUpdate
	if !s.decode(w, r, &input) {
		return
	}
	if input.Name == nil && input.Description == nil && input.Directory == nil && input.Command == nil && input.Port == nil && input.HealthPath == nil && input.AutoStart == nil {
		writeError(w, http.StatusBadRequest, "invalid_app", "At least one application field is required.")
		return
	}
	value, err := backend.UpdateLocalApp(r.Context(), r.PathValue("id"), input)
	s.respond(w, value, err)
}

func (s *Server) deleteLocalApp(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.localAppsBackend(w)
	if !ok {
		return
	}
	if err := backend.DeleteLocalApp(r.Context(), r.PathValue("id")); err != nil {
		s.respond(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) postStartLocalApp(w http.ResponseWriter, r *http.Request) {
	s.localAppAction(w, r, func(backend LocalAppsBackend) (LocalAppView, error) {
		return backend.StartLocalApp(r.Context(), r.PathValue("id"))
	})
}

func (s *Server) postStopLocalApp(w http.ResponseWriter, r *http.Request) {
	s.localAppAction(w, r, func(backend LocalAppsBackend) (LocalAppView, error) {
		return backend.StopLocalApp(r.Context(), r.PathValue("id"))
	})
}

func (s *Server) postRestartLocalApp(w http.ResponseWriter, r *http.Request) {
	s.localAppAction(w, r, func(backend LocalAppsBackend) (LocalAppView, error) {
		return backend.RestartLocalApp(r.Context(), r.PathValue("id"))
	})
}

func (s *Server) localAppAction(w http.ResponseWriter, _ *http.Request, action func(LocalAppsBackend) (LocalAppView, error)) {
	backend, ok := s.localAppsBackend(w)
	if !ok {
		return
	}
	value, err := action(backend)
	s.respond(w, value, err)
}

func (s *Server) getLocalAppLogs(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.localAppsBackend(w)
	if !ok {
		return
	}
	value, err := backend.LocalAppLogs(r.Context(), r.PathValue("id"))
	s.respond(w, value, err)
}
