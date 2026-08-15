package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/remoteaccess"
)

type CheckResult struct {
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

type SetupChecks struct {
	Codex      CheckResult   `json:"codex"`
	Git        CheckResult   `json:"git"`
	Workspaces []CheckResult `json:"workspaces,omitempty"`
}

type SetupCheckRequest struct {
	Workspaces []string `json:"workspaces"`
	Paths      []string `json:"paths"`
}

type SetupRequest struct {
	DisplayName string        `json:"display_name"`
	Mission     string        `json:"mission"`
	Context     string        `json:"context"`
	Workspaces  []string      `json:"workspaces"`
	Policy      domain.Policy `json:"policy"`
}

type MissionUpdate struct {
	Statement string `json:"statement"`
	Context   string `json:"context"`
}

type TaskCreate struct {
	Title            string          `json:"title"`
	Purpose          string          `json:"purpose"`
	Why              string          `json:"why"`
	Priority         domain.Priority `json:"priority"`
	DefinitionOfDone []string        `json:"definition_of_done"`
	DependsOnTaskIDs []string        `json:"depends_on_task_ids,omitempty"`
	WorkspaceID      string          `json:"workspace_id"`
	PlannedAt        *time.Time      `json:"planned_at,omitempty"`
}

type TaskUpdate struct {
	Title            *string            `json:"title,omitempty"`
	Purpose          *string            `json:"purpose,omitempty"`
	Why              *string            `json:"why,omitempty"`
	Status           *domain.TaskStatus `json:"status,omitempty"`
	Priority         *domain.Priority   `json:"priority,omitempty"`
	DefinitionOfDone *[]string          `json:"definition_of_done,omitempty"`
	DependsOnTaskIDs *[]string          `json:"depends_on_task_ids,omitempty"`
	PlannedAt        *time.Time         `json:"planned_at,omitempty"`
}

type TaskRecovery struct {
	Note string `json:"note,omitempty"`
}

type TaskDraftRequest struct {
	Request  string          `json:"request"`
	Priority domain.Priority `json:"priority,omitempty"`
}

type TaskDraft struct {
	Title            string          `json:"title"`
	Purpose          string          `json:"purpose"`
	Why              string          `json:"why"`
	Priority         domain.Priority `json:"priority"`
	DefinitionOfDone []string        `json:"definition_of_done"`
}

type Backend interface {
	Status(context.Context) (domain.StatusSnapshot, error)
	Mission(context.Context) (domain.Mission, error)
	UpdateMission(context.Context, MissionUpdate) (domain.Mission, error)
	Workspaces(context.Context) ([]domain.Workspace, error)
	CheckSetup(context.Context, []string) (SetupChecks, error)
	BrowseWorkspace(context.Context) (string, error)
	CompleteSetup(context.Context, SetupRequest) (domain.StatusSnapshot, error)
	StartMission(context.Context) (domain.StatusSnapshot, error)
	SetPaused(context.Context, bool) (domain.StatusSnapshot, error)
	RequestOrientation(context.Context, string) error
	Tasks(context.Context) ([]domain.Task, error)
	DraftTask(context.Context, TaskDraftRequest) (TaskDraft, error)
	CreateTask(context.Context, TaskCreate) (domain.Task, error)
	Task(context.Context, string) (domain.Task, error)
	UpdateTask(context.Context, string, TaskUpdate) (domain.Task, error)
	RunTask(context.Context, string) (domain.Task, error)
	RecoverTask(context.Context, string, TaskRecovery) (domain.Message, error)
	DeleteTask(context.Context, string) error
	Run(context.Context, string) (domain.Run, error)
	RecentEvents(context.Context, int64, int) ([]domain.Event, error)
	Subscribe() (<-chan domain.Event, func())
}

type Server struct {
	backend Backend
	assets  fs.FS
	logger  *slog.Logger
	handler http.Handler
}

func New(backend Backend, assets fs.FS, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{backend: backend, assets: assets, logger: logger}
	s.handler = s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.getStatus)
	mux.HandleFunc("GET /api/mission", s.getMission)
	mux.HandleFunc("PUT /api/mission", s.putMission)
	mux.HandleFunc("GET /api/workspaces", s.getWorkspaces)
	mux.HandleFunc("POST /api/setup/checks", s.postSetupChecks)
	mux.HandleFunc("POST /api/setup/browse", s.postSetupBrowse)
	mux.HandleFunc("POST /api/setup/complete", s.postSetupComplete)
	mux.HandleFunc("POST /api/mission/start", s.postStartMission)
	mux.HandleFunc("POST /api/pause", s.postPause)
	mux.HandleFunc("POST /api/orient", s.postOrient)
	mux.HandleFunc("GET /api/tasks", s.getTasks)
	mux.HandleFunc("POST /api/tasks/draft", s.postTaskDraft)
	mux.HandleFunc("POST /api/tasks", s.postTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.patchTask)
	mux.HandleFunc("POST /api/tasks/{id}/run", s.postTaskRun)
	mux.HandleFunc("POST /api/tasks/{id}/recover", s.postTaskRecovery)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.deleteTask)
	mux.HandleFunc("GET /api/runs/{id}", s.getRun)
	mux.HandleFunc("GET /api/events", s.getEvents)
	mux.HandleFunc("GET /api/remote-access/tailscale", s.getTailscaleStatus)
	mux.HandleFunc("POST /api/remote-access/tailscale/serve", s.postTailscaleServe)
	mux.HandleFunc("DELETE /api/remote-access/tailscale/serve", s.deleteTailscaleServe)
	s.registerProductRoutes(mux)
	s.registerExtendedProductRoutes(mux)
	s.registerDatabaseRoutes(mux)
	s.registerFileRoutes(mux)
	s.registerOutputRoutes(mux)
	s.registerLocalAppRoutes(mux)
	if s.assets != nil {
		mux.Handle("/", spaHandler(s.assets))
	}
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'")
		if !trustedRequest(r) {
			writeError(w, http.StatusForbidden, "invalid_host", "Nabu only accepts localhost or private Tailscale requests.")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			writeError(w, http.StatusForbidden, "invalid_origin", "Cross-origin requests are not allowed.")
			return
		}
		contentType := strings.ToLower(r.Header.Get("Content-Type"))
		iconUpload := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/scopes/") && strings.HasSuffix(r.URL.Path, "/icon")
		validBodyType := strings.HasPrefix(contentType, "application/json") || (iconUpload && strings.HasPrefix(contentType, "multipart/form-data"))
		if r.ContentLength != 0 && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) && !validBodyType {
			writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "JSON requests must use Content-Type: application/json.")
			return
		}
		started := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/events" {
			s.logger.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
		}
	})
}

