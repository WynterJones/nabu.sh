package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/appruntime"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/steering"
	"github.com/nabu-sh/nabu/internal/store"
)

const maximumChatMessageRunes = 32_000

const maximumInventoryEntries = 48

const (
	maximumChatDatasetReadRows  = 50
	maximumChatDatasetReadBytes = 256 * 1024
)

type chatEffectMetadata struct {
	Effects    []chatEffectView `json:"effects"`
	References []chatEntityRef  `json:"references"`
	Error      string           `json:"error,omitempty"`
}

type chatEffectView struct {
	Type    string         `json:"type"`
	Summary string         `json:"summary"`
	Entity  *chatEntityRef `json:"entity,omitempty"`
	Details []string       `json:"details,omitempty"`
	Actions []chatAction   `json:"actions,omitempty"`
}

type chatAction struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Primary     bool   `json:"primary,omitempty"`
}

type chatEntityRef struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type chatInputMetadata struct {
	RecoveryTaskID    string `json:"recovery_task_id,omitempty"`
	RecoveryTaskTitle string `json:"recovery_task_title,omitempty"`
}

func (o *Operator) ChatMessages(ctx context.Context, request api.ChatPageRequest) (api.ChatPage, error) {
	if request.Limit <= 0 {
		request.Limit = 10
	}
	filter := store.MessageFilter{
		BeforeID: request.BeforeID,
		Limit:    request.Limit + 1,
	}
	if request.ThreadRootID > 0 {
		filter.ThreadRootID = request.ThreadRootID
	} else {
		filter.TopLevelOnly = true
	}
	messages, err := o.store.ListMessages(ctx, filter)
	if err != nil {
		return api.ChatPage{}, translateNotFound(err)
	}

	page := api.ChatPage{Messages: messages}
	if request.ThreadRootID > 0 {
		// Thread pages always include their root. The page limit applies only to
		// replies, so the cursor must never point at the root itself.
		if len(messages) > request.Limit+1 {
			page.HasMore = true
			page.Messages = append(messages[:1:1], messages[2:]...)
		}
		if page.HasMore && len(page.Messages) > 1 {
			page.NextBeforeID = page.Messages[1].ID
		}
	} else if len(messages) > request.Limit {
		page.HasMore = true
		page.Messages = messages[1:]
		page.NextBeforeID = page.Messages[0].ID
	}
	if page.Messages == nil {
		page.Messages = []domain.Message{}
	}
	return page, nil
}

func (o *Operator) SendChat(ctx context.Context, input api.ChatSend) (domain.Message, error) {
	rawContent := strings.TrimSpace(input.Content)
	if containsLikelySecret(rawContent) {
		return domain.Message{}, fmt.Errorf("%w: for security, enter credentials in Settings > Secrets; secret values cannot be sent through Chat", api.ErrInvalid)
	}
	content := redactSecrets(rawContent)
	if content == "" {
		return domain.Message{}, fmt.Errorf("%w: message cannot be empty", api.ErrInvalid)
	}
	if len([]rune(content)) > maximumChatMessageRunes {
		return domain.Message{}, fmt.Errorf("%w: message exceeds %d characters", api.ErrInvalid, maximumChatMessageRunes)
	}
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return domain.Message{}, err
	}
	if !settings.SetupComplete || !settings.MissionStarted {
		return domain.Message{}, fmt.Errorf("%w: start this workspace's mission before messaging Nabu", api.ErrConflict)
	}
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return domain.Message{}, translateNotFound(err)
	}
	var parentID *int64
	if input.ParentMessageID > 0 {
		parent, getErr := o.store.GetMessage(ctx, input.ParentMessageID)
		if getErr != nil {
			return domain.Message{}, translateNotFound(getErr)
		}
		if parent.WorkspaceID != workspace.ID {
			return domain.Message{}, api.ErrNotFound
		}
		rootID := parent.ID
		if parent.ThreadRootID != nil {
			rootID = *parent.ThreadRootID
		}
		parentID = &rootID
	}
	var inputMetadata json.RawMessage
	if input.RecoveryTaskID != "" {
		encoded, encodeErr := json.Marshal(chatInputMetadata{
			RecoveryTaskID:    input.RecoveryTaskID,
			RecoveryTaskTitle: strings.TrimSpace(input.RecoveryTaskTitle),
		})
		if encodeErr != nil {
			return domain.Message{}, encodeErr
		}
		inputMetadata = encoded
	}
	message, err := o.store.AppendMessage(ctx, domain.Message{
		WorkspaceID: workspace.ID, ParentMessageID: parentID,
		Role: domain.MessageUser, Content: content, Status: domain.MessageQueued, Effect: domain.EffectConversationOnly, EffectMetadata: inputMetadata,
	})
	if err != nil {
		return domain.Message{}, err
	}
	o.emitForWorkspace(ctx, workspace.ID, "chat.message", strconv.FormatInt(message.ID, 10), message)
	o.signalChat()
	return message, nil
}

func (o *Operator) ChatWorking() bool {
	active := o.ChatActive()
	if active {
		return true
	}
	open, err := o.store.OpenMessageCount(context.Background())
	return err == nil && open > 0
}

// ChatActive reports execution, not backlog. ChatWorking remains the broader
// drain-state helper used by queue coordination and shutdown tests.
func (o *Operator) ChatActive() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.chatActive
}

func (o *Operator) ChatQueueDepth(ctx context.Context) (int, error) {
	return o.store.QueuedMessageCount(ctx)
}

// DeleteChatMessage permanently removes one message. SQLite's foreign-key
// cascade removes every reply when the selected message is a thread root.
func (o *Operator) DeleteChatMessage(ctx context.Context, id int64) error {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return translateNotFound(err)
	}
	message, err := o.store.GetMessage(ctx, id)
	if err != nil {
		return translateNotFound(err)
	}
	if message.WorkspaceID != workspace.ID {
		return api.ErrNotFound
	}
	if message.Status == domain.MessageProcessing {
		return fmt.Errorf("%w: wait for Nabu to finish replying before deleting this message", api.ErrConflict)
	}
	threadRootID := int64(0)
	if message.ParentMessageID == nil {
		threadRootID = message.ID
	} else if message.ThreadRootID != nil {
		threadRootID = *message.ThreadRootID
	}
	if err := o.store.DeleteMessage(ctx, id); err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			return fmt.Errorf("%w: wait for Nabu to finish replying before deleting this message or thread", api.ErrConflict)
		}
		return translateNotFound(err)
	}
	o.emitForWorkspace(ctx, workspace.ID, "chat.message.deleted", strconv.FormatInt(id, 10), map[string]any{
		"message_id": id, "thread_root_id": threadRootID, "deleted_reply_count": message.ReplyCount,
	})
	return nil
}

func (o *Operator) endChat() {
	o.mu.Lock()
	o.chatActive = false
	o.chatCancel = nil
	o.mu.Unlock()
	o.signalChat()
}

func (o *Operator) chatRunContext() (context.Context, context.CancelFunc) {
	o.mu.Lock()
	parent := o.lifecycleContext
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	o.chatCancel = cancel
	o.mu.Unlock()
	return ctx, cancel
}

