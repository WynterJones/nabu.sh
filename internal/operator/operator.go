package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/appruntime"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/drafting"
	"github.com/nabu-sh/nabu/internal/eventbus"
	"github.com/nabu-sh/nabu/internal/integrations"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
	"github.com/nabu-sh/nabu/internal/version"
)

const (
	maxReadyTasks        = 5
	maximumParallelTasks = store.MaximumParallelTasks
)

type Executor interface {
	Run(context.Context, runner.Request) (runner.ExecutionResult, error)
}

type Automation interface {
	RunScriptNow(context.Context, string) (domain.ScriptRun, error)
}

type Operator struct {
	store           *store.Store
	runner          Executor
	automation      Automation
	credentials     credentials.Backend
	integrations    *integrations.Service
	integrationSink IntegrationResponseSink
	appRuntime      *appruntime.Manager
	paths           config.Paths
	bus             *eventbus.Bus
	logger          *slog.Logger
	wake            chan struct{}
	taskWake        chan struct{}
	chatWake        chan struct{}

	mu                        sync.Mutex
	aiMu                      sync.Mutex
	queueMu                   sync.Mutex
	mcpAuthMu                 sync.Mutex
	mcpAuthCache              map[string]mcpAuthCacheEntry
	activeRuns                map[string]activeRun
	orientationClaimed        bool
	chatActive                bool
	chatCancel                context.CancelFunc
	lifecycleContext          context.Context
	lifecycleCancel           context.CancelFunc
	lifecycleDone             chan struct{}
	codexState                string
	codexReason               string
	codexRetryAt              *time.Time
	emptyOrientationAttempted bool
	orientationRetryAttempted bool
}

type activeRun struct {
	taskID string
	cancel context.CancelFunc
}

func (o *Operator) SetAutomation(automation Automation) {
	o.mu.Lock()
	o.automation = automation
	o.mu.Unlock()
}

func New(database *store.Store, executor Executor, paths config.Paths, bus *eventbus.Bus, logger *slog.Logger) *Operator {
	return NewWithIntegrations(database, executor, paths, bus, logger, credentials.NewPlatform(), nil, nil)
}

// NewWithIntegrations creates an operator with an explicit credential backend,
// HTTP transport, and optional response sink. Production uses New; tests and
// audited embedders can inject dependencies without mutating a running service.
func NewWithIntegrations(database *store.Store, executor Executor, paths config.Paths, bus *eventbus.Bus, logger *slog.Logger, credentialBackend credentials.Backend, integrationClient integrations.HTTPDoer, sink IntegrationResponseSink) *Operator {
	if bus == nil {
		bus = eventbus.New()
	}
	if logger == nil {
		logger = slog.Default()
	}
	if credentialBackend == nil {
		credentialBackend = credentials.Unsupported{}
	}
	integrationService, _ := integrations.NewService(credentialBackend, integrationClient)
	appRuntime, runtimeErr := appruntime.New(paths.Runs)
	if runtimeErr != nil {
		logger.Error("local app runtime unavailable", "error", runtimeErr)
	}
	return &Operator{
		store:           database,
		runner:          executor,
		paths:           paths,
		bus:             bus,
		logger:          logger,
		wake:            make(chan struct{}, 1),
		taskWake:        make(chan struct{}, maximumParallelTasks),
		chatWake:        make(chan struct{}, 1),
		credentials:     credentialBackend,
		integrations:    integrationService,
		integrationSink: sink,
		appRuntime:      appRuntime,
		mcpAuthCache:    make(map[string]mcpAuthCacheEntry),
		activeRuns:      make(map[string]activeRun),
	}
}

func (o *Operator) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	lifecycle, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	o.mu.Lock()
	if o.lifecycleCancel != nil {
		o.mu.Unlock()
		cancel()
		return
	}
	o.lifecycleContext = lifecycle
	o.lifecycleCancel = cancel
	o.lifecycleDone = done
	o.mu.Unlock()
	go func() {
		defer close(done)
		var workers sync.WaitGroup
		workers.Add(2 + maximumParallelTasks)
		go func() {
			defer workers.Done()
			o.loop(lifecycle)
		}()
		go func() {
			defer workers.Done()
			o.chatLoop(lifecycle)
		}()
		for worker := 0; worker < maximumParallelTasks; worker++ {
			worker := worker
			go func() {
				defer workers.Done()
				o.taskLoop(lifecycle, worker)
			}()
		}
		workers.Wait()
	}()
	o.signal()
	o.signalChat()
	go o.startAutoApps(lifecycle)
}

func (o *Operator) taskLoop(ctx context.Context, worker int) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-o.taskWake:
		}
		if err := o.taskWorkOnce(ctx, worker); err != nil && !errors.Is(err, context.Canceled) {
			o.logger.Error("task worker failed", "worker", worker+1, "error", err)
		}
	}
}

// Stop cancels active work and waits for the operator loop to leave its
// lifecycle. It is safe to call repeatedly and gives tests and the daemon a
// deterministic point after which the store and run directories can close.
func (o *Operator) Stop(ctx context.Context) error {
	o.mu.Lock()
	cancel, done := o.lifecycleCancel, o.lifecycleDone
	o.mu.Unlock()
	if cancel == nil {
		if o.appRuntime != nil {
			return o.appRuntime.Shutdown(ctx)
		}
		return nil
	}
	cancel()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		var runtimeErr error
		if o.appRuntime != nil {
			runtimeErr = o.appRuntime.Shutdown(ctx)
		}
		o.mu.Lock()
		o.lifecycleCancel, o.lifecycleDone, o.lifecycleContext = nil, nil, nil
		o.mu.Unlock()
		return runtimeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Operator) loop(ctx context.Context) {
	// Mutations signal the loop immediately. This slower ticker is only a
	// recovery/fallback heartbeat and keeps an idle daemon inexpensive.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			o.cancelActive()
			return
		case <-ticker.C:
		case <-o.wake:
		}
		if err := o.workOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			o.logger.Error("operator cycle failed", "error", err)
		}
	}
}

// chatLoop gives conversation its own single-file FIFO lane. Autonomous work
// remains one-at-a-time, while an owner can still ask questions, add context,
// or cancel the running task without waiting for that task's Codex process.
func (o *Operator) chatLoop(ctx context.Context) {
	// New messages wake this lane immediately; polling only recovers work left
	// queued across an unusual missed signal or restart boundary.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-o.chatWake:
		}
		if err := o.chatWorkOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			o.logger.Error("chat cycle failed", "error", err)
		}
	}
}

func (o *Operator) chatWorkOnce(ctx context.Context) error {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.SetupComplete || !settings.MissionStarted || !o.codexReady(time.Now().UTC()) {
		return nil
	}
	message, err := o.store.ClaimNextQueuedMessage(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	workspace, err := o.store.GetWorkspace(ctx, message.WorkspaceID)
	if err != nil {
		message.Status = domain.MessageFailed
		_ = o.store.UpdateMessage(context.WithoutCancel(ctx), message)
		return err
	}
	if !workspace.Allowed || !workspace.MissionStarted {
		message.Status = domain.MessageFailed
		_ = o.store.UpdateMessage(context.WithoutCancel(ctx), message)
		return fmt.Errorf("queued chat workspace is not available")
	}
	o.mu.Lock()
	o.chatActive = true
	o.mu.Unlock()
	o.runChat(message, workspace)
	return nil
}

func (o *Operator) workOnce(ctx context.Context) error {
	o.aiMu.Lock()
	defer o.aiMu.Unlock()
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.SetupComplete {
		return nil
	}
	if o.isActive() {
		_ = o.store.ResetIdleChecks(ctx, settings.ActiveWorkspaceID)
		return nil
	}
	if settings.MissionStarted && settings.ActiveWorkspaceID != "" {
		workspace, workspaceErr := o.store.GetWorkspace(ctx, settings.ActiveWorkspaceID)
		if workspaceErr != nil {
			return workspaceErr
		}
		if !workspace.ContextReady {
			if err := o.ensureContextPrompt(ctx, workspace); err != nil {
				return err
			}
		}
	}
	if !o.codexReady(time.Now().UTC()) {
		return nil
	}
	// Chat gets first claim on new Codex capacity. Existing tasks keep running
	// in parallel, but orientation waits until the conversational FIFO drains.
	if o.ChatWorking() {
		_ = o.store.ResetIdleChecks(ctx, settings.ActiveWorkspaceID)
		return nil
	}
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return err
	}
	if !workspace.ContextReady {
		return nil
	}
	if settings.Paused {
		_ = o.store.ResetIdleChecks(ctx, workspace.ID)
		return nil
	}
	if !settings.MissionStarted {
		_ = o.store.ResetIdleChecks(ctx, workspace.ID)
		return nil
	}
	o.queueMu.Lock()
	err = o.promoteDuePlannedTasks(ctx, settings.ActiveWorkspaceID, time.Now().UTC())
	o.queueMu.Unlock()
	if err != nil {
		return err
	}
	o.queueMu.Lock()
	if o.isActive() {
		o.queueMu.Unlock()
		return nil
	}
	queued, orientationErr := o.store.ConsumeOrientationRequest(ctx)
	if orientationErr != nil {
		o.queueMu.Unlock()
		return orientationErr
	}
	if queued {
		o.mu.Lock()
		o.orientationClaimed = true
		o.mu.Unlock()
	}
	o.queueMu.Unlock()
	if queued {
		defer func() {
			o.mu.Lock()
			o.orientationClaimed = false
			o.mu.Unlock()
			o.signal()
		}()
		o.emptyOrientationAttempted = true
		_ = o.store.ResetIdleChecks(ctx, workspace.ID)
		return o.runOrientation(ctx)
	}

	if settings.LastOrientationAt == nil && !o.emptyOrientationAttempted {
		o.emptyOrientationAttempted = true
		_ = o.store.ResetIdleChecks(ctx, workspace.ID)
		_, err = o.store.RequestOrientation(ctx)
		if err == nil {
			o.signal()
		}
		return err
	}
	due, err := o.store.RecordIdleCheck(ctx, workspace.ID, time.Now().UTC(), idleMinimumDuration, idleReviewLease)
	if err != nil {
		return err
	}
	if due {
		return o.runIdleOrientation(ctx)
	}
	return nil
}

