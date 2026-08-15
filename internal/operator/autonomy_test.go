package operator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
)

type parallelTaskExecutor struct {
	started chan string
	release chan struct{}
	mu      sync.Mutex
	active  int
	peak    int
}

type steeringRepairExecutor struct {
	mu       sync.Mutex
	requests []runner.Request
}

func (executor *steeringRepairExecutor) Run(_ context.Context, request runner.Request) (runner.ExecutionResult, error) {
	executor.mu.Lock()
	executor.requests = append(executor.requests, request)
	count := len(executor.requests)
	executor.mu.Unlock()
	output := `{"assistant_response":"","effects":[]}`
	if count == 2 {
		output = `{"assistant_response":"Repaired safely.","effects":[{"type":"conversation_only"}]}`
	}
	now := time.Now().UTC()
	exit := 0
	return runner.ExecutionResult{Status: domain.RunCompleted, StartedAt: now, EndedAt: now, ExitCode: &exit, Stdout: output}, nil
}

func (executor *parallelTaskExecutor) Run(ctx context.Context, request runner.Request) (runner.ExecutionResult, error) {
	executor.mu.Lock()
	executor.active++
	if executor.active > executor.peak {
		executor.peak = executor.active
	}
	executor.mu.Unlock()
	executor.started <- request.Prompt
	select {
	case <-ctx.Done():
		return runner.ExecutionResult{Status: domain.RunCancelled}, ctx.Err()
	case <-executor.release:
	}
	executor.mu.Lock()
	executor.active--
	executor.mu.Unlock()
	started := time.Now().UTC()
	exit := 0
	return runner.ExecutionResult{Status: domain.RunCompleted, StartedAt: started, EndedAt: started, ExitCode: &exit,
		Stdout: `{"status":"completed","summary":"Done.","files_changed":[],"verification":[{"name":"check","status":"passed","details":"ok"}],"artifacts":[],"uncertainties":[]}`,
	}, nil
}

func TestIndependentTaskWorkersRunToConfiguredBound(t *testing.T) {
	executor := &parallelTaskExecutor{started: make(chan string, 3), release: make(chan struct{}, 3)}
	service, database, _, workspace := testOperator(t, executor)
	settings, err := database.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxParallelTasks = 2
	if err := database.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Independent one", "Independent two", "Independent three"} {
		if _, err := database.CreateTask(context.Background(), domain.Task{WorkspaceID: workspace.ID, Title: title, Purpose: "Run independently", Status: domain.TaskReady, Priority: domain.PriorityNormal, DefinitionOfDone: []domain.DefinitionItem{{Text: "Done"}}}); err != nil {
			t.Fatal(err)
		}
	}
	service.Start(context.Background())
	for count := 0; count < 2; count++ {
		select {
		case <-executor.started:
		case <-time.After(2 * time.Second):
			t.Fatal("parallel task worker did not start")
		}
	}
	select {
	case <-executor.started:
		t.Fatal("worker exceeded configured task bound")
	case <-time.After(100 * time.Millisecond):
	}
	executor.mu.Lock()
	peak := executor.peak
	executor.mu.Unlock()
	if peak != 2 {
		t.Fatalf("peak task concurrency = %d", peak)
	}
	executor.release <- struct{}{}
	select {
	case <-executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("third independent task did not fill released capacity")
	}
	executor.release <- struct{}{}
	executor.release <- struct{}{}
}

func TestOperatorSettingsExposeAndValidateParallelBound(t *testing.T) {
	service, _, _, _ := testOperator(t, fakeExecutor{})
	updated, err := service.UpdateOperatorSettings(context.Background(), api.OperatorSettings{CodexModel: "gpt-5.6", MaxParallelTasks: 4})
	if err != nil || updated.MaxParallelTasks != 4 {
		t.Fatalf("update settings = %#v, %v", updated, err)
	}
	loaded, err := service.OperatorSettings(context.Background())
	if err != nil || loaded.MaxParallelTasks != 4 {
		t.Fatalf("loaded settings = %#v, %v", loaded, err)
	}
	if _, err := service.UpdateOperatorSettings(context.Background(), api.OperatorSettings{MaxParallelTasks: store.MaximumParallelTasks + 1}); err == nil {
		t.Fatal("oversized parallel task setting accepted")
	}
}

func TestTaskLifecycleMessagesAreDeduplicatedAndActionable(t *testing.T) {
	service, database, _, workspace := testOperator(t, fakeExecutor{})
	task := domain.Task{ID: "task-life", WorkspaceID: workspace.ID, Title: "Prepare report", Status: domain.TaskFailed}
	result := &domain.RunResult{Status: "failed", Summary: "The source API was unavailable.", Artifacts: []domain.Artifact{{ID: "artifact-1", Name: "Failure report"}}}
	service.appendTaskLifecycleMessage(context.Background(), task, "run-life", "task.failed", result, "")
	service.appendTaskLifecycleMessage(context.Background(), task, "run-life", "task.failed", result, "")
	messages, err := database.ListMessages(context.Background(), store.MessageFilter{WorkspaceID: workspace.ID, Role: domain.MessageAssistant, Limit: 10})
	if err != nil || len(messages) != 1 {
		t.Fatalf("lifecycle messages = %#v, %v", messages, err)
	}
	if !strings.Contains(messages[0].Content, "needs attention") || !strings.Contains(string(messages[0].EffectMetadata), `"automated_lifecycle":true`) || !strings.Contains(string(messages[0].EffectMetadata), "artifact-1") {
		t.Fatalf("lifecycle message = %#v", messages[0])
	}
}

func TestCancelTaskTargetsOnlyExactConcurrentRun(t *testing.T) {
	service := &Operator{activeRuns: make(map[string]activeRun)}
	oneCancelled, twoCancelled := make(chan struct{}), make(chan struct{})
	service.setActive("task-one", "run-one", func() { close(oneCancelled) })
	service.setActive("task-two", "run-two", func() { close(twoCancelled) })
	service.cancelTask("task-one")
	select {
	case <-oneCancelled:
	default:
		t.Fatal("exact task was not cancelled")
	}
	select {
	case <-twoCancelled:
		t.Fatal("unrelated concurrent task was cancelled")
	default:
	}
}

func TestChatRepairsInvalidStructuredResultExactlyOnceBeforeEffects(t *testing.T) {
	executor := &steeringRepairExecutor{}
	service, database, _, workspace := testOperator(t, executor)
	queued, err := service.SendChat(context.Background(), api.ChatSend{Content: "Summarize the safe next step."})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := database.ClaimNextQueuedMessage(context.Background())
	if err != nil || claimed.ID != queued.ID {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	service.runChat(claimed, workspace)
	executor.mu.Lock()
	requests := append([]runner.Request(nil), executor.requests...)
	executor.mu.Unlock()
	if len(requests) != 2 || !strings.Contains(requests[1].Prompt, "Structured Result Repair") || !strings.Contains(requests[1].Prompt, "before any effects were applied") {
		t.Fatalf("repair requests = %#v", requests)
	}
	messages, err := database.ListMessages(context.Background(), store.MessageFilter{WorkspaceID: workspace.ID, Limit: 10})
	if err != nil || len(messages) != 2 || messages[1].Content != "Repaired safely." {
		t.Fatalf("repaired Chat messages = %#v, %v", messages, err)
	}
}
