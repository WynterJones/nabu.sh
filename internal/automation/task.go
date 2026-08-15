package automation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

func (e *Engine) createTask(ctx context.Context, payload TaskPayload, createdBy, id string) (domain.Task, error) {
	priority := payload.Priority
	if priority == "" {
		priority = domain.PriorityNormal
	}
	definition := make([]domain.DefinitionItem, 0, len(payload.DefinitionOfDone))
	for _, item := range payload.DefinitionOfDone {
		definition = append(definition, domain.DefinitionItem{Text: strings.TrimSpace(item)})
	}
	task := domain.Task{
		ID:               id,
		Title:            strings.TrimSpace(payload.Title),
		Purpose:          strings.TrimSpace(payload.Purpose),
		Why:              strings.TrimSpace(payload.Why),
		Status:           domain.TaskReady,
		Priority:         priority,
		DefinitionOfDone: definition,
		WorkspaceID:      strings.TrimSpace(payload.WorkspaceID),
		CreatedBy:        createdBy,
	}
	created, err := e.store.CreateTask(ctx, task)
	if err == nil || id == "" {
		return created, err
	}
	// A stable schedule-occurrence ID turns recovery after an ambiguous commit
	// into a read, not a duplicate side effect. Only the same owned effect is
	// accepted; an unrelated collision still fails loudly.
	existing, getErr := e.store.GetTask(ctx, id)
	if getErr == nil && existing.CreatedBy == createdBy {
		return existing, nil
	}
	return domain.Task{}, fmt.Errorf("automation: create task: %w", err)
}

func stableID(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s-%x", prefix, digest[:16])
}

func truncateBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := 0
	for index := range value {
		if index > maximum {
			break
		}
		end = index
	}
	if end == 0 {
		return ""
	}
	return value[:end]
}
