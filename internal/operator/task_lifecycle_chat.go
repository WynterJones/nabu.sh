package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

type lifecycleMessageMetadata struct {
	AutomatedLifecycle bool             `json:"automated_lifecycle"`
	EventType          string           `json:"event_type"`
	TaskID             string           `json:"task_id"`
	RunID              string           `json:"run_id"`
	Effects            []chatEffectView `json:"effects"`
	References         []chatEntityRef  `json:"references,omitempty"`
}

func (o *Operator) appendTaskLifecycleMessage(ctx context.Context, task domain.Task, runID, eventType string, result *domain.RunResult, detail string) {
	if task.WorkspaceID == "" || task.ID == "" || runID == "" || eventType == "" {
		return
	}
	if o.lifecycleMessageExists(ctx, task.WorkspaceID, runID, eventType) {
		return
	}
	metadata := lifecycleMessageMetadata{AutomatedLifecycle: true, EventType: eventType, TaskID: task.ID, RunID: runID,
		Effects: []chatEffectView{}, References: []chatEntityRef{{Type: "task", ID: task.ID, Title: task.Title, Status: string(task.Status)}, {Type: "run", ID: runID, Title: "Task run"}},
	}
	content := lifecycleMessageContent(task, eventType, result, detail)
	if result != nil {
		for _, artifact := range result.Artifacts {
			if artifact.ID != "" {
				metadata.References = append(metadata.References, chatEntityRef{Type: "artifact", ID: artifact.ID, Title: artifact.Name})
			}
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	message, err := o.store.AppendMessage(context.WithoutCancel(ctx), domain.Message{
		WorkspaceID: task.WorkspaceID, Role: domain.MessageAssistant, Content: redactSecrets(content),
		Status: domain.MessageComplete, Effect: domain.EffectConversationOnly, EffectMetadata: encoded,
	})
	if err != nil {
		o.logger.Warn("append task lifecycle Chat message", "task_id", task.ID, "run_id", runID, "event_type", eventType, "error", err)
		return
	}
	o.emitForWorkspace(context.WithoutCancel(ctx), task.WorkspaceID, "chat.message", strconv.FormatInt(message.ID, 10), message)
}

func (o *Operator) lifecycleMessageExists(ctx context.Context, workspaceID, runID, eventType string) bool {
	messages, err := o.store.ListMessages(context.WithoutCancel(ctx), store.MessageFilter{
		WorkspaceID: workspaceID, Role: domain.MessageAssistant, TopLevelOnly: true, Limit: 500,
	})
	if err != nil {
		return false
	}
	for _, message := range messages {
		var metadata lifecycleMessageMetadata
		if json.Unmarshal(message.EffectMetadata, &metadata) == nil && metadata.AutomatedLifecycle && metadata.RunID == runID && metadata.EventType == eventType {
			return true
		}
	}
	return false
}

func automatedLifecycleMessage(message domain.Message) bool {
	var metadata lifecycleMessageMetadata
	return json.Unmarshal(message.EffectMetadata, &metadata) == nil && metadata.AutomatedLifecycle
}

func lifecycleMessageContent(task domain.Task, eventType string, result *domain.RunResult, detail string) string {
	title := strings.TrimSpace(task.Title)
	summary := strings.TrimSpace(detail)
	if result != nil && strings.TrimSpace(result.Summary) != "" {
		summary = strings.TrimSpace(result.Summary)
	}
	switch eventType {
	case "task.completed":
		content := fmt.Sprintf("Completed **%s**. %s", title, summary)
		if result != nil && len(result.Artifacts) > 0 {
			content += fmt.Sprintf(" I attached %d verified artifact", len(result.Artifacts))
			if len(result.Artifacts) != 1 {
				content += "s"
			}
			content += "."
		}
		return content
	case "task.needs_approval":
		return fmt.Sprintf("**%s** needs your approval before it can continue. %s Open the task’s approval card to review the exact action.", title, summary)
	default:
		return fmt.Sprintf("**%s** needs attention. %s Open the task run for the evidence, fix the stated blocker, then retry or continue the task.", title, summary)
	}
}

func actionableTaskFailure(detail string, execution runner.ExecutionResult) string {
	value := strings.TrimSpace(redactSecrets(detail))
	if value == "" {
		value = userFacingRunError(execution)
	}
	if len(value) > 1_500 {
		value = value[:1_500] + "…"
	}
	return value
}
