package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/appruntime"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/scheduler"
	"github.com/nabu-sh/nabu/internal/store"
)

const (
	maximumScopeNameBytes   = 160
	maximumScheduleName     = 160
	maximumScriptName       = 160
	maximumDescription      = 4 * 1024
	maximumMissionBytes     = 16 * 1024
	maximumContextBytes     = 64 * 1024
	maximumSchedulePayload  = 64 * 1024
	minimumScheduleInterval = int64(60)
)

var _ api.ProductBackend = (*Operator)(nil)

func (o *Operator) Scopes(ctx context.Context) ([]domain.Workspace, error) {
	workspaces, err := o.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	active, activeErr := o.store.ActiveWorkspace(ctx)
	if activeErr != nil && !errors.Is(activeErr, store.ErrNotFound) {
		return nil, activeErr
	}
	for index := range workspaces {
		workspaces[index].Active = activeErr == nil && workspaces[index].ID == active.ID
		workspaces[index] = o.workspaceView(workspaces[index])
	}
	if workspaces == nil {
		workspaces = []domain.Workspace{}
	}
	return workspaces, nil
}

func (o *Operator) ActiveScope(ctx context.Context) (domain.Workspace, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return domain.Workspace{}, translateNotFound(err)
	}
	workspace.Active = true
	return o.workspaceView(workspace), nil
}

func (o *Operator) CreateScope(ctx context.Context, input api.ScopeCreate) (domain.Workspace, error) {
	name, err := validateScopeName(input.Name)
	if err != nil {
		return domain.Workspace{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "connect"
	}
	if mode != "create" && mode != "connect" {
		return domain.Workspace{}, fmt.Errorf("%w: scope mode must be create or connect", api.ErrInvalid)
	}
	missionStatement := redactSecrets(strings.TrimSpace(input.Mission))
	businessContext := redactSecrets(strings.TrimSpace(input.Context))
	if len(missionStatement) > maximumMissionBytes || len(businessContext) > maximumContextBytes {
		return domain.Workspace{}, fmt.Errorf("%w: mission or business context is too long", api.ErrInvalid)
	}
	path, err := prepareScopePath(input.Path, mode)
	if err != nil {
		return domain.Workspace{}, err
	}
	if err := o.ensureUniqueScopePath(ctx, path, ""); err != nil {
		return domain.Workspace{}, err
	}
	if missionStatement == "" {
		return domain.Workspace{}, fmt.Errorf("%w: a starting mission is required", api.ErrInvalid)
	}
	if mode == "create" {
		if err := config.EnsureWorkspaceLayout(path); err != nil {
			return domain.Workspace{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
		}
	}
	workspace, err := o.store.CreateWorkspace(ctx, domain.Workspace{
		Name: name, Path: path, DefaultBranch: gitBranch(path), Allowed: true, MissionStarted: true,
	})
	if err != nil {
		return domain.Workspace{}, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = o.store.DeleteWorkspace(context.WithoutCancel(ctx), workspace.ID)
		}
	}()
	scope, err := config.EnsureScope(o.paths, workspace.ID)
	if err != nil {
		return domain.Workspace{}, err
	}
	mission, err := o.store.CreateMission(ctx, domain.Mission{
		WorkspaceID: workspace.ID, Statement: missionStatement,
		Context: businessContext, Active: true,
	})
	if err != nil {
		return domain.Workspace{}, err
	}
	if err := writeContextFile(scope.Mission, "Mission", mission.Statement, mission.Context); err != nil {
		return domain.Workspace{}, err
	}
	if mission.Context != "" {
		if err := writeContextFile(scope.Business, "Business", mission.Context, ""); err != nil {
			return domain.Workspace{}, err
		}
	}
	rollback = false
	active, activeErr := o.store.ActiveWorkspace(ctx)
	workspace.Active = activeErr == nil && active.ID == workspace.ID
	o.emitForWorkspace(ctx, workspace.ID, "workspace.created", workspace.ID, map[string]any{"workspace": workspace, "mode": mode})
	return o.workspaceView(workspace), nil
}

func (o *Operator) UpdateScope(ctx context.Context, id string, input api.ScopeUpdate) (domain.Workspace, error) {
	if input.Name == nil && input.Path == nil {
		return domain.Workspace{}, fmt.Errorf("%w: at least one scope field is required", api.ErrInvalid)
	}
	workspace, err := o.store.GetWorkspace(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Workspace{}, translateNotFound(err)
	}
	if input.Name != nil {
		workspace.Name, err = validateScopeName(*input.Name)
		if err != nil {
			return domain.Workspace{}, err
		}
	}
	if input.Path != nil {
		workspace.Path, err = prepareScopePath(*input.Path, "connect")
		if err != nil {
			return domain.Workspace{}, err
		}
		if err := o.ensureUniqueScopePath(ctx, workspace.Path, workspace.ID); err != nil {
			return domain.Workspace{}, err
		}
		workspace.DefaultBranch = gitBranch(workspace.Path)
	}
	if err := o.store.UpdateWorkspace(ctx, workspace); err != nil {
		return domain.Workspace{}, err
	}
	active, activeErr := o.store.ActiveWorkspace(ctx)
	workspace.Active = activeErr == nil && active.ID == workspace.ID
	o.emitForWorkspace(ctx, workspace.ID, "workspace.updated", workspace.ID, workspace)
	return o.workspaceView(workspace), nil
}

func (o *Operator) DeleteScope(ctx context.Context, id string, input api.ScopeDelete) (api.ScopeDeleteResult, error) {
	id = strings.TrimSpace(id)
	workspace, err := o.store.GetWorkspace(ctx, id)
	if err != nil {
		return api.ScopeDeleteResult{}, translateNotFound(err)
	}
	if strings.TrimSpace(input.Confirmation) != workspace.Name {
		return api.ScopeDeleteResult{}, fmt.Errorf("%w: confirmation must exactly match the workspace name", api.ErrInvalid)
	}
	open, err := o.store.WorkspaceHasOpenWork(ctx, workspace.ID)
	if err != nil {
		return api.ScopeDeleteResult{}, err
	}
	if open || o.workspaceExecutionActive(workspace.ID) {
		return api.ScopeDeleteResult{}, fmt.Errorf("%w: wait for Chat and scripts to finish, and cancel any running tasks before deleting this workspace", api.ErrConflict)
	}
	localApps, err := o.store.ListLocalApps(ctx, store.LocalAppFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return api.ScopeDeleteResult{}, err
	}
	if o.appRuntime != nil {
		for _, app := range localApps {
			if o.appRuntime.State(app.ID).Status == appruntime.StatusRunning {
				return api.ScopeDeleteResult{}, fmt.Errorf("%w: stop local app %q before deleting this workspace", api.ErrConflict, app.Name)
			}
		}
	}

	secrets, err := o.store.ListSecretRecords(ctx, store.SecretRecordFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return api.ScopeDeleteResult{}, err
	}
	integrations, err := o.store.ListIntegrations(ctx, store.IntegrationFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return api.ScopeDeleteResult{}, err
	}
	servers, err := o.store.ListMCPServers(ctx, store.MCPServerFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return api.ScopeDeleteResult{}, err
	}

	o.queueMu.Lock()
	deletion, err := o.store.DeleteWorkspaceData(ctx, workspace.ID)
	o.queueMu.Unlock()
	if err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			return api.ScopeDeleteResult{}, fmt.Errorf("%w: wait for Chat and scripts to finish, and cancel any running tasks before deleting this workspace", api.ErrConflict)
		}
		return api.ScopeDeleteResult{}, translateNotFound(err)
	}
	o.cleanupWorkspaceCredentials(context.WithoutCancel(ctx), secrets, integrations)
	o.cleanupWorkspaceFiles(deletion)
	o.cleanupWorkspaceMCPState(context.WithoutCancel(ctx), servers)

	result := api.ScopeDeleteResult{
		DeletedWorkspaceID: workspace.ID,
		ActiveWorkspaceID:  deletion.ActiveWorkspaceID,
		FolderPreserved:    true,
	}
	o.publishTransient("workspace.deleted", workspace.ID, result)
	o.publishTransient("scope.changed", deletion.ActiveWorkspaceID, map[string]string{
		"workspace_id": deletion.ActiveWorkspaceID,
	})
	o.signal()
	return result, nil
}

