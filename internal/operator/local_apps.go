package operator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/appruntime"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

var _ api.LocalAppsBackend = (*Operator)(nil)

func (o *Operator) LocalApps(ctx context.Context) ([]api.LocalAppView, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return nil, translateNotFound(err)
	}
	apps, err := o.store.ListLocalApps(ctx, store.LocalAppFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return nil, err
	}
	views := make([]api.LocalAppView, 0, len(apps))
	for _, app := range apps {
		views = append(views, o.localAppView(ctx, app))
	}
	return views, nil
}

func (o *Operator) LocalApp(ctx context.Context, id string) (api.LocalAppView, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	app, err := o.store.GetLocalAppForWorkspace(ctx, workspace.ID, id)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	return o.localAppView(ctx, app), nil
}

func (o *Operator) CreateLocalApp(ctx context.Context, input api.LocalAppInput) (api.LocalAppView, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	return o.createLocalAppForWorkspace(ctx, workspace, input)
}

func (o *Operator) createLocalAppForWorkspace(ctx context.Context, workspace domain.Workspace, input api.LocalAppInput) (api.LocalAppView, error) {
	directory, err := normalizeLocalAppDirectory(workspace.Path, input.Directory)
	if err != nil {
		return api.LocalAppView{}, err
	}
	command, err := normalizeLocalAppCommand(input.Command)
	if err != nil {
		return api.LocalAppView{}, err
	}
	healthPath, err := normalizeHealthPath(input.HealthPath)
	if err != nil {
		return api.LocalAppView{}, err
	}
	created, err := o.store.CreateLocalApp(ctx, domain.LocalApp{
		WorkspaceID: workspace.ID, Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description),
		Directory: directory, Command: command, Port: input.Port, HealthPath: healthPath, AutoStart: input.AutoStart,
	})
	if err != nil {
		return api.LocalAppView{}, translateLocalAppStoreError(err)
	}
	o.emitForWorkspace(ctx, workspace.ID, "app.created", created.ID, created)
	return o.localAppView(ctx, created), nil
}

func (o *Operator) UpdateLocalApp(ctx context.Context, id string, input api.LocalAppUpdate) (api.LocalAppView, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	app, err := o.store.GetLocalAppForWorkspace(ctx, workspace.ID, id)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	if o.appRuntime != nil && o.appRuntime.State(app.ID).Status == appruntime.StatusRunning {
		return api.LocalAppView{}, fmt.Errorf("%w: stop the application before editing its runtime settings", api.ErrConflict)
	}
	if input.Name != nil {
		app.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		app.Description = strings.TrimSpace(*input.Description)
	}
	if input.Directory != nil {
		app.Directory, err = normalizeLocalAppDirectory(workspace.Path, *input.Directory)
		if err != nil {
			return api.LocalAppView{}, err
		}
	}
	if input.Command != nil {
		app.Command, err = normalizeLocalAppCommand(*input.Command)
		if err != nil {
			return api.LocalAppView{}, err
		}
	}
	if input.Port != nil {
		app.Port = *input.Port
	}
	if input.HealthPath != nil {
		app.HealthPath, err = normalizeHealthPath(*input.HealthPath)
		if err != nil {
			return api.LocalAppView{}, err
		}
	}
	if input.AutoStart != nil {
		app.AutoStart = *input.AutoStart
	}
	if strings.TrimSpace(app.Name) == "" {
		return api.LocalAppView{}, fmt.Errorf("%w: application name is required", api.ErrInvalid)
	}
	if err := o.store.UpdateLocalApp(ctx, app); err != nil {
		return api.LocalAppView{}, translateLocalAppStoreError(err)
	}
	o.emitForWorkspace(ctx, workspace.ID, "app.updated", app.ID, app)
	return o.localAppView(ctx, app), nil
}

func (o *Operator) DeleteLocalApp(ctx context.Context, id string) error {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return translateNotFound(err)
	}
	app, err := o.store.GetLocalAppForWorkspace(ctx, workspace.ID, id)
	if err != nil {
		return translateNotFound(err)
	}
	if o.appRuntime != nil && o.appRuntime.State(app.ID).Status == appruntime.StatusRunning {
		stopContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := o.appRuntime.Stop(stopContext, app.ID); err != nil {
			return fmt.Errorf("%w: stop the application before deleting it", api.ErrConflict)
		}
	}
	if err := o.store.DeleteLocalAppForWorkspace(ctx, workspace.ID, app.ID); err != nil {
		return translateNotFound(err)
	}
	o.emitForWorkspace(ctx, workspace.ID, "app.deleted", app.ID, nil)
	return nil
}

