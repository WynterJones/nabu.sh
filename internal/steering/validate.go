package steering

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/scheduler"
)

const (
	maxAssistantResponseRunes = 32_000
	maxTaskTitleRunes         = 180
	maxTaskTextRunes          = 12_000
	maxContextChangeRunes     = 64_000
	maxPlanItems              = 24
	maxChatDatasetColumns     = 32
	maxChatDatasetRows        = 100
	maxChatDatasetPayload     = 256 * 1024
	maxChoicePromptRunes      = 500
	maxChoiceDescriptionRunes = 1_000
	maxChoiceLabelRunes       = 80
	maxChoiceValueRunes       = 500
	maxSecretNameRunes        = 120
	maxScriptPathRunes        = 512
	maxScriptContentBytes     = 64 * 1024
)

var chatDatasetIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var chatSecretIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,119}$`)
var chatEnvironmentName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

// ValidateResult normalizes a steering result and rejects any effect that is
// ambiguous, references non-authoritative IDs, or attempts a protected state
// transition. It performs no side effects.
func ValidateResult(result Result, state ValidationState) (Result, error) {
	result.AssistantResponse = strings.TrimSpace(result.AssistantResponse)
	if result.AssistantResponse == "" {
		return Result{}, fmt.Errorf("%w: assistant_response is required", ErrInvalidResult)
	}
	if runeCount(result.AssistantResponse) > maxAssistantResponseRunes {
		return Result{}, fmt.Errorf("%w: assistant_response exceeds %d characters", ErrInvalidResult, maxAssistantResponseRunes)
	}
	if len(result.Effects) == 0 {
		return Result{}, fmt.Errorf("%w: at least one effect is required", ErrInvalidResult)
	}
	if len(result.Effects) > MaxEffects {
		return Result{}, fmt.Errorf("%w: effects exceed maximum of %d", ErrInvalidResult, MaxEffects)
	}
	// A confirmed response may complete setup and begin safe work atomically.
	// Normalize the boundary transition first so harmless model ordering cannot
	// turn an explicit owner approval into a validation failure.
	if state.ContextGateEnabled && !state.ContextReady && state.ContextConfirmed {
		for index, effect := range result.Effects {
			if effect.Type == EffectCompleteContext && index > 0 {
				result.Effects = append([]Effect{effect}, append(result.Effects[:index], result.Effects[index+1:]...)...)
				break
			}
		}
	}

	tasks := make(map[string]domain.Task, len(state.Tasks))
	openTitles := make(map[string]struct{}, len(state.Tasks))
	for _, task := range state.Tasks {
		if task.ID != "" {
			tasks[task.ID] = task
		}
		if task.Status != domain.TaskCompleted && task.Status != domain.TaskFailed && task.Status != domain.TaskCancelled {
			if key := normalizedTitle(task.Title); key != "" {
				openTitles[key] = struct{}{}
			}
		}
	}
	workspaces := make(map[string]struct{}, len(state.Workspaces))
	for _, workspace := range state.Workspaces {
		if workspace.ID != "" && workspace.Allowed {
			workspaces[workspace.ID] = struct{}{}
		}
	}
	approvals := make(map[string]ApprovalSummary, len(state.PendingApprovals))
	for _, approval := range state.PendingApprovals {
		if approval.ID != "" && normalizeApprovalStatus(approval.Status) == ApprovalPending {
			approvals[approval.ID] = approval
		}
	}
	datasets := make(map[string]domain.Dataset, len(state.Datasets))
	for _, dataset := range state.Datasets {
		if dataset.ID != "" && dataset.DeletedAt == nil {
			datasets[dataset.ID] = dataset
		}
	}
	secrets := make(map[string]SecretSummary, len(state.Secrets))
	for _, secret := range state.Secrets {
		if strings.TrimSpace(secret.ID) != "" {
			secrets[secret.ID] = secret
		}
	}
	localApps := make(map[string]LocalAppSummary, len(state.LocalApps))
	for _, app := range state.LocalApps {
		if strings.TrimSpace(app.ID) != "" {
			localApps[app.ID] = app
		}
	}
	datasetRows := make(map[string]map[int64]domain.DatasetRow)
	for _, query := range state.DatasetQueries {
		if _, exists := datasets[query.DatasetID]; !exists {
			continue
		}
		if datasetRows[query.DatasetID] == nil {
			datasetRows[query.DatasetID] = make(map[int64]domain.DatasetRow)
		}
		for _, row := range query.Rows {
			if row.ID > 0 {
				datasetRows[query.DatasetID][row.ID] = row
			}
		}
	}

	created := 0
	conversationOnly := false
	contextCompleted := false
	stateAction := EffectType("")
	missionChanged, contextChanged, planCreated, choiceRequested := false, false, false, false
	mutatedTasks := make(map[string]EffectType)
	resolvedApprovals := make(map[string]EffectType)
	changedPolicy := make(map[ActionCategory]struct{})
	createdTitles := make(map[string]struct{})
	localAppActions := make(map[string]EffectType)

	for index := range result.Effects {
		effect, err := validateEffect(result.Effects[index], tasks, workspaces, approvals, datasets, datasetRows, secrets, localApps, state.ScheduleRequested)
		if err != nil {
			return Result{}, fmt.Errorf("effect %d: %w", index, err)
		}
		result.Effects[index] = effect
		if state.ContextGateEnabled && !state.ContextReady && !contextCompleted && !contextSetupEffect(effect.Type) {
			return Result{}, fmt.Errorf("%w: %s is unavailable until workspace context is confirmed", ErrHighImpactEffect, effect.Type)
		}

		switch effect.Type {
		case EffectConversationOnly:
			conversationOnly = true
		case EffectProposeContext:
			if state.ContextReady {
				return Result{}, fmt.Errorf("%w: workspace context is already ready", ErrInvalidResult)
			}
		case EffectRequestChoice:
			if choiceRequested {
				return Result{}, fmt.Errorf("%w: only one choice card may be requested per response", ErrInvalidResult)
			}
			choiceRequested = true
		case EffectCompleteContext:
			if state.ContextReady || contextCompleted {
				return Result{}, fmt.Errorf("%w: workspace context is already ready", ErrInvalidResult)
			}
			if !state.ContextConfirmed {
				return Result{}, fmt.Errorf("%w: complete_context requires explicit owner confirmation", ErrHighImpactEffect)
			}
			contextCompleted = true
		case EffectCreateTask:
			created++
			if created > MaxCreatedTasks {
				return Result{}, fmt.Errorf("%w: create_task effects exceed maximum of %d", ErrInvalidResult, MaxCreatedTasks)
			}
			key := normalizedTitle(effect.Task.Title)
			if _, exists := openTitles[key]; exists {
				return Result{}, fmt.Errorf("%w: create_task duplicates open task %q", ErrInvalidResult, effect.Task.Title)
			}
			if _, exists := createdTitles[key]; exists {
				return Result{}, fmt.Errorf("%w: duplicate create_task title %q", ErrInvalidResult, effect.Task.Title)
			}
			createdTitles[key] = struct{}{}
		case EffectUpdateTask, EffectCancelTask:
			if state.RecoveryTaskID != "" && effect.TaskID != state.RecoveryTaskID {
				return Result{}, fmt.Errorf("%w: recovery turn may only change task %q", ErrHighImpactEffect, state.RecoveryTaskID)
			}
			if prior, exists := mutatedTasks[effect.TaskID]; exists {
				return Result{}, fmt.Errorf("%w: task %q has conflicting %s and %s effects", ErrInvalidResult, effect.TaskID, prior, effect.Type)
			}
			mutatedTasks[effect.TaskID] = effect.Type
		case EffectUpdateMission:
			if missionChanged {
				return Result{}, fmt.Errorf("%w: duplicate update_mission effect", ErrInvalidResult)
			}
			missionChanged = true
		case EffectUpdateContext:
			if contextChanged {
				return Result{}, fmt.Errorf("%w: duplicate update_context effect", ErrInvalidResult)
			}
			contextChanged = true
		case EffectUpdateSoul:
			// Multiple character changes in one response make the evolution hard
			// to review; reuse the context change guard to keep it singular.
			if contextChanged {
				return Result{}, fmt.Errorf("%w: only one context or soul reflection may be proposed", ErrInvalidResult)
			}
			contextChanged = true
		case EffectUpdatePolicy:
			if _, exists := changedPolicy[effect.Policy.Category]; exists {
				return Result{}, fmt.Errorf("%w: duplicate policy update for %s", ErrInvalidResult, effect.Policy.Category)
			}
			changedPolicy[effect.Policy.Category] = struct{}{}
		case EffectApproveAction, EffectRejectAction:
			if prior, exists := resolvedApprovals[effect.ApprovalID]; exists {
				return Result{}, fmt.Errorf("%w: approval %q has conflicting %s and %s effects", ErrInvalidResult, effect.ApprovalID, prior, effect.Type)
			}
			resolvedApprovals[effect.ApprovalID] = effect.Type
		case EffectPause, EffectResume:
			if stateAction != "" {
				return Result{}, fmt.Errorf("%w: conflicting %s and %s effects", ErrInvalidResult, stateAction, effect.Type)
			}
			stateAction = effect.Type
		case EffectCreatePlan:
			if planCreated {
				return Result{}, fmt.Errorf("%w: only one plan may be proposed per response", ErrInvalidResult)
			}
			planCreated = true
		case EffectStartLocalApp, EffectStopLocalApp:
			if prior, exists := localAppActions[effect.AppID]; exists {
				return Result{}, fmt.Errorf("%w: local app %q has conflicting %s and %s effects", ErrInvalidResult, effect.AppID, prior, effect.Type)
			}
			localAppActions[effect.AppID] = effect.Type
		}
	}
	if conversationOnly && len(result.Effects) != 1 {
		return Result{}, fmt.Errorf("%w: conversation_only cannot be combined with state changes", ErrInvalidResult)
	}
	return result, nil
}

func contextSetupEffect(effect EffectType) bool {
	switch effect {
	case EffectConversationOnly, EffectProposeContext, EffectRequestChoice, EffectCompleteContext, EffectUpdateMission, EffectUpdateContext, EffectCreateSecret, EffectCreateScript, EffectCreateLocalApp, EffectUpdateSoul:
		return true
	default:
		return false
	}
}

func validateEffect(effect Effect, tasks map[string]domain.Task, workspaces map[string]struct{}, approvals map[string]ApprovalSummary, datasets map[string]domain.Dataset, datasetRows map[string]map[int64]domain.DatasetRow, secrets map[string]SecretSummary, localApps map[string]LocalAppSummary, scheduleRequested bool) (Effect, error) {
	effect.Type = EffectType(normalizeToken(string(effect.Type)))
	effect.TaskID = strings.TrimSpace(effect.TaskID)
	effect.AppID = strings.TrimSpace(effect.AppID)
	effect.ApprovalID = strings.TrimSpace(effect.ApprovalID)
	effect.Note = strings.TrimSpace(effect.Note)

	switch effect.Type {
	case EffectConversationOnly:
		if fields := extraneousFields(effect, nil); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
	case EffectProposeContext:
		if fields := extraneousFields(effect, nil); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
	case EffectRequestChoice:
		if fields := extraneousFields(effect, map[string]bool{"choice": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Choice == nil {
			return Effect{}, fmt.Errorf("%w: request_choice requires choice", ErrInvalidResult)
		}
		if err := normalizeChoiceRequest(effect.Choice); err != nil {
			return Effect{}, err
		}
	case EffectCompleteContext:
		if fields := extraneousFields(effect, nil); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
	case EffectCreateTask:
		if fields := extraneousFields(effect, map[string]bool{"task": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Task == nil {
			return Effect{}, fmt.Errorf("%w: create_task requires task", ErrInvalidResult)
		}
		change, err := normalizeTaskChange(*effect.Task, true, workspaces)
		if err != nil {
			return Effect{}, err
		}
		if err := validateTaskDependencies(change.DependsOnTaskIDs, "", change.WorkspaceID, tasks); err != nil {
			return Effect{}, err
		}
		effect.Task = &change
	case EffectUpdateTask:
		if fields := extraneousFields(effect, map[string]bool{"task_id": true, "task": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		current, exists := tasks[effect.TaskID]
		if effect.TaskID == "" || !exists {
			return Effect{}, fmt.Errorf("%w: update_task references unknown task %q", ErrInvalidResult, effect.TaskID)
		}
		if current.Status == domain.TaskCompleted {
			return Effect{}, fmt.Errorf("%w: completed task %q cannot be changed", ErrHighImpactEffect, effect.TaskID)
		}
		if current.Status == domain.TaskRunning {
			return Effect{}, fmt.Errorf("%w: running task %q cannot be edited; cancel it first", ErrHighImpactEffect, effect.TaskID)
		}
		if effect.Task == nil {
			return Effect{}, fmt.Errorf("%w: update_task requires task", ErrInvalidResult)
		}
		change, err := normalizeTaskChange(*effect.Task, false, workspaces)
		if err != nil {
			return Effect{}, err
		}
		if change.DependsOnTaskIDs != nil {
			if err := validateTaskDependencies(change.DependsOnTaskIDs, current.ID, current.WorkspaceID, tasks); err != nil {
				return Effect{}, err
			}
		}
		if !taskChangeHasValue(change) {
			return Effect{}, fmt.Errorf("%w: update_task has no changes", ErrInvalidResult)
		}
		if change.Status != "" && !allowedSteeringStatus(change.Status) {
			return Effect{}, fmt.Errorf("%w: task status %q must be changed by the daemon lifecycle", ErrHighImpactEffect, change.Status)
		}
		effect.Task = &change
	case EffectCancelTask:
		if fields := extraneousFields(effect, map[string]bool{"task_id": true, "note": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		current, exists := tasks[effect.TaskID]
		if effect.TaskID == "" || !exists {
			return Effect{}, fmt.Errorf("%w: cancel_task references unknown task %q", ErrInvalidResult, effect.TaskID)
		}
		if current.Status == domain.TaskCompleted || current.Status == domain.TaskCancelled {
			return Effect{}, fmt.Errorf("%w: task %q is already terminal", ErrInvalidResult, effect.TaskID)
		}
	case EffectUpdateMission:
		if fields := extraneousFields(effect, map[string]bool{"mission": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Mission == nil || strings.TrimSpace(effect.Mission.Statement) == "" {
			return Effect{}, fmt.Errorf("%w: update_mission requires a statement", ErrInvalidResult)
		}
		effect.Mission.Statement = strings.TrimSpace(effect.Mission.Statement)
	case EffectUpdateContext:
		if fields := extraneousFields(effect, map[string]bool{"context": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Context == nil || strings.TrimSpace(effect.Context.Value) == "" {
			return Effect{}, fmt.Errorf("%w: update_context requires a value", ErrInvalidResult)
		}
		effect.Context.Value = strings.TrimSpace(effect.Context.Value)
		if runeCount(effect.Context.Value) > maxContextChangeRunes {
			return Effect{}, fmt.Errorf("%w: context exceeds %d characters", ErrInvalidResult, maxContextChangeRunes)
		}
	case EffectUpdateSoul:
		if fields := extraneousFields(effect, map[string]bool{"soul": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Soul == nil || strings.TrimSpace(effect.Soul.Reflection) == "" {
			return Effect{}, fmt.Errorf("%w: update_soul requires a reflection", ErrInvalidResult)
		}
		effect.Soul.Reflection = strings.TrimSpace(effect.Soul.Reflection)
		if runeCount(effect.Soul.Reflection) > 1_000 {
			return Effect{}, fmt.Errorf("%w: soul reflection exceeds 1000 characters", ErrInvalidResult)
		}
	case EffectUpdatePolicy:
		if fields := extraneousFields(effect, map[string]bool{"policy": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Policy == nil {
			return Effect{}, fmt.Errorf("%w: update_policy requires policy", ErrInvalidResult)
		}
		category, valid := normalizeCategory(effect.Policy.Category)
		if !valid {
			return Effect{}, fmt.Errorf("%w: invalid policy category %q", ErrInvalidResult, effect.Policy.Category)
		}
		decision, valid := normalizeEffectDecision(effect.Policy.Decision)
		if !valid {
			return Effect{}, fmt.Errorf("%w: invalid policy decision %q", ErrInvalidResult, effect.Policy.Decision)
		}
		if category == CategoryDangerous && decision == DecisionAllow {
			return Effect{}, fmt.Errorf("%w: dangerous actions must require approval", ErrHighImpactEffect)
		}
		effect.Policy.Category, effect.Policy.Decision = category, decision
	case EffectApproveAction, EffectRejectAction:
		allowed := map[string]bool{"approval_id": true}
		if effect.Type == EffectRejectAction {
			allowed["note"] = true
		}
		if fields := extraneousFields(effect, allowed); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.ApprovalID == "" {
			return Effect{}, fmt.Errorf("%w: %s requires approval_id", ErrInvalidResult, effect.Type)
		}
		if _, exists := approvals[effect.ApprovalID]; !exists {
			return Effect{}, fmt.Errorf("%w: %s references unknown pending approval %q", ErrInvalidResult, effect.Type, effect.ApprovalID)
		}
	case EffectPause, EffectResume:
		if fields := extraneousFields(effect, nil); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
	case EffectRequestReport:
		if fields := extraneousFields(effect, map[string]bool{"report": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Report == nil || strings.TrimSpace(effect.Report.Scope) == "" {
			return Effect{}, fmt.Errorf("%w: request_report requires report scope", ErrInvalidResult)
		}
		effect.Report.Title = strings.TrimSpace(effect.Report.Title)
		effect.Report.Scope = strings.TrimSpace(effect.Report.Scope)
	case EffectCreatePlan:
		if fields := extraneousFields(effect, map[string]bool{"plan": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Plan == nil {
			return Effect{}, fmt.Errorf("%w: create_plan requires plan", ErrInvalidResult)
		}
		if err := normalizePlanChange(effect.Plan); err != nil {
			return Effect{}, err
		}
	case EffectCreateSchedule:
		if fields := extraneousFields(effect, map[string]bool{"schedule": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if !scheduleRequested {
			return Effect{}, fmt.Errorf("%w: create_schedule requires an explicit scheduling request from the user", ErrHighImpactEffect)
		}
		if effect.Schedule == nil {
			return Effect{}, fmt.Errorf("%w: create_schedule requires schedule", ErrInvalidResult)
		}
		if err := normalizeScheduleChange(effect.Schedule, workspaces); err != nil {
			return Effect{}, err
		}
	case EffectCreateSecret:
		if fields := extraneousFields(effect, map[string]bool{"secret": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Secret == nil {
			return Effect{}, fmt.Errorf("%w: create_secret requires secret metadata", ErrInvalidResult)
		}
		effect.Secret.Name = strings.TrimSpace(effect.Secret.Name)
		effect.Secret.Label = strings.TrimSpace(effect.Secret.Label)
		effect.Secret.Description = strings.TrimSpace(effect.Secret.Description)
		if !chatSecretIdentifier.MatchString(effect.Secret.Name) || runeCount(effect.Secret.Name) > maxSecretNameRunes || runeCount(effect.Secret.Label) > 160 || runeCount(effect.Secret.Description) > 2_000 {
			return Effect{}, fmt.Errorf("%w: secret metadata is invalid or oversized", ErrInvalidResult)
		}
	case EffectCreateScript:
		if fields := extraneousFields(effect, map[string]bool{"script": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Script == nil {
			return Effect{}, fmt.Errorf("%w: create_script requires script metadata", ErrInvalidResult)
		}
		if err := normalizeScriptChange(effect.Script, secrets); err != nil {
			return Effect{}, err
		}
	case EffectCreateLocalApp:
		if fields := extraneousFields(effect, map[string]bool{"local_app": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.LocalApp == nil {
			return Effect{}, fmt.Errorf("%w: create_local_app requires local_app", ErrInvalidResult)
		}
		if err := normalizeLocalAppChange(effect.LocalApp); err != nil {
			return Effect{}, err
		}
	case EffectStartLocalApp, EffectStopLocalApp:
		if fields := extraneousFields(effect, map[string]bool{"app_id": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if _, exists := localApps[effect.AppID]; !exists {
			return Effect{}, fmt.Errorf("%w: %s references unknown local app %q", ErrInvalidResult, effect.Type, effect.AppID)
		}
	case EffectCreateDataset:
		if fields := extraneousFields(effect, map[string]bool{"dataset": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.Dataset == nil {
			return Effect{}, fmt.Errorf("%w: create_dataset requires dataset", ErrInvalidResult)
		}
		if err := normalizeDatasetChange(effect.Dataset); err != nil {
			return Effect{}, err
		}
	case EffectUpsertDatasetRows:
		if fields := extraneousFields(effect, map[string]bool{"dataset_rows": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.DatasetRows == nil {
			return Effect{}, fmt.Errorf("%w: upsert_dataset_rows requires dataset_rows", ErrInvalidResult)
		}
		dataset, exists := datasets[strings.TrimSpace(effect.DatasetRows.DatasetID)]
		if !exists {
			return Effect{}, fmt.Errorf("%w: upsert_dataset_rows references unknown dataset %q", ErrInvalidResult, effect.DatasetRows.DatasetID)
		}
		effect.DatasetRows.DatasetID = dataset.ID
		if err := validateDatasetRows(dataset, effect.DatasetRows.Rows); err != nil {
			return Effect{}, err
		}
	case EffectUpdateDatasetRow, EffectDeleteDatasetRow:
		if fields := extraneousFields(effect, map[string]bool{"dataset_row": true}); len(fields) > 0 {
			return Effect{}, invalidPayload(effect.Type, fields)
		}
		if effect.DatasetRow == nil {
			return Effect{}, fmt.Errorf("%w: %s requires dataset_row", ErrInvalidResult, effect.Type)
		}
		effect.DatasetRow.DatasetID = strings.TrimSpace(effect.DatasetRow.DatasetID)
		dataset, exists := datasets[effect.DatasetRow.DatasetID]
		row, rowExists := datasetRows[effect.DatasetRow.DatasetID][effect.DatasetRow.RowID]
		if !exists || effect.DatasetRow.RowID <= 0 || !rowExists {
			return Effect{}, fmt.Errorf("%w: %s must reference an exact row from dataset_query_results", ErrInvalidResult, effect.Type)
		}
		if effect.Type == EffectDeleteDatasetRow {
			if len(effect.DatasetRow.Values) != 0 {
				return Effect{}, fmt.Errorf("%w: delete_dataset_row cannot include values", ErrInvalidResult)
			}
			break
		}
		if len(effect.DatasetRow.Values) == 0 || len(effect.DatasetRow.Values) > maxChatDatasetColumns {
			return Effect{}, fmt.Errorf("%w: update_dataset_row requires 1-%d values", ErrInvalidResult, maxChatDatasetColumns)
		}
		merged := make(map[string]any, len(row.Values))
		for key, value := range row.Values {
			merged[key] = value
		}
		for key, value := range effect.DatasetRow.Values {
			merged[key] = value
		}
		if err := validateDatasetRowsForOperation(dataset, []map[string]any{merged}, false); err != nil {
			return Effect{}, err
		}
	default:
		return Effect{}, fmt.Errorf("%w: %q", ErrUnknownEffect, effect.Type)
	}
	return effect, nil
}

func normalizeLocalAppChange(change *LocalAppChange) error {
	change.Name = strings.TrimSpace(change.Name)
	change.Description = strings.TrimSpace(change.Description)
	change.Directory = filepath.ToSlash(filepath.Clean(strings.TrimSpace(change.Directory)))
	change.HealthPath = strings.TrimSpace(change.HealthPath)
	if change.HealthPath == "" {
		change.HealthPath = "/"
	}
	if change.Name == "" || runeCount(change.Name) > 160 || runeCount(change.Description) > 4_000 {
		return fmt.Errorf("%w: local app name or description is invalid or oversized", ErrInvalidResult)
	}
	if change.Directory == "." || change.Directory == ".." || filepath.IsAbs(change.Directory) || strings.HasPrefix(change.Directory, "../") || runeCount(change.Directory) > 1_024 || !strings.HasPrefix(change.Directory, "repos/") || change.Directory == "repos/" {
		return fmt.Errorf("%w: local app directory must be repos/<app-folder>; workspace-root applications are not allowed", ErrInvalidResult)
	}
	if len(change.Command) == 0 || len(change.Command) > 32 {
		return fmt.Errorf("%w: local app command requires 1-32 argv entries", ErrInvalidResult)
	}
	for index := range change.Command {
		change.Command[index] = strings.TrimSpace(change.Command[index])
		if strings.ContainsRune(change.Command[index], '\x00') || len(change.Command[index]) > 4_096 {
			return fmt.Errorf("%w: local app command contains an invalid argument", ErrInvalidResult)
		}
	}
	if change.Command[0] == "" || localAppShellExecutable(change.Command[0]) || change.Port < 1_024 || change.Port > 65_535 {
		return fmt.Errorf("%w: local app executable or port is invalid", ErrInvalidResult)
	}
	if !strings.HasPrefix(change.HealthPath, "/") || strings.ContainsAny(change.HealthPath, "?#\x00") || runeCount(change.HealthPath) > 1_024 {
		return fmt.Errorf("%w: local app health path is invalid", ErrInvalidResult)
	}
	return nil
}

func localAppShellExecutable(value string) bool {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(value))) {
	case "sh", "bash", "zsh", "fish", "dash", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	default:
		return false
	}
}

func normalizeChoiceRequest(choice *ChoiceRequest) error {
	choice.Prompt = strings.TrimSpace(choice.Prompt)
	choice.Description = strings.TrimSpace(choice.Description)
	if choice.Prompt == "" || runeCount(choice.Prompt) > maxChoicePromptRunes || runeCount(choice.Description) > maxChoiceDescriptionRunes {
		return fmt.Errorf("%w: choice prompt and description are required and bounded", ErrInvalidResult)
	}
	if len(choice.Options) < 2 || len(choice.Options) > 5 {
		return fmt.Errorf("%w: choice requires 2-5 options", ErrInvalidResult)
	}
	labels := make(map[string]struct{}, len(choice.Options))
	values := make(map[string]struct{}, len(choice.Options))
	primary := 0
	for index := range choice.Options {
		option := &choice.Options[index]
		option.Label = strings.TrimSpace(option.Label)
		option.Value = strings.TrimSpace(option.Value)
		option.Description = strings.TrimSpace(option.Description)
		if option.Label == "" || option.Value == "" || runeCount(option.Label) > maxChoiceLabelRunes || runeCount(option.Value) > maxChoiceValueRunes || runeCount(option.Description) > maxChoiceDescriptionRunes {
			return fmt.Errorf("%w: choice option %d has invalid or oversized fields", ErrInvalidResult, index)
		}
		labelKey, valueKey := normalizedTitle(option.Label), normalizedTitle(option.Value)
		if _, duplicate := labels[labelKey]; duplicate {
			return fmt.Errorf("%w: duplicate choice label %q", ErrInvalidResult, option.Label)
		}
		if _, duplicate := values[valueKey]; duplicate {
			return fmt.Errorf("%w: duplicate choice value", ErrInvalidResult)
		}
		labels[labelKey], values[valueKey] = struct{}{}, struct{}{}
		if option.Primary {
			primary++
		}
	}
	if primary > 1 {
		return fmt.Errorf("%w: choice may have at most one primary option", ErrInvalidResult)
	}
	return nil
}

func normalizePlanChange(change *PlanChange) error {
	change.Title = strings.TrimSpace(change.Title)
	change.Objective = strings.TrimSpace(change.Objective)
	if change.Title == "" || change.Objective == "" || runeCount(change.Title) > 180 || runeCount(change.Objective) > maxTaskTextRunes {
		return fmt.Errorf("%w: plan title and objective are required and bounded", ErrInvalidResult)
	}
	if len(change.Items) == 0 || len(change.Items) > maxPlanItems {
		return fmt.Errorf("%w: plan requires 1-%d proposed items", ErrInvalidResult, maxPlanItems)
	}
	for index := range change.Items {
		item := &change.Items[index]
		item.Title, item.Purpose, item.Why = strings.TrimSpace(item.Title), strings.TrimSpace(item.Purpose), strings.TrimSpace(item.Why)
		if item.Title == "" || runeCount(item.Title) > maxTaskTitleRunes || runeCount(item.Purpose) > maxTaskTextRunes || runeCount(item.Why) > maxTaskTextRunes {
			return fmt.Errorf("%w: plan item %d has invalid or oversized fields", ErrInvalidResult, index)
		}
		switch item.Kind {
		case domain.PlanItemTask, domain.PlanItemSchedule, domain.PlanItemMilestone:
		default:
			return fmt.Errorf("%w: plan item %d has invalid kind %q", ErrInvalidResult, index, item.Kind)
		}
		if item.PlannedAt != nil {
			value := item.PlannedAt.UTC()
			if value.After(time.Now().UTC().Add(14 * 24 * time.Hour)) {
				return fmt.Errorf("%w: plan item %d exceeds the 14-day planning horizon", ErrInvalidResult, index)
			}
			item.PlannedAt = &value
		}
	}
	return nil
}

func normalizeScheduleChange(change *ScheduleChange, workspaces map[string]struct{}) error {
	change.Name, change.Expression, change.Reason = strings.TrimSpace(change.Name), strings.TrimSpace(change.Expression), strings.TrimSpace(change.Reason)
	if change.Name == "" || runeCount(change.Name) > 160 {
		return fmt.Errorf("%w: schedule name is required and bounded", ErrInvalidResult)
	}
	if (change.Expression == "") == (change.IntervalSeconds == 0) {
		return fmt.Errorf("%w: schedule requires exactly one expression or interval_seconds", ErrInvalidResult)
	}
	if change.IntervalSeconds != 0 && change.IntervalSeconds < 60 {
		return fmt.Errorf("%w: schedule interval must be at least 60 seconds", ErrInvalidResult)
	}
	switch change.Kind {
	case domain.ScheduleTask:
		if change.Task == nil || change.Reason != "" {
			return fmt.Errorf("%w: task schedule requires only a task payload", ErrInvalidResult)
		}
		task, err := normalizeTaskChange(*change.Task, true, workspaces)
		if err != nil {
			return err
		}
		change.Task = &task
	case domain.ScheduleOrient:
		if change.Task != nil || runeCount(change.Reason) > 2_000 {
			return fmt.Errorf("%w: orientation schedule has an invalid payload", ErrInvalidResult)
		}
	default:
		return fmt.Errorf("%w: chat schedules may only create task or orientation work", ErrHighImpactEffect)
	}
	probe := domain.Schedule{Enabled: true, Kind: change.Kind, Expression: change.Expression, IntervalSeconds: change.IntervalSeconds}
	if _, err := scheduler.Next(probe, time.Now().UTC()); err != nil {
		return fmt.Errorf("%w: invalid schedule cadence: %v", ErrInvalidResult, err)
	}
	return nil
}

func normalizeScriptChange(change *ScriptChange, secrets map[string]SecretSummary) error {
	change.Name = strings.TrimSpace(change.Name)
	change.Path = strings.TrimSpace(change.Path)
	change.Description = strings.TrimSpace(change.Description)
	change.Content = strings.TrimSpace(change.Content)
	change.Access = normalizeToken(change.Access)
	if change.Access == "" {
		change.Access = "read"
	}
	if change.Name == "" || runeCount(change.Name) > 160 || change.Path == "" || runeCount(change.Path) > maxScriptPathRunes || runeCount(change.Description) > 2_000 {
		return fmt.Errorf("%w: script metadata is invalid or oversized", ErrInvalidResult)
	}
	if strings.HasPrefix(change.Path, "/") || strings.ContainsAny(change.Path, "/\\") || strings.Contains(change.Path, "..") || strings.ContainsRune(change.Path, 0) {
		return fmt.Errorf("%w: create_script path must be a safe filename inside Nabu's scripts directory", ErrInvalidResult)
	}
	if len(change.Content) == 0 || len(change.Content) > maxScriptContentBytes || !strings.HasPrefix(change.Content, "#!/bin/sh\n") {
		return fmt.Errorf("%w: create_script content must be a bounded POSIX shell script beginning with #!/bin/sh", ErrInvalidResult)
	}
	if change.Access != "read" && change.Access != "write" {
		return fmt.Errorf("%w: script access must be read or write", ErrInvalidResult)
	}
	if change.TimeoutSeconds == 0 {
		change.TimeoutSeconds = 300
	}
	if change.TimeoutSeconds < 1 || change.TimeoutSeconds > 1800 {
		return fmt.Errorf("%w: script timeout must be between 1 and 1800 seconds", ErrInvalidResult)
	}
	if len(change.SecretBindings) > 16 {
		return fmt.Errorf("%w: script may bind at most 16 saved secrets", ErrInvalidResult)
	}
	seenEnv := make(map[string]struct{}, len(change.SecretBindings))
	for index := range change.SecretBindings {
		binding := &change.SecretBindings[index]
		binding.SecretID = strings.TrimSpace(binding.SecretID)
		binding.EnvVar = strings.TrimSpace(binding.EnvVar)
		secret, exists := secrets[binding.SecretID]
		if !exists || !secret.Configured {
			return fmt.Errorf("%w: script binding references unknown or unconfigured secret %q", ErrInvalidResult, binding.SecretID)
		}
		if !chatEnvironmentName.MatchString(binding.EnvVar) || reservedScriptEnvironment(binding.EnvVar) {
			return fmt.Errorf("%w: script binding %d has an invalid environment variable", ErrInvalidResult, index)
		}
		if _, duplicate := seenEnv[binding.EnvVar]; duplicate {
			return fmt.Errorf("%w: duplicate script environment variable %q", ErrInvalidResult, binding.EnvVar)
		}
		seenEnv[binding.EnvVar] = struct{}{}
	}
	return nil
}

func reservedScriptEnvironment(value string) bool {
	switch value {
	case "BASHOPTS", "BASH_ENV", "CDPATH", "ENV", "GLOBIGNORE", "HOME", "IFS", "NODE_OPTIONS", "OLDPWD", "PATH", "PERL5LIB", "PERL5OPT", "PROMPT_COMMAND", "PS4", "PWD", "PYTHONHOME", "PYTHONPATH", "RUBYLIB", "RUBYOPT", "SHELL", "SHELLOPTS":
		return true
	}
	return strings.HasPrefix(value, "NABU_") || strings.HasPrefix(value, "LD_") || strings.HasPrefix(value, "DYLD_") || strings.HasPrefix(value, "BASH_FUNC_") || strings.HasPrefix(value, "GIT_CONFIG_") || strings.HasSuffix(value, "_ASKPASS")
}

func normalizeDatasetChange(change *DatasetChange) error {
	change.Name, change.Slug, change.Description = strings.TrimSpace(change.Name), strings.TrimSpace(change.Slug), strings.TrimSpace(change.Description)
	if change.Name == "" || len(change.Name) > 160 || len(change.Description) > 8*1024 {
		return fmt.Errorf("%w: dataset name and description are invalid or oversized", ErrInvalidResult)
	}
	if len(change.Schema) == 0 || len(change.Schema) > maxChatDatasetColumns {
		return fmt.Errorf("%w: chat-created dataset requires 1-%d columns", ErrInvalidResult, maxChatDatasetColumns)
	}
	columns := make(map[string]domain.DatasetColumn, len(change.Schema))
	for index := range change.Schema {
		column := &change.Schema[index]
		column.Name, column.Description = strings.TrimSpace(column.Name), strings.TrimSpace(column.Description)
		if !chatDatasetIdentifier.MatchString(column.Name) || len(column.Description) > 2*1024 {
			return fmt.Errorf("%w: invalid dataset column %q", ErrInvalidResult, column.Name)
		}
		switch column.Type {
		case domain.DatasetString, domain.DatasetInteger, domain.DatasetNumber, domain.DatasetBoolean, domain.DatasetDatetime, domain.DatasetJSON:
		default:
			return fmt.Errorf("%w: invalid dataset type %q", ErrInvalidResult, column.Type)
		}
		if _, duplicate := columns[column.Name]; duplicate {
			return fmt.Errorf("%w: duplicate dataset column %q", ErrInvalidResult, column.Name)
		}
		columns[column.Name] = *column
	}
	change.UniqueKey = uniqueStrings(change.UniqueKey)
	for _, key := range change.UniqueKey {
		if _, exists := columns[key]; !exists {
			return fmt.Errorf("%w: unique key column %q is not in the dataset schema", ErrInvalidResult, key)
		}
	}
	return nil
}

func validateDatasetRows(dataset domain.Dataset, rows []map[string]any) error {
	return validateDatasetRowsForOperation(dataset, rows, true)
}

func validateDatasetRowsForOperation(dataset domain.Dataset, rows []map[string]any, requireUniqueKey bool) error {
	if requireUniqueKey && len(dataset.UniqueKey) == 0 {
		return fmt.Errorf("%w: dataset %q has no unique key for upsert", ErrInvalidResult, dataset.ID)
	}
	if len(rows) == 0 || len(rows) > maxChatDatasetRows {
		return fmt.Errorf("%w: dataset upsert requires 1-%d rows", ErrInvalidResult, maxChatDatasetRows)
	}
	encoded, err := json.Marshal(rows)
	if err != nil || len(encoded) > maxChatDatasetPayload {
		return fmt.Errorf("%w: dataset row payload exceeds %d bytes", ErrInvalidResult, maxChatDatasetPayload)
	}
	columns := make(map[string]domain.DatasetColumn, len(dataset.Schema))
	for _, column := range dataset.Schema {
		columns[column.Name] = column
	}
	for rowIndex, row := range rows {
		if row == nil {
			return fmt.Errorf("%w: dataset row %d is empty", ErrInvalidResult, rowIndex)
		}
		for key, value := range row {
			column, exists := columns[key]
			if !exists || !validChatDatasetValue(column, value) {
				return fmt.Errorf("%w: dataset row %d has invalid value for %q", ErrInvalidResult, rowIndex, key)
			}
		}
		for _, column := range dataset.Schema {
			value, exists := row[column.Name]
			if !exists || value == nil {
				if !column.Nullable {
					return fmt.Errorf("%w: dataset row %d requires %q", ErrInvalidResult, rowIndex, column.Name)
				}
			}
		}
	}
	return nil
}

func validChatDatasetValue(column domain.DatasetColumn, value any) bool {
	if value == nil {
		return column.Nullable
	}
	switch column.Type {
	case domain.DatasetString:
		text, ok := value.(string)
		return ok && len(text) <= 256*1024
	case domain.DatasetInteger:
		number, ok := chatNumber(value)
		return ok && math.Trunc(number) == number && number >= math.MinInt64 && number <= math.MaxInt64
	case domain.DatasetNumber:
		number, ok := chatNumber(value)
		return ok && !math.IsInf(number, 0) && !math.IsNaN(number)
	case domain.DatasetBoolean:
		_, ok := value.(bool)
		return ok
	case domain.DatasetDatetime:
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, text)
		return err == nil
	case domain.DatasetJSON:
		encoded, err := json.Marshal(value)
		return err == nil && len(encoded) <= 256*1024
	default:
		return false
	}
}

func chatNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func normalizeTaskChange(change TaskChange, creating bool, workspaces map[string]struct{}) (TaskChange, error) {
	change.Title = strings.TrimSpace(change.Title)
	change.Purpose = strings.TrimSpace(change.Purpose)
	change.Why = strings.TrimSpace(change.Why)
	change.WorkspaceID = strings.TrimSpace(change.WorkspaceID)
	if runeCount(change.Title) > maxTaskTitleRunes || runeCount(change.Purpose) > maxTaskTextRunes || runeCount(change.Why) > maxTaskTextRunes {
		return TaskChange{}, fmt.Errorf("%w: task fields exceed size limits", ErrInvalidResult)
	}
	if creating && (change.Title == "" || change.Purpose == "") {
		return TaskChange{}, fmt.Errorf("%w: create_task requires title and purpose", ErrInvalidResult)
	}
	priority, valid := normalizePriority(change.Priority, creating)
	if !valid {
		return TaskChange{}, fmt.Errorf("%w: invalid priority %q", ErrInvalidResult, change.Priority)
	}
	change.Priority = priority
	status, valid := normalizeTaskStatus(change.Status)
	if !valid {
		return TaskChange{}, fmt.Errorf("%w: invalid task status %q", ErrInvalidResult, change.Status)
	}
	change.Status = status
	if creating && change.Status != "" {
		// A newly created task normally enters Ready, so Codex may redundantly
		// include that value even though queue placement remains daemon-owned.
		// Accept and discard only that harmless default; every other explicit
		// lifecycle choice still fails closed.
		if change.Status != domain.TaskReady {
			return TaskChange{}, fmt.Errorf("%w: create_task cannot set lifecycle status", ErrHighImpactEffect)
		}
		change.Status = ""
	}
	if change.WorkspaceID != "" {
		if _, allowed := workspaces[change.WorkspaceID]; !allowed {
			return TaskChange{}, fmt.Errorf("%w: workspace %q is not approved", ErrInvalidResult, change.WorkspaceID)
		}
	}
	if change.DefinitionOfDone != nil {
		change.DefinitionOfDone = uniqueStrings(change.DefinitionOfDone)
		if len(change.DefinitionOfDone) == 0 {
			return TaskChange{}, fmt.Errorf("%w: definition_of_done cannot be empty", ErrInvalidResult)
		}
	}
	if change.DependsOnTaskIDs != nil {
		change.DependsOnTaskIDs = uniqueStrings(change.DependsOnTaskIDs)
	}
	if creating && len(change.DefinitionOfDone) == 0 {
		return TaskChange{}, fmt.Errorf("%w: create_task requires definition_of_done", ErrInvalidResult)
	}
	if change.PlannedAt != nil {
		planned := change.PlannedAt.UTC()
		if planned.After(time.Now().UTC().Add(14 * 24 * time.Hour)) {
			return TaskChange{}, fmt.Errorf("%w: task planned_at exceeds the 14-day planning horizon", ErrInvalidResult)
		}
		change.PlannedAt = &planned
	}
	return change, nil
}

func normalizePriority(priority domain.Priority, defaultNormal bool) (domain.Priority, bool) {
	switch normalizeToken(string(priority)) {
	case "":
		if defaultNormal {
			return domain.PriorityNormal, true
		}
		return "", true
	case "high":
		return domain.PriorityHigh, true
	case "normal", "medium", "default":
		return domain.PriorityNormal, true
	case "low":
		return domain.PriorityLow, true
	default:
		return "", false
	}
}

func normalizeTaskStatus(status domain.TaskStatus) (domain.TaskStatus, bool) {
	switch normalizeToken(string(status)) {
	case "":
		return "", true
	case "idea":
		return domain.TaskIdea, true
	case "ready", "queued", "pending":
		return domain.TaskReady, true
	case "waiting", "blocked":
		return domain.TaskWaiting, true
	case "running", "in_progress":
		return domain.TaskRunning, true
	case "needs_approval", "approval":
		return domain.TaskNeedsApproval, true
	case "completed", "complete", "done":
		return domain.TaskCompleted, true
	case "failed", "error":
		return domain.TaskFailed, true
	case "cancelled", "canceled":
		return domain.TaskCancelled, true
	default:
		return "", false
	}
}

func allowedSteeringStatus(status domain.TaskStatus) bool {
	return status == domain.TaskIdea || status == domain.TaskReady || status == domain.TaskWaiting
}

func normalizeCategory(category ActionCategory) (ActionCategory, bool) {
	switch ActionCategory(normalizeToken(string(category))) {
	case CategoryRead:
		return CategoryRead, true
	case CategoryWork:
		return CategoryWork, true
	case CategoryPublish:
		return CategoryPublish, true
	case CategoryDangerous:
		return CategoryDangerous, true
	default:
		return "", false
	}
}

func normalizeEffectDecision(decision PolicyDecision) (PolicyDecision, bool) {
	switch normalizeToken(string(decision)) {
	case "allow", "allowed", "auto", "automatic":
		return DecisionAllow, true
	case "ask", "approval", "approval_required", "require_approval", "deny":
		return DecisionAsk, true
	default:
		return "", false
	}
}

func taskChangeHasValue(change TaskChange) bool {
	return change.Title != "" || change.Purpose != "" || change.Why != "" || change.Priority != "" ||
		change.Status != "" || change.DefinitionOfDone != nil || change.DependsOnTaskIDs != nil || change.WorkspaceID != "" || change.PlannedAt != nil
}

func validateTaskDependencies(dependencies []string, taskID, workspaceID string, tasks map[string]domain.Task) error {
	for _, dependencyID := range dependencies {
		dependencyID = strings.TrimSpace(dependencyID)
		dependency, exists := tasks[dependencyID]
		if dependencyID == "" || !exists {
			return fmt.Errorf("%w: task dependency references unknown task %q", ErrInvalidResult, dependencyID)
		}
		if taskID != "" && dependencyID == taskID {
			return fmt.Errorf("%w: task cannot depend on itself", ErrInvalidResult)
		}
		if workspaceID != "" && dependency.WorkspaceID != workspaceID {
			return fmt.Errorf("%w: task dependency %q belongs to another workspace", ErrInvalidResult, dependencyID)
		}
	}
	if taskID == "" {
		return nil
	}
	graph := make(map[string][]string, len(tasks))
	for id, task := range tasks {
		graph[id] = append([]string(nil), task.DependsOnTaskIDs...)
	}
	graph[taskID] = append([]string(nil), dependencies...)
	visiting, visited := map[string]bool{}, map[string]bool{}
	var cycle func(string) bool
	cycle = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, next := range graph[id] {
			if cycle(next) {
				return true
			}
		}
		visiting[id], visited[id] = false, true
		return false
	}
	if cycle(taskID) {
		return fmt.Errorf("%w: task dependencies contain a cycle", ErrInvalidResult)
	}
	return nil
}

func extraneousFields(effect Effect, allowed map[string]bool) []string {
	var fields []string
	check := func(name string, present bool) {
		if present && !allowed[name] {
			fields = append(fields, name)
		}
	}
	check("task_id", effect.TaskID != "")
	check("app_id", effect.AppID != "")
	check("approval_id", effect.ApprovalID != "")
	check("task", effect.Task != nil)
	check("mission", effect.Mission != nil)
	check("context", effect.Context != nil)
	check("choice", effect.Choice != nil)
	check("policy", effect.Policy != nil)
	check("report", effect.Report != nil)
	check("plan", effect.Plan != nil)
	check("schedule", effect.Schedule != nil)
	check("secret", effect.Secret != nil)
	check("script", effect.Script != nil)
	check("local_app", effect.LocalApp != nil)
	check("dataset", effect.Dataset != nil)
	check("dataset_rows", effect.DatasetRows != nil)
	check("dataset_row", effect.DatasetRow != nil)
	check("soul", effect.Soul != nil)
	check("note", effect.Note != "")
	return fields
}

func invalidPayload(effectType EffectType, fields []string) error {
	return fmt.Errorf("%w: %s has unsupported fields: %s", ErrInvalidResult, effectType, strings.Join(fields, ", "))
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := normalizedTitle(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizedTitle(value string) string {
	var result strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			result.WriteRune(character)
		} else {
			result.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(result.String()), " ")
}
