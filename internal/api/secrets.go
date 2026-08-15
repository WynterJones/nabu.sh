package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// SecretView is deliberately metadata-only. Secret values never cross an API
// response boundary after they have entered the protected credential backend.
type SecretView struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Name        string    `json:"name"`
	Label       string    `json:"label,omitempty"`
	Description string    `json:"description,omitempty"`
	Configured  bool      `json:"configured"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SecretCreate struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type SecretUpdate struct {
	Name        *string `json:"name,omitempty"`
	Label       *string `json:"label,omitempty"`
	Description *string `json:"description,omitempty"`
}

type SecretBackend interface {
	Secrets(context.Context) ([]SecretView, error)
	Secret(context.Context, string) (SecretView, error)
	CreateSecret(context.Context, SecretCreate, []byte) (SecretView, error)
	UpdateSecret(context.Context, string, SecretUpdate, []byte) (SecretView, error)
	DeleteSecret(context.Context, string) error
}

type secretCreateRequest struct {
	Name        string      `json:"name"`
	Label       string      `json:"label,omitempty"`
	Description string      `json:"description,omitempty"`
	Value       secretBytes `json:"value"`
}

type secretUpdateRequest struct {
	Name        *string      `json:"name,omitempty"`
	Label       *string      `json:"label,omitempty"`
	Description *string      `json:"description,omitempty"`
	Value       *secretBytes `json:"value,omitempty"`
}

func (s *Server) registerSecretRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/secrets", s.getSecrets)
	mux.HandleFunc("POST /api/secrets", s.postSecret)
	mux.HandleFunc("GET /api/secrets/{id}", s.getSecret)
	mux.HandleFunc("PATCH /api/secrets/{id}", s.patchSecret)
	mux.HandleFunc("DELETE /api/secrets/{id}", s.deleteSecret)
}

func (s *Server) secretBackend(w http.ResponseWriter) SecretBackend {
	backend, ok := s.backend.(SecretBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include secure secrets.")
		return nil
	}
	return backend
}

func (s *Server) getSecrets(w http.ResponseWriter, r *http.Request) {
	if backend := s.secretBackend(w); backend != nil {
		values, err := backend.Secrets(r.Context())
		s.respond(w, map[string]any{"secrets": values}, err)
	}
}

func (s *Server) getSecret(w http.ResponseWriter, r *http.Request) {
	if backend := s.secretBackend(w); backend != nil {
		value, err := backend.Secret(r.Context(), r.PathValue("id"))
		s.respond(w, map[string]any{"secret": value}, err)
	}
}

func (s *Server) postSecret(w http.ResponseWriter, r *http.Request) {
	backend := s.secretBackend(w)
	if backend == nil {
		return
	}
	var input secretCreateRequest
	if !s.decode(w, r, &input) {
		wipeSecret(input.Value)
		return
	}
	defer wipeSecret(input.Value)
	if strings.TrimSpace(input.Name) == "" || len(input.Value) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_secret", "A name and secret value are required.")
		return
	}
	value, err := backend.CreateSecret(r.Context(), SecretCreate{Name: input.Name, Label: input.Label, Description: input.Description}, input.Value)
	if err == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"secret": value})
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) patchSecret(w http.ResponseWriter, r *http.Request) {
	backend := s.secretBackend(w)
	if backend == nil {
		return
	}
	var input secretUpdateRequest
	if !s.decode(w, r, &input) {
		if input.Value != nil {
			wipeSecret(*input.Value)
		}
		return
	}
	var valueBytes []byte
	if input.Value != nil {
		valueBytes = *input.Value
		defer wipeSecret(valueBytes)
	}
	if input.Name == nil && input.Label == nil && input.Description == nil && input.Value == nil {
		writeError(w, http.StatusBadRequest, "invalid_secret", "At least one secret field is required.")
		return
	}
	value, err := backend.UpdateSecret(r.Context(), r.PathValue("id"), SecretUpdate{Name: input.Name, Label: input.Label, Description: input.Description}, valueBytes)
	s.respond(w, map[string]any{"secret": value}, err)
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	if backend := s.secretBackend(w); backend != nil {
		s.respond(w, map[string]any{"deleted": true}, backend.DeleteSecret(r.Context(), r.PathValue("id")))
	}
}
