package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

type localAppsStubBackend struct {
	*stubBackend
	received LocalAppInput
	action   string
}

func (s *localAppsStubBackend) LocalApps(context.Context) ([]LocalAppView, error) {
	return []LocalAppView{{LocalApp: domain.LocalApp{ID: "app-1", Name: "Toolbox", Directory: "repos/toolbox", Command: []string{"npm", "run", "dev"}, Port: 4173}, Status: "running", URL: "http://127.0.0.1:4173", Healthy: true}}, nil
}
func (s *localAppsStubBackend) LocalApp(context.Context, string) (LocalAppView, error) {
	return LocalAppView{LocalApp: domain.LocalApp{ID: "app-1", Name: "Toolbox"}, Status: "stopped"}, nil
}
func (s *localAppsStubBackend) CreateLocalApp(_ context.Context, input LocalAppInput) (LocalAppView, error) {
	s.received = input
	return LocalAppView{LocalApp: domain.LocalApp{ID: "app-1", Name: input.Name, Directory: input.Directory, Command: input.Command, Port: input.Port}, Status: "stopped"}, nil
}
func (s *localAppsStubBackend) UpdateLocalApp(context.Context, string, LocalAppUpdate) (LocalAppView, error) {
	return LocalAppView{LocalApp: domain.LocalApp{ID: "app-1", Name: "Toolbox"}, Status: "stopped"}, nil
}
func (s *localAppsStubBackend) DeleteLocalApp(context.Context, string) error { return nil }
func (s *localAppsStubBackend) StartLocalApp(context.Context, string) (LocalAppView, error) {
	s.action = "start"
	return LocalAppView{LocalApp: domain.LocalApp{ID: "app-1", Name: "Toolbox"}, Status: "running"}, nil
}
func (s *localAppsStubBackend) StopLocalApp(context.Context, string) (LocalAppView, error) {
	s.action = "stop"
	return LocalAppView{LocalApp: domain.LocalApp{ID: "app-1", Name: "Toolbox"}, Status: "stopped"}, nil
}
func (s *localAppsStubBackend) RestartLocalApp(context.Context, string) (LocalAppView, error) {
	s.action = "restart"
	return LocalAppView{LocalApp: domain.LocalApp{ID: "app-1", Name: "Toolbox"}, Status: "running"}, nil
}
func (s *localAppsStubBackend) LocalAppLogs(context.Context, string) (LocalAppLogs, error) {
	return LocalAppLogs{AppID: "app-1", Content: "ready on 4173"}, nil
}

func TestLocalAppRoutesPreserveDirectArgvAndRuntimeActions(t *testing.T) {
	backend := &localAppsStubBackend{stubBackend: &stubBackend{}}
	handler := New(backend, testAssets(), nil).Handler()
	create := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/apps", strings.NewReader(`{"name":"Toolbox","directory":"repos/toolbox","command":["npm","run","dev","--","--port","4173"],"port":4173,"health_path":"/","auto_start":false}`))
	request.Host = "127.0.0.1:7777"
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(create, request)
	if create.Code != http.StatusCreated || len(backend.received.Command) != 6 || backend.received.Command[0] != "npm" {
		t.Fatalf("create = %d %s input=%#v", create.Code, create.Body.String(), backend.received)
	}

	started := httptest.NewRecorder()
	startRequest := httptest.NewRequest(http.MethodPost, "/api/apps/app-1/start", nil)
	startRequest.Host = "127.0.0.1:7777"
	handler.ServeHTTP(started, startRequest)
	if started.Code != http.StatusOK || backend.action != "start" || !strings.Contains(started.Body.String(), `"status":"running"`) {
		t.Fatalf("start = %d %s action=%q", started.Code, started.Body.String(), backend.action)
	}

	logs := httptest.NewRecorder()
	logsRequest := httptest.NewRequest(http.MethodGet, "/api/apps/app-1/logs", nil)
	logsRequest.Host = "127.0.0.1:7777"
	handler.ServeHTTP(logs, logsRequest)
	if logs.Code != http.StatusOK || !strings.Contains(logs.Body.String(), "ready on 4173") {
		t.Fatalf("logs = %d %s", logs.Code, logs.Body.String())
	}
}