func localHost(hostport string) bool {
	host := normalizedHost(hostport)
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func trustedHost(hostport string) bool {
	return localHost(hostport) || tailscaleHost(hostport)
}

func trustedRequest(r *http.Request) bool {
	if localHost(r.Host) {
		return true
	}
	// Tailscale Serve strips spoofed identity headers and adds this header only
	// for authenticated user traffic inside the tailnet. Funnel traffic is
	// public and deliberately has no identity headers, so it fails closed here.
	return tailscaleHost(r.Host) && strings.TrimSpace(r.Header.Get("Tailscale-User-Login")) != ""
}

func tailscaleHost(hostport string) bool {
	host := normalizedHost(hostport)
	if len(host) > 253 || !strings.HasSuffix(host, ".ts.net") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func normalizedHost(hostport string) string {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	return strings.Trim(strings.ToLower(host), "[]")
}

func sameOrigin(origin, requestHost string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	validScheme := parsed.Scheme == "http" && localHost(parsed.Host)
	validScheme = validScheme || parsed.Scheme == "https" && tailscaleHost(parsed.Host)
	return validScheme && strings.EqualFold(parsed.Host, requestHost) && trustedHost(parsed.Host)
}

func (s *Server) getTailscaleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, remoteaccess.ProbeTailscale(r.Context()))
}

