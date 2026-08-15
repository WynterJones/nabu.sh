// Package automation runs Nabu's durable schedules and deterministic local
// scripts. It queues reasoning work only for explicit task/orientation
// schedules or interesting script results; routine ticks never invoke AI.
package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/scheduler"
	"github.com/nabu-sh/nabu/internal/scriptrunner"
	"github.com/nabu-sh/nabu/internal/store"
)

const (
	defaultTickInterval = 5 * time.Second
	minimumLease        = 35 * time.Minute
	maximumClaimsPerRun = 32
	maximumStoredError  = 2 * 1024
)

var ErrAlreadyStarted = errors.New("automation: engine is already started")

type persistence interface {
	ClaimDueSchedule(context.Context, time.Time, time.Duration) (domain.Schedule, error)
	FinishScheduleClaim(context.Context, string, string, *time.Time, string, time.Time) error
	GetWorkspace(context.Context, string) (domain.Workspace, error)
	GetScript(context.Context, string) (domain.Script, error)
	CreateScriptRun(context.Context, domain.ScriptRun) (domain.ScriptRun, error)
	UpdateScriptRun(context.Context, domain.ScriptRun) error
	CreateArtifact(context.Context, domain.Artifact) (domain.Artifact, error)
	CreateTask(context.Context, domain.Task) (domain.Task, error)
	GetTask(context.Context, string) (domain.Task, error)
	RequestOrientation(context.Context) (bool, error)
	RequestOrientationForWorkspace(context.Context, string) (bool, error)
}

type scriptRunner interface {
	Run(context.Context, scriptrunner.Request) (scriptrunner.Execution, error)
}

// Options contains filesystem locations and dependencies owned by the daemon.
// LeaseDuration is clamped to 35 minutes, longer than the script runner's
// maximum timeout, preventing a second daemon from reclaiming a live script.
type Options struct {
	Store         persistence
	Runner        scriptRunner
	ScriptsRoot   string
	RunsRoot      string
	Environment   []string
	Credentials   credentials.Backend
	TickInterval  time.Duration
	LeaseDuration time.Duration
	Now           func() time.Time
	Logf          func(string, ...any)
}

type Engine struct {
	store         persistence
	runner        scriptRunner
	scriptsRoot   string
	runsRoot      string
	environment   []string
	credentials   credentials.Backend
	tickInterval  time.Duration
	leaseDuration time.Duration
	now           func() time.Time
	logf          func(string, ...any)

	stateMu sync.Mutex
	started bool
	tickMu  sync.Mutex
}

func New(options Options) (*Engine, error) {
	if options.Store == nil {
		return nil, errors.New("automation: store is required")
	}
	if options.Runner == nil {
		options.Runner = scriptrunner.New()
	}
	if options.Credentials == nil {
		options.Credentials = credentials.NewPlatform()
	}
	if strings.TrimSpace(options.ScriptsRoot) == "" {
		return nil, errors.New("automation: scripts root is required")
	}
	if strings.TrimSpace(options.RunsRoot) == "" {
		return nil, errors.New("automation: runs root is required")
	}
	if options.TickInterval <= 0 {
		options.TickInterval = defaultTickInterval
	}
	if options.LeaseDuration < minimumLease {
		options.LeaseDuration = minimumLease
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logf == nil {
		options.Logf = func(string, ...any) {}
	}
	return &Engine{
		store:         options.Store,
		runner:        options.Runner,
		scriptsRoot:   options.ScriptsRoot,
		runsRoot:      options.RunsRoot,
		environment:   append([]string(nil), options.Environment...),
		credentials:   options.Credentials,
		tickInterval:  options.TickInterval,
		leaseDuration: options.LeaseDuration,
		now:           options.Now,
		logf:          options.Logf,
	}, nil
}

// Start processes work due after a restart immediately, then polls until the
// context is cancelled. Individual schedule errors are persisted and logged;
// they do not stop unrelated schedules.
func (e *Engine) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("automation: nil context")
	}
	e.stateMu.Lock()
	if e.started {
		e.stateMu.Unlock()
		return ErrAlreadyStarted
	}
	e.started = true
	e.stateMu.Unlock()
	defer func() {
		e.stateMu.Lock()
		e.started = false
		e.stateMu.Unlock()
	}()

	if err := e.RunDue(ctx); err != nil && ctx.Err() == nil {
		e.logf("automation run: %v", err)
	}
	ticker := time.NewTicker(e.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := e.RunDue(ctx); err != nil && ctx.Err() == nil {
				e.logf("automation run: %v", err)
			}
		}
	}
}