func (o *Operator) taskWorkOnce(ctx context.Context, worker int) error {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	limit := settings.MaxParallelTasks
	if limit < 1 {
		limit = 1
	}
	if limit > maximumParallelTasks {
		limit = maximumParallelTasks
	}
	if worker >= limit || !settings.SetupComplete || !settings.MissionStarted || settings.Paused || settings.ActiveWorkspaceID == "" || !o.codexReady(time.Now().UTC()) {
		return nil
	}
	if o.ChatWorking() {
		return nil
	}
	if o.exclusiveActive() {
		return nil
	}
	workspace, err := o.store.GetWorkspace(ctx, settings.ActiveWorkspaceID)
	if err != nil {
		return err
	}
	if !workspace.Allowed || !workspace.ContextReady || !workspace.MissionStarted {
		return nil
	}
	o.queueMu.Lock()
	if err := o.promoteDuePlannedTasks(ctx, workspace.ID, time.Now().UTC()); err != nil {
		o.queueMu.Unlock()
		return err
	}
	o.mu.Lock()
	orientationActive := o.orientationClaimed
	o.mu.Unlock()
	if orientationActive {
		o.queueMu.Unlock()
		return nil
	}
	task, err := o.store.ClaimNextReadyTaskForWorkspace(ctx, workspace.ID)
	var taskContext context.Context
	var taskCancel context.CancelFunc
	if err == nil {
		taskContext, taskCancel = context.WithCancel(ctx)
		o.setActive(task.ID, "claim:"+task.ID, taskCancel)
	}
	o.queueMu.Unlock()
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = o.store.ResetIdleChecks(ctx, workspace.ID)
	// Persistence atomically enforces the global worker limit and dependency
	// readiness. Waking a peer fills capacity without allowing duplicate claims.
	o.signal()
	defer func() {
		taskCancel()
		o.clearActive("claim:" + task.ID)
		o.signal()
	}()
	return o.runTask(taskContext, task)
}

func (o *Operator) ensureContextPrompt(ctx context.Context, workspace domain.Workspace) error {
	if workspace.ContextReady || workspace.ContextPrompted || !workspace.MissionStarted {
		return nil
	}
	workspace.ContextPrompted = true
	if err := o.store.UpdateWorkspace(ctx, workspace); err != nil {
		return err
	}
	content := "Before I create or run work, I need to understand this workspace well enough to produce real results.\n\nAre we starting fresh, or do you already have a product, website, repositories, audience, content, analytics, and business accounts I should work with? Tell me what exists today—even if the answer is nothing. I’ll identify the smallest remaining gaps and tell you clearly when I have enough context to begin."
	message, err := o.store.AppendMessage(ctx, domain.Message{
		WorkspaceID: workspace.ID, Role: domain.MessageAssistant, Content: content,
		Status: domain.MessageComplete, Effect: domain.EffectConversationOnly,
	})
	if err != nil {
		workspace.ContextPrompted = false
		_ = o.store.UpdateWorkspace(context.WithoutCancel(ctx), workspace)
		return err
	}
	o.emitForWorkspace(ctx, workspace.ID, "chat.message", strconv.FormatInt(message.ID, 10), message)
	o.emitForWorkspace(ctx, workspace.ID, "context.requested", workspace.ID, map[string]bool{"context_ready": false})
	return nil
}

func (o *Operator) promoteDuePlannedTasks(ctx context.Context, workspaceID string, now time.Time) error {
	if workspaceID == "" {
		return nil
	}
	ready, err := o.store.ReadyTaskCountForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	if ready >= maxReadyTasks {
		return nil
	}
	due, err := o.store.ListTasks(ctx, store.TaskFilter{
		WorkspaceID: workspaceID, Statuses: []domain.TaskStatus{domain.TaskIdea}, PlannedTo: &now,
	})
	if err != nil {
		return err
	}
	for _, task := range due {
		if ready >= maxReadyTasks {
			break
		}
		task.Status = domain.TaskReady
		task.UpdatedAt = now
		if err := o.store.UpdateTask(ctx, task); err != nil {
			return err
		}
		o.emitForWorkspace(ctx, workspaceID, "task.planned.ready", task.ID, task)
		ready++
	}
	return nil
}