func (o *Operator) runChat(userMessage domain.Message, workspace domain.Workspace) {
	defer o.endChat()

	ctx, cancel := o.chatRunContext()
	defer cancel()
	pendingID := "pending-" + strconv.FormatInt(userMessage.ID, 10)
	threadRootID := int64(0)
	if userMessage.ThreadRootID != nil {
		threadRootID = *userMessage.ThreadRootID
	}
	o.publishTransient("chat.started", pendingID, map[string]any{
		"message_id": pendingID, "user_message_id": userMessage.ID, "thread_root_id": threadRootID,
	})
	o.emitForWorkspace(context.WithoutCancel(ctx), workspace.ID, "chat.message", strconv.FormatInt(userMessage.ID, 10), userMessage)
	o.publishChatActivity(pendingID, "context", "Reviewing workspace context")

	request, state, err := o.chatPacketRequest(ctx, workspace, userMessage)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			o.requeueChatMessage(workspace, userMessage, pendingID, domain.Run{}, runner.ExecutionResult{})
			return
		}
		o.finishChatFailure(ctx, workspace, userMessage, pendingID, domain.Run{}, runner.ExecutionResult{}, err)
		return
	}
	prompt, err := steering.BuildPacket(request)
	if err != nil {
		o.finishChatFailure(ctx, workspace, userMessage, pendingID, domain.Run{}, runner.ExecutionResult{}, err)
		return
	}
	record, err := o.store.CreateRun(ctx, domain.Run{
		Type: domain.RunChat, Status: domain.RunRunning,
		WorkingDirectory: workspace.Path, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			o.requeueChatMessage(workspace, userMessage, pendingID, domain.Run{}, runner.ExecutionResult{})
			return
		}
		o.finishChatFailure(ctx, workspace, userMessage, pendingID, domain.Run{}, runner.ExecutionResult{}, err)
		return
	}
	codexArgs, mcpSecrets, err := o.codexRunWithBrowser(ctx, workspace.ID, workspace.Path, true)
	if err != nil {
		o.finishChatFailure(ctx, workspace, userMessage, pendingID, record, runner.ExecutionResult{}, err)
		return
	}

	streamed := false
	execution, runErr := o.runner.Run(ctx, runner.Request{
		WorkingDirectory:  workspace.Path,
		Prompt:            prompt,
		Command:           o.codexCommand(ctx),
		Args:              codexArgs,
		SecretEnvironment: mcpSecrets,
		OnStart: func(started runner.ProcessStarted) {
			active, getErr := o.store.GetRun(context.WithoutCancel(ctx), record.ID)
			if getErr == nil {
				active.PID, active.Command, active.Attempt, active.StartedAt = started.PID, started.Command, started.Attempt, started.StartedAt
				_ = o.store.UpdateRun(context.WithoutCancel(ctx), active)
			}
			o.publishChatActivity(pendingID, "reasoning", "Considering the safest durable response")
		},
		OnOutput: func(output runner.OutputEvent) {
			if streamed || output.Stream != runner.OutputStdout || len(output.JSON) == 0 {
				return
			}
			if response, ok := chatAssistantResponse(output.JSON, state); ok {
				streamed = true
				o.publishTransient("chat.delta", pendingID, map[string]any{"message_id": pendingID, "delta": response})
			}
		},
	})
	o.recordCodexExecution(execution, runErr)
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			o.requeueChatMessage(workspace, userMessage, pendingID, record, execution)
			return
		}
		o.finishChatFailure(context.WithoutCancel(ctx), workspace, userMessage, pendingID, record, execution, runErr)
		return
	}
	o.publishChatActivity(pendingID, "validation", "Validating proposed changes")
	result, err := steering.ParseResult(execution.Stdout, state)
	if err != nil {
		o.publishChatActivity(pendingID, "validation", "Repairing the structured response")
		repairArgs, repairSecrets, runtimeErr := o.codexRun(ctx, workspace.ID)
		if runtimeErr == nil {
			repairPrompt := steering.BuildRepairPacket(prompt, execution.Stdout, err)
			repairExecution, repairErr := o.runner.Run(ctx, runner.Request{
				WorkingDirectory: workspace.Path, Prompt: repairPrompt, Command: o.codexCommand(ctx), Args: repairArgs, SecretEnvironment: repairSecrets,
				OnStart: func(started runner.ProcessStarted) {
					record.PID, record.Command, record.Attempt, record.StartedAt = started.PID, started.Command, started.Attempt, started.StartedAt
					_ = o.store.UpdateRun(context.WithoutCancel(ctx), record)
				},
			})
			o.recordCodexExecution(repairExecution, repairErr)
			if repairErr == nil {
				execution = repairExecution
				result, err = steering.ParseResult(execution.Stdout, state)
			} else {
				err = repairErr
			}
		} else {
			err = runtimeErr
		}
		if err != nil {
			o.finishChatFailure(context.WithoutCancel(ctx), workspace, userMessage, pendingID, record, execution, fmt.Errorf("structured response remained invalid after one repair: %w", err))
			return
		}
	}
	if !streamed {
		o.publishTransient("chat.delta", pendingID, map[string]any{"message_id": pendingID, "delta": result.AssistantResponse})
	}
	o.publishChatActivity(pendingID, "apply", "Applying durable updates")
	metadata, applyErr := o.applyChatEffects(context.WithoutCancel(ctx), result.Effects, workspace)
	content := result.AssistantResponse
	if applyErr != nil {
		metadata.Error = applyErr.Error()
		content += "\n\nI could not apply every proposed change. The changes shown below are the ones that were committed; please review the remaining request."
	}
	encoded, _ := json.Marshal(metadata)
	effect := domain.EffectConversationOnly
	if len(result.Effects) > 0 {
		effect = domain.ChatEffect(result.Effects[0].Type)
	}
	userMessage.Status = domain.MessageComplete
	userMessage.UpdatedAt = time.Now().UTC()
	if err := o.store.UpdateMessage(context.WithoutCancel(ctx), userMessage); err != nil {
		o.finishChatFailure(context.WithoutCancel(ctx), workspace, userMessage, pendingID, record, execution, err)
		return
	}
	o.emitForWorkspace(context.WithoutCancel(ctx), workspace.ID, "chat.message", strconv.FormatInt(userMessage.ID, 10), userMessage)
	assistant, err := o.store.AppendMessage(context.WithoutCancel(ctx), domain.Message{
		WorkspaceID: workspace.ID, ParentMessageID: userMessage.ThreadRootID,
		Role: domain.MessageAssistant, Content: redactSecrets(content), Effect: effect, EffectMetadata: encoded,
	})
	if err != nil {
		o.finishChatFailure(context.WithoutCancel(ctx), workspace, userMessage, pendingID, record, execution, err)
		return
	}
	o.finishChatRun(context.WithoutCancel(ctx), &record, execution, content, applyErr, workspace.ID)
	o.emitForWorkspace(context.WithoutCancel(ctx), workspace.ID, "chat.message", strconv.FormatInt(assistant.ID, 10), assistant)
	o.publishTransient("chat.completed", strconv.FormatInt(assistant.ID, 10), map[string]any{
		"message_id": assistant.ID, "pending_message_id": pendingID, "thread_root_id": threadRootID,
	})
}

