package operator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/scheduler"
	"github.com/nabu-sh/nabu/internal/store"
)

const (
	maximumModelCatalogBytes = 2 * 1024 * 1024
	maximumCalendarItems     = 2000
	maximumIconDimension     = 4096
)

var _ api.ExtendedProductBackend = (*Operator)(nil)

func (o *Operator) Calendar(ctx context.Context, from, to time.Time) ([]api.CalendarItem, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return nil, translateNotFound(err)
	}
	tasks, err := o.store.ListTasks(ctx, store.TaskFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return nil, err
	}
	items := make([]api.CalendarItem, 0)
	taskTitles := make(map[string]string, len(tasks))
	activeTaskIDs := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		taskTitles[task.ID] = task.Title
		activeTaskIDs[task.ID] = struct{}{}
		when := task.PlannedAt
		if task.CompletedAt != nil {
			when = task.CompletedAt
		} else if task.StartedAt != nil {
			when = task.StartedAt
		}
		if when != nil && inCalendarRange(*when, from, to) {
			items = append(items, api.CalendarItem{ID: task.ID, Kind: "task", Title: task.Title, Status: string(task.Status), StartsAt: when.UTC(), Href: "/tasks/" + task.ID})
		}
	}
	runs, err := o.store.ListRuns(ctx, store.RunFilter{Limit: maximumCalendarItems})
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if _, ok := activeTaskIDs[run.TaskID]; !ok || !inCalendarRange(run.StartedAt, from, to) {
			continue
		}
		items = append(items, api.CalendarItem{ID: run.ID, Kind: "run", Title: "Run · " + taskTitles[run.TaskID], Status: string(run.Status), StartsAt: run.StartedAt.UTC(), EndedAt: run.EndedAt, Href: "/runs/" + run.ID})
	}
	schedules, err := o.store.ListSchedules(ctx, store.ScheduleFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return nil, err
	}
	for _, schedule := range schedules {
		if schedule.LastRunAt != nil && inCalendarRange(*schedule.LastRunAt, from, to) {
			status := "completed"
			if schedule.LastError != "" {
				status = "failed"
			}
			items = append(items, api.CalendarItem{ID: schedule.ID + "-last", Kind: "schedule", Title: schedule.Name, Status: status, StartsAt: schedule.LastRunAt.UTC(), Recurring: true, Href: "/settings/schedules"})
		}
		if schedule.Enabled {
			var occurrence time.Time
			if schedule.NextRunAt != nil {
				occurrence = schedule.NextRunAt.UTC()
			} else {
				occurrence, _ = scheduler.Next(schedule, from)
			}
			for index := 0; !occurrence.IsZero() && index < maximumCalendarItems && len(items) < maximumCalendarItems; index++ {
				if !occurrence.Before(to) {
					break
				}
				if !occurrence.Before(from) {
					items = append(items, api.CalendarItem{ID: fmt.Sprintf("%s-next-%d", schedule.ID, occurrence.Unix()), Kind: "schedule", Title: schedule.Name, Status: "planned", StartsAt: occurrence, Recurring: true, Href: "/settings/schedules"})
				}
				next, nextErr := scheduler.Next(schedule, occurrence)
				if nextErr != nil || !next.After(occurrence) {
					break
				}
				occurrence = next
			}
		}
	}
	plans, err := o.store.ListPlans(ctx, store.PlanFilter{WorkspaceID: workspace.ID, Statuses: []domain.PlanStatus{domain.PlanProposed, domain.PlanActive}, Limit: 50})
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		for _, planItem := range plan.Items {
			if planItem.PlannedAt == nil || !inCalendarRange(*planItem.PlannedAt, from, to) || planItem.Status == domain.PlanItemSkipped {
				continue
			}
			// Once an item is linked to live work, the task or schedule is the
			// authoritative calendar entry and should not be duplicated here.
			if planItem.TaskID != "" || planItem.ScheduleID != "" {
				continue
			}
			status := string(planItem.Status)
			if plan.Status == domain.PlanActive && planItem.Status == domain.PlanItemAccepted {
				status = "planned"
			}
			items = append(items, api.CalendarItem{
				ID: planItem.ID, Kind: "milestone", Title: planItem.Title, Status: status,
				StartsAt: planItem.PlannedAt.UTC(), Href: "/calendar",
			})
		}
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].StartsAt.Equal(items[right].StartsAt) {
			return items[left].ID < items[right].ID
		}
		return items[left].StartsAt.Before(items[right].StartsAt)
	})
	if len(items) > maximumCalendarItems {
		items = items[:maximumCalendarItems]
	}
	return items, nil
}

func inCalendarRange(value, from, to time.Time) bool {
	value = value.UTC()
	return !value.Before(from) && value.Before(to)
}