// RunDue claims and executes a bounded batch of due schedules. Claims are
// always finished with the next occurrence and a durable error description.
func (e *Engine) RunDue(ctx context.Context) error {
	if ctx == nil {
		return errors.New("automation: nil context")
	}
	e.tickMu.Lock()
	defer e.tickMu.Unlock()

	var runErrors []error
	for range maximumClaimsPerRun {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(runErrors, err)...)
		}
		claimedAt := e.now().UTC()
		schedule, err := e.store.ClaimDueSchedule(ctx, claimedAt, e.leaseDuration)
		if errors.Is(err, store.ErrNotFound) {
			return errors.Join(runErrors...)
		}
		if err != nil {
			return errors.Join(append(runErrors, fmt.Errorf("automation: claim due schedule: %w", err))...)
		}

		dispatchErr := e.dispatch(ctx, schedule)
		finishedAt := e.now().UTC()
		next, nextErr := scheduler.Next(schedule, finishedAt)
		var nextRunAt *time.Time
		if nextErr == nil {
			next = next.UTC()
			nextRunAt = &next
		}
		attemptErr := errors.Join(dispatchErr, nextErr)
		lastError := boundedError(attemptErr)
		finishContext, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		finishErr := e.store.FinishScheduleClaim(
			finishContext, schedule.ID, schedule.ClaimToken, nextRunAt, lastError, finishedAt,
		)
		finishCancel()
		if finishErr != nil {
			attemptErr = errors.Join(attemptErr, fmt.Errorf("finish claim: %w", finishErr))
		}
		if attemptErr != nil {
			runErrors = append(runErrors, fmt.Errorf("schedule %q: %w", schedule.Name, attemptErr))
		}
	}
	return errors.Join(runErrors...)
}

func (e *Engine) dispatch(ctx context.Context, schedule domain.Schedule) error {
	switch schedule.Kind {
	case domain.ScheduleScript:
		var payload ScriptPayload
		if err := decodePayload(schedule.Payload, &payload, false); err != nil {
			return err
		}
		if strings.TrimSpace(payload.ScriptID) == "" {
			return errors.New("automation: script_id is required")
		}
		if err := validateInterestingAction(payload.OnInteresting); err != nil {
			return err
		}
		if payload.InterestingTask != nil {
			if err := validateTaskPayload(*payload.InterestingTask, false); err != nil {
				return err
			}
		}
		_, err := e.runScript(ctx, payload.ScriptID, schedule.ID, schedule.WorkspaceID, "", scheduleEffectID(schedule), false, payload)
		return err
	case domain.ScheduleTask:
		var payload TaskPayload
		if err := decodePayload(schedule.Payload, &payload, false); err != nil {
			return err
		}
		if err := validateTaskPayload(payload, true); err != nil {
			return err
		}
		if len(payload.DefinitionOfDone) == 0 {
			return errors.New("automation: scheduled task requires a definition of done")
		}
		if err := applyScheduleWorkspace(schedule.WorkspaceID, &payload.WorkspaceID); err != nil {
			return err
		}
		_, err := e.createTask(ctx, payload, "schedule:"+schedule.ID, scheduleEffectID(schedule))
		return err
	case domain.ScheduleOrient:
		var payload OrientPayload
		if err := decodePayload(schedule.Payload, &payload, true); err != nil {
			return err
		}
		if len(payload.Reason) > maximumWhyBytes {
			return fmt.Errorf("automation: orientation reason exceeds %d bytes", maximumWhyBytes)
		}
		_, err := e.requestOrientation(ctx, schedule.WorkspaceID)
		return err
	default:
		return fmt.Errorf("automation: unsupported schedule kind %q", schedule.Kind)
	}
}

func (e *Engine) requestOrientation(ctx context.Context, workspaceID string) (bool, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return e.store.RequestOrientation(ctx)
	}
	return e.store.RequestOrientationForWorkspace(ctx, workspaceID)
}

func applyScheduleWorkspace(scheduleWorkspace string, target *string) error {
	scheduleWorkspace = strings.TrimSpace(scheduleWorkspace)
	requested := strings.TrimSpace(*target)
	if scheduleWorkspace != "" && requested != "" && requested != scheduleWorkspace {
		return fmt.Errorf("automation: payload workspace %q does not match schedule workspace %q", requested, scheduleWorkspace)
	}
	if requested == "" {
		*target = scheduleWorkspace
	} else {
		*target = requested
	}
	return nil
}

func scheduleEffectID(schedule domain.Schedule) string {
	occurrence := "unscheduled"
	if schedule.NextRunAt != nil {
		occurrence = schedule.NextRunAt.UTC().Format(time.RFC3339Nano)
	}
	return stableID("schedule-effect", schedule.ID+"\x00"+occurrence)
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) <= maximumStoredError {
		return value
	}
	return value[:maximumStoredError]
}