func (o *Operator) chatPacketRequest(ctx context.Context, workspace domain.Workspace, user domain.Message) (steering.PacketRequest, steering.ValidationState, error) {
	mission, err := o.store.GetMissionForWorkspace(ctx, workspace.ID)
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, translateNotFound(err)
	}
	policy, err := o.store.GetPolicyForWorkspace(ctx, workspace.ID)
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	tasks, err := o.store.ListTasks(ctx, store.TaskFilter{WorkspaceID: workspace.ID})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	approvals, err := o.store.ListApprovals(ctx, store.ApprovalFilter{WorkspaceID: workspace.ID, Statuses: []domain.ApprovalStatus{domain.ApprovalPending}, Limit: 25})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	summaries := make([]steering.ApprovalSummary, 0, len(approvals))
	for _, approval := range approvals {
		summaries = append(summaries, approvalSummary(approval))
	}
	filter := store.MessageFilter{WorkspaceID: workspace.ID, TopLevelOnly: true, Limit: 30}
	if user.ThreadRootID != nil {
		filter = store.MessageFilter{WorkspaceID: workspace.ID, ThreadRootID: *user.ThreadRootID, Limit: 30}
	}
	history, err := o.store.ListMessages(ctx, filter)
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	recent := make([]steering.Message, 0, len(history))
	for _, message := range history {
		if message.ID == user.ID || message.Status != domain.MessageComplete || (message.Role != domain.MessageUser && message.Role != domain.MessageAssistant) {
			continue
		}
		if automatedLifecycleMessage(message) {
			continue
		}
		recent = append(recent, steering.Message{
			ID: strconv.FormatInt(message.ID, 10), Role: string(message.Role), Content: message.Content,
			CreatedAt: message.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	scope, err := config.EnsureScope(o.paths, workspace.ID)
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	memory, _ := readBounded(scope.MemoryFile, 128*1024)
	soul, _ := readBounded(o.paths.Soul, 64*1024)
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	inventory, err := inspectWorkspaceInventory(workspace.Path)
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	workspaceFiles := chatWorkspaceFiles(workspace, user.Content)
	schedules, err := o.store.ListSchedules(ctx, store.ScheduleFilter{WorkspaceID: workspace.ID, Limit: 25})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	plans, err := o.store.ListPlans(ctx, store.PlanFilter{WorkspaceID: workspace.ID, Statuses: []domain.PlanStatus{domain.PlanProposed, domain.PlanActive}, Limit: 12})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	secretRecords, err := o.store.ListSecretRecords(ctx, store.SecretRecordFilter{WorkspaceID: workspace.ID, Limit: 32})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	secretSummaries := make([]steering.SecretSummary, 0, len(secretRecords))
	secretNamesByID := make(map[string]string, len(secretRecords))
	for _, record := range secretRecords {
		configured, configuredErr := o.secretConfigured(ctx, record)
		if configuredErr != nil {
			return steering.PacketRequest{}, steering.ValidationState{}, configuredErr
		}
		secretSummaries = append(secretSummaries, steering.SecretSummary{ID: record.ID, Name: record.Name, Description: record.Description, Configured: configured})
		secretNamesByID[record.ID] = record.Name
	}
	scriptRecords, err := o.store.ListScripts(ctx, store.ScriptFilter{WorkspaceID: workspace.ID, Limit: 32})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	scriptSummaries := make([]steering.ScriptSummary, 0, len(scriptRecords))
	for _, script := range scriptRecords {
		names := make([]string, 0, len(script.CredentialBindings))
		for _, binding := range script.CredentialBindings {
			if name := secretNamesByID[binding.SecretRecordID]; name != "" {
				names = append(names, name)
			}
		}
		scriptSummaries = append(scriptSummaries, steering.ScriptSummary{ID: script.ID, Name: script.Name, Description: script.Description, Access: string(script.Access), SecretNames: names})
	}
	localAppRecords, err := o.store.ListLocalApps(ctx, store.LocalAppFilter{WorkspaceID: workspace.ID, Limit: 24})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	localAppSummaries := make([]steering.LocalAppSummary, 0, len(localAppRecords))
	for _, app := range localAppRecords {
		status := string(appruntime.StatusStopped)
		if o.appRuntime != nil {
			status = string(o.appRuntime.State(app.ID).Status)
		}
		localAppSummaries = append(localAppSummaries, steering.LocalAppSummary{
			ID: app.ID, Name: app.Name, Description: app.Description, Directory: app.Directory,
			Command: append([]string(nil), app.Command...), Port: app.Port, HealthPath: app.HealthPath,
			Status: status, URL: localAppURL(app), AutoStart: app.AutoStart,
		})
	}
	mcpRecords, err := o.store.ListMCPServers(ctx, store.MCPServerFilter{WorkspaceID: workspace.ID, Limit: 32})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	mcpSummaries := make([]steering.MCPServerSummary, 0, len(mcpRecords))
	for _, server := range mcpRecords {
		o.hydrateMCPReadiness(ctx, &server)
		mcpSummaries = append(mcpSummaries, steering.MCPServerSummary{
			ID: server.ID, Name: server.Name, Description: server.Description, Transport: string(server.Transport),
			Access: string(server.Access), Ready: server.Ready, ToolAllowlist: append([]string(nil), server.EnabledTools...),
		})
	}
	if discoverBuiltInBrowserRuntime().Available {
		mcpSummaries = append(mcpSummaries, steering.MCPServerSummary{
			ID: "built-in-browser", Name: builtInBrowserMCPName,
			Description: "Built-in isolated Chrome tools for visual QA, screenshots, responsive checks, and UI interaction.",
			Transport:   "stdio", Access: "full", Ready: true,
			ToolAllowlist: []string{"browser_navigate", "browser_snapshot", "browser_take_screenshot", "browser_resize", "browser_click", "browser_type"},
		})
	}
	datasets, err := o.store.ListDatasets(ctx, store.DatasetFilter{WorkspaceID: workspace.ID, Limit: 24})
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	datasetQueries, err := o.chatDatasetQueryContext(ctx, workspace.ID, user.Content, datasets)
	if err != nil {
		return steering.PacketRequest{}, steering.ValidationState{}, err
	}
	scheduleRequested := explicitlyRequestsScheduling(user.Content)
	contextConfirmed := explicitlyConfirmsContextReady(user.Content) || confirmsContextReadyReply(user.Content, recent)
	inputMetadata := chatInputMetadata{}
	_ = json.Unmarshal(user.EffectMetadata, &inputMetadata)
	if inputMetadata.RecoveryTaskID != "" {
		found := false
		for _, task := range tasks {
			if task.ID == inputMetadata.RecoveryTaskID && (task.Status == domain.TaskFailed || task.Status == domain.TaskWaiting) {
				found = true
				break
			}
		}
		if !found {
			return steering.PacketRequest{}, steering.ValidationState{}, fmt.Errorf("%w: recovery task is not available", api.ErrConflict)
		}
	}
	state := steering.ValidationState{
		Tasks: tasks, Workspaces: []domain.Workspace{workspace}, PendingApprovals: summaries,
		Plans: plans, Datasets: datasets, DatasetQueries: datasetQueries, Secrets: secretSummaries, LocalApps: localAppSummaries, ScheduleRequested: scheduleRequested,
		ContextGateEnabled: true, ContextReady: workspace.ContextReady, ContextConfirmed: contextConfirmed, RecoveryTaskID: inputMetadata.RecoveryTaskID,
	}
	return steering.PacketRequest{
		DisplayName: settings.DisplayName, WorkspaceRoot: workspace.Path, Mission: mission, Policy: policy,
		DurableContext: redactSecrets(memory), Soul: redactSecrets(soul), Queue: tasks, PendingApprovals: summaries,
		Inventory: inventory, WorkspaceFiles: workspaceFiles, Schedules: schedules, Plans: plans, Secrets: secretSummaries, Scripts: scriptSummaries, LocalApps: localAppSummaries, MCPServers: mcpSummaries, Datasets: datasets, DatasetQueries: datasetQueries,
		ScheduleRequested: scheduleRequested, ContextGateEnabled: true, ContextReady: workspace.ContextReady, ContextConfirmed: contextConfirmed,
		RecoveryTaskID: inputMetadata.RecoveryTaskID,
		RecentMessages: recent, UserMessage: user.Content,
	}, state, nil
}

// chatDatasetQueryContext performs at most one bounded, server-validated row
// query when the latest message names a dataset. A quoted phrase becomes the
// search term; otherwise the first page is provided. No query state is copied
// into durable conversation history.
func (o *Operator) chatDatasetQueryContext(ctx context.Context, workspaceID, message string, datasets []domain.Dataset) ([]steering.DatasetQueryContext, error) {
	messageLower := strings.ToLower(message)
	var selected *domain.Dataset
	selectedLength := 0
	for index := range datasets {
		for _, candidate := range []string{datasets[index].ID, datasets[index].Slug, datasets[index].Name} {
			candidate = strings.TrimSpace(candidate)
			if len(candidate) > selectedLength && strings.Contains(messageLower, strings.ToLower(candidate)) {
				selected, selectedLength = &datasets[index], len(candidate)
			}
		}
	}
	if selected == nil {
		return []steering.DatasetQueryContext{}, nil
	}
	search := quotedDatasetSearch(message, *selected)
	page, err := o.store.QueryDatasetRows(ctx, selected.ID, store.DatasetRowFilter{
		WorkspaceID: workspaceID, Limit: maximumChatDatasetReadRows, Search: search,
	})
	if err != nil {
		return nil, translateDatasetError(err)
	}
	result := steering.DatasetQueryContext{
		DatasetID: selected.ID, Name: selected.Name, Search: search, Rows: []domain.DatasetRow{},
		Total: page.Total, Truncated: page.NextCursor != "",
	}
	for _, row := range page.Rows {
		probe := result
		probe.Rows = append(append([]domain.DatasetRow(nil), result.Rows...), row)
		encoded, marshalErr := json.Marshal(probe)
		if marshalErr != nil || len(encoded) > maximumChatDatasetReadBytes {
			result.Truncated = true
			break
		}
		result.Rows = probe.Rows
	}
	if len(result.Rows) < len(page.Rows) {
		result.Truncated = true
	}
	return []steering.DatasetQueryContext{result}, nil
}

func quotedDatasetSearch(message string, dataset domain.Dataset) string {
	for _, quote := range []byte{'"', '\''} {
		start := strings.IndexByte(message, quote)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(message[start+1:], quote)
		if end < 0 {
			continue
		}
		value := strings.TrimSpace(message[start+1 : start+1+end])
		if value != "" && len(value) <= 512 && !strings.EqualFold(value, dataset.Name) && !strings.EqualFold(value, dataset.Slug) && value != dataset.ID {
			return value
		}
	}
	return ""
}

// inspectWorkspaceInventory reads directory metadata only. It never opens a
// workspace file and never descends beyond checking a top-level .git marker.
func inspectWorkspaceInventory(root string) (steering.WorkspaceInventory, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return steering.WorkspaceInventory{}, fmt.Errorf("inspect workspace inventory: %w", err)
	}
	result := steering.WorkspaceInventory{Empty: len(entries) == 0, Entries: []steering.InventoryEntry{}}
	if info, statErr := os.Lstat(filepath.Join(root, ".git")); statErr == nil && (info.IsDir() || info.Mode().IsRegular()) {
		result.RootRepo = true
	}
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".DS_Store" {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			result.FileCount++
			continue
		}
		if len(result.Entries) == maximumInventoryEntries {
			result.Omitted++
			continue
		}
		kind := "folder"
		if info, statErr := os.Lstat(filepath.Join(root, entry.Name(), ".git")); statErr == nil && (info.IsDir() || info.Mode().IsRegular()) {
			kind = "repository"
		}
		result.Entries = append(result.Entries, steering.InventoryEntry{Name: entry.Name(), Kind: kind})
	}
	sort.Slice(result.Entries, func(left, right int) bool { return result.Entries[left].Name < result.Entries[right].Name })
	return result, nil
}

