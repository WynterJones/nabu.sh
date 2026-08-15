package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/nabu-sh/nabu/internal/domain"
)

// MaxOrientationTasks is the hard safety limit for new Ready tasks produced by
// a single orientation.
const MaxOrientationTasks = 3

const (
	MaxRunDatasetWrites        = 4
	MaxRunDatasetColumns       = 32
	MaxRunDatasetRowsPerWrite  = 100
	MaxRunDatasetWriteBytes    = 256 * 1024
	MaxRunDatasetRowsFileRows  = 1_000
	MaxRunDatasetRowsFileBytes = 16 * 1024 * 1024
	MaxRunLocalApps            = 4
)

// ParseRunResult extracts and normalizes the final result from plain JSON,
// Markdown code fences, surrounding prose, or Codex JSONL event output.
func ParseRunResult(raw string) (domain.RunResult, error) {
	if strings.TrimSpace(raw) == "" {
		return domain.RunResult{}, errors.New("runner: empty run result")
	}
	candidates := extractJSONObjects(raw)
	var lastValidationError error
	for index := len(candidates) - 1; index >= 0; index-- {
		var result domain.RunResult
		if err := json.Unmarshal(candidates[index], &result); err != nil {
			continue
		}
		normalized, err := normalizeRunResult(result)
		if err == nil {
			return normalized, nil
		}
		// Codex JSONL contains many event envelopes with their own status
		// fields. Those are not run results and must not hide a specific
		// validation error from the final structured result nested inside an
		// agent_message event.
		if looksLikeRunResult(candidates[index]) {
			lastValidationError = err
		}
	}
	if lastValidationError != nil {
		return domain.RunResult{}, lastValidationError
	}
	return domain.RunResult{}, errors.New("runner: no structured run result found")
}

func normalizeRunResult(result domain.RunResult) (domain.RunResult, error) {
	status := normalizeToken(result.Status)
	switch status {
	case "success", "succeeded", "done":
		status = "completed"
	case "error":
		status = "failed"
	case "approval_required", "waiting_for_approval":
		status = "needs_approval"
	}
	switch status {
	case "completed", "failed", "needs_approval", "cancelled":
		result.Status = status
	default:
		return domain.RunResult{}, fmt.Errorf("runner: invalid run result status %q", result.Status)
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" {
		return domain.RunResult{}, errors.New("runner: run result summary is required")
	}

	result.FilesChanged = uniqueTrimmed(result.FilesChanged)
	result.Uncertainties = uniqueTrimmed(result.Uncertainties)
	if result.FilesChanged == nil {
		result.FilesChanged = []string{}
	}
	if result.Verification == nil {
		result.Verification = []domain.Verification{}
	}
	if len(result.DefinitionDone) > 32 {
		return domain.RunResult{}, errors.New("runner: definition_of_done exceeds maximum of 32 items")
	}
	for index := range result.DefinitionDone {
		outcome := &result.DefinitionDone[index]
		outcome.Text = strings.TrimSpace(outcome.Text)
		outcome.Status = normalizeToken(outcome.Status)
		outcome.Details = strings.TrimSpace(outcome.Details)
		if outcome.Text == "" {
			return domain.RunResult{}, fmt.Errorf("runner: definition_of_done item %d requires text", index)
		}
		switch outcome.Status {
		case "passed", "failed", "not_run":
		default:
			return domain.RunResult{}, fmt.Errorf("runner: definition_of_done item %d has invalid status %q", index, outcome.Status)
		}
	}
	if result.DefinitionDone == nil {
		result.DefinitionDone = []domain.DefinitionOutcome{}
	}
	if result.Artifacts == nil {
		result.Artifacts = []domain.Artifact{}
	}
	if result.Uncertainties == nil {
		result.Uncertainties = []string{}
	}
	for index := range result.Verification {
		result.Verification[index].Name = strings.TrimSpace(result.Verification[index].Name)
		result.Verification[index].Status = normalizeToken(result.Verification[index].Status)
		result.Verification[index].Details = strings.TrimSpace(result.Verification[index].Details)
	}
	for index := range result.Artifacts {
		result.Artifacts[index].Kind = normalizeToken(result.Artifacts[index].Kind)
		result.Artifacts[index].Name = strings.TrimSpace(result.Artifacts[index].Name)
		result.Artifacts[index].Path = strings.TrimSpace(result.Artifacts[index].Path)
		result.Artifacts[index].URL = strings.TrimSpace(result.Artifacts[index].URL)
	}
	if result.ApprovalNeeded != nil {
		approval := strings.TrimSpace(*result.ApprovalNeeded)
		if approval == "" {
			result.ApprovalNeeded = nil
		} else {
			result.ApprovalNeeded = &approval
		}
	}
	if len(result.DatasetWrites) > MaxRunDatasetWrites {
		return domain.RunResult{}, fmt.Errorf("runner: dataset writes exceed maximum of %d", MaxRunDatasetWrites)
	}
	for index := range result.DatasetWrites {
		write, err := normalizeDatasetWrite(result.DatasetWrites[index])
		if err != nil {
			return domain.RunResult{}, fmt.Errorf("runner: dataset write %d: %w", index, err)
		}
		result.DatasetWrites[index] = write
	}
	if result.DatasetWrites == nil {
		result.DatasetWrites = []domain.DatasetWrite{}
	}
	if len(result.LocalApps) > MaxRunLocalApps {
		return domain.RunResult{}, fmt.Errorf("runner: local app registrations exceed maximum of %d", MaxRunLocalApps)
	}
	for index := range result.LocalApps {
		registration, err := normalizeLocalAppRegistration(result.LocalApps[index])
		if err != nil {
			return domain.RunResult{}, fmt.Errorf("runner: local app registration %d: %w", index, err)
		}
		result.LocalApps[index] = registration
	}
	if result.LocalApps == nil {
		result.LocalApps = []domain.LocalAppRegistration{}
	}
	return result, nil
}