func (o *Operator) StartLocalApp(ctx context.Context, id string) (api.LocalAppView, error) {
	workspace, app, err := o.activeLocalApp(ctx, id)
	if err != nil {
		return api.LocalAppView{}, err
	}
	return o.startLocalAppRecord(ctx, workspace, app)
}

func (o *Operator) startLocalAppForWorkspace(ctx context.Context, workspaceID, id string) (api.LocalAppView, error) {
	workspace, err := o.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	app, err := o.store.GetLocalAppForWorkspace(ctx, workspaceID, id)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	return o.startLocalAppRecord(ctx, workspace, app)
}

func (o *Operator) startLocalAppRecord(ctx context.Context, workspace domain.Workspace, app domain.LocalApp) (api.LocalAppView, error) {
	if o.appRuntime == nil {
		return api.LocalAppView{}, fmt.Errorf("%w: local application runtime is unavailable", api.ErrUnavailable)
	}
	directory, err := resolveStoredLocalAppDirectory(workspace.Path, app.Directory)
	if err != nil {
		return api.LocalAppView{}, err
	}
	if _, err := o.appRuntime.Start(app, directory); err != nil {
		if strings.Contains(err.Error(), "already running") {
			return api.LocalAppView{}, fmt.Errorf("%w: %s", api.ErrConflict, strings.TrimPrefix(err.Error(), "app runtime: "))
		}
		return api.LocalAppView{}, fmt.Errorf("%w: %s", api.ErrUnavailable, strings.TrimPrefix(err.Error(), "app runtime: "))
	}
	o.emitForWorkspace(ctx, workspace.ID, "app.started", app.ID, map[string]any{"url": localAppURL(app), "port": app.Port})
	return o.localAppView(ctx, app), nil
}

func (o *Operator) StopLocalApp(ctx context.Context, id string) (api.LocalAppView, error) {
	workspace, app, err := o.activeLocalApp(ctx, id)
	if err != nil {
		return api.LocalAppView{}, err
	}
	return o.stopLocalAppRecord(ctx, workspace, app)
}

func (o *Operator) stopLocalAppForWorkspace(ctx context.Context, workspaceID, id string) (api.LocalAppView, error) {
	workspace, err := o.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	app, err := o.store.GetLocalAppForWorkspace(ctx, workspaceID, id)
	if err != nil {
		return api.LocalAppView{}, translateNotFound(err)
	}
	return o.stopLocalAppRecord(ctx, workspace, app)
}

func (o *Operator) stopLocalAppRecord(ctx context.Context, workspace domain.Workspace, app domain.LocalApp) (api.LocalAppView, error) {
	if o.appRuntime == nil {
		return api.LocalAppView{}, fmt.Errorf("%w: local application runtime is unavailable", api.ErrUnavailable)
	}
	stopContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := o.appRuntime.Stop(stopContext, app.ID); err != nil {
		return api.LocalAppView{}, fmt.Errorf("%w: %s", api.ErrUnavailable, strings.TrimPrefix(err.Error(), "app runtime: "))
	}
	o.emitForWorkspace(ctx, workspace.ID, "app.stopped", app.ID, nil)
	return o.localAppView(ctx, app), nil
}

func (o *Operator) RestartLocalApp(ctx context.Context, id string) (api.LocalAppView, error) {
	workspace, app, err := o.activeLocalApp(ctx, id)
	if err != nil {
		return api.LocalAppView{}, err
	}
	if _, err := o.stopLocalAppRecord(ctx, workspace, app); err != nil {
		return api.LocalAppView{}, err
	}
	return o.startLocalAppRecord(ctx, workspace, app)
}

func (o *Operator) LocalAppLogs(ctx context.Context, id string) (api.LocalAppLogs, error) {
	_, app, err := o.activeLocalApp(ctx, id)
	if err != nil {
		return api.LocalAppLogs{}, err
	}
	if o.appRuntime == nil {
		return api.LocalAppLogs{}, fmt.Errorf("%w: local application runtime is unavailable", api.ErrUnavailable)
	}
	content, err := o.appRuntime.Logs(app.ID)
	if err != nil {
		return api.LocalAppLogs{}, err
	}
	return api.LocalAppLogs{AppID: app.ID, Content: content}, nil
}

func (o *Operator) activeLocalApp(ctx context.Context, id string) (domain.Workspace, domain.LocalApp, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return domain.Workspace{}, domain.LocalApp{}, translateNotFound(err)
	}
	app, err := o.store.GetLocalAppForWorkspace(ctx, workspace.ID, id)
	if err != nil {
		return domain.Workspace{}, domain.LocalApp{}, translateNotFound(err)
	}
	return workspace, app, nil
}