func explicitlyRequestsScheduling(message string) bool {
	value := strings.ToLower(strings.Join(strings.Fields(message), " "))
	for _, phrase := range []string{"schedule ", "schedule a ", "create a schedule", "set up a schedule", "add a schedule", "remind me", "run this every", "run it every", "recurring task", "recurring orientation"} {
		if strings.HasPrefix(value, phrase) || strings.Contains(value, " "+phrase) {
			return true
		}
	}
	return false
}

func explicitlyConfirmsContextReady(message string) bool {
	value := strings.ToLower(strings.Join(strings.Fields(message), " "))
	if strings.Contains(value, "?") || strings.HasPrefix(value, "do ") || strings.HasPrefix(value, "does ") || strings.HasPrefix(value, "tell me") {
		return false
	}
	for _, phrase := range []string{
		"i confirm this workspace context is sufficient", "you have enough context", "that is enough context",
		"that's enough context", "ready to begin", "begin work", "start working", "start the work",
		"proceed with the assumptions", "go ahead and begin", "go ahead and start",
		"approve and begin", "approve workspace context", "confirm workspace context", "context is ready",
	} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func confirmsContextReadyReply(message string, recent []steering.Message) bool {
	value := strings.Trim(strings.ToLower(strings.Join(strings.Fields(message), " ")), " .!")
	switch value {
	case "yes", "yes please", "yep", "yeah", "correct", "confirmed", "confirm", "approved", "approve", "go ahead", "do it", "sounds good":
	default:
		return false
	}
	for index := len(recent) - 1; index >= 0; index-- {
		if recent[index].Role != "assistant" {
			continue
		}
		prior := strings.ToLower(recent[index].Content)
		return strings.Contains(prior, "context") && (strings.Contains(prior, "enough") || strings.Contains(prior, "ready")) &&
			(strings.Contains(prior, "confirm") || strings.Contains(prior, "should i treat") || strings.Contains(prior, "approve"))
	}
	return false
}

func approvalSummary(approval domain.Approval) steering.ApprovalSummary {
	evidence := strings.Split(strings.TrimSpace(approval.Evidence), "\n")
	if len(evidence) == 1 && evidence[0] == "" {
		evidence = []string{}
	}
	return steering.ApprovalSummary{
		ID: approval.ID, TaskID: approval.TaskID, RunID: approval.RunID,
		Action: approval.ProposedAction, Category: steering.CategoryDangerous,
		Reason: approval.Why, Change: approval.ProposedChange, Evidence: evidence,
		Status: steering.ApprovalStatus(approval.Status),
	}
}

func (o *Operator) approvalContinuation(ctx context.Context, task domain.Task, mission domain.Mission, policy domain.Policy) (string, error) {
	approvals, err := o.store.ListApprovals(ctx, store.ApprovalFilter{
		WorkspaceID: task.WorkspaceID,
		TaskID:      task.ID,
		Statuses:    []domain.ApprovalStatus{domain.ApprovalApproved},
		Limit:       1,
	})
	if err != nil || len(approvals) == 0 {
		return "", err
	}
	approval := approvals[0]
	if approval.ResolvedAt == nil || approval.ResolvedAt.Before(task.UpdatedAt) {
		return "", nil
	}
	packet, err := steering.BuildApprovalContinuationPacket(steering.ApprovalContinuationRequest{
		Approval:   approvalSummary(approval),
		Resolution: steering.ResolutionApproved,
		Mission:    mission,
		Policy:     policy,
		Task:       &task,
	})
	if err != nil {
		return "", err
	}
	return packet, nil
}

func chatAssistantResponse(raw json.RawMessage, state steering.ValidationState) (string, bool) {
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Item.Type != "agent_message" || strings.TrimSpace(event.Item.Text) == "" {
		return "", false
	}
	result, err := steering.ParseResult(event.Item.Text, state)
	return result.AssistantResponse, err == nil
}

func (o *Operator) applyChatEffects(ctx context.Context, effects []steering.Effect, workspace domain.Workspace) (chatEffectMetadata, error) {
	metadata := chatEffectMetadata{Effects: []chatEffectView{}, References: []chatEntityRef{}}
	for _, effect := range effects {
		view, err := o.applyChatEffect(ctx, effect, workspace)
		if err != nil {
			return metadata, err
		}
		metadata.Effects = append(metadata.Effects, view)
		if view.Entity != nil {
			metadata.References = append(metadata.References, *view.Entity)
		}
	}
	return metadata, nil
}

func (o *Operator) applyChatEffect(ctx context.Context, effect steering.Effect, workspace domain.Workspace) (chatEffectView, error) {
	view := chatEffectView{Type: string(effect.Type)}
	switch effect.Type {
	case steering.EffectConversationOnly:
		view.Summary = "No durable state changed."
	case steering.EffectProposeContext:
		view.Summary = "Context gathering is complete and ready for approval."
		view.Entity = &chatEntityRef{ID: workspace.ID, Type: "context_approval", Title: "Workspace context", Status: "pending", Summary: "Approve this context to let Nabu begin autonomous work."}
	case steering.EffectRequestChoice:
		view.Summary = effect.Choice.Prompt
		if effect.Choice.Description != "" {
			view.Details = []string{effect.Choice.Description}
		}
		view.Actions = make([]chatAction, 0, len(effect.Choice.Options))
		for _, option := range effect.Choice.Options {
			view.Actions = append(view.Actions, chatAction{
				Label: option.Label, Value: option.Value, Description: option.Description, Primary: option.Primary,
			})
		}
	case steering.EffectCompleteContext:
		current, err := o.store.GetWorkspace(ctx, workspace.ID)
		if err != nil {
			return view, err
		}
		current.ContextReady, current.ContextPrompted = true, true
		if err := o.store.UpdateWorkspace(ctx, current); err != nil {
			return view, err
		}
		_, _ = o.store.RequestOrientationForWorkspace(ctx, workspace.ID)
		o.emitForWorkspace(ctx, workspace.ID, "context.ready", workspace.ID, map[string]bool{"context_ready": true})
		o.signal()
		view.Summary = "Context confirmed. Nabu can begin real work."
		view.Entity = &chatEntityRef{ID: workspace.ID, Type: "context", Title: "Workspace context", Status: "ready"}
	case steering.EffectCreateTask:
		workspaceID := effect.Task.WorkspaceID
		if workspaceID == "" {
			workspaceID = workspace.ID
		}
		task, err := o.CreateTask(ctx, api.TaskCreate{
			Title: effect.Task.Title, Purpose: effect.Task.Purpose, Why: effect.Task.Why,
			Priority: effect.Task.Priority, DefinitionOfDone: effect.Task.DefinitionOfDone, DependsOnTaskIDs: effect.Task.DependsOnTaskIDs, WorkspaceID: workspaceID, PlannedAt: effect.Task.PlannedAt,
		})
		if err != nil {
			return view, err
		}
		task.CreatedBy = "chat"
		_ = o.store.UpdateTask(ctx, task)
		view.Summary = "Created task “" + task.Title + "”."
		view.Entity = &chatEntityRef{ID: task.ID, Type: "task", Title: task.Title, Status: string(task.Status), Summary: task.Purpose}
	case steering.EffectUpdateTask:
		current, err := o.Task(ctx, effect.TaskID)
		if err != nil {
			return view, err
		}
		if current.WorkspaceID != workspace.ID {
			return view, api.ErrNotFound
		}
		if effect.Task.WorkspaceID != "" && effect.Task.WorkspaceID != current.WorkspaceID {
			return view, fmt.Errorf("%w: chat cannot move tasks between workspaces", api.ErrInvalid)
		}
		update := taskUpdateFromSteering(*effect.Task)
		updated, err := o.UpdateTask(ctx, current.ID, update)
		if err != nil {
			return view, err
		}
		view.Summary = "Updated task “" + updated.Title + "”."
		view.Entity = &chatEntityRef{ID: updated.ID, Type: "task", Title: updated.Title, Status: string(updated.Status), Summary: updated.Purpose}
	case steering.EffectCancelTask:
		status := domain.TaskCancelled
		updated, err := o.UpdateTask(ctx, effect.TaskID, api.TaskUpdate{Status: &status})
		if err != nil {
			return view, err
		}
		view.Summary = "Cancelled task “" + updated.Title + "”."
		view.Entity = &chatEntityRef{ID: updated.ID, Type: "task", Title: updated.Title, Status: string(updated.Status)}
	case steering.EffectUpdateMission:
		mission, err := o.store.GetMissionForWorkspace(ctx, workspace.ID)
		if err != nil {
			return view, err
		}
		updated, err := o.UpdateMission(ctx, api.MissionUpdate{Statement: effect.Mission.Statement, Context: mission.Context})
		if err != nil {
			return view, err
		}
		view.Summary = "Updated the workspace mission."
		view.Entity = &chatEntityRef{ID: updated.ID, Type: "mission", Title: "Mission", Summary: updated.Statement}
	case steering.EffectUpdateContext:
		proposal, err := o.store.CreateMemoryUpdate(ctx, domain.MemoryUpdate{
			WorkspaceID: workspace.ID, Target: domain.MemoryDurable,
			Content: redactSecrets(effect.Context.Value), Source: "chat", Status: domain.MemoryProposed,
		})
		if err != nil {
			return view, err
		}
		resolved, err := o.ResolveMemoryUpdate(ctx, proposal.ID, domain.MemoryApplied, "")
		if err != nil {
			return view, err
		}
		view.Summary = "Added durable workspace context."
		view.Entity = &chatEntityRef{ID: resolved.ID, Type: "memory", Title: "Memory update", Status: string(resolved.Status)}
	case steering.EffectUpdateSoul:
		proposal, err := o.store.CreateMemoryUpdate(ctx, domain.MemoryUpdate{
			WorkspaceID: workspace.ID, Target: domain.MemorySoul,
			Content: redactSecrets(effect.Soul.Reflection), Source: "chat", Status: domain.MemoryProposed,
		})
		if err != nil {
			return view, err
		}
		resolved, err := o.ResolveMemoryUpdate(ctx, proposal.ID, domain.MemoryApplied, "")
		if err != nil {
			return view, err
		}
		view.Summary = "Added a reflection to Nabu's character charter."
		view.Entity = &chatEntityRef{ID: resolved.ID, Type: "soul", Title: "Soul reflection", Status: string(resolved.Status)}
	case steering.EffectUpdatePolicy:
		policy, err := o.Policy(ctx)
		if err != nil {
			return view, err
		}
		setPolicyDecision(&policy, effect.Policy.Category, effect.Policy.Decision)
		updated, err := o.UpdatePolicy(ctx, policy)
		if err != nil {
			return view, err
		}
		view.Summary = fmt.Sprintf("Set %s actions to %s.", effect.Policy.Category, effect.Policy.Decision)
		view.Entity = &chatEntityRef{ID: workspace.ID, Type: "policy", Title: "Workspace policy", Summary: fmt.Sprintf("read %s · work %s · publish %s · dangerous %s", updated.Read, updated.Work, updated.Publish, updated.Dangerous)}
	case steering.EffectApproveAction, steering.EffectRejectAction:
		status := domain.ApprovalApproved
		if effect.Type == steering.EffectRejectAction {
			status = domain.ApprovalRejected
		}
		approval, err := o.ResolveApproval(ctx, effect.ApprovalID, status, effect.Note)
		if err != nil {
			return view, err
		}
		verb := "Approved"
		if status == domain.ApprovalRejected {
			verb = "Rejected"
		}
		view.Summary = verb + " “" + approval.ProposedAction + "”."
		view.Entity = &chatEntityRef{ID: approval.ID, Type: "approval", Title: approval.ProposedAction, Status: string(approval.Status), Summary: approval.ProposedChange}
	case steering.EffectPause:
		if err := o.setPausedFromChat(ctx, true); err != nil {
			return view, err
		}
		view.Summary = "Paused autonomous work."
	case steering.EffectResume:
		if err := o.setPausedFromChat(ctx, false); err != nil {
			return view, err
		}
		view.Summary = "Resumed autonomous work."
	case steering.EffectRequestReport:
		title := strings.TrimSpace(effect.Report.Title)
		if title == "" {
			title = "Requested report"
		}
		task, err := o.CreateTask(ctx, api.TaskCreate{
			Title:   "Prepare report: " + title,
			Purpose: "Produce a durable report covering " + effect.Report.Scope,
			Why:     "The owner requested this report through Nabu chat.", Priority: domain.PriorityNormal,
			DefinitionOfDone: []string{"The report is written, evidence-backed, and linked from Nabu."}, WorkspaceID: workspace.ID,
		})
		if err != nil {
			return view, err
		}
		task.CreatedBy = "chat"
		_ = o.store.UpdateTask(ctx, task)
		view.Summary = "Queued report “" + title + "”."
		view.Entity = &chatEntityRef{ID: task.ID, Type: "task", Title: task.Title, Status: string(task.Status), Summary: task.Purpose}
	case steering.EffectCreatePlan:
		items := make([]domain.PlanItem, 0, len(effect.Plan.Items))
		for position, proposed := range effect.Plan.Items {
			items = append(items, domain.PlanItem{
				Kind: proposed.Kind, Title: proposed.Title, Purpose: proposed.Purpose, Why: proposed.Why,
				PlannedAt: proposed.PlannedAt, Position: position, Status: domain.PlanItemProposed,
			})
		}
		plan, err := o.store.CreatePlan(ctx, domain.Plan{WorkspaceID: workspace.ID, Title: effect.Plan.Title, Objective: effect.Plan.Objective, Status: domain.PlanProposed, Items: items})
		if err != nil {
			return view, err
		}
		o.emitForWorkspace(ctx, workspace.ID, "plan.created", plan.ID, plan)
		view.Summary = "Proposed plan “" + plan.Title + "” for review."
		view.Entity = &chatEntityRef{ID: plan.ID, Type: "plan", Title: plan.Title, Status: string(plan.Status), Summary: plan.Objective}
	case steering.EffectCreateSchedule:
		input, err := scheduleInputFromChat(*effect.Schedule, workspace.ID)
		if err != nil {
			return view, err
		}
		schedule, err := o.scheduleFromInput(ctx, domain.Schedule{WorkspaceID: workspace.ID, Enabled: true}, input, true)
		if err != nil {
			return view, err
		}
		schedule, err = o.store.CreateSchedule(ctx, schedule)
		if err != nil {
			return view, err
		}
		o.emitForWorkspace(ctx, workspace.ID, "schedule.created", schedule.ID, schedule)
		view.Summary = "Created schedule “" + schedule.Name + "”."
		view.Entity = &chatEntityRef{ID: schedule.ID, Type: "schedule", Title: schedule.Name, Status: "enabled", Summary: scheduleCadenceSummary(schedule)}
	case steering.EffectCreateSecret:
		record, err := o.createSecretRecordForWorkspace(ctx, workspace.ID, api.SecretCreate{
			Name: effect.Secret.Name, Label: effect.Secret.Label, Description: effect.Secret.Description,
		})
		if err != nil {
			return view, err
		}
		view.Summary = "Opened protected setup for “" + record.Label + "”."
		view.Entity = &chatEntityRef{ID: record.ID, Type: "secret", Title: record.Label, Status: "setup_needed", Summary: record.Description}
	case steering.EffectCreateScript:
		script, err := o.createManagedScriptFromChat(ctx, workspace.ID, *effect.Script)
		if err != nil {
			return view, err
		}
		view.Summary = "Created secure script “" + script.Name + "”."
		view.Entity = &chatEntityRef{ID: script.ID, Type: "script", Title: script.Name, Status: "ready", Summary: script.Description}
	case steering.EffectCreateLocalApp:
		app, err := o.createLocalAppForWorkspace(ctx, workspace, api.LocalAppInput{
			Name: effect.LocalApp.Name, Description: effect.LocalApp.Description, Directory: effect.LocalApp.Directory,
			Command: effect.LocalApp.Command, Port: effect.LocalApp.Port, HealthPath: effect.LocalApp.HealthPath, AutoStart: effect.LocalApp.AutoStart,
		})
		if err != nil {
			return view, err
		}
		view.Summary = "Registered local app “" + app.Name + "”."
		view.Entity = &chatEntityRef{ID: app.ID, Type: "app", Title: app.Name, Status: app.Status, Summary: app.URL}
	case steering.EffectStartLocalApp:
		app, err := o.startLocalAppForWorkspace(ctx, workspace.ID, effect.AppID)
		if err != nil {
			return view, err
		}
		view.Summary = "Started local app “" + app.Name + "”."
		view.Entity = &chatEntityRef{ID: app.ID, Type: "app", Title: app.Name, Status: app.Status, Summary: app.URL}
	case steering.EffectStopLocalApp:
		app, err := o.stopLocalAppForWorkspace(ctx, workspace.ID, effect.AppID)
		if err != nil {
			return view, err
		}
		view.Summary = "Stopped local app “" + app.Name + "”."
		view.Entity = &chatEntityRef{ID: app.ID, Type: "app", Title: app.Name, Status: app.Status, Summary: app.Directory}
	case steering.EffectCreateDataset:
		dataset, err := o.store.CreateDataset(ctx, domain.Dataset{
			WorkspaceID: workspace.ID, Name: effect.Dataset.Name, Slug: effect.Dataset.Slug, Description: effect.Dataset.Description,
			Schema: effect.Dataset.Schema, UniqueKey: effect.Dataset.UniqueKey,
		})
		if err != nil {
			return view, translateDatasetError(err)
		}
		o.emitForWorkspace(ctx, workspace.ID, "dataset.created", dataset.ID, dataset)
		view.Summary = "Created dataset “" + dataset.Name + "”."
		view.Entity = &chatEntityRef{ID: dataset.ID, Type: "dataset", Title: dataset.Name, Status: "active", Summary: dataset.Description}
	case steering.EffectUpsertDatasetRows:
		dataset, err := o.store.GetDatasetForWorkspace(ctx, workspace.ID, effect.DatasetRows.DatasetID, false)
		if err != nil {
			return view, translateDatasetError(err)
		}
		written, err := o.store.BulkWriteDatasetRowsForWorkspace(ctx, workspace.ID, dataset.ID, effect.DatasetRows.Rows, store.DatasetUpsert)
		if err != nil {
			return view, translateDatasetError(err)
		}
		o.emitForWorkspace(ctx, workspace.ID, "dataset.rows.written", dataset.ID, map[string]int{"inserted": written.Inserted, "updated": written.Updated})
		view.Summary = fmt.Sprintf("Upserted %d row(s) in “%s”.", written.Inserted+written.Updated, dataset.Name)
		view.Entity = &chatEntityRef{ID: dataset.ID, Type: "dataset", Title: dataset.Name, Status: "active", Summary: view.Summary}
	case steering.EffectUpdateDatasetRow:
		dataset, err := o.store.GetDatasetForWorkspace(ctx, workspace.ID, effect.DatasetRow.DatasetID, false)
		if err != nil {
			return view, translateDatasetError(err)
		}
		row, err := o.store.UpdateDatasetRowForWorkspace(ctx, workspace.ID, dataset.ID, effect.DatasetRow.RowID, effect.DatasetRow.Values)
		if err != nil {
			return view, translateDatasetError(err)
		}
		o.emitForWorkspace(ctx, workspace.ID, "dataset.row.updated", dataset.ID, map[string]any{"row_id": row.ID, "source": "chat"})
		view.Summary = fmt.Sprintf("Updated row %d in “%s”.", row.ID, dataset.Name)
		view.Entity = &chatEntityRef{ID: dataset.ID, Type: "dataset", Title: dataset.Name, Status: "active", Summary: view.Summary}
	case steering.EffectDeleteDatasetRow:
		dataset, err := o.store.GetDatasetForWorkspace(ctx, workspace.ID, effect.DatasetRow.DatasetID, false)
		if err != nil {
			return view, translateDatasetError(err)
		}
		approval, err := o.requestDatasetDeletionApproval(ctx, workspace.ID, "", "", []datasetDeletionTarget{{DatasetID: dataset.ID, RowID: effect.DatasetRow.RowID}})
		if err != nil {
			return view, err
		}
		view.Summary = fmt.Sprintf("Requested approval to delete row %d from “%s”.", effect.DatasetRow.RowID, dataset.Name)
		view.Entity = &chatEntityRef{ID: approval.ID, Type: "approval", Title: approval.ProposedAction, Status: string(approval.Status), Summary: approval.ProposedChange}
	default:
		return view, fmt.Errorf("%w: unsupported chat effect %q", api.ErrInvalid, effect.Type)
	}
	return view, nil
}

func (o *Operator) createManagedScriptFromChat(ctx context.Context, workspaceID string, change steering.ScriptChange) (domain.Script, error) {
	path := filepath.Join(o.paths.Scripts, change.Path)
	existingScripts, err := o.store.ListScripts(ctx, store.ScriptFilter{WorkspaceID: workspaceID})
	if err != nil {
		return domain.Script{}, err
	}
	for _, existing := range existingScripts {
		if existing.Path != change.Path {
			continue
		}
		existingContent, readErr := os.ReadFile(path)
		if readErr == nil && sameManagedScript(existing, existingContent, change) {
			return existing, nil
		}
		return domain.Script{}, fmt.Errorf("%w: a different managed script named %q already exists", api.ErrConflict, change.Path)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return domain.Script{}, fmt.Errorf("%w: a managed script named %q already exists", api.ErrConflict, change.Path)
		}
		return domain.Script{}, fmt.Errorf("create managed script: %w", err)
	}
	createdFile := true
	defer func() {
		if createdFile {
			_ = os.Remove(path)
		}
	}()
	content := []byte(change.Content)
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return domain.Script{}, fmt.Errorf("write managed script: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return domain.Script{}, fmt.Errorf("sync managed script: %w", err)
	}
	if err := file.Close(); err != nil {
		return domain.Script{}, fmt.Errorf("close managed script: %w", err)
	}
	bindings := make([]domain.ScriptCredentialBinding, 0, len(change.SecretBindings))
	for _, binding := range change.SecretBindings {
		bindings = append(bindings, domain.ScriptCredentialBinding{Env: binding.EnvVar, SecretRecordID: binding.SecretID})
	}
	script, err := o.store.CreateScript(ctx, domain.Script{
		WorkspaceID: workspaceID, Name: change.Name, Path: change.Path, Description: change.Description,
		Enabled: true, Access: domain.ScriptAccess(change.Access), TimeoutSeconds: change.TimeoutSeconds,
		CredentialBindings: bindings,
	})
	if err != nil {
		return domain.Script{}, err
	}
	createdFile = false
	o.emitForWorkspace(ctx, workspaceID, "script.created", script.ID, script)
	return script, nil
}

