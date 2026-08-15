package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/integrations"
)

const maximumCredentialBytes = 64 * 1024

type IntegrationCredentialView struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
	Configured  bool   `json:"configured"`
}

type IntegrationCapabilityView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type IntegrationView struct {
	ID                    string                      `json:"id"`
	WorkspaceID           string                      `json:"workspace_id,omitempty"`
	Name                  string                      `json:"name"`
	Description           string                      `json:"description,omitempty"`
	Status                domain.IntegrationStatus    `json:"status"`
	Available             bool                        `json:"available"`
	Configured            bool                        `json:"configured"`
	Capabilities          []IntegrationCapabilityView `json:"capabilities"`
	RequiredCredentials   []IntegrationCredentialView `json:"required_credentials"`
	ConfiguredCredentials []string                    `json:"configured_credentials"`
	Error                 string                      `json:"error,omitempty"`
	VerifiedAt            *time.Time                  `json:"verified_at,omitempty"`
}

type IntegrationBackend interface {
	Integrations(context.Context) ([]IntegrationView, error)
	Integration(context.Context, string) (IntegrationView, error)
	SaveIntegrationCredentials(context.Context, string, map[string][]byte) (IntegrationView, error)
	DeleteIntegrationCredential(context.Context, string, string) (IntegrationView, error)
	VerifyIntegration(context.Context, string) (IntegrationView, error)
	CreateGeneratedIntegration(context.Context, integrations.Manifest) (IntegrationView, error)
}

func (s *Server) registerIntegrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations", s.getIntegrations)
	mux.HandleFunc("POST /api/integrations", s.postGeneratedIntegration)
	mux.HandleFunc("GET /api/integrations/{id}", s.getIntegration)
	mux.HandleFunc("POST /api/integrations/{id}/credentials", s.postIntegrationCredentials)
	mux.HandleFunc("DELETE /api/integrations/{id}/credentials/{name}", s.deleteIntegrationCredential)
	mux.HandleFunc("POST /api/integrations/{id}/verify", s.postIntegrationVerify)
}

func (s *Server) postGeneratedIntegration(w http.ResponseWriter, r *http.Request) {
	backend := s.integrationBackend(w)
	if backend == nil {
		return
	}
	var input integrations.Manifest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := backend.CreateGeneratedIntegration(r.Context(), input)
	if err == nil {
		w.WriteHeader(http.StatusCreated)
	}
	s.respond(w, map[string]any{"integration": value}, err)
}

func (s *Server) integrationBackend(w http.ResponseWriter) IntegrationBackend {
	backend, ok := s.backend.(IntegrationBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include integrations.")
		return nil
	}
	return backend
}

func (s *Server) getIntegrations(w http.ResponseWriter, r *http.Request) {
	if backend := s.integrationBackend(w); backend != nil {
		value, err := backend.Integrations(r.Context())
		s.respond(w, map[string]any{"integrations": value}, err)
	}
}

func (s *Server) getIntegration(w http.ResponseWriter, r *http.Request) {
	if backend := s.integrationBackend(w); backend != nil {
		value, err := backend.Integration(r.Context(), r.PathValue("id"))
		s.respond(w, map[string]any{"integration": value}, err)
	}
}

func (s *Server) postIntegrationCredentials(w http.ResponseWriter, r *http.Request) {
	backend := s.integrationBackend(w)
	if backend == nil {
		return
	}
	var input credentialInput
	if !s.decode(w, r, &input) {
		input.destroy()
		return
	}
	defer input.destroy()
	if len(input.Credentials) == 0 || len(input.Credentials) > 16 {
		writeError(w, http.StatusBadRequest, "invalid_credentials", "Credentials must contain between 1 and 16 named values.")
		return
	}
	values := make(map[string][]byte, len(input.Credentials))
	for name, secret := range input.Credentials {
		values[name] = []byte(secret)
	}
	value, err := backend.SaveIntegrationCredentials(r.Context(), r.PathValue("id"), values)
	s.respond(w, map[string]any{"integration": value}, err)
}

func (s *Server) deleteIntegrationCredential(w http.ResponseWriter, r *http.Request) {
	if backend := s.integrationBackend(w); backend != nil {
		value, err := backend.DeleteIntegrationCredential(r.Context(), r.PathValue("id"), r.PathValue("name"))
		s.respond(w, map[string]any{"integration": value}, err)
	}
}

func (s *Server) postIntegrationVerify(w http.ResponseWriter, r *http.Request) {
	if backend := s.integrationBackend(w); backend != nil {
		value, err := backend.VerifyIntegration(r.Context(), r.PathValue("id"))
		s.respond(w, map[string]any{"integration": value}, err)
	}
}

type secretBytes []byte

func (s *secretBytes) UnmarshalJSON(raw []byte) error {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return errors.New("credential value must be a string")
	}
	if len(raw) > maximumCredentialBytes*6+2 {
		return errors.New("credential value is too large")
	}
	decoded, err := decodeJSONString(raw)
	if err != nil {
		return err
	}
	if len(decoded) > maximumCredentialBytes {
		wipeSecret(decoded)
		return errors.New("credential exceeds 64 KiB")
	}
	*s = decoded
	return nil
}

func decodeJSONString(raw []byte) ([]byte, error) {
	result := make([]byte, 0, len(raw)-2)
	for index := 1; index < len(raw)-1; index++ {
		value := raw[index]
		if value != '\\' {
			if value < 0x20 {
				wipeSecret(result)
				return nil, errors.New("credential contains an invalid control character")
			}
			result = append(result, value)
			continue
		}
		index++
		if index >= len(raw)-1 {
			wipeSecret(result)
			return nil, errors.New("credential contains an incomplete escape")
		}
		switch raw[index] {
		case '"', '\\', '/':
			result = append(result, raw[index])
		case 'b':
			result = append(result, '\b')
		case 'f':
			result = append(result, '\f')
		case 'n':
			result = append(result, '\n')
		case 'r':
			result = append(result, '\r')
		case 't':
			result = append(result, '\t')
		case 'u':
			if index+4 >= len(raw) {
				wipeSecret(result)
				return nil, errors.New("credential contains an incomplete unicode escape")
			}
			first, err := strconv.ParseUint(string(raw[index+1:index+5]), 16, 16)
			if err != nil {
				wipeSecret(result)
				return nil, errors.New("credential contains an invalid unicode escape")
			}
			index += 4
			runeValue := rune(first)
			if runeValue >= 0xD800 && runeValue <= 0xDBFF {
				if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
					wipeSecret(result)
					return nil, errors.New("credential contains an invalid unicode surrogate")
				}
				second, parseErr := strconv.ParseUint(string(raw[index+3:index+7]), 16, 16)
				if parseErr != nil || second < 0xDC00 || second > 0xDFFF {
					wipeSecret(result)
					return nil, errors.New("credential contains an invalid unicode surrogate")
				}
				index += 6
				runeValue = 0x10000 + (runeValue-0xD800)*0x400 + (rune(second) - 0xDC00)
			}
			buffer := make([]byte, utf8.UTFMax)
			count := utf8.EncodeRune(buffer, runeValue)
			result = append(result, buffer[:count]...)
		default:
			wipeSecret(result)
			return nil, fmt.Errorf("credential contains an invalid escape")
		}
	}
	return result, nil
}

type credentialInput struct {
	Credentials map[string]secretBytes `json:"credentials"`
}

func (input *credentialInput) destroy() {
	for name, value := range input.Credentials {
		wipeSecret(value)
		delete(input.Credentials, name)
	}
}

func wipeSecret(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ json.Unmarshaler = (*secretBytes)(nil)