func (o *Operator) localAppView(ctx context.Context, app domain.LocalApp) api.LocalAppView {
	state := appruntime.State{AppID: app.ID, Status: appruntime.StatusStopped}
	if o.appRuntime != nil {
		state = o.appRuntime.State(app.ID)
	}
	view := api.LocalAppView{LocalApp: app, Status: string(state.Status), PID: state.PID, URL: localAppURL(app),
		StartedAt: state.StartedAt, StoppedAt: state.StoppedAt, ExitCode: state.ExitCode, Error: state.Error}
	if state.Status == appruntime.StatusRunning {
		view.Healthy = localAppHealthy(ctx, view.URL, app.HealthPath)
	}
	return view
}

func (o *Operator) startAutoApps(ctx context.Context) {
	apps, err := o.LocalApps(ctx)
	if err != nil {
		return
	}
	for _, view := range apps {
		if !view.AutoStart {
			continue
		}
		if _, err := o.StartLocalApp(ctx, view.ID); err != nil {
			o.logger.Warn("auto-start local app failed", "app_id", view.ID, "error", err)
		}
	}
}

func normalizeLocalAppDirectory(workspaceRoot, value string) (string, error) {
	resolved, err := resolveStoredLocalAppDirectory(workspaceRoot, value)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("%w: workspace folder is unavailable", api.ErrUnavailable)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: application folder must be inside the active workspace", api.ErrInvalid)
	}
	relative = filepath.ToSlash(relative)
	if !strings.HasPrefix(relative, "repos/") || relative == "repos/" {
		return "", fmt.Errorf("%w: application folder must be inside repos/<app-folder>", api.ErrInvalid)
	}
	return relative, nil
}

func resolveStoredLocalAppDirectory(workspaceRoot, value string) (string, error) {
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("%w: workspace folder is unavailable", api.ErrUnavailable)
	}
	requested := strings.TrimSpace(value)
	if requested == "" || strings.ContainsRune(requested, '\x00') {
		return "", fmt.Errorf("%w: application folder is required", api.ErrInvalid)
	}
	candidate := filepath.Clean(requested)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: application folder does not exist", api.ErrInvalid)
	}
	info, err := filepath.Rel(root, resolved)
	if err != nil || info == ".." || strings.HasPrefix(info, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: application folder must be inside the active workspace", api.ErrInvalid)
	}
	return resolved, nil
}

func normalizeLocalAppCommand(command []string) ([]string, error) {
	if len(command) == 0 || len(command) > 32 {
		return nil, fmt.Errorf("%w: start command must contain between 1 and 32 arguments", api.ErrInvalid)
	}
	result := make([]string, len(command))
	for index, argument := range command {
		if strings.ContainsRune(argument, '\x00') || len(argument) > 4096 {
			return nil, fmt.Errorf("%w: start command contains an invalid argument", api.ErrInvalid)
		}
		result[index] = strings.TrimSpace(argument)
	}
	if result[0] == "" {
		return nil, fmt.Errorf("%w: start executable is required", api.ErrInvalid)
	}
	if localAppShellExecutable(result[0]) {
		return nil, fmt.Errorf("%w: shell wrappers are not supported; run an executable or checked-in script directly", api.ErrInvalid)
	}
	return result, nil
}

func localAppShellExecutable(value string) bool {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(value))) {
	case "sh", "bash", "zsh", "fish", "dash", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	default:
		return false
	}
}

func normalizeHealthPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", fmt.Errorf("%w: health path must be a path such as / or /health", api.ErrInvalid)
	}
	return path.Clean(parsed.Path), nil
}

func localAppURL(app domain.LocalApp) string {
	return fmt.Sprintf("http://127.0.0.1:%d", app.Port)
}

func localAppHealthy(ctx context.Context, baseURL, healthPath string) bool {
	requestContext, cancel := context.WithTimeout(ctx, 450*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, strings.TrimRight(baseURL, "/")+healthPath, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 450 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 500
}

func translateLocalAppStoreError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return api.ErrNotFound
	}
	value := strings.ToLower(err.Error())
	if strings.Contains(value, "unique constraint") {
		return fmt.Errorf("%w: an application with this name or port already exists in the workspace", api.ErrConflict)
	}
	if strings.Contains(value, "required") || strings.Contains(value, "port") || strings.Contains(value, "argument") {
		return fmt.Errorf("%w: %s", api.ErrInvalid, strings.TrimPrefix(err.Error(), "store: "))
	}
	return err
}