func (o *Operator) Status(ctx context.Context) (domain.StatusSnapshot, error) {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	chatQueued, err := o.store.QueuedMessageCount(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	running, err := o.store.ListTasks(ctx, store.TaskFilter{Statuses: []domain.TaskStatus{domain.TaskRunning}, Limit: maximumParallelTasks})
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	readyCount, err := o.store.ReadyTaskCount(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	needs, err := o.store.ListTasks(ctx, store.TaskFilter{Statuses: []domain.TaskStatus{domain.TaskNeedsApproval, domain.TaskFailed, domain.TaskWaiting}})
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	failedOrientations, err := o.store.ListRuns(ctx, store.RunFilter{Statuses: []domain.RunStatus{domain.RunFailed, domain.RunCancelled, domain.RunTimedOut}, Types: []domain.RunType{domain.RunOrient}, Limit: 1})
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	orientationNeedsAttention := len(failedOrientations) > 0 && (settings.LastOrientationAt == nil || failedOrientations[0].StartedAt.After(*settings.LastOrientationAt))
	codexAvailable := binaryAvailable(settings.CodexPath, "codex")
	codexState, codexReason, codexRetryAt := o.codexHealth(codexAvailable)
	chatActive := o.ChatActive()
	autonomousActive := o.isActive()
	status := domain.GlobalIdle
	switch {
	case settings.Paused:
		status = domain.GlobalPaused
	case len(running) > 0 || autonomousActive || chatActive:
		status = domain.GlobalWorking
	case hasStatus(needs, domain.TaskNeedsApproval):
		status = domain.GlobalWaitingForApproval
	case chatQueued > 0 && codexState != "available":
		status = domain.GlobalNeedsAttention
	case len(needs) > 0 || orientationNeedsAttention:
		status = domain.GlobalNeedsAttention
	}
	snapshot := domain.StatusSnapshot{
		Status: status, DisplayName: settings.DisplayName, SetupComplete: settings.SetupComplete,
		MissionStarted: settings.MissionStarted, Paused: settings.Paused,
		CodexAvailable: codexAvailable && codexState == "available", Version: version.Current(),
		Activities: []domain.OperatorActivity{}, ChatQueued: chatQueued,
		ReadyCount: readyCount, NeedsAttention: len(needs),
	}
	if workspace, workspaceErr := o.store.ActiveWorkspace(ctx); workspaceErr == nil {
		snapshot.ContextReady = workspace.ContextReady
		if idleState, idleErr := o.store.GetIdleStewardState(ctx, workspace.ID); idleErr == nil {
			snapshot.NextOrientationAt = idleState.NextRunAt
		}
	}
	setStatusHealth(&snapshot, codexState, codexReason, codexRetryAt, o.paths.Root)
	if orientationNeedsAttention {
		snapshot.NeedsAttention++
	}
	if len(running) > 0 {
		snapshot.ActiveTask = &running[0]
		for _, task := range running {
			snapshot.Activities = append(snapshot.Activities, domain.OperatorActivity{
				Kind: "task", Label: task.Title, Status: "running", EntityID: task.ID,
				Detail: "Autonomous task in progress",
			})
		}
	}
	_, activeRunID, _ := o.activeState()
	if autonomousActive && len(running) == 0 {
		activity := domain.OperatorActivity{Kind: "operator", Label: "Nabu is preparing the next step", Status: "running", EntityID: activeRunID}
		if activeRunID != "" {
			if run, runErr := o.store.GetRun(ctx, activeRunID); runErr == nil {
				activity.Kind = "run"
				switch run.Type {
				case domain.RunOrient:
					activity.Label = "Reviewing the workspace queue"
				case domain.RunChat:
					activity.Label = "Preparing work from Chat"
				default:
					activity.Label = "Running operator work"
				}
			}
		}
		snapshot.Activities = append(snapshot.Activities, activity)
	}
	if chatActive {
		snapshot.Activities = append(snapshot.Activities, domain.OperatorActivity{
			Kind: "chat", Label: "Replying in Chat", Status: "running", Detail: "Conversation work is running in its own lane",
		})
	}
	if chatQueued > 0 {
		label := fmt.Sprintf("%d Chat messages queued", chatQueued)
		if chatQueued == 1 {
			label = "1 Chat message queued"
		}
		activityStatus := "queued"
		detail := "Nabu will begin it shortly"
		if codexState != "available" {
			activityStatus = "waiting"
			detail = codexReason
		}
		snapshot.Activities = append(snapshot.Activities, domain.OperatorActivity{
			Kind: "chat", Label: label, Status: activityStatus, Detail: detail,
		})
	}
	return snapshot, nil
}

func (o *Operator) Mission(ctx context.Context) (domain.Mission, error) {
	mission, err := o.store.ActiveMission(ctx)
	return mission, translateNotFound(err)
}

func (o *Operator) UpdateMission(ctx context.Context, input api.MissionUpdate) (domain.Mission, error) {
	mission, err := o.store.ActiveMission(ctx)
	if err != nil {
		return domain.Mission{}, translateNotFound(err)
	}
	nextStatement := redactSecrets(strings.TrimSpace(input.Statement))
	nextContext := redactSecrets(strings.TrimSpace(input.Context))
	changed := nextStatement != mission.Statement || nextContext != mission.Context
	mission.Statement = nextStatement
	mission.Context = nextContext
	mission.UpdatedAt = time.Now().UTC()
	if err := o.store.UpdateMission(ctx, mission); err != nil {
		return domain.Mission{}, err
	}
	scope, err := o.activeScopePaths(ctx)
	if err != nil {
		return domain.Mission{}, err
	}
	if err := writeContextFile(scope.Mission, "Mission", mission.Statement, mission.Context); err != nil {
		return domain.Mission{}, err
	}
	if workspace, workspaceErr := o.store.ActiveWorkspace(ctx); changed && workspaceErr == nil && workspace.ContextReady {
		workspace.ContextReady = false
		workspace.ContextPrompted = false
		if workspaceErr = o.store.UpdateWorkspace(ctx, workspace); workspaceErr != nil {
			return domain.Mission{}, workspaceErr
		}
		if workspaceErr = o.ensureContextPrompt(ctx, workspace); workspaceErr != nil {
			return domain.Mission{}, workspaceErr
		}
	}
	o.emit(ctx, "mission.updated", mission.ID, mission)
	o.signal()
	return mission, nil
}

func (o *Operator) Workspaces(ctx context.Context) ([]domain.Workspace, error) {
	workspaces, err := o.store.ListWorkspaces(ctx)
	for index := range workspaces {
		workspaces[index] = o.workspaceView(workspaces[index])
	}
	if workspaces == nil {
		workspaces = []domain.Workspace{}
	}
	return workspaces, err
}

func (o *Operator) OperatorSettings(ctx context.Context) (api.OperatorSettings, error) {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return api.OperatorSettings{}, err
	}
	return api.OperatorSettings{CodexModel: settings.CodexModel, CodexReasoningEffort: settings.CodexReasoningEffort, MaxParallelTasks: settings.MaxParallelTasks}, nil
}

func (o *Operator) UpdateOperatorSettings(ctx context.Context, input api.OperatorSettings) (api.OperatorSettings, error) {
	input.CodexModel = strings.TrimSpace(input.CodexModel)
	input.CodexReasoningEffort = strings.TrimSpace(strings.ToLower(input.CodexReasoningEffort))
	if input.CodexModel != "" {
		if len(input.CodexModel) > 100 || !regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`).MatchString(input.CodexModel) {
			return api.OperatorSettings{}, fmt.Errorf("%w: Codex model contains unsupported characters", api.ErrInvalid)
		}
	}
	switch input.CodexReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return api.OperatorSettings{}, fmt.Errorf("%w: reasoning effort must be default, none, low, medium, high, xhigh, max, or ultra", api.ErrInvalid)
	}
	if input.MaxParallelTasks < 0 || input.MaxParallelTasks > maximumParallelTasks {
		return api.OperatorSettings{}, fmt.Errorf("%w: max parallel tasks must be between 1 and %d", api.ErrInvalid, maximumParallelTasks)
	}
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return api.OperatorSettings{}, err
	}
	if input.MaxParallelTasks == 0 {
		input.MaxParallelTasks = settings.MaxParallelTasks
	}
	settings.CodexModel, settings.CodexReasoningEffort, settings.MaxParallelTasks = input.CodexModel, input.CodexReasoningEffort, input.MaxParallelTasks
	if err := o.store.UpdateSettings(ctx, settings); err != nil {
		return api.OperatorSettings{}, err
	}
	o.emit(ctx, "settings.updated", "codex", input)
	o.signal()
	return input, nil
}

func (o *Operator) CheckSetup(ctx context.Context, workspacePaths []string) (api.SetupChecks, error) {
	codex := checkBinary(ctx, "codex", "--version")
	if codex.Available {
		auth := checkBinary(ctx, codex.Path, "login", "status")
		if !auth.Available {
			codex.Available = false
			codex.Error = "Codex is installed but is not authenticated: " + auth.Error
		}
	}
	checks := api.SetupChecks{Codex: codex, Git: checkBinary(ctx, "git", "--version"), Workspaces: []api.CheckResult{}}
	for _, path := range workspacePaths {
		result := api.CheckResult{Path: strings.TrimSpace(path)}
		if _, err := validateWorkspaces([]string{path}); err != nil {
			result.Error = err.Error()
		} else {
			result.Available = true
		}
		checks.Workspaces = append(checks.Workspaces, result)
	}
	return checks, nil
}

func (o *Operator) BrowseWorkspace(ctx context.Context) (string, error) {
	browseCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(browseCtx, "osascript", "-e", `POSIX path of (choose folder with prompt "Choose a workspace for Nabu")`)
	case "linux":
		if path, err := exec.LookPath("zenity"); err == nil {
			command = exec.CommandContext(browseCtx, path, "--file-selection", "--directory", "--title=Choose a workspace for Nabu")
		} else if path, err := exec.LookPath("kdialog"); err == nil {
			command = exec.CommandContext(browseCtx, path, "--getexistingdirectory", ".", "--title", "Choose a workspace for Nabu")
		} else {
			return "", fmt.Errorf("%w: install zenity or kdialog, or enter an absolute path", api.ErrInvalid)
		}
	default:
		return "", fmt.Errorf("%w: native folder browsing is not supported on %s", api.ErrInvalid, runtime.GOOS)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: folder selection was cancelled or unavailable", api.ErrInvalid)
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("%w: no folder was selected", api.ErrInvalid)
	}
	validated, err := validateWorkspaces([]string{path})
	if err != nil {
		return "", fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	return validated[0].Path, nil
}

func (o *Operator) CompleteSetup(ctx context.Context, input api.SetupRequest) (domain.StatusSnapshot, error) {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	if settings.SetupComplete {
		return domain.StatusSnapshot{}, fmt.Errorf("%w: setup is already complete", api.ErrConflict)
	}
	checks, err := o.CheckSetup(ctx, input.Workspaces)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	if !checks.Codex.Available || !checks.Git.Available {
		return domain.StatusSnapshot{}, fmt.Errorf("%w: Codex and Git must be available before setup can finish", api.ErrInvalid)
	}
	workspaces, err := validateWorkspaces(input.Workspaces)
	if err != nil {
		return domain.StatusSnapshot{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	policy := normalizePolicy(input.Policy)
	existingWorkspaces, err := o.store.ListWorkspaces(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	existingPaths := make(map[string]domain.Workspace, len(existingWorkspaces))
	for _, workspace := range existingWorkspaces {
		existingPaths[workspace.Path] = workspace
	}
	var activeWorkspace domain.Workspace
	createdWorkspaces := make([]domain.Workspace, 0, len(workspaces))
	for _, workspace := range workspaces {
		if existing, exists := existingPaths[workspace.Path]; exists {
			createdWorkspaces = append(createdWorkspaces, existing)
			if activeWorkspace.ID == "" {
				activeWorkspace = existing
			}
			continue
		}
		created, createErr := o.store.CreateWorkspace(ctx, workspace)
		if createErr != nil {
			return domain.StatusSnapshot{}, createErr
		}
		createdWorkspaces = append(createdWorkspaces, created)
		if activeWorkspace.ID == "" {
			activeWorkspace = created
		}
	}
	if activeWorkspace.ID == "" {
		return domain.StatusSnapshot{}, fmt.Errorf("%w: at least one approved workspace is required", api.ErrInvalid)
	}
	if err := o.store.SetActiveWorkspace(ctx, activeWorkspace.ID); err != nil {
		return domain.StatusSnapshot{}, err
	}
	missionStatement := redactSecrets(strings.TrimSpace(input.Mission))
	businessContext := redactSecrets(strings.TrimSpace(input.Context))
	var mission domain.Mission
	for _, workspace := range createdWorkspaces {
		scope, scopeErr := config.EnsureScope(o.paths, workspace.ID)
		if scopeErr != nil {
			return domain.StatusSnapshot{}, scopeErr
		}
		workspaceMission, missionErr := o.store.GetMissionForWorkspace(ctx, workspace.ID)
		if errors.Is(missionErr, store.ErrNotFound) {
			workspaceMission, missionErr = o.store.CreateMission(ctx, domain.Mission{
				WorkspaceID: workspace.ID, Statement: missionStatement, Context: businessContext, Active: true,
			})
		}
		if missionErr != nil {
			return domain.StatusSnapshot{}, missionErr
		}
		if workspace.ID == activeWorkspace.ID {
			mission = workspaceMission
		}
		if err := writeContextFile(scope.Mission, "Mission", workspaceMission.Statement, workspaceMission.Context); err != nil {
			return domain.StatusSnapshot{}, err
		}
		if err := writeContextFile(scope.Business, "Business", workspaceMission.Context, ""); err != nil {
			return domain.StatusSnapshot{}, err
		}
		if err := writePolicyFile(scope.Policy, policy); err != nil {
			return domain.StatusSnapshot{}, err
		}
		if err := o.store.UpdatePolicyForWorkspace(ctx, workspace.ID, policy); err != nil {
			return domain.StatusSnapshot{}, err
		}
	}
	if err := o.store.UpdatePolicy(ctx, policy); err != nil {
		return domain.StatusSnapshot{}, err
	}
	settings.DisplayName = strings.TrimSpace(input.DisplayName)
	settings.SetupComplete = true
	settings.CodexPath = checks.Codex.Path
	settings.GitPath = checks.Git.Path
	settings.ActiveWorkspaceID = activeWorkspace.ID
	if err := o.store.UpdateSettings(ctx, settings); err != nil {
		return domain.StatusSnapshot{}, err
	}
	o.emit(ctx, "setup.completed", mission.ID, map[string]any{"workspaces": len(workspaces)})
	return o.Status(ctx)
}

func (o *Operator) StartMission(ctx context.Context) (domain.StatusSnapshot, error) {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	if !settings.SetupComplete {
		return domain.StatusSnapshot{}, fmt.Errorf("%w: setup must be completed first", api.ErrConflict)
	}
	settings.MissionStarted = true
	settings.Paused = false
	if err := o.store.UpdateSettings(ctx, settings); err != nil {
		return domain.StatusSnapshot{}, err
	}
	if workspace, workspaceErr := o.store.ActiveWorkspace(ctx); workspaceErr == nil {
		workspace.MissionStarted = true
		if workspaceErr = o.store.UpdateWorkspace(ctx, workspace); workspaceErr != nil {
			return domain.StatusSnapshot{}, workspaceErr
		}
		if !workspace.ContextReady {
			if workspaceErr = o.ensureContextPrompt(ctx, workspace); workspaceErr != nil {
				return domain.StatusSnapshot{}, workspaceErr
			}
		} else if _, workspaceErr = o.store.RequestOrientationForWorkspace(ctx, workspace.ID); workspaceErr != nil {
			return domain.StatusSnapshot{}, workspaceErr
		}
	}
	o.emit(ctx, "status.changed", "", map[string]string{"status": "idle", "reason": "mission_started"})
	o.signal()
	return o.Status(ctx)
}

func (o *Operator) SetPaused(ctx context.Context, paused bool) (domain.StatusSnapshot, error) {
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	settings.Paused = paused
	if err := o.store.UpdateSettings(ctx, settings); err != nil {
		return domain.StatusSnapshot{}, err
	}
	if paused {
		o.pauseActive(ctx)
	} else {
		waiting, listErr := o.store.ListTasks(ctx, store.TaskFilter{Statuses: []domain.TaskStatus{domain.TaskWaiting}})
		if listErr != nil {
			return domain.StatusSnapshot{}, listErr
		}
		for _, task := range waiting {
			if task.CurrentRunID == "" {
				continue
			}
			task.Status, task.CurrentRunID, task.CompletedAt = domain.TaskReady, "", nil
			task.UpdatedAt = time.Now().UTC()
			if updateErr := o.store.UpdateTask(ctx, task); updateErr != nil {
				return domain.StatusSnapshot{}, updateErr
			}
			o.emit(ctx, "task.updated", task.ID, task)
		}
		o.signal()
	}
	o.emit(ctx, "status.changed", "", map[string]bool{"paused": paused})
	return o.Status(ctx)
}

func (o *Operator) RequestOrientation(ctx context.Context, reason string) error {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return translateNotFound(err)
	}
	if !workspace.ContextReady {
		return fmt.Errorf("%w: finish workspace context setup before orienting work", api.ErrConflict)
	}
	queued, err := o.store.RequestOrientation(ctx)
	if err != nil {
		return err
	}
	if queued {
		o.emit(ctx, "orientation.queued", "", map[string]string{"reason": reason})
	}
	o.signal()
	return nil
}

func (o *Operator) Tasks(ctx context.Context) ([]domain.Task, error) {
	tasks, err := o.store.ListTasks(ctx, store.TaskFilter{})
	if tasks == nil {
		tasks = []domain.Task{}
	}
	return tasks, err
}

func (o *Operator) DraftTask(ctx context.Context, input api.TaskDraftRequest) (api.TaskDraft, error) {
	o.aiMu.Lock()
	defer o.aiMu.Unlock()
	settings, err := o.store.GetSettings(ctx)
	if err != nil {
		return api.TaskDraft{}, err
	}
	if !settings.SetupComplete || !settings.MissionStarted {
		return api.TaskDraft{}, fmt.Errorf("%w: start a mission before drafting tasks", api.ErrConflict)
	}
	if settings.Paused {
		return api.TaskDraft{}, fmt.Errorf("%w: resume Nabu before asking Codex to draft a task", api.ErrConflict)
	}
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return api.TaskDraft{}, translateNotFound(err)
	}
	if !workspace.ContextReady {
		return api.TaskDraft{}, fmt.Errorf("%w: finish workspace context setup in Chat before drafting tasks", api.ErrConflict)
	}
	if !o.codexReady(time.Now().UTC()) {
		_, reason, _ := o.codexHealth(binaryAvailable(settings.CodexPath, "codex"))
		return api.TaskDraft{}, fmt.Errorf("%w: %s", api.ErrUnavailable, reason)
	}
	mission, err := o.store.ActiveMission(ctx)
	if err != nil {
		return api.TaskDraft{}, translateNotFound(err)
	}
	tasks, err := o.store.ListTasks(ctx, store.TaskFilter{})
	if err != nil {
		return api.TaskDraft{}, err
	}
	scope, _ := o.activeScopePaths(ctx)
	memory, _ := readBounded(scope.MemoryFile, 128*1024)
	soul, _ := readBounded(o.paths.Soul, 64*1024)
	prompt, err := drafting.BuildPacket(drafting.Request{
		Intent: input.Request, Mission: redactMission(mission), Memory: redactSecrets(memory), Soul: redactSecrets(soul), Queue: tasks,
	})
	if err != nil {
		return api.TaskDraft{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	workingDirectory := o.paths.Workspace
	workspaces, _ := o.store.ListWorkspaces(ctx)
	for _, workspace := range workspaces {
		if workspace.Allowed && (settings.ActiveWorkspaceID == "" || workspace.ID == settings.ActiveWorkspaceID) {
			workingDirectory = workspace.Path
			break
		}
	}
	record, err := o.store.CreateRun(ctx, domain.Run{
		Type: domain.RunChat, Status: domain.RunRunning, WorkingDirectory: workingDirectory, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		return api.TaskDraft{}, err
	}
	runContext, cancel := context.WithCancel(ctx)
	o.setActive("", record.ID, cancel)
	defer func() {
		cancel()
		o.clearActive(record.ID)
	}()
	o.emit(ctx, "task.draft.started", record.ID, nil)
	codexArgs, mcpSecrets, err := o.codexRun(ctx, settings.ActiveWorkspaceID)
	if err != nil {
		return api.TaskDraft{}, err
	}
	execution, runErr := o.runner.Run(runContext, runner.Request{
		WorkingDirectory: workingDirectory, Prompt: prompt, Command: o.codexCommand(ctx), Args: codexArgs, SecretEnvironment: mcpSecrets,
		OnStart: func(started runner.ProcessStarted) {
			record.PID, record.Command, record.Attempt, record.StartedAt = started.PID, started.Command, started.Attempt, started.StartedAt
			_ = o.store.UpdateRun(context.Background(), record)
		},
	})
	o.recordCodexExecution(execution, runErr)
	ended := execution.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	record.PID, record.Command, record.Attempt = execution.PID, execution.Command, execution.Attempt
	record.EndedAt, record.ExitCode, record.Error = &ended, execution.ExitCode, execution.Error
	if runErr != nil {
		record.Status = execution.Status
		if record.Status == "" {
			record.Status = domain.RunFailed
		}
		_ = o.store.UpdateRun(ctx, record)
		o.emit(ctx, "task.draft.failed", record.ID, map[string]string{"error": record.Error})
		return api.TaskDraft{}, fmt.Errorf("%w: Codex could not draft this task; %s", api.ErrUnavailable, userFacingRunError(execution))
	}
	draft, err := drafting.Parse(execution.Stdout)
	if err != nil {
		record.Status, record.Error = domain.RunFailed, err.Error()
		_ = o.store.UpdateRun(ctx, record)
		return api.TaskDraft{}, fmt.Errorf("%w: Codex returned an invalid task draft", api.ErrUnavailable)
	}
	if validPriority(input.Priority) {
		draft.Priority = input.Priority
	}
	record.Status = domain.RunCompleted
	result := domain.RunResult{Status: "completed", Summary: "Drafted a task for review.", FilesChanged: []string{}, Verification: []domain.Verification{}, Artifacts: []domain.Artifact{}, Uncertainties: []string{}}
	record.Result = &result
	if err := o.store.UpdateRun(ctx, record); err != nil {
		return api.TaskDraft{}, err
	}
	value := api.TaskDraft{Title: draft.Title, Purpose: draft.Purpose, Why: draft.Why, Priority: draft.Priority, DefinitionOfDone: draft.DefinitionOfDone}
	o.emit(ctx, "task.draft.completed", record.ID, value)
	return value, nil
}

func (o *Operator) CreateTask(ctx context.Context, input api.TaskCreate) (domain.Task, error) {
	o.queueMu.Lock()
	defer o.queueMu.Unlock()
	priority := input.Priority
	if !validPriority(priority) {
		priority = domain.PriorityNormal
	}
	workspace, err := o.store.ActiveWorkspace(ctx)
	if input.WorkspaceID != "" {
		workspace, err = o.store.GetWorkspace(ctx, input.WorkspaceID)
	}
	if err != nil || !workspace.Allowed {
		return domain.Task{}, fmt.Errorf("%w: workspace is not approved", api.ErrInvalid)
	}
	if !workspace.ContextReady {
		return domain.Task{}, fmt.Errorf("%w: finish workspace context setup in Chat before creating tasks", api.ErrConflict)
	}
	ready, err := o.store.ReadyTaskCount(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	status := domain.TaskReady
	if ready >= maxReadyTasks || (input.PlannedAt != nil && input.PlannedAt.After(time.Now().UTC())) {
		status = domain.TaskIdea
	}
	task, err := o.store.CreateTask(ctx, domain.Task{
		Title: redactSecrets(strings.TrimSpace(input.Title)), Purpose: redactSecrets(strings.TrimSpace(input.Purpose)), Why: redactSecrets(strings.TrimSpace(input.Why)),
		Status: status, Priority: priority, DefinitionOfDone: definitions(input.DefinitionOfDone),
		DependsOnTaskIDs: append([]string(nil), input.DependsOnTaskIDs...), WorkspaceID: input.WorkspaceID, CreatedBy: "user", PlannedAt: input.PlannedAt,
	})
	if err != nil {
		return domain.Task{}, err
	}
	o.emit(ctx, "task.created", task.ID, task)
	o.signal()
	return task, nil
}

func (o *Operator) Task(ctx context.Context, id string) (domain.Task, error) {
	task, err := o.store.GetTask(ctx, id)
	if err != nil {
		return domain.Task{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, task.WorkspaceID); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func (o *Operator) UpdateTask(ctx context.Context, id string, input api.TaskUpdate) (domain.Task, error) {
	task, err := o.store.GetTask(ctx, id)
	if err != nil {
		return domain.Task{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, task.WorkspaceID); err != nil {
		return domain.Task{}, err
	}
	if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
		task.Title = strings.TrimSpace(*input.Title)
	}
	if input.Purpose != nil {
		task.Purpose = strings.TrimSpace(*input.Purpose)
	}
	if input.Why != nil {
		task.Why = strings.TrimSpace(*input.Why)
	}
	if input.Priority != nil {
		if !validPriority(*input.Priority) {
			return domain.Task{}, fmt.Errorf("%w: priority must be high, normal, or low", api.ErrInvalid)
		}
		task.Priority = *input.Priority
	}
	if input.DefinitionOfDone != nil {
		if len(*input.DefinitionOfDone) == 0 {
			return domain.Task{}, fmt.Errorf("%w: definition of done cannot be empty", api.ErrInvalid)
		}
		task.DefinitionOfDone = definitions(*input.DefinitionOfDone)
	}
	if input.DependsOnTaskIDs != nil {
		task.DependsOnTaskIDs = append([]string(nil), (*input.DependsOnTaskIDs)...)
	}
	if input.PlannedAt != nil {
		plannedAt := input.PlannedAt.UTC()
		task.PlannedAt = &plannedAt
		if plannedAt.After(time.Now().UTC()) && task.Status == domain.TaskReady {
			task.Status = domain.TaskIdea
		}
	}
	cancelAfterUpdate := false
	if input.Status != nil && *input.Status != task.Status {
		if !allowedUserTransition(task.Status, *input.Status) {
			return domain.Task{}, fmt.Errorf("%w: task cannot move from %s to %s", api.ErrConflict, task.Status, *input.Status)
		}
		task.Status = *input.Status
		if task.Status == domain.TaskCancelled {
			cancelAfterUpdate = true
			now := time.Now().UTC()
			task.CompletedAt = &now
		} else if task.Status == domain.TaskReady {
			task.CurrentRunID, task.Result, task.StartedAt, task.CompletedAt = "", nil, nil, nil
		}
	}
	task.UpdatedAt = time.Now().UTC()
	if err := o.store.UpdateTask(ctx, task); err != nil {
		return domain.Task{}, err
	}
	if cancelAfterUpdate {
		o.cancelTask(task.ID)
	}
	o.emit(ctx, "task.updated", task.ID, task)
	if (task.Status == domain.TaskCancelled || task.Status == domain.TaskIdea) && o.readyQueueEmpty(ctx) {
		_, _ = o.store.RequestOrientation(ctx)
	}
	o.signal()
	return o.store.GetTask(ctx, task.ID)
}

func (o *Operator) RunTask(ctx context.Context, id string) (domain.Task, error) {
	task, err := o.store.GetTask(ctx, id)
	if err != nil {
		return domain.Task{}, translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, task.WorkspaceID); err != nil {
		return domain.Task{}, err
	}
	task, err = o.store.RequestTaskRunForWorkspace(ctx, task.WorkspaceID, task.ID)
	if errors.Is(err, store.ErrInvalidTransition) {
		return domain.Task{}, fmt.Errorf("%w: only idea or ready tasks can be run now", api.ErrConflict)
	}
	if err != nil {
		return domain.Task{}, translateNotFound(err)
	}
	o.emit(ctx, "task.run_requested", task.ID, task)
	o.signal()
	return task, nil
}

// RecoverTask starts a durable, task-scoped steering turn instead of blindly
// rerunning a failure. The visible recovery envelope gives Nabu the exact task
// and latest evidence while keeping secrets out of Chat and preserving the
// owner's optional note as untrusted input.
func (o *Operator) RecoverTask(ctx context.Context, id string, input api.TaskRecovery) (domain.Message, error) {
	task, err := o.Task(ctx, id)
	if err != nil {
		return domain.Message{}, err
	}
	if task.Status != domain.TaskFailed && task.Status != domain.TaskWaiting {
		return domain.Message{}, fmt.Errorf("%w: only failed or waiting tasks can be continued with Nabu", api.ErrConflict)
	}
	note := strings.TrimSpace(input.Note)
	if containsLikelySecret(note) {
		return domain.Message{}, fmt.Errorf("%w: for security, enter credentials in Settings > Secrets; secret values cannot be sent through Chat", api.ErrInvalid)
	}
	if len([]rune(note)) > maximumChatMessageRunes/2 {
		return domain.Message{}, fmt.Errorf("%w: recovery note is too long", api.ErrInvalid)
	}
	content := buildTaskRecoveryMessage(note)
	return o.SendChat(ctx, api.ChatSend{
		Content: content, RecoveryTaskID: task.ID, RecoveryTaskTitle: task.Title,
	})
}

func buildTaskRecoveryMessage(note string) string {
	if note == "" {
		note = "Diagnose the recorded blocker and help me take the smallest useful next step."
	}
	return redactSecrets(note)
}

func (o *Operator) DeleteTask(ctx context.Context, id string) error {
	task, err := o.store.GetTask(ctx, id)
	if err != nil {
		return translateNotFound(err)
	}
	if err := o.requireActiveWorkspace(ctx, task.WorkspaceID); err != nil {
		return err
	}
	if task.Status == domain.TaskRunning {
		return fmt.Errorf("%w: cancel the running task before deleting it", api.ErrConflict)
	}
	if err := o.store.DeleteTask(ctx, task.ID); err != nil {
		return translateNotFound(err)
	}
	o.emitForWorkspace(ctx, task.WorkspaceID, "task.deleted", task.ID, map[string]string{"title": task.Title})
	return nil
}

func (o *Operator) Run(ctx context.Context, id string) (domain.Run, error) {
	run, err := o.store.GetRun(ctx, id)
	if err != nil {
		return domain.Run{}, translateNotFound(err)
	}
	if run.StdoutPath != "" {
		run.Stdout, _ = readBounded(run.StdoutPath, 2*1024*1024)
	}
	if run.StderrPath != "" {
		run.Stderr, _ = readBounded(run.StderrPath, 2*1024*1024)
	}
	if events, eventErr := o.store.RecentEvents(ctx, 500); eventErr == nil {
		run.Events = runActivity(run, events)
	}
	return run, nil
}

func (o *Operator) Subscribe() (<-chan domain.Event, func()) { return o.bus.Subscribe() }

func (o *Operator) RecentEvents(ctx context.Context, afterID int64, limit int) ([]domain.Event, error) {
	return o.store.ListEvents(ctx, afterID, limit)
}

func (o *Operator) runTask(parent context.Context, task domain.Task) error {
	workspace := domain.Workspace{ID: "nabu", Name: "Nabu workspace", Path: o.paths.Workspace, Allowed: true}
	if task.Workspace != nil {
		if !task.Workspace.Allowed {
			_ = o.store.UpdateTaskStatus(parent, task.ID, domain.TaskWaiting, "", time.Now())
			o.emit(parent, "task.updated", task.ID, map[string]string{"status": "waiting", "reason": "approved workspace required"})
			return nil
		}
		workspace = *task.Workspace
	}
	mission, err := o.store.GetMissionForWorkspace(parent, workspace.ID)
	if err != nil {
		return err
	}
	policy, err := o.store.GetPolicyForWorkspace(parent, workspace.ID)
	if err != nil {
		return err
	}
	scope, _ := config.EnsureScope(o.paths, workspace.ID)
	memory, _ := readBounded(scope.MemoryFile, 128*1024)
	soul, _ := readBounded(o.paths.Soul, 64*1024)
	relevantContext := redactSecrets(memory)
	if continuation, continuationErr := o.approvalContinuation(parent, task, mission, policy); continuationErr != nil {
		return o.failClaimedTask(parent, task, continuationErr)
	} else if continuation != "" {
		relevantContext += "\n\n" + continuation
	}
	scriptPlan := o.taskScriptContext(parent, task, workspace)
	datasets, err := o.store.ListDatasets(parent, store.DatasetFilter{WorkspaceID: workspace.ID, Limit: 24})
	if err != nil {
		return o.failClaimedTask(parent, task, err)
	}
	localApps, err := o.store.ListLocalApps(parent, store.LocalAppFilter{WorkspaceID: workspace.ID, Limit: 24})
	if err != nil {
		return o.failClaimedTask(parent, task, err)
	}
	prompt, err := runner.BuildTaskPacket(runner.TaskPacketRequest{
		Task: task, Mission: redactMission(mission), Workspace: workspace, Policy: policy,
		RelevantContext: relevantContext, ScriptData: scriptPlan.Data, CharacterCharter: redactSecrets(soul),
		Datasets: datasets, LocalApps: localApps,
		Scripts: scriptPlan.Inventory, BrowserQARequired: scriptPlan.BrowserQARequired,
		BrowserMCP: scriptPlan.BrowserMCPName, BrowserVerifier: scriptPlan.BrowserVerifierName,
		RequiredEvidence: []string{"command exit codes", "tests or checks performed", "files changed", "remaining uncertainty"},
	})
	if err != nil {
		return o.failClaimedTask(parent, task, err)
	}
	runRecord, err := o.store.CreateRun(parent, domain.Run{
		TaskID: task.ID, Type: domain.RunExecute, Status: domain.RunRunning,
		WorkingDirectory: workspace.Path, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		return o.failClaimedTask(parent, task, err)
	}
	if err := o.store.UpdateTaskStatus(parent, task.ID, domain.TaskRunning, runRecord.ID, time.Now()); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	o.setActive(task.ID, runRecord.ID, cancel)
	defer o.clearActive(runRecord.ID)
	o.emit(parent, "task.started", task.ID, map[string]string{"run_id": runRecord.ID})

	codexArgs, mcpSecrets, err := o.codexRunWithBrowser(parent, workspace.ID, workspace.Path, scriptPlan.BrowserMCPName == builtInBrowserMCPName)
	if err != nil {
		return o.failClaimedTask(parent, task, err)
	}
	execution, runErr := o.runner.Run(ctx, runner.Request{
		WorkingDirectory:  workspace.Path,
		Prompt:            prompt,
		Command:           o.codexCommand(parent),
		Args:              codexArgs,
		SecretEnvironment: mcpSecrets,
		OnStart: func(started runner.ProcessStarted) {
			active, getErr := o.store.GetRun(parent, runRecord.ID)
			if getErr != nil {
				return
			}
			active.PID, active.Command, active.Attempt, active.StartedAt = started.PID, started.Command, started.Attempt, started.StartedAt
			_ = o.store.UpdateRun(parent, active)
			o.emit(parent, "run.started", runRecord.ID, started)
		},
		OnOutput: func(output runner.OutputEvent) {
			o.publishTransient("run.output", runRecord.ID, map[string]any{
				"run_id": runRecord.ID, "task_id": task.ID, "attempt": output.Attempt,
				"stream": output.Stream, "data": redactSecrets(output.Data), "at": output.At,
			})
		},
	})
	o.recordCodexExecution(execution, runErr)
	if runErr == nil {
		execution = o.applyTaskBrowserVerification(parent, task, execution, scriptPlan)
	}
	return o.finishTaskRun(parent, task, runRecord, execution, runErr)
}

func (o *Operator) finishTaskRun(ctx context.Context, task domain.Task, record domain.Run, execution runner.ExecutionResult, runErr error) error {
	ended := execution.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	record.PID = execution.PID
	record.Command = execution.Command
	record.Attempt = execution.Attempt
	record.EndedAt = &ended
	record.ExitCode = execution.ExitCode
	record.RawOutput = ""
	record.Error = execution.Error
	runDir := filepath.Join(o.paths.Runs, record.ID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		o.logger.Error("create run output directory", "run_id", record.ID, "error", err)
	} else {
		record.StdoutPath = filepath.Join(runDir, "stdout.log")
		record.StderrPath = filepath.Join(runDir, "stderr.log")
		if err := os.WriteFile(record.StdoutPath, []byte(redactSecrets(execution.Stdout)), 0o600); err != nil {
			o.logger.Error("persist run stdout", "run_id", record.ID, "error", err)
			record.StdoutPath = ""
		}
		if err := os.WriteFile(record.StderrPath, []byte(redactSecrets(execution.Stderr)), 0o600); err != nil {
			o.logger.Error("persist run stderr", "run_id", record.ID, "error", err)
			record.StderrPath = ""
		}
	}
	if runErr != nil {
		record.Status = execution.Status
		if record.Status == "" {
			record.Status = domain.RunFailed
		}
		_ = o.store.UpdateRun(ctx, record)
		current, getErr := o.store.GetTask(ctx, task.ID)
		if getErr == nil && current.CurrentRunID != record.ID {
			o.emit(ctx, "run.completed", record.ID, map[string]any{"task_id": task.ID, "status": record.Status, "superseded": true})
			return nil
		}
		if getErr == nil && current.Status != domain.TaskCancelled && current.Status != domain.TaskWaiting {
			_ = o.store.UpdateTaskStatus(ctx, task.ID, domain.TaskFailed, record.ID, ended)
		}
		if getErr == nil && current.Status == domain.TaskCancelled {
			o.emit(ctx, "task.cancelled", task.ID, map[string]any{"run_id": record.ID})
			_ = o.RequestOrientation(ctx, "task_cancelled")
		} else if getErr == nil && current.Status == domain.TaskWaiting && execution.Status == domain.RunCancelled {
			o.emit(ctx, "task.updated", task.ID, map[string]any{"run_id": record.ID, "status": domain.TaskWaiting, "reason": "operator_paused"})
		} else {
			o.emit(ctx, "task.failed", task.ID, map[string]any{"run_id": record.ID, "error": record.Error})
			o.appendTaskLifecycleMessage(ctx, task, record.ID, "task.failed", nil, actionableTaskFailure(record.Error, execution))
			_ = o.RequestOrientation(ctx, "task_failed")
		}
		return nil
	}
	result, err := runner.ParseRunResult(execution.Stdout)
	if err != nil {
		record.Status = domain.RunFailed
		record.Error = err.Error()
		_ = o.store.UpdateRun(ctx, record)
		_ = o.store.UpdateTaskStatus(ctx, task.ID, domain.TaskFailed, record.ID, ended)
		o.emit(ctx, "task.failed", task.ID, map[string]any{"run_id": record.ID, "error": err.Error()})
		o.appendTaskLifecycleMessage(ctx, task, record.ID, "task.failed", nil, "Codex returned an invalid structured result. Review the run output and retry the task.")
		_ = o.RequestOrientation(ctx, "invalid_run_result")
		return nil
	}
	result = redactRunResult(result)
	record.Result = &result
	record.Status = domain.RunCompleted
	for index := range result.Artifacts {
		result.Artifacts[index].TaskID = task.ID
		result.Artifacts[index].RunID = record.ID
		created, artifactErr := o.store.CreateArtifact(ctx, result.Artifacts[index])
		if artifactErr == nil {
			result.Artifacts[index] = created
			o.emitForWorkspace(ctx, task.WorkspaceID, "output.created", created.ID, map[string]any{
				"artifact_id": created.ID, "task_id": task.ID, "run_id": record.ID,
			})
		}
	}
	if task.Workspace != nil {
		if artifact, ok := o.captureGitDiff(ctx, task, record); ok {
			result.Artifacts = append(result.Artifacts, artifact)
		}
	}
	status := domain.TaskCompleted
	if result.Status == "needs_approval" || result.ApprovalNeeded != nil {
		status = domain.TaskNeedsApproval
	} else if result.Status == "failed" {
		status = domain.TaskFailed
	} else if result.Status == "cancelled" {
		status = domain.TaskCancelled
	} else if !verificationPassed(result.Verification) {
		status = domain.TaskFailed
		result.Status = "failed"
		result.Uncertainties = append(result.Uncertainties, "Task completion was rejected because verification evidence was missing or did not pass.")
	}
	if status == domain.TaskCompleted && taskRequiresDatasetRows(task) && !resultIncludesDatasetRows(result) {
		status = domain.TaskFailed
		result.Status = "failed"
		result.Uncertainties = append(result.Uncertainties, "Task completion was rejected because its definition requires durable dataset rows, but the final result omitted an upsert_rows dataset_writes operation.")
	}
	if status == domain.TaskCompleted {
		if err := o.applyTaskDatasetWrites(ctx, task, record.ID, &result); err != nil {
			clearDatasetWriteRows(result.DatasetWrites)
			record.Status = domain.RunFailed
			record.Error = err.Error()
			result.Status = "failed"
			result.Uncertainties = append(result.Uncertainties, "Dataset writes were rejected and the task was not accepted as complete: "+err.Error())
			record.Result = &result
			_ = o.store.UpdateRun(ctx, record)
			_ = o.store.UpdateTaskStatus(ctx, task.ID, domain.TaskFailed, record.ID, ended)
			o.emitForWorkspace(ctx, task.WorkspaceID, "task.dataset_write_failed", task.ID, map[string]any{"run_id": record.ID, "error": err.Error()})
			o.appendTaskLifecycleMessage(ctx, task, record.ID, "task.failed", &result, err.Error())
			_ = o.RequestOrientation(ctx, "invalid_dataset_write")
			return nil
		}
		if err := o.applyTaskLocalApps(ctx, task, &result); err != nil {
			record.Status = domain.RunFailed
			record.Error = err.Error()
			result.Status = "failed"
			result.Uncertainties = append(result.Uncertainties, "Local app registration was rejected and the task was not accepted as complete: "+err.Error())
			record.Result = &result
			_ = o.store.UpdateRun(ctx, record)
			_ = o.store.UpdateTaskStatus(ctx, task.ID, domain.TaskFailed, record.ID, ended)
			o.emitForWorkspace(ctx, task.WorkspaceID, "task.app_registration_failed", task.ID, map[string]any{"run_id": record.ID, "error": err.Error()})
			o.appendTaskLifecycleMessage(ctx, task, record.ID, "task.failed", &result, err.Error())
			_ = o.RequestOrientation(ctx, "invalid_local_app")
			return nil
		}
	} else {
		clearDatasetWriteRows(result.DatasetWrites)
	}
	record.Result = &result
	if err := o.store.UpdateRun(ctx, record); err != nil {
		return err
	}
	approvalID := ""
	if status == domain.TaskNeedsApproval {
		action := "Approval is required before the task can continue."
		if result.ApprovalNeeded != nil && strings.TrimSpace(*result.ApprovalNeeded) != "" {
			action = strings.TrimSpace(*result.ApprovalNeeded)
		}
		evidence := make([]string, 0, len(result.Verification)+len(result.Artifacts))
		for _, check := range result.Verification {
			evidence = append(evidence, fmt.Sprintf("%s: %s — %s", check.Name, check.Status, check.Details))
		}
		for _, artifact := range result.Artifacts {
			evidence = append(evidence, fmt.Sprintf("%s: %s", artifact.Kind, artifact.Name))
		}
		metadata, _ := json.Marshal(map[string]any{"verification": result.Verification, "artifacts": result.Artifacts})
		approval, approvalErr := o.store.CreateApproval(ctx, domain.Approval{
			TaskID: task.ID, RunID: record.ID, ProposedAction: action,
			Why:            "The requested action is outside the current automatic policy boundary.",
			ProposedChange: result.Summary, Evidence: strings.Join(evidence, "\n"), EvidenceMetadata: metadata,
		})
		if approvalErr != nil {
			return approvalErr
		}
		approvalID = approval.ID
	} else if err := o.store.UpdateTaskStatus(ctx, task.ID, status, record.ID, ended); err != nil {
		return err
	}
	current, err := o.store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	current.Result = &result
	applyDefinitionOutcomes(current.DefinitionOfDone, result.DefinitionDone, status)
	if err := o.store.UpdateTask(ctx, current); err != nil {
		return err
	}
	if status == domain.TaskCompleted {
		if _, err := o.promoteTaskReports(ctx, current, result.Artifacts); err != nil {
			o.logger.Error("promote task report", "task_id", task.ID, "run_id", record.ID, "error", err)
			o.emitForWorkspace(ctx, task.WorkspaceID, "report.promotion_failed", task.ID, map[string]any{
				"task_id": task.ID, "run_id": record.ID,
			})
		}
	}
	eventType := "task.completed"
	if status == domain.TaskNeedsApproval {
		eventType = "approval.created"
	} else if status == domain.TaskFailed {
		eventType = "task.failed"
	}
	entityID := task.ID
	if approvalID != "" {
		entityID = approvalID
	}
	o.emit(ctx, eventType, entityID, map[string]any{"task_id": task.ID, "run_id": record.ID, "approval_id": approvalID, "result": result})
	o.emit(ctx, "run.completed", record.ID, map[string]any{"task_id": task.ID, "status": record.Status, "result_status": result.Status})
	lifecycleType := eventType
	if status == domain.TaskNeedsApproval {
		lifecycleType = "task.needs_approval"
	}
	o.appendTaskLifecycleMessage(ctx, current, record.ID, lifecycleType, &result, "")
	_ = o.RequestOrientation(ctx, "task_finished")
	return nil
}

func applyDefinitionOutcomes(items []domain.DefinitionItem, outcomes []domain.DefinitionOutcome, status domain.TaskStatus) {
	byText := make(map[string]domain.DefinitionOutcome, len(outcomes))
	for _, outcome := range outcomes {
		byText[strings.ToLower(strings.Join(strings.Fields(outcome.Text), " "))] = outcome
	}
	for index := range items {
		item := &items[index]
		item.Failed = false
		item.Details = ""
		if status == domain.TaskCompleted {
			item.Completed = true
			continue
		}
		outcome, found := byText[strings.ToLower(strings.Join(strings.Fields(item.Text), " "))]
		if !found && index < len(outcomes) {
			outcome, found = outcomes[index], true
		}
		if found {
			item.Details = outcome.Details
			switch outcome.Status {
			case "passed":
				item.Completed = true
			case "failed":
				item.Completed = false
				item.Failed = true
			}
		}
		if status == domain.TaskFailed && !item.Completed {
			item.Failed = true
		}
	}
}

func (o *Operator) runOrientation(parent context.Context) error {
	return o.runOrientationMode(parent, false)
}

func (o *Operator) runIdleOrientation(parent context.Context) error {
	workspace, err := o.store.ActiveWorkspace(parent)
	if err != nil {
		return err
	}
	err = o.runOrientationMode(parent, true)
	if err != nil {
		now := time.Now().UTC()
		_ = o.store.CompleteIdleSteward(context.WithoutCancel(parent), workspace.ID, now, now.Add(idleFailureCooldown))
	}
	return err
}

func (o *Operator) runOrientationMode(parent context.Context, idleSteward bool) error {
	workspace, err := o.store.ActiveWorkspace(parent)
	if err != nil {
		return err
	}
	if !workspace.ContextReady {
		return fmt.Errorf("%w: workspace context is not ready", api.ErrConflict)
	}
	mission, err := o.store.ActiveMission(parent)
	if err != nil {
		return err
	}
	all, err := o.store.ListTasks(parent, store.TaskFilter{})
	if err != nil {
		return err
	}
	queue := filterTasks(all, domain.TaskIdea, domain.TaskReady, domain.TaskWaiting, domain.TaskNeedsApproval)
	completed := filterLimit(all, 10, domain.TaskCompleted)
	failed := filterLimit(all, 10, domain.TaskFailed)
	events, err := o.store.RecentEvents(parent, 30)
	if err != nil {
		return err
	}
	workspaces, err := o.store.ListWorkspaces(parent)
	if err != nil {
		return err
	}
	scope, _ := o.activeScopePaths(parent)
	business, _ := readBounded(scope.Business, 128*1024)
	memory, _ := readBounded(scope.MemoryFile, 128*1024)
	soul, _ := readBounded(o.paths.Soul, 64*1024)
	var stewardshipState json.RawMessage
	if idleSteward {
		stewardshipState, err = o.idleStewardContext(parent, workspace, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	prompt, err := runner.GenerateOrientationPacket(runner.OrientationPacketRequest{
		Mission: redactMission(mission), BusinessContext: redactSecrets(business), DurableMemory: redactSecrets(memory), RecentCompleted: completed,
		CharacterCharter: redactSecrets(soul), RecentFailures: failed, CurrentQueue: queue, RecentEvents: events, Workspaces: workspaces,
		IdleSteward: idleSteward, StewardshipState: stewardshipState,
	})
	if err != nil {
		return err
	}
	record, err := o.store.CreateRun(parent, domain.Run{
		Type: domain.RunOrient, Status: domain.RunRunning, WorkingDirectory: workspace.Path, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	o.setActive("", record.ID, cancel)
	defer o.clearActive(record.ID)
	o.emit(parent, "orientation.started", record.ID, map[string]any{"source": map[bool]string{true: "idle_steward", false: "requested"}[idleSteward]})
	codexArgs, mcpSecrets, err := o.codexRun(parent, workspace.ID)
	if err != nil {
		return err
	}
	execution, runErr := o.runner.Run(ctx, runner.Request{
		WorkingDirectory: workspace.Path, Prompt: prompt, Command: o.codexCommand(parent), Args: codexArgs, SecretEnvironment: mcpSecrets,
		OnStart: func(started runner.ProcessStarted) {
			active, getErr := o.store.GetRun(parent, record.ID)
			if getErr != nil {
				return
			}
			active.PID, active.Command, active.Attempt, active.StartedAt = started.PID, started.Command, started.Attempt, started.StartedAt
			_ = o.store.UpdateRun(parent, active)
			o.emit(parent, "run.started", record.ID, started)
		},
		OnOutput: func(output runner.OutputEvent) {
			o.publishTransient("run.output", record.ID, map[string]any{"run_id": record.ID, "stream": output.Stream, "data": redactSecrets(output.Data), "at": output.At})
		},
	})
	o.recordCodexExecution(execution, runErr)
	return o.finishOrientation(parent, record, execution, runErr, queue, workspaces, workspace.ID, idleSteward)
}

func (o *Operator) finishOrientation(ctx context.Context, record domain.Run, execution runner.ExecutionResult, runErr error, queue []domain.Task, workspaces []domain.Workspace, workspaceID string, idleSteward bool) error {
	ended := execution.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	record.PID, record.Command, record.Attempt = execution.PID, execution.Command, execution.Attempt
	record.EndedAt, record.ExitCode, record.RawOutput, record.Error = &ended, execution.ExitCode, "", execution.Error
	runDir := filepath.Join(o.paths.Runs, record.ID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		o.logger.Error("create orientation output directory", "run_id", record.ID, "error", err)
	} else {
		record.StdoutPath, record.StderrPath = filepath.Join(runDir, "stdout.log"), filepath.Join(runDir, "stderr.log")
		if err := os.WriteFile(record.StdoutPath, []byte(redactSecrets(execution.Stdout)), 0o600); err != nil {
			o.logger.Error("persist orientation stdout", "run_id", record.ID, "error", err)
			record.StdoutPath = ""
		}
		if err := os.WriteFile(record.StderrPath, []byte(redactSecrets(execution.Stderr)), 0o600); err != nil {
			o.logger.Error("persist orientation stderr", "run_id", record.ID, "error", err)
			record.StderrPath = ""
		}
	}
	if runErr != nil {
		record.Status = execution.Status
		if record.Status == "" {
			record.Status = domain.RunFailed
		}
		_ = o.store.UpdateRun(ctx, record)
		o.emit(ctx, "orientation.failed", record.ID, map[string]string{"error": record.Error})
		now := time.Now().UTC()
		_ = o.store.CompleteIdleSteward(context.WithoutCancel(ctx), workspaceID, now, now.Add(idleFailureCooldown))
		if !idleSteward {
			o.retryOrientationOnce(ctx)
		}
		return nil
	}
	result, err := runner.ParseOrientationResult(execution.Stdout, queue)
	if err != nil {
		record.Status, record.Error = domain.RunFailed, err.Error()
		_ = o.store.UpdateRun(ctx, record)
		o.emit(ctx, "orientation.failed", record.ID, map[string]string{"error": err.Error()})
		now := time.Now().UTC()
		_ = o.store.CompleteIdleSteward(context.WithoutCancel(ctx), workspaceID, now, now.Add(idleFailureCooldown))
		if !idleSteward {
			o.retryOrientationOnce(ctx)
		}
		return nil
	}
	allowed := make(map[string]domain.Workspace)
	for _, workspace := range workspaces {
		if workspace.Allowed {
			allowed[workspace.ID] = workspace
		}
	}
	for _, update := range result.PriorityUpdates {
		task, getErr := o.store.GetTask(ctx, update.TaskID)
		if getErr == nil {
			task.Priority = update.Priority
			_ = o.store.UpdateTask(ctx, task)
			o.emit(ctx, "task.updated", task.ID, task)
		}
	}
	o.queueMu.Lock()
	defer o.queueMu.Unlock()
	readyCount, _ := o.store.ReadyTaskCount(ctx)
	createdCount := 0
	for _, proposal := range result.Tasks {
		duplicate, checkErr := o.store.HasOpenTaskTitle(ctx, proposal.Title)
		if checkErr != nil || duplicate {
			continue
		}
		workspaceID := proposal.WorkspaceID
		if _, ok := allowed[workspaceID]; !ok {
			workspaceID = ""
			if len(allowed) == 1 {
				for id := range allowed {
					workspaceID = id
				}
			}
		}
		status := domain.TaskReady
		if readyCount >= maxReadyTasks {
			status = domain.TaskIdea
		} else {
			readyCount++
		}
		task, createErr := o.store.CreateTask(ctx, domain.Task{
			Title: proposal.Title, Purpose: proposal.Purpose, Why: proposal.Why, Priority: proposal.Priority,
			DefinitionOfDone: proposal.DefinitionOfDone, WorkspaceID: workspaceID, Status: status, CreatedBy: "orientation",
		})
		if createErr != nil {
			continue
		}
		createdCount++
		o.emit(ctx, "task.created", task.ID, task)
	}
	normalized := domain.RunResult{
		Status: "completed", Summary: result.Summary, FilesChanged: []string{}, Verification: []domain.Verification{},
		Artifacts: []domain.Artifact{}, Uncertainties: []string{},
	}
	record.Status, record.Result = domain.RunCompleted, &normalized
	if err := o.store.UpdateRun(ctx, record); err != nil {
		return err
	}
	if err := o.store.CompleteOrientation(ctx, ended); err != nil {
		return err
	}
	noWork := result.NoWorkNeeded || createdCount == 0
	if err := o.store.CompleteIdleSteward(ctx, workspaceID, ended, ended.Add(idleStewardCooldown(noWork))); err != nil {
		return err
	}
	o.emit(ctx, "orientation.completed", record.ID, map[string]any{"summary": result.Summary, "tasks_created": createdCount, "no_work_needed": noWork, "source": map[bool]string{true: "idle_steward", false: "requested"}[idleSteward]})
	if idleSteward {
		o.emit(ctx, "idle_steward.completed", record.ID, map[string]any{"summary": result.Summary, "tasks_created": createdCount, "no_work_needed": noWork, "next_review_at": ended.Add(idleStewardCooldown(noWork))})
	}
	o.emit(ctx, "run.completed", record.ID, map[string]any{"status": record.Status, "type": record.Type})
	o.orientationRetryAttempted = false
	o.signal()
	return nil
}

func (o *Operator) captureGitDiff(ctx context.Context, task domain.Task, run domain.Run) (domain.Artifact, bool) {
	command := exec.CommandContext(ctx, "git", "-C", task.Workspace.Path, "diff", "--no-ext-diff", "--binary", "HEAD")
	output, err := command.Output()
	if err != nil || len(output) == 0 {
		return domain.Artifact{}, false
	}
	path := filepath.Join(o.paths.Artifacts, run.ID+"-git.diff")
	if err := os.WriteFile(path, output, 0o600); err != nil {
		return domain.Artifact{}, false
	}
	artifact, err := o.store.CreateArtifact(ctx, domain.Artifact{
		TaskID: task.ID, RunID: run.ID, Kind: "git_diff", Name: "Git diff", Path: path,
	})
	return artifact, err == nil
}

func (o *Operator) failClaimedTask(ctx context.Context, task domain.Task, cause error) error {
	_ = o.store.UpdateTaskStatus(ctx, task.ID, domain.TaskFailed, "", time.Now())
	o.emit(ctx, "task.failed", task.ID, map[string]string{"error": cause.Error()})
	return cause
}

func (o *Operator) emit(ctx context.Context, eventType, entityID string, value any) {
	var data json.RawMessage
	if value != nil {
		data, _ = json.Marshal(value)
	}
	event, err := o.store.AppendEvent(ctx, domain.Event{Type: eventType, EntityID: entityID, Data: data})
	if err != nil {
		o.logger.Error("persist event", "type", eventType, "error", err)
		return
	}
	o.bus.Publish(event)
}

func (o *Operator) publishTransient(eventType, entityID string, value any) {
	data, _ := json.Marshal(value)
	o.bus.Publish(domain.Event{Type: eventType, EntityID: entityID, Data: data, CreatedAt: time.Now().UTC()})
}

func (o *Operator) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
	for range maximumParallelTasks {
		select {
		case o.taskWake <- struct{}{}:
		default:
			return
		}
	}
}

func (o *Operator) signalChat() {
	select {
	case o.chatWake <- struct{}{}:
	default:
	}
}

func (o *Operator) isActive() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.activeRuns) > 0
}

func (o *Operator) exclusiveActive() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, run := range o.activeRuns {
		if run.taskID == "" {
			return true
		}
	}
	return false
}

func (o *Operator) activeState() (taskID, runID string, active bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for candidateRunID, run := range o.activeRuns {
		if runID == "" || candidateRunID < runID {
			taskID, runID = run.taskID, candidateRunID
		}
	}
	return taskID, runID, runID != ""
}

func (o *Operator) setActive(taskID, runID string, cancel context.CancelFunc) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.activeRuns == nil {
		o.activeRuns = make(map[string]activeRun)
	}
	o.activeRuns[runID] = activeRun{taskID: taskID, cancel: cancel}
}

func (o *Operator) clearActive(runID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.activeRuns, runID)
}

func (o *Operator) cancelTask(taskID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, run := range o.activeRuns {
		if run.taskID == taskID && run.cancel != nil {
			run.cancel()
		}
	}
}

func (o *Operator) cancelActive() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, run := range o.activeRuns {
		if run.cancel != nil {
			run.cancel()
		}
	}
}

func (o *Operator) pauseActive(ctx context.Context) {
	o.mu.Lock()
	runs := make(map[string]activeRun, len(o.activeRuns))
	for runID, run := range o.activeRuns {
		runs[runID] = run
	}
	o.mu.Unlock()
	for runID, run := range runs {
		if run.taskID != "" {
			_ = o.store.UpdateTaskStatus(ctx, run.taskID, domain.TaskWaiting, runID, time.Now())
		} else {
			_, _ = o.store.RequestOrientation(ctx)
		}
		if run.cancel != nil {
			run.cancel()
		}
	}
}

func (o *Operator) retryOrientationOnce(ctx context.Context) {
	if o.orientationRetryAttempted {
		return
	}
	o.orientationRetryAttempted = true
	_, _ = o.store.RequestOrientation(ctx)
	o.signal()
}

func (o *Operator) readyQueueEmpty(ctx context.Context) bool {
	count, err := o.store.ReadyTaskCount(ctx)
	return err == nil && count == 0 && !o.isActive()
}

func (o *Operator) codexCommand(ctx context.Context) string {
	settings, err := o.store.GetSettings(ctx)
	if err == nil && settings.CodexPath != "" {
		return settings.CodexPath
	}
	return "codex"
}

func (o *Operator) codexArgs(ctx context.Context) []string {
	settings, err := o.store.GetSettings(ctx)
	if err != nil || (settings.CodexModel == "" && settings.CodexReasoningEffort == "") {
		return nil
	}
	args := append([]string(nil), runner.DefaultConfig().Args...)
	if settings.CodexModel != "" {
		args = append([]string{"--model", settings.CodexModel}, args...)
	}
	if settings.CodexReasoningEffort != "" {
		args = append([]string{"-c", fmt.Sprintf(`model_reasoning_effort=%q`, settings.CodexReasoningEffort)}, args...)
	}
	return args
}

func (o *Operator) activeScopePaths(ctx context.Context) (config.WorkspaceContextPaths, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return config.WorkspaceContextPaths{}, err
	}
	return config.EnsureScope(o.paths, workspace.ID)
}

func (o *Operator) recordCodexExecution(execution runner.ExecutionResult, runErr error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if runErr == nil {
		if o.codexRetryAt != nil && time.Now().UTC().Before(*o.codexRetryAt) {
			return
		}
		o.codexState, o.codexReason, o.codexRetryAt = "available", "", nil
		return
	}
	message := strings.ToLower(execution.Stderr + "\n" + execution.Stdout + "\n" + runErr.Error())
	now := time.Now().UTC()
	state, reason, delay := "unavailable", "Codex is temporarily unavailable.", 2*time.Minute
	if containsAny(message, "rate limit", "too many requests", "quota exceeded", "429") {
		state, reason, delay = "rate_limited", "Codex is rate limited. Nabu kept the queue intact and will retry conservatively.", 10*time.Minute
	} else if containsAny(message, "authentication required", "not authenticated", "unauthorized", "forbidden") {
		reason, delay = "Codex authentication needs attention. Run `codex login` and then resume Nabu.", 15*time.Minute
	} else if containsAny(message, "executable file not found", "no such file or directory", "permission denied") {
		reason, delay = "The configured Codex executable is unavailable. Run `nabu doctor`.", 15*time.Minute
	} else if !containsAny(message, "temporarily unavailable", "temporary failure", "connection reset", "connection refused", "network is unreachable", "service unavailable", "stream disconnected") {
		return
	}
	retryAt := now.Add(delay)
	o.codexState, o.codexReason, o.codexRetryAt = state, reason, &retryAt
}

func (o *Operator) codexReady(now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.codexRetryAt == nil || !now.Before(*o.codexRetryAt)
}

func (o *Operator) codexHealth(binaryAvailable bool) (string, string, *time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !binaryAvailable {
		return "unavailable", "The configured Codex executable is unavailable. Run `nabu doctor`.", nil
	}
	if o.codexState == "" || (o.codexRetryAt != nil && !time.Now().UTC().Before(*o.codexRetryAt)) {
		return "available", "", nil
	}
	return o.codexState, o.codexReason, o.codexRetryAt
}

func containsAny(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func userFacingRunError(execution runner.ExecutionResult) string {
	message := strings.ToLower(execution.Stderr + "\n" + execution.Stdout + "\n" + execution.Error)
	switch {
	case execution.Status == domain.RunTimedOut:
		return "the drafting request timed out"
	case execution.Status == domain.RunCancelled:
		return "the drafting request was cancelled"
	case containsAny(message, "rate limit", "too many requests", "quota exceeded", "429"):
		return "Codex is rate limited; try again after the retry time shown in status"
	case containsAny(message, "authentication required", "not authenticated", "unauthorized"):
		return "Codex authentication needs attention; run `codex login`"
	case containsAny(message, "executable file not found", "no such file or directory"):
		return "the configured Codex executable is unavailable; run `nabu doctor`"
	default:
		return "review the daemon logs for details"
	}
}

func translateNotFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return api.ErrNotFound
	}
	return err
}

func checkBinary(ctx context.Context, binary string, args ...string) api.CheckResult {
	path, err := exec.LookPath(binary)
	if err != nil {
		return api.CheckResult{Error: err.Error()}
	}
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, path, args...).CombinedOutput()
	if err != nil {
		return api.CheckResult{Path: path, Error: strings.TrimSpace(string(output))}
	}
	return api.CheckResult{Available: true, Path: path, Version: strings.TrimSpace(string(output))}
}

func binaryAvailable(configured, fallback string) bool {
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return true
		}
	}
	_, err := exec.LookPath(fallback)
	return err == nil
}

func validateWorkspaces(paths []string) ([]domain.Workspace, error) {
	seen := make(map[string]struct{}, len(paths))
	result := make([]domain.Workspace, 0, len(paths))
	for _, raw := range paths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !filepath.IsAbs(raw) {
			return nil, fmt.Errorf("workspace path must be absolute: %s", raw)
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(raw))
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %s: %w", raw, err)
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("workspace is not a readable directory: %s", raw)
		}
		directory, err := os.Open(canonical)
		if err != nil {
			return nil, fmt.Errorf("open workspace %s: %w", raw, err)
		}
		_ = directory.Close()
		probe, err := os.CreateTemp(canonical, ".nabu-write-check-*")
		if err != nil {
			return nil, fmt.Errorf("workspace is not writable: %s: %w", raw, err)
		}
		probePath := probe.Name()
		if closeErr := probe.Close(); closeErr != nil {
			_ = os.Remove(probePath)
			return nil, fmt.Errorf("verify workspace writability: %s: %w", raw, closeErr)
		}
		if removeErr := os.Remove(probePath); removeErr != nil {
			return nil, fmt.Errorf("clean workspace write check: %s: %w", raw, removeErr)
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, domain.Workspace{Name: filepath.Base(canonical), Path: canonical, DefaultBranch: gitBranch(canonical), Allowed: true})
	}
	if len(result) == 0 {
		return nil, errors.New("at least one approved workspace is required")
	}
	return result, nil
}

func gitBranch(path string) string {
	output, err := exec.Command("git", "-C", path, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func normalizePolicy(policy domain.Policy) domain.Policy {
	if policy.Read == "" {
		policy.Read = "allow"
	}
	if policy.Work == "" {
		policy.Work = "allow"
	}
	if policy.Publish == "" {
		policy.Publish = "ask"
	}
	if policy.Dangerous == "" {
		policy.Dangerous = "ask"
	}
	return policy
}

func writeContextFile(path, heading, primary, secondary string) error {
	content := "# " + heading + "\n\n" + strings.TrimSpace(primary) + "\n"
	if strings.TrimSpace(secondary) != "" {
		content += "\n## Context\n\n" + strings.TrimSpace(secondary) + "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func writePolicyFile(path string, policy domain.Policy) error {
	content := fmt.Sprintf("# Policy\n\n- Read: %s\n- Work: %s\n- Publish: %s\n- Dangerous: %s\n", policy.Read, policy.Work, policy.Publish, policy.Dangerous)
	return os.WriteFile(path, []byte(content), 0o600)
}

func readBounded(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buffer := make([]byte, limit)
	read, err := file.Read(buffer)
	if err != nil && read == 0 {
		return "", err
	}
	return string(buffer[:read]), nil
}

var (
	secretPattern      = regexp.MustCompile(`(?im)(api[_ -]?key|access[_ -]?token|auth[_ -]?token|password|client[_ -]?secret|secret)\s*(?:is|[:=])\s*[^\r\n]+`)
	secretValuePattern = regexp.MustCompile(`(?im)\b(?:api[_ -]?key|access[_ -]?token|auth[_ -]?token|password|client[_ -]?secret|secret)\b\s*(?:is|[:=])\s*["']?[A-Za-z0-9_./+=~-]{12,}`)
	bearerValuePattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`)
	commonTokenPattern = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9_]{20,})\b`)
)

func containsLikelySecret(value string) bool {
	return secretValuePattern.MatchString(value) || bearerValuePattern.MatchString(value) || commonTokenPattern.MatchString(value)
}

func redactSecrets(value string) string {
	return secretPattern.ReplaceAllString(value, "$1: [REDACTED]")
}

func redactMission(mission domain.Mission) domain.Mission {
	mission.Statement = redactSecrets(mission.Statement)
	mission.Context = redactSecrets(mission.Context)
	return mission
}

func redactRunResult(result domain.RunResult) domain.RunResult {
	result.Summary = redactSecrets(result.Summary)
	for index := range result.FilesChanged {
		result.FilesChanged[index] = redactSecrets(result.FilesChanged[index])
	}
	for index := range result.Verification {
		result.Verification[index].Name = redactSecrets(result.Verification[index].Name)
		result.Verification[index].Details = redactSecrets(result.Verification[index].Details)
	}
	for index := range result.Uncertainties {
		result.Uncertainties[index] = redactSecrets(result.Uncertainties[index])
	}
	if result.ApprovalNeeded != nil {
		value := redactSecrets(*result.ApprovalNeeded)
		result.ApprovalNeeded = &value
	}
	for writeIndex := range result.DatasetWrites {
		if result.DatasetWrites[writeIndex].Dataset != nil {
			result.DatasetWrites[writeIndex].Dataset.Name = redactSecrets(result.DatasetWrites[writeIndex].Dataset.Name)
			result.DatasetWrites[writeIndex].Dataset.Description = redactSecrets(result.DatasetWrites[writeIndex].Dataset.Description)
			for columnIndex := range result.DatasetWrites[writeIndex].Dataset.Schema {
				result.DatasetWrites[writeIndex].Dataset.Schema[columnIndex].Description = redactSecrets(result.DatasetWrites[writeIndex].Dataset.Schema[columnIndex].Description)
			}
		}
	}
	for appIndex := range result.LocalApps {
		result.LocalApps[appIndex].Name = redactSecrets(result.LocalApps[appIndex].Name)
		result.LocalApps[appIndex].Description = redactSecrets(result.LocalApps[appIndex].Description)
		for argumentIndex := range result.LocalApps[appIndex].Command {
			result.LocalApps[appIndex].Command[argumentIndex] = redactSecrets(result.LocalApps[appIndex].Command[argumentIndex])
		}
	}
	return result
}

func verificationPassed(items []domain.Verification) bool {
	if len(items) == 0 {
		return false
	}
	passed := false
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Details) == "" {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(item.Status)) {
		case "passed":
			passed = true
		case "not_run":
			// An explicitly documented out-of-scope check is useful evidence,
			// but completion still requires at least one check that passed.
		default:
			return false
		}
	}
	return passed
}