func (o *Operator) workspaceExecutionActive(workspaceID string) bool {
	o.mu.Lock()
	runs := make(map[string]activeRun, len(o.activeRuns))
	for runID, run := range o.activeRuns {
		runs[runID] = run
	}
	o.mu.Unlock()
	workspace, err := o.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		return false
	}
	for runID, active := range runs {
		if active.taskID != "" {
			task, taskErr := o.store.GetTask(context.Background(), active.taskID)
			if taskErr == nil && task.WorkspaceID == workspaceID {
				return true
			}
		}
		if run, runErr := o.store.GetRun(context.Background(), runID); runErr == nil && run.WorkingDirectory == workspace.Path {
			return true
		}
	}
	return false
}

func (o *Operator) cleanupWorkspaceCredentials(ctx context.Context, secrets []domain.SecretRecord, integrations []domain.Integration) {
	backend, _, _ := o.integrationDependencies()
	if backend == nil {
		return
	}
	for _, record := range secrets {
		if err := backend.Delete(ctx, secretRef(record)); err != nil && !errors.Is(err, credentials.ErrNotFound) && !errors.Is(err, credentials.ErrUnsupported) {
			o.logger.Warn("workspace secret could not be removed", "workspace_id", record.WorkspaceID, "secret_id", record.ID)
		}
	}
	for _, integration := range integrations {
		for _, requirement := range integration.CredentialRequirements {
			if err := backend.Delete(ctx, credentialRef(integration, requirement.Name)); err != nil && !errors.Is(err, credentials.ErrNotFound) && !errors.Is(err, credentials.ErrUnsupported) {
				o.logger.Warn("workspace integration credential could not be removed", "workspace_id", integration.WorkspaceID, "integration_id", integration.ID)
			}
		}
	}
}

func (o *Operator) cleanupWorkspaceMCPState(ctx context.Context, servers []domain.MCPServer) {
	for _, server := range servers {
		if server.Auth != domain.MCPAuthOAuth {
			continue
		}
		name := mcpConfigName(server)
		config, err := mcpConfigValue(server)
		if err != nil {
			continue
		}
		commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err = exec.CommandContext(commandCtx, o.codexCommand(ctx), "-c", "mcp_servers."+name+"="+config, "mcp", "logout", name).Run()
		cancel()
		if err != nil {
			o.logger.Warn("workspace MCP authorization could not be removed", "workspace_id", server.WorkspaceID, "mcp_server_id", server.ID)
		}
	}
	o.mcpAuthMu.Lock()
	for _, server := range servers {
		delete(o.mcpAuthCache, server.ID)
	}
	o.mcpAuthMu.Unlock()
}

