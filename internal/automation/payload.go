package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

const (
	maximumPayloadBytes        = 64 * 1024
	maximumTitleBytes          = 160
	maximumPurposeBytes        = 4 * 1024
	maximumWhyBytes            = 2 * 1024
	maximumDefinitionItems     = 12
	maximumDefinitionItemBytes = 500
	maximumSummaryBytes        = 4 * 1024
	maximumResultDataBytes     = 256 * 1024
	maximumArtifacts           = 16
	maximumArtifactFieldBytes  = 4 * 1024
	maximumArtifactMetadata    = 64 * 1024
)

// InterestingAction controls the durable follow-up for a script result that
// explicitly marks itself interesting. No follow-up occurs for ordinary
// results, so routine monitoring does not consume AI calls.
type InterestingAction string

const (
	InterestingTask   InterestingAction = "task"
	InterestingOrient InterestingAction = "orient"
	InterestingNone   InterestingAction = "none"
)

// TaskPayload is the structured payload for a task schedule. The same shape
// may customize the task created by an interesting script result.
type TaskPayload struct {
	Title            string          `json:"title"`
	Purpose          string          `json:"purpose,omitempty"`
	Why              string          `json:"why,omitempty"`
	Priority         domain.Priority `json:"priority,omitempty"`
	DefinitionOfDone []string        `json:"definition_of_done,omitempty"`
	WorkspaceID      string          `json:"workspace_id,omitempty"`
}

// ScriptPayload identifies a registered script and its optional interesting
// result behavior. Interesting defaults to a bounded task when omitted.
type ScriptPayload struct {
	ScriptID        string            `json:"script_id"`
	OnInteresting   InterestingAction `json:"on_interesting,omitempty"`
	InterestingTask *TaskPayload      `json:"interesting_task,omitempty"`
}

// OrientPayload is intentionally small: an orientation schedule only queues
// a durable request. It never invokes an AI process directly.
type OrientPayload struct {
	Reason string `json:"reason,omitempty"`
}

func decodePayload(payload json.RawMessage, destination any, allowEmpty bool) error {
	if len(payload) == 0 || len(bytes.TrimSpace(payload)) == 0 {
		if allowEmpty {
			return nil
		}
		return errors.New("automation: schedule payload is required")
	}
	if len(payload) > maximumPayloadBytes {
		return fmt.Errorf("automation: schedule payload exceeds %d bytes", maximumPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("automation: invalid schedule payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("automation: schedule payload must contain one JSON object")
	}
	return nil
}

func validateTaskPayload(payload TaskPayload, requireTitle bool) error {
	payload.Title = strings.TrimSpace(payload.Title)
	if requireTitle && payload.Title == "" {
		return errors.New("automation: task title is required")
	}
	for name, value := range map[string]struct {
		value   string
		maximum int
	}{
		"task title":        {payload.Title, maximumTitleBytes},
		"task purpose":      {payload.Purpose, maximumPurposeBytes},
		"task why":          {payload.Why, maximumWhyBytes},
		"task workspace ID": {payload.WorkspaceID, maximumTitleBytes},
	} {
		if len(value.value) > value.maximum {
			return fmt.Errorf("automation: %s exceeds %d bytes", name, value.maximum)
		}
	}
	switch payload.Priority {
	case "", domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow:
	default:
		return fmt.Errorf("automation: unsupported task priority %q", payload.Priority)
	}
	if len(payload.DefinitionOfDone) > maximumDefinitionItems {
		return fmt.Errorf("automation: task definition has more than %d items", maximumDefinitionItems)
	}
	for index, item := range payload.DefinitionOfDone {
		if strings.TrimSpace(item) == "" {
			return fmt.Errorf("automation: task definition item %d is empty", index+1)
		}
		if len(item) > maximumDefinitionItemBytes {
			return fmt.Errorf("automation: task definition item %d exceeds %d bytes", index+1, maximumDefinitionItemBytes)
		}
	}
	return nil
}

func validateScriptResult(result *domain.ScriptResult) error {
	if result == nil {
		return errors.New("automation: completed script has no result")
	}
	if strings.TrimSpace(result.Summary) == "" {
		return errors.New("automation: script result summary is required")
	}
	if len(result.Summary) > maximumSummaryBytes {
		return fmt.Errorf("automation: script result summary exceeds %d bytes", maximumSummaryBytes)
	}
	if len(result.Data) > maximumResultDataBytes {
		return fmt.Errorf("automation: script result data exceeds %d bytes", maximumResultDataBytes)
	}
	if len(result.Data) > 0 && !json.Valid(result.Data) {
		return errors.New("automation: script result data is not valid JSON")
	}
	if len(result.Artifacts) > maximumArtifacts {
		return fmt.Errorf("automation: script result has more than %d artifacts", maximumArtifacts)
	}
	for index, artifact := range result.Artifacts {
		if strings.TrimSpace(artifact.Kind) == "" || strings.TrimSpace(artifact.Name) == "" {
			return fmt.Errorf("automation: artifact %d requires kind and name", index+1)
		}
		for name, value := range map[string]string{
			"kind": artifact.Kind,
			"name": artifact.Name,
			"path": artifact.Path,
			"URL":  artifact.URL,
		} {
			if strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("automation: artifact %d %s contains a null byte", index+1, name)
			}
			if len(value) > maximumArtifactFieldBytes {
				return fmt.Errorf("automation: artifact %d %s exceeds %d bytes", index+1, name, maximumArtifactFieldBytes)
			}
		}
		if len(artifact.Metadata) > maximumArtifactMetadata || (len(artifact.Metadata) > 0 && !json.Valid(artifact.Metadata)) {
			return fmt.Errorf("automation: artifact %d metadata must be valid JSON within %d bytes", index+1, maximumArtifactMetadata)
		}
	}
	return nil
}