func sameManagedScript(existing domain.Script, content []byte, change steering.ScriptChange) bool {
	if existing.Name != change.Name || existing.Description != change.Description || existing.Access != domain.ScriptAccess(change.Access) ||
		existing.TimeoutSeconds != change.TimeoutSeconds || strings.TrimSpace(string(content)) != strings.TrimSpace(change.Content) ||
		len(existing.CredentialBindings) != len(change.SecretBindings) {
		return false
	}
	for index, binding := range existing.CredentialBindings {
		if binding.SecretRecordID != change.SecretBindings[index].SecretID || binding.Env != change.SecretBindings[index].EnvVar {
			return false
		}
	}
	return true
}

func taskUpdateFromSteering(change steering.TaskChange) api.TaskUpdate {
	update := api.TaskUpdate{}
	if change.Title != "" {
		update.Title = &change.Title
	}
	if change.Purpose != "" {
		update.Purpose = &change.Purpose
	}
	if change.Why != "" {
		update.Why = &change.Why
	}
	if change.Priority != "" {
		update.Priority = &change.Priority
	}
	if change.Status != "" {
		update.Status = &change.Status
	}
	if change.DefinitionOfDone != nil {
		update.DefinitionOfDone = &change.DefinitionOfDone
	}
	if change.DependsOnTaskIDs != nil {
		dependencies := append([]string(nil), change.DependsOnTaskIDs...)
		update.DependsOnTaskIDs = &dependencies
	}
	if change.PlannedAt != nil {
		update.PlannedAt = change.PlannedAt
	}
	return update
}