func (o *Operator) cleanupWorkspaceFiles(deletion store.WorkspaceDeletion) {
	if scope, err := o.paths.Scope(deletion.Workspace.ID); err == nil && containedPath(o.paths.Scopes, scope.Root) {
		if err := os.RemoveAll(scope.Root); err != nil {
			o.logger.Warn("workspace context files could not be removed", "workspace_id", deletion.Workspace.ID)
		}
	}
	for _, runID := range deletion.RunIDs {
		path := filepath.Join(o.paths.Runs, runID)
		if containedPath(o.paths.Runs, path) {
			_ = os.RemoveAll(path)
		}
	}
	for _, runID := range deletion.ScriptRunIDs {
		path := filepath.Join(o.paths.Runs, "scripts", runID)
		if containedPath(filepath.Join(o.paths.Runs, "scripts"), path) {
			_ = os.RemoveAll(path)
		}
	}
	for _, path := range deletion.ArtifactPaths {
		if containedPath(o.paths.Artifacts, path) || containedPath(o.paths.Reports, path) || containedPath(o.paths.Runs, path) {
			_ = os.Remove(path)
		}
	}
}

func containedPath(root, candidate string) bool {
	root, rootErr := filepath.Abs(filepath.Clean(root))
	candidate, candidateErr := filepath.Abs(filepath.Clean(candidate))
	if rootErr != nil || candidateErr != nil || candidate == root {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (o *Operator) SetActiveScope(ctx context.Context, id string) (domain.Workspace, error) {
	id = strings.TrimSpace(id)
	workspace, err := o.store.GetWorkspace(ctx, id)
	if err != nil {
		return domain.Workspace{}, translateNotFound(err)
	}
	if !workspace.Allowed {
		return domain.Workspace{}, fmt.Errorf("%w: workspace is not approved", api.ErrInvalid)
	}
	if _, err := config.EnsureScope(o.paths, workspace.ID); err != nil {
		return domain.Workspace{}, err
	}
	if err := o.store.SetActiveWorkspace(ctx, workspace.ID); err != nil {
		return domain.Workspace{}, translateNotFound(err)
	}
	// Context onboarding belongs to each workspace. Create its opening prompt
	// synchronously when the workspace becomes active so it is never delayed by
	// unrelated task workers or the operator's recovery poll.
	if err := o.ensureContextPrompt(ctx, workspace); err != nil {
		return domain.Workspace{}, err
	}
	workspace, err = o.store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		return domain.Workspace{}, translateNotFound(err)
	}
	workspace.Active = true
	o.emitForWorkspace(ctx, workspace.ID, "scope.changed", workspace.ID, workspace)
	o.signal()
	return o.workspaceView(workspace), nil
}

func (o *Operator) Reports(ctx context.Context) ([]domain.Report, error) {
	reports, err := o.store.ListReports(ctx, store.ReportFilter{})
	if reports == nil {
		reports = []domain.Report{}
	}
	return reports, err
}

func (o *Operator) Report(ctx context.Context, id string) (domain.Report, error) {
	report, err := o.store.GetReport(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Report{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, report.WorkspaceID); err != nil {
		return domain.Report{}, err
	}
	return report, nil
}

func (o *Operator) UpdateReportStatus(ctx context.Context, id string, status domain.ReportStatus) (domain.Report, error) {
	report, err := o.Report(ctx, id)
	if err != nil {
		return domain.Report{}, err
	}
	switch status {
	case domain.ReportUnread, domain.ReportRead, domain.ReportArchived:
	default:
		return domain.Report{}, fmt.Errorf("%w: report status must be unread, read, or archived", api.ErrInvalid)
	}
	if err := o.store.UpdateReportStatusForWorkspace(ctx, report.WorkspaceID, report.ID, status); err != nil {
		return domain.Report{}, translateNotFound(err)
	}
	updated, err := o.store.GetReport(ctx, report.ID)
	if err != nil {
		return domain.Report{}, err
	}
	o.emitForWorkspace(ctx, report.WorkspaceID, "report.updated", report.ID, map[string]any{"status": status})
	return updated, nil
}

func (o *Operator) DeleteReport(ctx context.Context, id string) error {
	report, err := o.Report(ctx, id)
	if err != nil {
		return err
	}
	if err := o.store.DeleteReport(ctx, report.ID); err != nil {
		return translateNotFound(err)
	}
	o.emitForWorkspace(ctx, report.WorkspaceID, "report.deleted", report.ID, nil)
	return nil
}

func (o *Operator) Approvals(ctx context.Context, statuses []domain.ApprovalStatus) ([]domain.Approval, error) {
	for _, status := range statuses {
		if !validApprovalStatus(status) {
			return nil, fmt.Errorf("%w: unsupported approval status %q", api.ErrInvalid, status)
		}
	}
	approvals, err := o.store.ListApprovals(ctx, store.ApprovalFilter{Statuses: statuses})
	if approvals == nil {
		approvals = []domain.Approval{}
	}
	return approvals, err
}

func (o *Operator) Approval(ctx context.Context, id string) (domain.Approval, error) {
	approval, err := o.store.GetApproval(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Approval{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, approval.WorkspaceID); err != nil {
		return domain.Approval{}, err
	}
	return approval, nil
}

func (o *Operator) ResolveApproval(ctx context.Context, id string, status domain.ApprovalStatus, rejectionNote string) (domain.Approval, error) {
	if status != domain.ApprovalApproved && status != domain.ApprovalRejected {
		return domain.Approval{}, fmt.Errorf("%w: approval decision must be approved or rejected", api.ErrInvalid)
	}
	current, err := o.Approval(ctx, id)
	if err != nil {
		return domain.Approval{}, err
	}
	if current.Status != domain.ApprovalPending {
		return domain.Approval{}, fmt.Errorf("%w: approval is already %s", api.ErrConflict, current.Status)
	}
	rejectionNote = redactSecrets(strings.TrimSpace(rejectionNote))
	if len(rejectionNote) > maximumDescription {
		return domain.Approval{}, fmt.Errorf("%w: rejection note is too long", api.ErrInvalid)
	}
	approval, err := o.store.ResolveApproval(ctx, current.ID, status, rejectionNote, time.Now().UTC())
	if errors.Is(err, store.ErrInvalidTransition) {
		return domain.Approval{}, fmt.Errorf("%w: approval was resolved by another request", api.ErrConflict)
	}
	if err != nil {
		return domain.Approval{}, translateNotFound(err)
	}
	o.emitForWorkspace(ctx, approval.WorkspaceID, "approval.resolved", approval.ID, approval)
	if status == domain.ApprovalApproved {
		if applyErr := o.applyApprovedDatasetDeletion(ctx, approval); applyErr != nil {
			o.emitForWorkspace(ctx, approval.WorkspaceID, "approval.execution_failed", approval.ID, map[string]string{"error": applyErr.Error()})
			return approval, applyErr
		}
	}
	o.signal()
	return approval, nil
}

func (o *Operator) Policy(ctx context.Context) (domain.Policy, error) {
	return o.store.GetPolicy(ctx)
}

func (o *Operator) UpdatePolicy(ctx context.Context, policy domain.Policy) (domain.Policy, error) {
	if err := validatePolicy(policy); err != nil {
		return domain.Policy{}, err
	}
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return domain.Policy{}, translateNotFound(err)
	}
	if err := o.store.UpdatePolicyForWorkspace(ctx, workspace.ID, policy); err != nil {
		return domain.Policy{}, err
	}
	scope, err := config.EnsureScope(o.paths, workspace.ID)
	if err != nil {
		return domain.Policy{}, err
	}
	if err := writePolicyFile(scope.Policy, policy); err != nil {
		return domain.Policy{}, err
	}
	o.emitForWorkspace(ctx, workspace.ID, "policy.updated", workspace.ID, policy)
	return policy, nil
}

func (o *Operator) Schedules(ctx context.Context) ([]domain.Schedule, error) {
	schedules, err := o.store.ListSchedules(ctx, store.ScheduleFilter{})
	if schedules == nil {
		schedules = []domain.Schedule{}
	}
	return schedules, err
}

func (o *Operator) CreateSchedule(ctx context.Context, input api.ScheduleInput) (domain.Schedule, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return domain.Schedule{}, translateNotFound(err)
	}
	schedule, err := o.scheduleFromInput(ctx, domain.Schedule{WorkspaceID: workspace.ID, Enabled: true}, input, true)
	if err != nil {
		return domain.Schedule{}, err
	}
	created, err := o.store.CreateSchedule(ctx, schedule)
	if err != nil {
		return domain.Schedule{}, err
	}
	o.emitForWorkspace(ctx, workspace.ID, "schedule.created", created.ID, created)
	return created, nil
}

func (o *Operator) UpdateSchedule(ctx context.Context, id string, input api.ScheduleInput) (domain.Schedule, error) {
	schedule, err := o.store.GetSchedule(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Schedule{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, schedule.WorkspaceID); err != nil {
		return domain.Schedule{}, err
	}
	schedule, err = o.scheduleFromInput(ctx, schedule, input, false)
	if err != nil {
		return domain.Schedule{}, err
	}
	if err := o.store.UpdateSchedule(ctx, schedule); err != nil {
		return domain.Schedule{}, err
	}
	updated, err := o.store.GetSchedule(ctx, schedule.ID)
	if err != nil {
		return domain.Schedule{}, err
	}
	o.emitForWorkspace(ctx, schedule.WorkspaceID, "schedule.updated", schedule.ID, updated)
	return updated, nil
}

func (o *Operator) DeleteSchedule(ctx context.Context, id string) error {
	schedule, err := o.store.GetSchedule(ctx, strings.TrimSpace(id))
	if err != nil {
		return translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, schedule.WorkspaceID); err != nil {
		return err
	}
	if err := o.store.DeleteSchedule(ctx, schedule.ID); err != nil {
		return translateNotFound(err)
	}
	o.emitForWorkspace(ctx, schedule.WorkspaceID, "schedule.deleted", schedule.ID, nil)
	return nil
}

func (o *Operator) Scripts(ctx context.Context) ([]domain.Script, error) {
	scripts, err := o.store.ListScripts(ctx, store.ScriptFilter{})
	if scripts == nil {
		scripts = []domain.Script{}
	}
	return scripts, err
}

func (o *Operator) Script(ctx context.Context, id string) (domain.Script, error) {
	script, err := o.store.GetScript(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Script{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, script.WorkspaceID); err != nil {
		return domain.Script{}, err
	}
	return script, nil
}

func (o *Operator) CreateScript(ctx context.Context, input api.ScriptInput) (domain.Script, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return domain.Script{}, translateNotFound(err)
	}
	script, err := o.scriptFromInput(domain.Script{WorkspaceID: workspace.ID, Enabled: true}, input, true)
	if err != nil {
		return domain.Script{}, err
	}
	created, err := o.store.CreateScript(ctx, script)
	if err != nil {
		return domain.Script{}, err
	}
	o.emitForWorkspace(ctx, workspace.ID, "script.created", created.ID, created)
	return created, nil
}

func (o *Operator) UpdateScript(ctx context.Context, id string, input api.ScriptInput) (domain.Script, error) {
	script, err := o.Script(ctx, id)
	if err != nil {
		return domain.Script{}, err
	}
	script, err = o.scriptFromInput(script, input, false)
	if err != nil {
		return domain.Script{}, err
	}
	if err := o.store.UpdateScript(ctx, script); err != nil {
		return domain.Script{}, err
	}
	updated, err := o.store.GetScript(ctx, script.ID)
	if err != nil {
		return domain.Script{}, err
	}
	o.emitForWorkspace(ctx, script.WorkspaceID, "script.updated", script.ID, updated)
	return updated, nil
}

func (o *Operator) DeleteScript(ctx context.Context, id string) error {
	script, err := o.Script(ctx, id)
	if err != nil {
		return err
	}
	if err := o.store.DeleteScript(ctx, script.ID); err != nil {
		return translateNotFound(err)
	}
	o.emitForWorkspace(ctx, script.WorkspaceID, "script.deleted", script.ID, nil)
	return nil
}

func (o *Operator) RunScript(ctx context.Context, id string) (domain.ScriptRun, error) {
	script, err := o.Script(ctx, id)
	if err != nil {
		return domain.ScriptRun{}, err
	}
	if !script.Enabled {
		return domain.ScriptRun{}, fmt.Errorf("%w: script is disabled", api.ErrConflict)
	}
	o.mu.Lock()
	automation := o.automation
	o.mu.Unlock()
	if automation == nil {
		return domain.ScriptRun{}, fmt.Errorf("%w: script automation is not available", api.ErrUnavailable)
	}
	run, err := automation.RunScriptNow(ctx, script.ID)
	if err != nil {
		return run, err
	}
	o.emitForWorkspace(ctx, script.WorkspaceID, "script.completed", run.ID, run)
	return run, nil
}

func (o *Operator) ScriptRuns(ctx context.Context, scriptID string) ([]domain.ScriptRun, error) {
	if scriptID != "" {
		script, err := o.Script(ctx, scriptID)
		if err != nil {
			return nil, err
		}
		runs, err := o.store.ListScriptRuns(ctx, store.ScriptRunFilter{ScriptID: script.ID})
		if runs == nil {
			runs = []domain.ScriptRun{}
		}
		return runs, err
	}
	scripts, err := o.Scripts(ctx)
	if err != nil {
		return nil, err
	}
	var runs []domain.ScriptRun
	for _, script := range scripts {
		items, listErr := o.store.ListScriptRuns(ctx, store.ScriptRunFilter{ScriptID: script.ID})
		if listErr != nil {
			return nil, listErr
		}
		runs = append(runs, items...)
	}
	sort.Slice(runs, func(left, right int) bool { return runs[left].StartedAt.After(runs[right].StartedAt) })
	if runs == nil {
		runs = []domain.ScriptRun{}
	}
	return runs, nil
}

func (o *Operator) ScriptRun(ctx context.Context, id string) (domain.ScriptRun, error) {
	run, err := o.store.GetScriptRun(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.ScriptRun{}, translateNotFound(err)
	}
	if _, err := o.Script(ctx, run.ScriptID); err != nil {
		return domain.ScriptRun{}, err
	}
	return run, nil
}

func (o *Operator) Memory(ctx context.Context) (api.MemoryView, error) {
	scope, err := o.activeScopePaths(ctx)
	if err != nil {
		return api.MemoryView{}, translateNotFound(err)
	}
	content, err := readBounded(scope.MemoryFile, 256*1024)
	if err != nil {
		return api.MemoryView{}, err
	}
	view := api.MemoryView{Content: content}
	if info, statErr := os.Stat(scope.MemoryFile); statErr == nil {
		view.UpdatedAt = info.ModTime().UTC()
	}
	return view, nil
}

func (o *Operator) UpdateMemory(ctx context.Context, content string) (api.MemoryView, error) {
	scope, err := o.activeScopePaths(ctx)
	if err != nil {
		return api.MemoryView{}, translateNotFound(err)
	}
	content = redactSecrets(content)
	if err := config.UpdateScopeMemory(scope, content); err != nil {
		return api.MemoryView{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	workspace, _ := o.store.ActiveWorkspace(ctx)
	o.emitForWorkspace(ctx, workspace.ID, "memory.updated", workspace.ID, map[string]string{"source": "user"})
	return o.Memory(ctx)
}

func (o *Operator) Soul(context.Context) (api.SoulView, error) {
	content, err := readBounded(o.paths.Soul, 256*1024)
	if err != nil {
		return api.SoulView{}, err
	}
	view := api.SoulView{Content: content}
	if info, statErr := os.Stat(o.paths.Soul); statErr == nil {
		view.UpdatedAt = info.ModTime().UTC()
	}
	return view, nil
}

func (o *Operator) UpdateSoul(ctx context.Context, content string) (api.SoulView, error) {
	content = redactSecrets(content)
	if err := config.UpdateSoul(o.paths, content); err != nil {
		return api.SoulView{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	workspace, _ := o.store.ActiveWorkspace(ctx)
	o.emitForWorkspace(ctx, workspace.ID, "soul.updated", workspace.ID, map[string]string{"source": "user"})
	return o.Soul(ctx)
}

func (o *Operator) MemoryUpdates(ctx context.Context) ([]domain.MemoryUpdate, error) {
	updates, err := o.store.ListMemoryUpdates(ctx, store.MemoryUpdateFilter{Statuses: []domain.MemoryUpdateStatus{domain.MemoryProposed}})
	if updates == nil {
		updates = []domain.MemoryUpdate{}
	}
	return updates, err
}

func (o *Operator) ResolveMemoryUpdate(ctx context.Context, id string, status domain.MemoryUpdateStatus, rejectionNote string) (domain.MemoryUpdate, error) {
	if status != domain.MemoryApplied && status != domain.MemoryRejected {
		return domain.MemoryUpdate{}, fmt.Errorf("%w: memory decision must be applied or rejected", api.ErrInvalid)
	}
	update, err := o.store.GetMemoryUpdate(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.MemoryUpdate{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, update.WorkspaceID); err != nil {
		return domain.MemoryUpdate{}, err
	}
	// Resolving a proposal is idempotent for the same decision. This handles
	// retries and stale clients without appending the Markdown projection a
	// second time. A different decision remains a real conflict.
	if update.Status == status {
		return update, nil
	}
	if update.Status != domain.MemoryProposed {
		return domain.MemoryUpdate{}, fmt.Errorf("%w: memory update is already %s", api.ErrConflict, update.Status)
	}
	rejectionNote = redactSecrets(strings.TrimSpace(rejectionNote))
	if len(rejectionNote) > maximumDescription {
		return domain.MemoryUpdate{}, fmt.Errorf("%w: rejection note is too long", api.ErrInvalid)
	}
	resolved, err := o.store.ResolveMemoryUpdate(ctx, update.ID, status, rejectionNote, time.Now().UTC())
	if errors.Is(err, store.ErrInvalidTransition) {
		return domain.MemoryUpdate{}, fmt.Errorf("%w: memory update was resolved by another request", api.ErrConflict)
	}
	if err != nil {
		return domain.MemoryUpdate{}, translateNotFound(err)
	}
	// SQLite is authoritative. Update the Markdown projection only after the
	// pending-only transition succeeds, preventing concurrent requests from
	// appending the same proposal more than once.
	if status == domain.MemoryApplied {
		scope, err := config.EnsureScope(o.paths, update.WorkspaceID)
		if err != nil {
			return resolved, err
		}
		if update.Target == domain.MemoryDaily {
			if _, err := config.AppendScopeDailyMemory(scope, time.Now(), redactSecrets(update.Content)); err != nil {
				return resolved, err
			}
		} else if update.Target == domain.MemorySoul {
			if err := config.AppendSoul(o.paths, redactSecrets(update.Content)); err != nil {
				return resolved, err
			}
		} else if err := config.AppendScopeMemory(scope, redactSecrets(update.Content)); err != nil {
			return resolved, err
		}
	}
	o.emitForWorkspace(ctx, update.WorkspaceID, "memory.proposal.resolved", update.ID, resolved)
	return resolved, nil
}

// RestartService cannot safely restart the process that is synchronously
// serving this request without an injected service controller. The CLI remains
// the authoritative emergency control surface.
func (o *Operator) RestartService(context.Context) error {
	return fmt.Errorf("%w: restart Nabu with `nabu restart`", api.ErrUnavailable)
}

func (o *Operator) scheduleFromInput(ctx context.Context, schedule domain.Schedule, input api.ScheduleInput, creating bool) (domain.Schedule, error) {
	if !creating && input.Name == nil && input.Enabled == nil && input.Kind == nil && input.Expression == nil &&
		input.IntervalSeconds == nil && input.Payload == nil && input.Cadence == nil {
		return domain.Schedule{}, fmt.Errorf("%w: at least one schedule field is required", api.ErrInvalid)
	}
	cadenceFields := 0
	if input.Expression != nil {
		cadenceFields++
	}
	if input.IntervalSeconds != nil {
		cadenceFields++
	}
	if input.Cadence != nil {
		if input.Cadence.Expression == nil && input.Cadence.IntervalSeconds == nil {
			return domain.Schedule{}, fmt.Errorf("%w: cadence must include expression or interval_seconds", api.ErrInvalid)
		}
		if input.Cadence.Expression != nil {
			cadenceFields++
		}
		if input.Cadence.IntervalSeconds != nil {
			cadenceFields++
		}
	}
	if cadenceFields > 1 {
		return domain.Schedule{}, fmt.Errorf("%w: provide exactly one schedule cadence", api.ErrInvalid)
	}
	if input.Name != nil {
		schedule.Name = strings.TrimSpace(*input.Name)
	}
	if input.Enabled != nil {
		schedule.Enabled = *input.Enabled
	}
	if input.Kind != nil {
		schedule.Kind = *input.Kind
	}
	if input.Expression != nil {
		schedule.Expression = strings.TrimSpace(*input.Expression)
		schedule.IntervalSeconds = 0
	}
	if input.IntervalSeconds != nil {
		schedule.IntervalSeconds = *input.IntervalSeconds
		schedule.Expression = ""
	}
	if input.Cadence != nil {
		if input.Cadence.Expression != nil {
			schedule.Expression = strings.TrimSpace(*input.Cadence.Expression)
			schedule.IntervalSeconds = 0
		}
		if input.Cadence.IntervalSeconds != nil {
			schedule.IntervalSeconds = *input.Cadence.IntervalSeconds
			schedule.Expression = ""
		}
	}
	if input.Payload != nil {
		schedule.Payload = append(json.RawMessage(nil), input.Payload...)
	}
	if creating && input.Name == nil {
		return domain.Schedule{}, fmt.Errorf("%w: schedule name is required", api.ErrInvalid)
	}
	if schedule.Name == "" || strings.IndexByte(schedule.Name, 0) >= 0 || len(schedule.Name) > maximumScheduleName {
		return domain.Schedule{}, fmt.Errorf("%w: schedule name is required and must be at most %d bytes", api.ErrInvalid, maximumScheduleName)
	}
	if schedule.Kind != domain.ScheduleScript && schedule.Kind != domain.ScheduleTask && schedule.Kind != domain.ScheduleOrient {
		return domain.Schedule{}, fmt.Errorf("%w: schedule kind must be script, task, or orient", api.ErrInvalid)
	}
	if schedule.IntervalSeconds > 0 && schedule.IntervalSeconds < minimumScheduleInterval {
		return domain.Schedule{}, fmt.Errorf("%w: schedule interval must be at least %d seconds", api.ErrInvalid, minimumScheduleInterval)
	}
	if schedule.IntervalSeconds == 0 && schedule.Expression == "" {
		return domain.Schedule{}, fmt.Errorf("%w: schedule cadence is required", api.ErrInvalid)
	}
	if len(schedule.Payload) > maximumSchedulePayload {
		return domain.Schedule{}, fmt.Errorf("%w: schedule payload is too large", api.ErrInvalid)
	}
	var err error
	schedule.Payload, err = o.normalizeSchedulePayload(ctx, schedule.Kind, schedule.WorkspaceID, schedule.Payload)
	if err != nil {
		return domain.Schedule{}, err
	}
	probe := schedule
	probe.Enabled = true
	next, err := scheduler.Next(probe, time.Now().UTC())
	if err != nil {
		return domain.Schedule{}, fmt.Errorf("%w: invalid cadence: %v", api.ErrInvalid, err)
	}
	schedule.NextRunAt = &next
	schedule.UpdatedAt = time.Now().UTC()
	schedule.Cadence = domain.ScheduleCadence{Expression: schedule.Expression, IntervalSeconds: schedule.IntervalSeconds}
	return schedule, nil
}

func (o *Operator) normalizeSchedulePayload(ctx context.Context, kind domain.ScheduleKind, workspaceID string, raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage(`{}`)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("%w: schedule payload must be a JSON object", api.ErrInvalid)
	}
	target, _ := payload["target"].(string)
	target = strings.TrimSpace(target)
	delete(payload, "target")
	switch kind {
	case domain.ScheduleScript:
		if _, ok := payload["script_id"]; !ok && target != "" {
			payload["script_id"] = target
		}
		identifier, _ := payload["script_id"].(string)
		identifier = strings.TrimSpace(identifier)
		if identifier == "" {
			return nil, fmt.Errorf("%w: script schedule requires a script ID or name", api.ErrInvalid)
		}
		script, err := o.findWorkspaceScript(ctx, workspaceID, identifier)
		if err != nil {
			return nil, err
		}
		payload["script_id"] = script.ID
	case domain.ScheduleTask:
		if _, ok := payload["title"]; !ok && target != "" {
			payload["title"] = target
		}
		title, _ := payload["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, fmt.Errorf("%w: task schedule requires a task title", api.ErrInvalid)
		}
		payload["title"] = title
		if _, ok := payload["purpose"]; !ok {
			payload["purpose"] = "Complete the scheduled work."
		}
		if _, ok := payload["why"]; !ok {
			payload["why"] = "Created by a durable schedule."
		}
		if _, ok := payload["priority"]; !ok {
			payload["priority"] = domain.PriorityNormal
		}
		if _, ok := payload["definition_of_done"]; !ok {
			payload["definition_of_done"] = []string{"The scheduled outcome is completed and verified."}
		}
		payload["workspace_id"] = workspaceID
	case domain.ScheduleOrient:
		if len(payload) == 0 {
			payload["reason"] = "Scheduled orientation"
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode schedule payload: %v", api.ErrInvalid, err)
	}
	return encoded, nil
}

func (o *Operator) scriptFromInput(script domain.Script, input api.ScriptInput, creating bool) (domain.Script, error) {
	if !creating && input.Name == nil && input.Path == nil && input.Description == nil && input.Enabled == nil && input.Access == nil && input.TimeoutSeconds == nil && input.SecretBindings == nil {
		return domain.Script{}, fmt.Errorf("%w: at least one script field is required", api.ErrInvalid)
	}
	if input.Name != nil {
		script.Name = strings.TrimSpace(*input.Name)
	}
	if input.Path != nil {
		script.Path = strings.TrimSpace(*input.Path)
	}
	if input.Description != nil {
		script.Description = redactSecrets(strings.TrimSpace(*input.Description))
	}
	if input.Enabled != nil {
		script.Enabled = *input.Enabled
	}
	if input.Access != nil {
		script.Access = *input.Access
	}
	if input.TimeoutSeconds != nil {
		script.TimeoutSeconds = *input.TimeoutSeconds
	}
	if input.SecretBindings != nil {
		script.CredentialBindings = append([]domain.ScriptCredentialBinding(nil), (*input.SecretBindings)...)
	}
	if creating && (input.Name == nil || input.Path == nil) {
		return domain.Script{}, fmt.Errorf("%w: script name and path are required", api.ErrInvalid)
	}
	if script.Name == "" || strings.IndexByte(script.Name, 0) >= 0 || len(script.Name) > maximumScriptName {
		return domain.Script{}, fmt.Errorf("%w: script name is required and must be at most %d bytes", api.ErrInvalid, maximumScriptName)
	}
	if len(script.Description) > maximumDescription {
		return domain.Script{}, fmt.Errorf("%w: script description is too long", api.ErrInvalid)
	}
	if script.TimeoutSeconds < 0 || script.TimeoutSeconds > int64((30*time.Minute)/time.Second) {
		return domain.Script{}, fmt.Errorf("%w: script timeout must be between 0 and 1800 seconds", api.ErrInvalid)
	}
	if script.Access == "" {
		script.Access = domain.ScriptAccessRead
	}
	if script.Access != domain.ScriptAccessRead && script.Access != domain.ScriptAccessWrite {
		return domain.Script{}, fmt.Errorf("%w: script access must be read or write", api.ErrInvalid)
	}
	canonical, relative, err := validateScriptPath(o.paths.Scripts, script.Path)
	if err != nil {
		return domain.Script{}, err
	}
	_ = canonical
	script.Path = relative
	script.UpdatedAt = time.Now().UTC()
	return script, nil
}

func (o *Operator) findWorkspaceScript(ctx context.Context, workspaceID, identifier string) (domain.Script, error) {
	scripts, err := o.store.ListScripts(ctx, store.ScriptFilter{WorkspaceID: workspaceID})
	if err != nil {
		return domain.Script{}, err
	}
	for _, script := range scripts {
		if script.ID == identifier || strings.EqualFold(script.Name, identifier) {
			return script, nil
		}
	}
	return domain.Script{}, fmt.Errorf("%w: script %q was not found in this workspace", api.ErrInvalid, identifier)
}

func validateScriptPath(root, value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", fmt.Errorf("%w: script path is required", api.ErrInvalid)
	}
	rootPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("%w: scripts directory is unavailable", api.ErrInvalid)
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootPath, path)
	}
	path, err = filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", "", fmt.Errorf("%w: script does not exist", api.ErrInvalid)
	}
	relative, err := filepath.Rel(rootPath, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: script must be inside %s", api.ErrInvalid, rootPath)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%w: script must be a regular file", api.ErrInvalid)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", "", fmt.Errorf("%w: script is not executable", api.ErrInvalid)
	}
	return path, relative, nil
}

func prepareScopePath(raw, mode string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("%w: workspace path must be absolute", api.ErrInvalid)
	}
	path := filepath.Clean(raw)
	if path == string(filepath.Separator) || path == filepath.VolumeName(path)+string(filepath.Separator) {
		return "", fmt.Errorf("%w: filesystem root cannot be used as a workspace", api.ErrInvalid)
	}
	if mode != "create" {
		validated, err := validateWorkspaces([]string{path})
		if err != nil {
			return "", fmt.Errorf("%w: %v", api.ErrInvalid, err)
		}
		path = validated[0].Path
	}
	canonical, err := canonicalScopePath(path)
	if err != nil {
		return "", fmt.Errorf("%w: resolve workspace: %v", api.ErrInvalid, err)
	}
	return filepath.Clean(canonical), nil
}

// canonicalScopePath resolves symlinks in the existing portion of a path. It
// also supports a not-yet-created leaf without touching the filesystem, which
// lets CreateScope check for duplicate paths before creating its layout.
func canonicalScopePath(path string) (string, error) {
	current := filepath.Clean(path)
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			canonical, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				canonical = filepath.Join(canonical, missing[index])
			}
			return canonical, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func validateScopeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.IndexByte(value, 0) >= 0 || len(value) > maximumScopeNameBytes {
		return "", fmt.Errorf("%w: workspace name is required and must be at most %d bytes", api.ErrInvalid, maximumScopeNameBytes)
	}
	return redactSecrets(value), nil
}