func runActivity(run domain.Run, events []domain.Event) []domain.RunEvent {
	activity := make([]domain.RunEvent, 0)
	for _, event := range events {
		data := map[string]any{}
		_ = json.Unmarshal(event.Data, &data)
		runID, _ := data["run_id"].(string)
		if event.EntityID != run.ID && runID != run.ID {
			continue
		}
		message := "Run activity updated."
		switch event.Type {
		case "task.started":
			message = "Nabu started the task and generated its execution packet."
		case "run.started":
			message = "Codex started in the approved workspace."
		case "task.completed":
			message = "The task completed with structured verification evidence."
		case "task.failed":
			message = "The task failed and the queue state was preserved."
		case "task.cancelled":
			message = "The running task was cancelled."
		case "run.completed":
			message = "The Codex process finished and its result was recorded."
		case "orientation.started":
			message = "Nabu started an orientation run."
		case "orientation.completed":
			message = "Nabu completed orientation and updated the durable queue."
		case "orientation.failed":
			message = "Orientation failed; Nabu retained the mission and queue."
		}
		activity = append(activity, domain.RunEvent{ID: fmt.Sprintf("%d", event.ID), Type: event.Type, Message: message, At: event.CreatedAt})
	}
	if activity == nil {
		return []domain.RunEvent{}
	}
	return activity
}

