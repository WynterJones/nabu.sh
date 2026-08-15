package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/scriptrunner"
)

// RunScriptNow executes one enabled registered script and persists its complete
// lifecycle. An interesting manual result uses the default bounded task action.
func (e *Engine) RunScriptNow(ctx context.Context, id string) (domain.ScriptRun, error) {
	if ctx == nil {
		return domain.ScriptRun{}, errors.New("automation: nil context")
	}
	return e.runScript(ctx, strings.TrimSpace(id), "", "", "", "", false, ScriptPayload{
		ScriptID: id, OnInteresting: InterestingTask,
	})
}

// RunScriptForTask executes a registered script as bounded read context for an
// autonomous task. It enforces the expected workspace and never creates a
// second task or orientation from an "interesting" result.
func (e *Engine) RunScriptForTask(ctx context.Context, id, workspaceID, workspacePath string) (domain.ScriptRun, error) {
	if ctx == nil {
		return domain.ScriptRun{}, errors.New("automation: nil context")
	}
	return e.runScript(ctx, strings.TrimSpace(id), "", strings.TrimSpace(workspaceID), strings.TrimSpace(workspacePath), "", true, ScriptPayload{
		ScriptID: id, OnInteresting: InterestingNone,
	})
}

func (e *Engine) runScript(ctx context.Context, id, scheduleID, workspaceID, workingDirectory, effectID string, requireReadAccess bool, payload ScriptPayload) (domain.ScriptRun, error) {
	if id == "" {
		return domain.ScriptRun{}, errors.New("automation: script ID is required")
	}
	script, err := e.store.GetScript(ctx, id)
	if err != nil {
		return domain.ScriptRun{}, fmt.Errorf("automation: get script: %w", err)
	}
	if !script.Enabled {
		return domain.ScriptRun{}, fmt.Errorf("automation: script %q is disabled", script.Name)
	}
	if requireReadAccess && script.Access != domain.ScriptAccessRead {
		return domain.ScriptRun{}, fmt.Errorf("automation: task scripts must have read access")
	}
	if requireReadAccess && (strings.TrimSpace(workspaceID) == "" || script.WorkspaceID != workspaceID) {
		return domain.ScriptRun{}, fmt.Errorf("automation: task script does not belong to the requested workspace")
	}
	if strings.TrimSpace(workingDirectory) == "" && strings.TrimSpace(script.WorkspaceID) != "" {
		workspace, workspaceErr := e.store.GetWorkspace(ctx, script.WorkspaceID)
		if workspaceErr != nil {
			return domain.ScriptRun{}, fmt.Errorf("automation: resolve script workspace: %w", workspaceErr)
		}
		if !workspace.Allowed || strings.TrimSpace(workspace.Path) == "" {
			return domain.ScriptRun{}, fmt.Errorf("automation: script workspace is not approved")
		}
		workingDirectory = workspace.Path
	}
	secretEnvironment, err := e.loadScriptSecrets(ctx, script, workspaceID)
	if err != nil {
		return domain.ScriptRun{}, err
	}
	defer destroyEnvironmentSecrets(secretEnvironment)
	run, err := e.store.CreateScriptRun(ctx, domain.ScriptRun{
		ScriptID: script.ID, ScheduleID: scheduleID, Status: domain.ScriptRunPending,
	})
	if err != nil {
		return domain.ScriptRun{}, fmt.Errorf("automation: create script run: %w", err)
	}

	var callbackMu sync.Mutex
	var callbackErr error
	execution, runErr := e.runner.Run(ctx, scriptrunner.Request{
		RunID:            run.ID,
		ScheduleID:       scheduleID,
		Script:           script,
		ScriptsRoot:      e.scriptsRoot,
		WorkingDirectory: workingDirectory,
		RunsRoot:         e.runsRoot,
		Environment:      append([]string(nil), e.environment...),
		Secrets:          secretEnvironment,
		OnStart: func(pid int, startedAt time.Time) {
			starting := run
			starting.Status = domain.ScriptRunRunning
			starting.PID = pid
			starting.StartedAt = startedAt.UTC()
			callbackMu.Lock()
			callbackErr = e.store.UpdateScriptRun(ctx, starting)
			callbackMu.Unlock()
		},
	})
	callbackMu.Lock()
	startErr := callbackErr
	callbackMu.Unlock()

	finalRun := execution.Run
	if finalRun.ID == "" {
		finalRun = run
		finalRun.Status = domain.ScriptRunFailed
		finalRun.Error = boundedError(runErr)
		endedAt := e.now().UTC()
		finalRun.EndedAt = &endedAt
	}
	if finalRun.ScriptID == "" {
		finalRun.ScriptID = script.ID
	}
	if finalRun.ScheduleID == "" {
		finalRun.ScheduleID = scheduleID
	}

	resultErr := normalizeResult(&finalRun, runErr)
	persistenceContext, persistenceCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer persistenceCancel()
	if resultErr == nil && finalRun.Result != nil {
		artifacts, artifactErr := e.persistArtifacts(persistenceContext, finalRun.ID, finalRun.Result.Artifacts)
		finalRun.Result.Artifacts = artifacts
		resultErr = artifactErr
	}
	persistErr := e.store.UpdateScriptRun(persistenceContext, finalRun)
	if combinedErr := errors.Join(runErr, startErr, resultErr, persistErr); combinedErr != nil {
		return finalRun, combinedErr
	}
	if finalRun.Result != nil && finalRun.Result.Interesting {
		if err := e.handleInteresting(ctx, script, *finalRun.Result, workspaceID, effectID, payload); err != nil {
			return finalRun, err
		}
	}
	return finalRun, nil
}