func (o *Operator) ensureUniqueScopePath(ctx context.Context, path, exceptID string) error {
	workspaces, err := o.store.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		workspacePath, resolveErr := canonicalScopePath(workspace.Path)
		if resolveErr != nil {
			workspacePath = filepath.Clean(workspace.Path)
		}
		if workspace.ID != exceptID && workspacePath == path {
			return fmt.Errorf("%w: that workspace path is already connected", api.ErrConflict)
		}
	}
	return nil
}

func (o *Operator) requireActiveWorkspace(ctx context.Context, workspaceID string) error {
	active, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return translateNotFound(err)
	}
	if workspaceID == "" || active.ID != workspaceID {
		// Deliberately return not found so identifiers cannot reveal records from
		// another business scope.
		return api.ErrNotFound
	}
	return nil
}

func validApprovalStatus(status domain.ApprovalStatus) bool {
	return status == domain.ApprovalPending || status == domain.ApprovalApproved ||
		status == domain.ApprovalRejected || status == domain.ApprovalExpired
}

func validatePolicy(policy domain.Policy) error {
	for name, value := range map[string]string{
		"read": policy.Read, "work": policy.Work, "publish": policy.Publish, "dangerous": policy.Dangerous,
	} {
		if value != "allow" && value != "ask" && value != "deny" {
			return fmt.Errorf("%w: policy %s must be allow, ask, or deny", api.ErrInvalid, name)
		}
	}
	return nil
}

func (o *Operator) emitForWorkspace(ctx context.Context, workspaceID, eventType, entityID string, value any) {
	var data json.RawMessage
	if value != nil {
		data, _ = json.Marshal(value)
	}
	event, err := o.store.AppendEvent(ctx, domain.Event{
		WorkspaceID: workspaceID, Type: eventType, EntityID: entityID, Data: data,
	})
	if err != nil {
		o.logger.Error("persist event", "type", eventType, "error", err)
		return
	}
	o.bus.Publish(event)
}
