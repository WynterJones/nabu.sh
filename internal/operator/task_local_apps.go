package operator

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

// applyTaskLocalApps registers only validated app definitions emitted by a
// completed task. It is idempotent for exact replays and never evaluates a
// shell expression.
func (o *Operator) applyTaskLocalApps(ctx context.Context, task domain.Task, result *domain.RunResult) error {
	if result == nil || len(result.LocalApps) == 0 {
		return nil
	}
	workspace, err := o.store.GetWorkspace(ctx, task.WorkspaceID)
	if err != nil || !workspace.Allowed {
		return fmt.Errorf("approved task workspace is unavailable")
	}
	existing, err := o.store.ListLocalApps(ctx, store.LocalAppFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return err
	}
	for index := range result.LocalApps {
		registration := &result.LocalApps[index]
		directory, err := normalizeLocalAppDirectory(workspace.Path, registration.Directory)
		if err != nil {
			return fmt.Errorf("register local app %q: %w", registration.Name, err)
		}
		command, err := normalizeLocalAppCommand(registration.Command)
		if err != nil {
			return fmt.Errorf("register local app %q: %w", registration.Name, err)
		}
		healthPath, err := normalizeHealthPath(registration.HealthPath)
		if err != nil {
			return fmt.Errorf("register local app %q: %w", registration.Name, err)
		}
		var matched *domain.LocalApp
		for appIndex := range existing {
			app := &existing[appIndex]
			if strings.EqualFold(app.Name, registration.Name) || app.Directory == directory || app.Port == registration.Port {
				matched = app
				break
			}
		}
		if matched != nil {
			if !strings.EqualFold(matched.Name, registration.Name) || matched.Directory != directory || matched.Port != registration.Port || !slices.Equal(matched.Command, command) || matched.HealthPath != healthPath {
				return fmt.Errorf("local app %q conflicts with existing registration %q", registration.Name, matched.Name)
			}
			registration.ID, registration.Applied = matched.ID, true
			continue
		}
		created, err := o.store.CreateLocalApp(ctx, domain.LocalApp{
			WorkspaceID: workspace.ID,
			Name:        strings.TrimSpace(registration.Name),
			Description: strings.TrimSpace(registration.Description),
			Directory:   directory,
			Command:     command,
			Port:        registration.Port,
			HealthPath:  healthPath,
			AutoStart:   registration.AutoStart,
		})
		if err != nil {
			return fmt.Errorf("register local app %q: %w", registration.Name, err)
		}
		existing = append(existing, created)
		registration.ID, registration.Applied = created.ID, true
		o.emitForWorkspace(ctx, workspace.ID, "app.created", created.ID, created)
		if registration.AutoStart {
			directoryPath, resolveErr := resolveStoredLocalAppDirectory(workspace.Path, created.Directory)
			if resolveErr != nil {
				return resolveErr
			}
			if o.appRuntime == nil {
				return fmt.Errorf("local application runtime is unavailable")
			}
			if _, startErr := o.appRuntime.Start(created, directoryPath); startErr != nil {
				return fmt.Errorf("start local app %q: %w", created.Name, startErr)
			}
			o.emitForWorkspace(ctx, workspace.ID, "app.started", created.ID, map[string]any{"url": localAppURL(created), "port": created.Port})
		}
	}
	return nil
}
