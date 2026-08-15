package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

const (
	maximumTaskScriptContext = 256 * 1024
	hostBrowserQATimeout     = 10 * time.Minute
)

type taskScriptExecutor interface {
	RunScriptForTask(context.Context, string, string, string) (domain.ScriptRun, error)
}

type taskScriptResult struct {
	ScriptID   string          `json:"script_id"`
	ScriptName string          `json:"script_name"`
	Status     string          `json:"status"`
	Summary    string          `json:"summary,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type taskScriptPlan struct {
	Data                string
	Inventory           []string
	BrowserQARequired   bool
	BrowserMCPName      string
	BrowserVerifier     *domain.Script
	WorkspaceID         string
	WorkspacePath       string
	BrowserVerifierName string
}

// taskScriptContext inventories enabled read-only host capabilities. Ordinary
// scripts explicitly named by the task run before Codex as bounded context.
// Browser verifiers are deferred until after Codex finishes changing files, so
// their evidence describes the resulting workspace rather than the old state.
func (o *Operator) taskScriptContext(ctx context.Context, task domain.Task, workspace domain.Workspace) taskScriptPlan {
	plan := taskScriptPlan{
		BrowserQARequired: browserQATask(task), WorkspaceID: workspace.ID, WorkspacePath: workspace.Path,
	}
	if plan.BrowserQARequired {
		plan.BrowserMCPName = o.readyBrowserMCPName(ctx, workspace.ID)
	}
	haystack := taskSearchText(task)
	enabled := true
	scripts, err := o.store.ListScripts(ctx, store.ScriptFilter{WorkspaceID: workspace.ID, Enabled: &enabled, Limit: 32})
	if err != nil {
		return plan
	}
	browserVerifiers := make([]domain.Script, 0, 4)
	for index := range scripts {
		script := scripts[index]
		if script.Access != domain.ScriptAccessRead {
			continue
		}
		kind := "read-only host script"
		if isBrowserVerifier(script) {
			kind = "read-only host browser verifier"
			browserVerifiers = append(browserVerifiers, script)
		}
		plan.Inventory = append(plan.Inventory, capabilityDescription(script, kind))
	}
	if plan.BrowserQARequired {
		for index := range browserVerifiers {
			if len(browserVerifiers) == 1 || scriptMentioned(haystack, browserVerifiers[index]) {
				selected := browserVerifiers[index]
				plan.BrowserVerifier = &selected
				plan.BrowserVerifierName = selected.Name
				break
			}
		}
	}

	o.mu.Lock()
	executor, ok := o.automation.(taskScriptExecutor)
	o.mu.Unlock()
	if !ok || executor == nil {
		return plan
	}
	results := make([]taskScriptResult, 0, 4)
	for _, script := range scripts {
		if script.Access != domain.ScriptAccessRead || isBrowserVerifier(script) || !scriptMentioned(haystack, script) || len(results) >= 4 {
			continue
		}
		run, runErr := executor.RunScriptForTask(ctx, script.ID, workspace.ID, workspace.Path)
		entry := taskScriptResult{ScriptID: script.ID, ScriptName: script.Name, Status: string(run.Status)}
		if runErr != nil {
			entry.Error = boundedIntegrationError(runErr)
		} else if run.Result != nil {
			entry.Summary = redactSecrets(run.Result.Summary)
			if json.Valid(run.Result.Data) {
				entry.Data = append(json.RawMessage(nil), run.Result.Data...)
			}
		}
		results = append(results, entry)
		o.emitForWorkspace(ctx, workspace.ID, "script.read_completed", script.ID, map[string]any{
			"task_id": task.ID, "script_run_id": run.ID, "status": run.Status, "error": entry.Error,
		})
	}
	plan.Data = encodeTaskScriptResults(results)
	return plan
}

func (o *Operator) applyTaskBrowserVerification(ctx context.Context, task domain.Task, execution runner.ExecutionResult, plan taskScriptPlan) runner.ExecutionResult {
	if !plan.BrowserQARequired || execution.Status != domain.RunCompleted {
		return execution
	}
	result, err := runner.ParseRunResult(execution.Stdout)
	if err != nil || result.Status != "completed" {
		return execution
	}
	if plan.BrowserMCPName != "" && hasPassedBrowserVerification(result.Verification) {
		return execution
	}
	result.Verification = removeDelegatedBrowserChecks(result.Verification)

	o.mu.Lock()
	executor, available := o.automation.(taskScriptExecutor)
	o.mu.Unlock()
	if plan.BrowserVerifier == nil || !available || executor == nil {
		detail := "No ready browser MCP or enabled read-only browser verifier is available for this workspace. Browser verification was deferred; implementation and non-browser evidence remain valid. A later browser check can use Settings > MCP connectors or an owner-managed read-only verifier in Settings > Scripts."
		if plan.BrowserMCPName != "" {
			detail = fmt.Sprintf("The browser MCP %q did not return observable browser evidence. Browser verification was deferred without discarding completed implementation or non-browser checks.", plan.BrowserMCPName)
		}
		if hasDurableNonBrowserOutcome(result) {
			return replaceTaskResult(execution, browserVerificationDeferred(result, detail))
		}
		return replaceTaskResult(execution, browserVerificationFailure(result, detail))
	}

	verifyContext, cancel := context.WithTimeout(ctx, hostBrowserQATimeout)
	defer cancel()
	run, runErr := executor.RunScriptForTask(verifyContext, plan.BrowserVerifier.ID, plan.WorkspaceID, plan.WorkspacePath)
	if runErr != nil || run.Status != domain.ScriptRunCompleted || run.Result == nil || run.Result.Status != "completed" || run.Result.Interesting {
		detail := fmt.Sprintf("Registered browser verifier %q failed", plan.BrowserVerifier.Name)
		if runErr != nil {
			detail += ": " + redactSecrets(boundedIntegrationError(runErr))
		} else if run.Error != "" {
			detail += ": " + redactSecrets(run.Error)
		}
		detail += ". Review its script run and fix the verifier or application before retrying. Nabu did not broaden the Codex sandbox."
		o.emitForWorkspace(ctx, plan.WorkspaceID, "script.browser_verification_failed", plan.BrowserVerifier.ID, map[string]any{
			"task_id": task.ID, "script_run_id": run.ID, "status": run.Status, "error": redactSecrets(boundedIntegrationError(runErr)),
		})
		return replaceTaskResult(execution, browserVerificationFailure(result, detail))
	}

	detail := strings.TrimSpace(redactSecrets(run.Result.Summary))
	if detail == "" {
		detail = "The registered host-side browser verifier completed successfully."
	}
	result.Verification = append(result.Verification, domain.Verification{
		Name: "Host browser QA: " + plan.BrowserVerifier.Name, Status: "passed", Details: detail,
	})
	for _, artifact := range run.Result.Artifacts {
		artifact.ID, artifact.TaskID, artifact.RunID = "", "", ""
		artifact.ScriptRunID = run.ID
		artifact.CreatedAt = time.Time{}
		result.Artifacts = append(result.Artifacts, artifact)
	}
	o.emitForWorkspace(ctx, plan.WorkspaceID, "script.browser_verification_completed", plan.BrowserVerifier.ID, map[string]any{
		"task_id": task.ID, "script_run_id": run.ID, "status": run.Status,
	})
	return replaceTaskResult(execution, result)
}

func hasDurableNonBrowserOutcome(result domain.RunResult) bool {
	return len(result.FilesChanged) > 0 || len(result.Artifacts) > 0 || len(result.DatasetWrites) > 0 || len(result.LocalApps) > 0
}

func browserVerificationDeferred(result domain.RunResult, detail string) domain.RunResult {
	detail = redactSecrets(detail)
	result.Status = "completed"
	result.Summary = strings.TrimSpace(result.Summary) + " Browser verification was deferred."
	result.Verification = append(result.Verification, domain.Verification{Name: "Browser QA", Status: "not_run", Details: detail})
	result.Uncertainties = append(result.Uncertainties, detail)
	return result
}

func browserVerificationFailure(result domain.RunResult, detail string) domain.RunResult {
	detail = redactSecrets(detail)
	result.Status = "failed"
	result.Summary = strings.TrimSpace(result.Summary) + " Browser QA needs attention."
	result.Verification = append(result.Verification, domain.Verification{Name: "Host browser QA", Status: "failed", Details: detail})
	result.Uncertainties = append(result.Uncertainties, detail)
	return result
}

func replaceTaskResult(execution runner.ExecutionResult, result domain.RunResult) runner.ExecutionResult {
	encoded, err := json.Marshal(result)
	if err != nil {
		return execution
	}
	execution.Stdout = strings.TrimSpace(execution.Stdout) + "\n" + string(encoded)
	return execution
}

func removeDelegatedBrowserChecks(values []domain.Verification) []domain.Verification {
	result := make([]domain.Verification, 0, len(values))
	for _, value := range values {
		text := strings.ToLower(value.Name + " " + value.Details)
		if strings.Contains(text, "browser") || browserVerificationText(text) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func hasPassedBrowserVerification(values []domain.Verification) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value.Status), "passed") && browserVerificationText(value.Name+" "+value.Details) {
			return true
		}
	}
	return false
}

func (o *Operator) readyBrowserMCPName(ctx context.Context, workspaceID string) string {
	enabled := true
	servers, err := o.store.ListMCPServers(ctx, store.MCPServerFilter{WorkspaceID: workspaceID, Enabled: &enabled, Limit: 32})
	if err != nil {
		return ""
	}
	for index := range servers {
		if !isBrowserMCP(servers[index]) {
			continue
		}
		o.hydrateMCPReadiness(ctx, &servers[index])
		if servers[index].Ready {
			return strings.TrimSpace(servers[index].Name)
		}
	}
	if discoverBuiltInBrowserRuntime().Available {
		return builtInBrowserMCPName
	}
	return ""
}

func isBrowserMCP(server domain.MCPServer) bool {
	values := []string{server.Name, server.Description, server.Command, server.URL}
	values = append(values, server.Args...)
	values = append(values, server.EnabledTools...)
	text := strings.ToLower(strings.Join(values, " "))
	return containsAny(text, "browser", "chrome", "chromium", "playwright", "puppeteer", "devtools", "selenium", "browserbase")
}

func encodeTaskScriptResults(results []taskScriptResult) string {
	for len(results) > 0 {
		encoded, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return ""
		}
		if len(encoded) <= maximumTaskScriptContext {
			return string(encoded)
		}
		trimmed := false
		for index := len(results) - 1; index >= 0; index-- {
			if len(results[index].Data) == 0 {
				continue
			}
			results[index].Data = nil
			results[index].Error = "Script data omitted because it exceeded Nabu's task context limit."
			trimmed = true
			break
		}
		if !trimmed {
			return ""
		}
	}
	return ""
}

func taskSearchText(task domain.Task) string {
	return strings.ToLower(strings.Join([]string{task.Title, task.Purpose, task.Why, definitionText(task.DefinitionOfDone)}, "\n"))
}

func browserQATask(task domain.Task) bool {
	return browserVerificationText(taskSearchText(task))
}

// browserVerificationText recognizes both implementation-specific language
// (Playwright/Chromium) and product-level acceptance criteria. Task authors
// should not have to know which browser runner Nabu uses for accessibility,
// responsive behavior, or performance verification to reach the host-side
// browser QA lane.
func browserVerificationText(text string) bool {
	text = strings.ToLower(text)
	if containsAny(text,
		"playwright", "chromium", "browser qa", "browser test", "browser verification",
		"visual regression", "end-to-end", "e2e", "page screenshot", "viewport",
		"lighthouse", "core web vitals", "axe accessibility", "axe-core",
	) {
		return true
	}
	productCriterion := containsAny(text,
		"desktop and mobile", "mobile and desktop", "responsive behavior",
		"responsive rendering", "responsive overflow", "responsive layout",
		"accessibility", "performance budget", "performance budgets",
	)
	verificationIntent := containsAny(text,
		"test", "verify", "verification", "validate", "validation", "audit",
		"check", "behavior", "rendering", "overflow", "budget",
	)
	return productCriterion && verificationIntent
}

func isBrowserVerifier(script domain.Script) bool {
	text := strings.ToLower(strings.Join([]string{script.Name, script.Description, script.Path}, " "))
	return containsAny(text, "playwright", "chromium", "browser qa", "browser-qa", "browser_verify", "browser-verify", "visual regression", "e2e")
}

func capabilityDescription(script domain.Script, kind string) string {
	description := strings.Join(strings.Fields(strings.TrimSpace(script.Description)), " ")
	value := fmt.Sprintf("%s (%s, id %s)", strings.TrimSpace(script.Name), kind, script.ID)
	if description != "" {
		value += ": " + description
	}
	if len(value) > 768 {
		value = value[:768] + "…"
	}
	return value
}

func scriptMentioned(haystack string, script domain.Script) bool {
	for _, candidate := range []string{script.Name, script.Path} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		candidate = strings.TrimSuffix(candidate, ".sh")
		if len(candidate) >= 3 && strings.Contains(haystack, candidate) {
			return true
		}
		for _, token := range strings.FieldsFunc(candidate, func(value rune) bool {
			return value == '-' || value == '_' || value == '.' || value == ' '
		}) {
			switch token {
			case "read", "fetch", "check", "report", "summary", "sync", "script", "verify", "verifier", "qa":
				continue
			}
			if len(token) >= 4 && strings.Contains(haystack, token) {
				return true
			}
		}
	}
	return false
}
