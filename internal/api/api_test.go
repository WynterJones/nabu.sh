package api

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestSafeAPIErrorDoesNotExposeInternalCategoryPrefix(t *testing.T) {
	server := New(&stubBackend{}, testAssets(), nil)
	response := httptest.NewRecorder()
	server.respond(response, nil, fmt.Errorf("%w: unlock the system credential vault and try again", ErrUnavailable))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, `"message":"unavailable:`) || !strings.Contains(body, "unlock the system credential vault") {
		t.Fatalf("response = %s", body)
	}
}

type stubBackend struct {
	status        domain.StatusSnapshot
	tasks         []domain.Task
	stream        chan domain.Event
	deletedTask   string
	recoveredTask string
	recoveryNote  string
	runTask       string
}

func (s *stubBackend) Status(context.Context) (domain.StatusSnapshot, error) { return s.status, nil }
func (s *stubBackend) Mission(context.Context) (domain.Mission, error)       { return domain.Mission{}, nil }
func (s *stubBackend) UpdateMission(context.Context, MissionUpdate) (domain.Mission, error) {
	return domain.Mission{}, nil
}
func (s *stubBackend) Workspaces(context.Context) ([]domain.Workspace, error) {
	return []domain.Workspace{}, nil
}
func (s *stubBackend) CheckSetup(context.Context, []string) (SetupChecks, error) {
	return SetupChecks{}, nil
}
func (s *stubBackend) BrowseWorkspace(context.Context) (string, error) { return "/tmp/project", nil }
func (s *stubBackend) CompleteSetup(context.Context, SetupRequest) (domain.StatusSnapshot, error) {
	return s.status, nil
}
func (s *stubBackend) StartMission(context.Context) (domain.StatusSnapshot, error) {
	return s.status, nil
}
func (s *stubBackend) SetPaused(_ context.Context, paused bool) (domain.StatusSnapshot, error) {
	s.status.Paused = paused
	return s.status, nil
}
func (s *stubBackend) RequestOrientation(context.Context, string) error { return nil }
func (s *stubBackend) Tasks(context.Context) ([]domain.Task, error)     { return s.tasks, nil }
func (s *stubBackend) DraftTask(_ context.Context, input TaskDraftRequest) (TaskDraft, error) {
	return TaskDraft{Title: "Drafted task", Purpose: input.Request, Why: "Mission", Priority: domain.PriorityNormal, DefinitionOfDone: []string{"Verified"}}, nil
}
func (s *stubBackend) CreateTask(_ context.Context, input TaskCreate) (domain.Task, error) {
	return domain.Task{ID: "task-1", Title: input.Title}, nil
}
func (s *stubBackend) Task(context.Context, string) (domain.Task, error) {
	return domain.Task{}, ErrNotFound
}
func (s *stubBackend) UpdateTask(context.Context, string, TaskUpdate) (domain.Task, error) {
	return domain.Task{}, ErrNotFound
}
func (s *stubBackend) RunTask(_ context.Context, id string) (domain.Task, error) {
	s.runTask = id
	return domain.Task{ID: id, Status: domain.TaskReady}, nil
}
func (s *stubBackend) RecoverTask(_ context.Context, id string, input TaskRecovery) (domain.Message, error) {
	s.recoveredTask, s.recoveryNote = id, input.Note
	return domain.Message{ID: 42, Role: domain.MessageUser, Status: domain.MessageQueued}, nil
}
func (s *stubBackend) DeleteTask(_ context.Context, id string) error {
	s.deletedTask = id
	return nil
}
func (s *stubBackend) Run(context.Context, string) (domain.Run, error) {
	return domain.Run{}, ErrNotFound
}
func (s *stubBackend) RecentEvents(context.Context, int64, int) ([]domain.Event, error) {
	return []domain.Event{}, nil
}
func (s *stubBackend) Subscribe() (<-chan domain.Event, func()) {
	if s.stream == nil {
		s.stream = make(chan domain.Event)
	}
	return s.stream, func() {}
}

func TestStatusAndSecurityHeaders(t *testing.T) {
	backend := &stubBackend{status: domain.StatusSnapshot{Status: domain.GlobalIdle, DisplayName: "Nabu", Version: "test"}}
	handler := New(backend, testAssets(), nil).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Host = "127.0.0.1:7777"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"idle"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("local UI security headers were not applied")
	}
}