func scheduleInputFromChat(change steering.ScheduleChange, workspaceID string) (api.ScheduleInput, error) {
	name, kind := change.Name, change.Kind
	input := api.ScheduleInput{Name: &name, Kind: &kind}
	if change.Expression != "" {
		expression := change.Expression
		input.Expression = &expression
	} else {
		interval := change.IntervalSeconds
		input.IntervalSeconds = &interval
	}
	var payload any
	switch change.Kind {
	case domain.ScheduleTask:
		payload = map[string]any{
			"title": change.Task.Title, "purpose": change.Task.Purpose, "why": change.Task.Why,
			"priority": change.Task.Priority, "definition_of_done": change.Task.DefinitionOfDone,
			"workspace_id": workspaceID,
		}
	case domain.ScheduleOrient:
		payload = map[string]string{"reason": change.Reason}
	default:
		return api.ScheduleInput{}, fmt.Errorf("%w: unsupported chat schedule kind %q", api.ErrInvalid, change.Kind)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return api.ScheduleInput{}, fmt.Errorf("%w: encode schedule payload", api.ErrInvalid)
	}
	input.Payload = encoded
	return input, nil
}

func scheduleCadenceSummary(schedule domain.Schedule) string {
	if schedule.Expression != "" {
		return schedule.Expression
	}
	return fmt.Sprintf("every %d seconds", schedule.IntervalSeconds)
}

