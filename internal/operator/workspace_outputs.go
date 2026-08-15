package operator

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

const maximumWorkspaceOutputs = 200

var _ api.WorkspaceOutputsBackend = (*Operator)(nil)

// WorkspaceOutputs projects durable task/script artifacts into the active
// workspace without exposing arbitrary filesystem paths. Missing files are
// omitted; their historical run evidence remains available on the task.
func (o *Operator) WorkspaceOutputs(ctx context.Context) (api.WorkspaceOutputs, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return api.WorkspaceOutputs{}, translateNotFound(err)
	}
	tasks, err := o.store.ListTasks(ctx, store.TaskFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return api.WorkspaceOutputs{}, err
	}
	items := make([]api.WorkspaceOutput, 0)
	for _, task := range tasks {
		artifacts, artifactErr := o.store.ListArtifacts(ctx, task.ID, "")
		if artifactErr != nil {
			return api.WorkspaceOutputs{}, artifactErr
		}
		for _, artifact := range artifacts {
			if item, ok := o.workspaceOutputFromArtifact(ctx, artifact, task.ID, task.Title); ok {
				items = append(items, item)
			}
		}
	}

	scripts, err := o.store.ListScripts(ctx, store.ScriptFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return api.WorkspaceOutputs{}, err
	}
	for index := range scripts {
		runs, runErr := o.store.ListScriptRuns(ctx, store.ScriptRunFilter{ScriptID: scripts[index].ID, Limit: 1})
		if runErr != nil {
			return api.WorkspaceOutputs{}, runErr
		}
		if len(runs) == 0 {
			continue
		}
		artifacts, artifactErr := o.store.ListScriptRunArtifacts(ctx, runs[0].ID)
		if artifactErr != nil {
			return api.WorkspaceOutputs{}, artifactErr
		}
		for _, artifact := range artifacts {
			if item, ok := o.workspaceOutputFromArtifact(ctx, artifact, "", scripts[index].Name); ok {
				items = append(items, item)
			}
		}
	}

	sort.SliceStable(items, func(left, right int) bool {
		return items[left].CreatedAt.After(items[right].CreatedAt)
	})
	seen := make(map[string]struct{}, len(items))
	unique := items[:0]
	for _, item := range items {
		key := workspaceOutputKey(item)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, item)
	}
	items = unique
	if len(items) > maximumWorkspaceOutputs {
		items = items[:maximumWorkspaceOutputs]
	}
	if items == nil {
		items = []api.WorkspaceOutput{}
	}
	if scripts == nil {
		scripts = []domain.Script{}
	}
	return api.WorkspaceOutputs{Items: items, Scripts: scripts}, nil
}

func (o *Operator) workspaceOutputFromArtifact(ctx context.Context, artifact domain.Artifact, taskID, taskTitle string) (api.WorkspaceOutput, bool) {
	item := api.WorkspaceOutput{
		ID: artifact.ID, Kind: strings.TrimSpace(artifact.Kind), Name: strings.TrimSpace(artifact.Name),
		TaskID: taskID, TaskTitle: strings.TrimSpace(taskTitle), ScriptRunID: artifact.ScriptRunID, CreatedAt: artifact.CreatedAt,
	}
	if item.Name == "" {
		item.Name = "Workspace output"
	}
	if candidate := strings.TrimSpace(artifact.URL); candidate != "" {
		parsed, err := url.Parse(candidate)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil {
			item.URL = parsed.String()
		}
	}
	if strings.TrimSpace(artifact.Path) != "" {
		file, err := o.WorkspaceFile(ctx, artifact.Path, false)
		if err == nil {
			item.Path, item.FileKind, item.MIMEType = file.Path, file.Kind, file.MIMEType
			item.Size, item.Editable = file.Size, file.Editable
		} else if !errors.Is(err, api.ErrNotFound) && !errors.Is(err, api.ErrUnavailable) {
			o.logger.Debug("skip unavailable workspace output", "artifact_id", artifact.ID, "error", err)
		}
	}
	return item, item.Path != "" || item.URL != ""
}

func workspaceOutputKey(item api.WorkspaceOutput) string {
	if item.Path != "" {
		return "path:" + strings.ToLower(item.Path)
	}
	if item.URL != "" {
		return "url:" + strings.ToLower(item.URL)
	}
	return "id:" + item.ID
}
