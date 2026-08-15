package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/steering"
	"github.com/nabu-sh/nabu/internal/store"
)

func TestSendChatCreatesTaskAndDurableAssistantMessage(t *testing.T) {
	result := `{
  "assistant_response":"I created a focused task for that outcome.",
  "effects":[{"type":"create_task","task":{"title":"Audit conversion path","purpose":"Find the highest-impact conversion leak","why":"Supports the active growth mission","priority":"high","definition_of_done":["The largest verified leak is documented"]}}]
}`
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: result})
	operator.Start(context.Background())

	message, err := operator.SendChat(context.Background(), api.ChatSend{Content: "Find our largest conversion problem"})
	if err != nil {
		t.Fatal(err)
	}
	if message.Role != domain.MessageUser || message.WorkspaceID != workspace.ID {
		t.Fatalf("unexpected accepted message: %#v", message)
	}
	waitForChat(t, operator)

	messages, err := operator.ChatMessages(context.Background(), api.ChatPageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages.Messages) != 2 || messages.Messages[1].Role != domain.MessageAssistant {
		t.Fatalf("durable chat messages = %#v", messages.Messages)
	}
	if len(messages.Messages[1].EffectMetadata) == 0 {
		t.Fatal("assistant message omitted effect metadata")
	}
	tasks, err := database.ListTasks(context.Background(), store.TaskFilter{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Audit conversion path" || tasks[0].CreatedBy != "chat" {
		t.Fatalf("chat-created tasks = %#v", tasks)
	}
	runs, err := database.ListRuns(context.Background(), store.RunFilter{Types: []domain.RunType{domain.RunChat}})
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunCompleted {
		t.Fatalf("chat runs = %#v, err = %v", runs, err)
	}
}