// loadScriptSecrets resolves hydrated metadata from SQLite against the secure
// credential backend. It never accepts a workspace or credential namespace
// supplied by a schedule payload.
func (e *Engine) loadScriptSecrets(ctx context.Context, script domain.Script, expectedWorkspaceID string) ([]scriptrunner.EnvironmentSecret, error) {
	bindings := script.CredentialBindings
	if len(bindings) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(script.WorkspaceID) == "" {
		return nil, errors.New("automation: scripts with credential bindings require a workspace")
	}
	if expectedWorkspaceID != "" && script.WorkspaceID != expectedWorkspaceID {
		return nil, fmt.Errorf("automation: script %q does not belong to schedule workspace", script.Name)
	}
	result := make([]scriptrunner.EnvironmentSecret, 0, len(bindings))
	for index, binding := range bindings {
		env := strings.TrimSpace(binding.Env)
		integration := strings.TrimSpace(binding.CredentialIntegration)
		name := strings.TrimSpace(binding.CredentialName)
		if env == "" || strings.TrimSpace(binding.SecretRecordID) == "" || integration == "" || name == "" {
			destroyEnvironmentSecrets(result)
			return nil, fmt.Errorf("automation: script credential binding %d is incomplete", index)
		}
		if integration != domain.SecretCredentialIntegration {
			destroyEnvironmentSecrets(result)
			return nil, fmt.Errorf("automation: script credential binding %d has an invalid namespace", index)
		}
		secret, err := e.credentials.Get(ctx, credentials.Ref{
			WorkspaceID: script.WorkspaceID, Integration: integration, Name: name,
		})
		if err != nil {
			destroyEnvironmentSecrets(result)
			return nil, fmt.Errorf("automation: credential for environment %q is unavailable: %w", env, err)
		}
		value, valueErr := secret.Bytes()
		secret.Destroy()
		if valueErr != nil {
			destroyEnvironmentSecrets(result)
			return nil, fmt.Errorf("automation: read credential for environment %q: %w", env, valueErr)
		}
		result = append(result, scriptrunner.EnvironmentSecret{Name: env, Value: value})
	}
	return result, nil
}

func destroyEnvironmentSecrets(secrets []scriptrunner.EnvironmentSecret) {
	for index := range secrets {
		for valueIndex := range secrets[index].Value {
			secrets[index].Value[valueIndex] = 0
		}
		secrets[index].Value = nil
	}
}

func normalizeResult(run *domain.ScriptRun, runErr error) error {
	if runErr != nil || run.Status != domain.ScriptRunCompleted {
		return nil
	}
	if err := validateScriptResult(run.Result); err != nil {
		run.Status = domain.ScriptRunFailed
		run.Error = boundedError(err)
		return err
	}
	return nil
}

func (e *Engine) persistArtifacts(ctx context.Context, scriptRunID string, artifacts []domain.Artifact) ([]domain.Artifact, error) {
	if len(artifacts) == 0 {
		return nil, nil
	}
	persisted := make([]domain.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifact.ID = ""
		artifact.TaskID = ""
		artifact.RunID = ""
		artifact.ScriptRunID = scriptRunID
		artifact.CreatedAt = time.Time{}
		created, err := e.store.CreateArtifact(ctx, artifact)
		if err != nil {
			return persisted, fmt.Errorf("automation: persist script artifact: %w", err)
		}
		persisted = append(persisted, created)
	}
	return persisted, nil
}

func (e *Engine) handleInteresting(ctx context.Context, script domain.Script, result domain.ScriptResult, workspaceID, effectID string, payload ScriptPayload) error {
	action := payload.OnInteresting
	if action == "" {
		action = InterestingTask
	}
	switch action {
	case InterestingNone:
		return nil
	case InterestingOrient:
		_, err := e.requestOrientation(ctx, workspaceID)
		return err
	case InterestingTask:
		task := interestingTask(script, result, payload.InterestingTask)
		if err := applyScheduleWorkspace(workspaceID, &task.WorkspaceID); err != nil {
			return err
		}
		if err := validateTaskPayload(task, true); err != nil {
			return err
		}
		_, err := e.createTask(ctx, task, "script:"+script.ID, effectID)
		return err
	default:
		return fmt.Errorf("automation: unsupported interesting action %q", action)
	}
}

func interestingTask(script domain.Script, result domain.ScriptResult, override *TaskPayload) TaskPayload {
	defaultTitle := truncateBytes("Investigate "+strings.TrimSpace(script.Name), maximumTitleBytes)
	task := TaskPayload{
		Title:            defaultTitle,
		Purpose:          strings.TrimSpace(result.Summary),
		Why:              "A deterministic script reported a meaningful change.",
		Priority:         domain.PriorityNormal,
		DefinitionOfDone: []string{"Review the script signal and take the appropriate verified action."},
	}
	if override == nil {
		return task
	}
	task = *override
	if strings.TrimSpace(task.Title) == "" {
		task.Title = defaultTitle
	}
	if strings.TrimSpace(task.Purpose) == "" {
		task.Purpose = strings.TrimSpace(result.Summary)
	}
	if len(task.DefinitionOfDone) == 0 {
		task.DefinitionOfDone = []string{"Review the script signal and take the appropriate verified action."}
	}
	return task
}

func validateInterestingAction(action InterestingAction) error {
	switch action {
	case "", InterestingTask, InterestingOrient, InterestingNone:
		return nil
	default:
		return fmt.Errorf("automation: unsupported interesting action %q", action)
	}
}