func (o *Operator) CodexModels(ctx context.Context) (api.CodexModelCatalog, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, o.codexCommand(ctx), "debug", "models")
	var stdout cappedWriter
	stdout.limit = maximumModelCatalogBytes
	var stderr cappedWriter
	stderr.limit = 4096
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err == nil && !stdout.exceeded {
		if models, parseErr := parseCodexModelCatalog(stdout.Bytes()); parseErr == nil && len(models) > 0 {
			return api.CodexModelCatalog{Models: models, Source: "codex"}, nil
		}
	}
	return api.CodexModelCatalog{Models: fallbackCodexModels(), Source: "fallback"}, nil
}

type cappedWriter struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (w *cappedWriter) Write(value []byte) (int, error) {
	original := len(value)
	remaining := w.limit - w.Len()
	if remaining <= 0 {
		w.exceeded = true
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		w.exceeded = true
	}
	_, _ = w.Buffer.Write(value)
	return original, nil
}

func parseCodexModelCatalog(raw []byte) ([]api.CodexModelOption, error) {
	var catalog struct {
		Models []struct {
			Slug                  string `json:"slug"`
			DisplayName           string `json:"display_name"`
			Description           string `json:"description"`
			Visibility            string `json:"visibility"`
			DefaultReasoningLevel string `json:"default_reasoning_level"`
			SupportedLevels       []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("parse Codex model catalog: %w", err)
	}
	models := make([]api.CodexModelOption, 0, len(catalog.Models))
	seen := make(map[string]struct{})
	for _, source := range catalog.Models {
		if source.Visibility != "list" || source.Slug == "" || len(source.Slug) > 100 {
			continue
		}
		if _, exists := seen[source.Slug]; exists {
			continue
		}
		seen[source.Slug] = struct{}{}
		efforts := make([]string, 0, len(source.SupportedLevels))
		for _, level := range source.SupportedLevels {
			if validReasoningEffort(level.Effort) {
				efforts = append(efforts, level.Effort)
			}
		}
		name := strings.TrimSpace(source.DisplayName)
		if name == "" {
			name = source.Slug
		}
		models = append(models, api.CodexModelOption{ID: source.Slug, DisplayName: name, Description: strings.TrimSpace(source.Description), DefaultReasoningEffort: source.DefaultReasoningLevel, SupportedReasoningEfforts: efforts})
		if len(models) == 50 {
			break
		}
	}
	return models, nil
}

func validReasoningEffort(value string) bool {
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return true
	default:
		return false
	}
}

func fallbackCodexModels() []api.CodexModelOption {
	return []api.CodexModelOption{
		{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", Description: "Frontier agentic coding model for complex work.", DefaultReasoningEffort: "low", SupportedReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{ID: "gpt-5.6-terra", DisplayName: "GPT-5.6-Terra", Description: "Balanced agentic coding model for everyday work.", DefaultReasoningEffort: "medium", SupportedReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{ID: "gpt-5.6-luna", DisplayName: "GPT-5.6-Luna", Description: "Fast and efficient agentic coding model.", DefaultReasoningEffort: "medium", SupportedReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}},
	}
}

func (o *Operator) SaveScopeIcon(ctx context.Context, id string, content []byte, contentType string) (domain.Workspace, error) {
	workspace, err := o.store.GetWorkspace(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Workspace{}, translateNotFound(err)
	}
	extension, err := validateWorkspaceIcon(content, contentType)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	scope, err := o.paths.Scope(workspace.ID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if err := os.MkdirAll(scope.Root, 0o700); err != nil {
		return domain.Workspace{}, err
	}
	temporary, err := os.CreateTemp(scope.Root, ".workspace-icon-*")
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("create workspace image: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return domain.Workspace{}, err
	}
	if _, err := io.Copy(temporary, bytes.NewReader(content)); err != nil {
		return domain.Workspace{}, err
	}
	if err := temporary.Sync(); err != nil {
		return domain.Workspace{}, err
	}
	if err := temporary.Close(); err != nil {
		return domain.Workspace{}, err
	}
	digest := sha256Sum(content)
	finalPath := filepath.Join(scope.Root, "workspace-icon-"+hex.EncodeToString(digest[:6])+extension)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return domain.Workspace{}, fmt.Errorf("commit workspace image: %w", err)
	}
	committed = true
	previous := workspace.IconPath
	workspace.IconPath = finalPath
	if err := o.store.UpdateWorkspace(ctx, workspace); err != nil {
		_ = os.Remove(finalPath)
		return domain.Workspace{}, err
	}
	if previous != "" && previous != finalPath && validIconPath(scope.Root, previous) {
		_ = os.Remove(previous)
	}
	workspace.IconURL = scopeIconURL(workspace.ID, content)
	o.emitForWorkspace(ctx, workspace.ID, "workspace.icon.updated", workspace.ID, workspace)
	return workspace, nil
}

func (o *Operator) ScopeIcon(ctx context.Context, id string) (api.ScopeIcon, error) {
	workspace, err := o.store.GetWorkspace(ctx, strings.TrimSpace(id))
	if err != nil || workspace.IconPath == "" {
		return api.ScopeIcon{}, api.ErrNotFound
	}
	scope, err := o.paths.Scope(workspace.ID)
	if err != nil || !validIconPath(scope.Root, workspace.IconPath) {
		return api.ScopeIcon{}, api.ErrNotFound
	}
	content, err := os.ReadFile(workspace.IconPath)
	if err != nil || len(content) == 0 || len(content) > 2*1024*1024 {
		return api.ScopeIcon{}, api.ErrNotFound
	}
	contentType := http.DetectContentType(content)
	if _, err := validateWorkspaceIcon(content, contentType); err != nil {
		return api.ScopeIcon{}, api.ErrNotFound
	}
	digest := sha256.Sum256(content)
	return api.ScopeIcon{Content: content, ContentType: contentType, ETag: `"` + hex.EncodeToString(digest[:12]) + `"`}, nil
}

func (o *Operator) DeleteScopeIcon(ctx context.Context, id string) error {
	workspace, err := o.store.GetWorkspace(ctx, strings.TrimSpace(id))
	if err != nil {
		return translateNotFound(err)
	}
	if workspace.IconPath == "" {
		return api.ErrNotFound
	}
	scope, err := o.paths.Scope(workspace.ID)
	if err != nil || !validIconPath(scope.Root, workspace.IconPath) {
		return api.ErrNotFound
	}
	path := workspace.IconPath
	workspace.IconPath = ""
	if err := o.store.UpdateWorkspace(ctx, workspace); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove workspace image: %w", err)
	}
	o.emitForWorkspace(ctx, workspace.ID, "workspace.icon.deleted", workspace.ID, workspace)
	return nil
}