func TestAffirmativeContextReplyCompletesSetupAndCreatesSafeWork(t *testing.T) {
	result := `{
  "assistant_response":"Context is ready. I created the first local repository task.",
  "effects":[
    {"type":"complete_context"},
    {"type":"create_task","task":{"title":"Pull approved repositories","purpose":"Clone the named repositories into the approved workspace and inspect their structure","why":"Establishes the local code context needed for the mission","priority":"high","definition_of_done":["Each named repository is available under repos and its default branch is documented"]}}
  ]
}`
	service, database, _, workspace := testOperator(t, fakeExecutor{result: result})
	workspace.ContextReady = false
	workspace.ContextPrompted = true
	if err := database.UpdateWorkspace(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AppendMessage(context.Background(), domain.Message{
		WorkspaceID: workspace.ID, Role: domain.MessageAssistant, Status: domain.MessageComplete,
		Content: "I have enough context to begin once you confirm. Should I treat the workspace context as ready?",
	}); err != nil {
		t.Fatal(err)
	}
	service.Start(context.Background())
	if _, err := service.SendChat(context.Background(), api.ChatSend{Content: "yes"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, service)

	updated, err := database.GetWorkspace(context.Background(), workspace.ID)
	if err != nil || !updated.ContextReady {
		t.Fatalf("context was not completed: %#v, %v", updated, err)
	}
	tasks, err := database.ListTasks(context.Background(), store.TaskFilter{WorkspaceID: workspace.ID})
	if err != nil || len(tasks) != 1 || tasks[0].Title != "Pull approved repositories" {
		t.Fatalf("safe work was not created after context completion: %#v, %v", tasks, err)
	}
}

func TestChatChoicePersistsInteractiveActions(t *testing.T) {
	result := `{"assistant_response":"Choose the best starting point.","effects":[{"type":"request_choice","choice":{"prompt":"Which repository first?","options":[{"label":"Marketing site","value":"Inspect the marketing repository first.","primary":true},{"label":"Application","value":"Inspect the application repository first."}]}}]}`
	service, _, _, _ := testOperator(t, fakeExecutor{result: result})
	service.Start(context.Background())
	if _, err := service.SendChat(context.Background(), api.ChatSend{Content: "Help me pick the first repository."}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, service)
	page, err := service.ChatMessages(context.Background(), api.ChatPageRequest{Limit: 10})
	if err != nil || len(page.Messages) != 2 || !strings.Contains(string(page.Messages[1].EffectMetadata), `"actions":[`) || !strings.Contains(string(page.Messages[1].EffectMetadata), `"Marketing site"`) {
		t.Fatalf("choice metadata = %#v, %v", page.Messages, err)
	}
}

func TestChatPacketIncludesWorkspaceMemoryAndGlobalSoul(t *testing.T) {
	operator, _, paths, workspace := testOperator(t, fakeExecutor{})
	workspace.ContextReady = false
	if err := os.WriteFile(filepath.Join(workspace.Path, "PLAN.md"), []byte("# Plan\n\nInspect before creating work."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.UpdateScopeMemory(mustScope(t, paths, workspace.ID), "# Memory\n\nThe owner prefers weekly summaries."); err != nil {
		t.Fatal(err)
	}
	if err := config.UpdateSoul(paths, "# Soul\n\nBe calm, candid, and curious."); err != nil {
		t.Fatal(err)
	}
	message := domain.Message{ID: 1, WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "What next?", Status: domain.MessageComplete}
	request, _, err := operator.chatPacketRequest(context.Background(), workspace, message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request.DurableContext, "weekly summaries") || !strings.Contains(request.Soul, "calm, candid") || request.WorkspaceRoot != workspace.Path || len(request.WorkspaceFiles) != 1 || request.WorkspaceFiles[0].Path != "PLAN.md" {
		t.Fatalf("packet request omitted memory or soul: %#v", request)
	}
}

func TestRecoverTaskQueuesFailureEvidenceAndOwnerContext(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	task, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Audit production observability", Purpose: "Map monitoring gaps", Why: "Protect production reliability",
		Status: domain.TaskFailed, Priority: domain.PriorityHigh, WorkspaceID: workspace.ID,
		DefinitionOfDone: []domain.DefinitionItem{{Text: "Runtime Sentry configuration is verified"}},
		Result: &domain.RunResult{
			Summary:       "Repository coverage was audited and a remediation plan was written.",
			Uncertainties: []string{"Railway runtime variables were not available."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := service.RecoverTask(context.Background(), task.ID, api.TaskRecovery{Note: "Railway is connected now; preserve the report."})
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "Railway is connected now; preserve the report." {
		t.Fatalf("recovery note was not kept as the visible message: %q", message.Content)
	}
	if strings.Contains(message.Content, task.ID) || strings.Contains(message.Content, task.Title) || strings.Contains(message.Content, "Repository coverage was audited") {
		t.Fatalf("visible recovery message exposed internal evidence envelope: %s", message.Content)
	}
	metadata := chatInputMetadata{}
	if json.Unmarshal(message.EffectMetadata, &metadata) != nil || metadata.RecoveryTaskID != task.ID || metadata.RecoveryTaskTitle != task.Title {
		t.Fatalf("recovery metadata = %#v (%s)", metadata, message.EffectMetadata)
	}
	if message.Status != domain.MessageQueued || message.WorkspaceID != workspace.ID {
		t.Fatalf("unexpected recovery message: %#v", message)
	}
}

func TestRecoverTaskRejectsSecretsAndNonRecoverableState(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	failed, err := database.CreateTask(context.Background(), domain.Task{
		Title: "Read analytics", Purpose: "Report traffic", Why: "Measure growth", Status: domain.TaskFailed,
		Priority: domain.PriorityNormal, WorkspaceID: workspace.ID, DefinitionOfDone: []domain.DefinitionItem{{Text: "Traffic is reported"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverTask(context.Background(), failed.ID, api.TaskRecovery{Note: "api key: secret_value_123456789"}); !errors.Is(err, api.ErrInvalid) {
		t.Fatalf("secret recovery note error = %v", err)
	}
	failed.Status = domain.TaskCompleted
	if err := database.UpdateTask(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverTask(context.Background(), failed.ID, api.TaskRecovery{}); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("completed recovery error = %v", err)
	}
}

func mustScope(t *testing.T, paths config.Paths, workspaceID string) config.WorkspaceContextPaths {
	t.Helper()
	scope, err := config.EnsureScope(paths, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

type queuedChatExecutor struct {
	mu      sync.Mutex
	started chan int
	release chan struct{}
	count   int
	prompts []string
}

type parallelLaneExecutor struct {
	started     chan string
	releaseTask chan struct{}
	releaseChat chan struct{}
}

func (executor *parallelLaneExecutor) Run(ctx context.Context, request runner.Request) (runner.ExecutionResult, error) {
	kind := "task"
	result := `{"status":"completed","summary":"Task completed.","files_changed":[],"verification":[{"name":"check","status":"passed","details":"done"}],"artifacts":[],"uncertainties":[],"approval_needed":null}`
	release := executor.releaseTask
	if strings.Contains(request.Prompt, `"assistant_response"`) {
		kind = "chat"
		result = `{"assistant_response":"I can answer while the task continues.","effects":[{"type":"conversation_only"}]}`
		release = executor.releaseChat
	}
	executor.started <- kind
	select {
	case <-ctx.Done():
		return runner.ExecutionResult{Status: domain.RunCancelled}, ctx.Err()
	case <-release:
	}
	started := time.Now().UTC()
	exit := 0
	return runner.ExecutionResult{StartedAt: started, EndedAt: started, ExitCode: &exit, Status: domain.RunCompleted, Stdout: result}, nil
}

func TestChatRunsInParallelWithAutonomousTask(t *testing.T) {
	executor := &parallelLaneExecutor{
		started: make(chan string, 3), releaseTask: make(chan struct{}), releaseChat: make(chan struct{}),
	}
	service, database, _, workspace := testOperator(t, executor)
	if _, err := database.CreateTask(context.Background(), domain.Task{
		WorkspaceID: workspace.ID, Title: "Long autonomous task", Purpose: "Keep the task lane occupied",
		Status: domain.TaskReady, Priority: domain.PriorityNormal, CreatedBy: "user",
		DefinitionOfDone: []domain.DefinitionItem{{Text: "The task finishes"}},
	}); err != nil {
		t.Fatal(err)
	}
	service.Start(context.Background())
	select {
	case kind := <-executor.started:
		if kind != "task" {
			t.Fatalf("first lane = %q, want task", kind)
		}
	case <-time.After(time.Second):
		t.Fatal("autonomous task did not start")
	}
	if _, err := service.SendChat(context.Background(), api.ChatSend{Content: "What is happening while that runs?"}); err != nil {
		t.Fatal(err)
	}
	select {
	case kind := <-executor.started:
		if kind != "chat" {
			t.Fatalf("parallel lane = %q, want chat", kind)
		}
	case <-time.After(time.Second):
		t.Fatal("chat waited behind the active autonomous task")
	}
	close(executor.releaseChat)
	waitForChat(t, service)
	close(executor.releaseTask)
}

func (executor *queuedChatExecutor) Run(ctx context.Context, request runner.Request) (runner.ExecutionResult, error) {
	executor.mu.Lock()
	executor.count++
	index := executor.count
	executor.prompts = append(executor.prompts, request.Prompt)
	executor.mu.Unlock()
	executor.started <- index
	select {
	case <-ctx.Done():
		return runner.ExecutionResult{Status: domain.RunCancelled}, ctx.Err()
	case <-executor.release:
	}
	result := fmt.Sprintf(`{"assistant_response":"reply %d","effects":[{"type":"conversation_only"}]}`, index)
	started := time.Now().UTC()
	exit := 0
	return runner.ExecutionResult{StartedAt: started, EndedAt: started, ExitCode: &exit, Status: domain.RunCompleted, Stdout: result}, nil
}

func TestSendChatQueuesDurablyWhileAnotherMessageRuns(t *testing.T) {
	executor := &queuedChatExecutor{started: make(chan int, 2), release: make(chan struct{}, 2)}
	operator, database, _, workspace := testOperator(t, executor)
	operator.Start(context.Background())
	first, err := operator.SendChat(context.Background(), api.ChatSend{Content: "first request"})
	if err != nil || first.Status != domain.MessageQueued {
		t.Fatalf("first enqueue = %#v, %v", first, err)
	}
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("first queued message did not start")
	}
	second, err := operator.SendChat(context.Background(), api.ChatSend{Content: "second request"})
	if err != nil || second.Status != domain.MessageQueued {
		t.Fatalf("second enqueue = %#v, %v", second, err)
	}
	if queued, err := database.QueuedMessageCount(context.Background()); err != nil || queued != 1 {
		t.Fatalf("queue depth = %d, %v", queued, err)
	}
	executor.release <- struct{}{}
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("second queued message did not start")
	}
	executor.mu.Lock()
	secondPrompt := executor.prompts[1]
	executor.mu.Unlock()
	if !strings.Contains(secondPrompt, "reply 1") || strings.Count(secondPrompt, "second request") != 1 {
		t.Fatalf("second prompt omitted prior answer or duplicated current request: %s", secondPrompt)
	}
	executor.release <- struct{}{}
	waitForChat(t, operator)
	page, err := operator.ChatMessages(context.Background(), api.ChatPageRequest{Limit: 10})
	if err != nil || len(page.Messages) != 4 || page.Messages[0].Status != domain.MessageComplete || page.Messages[2].Status != domain.MessageComplete {
		t.Fatalf("durable FIFO history = %#v, %v; workspace=%s", page.Messages, err, workspace.ID)
	}
}

func TestSendChatRejectsLikelySecretBeforePersistence(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	_, err := operator.SendChat(context.Background(), api.ChatSend{Content: "Plausible API key: plau_1234567890_example_secret"})
	if err == nil || !strings.Contains(err.Error(), "Settings > Secrets") {
		t.Fatalf("SendChat error = %v", err)
	}
	messages, listErr := database.ListMessages(context.Background(), store.MessageFilter{WorkspaceID: workspace.ID})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(messages) != 0 {
		t.Fatalf("secret-like chat input was persisted: %#v", messages)
	}
}

func TestDeleteChatMessageIsWorkspaceScopedAndCascadesThread(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	root, err := database.AppendMessage(ctx, domain.Message{WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "remove this"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AppendMessage(ctx, domain.Message{WorkspaceID: workspace.ID, ParentMessageID: &root.ID, Role: domain.MessageAssistant, Content: "thread reply"}); err != nil {
		t.Fatal(err)
	}
	if err := operator.DeleteChatMessage(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	page, err := operator.ChatMessages(ctx, api.ChatPageRequest{Limit: 10})
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("chat after deletion = %#v, %v", page.Messages, err)
	}

	other, err := database.CreateWorkspace(ctx, domain.Workspace{ID: "other", Name: "Other", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := database.AppendMessage(ctx, domain.Message{WorkspaceID: other.ID, Role: domain.MessageUser, Content: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.DeleteChatMessage(ctx, foreign.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("cross-workspace delete error = %v, want not found", err)
	}
}

func TestSendChatAcceptsCodexReadyStatusForNewTask(t *testing.T) {
	result := `{
  "assistant_response":"I identified the next focused outcome.",
  "effects":[{"type":"create_task","task":{"title":"Define acquisition strategy","purpose":"Create a measurable acquisition plan","why":"Supports the growth mission","priority":"high","status":"ready","definition_of_done":["The plan has measurable success criteria"]}}]
}`
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: result})
	operator.Start(context.Background())
	if _, err := operator.SendChat(context.Background(), api.ChatSend{Content: "What should we focus on next?"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, operator)

	tasks, err := database.ListTasks(context.Background(), store.TaskFilter{WorkspaceID: workspace.ID})
	if err != nil || len(tasks) != 1 || tasks[0].Title != "Define acquisition strategy" {
		t.Fatalf("chat-created tasks = %#v, err = %v", tasks, err)
	}
	page, err := operator.ChatMessages(context.Background(), api.ChatPageRequest{Limit: 10})
	if err != nil || len(page.Messages) != 2 || page.Messages[1].Content != "I identified the next focused outcome." {
		t.Fatalf("chat messages = %#v, err = %v", page.Messages, err)
	}
}

func TestChatPacketSummarizesEmptyAndTopLevelWorkspaceWithoutReadingFiles(t *testing.T) {
	operator, _, _, workspace := testOperator(t, fakeExecutor{})
	request, _, err := operator.chatPacketRequest(context.Background(), workspace, domain.Message{Content: "What should we build?"})
	if err != nil {
		t.Fatal(err)
	}
	if !request.Inventory.Empty || len(request.Inventory.Entries) != 0 {
		t.Fatalf("empty inventory = %#v", request.Inventory)
	}
	repository := filepath.Join(workspace.Path, "site")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "private.txt"), []byte("never-read-this-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	request, _, err = operator.chatPacketRequest(context.Background(), workspace, domain.Message{Content: "Review the workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Inventory.Empty || request.Inventory.FileCount != 1 || len(request.Inventory.Entries) != 1 || request.Inventory.Entries[0].Kind != "repository" {
		t.Fatalf("populated inventory = %#v", request.Inventory)
	}
	packet, err := steering.BuildPacket(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(packet, "never-read-this-secret") {
		t.Fatal("workspace inventory read file contents")
	}
}

func TestChatCreatesProposedPlanWithDurableReference(t *testing.T) {
	result := `{
  "assistant_response":"I drafted a six-month plan for review.",
  "effects":[{"type":"create_plan","plan":{"title":"Six-month launch","objective":"Launch and establish a measurable growth loop","items":[{"kind":"milestone","title":"Foundation ready"},{"kind":"task","title":"Launch beta","purpose":"Ship to the first cohort"}]}}]
}`
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: result})
	operator.Start(context.Background())
	if _, err := operator.SendChat(context.Background(), api.ChatSend{Content: "Draft a six-month launch plan for me to review"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, operator)
	plans, err := database.ListPlans(context.Background(), store.PlanFilter{WorkspaceID: workspace.ID})
	if err != nil || len(plans) != 1 || plans[0].Status != domain.PlanProposed || len(plans[0].Items) != 2 {
		t.Fatalf("plans = %#v, err=%v", plans, err)
	}
	page, err := operator.ChatMessages(context.Background(), api.ChatPageRequest{Limit: 10})
	if err != nil || len(page.Messages) != 2 || !strings.Contains(string(page.Messages[1].EffectMetadata), `"type":"plan"`) {
		t.Fatalf("chat plan reference = %#v, err=%v", page.Messages, err)
	}
}

func TestChatCreatesScheduleOnlyForExplicitRequest(t *testing.T) {
	result := `{
  "assistant_response":"I scheduled a weekly orientation.",
  "effects":[{"type":"create_schedule","schedule":{"name":"Weekly review","kind":"orient","expression":"@weekly","reason":"Review mission progress"}}]
}`
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: result})
	operator.Start(context.Background())
	if _, err := operator.SendChat(context.Background(), api.ChatSend{Content: "Schedule a weekly mission review"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, operator)
	schedules, err := database.ListSchedules(context.Background(), store.ScheduleFilter{WorkspaceID: workspace.ID})
	if err != nil || len(schedules) != 1 || schedules[0].Expression != "@weekly" {
		t.Fatalf("schedules = %#v, err=%v", schedules, err)
	}
}

func TestChatCreatesTypedDataset(t *testing.T) {
	result := `{
  "assistant_response":"I created a typed lead dataset.",
  "effects":[{"type":"create_dataset","dataset":{"name":"Leads","slug":"leads","description":"Qualified leads","schema":[{"name":"email","type":"string","nullable":false},{"name":"score","type":"integer","nullable":false}],"unique_key":["email"]}}]
}`
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: result})
	operator.Start(context.Background())
	if _, err := operator.SendChat(context.Background(), api.ChatSend{Content: "Create a typed dataset for our leads"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, operator)
	datasets, err := database.ListDatasets(context.Background(), store.DatasetFilter{WorkspaceID: workspace.ID})
	if err != nil || len(datasets) != 1 || len(datasets[0].Schema) != 2 || datasets[0].UniqueKey[0] != "email" {
		t.Fatalf("datasets = %#v, err=%v", datasets, err)
	}
}

func TestChatUpsertsRowsIntoAuthoritativeDataset(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	dataset, err := database.CreateDataset(context.Background(), domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Leads", Slug: "leads", UniqueKey: []string{"email"},
		Schema: []domain.DatasetColumn{{Name: "email", Type: domain.DatasetString}, {Name: "score", Type: domain.DatasetInteger}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operator.runner = fakeExecutor{result: fmt.Sprintf(`{
  "assistant_response":"I upserted the two validated lead rows.",
  "effects":[{"type":"upsert_dataset_rows","dataset_rows":{"dataset_id":%q,"rows":[{"email":"a@example.com","score":7},{"email":"b@example.com","score":5}]}}]
}`, dataset.ID)}
	operator.Start(context.Background())
	if _, err := operator.SendChat(context.Background(), api.ChatSend{Content: "Upsert these two lead rows into the Leads dataset"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, operator)
	page, err := database.QueryDatasetRows(context.Background(), dataset.ID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil || page.Total != 2 || len(page.Rows) != 2 {
		t.Fatalf("dataset rows = %#v, err=%v", page, err)
	}
}

func TestChatCreatesProtectedSecretRequestWithoutCredentials(t *testing.T) {
	result := `{
  "assistant_response":"I opened protected setup for the analytics API key. Once it is saved, I can create a script that receives it as an environment variable.",
  "effects":[{"type":"create_secret","secret":{"name":"analytics_api_key","label":"Analytics API key","description":"Create this key in the provider's account settings."}}]
}`
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: result})
	operator.Start(context.Background())
	if _, err := operator.SendChat(context.Background(), api.ChatSend{Content: "Set up analytics access and ask me securely for its API key"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, operator)
	items, err := database.ListSecretRecords(context.Background(), store.SecretRecordFilter{WorkspaceID: workspace.ID})
	if err != nil || len(items) != 1 || items[0].Name != "analytics_api_key" {
		t.Fatalf("secrets = %#v, err=%v", items, err)
	}
}

func TestChatPacketIncludesBoundedDatasetSearchRows(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	dataset, err := database.CreateDataset(context.Background(), domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Leads", Slug: "leads", UniqueKey: []string{"email"},
		Schema: []domain.DatasetColumn{{Name: "email", Type: domain.DatasetString}, {Name: "segment", Type: domain.DatasetString}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 60)
	for index := range rows {
		segment := "other"
		if index < 3 {
			segment = "enterprise"
		}
		rows[index] = map[string]any{"email": fmt.Sprintf("lead-%d@example.com", index), "segment": segment}
	}
	if _, err := database.BulkWriteDatasetRowsForWorkspace(context.Background(), workspace.ID, dataset.ID, rows, store.DatasetUpsert); err != nil {
		t.Fatal(err)
	}
	request, state, err := operator.chatPacketRequest(context.Background(), workspace, domain.Message{Content: `Which Leads are in "enterprise"?`})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.DatasetQueries) != 1 || request.DatasetQueries[0].Search != "enterprise" || len(request.DatasetQueries[0].Rows) != 3 || request.DatasetQueries[0].Total != 3 {
		t.Fatalf("dataset query context = %#v", request.DatasetQueries)
	}
	if len(state.DatasetQueries) != 1 || state.DatasetQueries[0].DatasetID != dataset.ID {
		t.Fatalf("validation query state = %#v", state.DatasetQueries)
	}
}

func TestChatDatasetDeleteCreatesApprovalThenDeletesExactRow(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{})
	ctx := context.Background()
	dataset, err := database.CreateDataset(ctx, domain.Dataset{
		WorkspaceID: workspace.ID, Name: "Leads", Slug: "leads", UniqueKey: []string{"email"},
		Schema: []domain.DatasetColumn{{Name: "email", Type: domain.DatasetString}},
	})
	if err != nil {
		t.Fatal(err)
	}
	written, err := database.BulkWriteDatasetRowsForWorkspace(ctx, workspace.ID, dataset.ID, []map[string]any{{"email": "delete@example.com"}}, store.DatasetUpsert)
	if err != nil {
		t.Fatal(err)
	}
	effect := steering.Effect{Type: steering.EffectDeleteDatasetRow, DatasetRow: &steering.DatasetRowChange{DatasetID: dataset.ID, RowID: written.Rows[0].ID}}
	view, err := operator.applyChatEffect(ctx, effect, workspace)
	if err != nil || view.Entity == nil || view.Entity.Type != "approval" {
		t.Fatalf("delete approval view = %#v, err=%v", view, err)
	}
	if page, err := database.QueryDatasetRows(ctx, dataset.ID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10}); err != nil || page.Total != 1 {
		t.Fatalf("row deleted before approval: %#v, %v", page, err)
	}
	if _, err := operator.ResolveApproval(ctx, view.Entity.ID, domain.ApprovalApproved, ""); err != nil {
		t.Fatal(err)
	}
	if page, err := database.QueryDatasetRows(ctx, dataset.ID, store.DatasetRowFilter{WorkspaceID: workspace.ID, Limit: 10}); err != nil || page.Total != 0 {
		t.Fatalf("approved row not deleted: %#v, %v", page, err)
	}
}

func TestChatUsesOnlyRecentMainHistory(t *testing.T) {
	operator, database, _, workspace := testOperator(t, fakeExecutor{result: `{"assistant_response":"No change needed.","effects":[{"type":"conversation_only"}]}`})
	for index := 0; index < 15; index++ {
		if _, err := database.AppendMessage(context.Background(), domain.Message{
			WorkspaceID: workspace.ID, Role: domain.MessageUser, Content: "historic", Effect: domain.EffectConversationOnly,
		}); err != nil {
			t.Fatal(err)
		}
	}
	operator.Start(context.Background())
	if _, err := operator.SendChat(context.Background(), api.ChatSend{Content: "What next?"}); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, operator)

	page, err := operator.ChatMessages(context.Background(), api.ChatPageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 10 || !page.HasMore {
		t.Fatalf("chat page = %#v", page)
	}
}

func TestOperatorSettingsAcceptCurrentReasoningEfforts(t *testing.T) {
	operator, _, _, _ := testOperator(t, fakeExecutor{})
	for _, effort := range []string{"", "none", "low", "medium", "high", "xhigh", "max", "ultra"} {
		value, err := operator.UpdateOperatorSettings(context.Background(), api.OperatorSettings{
			CodexModel: "gpt-5.6", CodexReasoningEffort: effort,
		})
		if err != nil || value.CodexReasoningEffort != effort {
			t.Fatalf("effort %q: value=%#v err=%v", effort, value, err)
		}
	}
}

func TestExplicitContextConfirmationPhrases(t *testing.T) {
	for _, message := range []string{"You have enough context to begin.", "Go ahead and start", "Proceed with the assumptions"} {
		if !explicitlyConfirmsContextReady(message) {
			t.Fatalf("confirmation was not recognized: %q", message)
		}
	}
	if explicitlyConfirmsContextReady("Tell me whether you have enough context") {
		t.Fatal("a question was treated as confirmation")
	}
}

func TestShortAffirmativeConfirmsOnlyAfterContextPrompt(t *testing.T) {
	prompt := []steering.Message{{Role: "assistant", Content: "I have enough context to begin once you confirm. Should I treat the workspace context as ready?"}}
	if !confirmsContextReadyReply("yes", prompt) {
		t.Fatal("short affirmative did not confirm the immediately preceding context prompt")
	}
	if confirmsContextReadyReply("yes", []steering.Message{{Role: "assistant", Content: "Should I create a weekly report?"}}) {
		t.Fatal("unrelated affirmative was treated as context confirmation")
	}
}

func waitForChat(t *testing.T, operator *Operator) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for operator.ChatWorking() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if operator.ChatWorking() {
		t.Fatal("chat did not finish")
	}
}