func TestTaskValidationAndCreation(t *testing.T) {
	backend := &stubBackend{}
	handler := New(backend, testAssets(), nil).Handler()

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`{"title":"Only a title"}`))
	invalidRequest.Host = "127.0.0.1:7777"
	invalidRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_task") {
		t.Fatalf("invalid task was accepted: %d %s", invalid.Code, invalid.Body.String())
	}

	valid := httptest.NewRecorder()
	body := `{"title":"Inspect the site","purpose":"Find broken links","priority":"high","definition_of_done":["Report checked URLs"]}`
	validRequest := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	validRequest.Host = "127.0.0.1:7777"
	validRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(valid, validRequest)
	if valid.Code != http.StatusCreated || !strings.Contains(valid.Body.String(), `"id":"task-1"`) {
		t.Fatalf("valid task was not created: %d %s", valid.Code, valid.Body.String())
	}
}

func TestDeleteTask(t *testing.T) {
	backend := &stubBackend{}
	handler := New(backend, testAssets(), nil).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/tasks/task-1", nil)
	request.Host = "127.0.0.1:7777"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || backend.deletedTask != "task-1" {
		t.Fatalf("task was not deleted: status=%d id=%q body=%s", response.Code, backend.deletedTask, response.Body.String())
	}
}

func TestRunTaskQueuesItImmediately(t *testing.T) {
	backend := &stubBackend{}
	handler := New(backend, testAssets(), nil).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tasks/task-1/run", nil)
	request.Host = "127.0.0.1:7777"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || backend.runTask != "task-1" || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("task was not queued: status=%d id=%q body=%s", response.Code, backend.runTask, response.Body.String())
	}
}

func TestRecoverTaskQueuesContextualChatTurn(t *testing.T) {
	backend := &stubBackend{}
	handler := New(backend, testAssets(), nil).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tasks/task-1/recover", strings.NewReader(`{"note":"Railway variables are now configured."}`))
	request.Host = "127.0.0.1:7777"
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || backend.recoveredTask != "task-1" || backend.recoveryNote != "Railway variables are now configured." {
		t.Fatalf("recovery was not queued: status=%d task=%q note=%q body=%s", response.Code, backend.recoveredTask, backend.recoveryNote, response.Body.String())
	}
}

func TestSPAHistoryFallback(t *testing.T) {
	handler := New(&stubBackend{}, testAssets(), nil).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/tasks/task-1", nil)
	request.Host = "localhost:7777"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root">`) {
		t.Fatalf("SPA route did not fall back to index: %d %s", response.Code, response.Body.String())
	}
}

func TestRejectsDNSRebindingAndCrossOriginRequests(t *testing.T) {
	handler := New(&stubBackend{}, testAssets(), nil).Handler()
	for name, values := range map[string][2]string{
		"remote host":  {"attacker.example", ""},
		"cross origin": {"127.0.0.1:7777", "https://attacker.example"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/pause", strings.NewReader(`{"paused":true}`))
			request.Host = values[0]
			if values[1] != "" {
				request.Header.Set("Origin", values[1])
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("unsafe request returned %d", response.Code)
			}
		})
	}
}

func TestAllowsSameOriginPrivateTailscaleRequests(t *testing.T) {
	handler := New(&stubBackend{status: domain.StatusSnapshot{Status: domain.GlobalIdle}}, testAssets(), nil).Handler()
	request := httptest.NewRequest(http.MethodGet, "https://nabu.example.ts.net/api/status", nil)
	request.Host = "nabu.example.ts.net"
	request.Header.Set("Origin", "https://nabu.example.ts.net")
	request.Header.Set("Tailscale-User-Login", "operator@example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("private Tailscale request returned %d: %s", response.Code, response.Body.String())
	}
}

func TestRejectsCrossTailnetOrigin(t *testing.T) {
	handler := New(&stubBackend{}, testAssets(), nil).Handler()
	request := httptest.NewRequest(http.MethodPost, "https://nabu.example.ts.net/api/pause", strings.NewReader(`{"paused":true}`))
	request.Host = "nabu.example.ts.net"
	request.Header.Set("Origin", "https://attacker.other.ts.net")
	request.Header.Set("Tailscale-User-Login", "operator@example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-tailnet request returned %d", response.Code)
	}
}

func TestRejectsPublicTailscaleFunnelRequests(t *testing.T) {
	handler := New(&stubBackend{}, testAssets(), nil).Handler()
	request := httptest.NewRequest(http.MethodGet, "https://nabu.example.ts.net/api/status", nil)
	request.Host = "nabu.example.ts.net"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("Tailscale request without private identity returned %d", response.Code)
	}
}

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(`<!doctype html><div id="root"></div>`)},
		"assets/app.js": &fstest.MapFile{Data: []byte(`console.log("Nabu")`)},
	}
}