func validateWorkspaceIcon(content []byte, contentType string) (string, error) {
	if len(content) == 0 || len(content) > 2*1024*1024 {
		return "", errors.New("workspace image must be no larger than 2 MB")
	}
	var width, height int
	var extension string
	switch contentType {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
		width, height = webpDimensions(content)
	default:
		return "", errors.New("workspace image must be PNG, JPEG, or WebP")
	}
	if contentType != "image/webp" {
		config, _, err := image.DecodeConfig(bytes.NewReader(content))
		if err != nil {
			return "", errors.New("workspace image is corrupt")
		}
		width, height = config.Width, config.Height
	}
	if width < 1 || height < 1 || width > maximumIconDimension || height > maximumIconDimension {
		return "", fmt.Errorf("workspace image dimensions must be between 1 and %d pixels", maximumIconDimension)
	}
	return extension, nil
}

func webpDimensions(content []byte) (int, int) {
	if len(content) < 30 || string(content[:4]) != "RIFF" || string(content[8:12]) != "WEBP" {
		return 0, 0
	}
	for offset := 12; offset+8 <= len(content); {
		size := int(binary.LittleEndian.Uint32(content[offset+4 : offset+8]))
		start, end := offset+8, offset+8+size
		if size < 0 || end > len(content) {
			return 0, 0
		}
		switch string(content[offset : offset+4]) {
		case "VP8X":
			if size >= 10 {
				return 1 + int(content[start+4]) + int(content[start+5])<<8 + int(content[start+6])<<16,
					1 + int(content[start+7]) + int(content[start+8])<<8 + int(content[start+9])<<16
			}
		case "VP8L":
			if size >= 5 && content[start] == 0x2f {
				return 1 + int(content[start+1]) + (int(content[start+2])&0x3f)<<8,
					1 + (int(content[start+2]) >> 6) + int(content[start+3])<<2 + (int(content[start+4])&0x0f)<<10
			}
		case "VP8 ":
			if size >= 10 && bytes.Equal(content[start+3:start+6], []byte{0x9d, 0x01, 0x2a}) {
				return int(binary.LittleEndian.Uint16(content[start+6:start+8]) & 0x3fff), int(binary.LittleEndian.Uint16(content[start+8:start+10]) & 0x3fff)
			}
		}
		offset = end + size%2
	}
	return 0, 0
}

func validIconPath(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func scopeIconURL(id string, content []byte) string {
	digest := sha256Sum(content)
	return "/api/scopes/" + id + "/icon?v=" + hex.EncodeToString(digest[:6])
}

func sha256Sum(content []byte) [sha256.Size]byte { return sha256.Sum256(content) }

func (o *Operator) workspaceView(workspace domain.Workspace) domain.Workspace {
	if workspace.IconPath == "" {
		return workspace
	}
	scope, err := o.paths.Scope(workspace.ID)
	if err != nil || !validIconPath(scope.Root, workspace.IconPath) {
		return workspace
	}
	if info, err := os.Stat(workspace.IconPath); err == nil && info.Mode().IsRegular() {
		workspace.IconURL = "/api/scopes/" + workspace.ID + "/icon?v=" + strconv.FormatInt(info.ModTime().UnixNano(), 36)
	}
	return workspace
}