func normalizeLocalAppRegistration(app domain.LocalAppRegistration) (domain.LocalAppRegistration, error) {
	app.ID = ""
	app.Applied = false
	app.Name = strings.TrimSpace(app.Name)
	app.Description = strings.TrimSpace(app.Description)
	app.Directory = filepath.ToSlash(filepath.Clean(strings.TrimSpace(app.Directory)))
	app.HealthPath = strings.TrimSpace(app.HealthPath)
	if app.HealthPath == "" {
		app.HealthPath = "/"
	}
	if app.Name == "" || len(app.Name) > 160 || len(app.Description) > 4*1024 {
		return domain.LocalAppRegistration{}, errors.New("name or description is invalid or oversized")
	}
	if app.Directory == "." || app.Directory == ".." || filepath.IsAbs(app.Directory) || strings.HasPrefix(app.Directory, "../") || len(app.Directory) > 1024 || !strings.HasPrefix(app.Directory, "repos/") || app.Directory == "repos/" {
		return domain.LocalAppRegistration{}, errors.New("directory must be repos/<app-folder>; workspace-root applications are not allowed")
	}
	if len(app.Command) == 0 || len(app.Command) > 32 {
		return domain.LocalAppRegistration{}, errors.New("command requires 1-32 argv entries")
	}
	for index := range app.Command {
		if strings.ContainsRune(app.Command[index], '\x00') || len(app.Command[index]) > 4096 {
			return domain.LocalAppRegistration{}, errors.New("command contains an invalid argument")
		}
		app.Command[index] = strings.TrimSpace(app.Command[index])
	}
	if app.Command[0] == "" || localAppShellExecutable(app.Command[0]) || app.Port < 1024 || app.Port > 65535 {
		return domain.LocalAppRegistration{}, errors.New("executable or localhost port is invalid")
	}
	if !strings.HasPrefix(app.HealthPath, "/") || strings.ContainsAny(app.HealthPath, "?#\x00") || len(app.HealthPath) > 1024 {
		return domain.LocalAppRegistration{}, errors.New("health_path must be a local path such as / or /health")
	}
	return app, nil
}

func looksLikeRunResult(candidate json.RawMessage) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(candidate, &value) != nil {
		return false
	}
	for _, key := range []string{"summary", "definition_of_done", "files_changed", "verification", "artifacts", "approval_needed", "dataset_writes", "local_apps"} {
		if _, exists := value[key]; exists {
			return true
		}
	}
	return false
}

func localAppShellExecutable(value string) bool {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(value))) {
	case "sh", "bash", "zsh", "fish", "dash", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	default:
		return false
	}
}

