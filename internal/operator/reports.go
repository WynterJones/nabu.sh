package operator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/store"
)

const maximumReportBodyBytes = 4 * 1024 * 1024

// ReconcileReports promotes report artifacts from previously completed tasks.
// It is safe to run repeatedly: a task/path pair is represented only once.
func (o *Operator) ReconcileReports(ctx context.Context) (int, error) {
	workspaces, err := o.store.ListWorkspaces(ctx)
	if err != nil {
		return 0, err
	}
	created := 0
	var reconcileErrors []error
	for _, workspace := range workspaces {
		tasks, listErr := o.store.ListTasks(ctx, store.TaskFilter{
			WorkspaceID: workspace.ID,
			Statuses:    []domain.TaskStatus{domain.TaskCompleted},
		})
		if listErr != nil {
			reconcileErrors = append(reconcileErrors, listErr)
			continue
		}
		for _, task := range tasks {
			artifacts, artifactErr := o.store.ListArtifacts(ctx, task.ID, "")
			if artifactErr != nil {
				reconcileErrors = append(reconcileErrors, artifactErr)
				continue
			}
			count, promoteErr := o.promoteTaskReports(ctx, task, artifacts)
			created += count
			if promoteErr != nil {
				reconcileErrors = append(reconcileErrors, promoteErr)
			}
		}
	}
	return created, errors.Join(reconcileErrors...)
}

func (o *Operator) promoteTaskReports(ctx context.Context, task domain.Task, artifacts []domain.Artifact) (int, error) {
	if task.Workspace == nil || strings.TrimSpace(task.Workspace.Path) == "" {
		return 0, nil
	}
	existing, err := o.store.ListReports(ctx, store.ReportFilter{WorkspaceID: task.WorkspaceID, TaskID: task.ID})
	if err != nil {
		return 0, err
	}
	existingPaths := make(map[string]struct{}, len(existing))
	for _, report := range existing {
		existingPaths[filepath.ToSlash(filepath.Clean(report.Path))] = struct{}{}
	}

	// Artifact creation is append-only. If a retry emitted the same report path,
	// promote the newest artifact and create only one durable report.
	latestByPath := make(map[string]domain.Artifact)
	for _, artifact := range artifacts {
		if !strings.EqualFold(strings.TrimSpace(artifact.Kind), "report") || strings.TrimSpace(artifact.Path) == "" {
			continue
		}
		path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(artifact.Path)))
		latestByPath[path] = artifact
	}

	created := 0
	var promoteErrors []error
	for path, artifact := range latestByPath {
		if _, ok := existingPaths[path]; ok {
			continue
		}
		body, relative, readErr := readReportArtifact(*task.Workspace, artifact.Path)
		if readErr != nil {
			promoteErrors = append(promoteErrors, fmt.Errorf("promote report %q: %w", artifact.Name, readErr))
			continue
		}
		title := strings.TrimSpace(artifact.Name)
		if title == "" {
			title = reportTitleFromTask(task.Title)
		}
		summary := strings.TrimSpace(task.Purpose)
		if task.Result != nil && strings.TrimSpace(task.Result.Summary) != "" {
			summary = strings.TrimSpace(task.Result.Summary)
		}
		report, createErr := o.store.CreateReport(ctx, domain.Report{
			WorkspaceID: task.WorkspaceID,
			Kind:        "report",
			Title:       title,
			Summary:     summary,
			Body:        body,
			Path:        relative,
			RelatedTaskIDs: []string{
				task.ID,
			},
			ArtifactIDs: []string{artifact.ID},
			CreatedAt:   artifact.CreatedAt,
		})
		if createErr != nil {
			promoteErrors = append(promoteErrors, fmt.Errorf("promote report %q: %w", artifact.Name, createErr))
			continue
		}
		created++
		existingPaths[path] = struct{}{}
		o.emitForWorkspace(ctx, task.WorkspaceID, "report.created", report.ID, map[string]any{
			"report_id": report.ID, "task_id": task.ID, "artifact_id": artifact.ID,
		})
	}
	return created, errors.Join(promoteErrors...)
}

func readReportArtifact(workspace domain.Workspace, requestedPath string) (string, string, error) {
	root, err := filepath.EvalSymlinks(workspace.Path)
	if err != nil {
		return "", "", fmt.Errorf("workspace is unavailable: %w", err)
	}
	candidate := filepath.Clean(strings.TrimSpace(requestedPath))
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", fmt.Errorf("report file is unavailable: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("report file is outside its workspace")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("report path is not a regular file")
	}
	if info.Size() > maximumReportBodyBytes {
		return "", "", fmt.Errorf("report exceeds the 4 MB limit")
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", err
	}
	return string(body), filepath.ToSlash(relative), nil
}

func reportTitleFromTask(title string) string {
	value := strings.TrimSpace(title)
	if before, after, ok := strings.Cut(value, ":"); ok && strings.EqualFold(strings.TrimSpace(before), "prepare report") {
		value = strings.TrimSpace(after)
	}
	if value == "" {
		return "Nabu report"
	}
	return value
}