func definitions(values []string) []domain.DefinitionItem {
	result := make([]domain.DefinitionItem, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, domain.DefinitionItem{Text: value})
		}
	}
	return result
}

func validPriority(priority domain.Priority) bool {
	return priority == domain.PriorityHigh || priority == domain.PriorityNormal || priority == domain.PriorityLow
}

func allowedUserTransition(from, to domain.TaskStatus) bool {
	if to == domain.TaskCancelled {
		return from != domain.TaskCompleted && from != domain.TaskCancelled
	}
	if to == domain.TaskReady {
		return from == domain.TaskIdea || from == domain.TaskWaiting || from == domain.TaskFailed || from == domain.TaskCancelled
	}
	if to == domain.TaskIdea {
		return from == domain.TaskReady
	}
	return false
}

func hasStatus(tasks []domain.Task, status domain.TaskStatus) bool {
	for _, task := range tasks {
		if task.Status == status {
			return true
		}
	}
	return false
}

func filterTasks(tasks []domain.Task, statuses ...domain.TaskStatus) []domain.Task {
	wanted := make(map[domain.TaskStatus]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	result := make([]domain.Task, 0)
	for _, task := range tasks {
		if _, ok := wanted[task.Status]; ok {
			result = append(result, task)
		}
	}
	return result
}

func filterLimit(tasks []domain.Task, limit int, statuses ...domain.TaskStatus) []domain.Task {
	result := filterTasks(tasks, statuses...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}