func (s *Server) postTailscaleServe(w http.ResponseWriter, r *http.Request) {
	result, err := remoteaccess.EnableNabuServe(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tailscale_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deleteTailscaleServe(w http.ResponseWriter, r *http.Request) {
	status, err := remoteaccess.DisableNabuServe(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tailscale_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.Status(r.Context())
	s.respond(w, value, err)
}

func (s *Server) getMission(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.Mission(r.Context())
	s.respond(w, value, err)
}

func (s *Server) putMission(w http.ResponseWriter, r *http.Request) {
	var input MissionUpdate
	if !s.decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Statement) == "" {
		writeError(w, http.StatusBadRequest, "invalid_mission", "Mission cannot be empty.")
		return
	}
	value, err := s.backend.UpdateMission(r.Context(), input)
	s.respond(w, value, err)
}

func (s *Server) getWorkspaces(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.Workspaces(r.Context())
	s.respond(w, value, err)
}

func (s *Server) postSetupChecks(w http.ResponseWriter, r *http.Request) {
	var input SetupCheckRequest
	if r.ContentLength != 0 && !s.decode(w, r, &input) {
		return
	}
	paths := input.Workspaces
	if len(paths) == 0 {
		paths = input.Paths
	}
	value, err := s.backend.CheckSetup(r.Context(), paths)
	s.respond(w, value, err)
}

func (s *Server) postSetupBrowse(w http.ResponseWriter, r *http.Request) {
	path, err := s.backend.BrowseWorkspace(r.Context())
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) postSetupComplete(w http.ResponseWriter, r *http.Request) {
	var input SetupRequest
	if !s.decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.DisplayName) == "" || strings.TrimSpace(input.Mission) == "" {
		writeError(w, http.StatusBadRequest, "invalid_setup", "Display name and mission are required.")
		return
	}
	value, err := s.backend.CompleteSetup(r.Context(), input)
	if err == nil {
		writeJSON(w, http.StatusCreated, value)
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) postStartMission(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.StartMission(r.Context())
	s.respond(w, value, err)
}

func (s *Server) postPause(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Paused bool `json:"paused"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.backend.SetPaused(r.Context(), input.Paused)
	s.respond(w, value, err)
}

func (s *Server) postOrient(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.RequestOrientation(r.Context(), "user_requested"); err != nil {
		s.respond(w, nil, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func (s *Server) getTasks(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.Tasks(r.Context())
	s.respond(w, value, err)
}

func (s *Server) postTask(w http.ResponseWriter, r *http.Request) {
	var input TaskCreate
	if !s.decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Purpose) == "" || !hasNonEmpty(input.DefinitionOfDone) {
		writeError(w, http.StatusBadRequest, "invalid_task", "Title, purpose, and at least one definition-of-done item are required.")
		return
	}
	value, err := s.backend.CreateTask(r.Context(), input)
	if err == nil {
		writeJSON(w, http.StatusCreated, value)
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) postTaskDraft(w http.ResponseWriter, r *http.Request) {
	var input TaskDraftRequest
	if !s.decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Request) == "" {
		writeError(w, http.StatusBadRequest, "invalid_task_request", "Describe what Nabu should work on.")
		return
	}
	value, err := s.backend.DraftTask(r.Context(), input)
	s.respond(w, value, err)
}

func hasNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.Task(r.Context(), r.PathValue("id"))
	s.respond(w, value, err)
}

func (s *Server) patchTask(w http.ResponseWriter, r *http.Request) {
	var input TaskUpdate
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.backend.UpdateTask(r.Context(), r.PathValue("id"), input)
	s.respond(w, value, err)
}

func (s *Server) postTaskRun(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.RunTask(r.Context(), r.PathValue("id"))
	if err == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"task": value})
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) postTaskRecovery(w http.ResponseWriter, r *http.Request) {
	var input TaskRecovery
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.backend.RecoverTask(r.Context(), r.PathValue("id"), input)
	if err == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"message": value})
		return
	}
	s.respond(w, nil, err)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.DeleteTask(r.Context(), r.PathValue("id")); err != nil {
		s.respond(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	value, err := s.backend.Run(r.Context(), r.PathValue("id"))
	s.respond(w, value, err)
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "Live updates are unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	events, unsubscribe := s.backend.Subscribe()
	defer unsubscribe()
	var afterID int64
	if value := strings.TrimSpace(r.Header.Get("Last-Event-ID")); value != "" {
		_, _ = fmt.Sscan(value, &afterID)
	}
	if replay, err := s.backend.RecentEvents(r.Context(), afterID, 200); err == nil {
		for _, event := range replay {
			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload)
		}
	}
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload)
			flusher.Flush()
		}
	}
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must be valid JSON: "+err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func (s *Server) respond(w http.ResponseWriter, value any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, value)
		return
	}
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	if errors.Is(err, ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", publicErrorMessage(err, ErrConflict))
		return
	}
	if errors.Is(err, ErrInvalid) {
		writeError(w, http.StatusBadRequest, "invalid_request", publicErrorMessage(err, ErrInvalid))
		return
	}
	if errors.Is(err, ErrUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "unavailable", publicErrorMessage(err, ErrUnavailable))
		return
	}
	s.logger.Error("api request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Nabu could not complete the request.")
}

func publicErrorMessage(err, category error) string {
	message := strings.TrimSpace(err.Error())
	prefix := category.Error() + ":"
	if strings.HasPrefix(message, prefix) {
		message = strings.TrimSpace(strings.TrimPrefix(message, prefix))
	}
	return message
}

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrInvalid     = errors.New("invalid request")
	ErrUnavailable = errors.New("unavailable")
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func spaHandler(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if info, err := fs.Stat(assets, path); err == nil && !info.IsDir() {
				if strings.Contains(path, "/assets/") || strings.HasPrefix(path, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		content, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "Nabu frontend assets are unavailable. Run the frontend build.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(content)
	})
}