func setPolicyDecision(policy *domain.Policy, category steering.ActionCategory, decision steering.PolicyDecision) {
	value := string(decision)
	switch category {
	case steering.CategoryRead:
		policy.Read = value
	case steering.CategoryWork:
		policy.Work = value
	case steering.CategoryPublish:
		policy.Publish = value
	case steering.CategoryDangerous:
		policy.Dangerous = "ask"
	}
}

func (o *Operator) setPausedFromChat(ctx context.Context, paused bool) error {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	settings.Paused = paused
	if err := o.store.UpdateSettings(ctx, settings); err != nil {
		return err
	}
	o.emitForWorkspace(ctx, settings.ActiveWorkspaceID, "status.changed", "", map[string]bool{"paused": paused})
	if !paused {
		o.signal()
	}
	return nil
}

func (o *Operator) publishChatActivity(messageID, kind, label string) {
	o.publishTransient("chat.activity", messageID, map[string]any{
		"message_id": messageID,
		"activity":   map[string]any{"type": kind, "label": label, "status": "active", "created_at": time.Now().UTC()},
	})
}

func (o *Operator) finishChatFailure(ctx context.Context, workspace domain.Workspace, user domain.Message, pendingID string, record domain.Run, execution runner.ExecutionResult, cause error) {
	content := "I couldn't complete that steering request. " + userFacingRunError(execution) + "."
	if errors.Is(cause, context.Canceled) {
		content = "That steering request was cancelled before Nabu applied any changes."
	} else if execution.ExitCode != nil && *execution.ExitCode == 0 {
		detail := strings.TrimSpace(redactSecrets(cause.Error()))
		if len(detail) > 700 {
			detail = detail[:700] + "…"
		}
		content = "I understood the request, but the proposed changes still did not match Nabu's validated action contract after one automatic repair attempt."
		if detail != "" {
			content += "\n\n**What needs correction:** " + detail
		}
		content += "\n\nNo changes were made. You can restate the intended outcome, or open the linked run if you need the complete diagnostic."
	}
	o.logger.Error("chat steering failed", "run_id", record.ID, "workspace_id", workspace.ID, "error", cause)
	user.Status = domain.MessageFailed
	user.UpdatedAt = time.Now().UTC()
	_ = o.store.UpdateMessage(ctx, user)
	o.emitForWorkspace(ctx, workspace.ID, "chat.message", strconv.FormatInt(user.ID, 10), user)
	metadata, _ := json.Marshal(chatEffectMetadata{Effects: []chatEffectView{}, References: []chatEntityRef{}, Error: cause.Error()})
	assistant, appendErr := o.store.AppendMessage(ctx, domain.Message{
		WorkspaceID: workspace.ID, ParentMessageID: user.ThreadRootID,
		Role: domain.MessageAssistant, Content: content, Effect: domain.EffectConversationOnly, EffectMetadata: metadata,
	})
	if record.ID != "" {
		o.finishChatRun(ctx, &record, execution, content, cause, workspace.ID)
	}
	if appendErr == nil {
		o.emitForWorkspace(ctx, workspace.ID, "chat.message", strconv.FormatInt(assistant.ID, 10), assistant)
	}
	o.publishTransient("chat.completed", pendingID, map[string]any{"message_id": assistant.ID, "pending_message_id": pendingID, "error": true})
}