func normalizeDatasetWrite(write domain.DatasetWrite) (domain.DatasetWrite, error) {
	write.DatasetID = strings.TrimSpace(write.DatasetID)
	write.RowsFile = strings.TrimSpace(write.RowsFile)
	write.Applied, write.Inserted, write.Updated = false, 0, 0
	encoded, err := json.Marshal(write)
	if err != nil || len(encoded) > MaxRunDatasetWriteBytes {
		return domain.DatasetWrite{}, fmt.Errorf("payload exceeds %d bytes", MaxRunDatasetWriteBytes)
	}
	switch write.Operation {
	case domain.DatasetWriteCreate:
		if write.Dataset == nil || write.DatasetID != "" || len(write.Rows) > MaxRunDatasetRowsPerWrite || (len(write.Rows) > 0 && write.RowsFile != "") {
			return domain.DatasetWrite{}, fmt.Errorf("create_dataset requires dataset metadata and at most one of 1-%d rows or rows_file", MaxRunDatasetRowsPerWrite)
		}
		dataset := *write.Dataset
		dataset.ID, dataset.WorkspaceID = "", ""
		dataset.Name, dataset.Slug, dataset.Description = strings.TrimSpace(dataset.Name), strings.TrimSpace(dataset.Slug), strings.TrimSpace(dataset.Description)
		dataset.RowCount, dataset.DeletedAt = 0, nil
		dataset.CreatedAt, dataset.UpdatedAt = time.Time{}, time.Time{}
		if dataset.Name == "" || len(dataset.Name) > 160 || !datasetSlug.MatchString(dataset.Slug) || len(dataset.Description) > 8*1024 {
			return domain.DatasetWrite{}, errors.New("dataset name or description is invalid or oversized")
		}
		if len(dataset.Schema) == 0 || len(dataset.Schema) > MaxRunDatasetColumns {
			return domain.DatasetWrite{}, fmt.Errorf("dataset schema requires 1-%d columns", MaxRunDatasetColumns)
		}
		seen := make(map[string]struct{}, len(dataset.Schema))
		for columnIndex := range dataset.Schema {
			column := &dataset.Schema[columnIndex]
			column.Name, column.Description = strings.TrimSpace(column.Name), strings.TrimSpace(column.Description)
			if !datasetIdentifier.MatchString(column.Name) || len(column.Description) > 2*1024 || !validDatasetColumnType(column.Type) {
				return domain.DatasetWrite{}, fmt.Errorf("invalid dataset column %q", column.Name)
			}
			if _, duplicate := seen[column.Name]; duplicate {
				return domain.DatasetWrite{}, fmt.Errorf("duplicate dataset column %q", column.Name)
			}
			seen[column.Name] = struct{}{}
		}
		uniqueKeys := make(map[string]struct{}, len(dataset.UniqueKey))
		for _, key := range dataset.UniqueKey {
			if _, exists := seen[key]; !exists {
				return domain.DatasetWrite{}, fmt.Errorf("unique key column %q is not in schema", key)
			}
			if _, duplicate := uniqueKeys[key]; duplicate {
				return domain.DatasetWrite{}, fmt.Errorf("duplicate unique key column %q", key)
			}
			uniqueKeys[key] = struct{}{}
		}
		write.Dataset = &dataset
		if write.RowsFile != "" {
			clean := filepath.Clean(write.RowsFile)
			if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || len(clean) > 1_024 {
				return domain.DatasetWrite{}, errors.New("create_dataset rows_file must be a bounded workspace-relative path")
			}
			write.RowsFile = filepath.ToSlash(clean)
		}
		for rowIndex, row := range write.Rows {
			if row == nil || len(row) == 0 || len(row) > MaxRunDatasetColumns {
				return domain.DatasetWrite{}, fmt.Errorf("row %d is empty or exceeds %d columns", rowIndex, MaxRunDatasetColumns)
			}
		}
	case domain.DatasetWriteUpsert:
		inline := len(write.Rows) > 0
		fromFile := write.RowsFile != ""
		if write.DatasetID == "" || write.Dataset != nil || inline == fromFile || len(write.Rows) > MaxRunDatasetRowsPerWrite {
			return domain.DatasetWrite{}, fmt.Errorf("upsert_rows requires dataset_id and exactly one of 1-%d rows or rows_file", MaxRunDatasetRowsPerWrite)
		}
		if fromFile {
			clean := filepath.Clean(write.RowsFile)
			if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || len(clean) > 1_024 {
				return domain.DatasetWrite{}, errors.New("upsert_rows rows_file must be a bounded workspace-relative path")
			}
			write.RowsFile = filepath.ToSlash(clean)
		}
		for rowIndex, row := range write.Rows {
			if row == nil || len(row) == 0 || len(row) > MaxRunDatasetColumns {
				return domain.DatasetWrite{}, fmt.Errorf("row %d is empty or exceeds %d columns", rowIndex, MaxRunDatasetColumns)
			}
		}
	case domain.DatasetWriteUpdate:
		if write.DatasetID == "" || write.RowID <= 0 || len(write.Values) == 0 || len(write.Values) > MaxRunDatasetColumns || write.Dataset != nil || len(write.Rows) != 0 || write.RowsFile != "" {
			return domain.DatasetWrite{}, fmt.Errorf("update_row requires dataset_id, exact row_id, and 1-%d values", MaxRunDatasetColumns)
		}
	case domain.DatasetWriteDelete:
		if write.DatasetID == "" || write.RowID <= 0 || write.Dataset != nil || len(write.Rows) != 0 || write.RowsFile != "" || len(write.Values) != 0 {
			return domain.DatasetWrite{}, errors.New("delete_row requires only dataset_id and exact row_id")
		}
	default:
		return domain.DatasetWrite{}, fmt.Errorf("invalid operation %q", write.Operation)
	}
	return write, nil
}

var datasetIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var datasetSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$|^[a-z0-9]$`)

func validDatasetColumnType(value domain.DatasetColumnType) bool {
	switch value {
	case domain.DatasetString, domain.DatasetInteger, domain.DatasetNumber, domain.DatasetBoolean, domain.DatasetDatetime, domain.DatasetJSON:
		return true
	default:
		return false
	}
}

// ParseOrientationResult extracts structured orientation output, then applies
// queue-aware safety limits and validation.
func ParseOrientationResult(raw string, currentQueue []domain.Task) (domain.OrientationResult, error) {
	if strings.TrimSpace(raw) == "" {
		return domain.OrientationResult{}, errors.New("runner: empty orientation result")
	}
	candidates := extractJSONObjects(raw)
	var lastValidationError error
	for index := len(candidates) - 1; index >= 0; index-- {
		var result domain.OrientationResult
		if err := json.Unmarshal(candidates[index], &result); err != nil {
			continue
		}
		result.Summary = strings.TrimSpace(result.Summary)
		if result.Summary == "" {
			continue
		}
		sanitized, err := SanitizeOrientationResult(result, currentQueue)
		if err == nil {
			return sanitized, nil
		}
		lastValidationError = err
	}
	if lastValidationError != nil {
		return domain.OrientationResult{}, lastValidationError
	}
	return domain.OrientationResult{}, errors.New("runner: no structured orientation result found")
}

// SanitizeOrientationResult enforces priorities, removes duplicate proposals,
// caps new tasks at three, and restricts reprioritization to the current queue.
func SanitizeOrientationResult(result domain.OrientationResult, currentQueue []domain.Task) (domain.OrientationResult, error) {
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" {
		return domain.OrientationResult{}, errors.New("runner: orientation summary is required")
	}

	queueTitles := make(map[string]struct{}, len(currentQueue))
	queueByID := make(map[string]domain.Task, len(currentQueue))
	for _, task := range currentQueue {
		if key := normalizedTitle(task.Title); key != "" {
			queueTitles[key] = struct{}{}
		}
		if task.ID != "" {
			queueByID[task.ID] = task
		}
	}

	proposedTitles := make(map[string]struct{}, len(result.Tasks))
	tasks := make([]domain.OrientationTask, 0, min(len(result.Tasks), MaxOrientationTasks))
	if !result.NoWorkNeeded {
		for _, task := range result.Tasks {
			if len(tasks) == MaxOrientationTasks {
				break
			}
			priority, err := normalizedPriority(task.Priority)
			if err != nil {
				return domain.OrientationResult{}, fmt.Errorf("runner: orientation task %q: %w", task.Title, err)
			}
			task.Title = strings.TrimSpace(task.Title)
			task.Purpose = strings.TrimSpace(task.Purpose)
			task.Why = strings.TrimSpace(task.Why)
			task.WorkspaceID = strings.TrimSpace(task.WorkspaceID)
			if task.Title == "" || task.Purpose == "" {
				continue
			}
			key := normalizedTitle(task.Title)
			if key == "" {
				continue
			}
			if _, exists := queueTitles[key]; exists {
				continue
			}
			if _, exists := proposedTitles[key]; exists {
				continue
			}
			proposedTitles[key] = struct{}{}
			task.Priority = priority
			task.DefinitionOfDone = sanitizeDefinition(task.DefinitionOfDone)
			tasks = append(tasks, task)
		}
	}
	result.Tasks = tasks
	if result.Tasks == nil {
		result.Tasks = []domain.OrientationTask{}
	}

	updates := make([]domain.PriorityUpdate, 0, len(result.PriorityUpdates))
	updatedIDs := make(map[string]struct{}, len(result.PriorityUpdates))
	for _, update := range result.PriorityUpdates {
		update.TaskID = strings.TrimSpace(update.TaskID)
		current, exists := queueByID[update.TaskID]
		if !exists || update.TaskID == "" {
			continue
		}
		if _, duplicate := updatedIDs[update.TaskID]; duplicate {
			continue
		}
		priority, err := normalizedPriority(update.Priority)
		if err != nil {
			return domain.OrientationResult{}, fmt.Errorf("runner: priority update for task %q: %w", update.TaskID, err)
		}
		if current.Priority == priority {
			continue
		}
		updatedIDs[update.TaskID] = struct{}{}
		update.Priority = priority
		updates = append(updates, update)
	}
	result.PriorityUpdates = updates
	if result.PriorityUpdates == nil {
		result.PriorityUpdates = []domain.PriorityUpdate{}
	}
	return result, nil
}

func normalizedPriority(priority domain.Priority) (domain.Priority, error) {
	normalized := domain.Priority(normalizeToken(string(priority)))
	if !validPriority(normalized) {
		return "", fmt.Errorf("invalid priority %q", priority)
	}
	return normalized, nil
}

func validPriority(priority domain.Priority) bool {
	switch priority {
	case domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow:
		return true
	default:
		return false
	}
}

func sanitizeDefinition(items []domain.DefinitionItem) []domain.DefinitionItem {
	clean := make([]domain.DefinitionItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item.Text = strings.TrimSpace(item.Text)
		key := normalizedTitle(item.Text)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		// A proposal cannot arrive already completed.
		item.Completed = false
		clean = append(clean, item)
	}
	if clean == nil {
		return []domain.DefinitionItem{}
	}
	return clean
}

func normalizedTitle(title string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(title)) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			normalized.WriteRune(character)
		} else {
			normalized.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(normalized.String()), " ")
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func uniqueTrimmed(values []string) []string {
	clean := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		clean = append(clean, value)
	}
	if clean == nil {
		return []string{}
	}
	return clean
}

// extractJSONObjects understands plain JSON, JSONL envelopes, JSON embedded in
// string fields, Markdown fences, and balanced objects surrounded by prose.
func extractJSONObjects(raw string) []json.RawMessage {
	var candidates []json.RawMessage
	seen := make(map[string]struct{})
	add := func(encoded []byte) {
		encoded = bytes.TrimSpace(encoded)
		if len(encoded) == 0 || !json.Valid(encoded) || encoded[0] != '{' {
			return
		}
		key := string(encoded)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, append(json.RawMessage(nil), encoded...))
	}

	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if encoded, err := json.Marshal(typed); err == nil {
				add(encoded)
			}
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(typed[key])
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case string:
			for _, object := range balancedJSONObjects(typed) {
				add(object)
				var nested any
				if json.Unmarshal(object, &nested) == nil {
					visit(nested)
				}
			}
		}
	}

	// Decode individual JSONL records, including Codex event envelopes.
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(line), &value) == nil {
			visit(value)
		}
	}
	// Finally extract fenced or prose-surrounded objects from the full output.
	for _, object := range balancedJSONObjects(raw) {
		add(object)
		var value any
		if json.Unmarshal(object, &value) == nil {
			visit(value)
		}
	}
	return candidates
}

func balancedJSONObjects(text string) [][]byte {
	data := []byte(text)
	objects := make([][]byte, 0)
	start := -1
	depth := 0
	inString := false
	escaped := false
	for index, character := range data {
		if start < 0 {
			if character == '{' {
				start = index
				depth = 1
				inString = false
				escaped = false
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				objects = append(objects, append([]byte(nil), data[start:index+1]...))
				start = -1
			}
		}
	}
	return objects
}