func (o *Operator) requeueChatMessage(workspace domain.Workspace, user domain.Message, pendingID string, record domain.Run, execution runner.ExecutionResult) {
	ctx := context.Background()
	if err := o.store.RequeueMessage(ctx, user.ID); err != nil {
		o.logger.Error("requeue interrupted chat", "message_id", user.ID, "error", err)
		return
	}
	user.Status = domain.MessageQueued
	o.emitForWorkspace(ctx, workspace.ID, "chat.message", strconv.FormatInt(user.ID, 10), user)
	if record.ID != "" {
		o.finishChatRun(ctx, &record, execution, "Chat message returned to the queue before completion.", context.Canceled, workspace.ID)
	}
	o.publishTransient("chat.completed", pendingID, map[string]any{
		"message_id": user.ID, "pending_message_id": pendingID, "queued": true,
	})
}

func (o *Operator) finishChatRun(ctx context.Context, record *domain.Run, execution runner.ExecutionResult, summary string, runErr error, workspaceID string) {
	ended := execution.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	record.PID, record.Command, record.Attempt = execution.PID, execution.Command, execution.Attempt
	record.EndedAt, record.ExitCode, record.Error = &ended, execution.ExitCode, ""
	record.Status = domain.RunCompleted
	if runErr != nil {
		record.Status = execution.Status
		if record.Status == "" || record.Status == domain.RunCompleted {
			record.Status = domain.RunFailed
		}
		record.Error = runErr.Error()
	}
	record.Result = &domain.RunResult{
		Status: string(record.Status), Summary: redactSecrets(summary), FilesChanged: []string{},
		Verification: []domain.Verification{}, Artifacts: []domain.Artifact{}, Uncertainties: []string{},
	}
	runDirectory := filepath.Join(o.paths.Runs, record.ID)
	if os.MkdirAll(runDirectory, 0o700) == nil {
		record.StdoutPath = filepath.Join(runDirectory, "stdout.log")
		record.StderrPath = filepath.Join(runDirectory, "stderr.log")
		if os.WriteFile(record.StdoutPath, []byte(redactSecrets(execution.Stdout)), 0o600) != nil {
			record.StdoutPath = ""
		}
		if os.WriteFile(record.StderrPath, []byte(redactSecrets(execution.Stderr)), 0o600) != nil {
			record.StderrPath = ""
		}
	}
	_ = o.store.UpdateRun(ctx, *record)
	o.emitForWorkspace(ctx, workspaceID, "run.completed", record.ID, map[string]any{"status": record.Status, "type": record.Type})
}
