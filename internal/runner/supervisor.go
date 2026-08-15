// Package runner supervises Codex CLI processes and builds the packets exchanged
// with them. It deliberately has no persistence dependency; callers can store the
// returned metadata in whatever durable store they own.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const (
	defaultTimeout          = 30 * time.Minute
	defaultTerminationGrace = 2 * time.Second
	defaultRetryDelay       = 250 * time.Millisecond
	maximumAttempts         = 2
	maximumCapturedBytes    = 10 * 1024 * 1024
	maximumPendingBytes     = 1024 * 1024
)

// OutputStream identifies the child-process stream that produced an event.
type OutputStream string

const (
	OutputStdout OutputStream = "stdout"
	OutputStderr OutputStream = "stderr"
)

// OutputEvent is a complete output line suitable for forwarding as an SSE event.
// JSON contains the same line when it is valid JSON (as Codex --json output is).
type OutputEvent struct {
	Attempt int             `json:"attempt"`
	Stream  OutputStream    `json:"stream"`
	Data    string          `json:"data"`
	JSON    json.RawMessage `json:"json,omitempty"`
	At      time.Time       `json:"at"`
}

// OutputCallback receives stdout and stderr incrementally. Callbacks may be
// called from either stream but are serialized, and should return promptly.
type OutputCallback func(OutputEvent)

// ProcessStarted is emitted as soon as a child process has a PID, allowing the
// daemon to persist and display an active run while Run is still blocked.
type ProcessStarted struct {
	Attempt          int       `json:"attempt"`
	PID              int       `json:"pid"`
	WorkingDirectory string    `json:"working_directory"`
	Command          []string  `json:"command"`
	StartedAt        time.Time `json:"started_at"`
}

// ProcessStartCallback receives child-process start metadata.
type ProcessStartCallback func(ProcessStarted)

// RetryDecider decides whether a failed attempt is safe to retry. The supervisor
// always caps execution at two attempts and never retries cancellation or timeout.
type RetryDecider func(AttemptResult, error) bool

// Config supplies process defaults. A zero Config uses the Codex CLI in JSONL
// mode with an explicit writable-workspace sandbox and the packet on stdin.
type Config struct {
	Command          string
	Args             []string
	Environment      []string
	Timeout          time.Duration
	TerminationGrace time.Duration
	RetryDelay       time.Duration
	RetryDecider     RetryDecider
}

// DefaultConfig returns production defaults for invoking Codex.
func DefaultConfig() Config {
	return Config{
		Command: "codex",
		Args: []string{
			"exec", "--json", "--ignore-user-config",
			"--sandbox", "workspace-write", "-c", `approval_policy="never"`,
			"-c", `sandbox_workspace_write.network_access=true`,
			"--skip-git-repo-check", "-",
		},
		Timeout:          defaultTimeout,
		TerminationGrace: defaultTerminationGrace,
		RetryDelay:       defaultRetryDelay,
		RetryDecider:     DefaultRetryDecider,
	}
}

// Request describes one logical Codex run. Command and Args override Config and
// primarily exist to make the supervisor testable without a real Codex process.
// A nil Args slice inherits Config.Args; a non-nil empty slice passes no args.
type Request struct {
	WorkingDirectory  string
	Prompt            string
	Command           string
	Args              []string
	Environment       []string
	SecretEnvironment []EnvironmentSecret
	Timeout           time.Duration
	MaxAttempts       int
	OnStart           ProcessStartCallback
	OnOutput          OutputCallback
}

// EnvironmentSecret owns a mutable credential buffer for one Codex process.
// Values are copied into the child environment, redacted from all captured
// output, then wiped immediately after the process starts.
type EnvironmentSecret struct {
	Name  string
	Value []byte
}

// AttemptResult records one child process, including attempts that were retried.
type AttemptResult struct {
	Attempt          int              `json:"attempt"`
	PID              int              `json:"pid,omitempty"`
	WorkingDirectory string           `json:"working_directory"`
	Command          []string         `json:"command"`
	StartedAt        time.Time        `json:"started_at"`
	EndedAt          time.Time        `json:"ended_at"`
	ExitCode         *int             `json:"exit_code,omitempty"`
	Signal           string           `json:"signal,omitempty"`
	Status           domain.RunStatus `json:"status"`
	Stdout           string           `json:"stdout"`
	Stderr           string           `json:"stderr"`
	Error            string           `json:"error,omitempty"`
}

// ExecutionResult exposes the final attempt at the top level and retains every
// attempt for durable audit/history.
type ExecutionResult struct {
	Attempt          int              `json:"attempt"`
	PID              int              `json:"pid,omitempty"`
	WorkingDirectory string           `json:"working_directory"`
	Command          []string         `json:"command"`
	StartedAt        time.Time        `json:"started_at"`
	EndedAt          time.Time        `json:"ended_at"`
	ExitCode         *int             `json:"exit_code,omitempty"`
	Signal           string           `json:"signal,omitempty"`
	Status           domain.RunStatus `json:"status"`
	Stdout           string           `json:"stdout"`
	Stderr           string           `json:"stderr"`
	Error            string           `json:"error,omitempty"`
	Attempts         []AttemptResult  `json:"attempts"`
}

// Supervisor owns the lifecycle of local Codex processes.
type Supervisor struct {
	config Config
}

// NewSupervisor returns a process supervisor with zero config fields defaulted.
func NewSupervisor(config Config) *Supervisor {
	defaults := DefaultConfig()
	if config.Command == "" {
		config.Command = defaults.Command
	}
	if config.Args == nil {
		config.Args = defaults.Args
	}
	if config.Timeout <= 0 {
		config.Timeout = defaults.Timeout
	}
	if config.TerminationGrace <= 0 {
		config.TerminationGrace = defaults.TerminationGrace
	}
	if config.RetryDelay < 0 {
		config.RetryDelay = 0
	} else if config.RetryDelay == 0 {
		config.RetryDelay = defaults.RetryDelay
	}
	if config.RetryDecider == nil {
		config.RetryDecider = defaults.RetryDecider
	}
	config.Args = append([]string(nil), config.Args...)
	config.Environment = append([]string(nil), config.Environment...)
	return &Supervisor{config: config}
}

// Run starts a supervised child process, captures its output, and retries at
// most once when the configured decider identifies a transient failure.
func (s *Supervisor) Run(ctx context.Context, request Request) (ExecutionResult, error) {
	defer destroyEnvironmentSecrets(request.SecretEnvironment)
	if ctx == nil {
		return ExecutionResult{}, errors.New("runner: nil context")
	}
	workingDirectory, err := validateWorkingDirectory(request.WorkingDirectory)
	if err != nil {
		return ExecutionResult{}, err
	}

	command := request.Command
	if command == "" {
		command = s.config.Command
	}
	if strings.TrimSpace(command) == "" {
		return ExecutionResult{}, errors.New("runner: command is required")
	}
	args := request.Args
	if args == nil {
		args = s.config.Args
	}
	args = append([]string(nil), args...)

	timeout := request.Timeout
	if timeout <= 0 {
		timeout = s.config.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	maxAttempts := request.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = maximumAttempts
	}
	if maxAttempts > maximumAttempts {
		maxAttempts = maximumAttempts
	}

	environment := append([]string(nil), s.config.Environment...)
	environment = append(environment, request.Environment...)
	secretEnvironment, redactionValues, err := prepareEnvironmentSecrets(request.SecretEnvironment)
	if err != nil {
		return ExecutionResult{}, err
	}
	defer clearEnvironmentStrings(secretEnvironment)
	defer wipeEnvironmentValues(redactionValues)
	environment = append(environment, secretEnvironment...)
	dispatcher := &callbackDispatcher{callback: request.OnOutput}
	var result ExecutionResult
	var finalErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptResult, runErr := s.runAttempt(ctx, attemptSpec{
			attempt:          attempt,
			workingDirectory: workingDirectory,
			command:          command,
			args:             args,
			environment:      environment,
			prompt:           request.Prompt,
			onStart:          request.OnStart,
			dispatcher:       dispatcher,
			redactionValues:  redactionValues,
		})
		// exec.Cmd copied the environment into the child on the first attempt.
		// The supervisor never retries secret-bearing requests because the
		// caller-owned buffers are deliberately destroyed at this boundary.
		if len(request.SecretEnvironment) > 0 {
			clearEnvironmentStrings(secretEnvironment)
			destroyEnvironmentSecrets(request.SecretEnvironment)
			maxAttempts = 1
		}
		result.Attempts = append(result.Attempts, attemptResult)
		copyFinalAttempt(&result, attemptResult)
		finalErr = runErr
		if runErr == nil {
			return result, nil
		}
		if attempt == maxAttempts || ctx.Err() != nil || !s.config.RetryDecider(attemptResult, runErr) {
			break
		}
		if err := waitForRetry(ctx, s.config.RetryDelay); err != nil {
			finalErr = err
			result.Status = statusFromContext(ctx)
			result.Error = err.Error()
			break
		}
	}

	return result, finalErr
}

type attemptSpec struct {
	attempt          int
	workingDirectory string
	command          string
	args             []string
	environment      []string
	prompt           string
	onStart          ProcessStartCallback
	dispatcher       *callbackDispatcher
	redactionValues  [][]byte
}

func (s *Supervisor) runAttempt(ctx context.Context, spec attemptSpec) (AttemptResult, error) {
	startedAt := time.Now()
	fullCommand := append([]string{spec.command}, spec.args...)
	result := AttemptResult{
		Attempt:          spec.attempt,
		WorkingDirectory: spec.workingDirectory,
		Command:          fullCommand,
		StartedAt:        startedAt,
		Status:           domain.RunRunning,
	}
	if err := ctx.Err(); err != nil {
		result.EndedAt = time.Now()
		result.Status = statusFromContext(ctx)
		result.Error = err.Error()
		return result, err
	}

	stdout := newLineCapture(spec.attempt, OutputStdout, spec.dispatcher, spec.redactionValues)
	stderr := newLineCapture(spec.attempt, OutputStderr, spec.dispatcher, spec.redactionValues)
	command := exec.Command(spec.command, spec.args...)
	command.Dir = spec.workingDirectory
	command.Stdin = strings.NewReader(spec.prompt)
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = append(os.Environ(), spec.environment...)
	configureProcessGroup(command)

	if err := command.Start(); err != nil {
		stdout.flush()
		stderr.flush()
		result.EndedAt = time.Now()
		result.Status = domain.RunFailed
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()
		result.Error = err.Error()
		return result, fmt.Errorf("runner: start %q: %w", spec.command, err)
	}
	command.Env = nil
	result.PID = command.Process.Pid
	if spec.onStart != nil {
		spec.onStart(ProcessStarted{
			Attempt:          result.Attempt,
			PID:              result.PID,
			WorkingDirectory: result.WorkingDirectory,
			Command:          append([]string(nil), result.Command...),
			StartedAt:        result.StartedAt,
		})
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		waitErr = terminateProcessGroup(command.Process.Pid, done, s.config.TerminationGrace)
		if waitErr == nil {
			waitErr = ctx.Err()
		}
	}

	stdout.flush()
	stderr.flush()
	result.EndedAt = time.Now()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if command.ProcessState != nil {
		exitCode := command.ProcessState.ExitCode()
		result.ExitCode = &exitCode
		if waitStatus, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
			result.Signal = waitStatus.Signal().String()
		}
	}

	if ctx.Err() != nil {
		result.Status = statusFromContext(ctx)
		result.Error = ctx.Err().Error()
		return result, ctx.Err()
	}
	if waitErr != nil {
		result.Status = domain.RunFailed
		result.Error = actionableCommandFailure(result.Stdout, result.Stderr, waitErr)
		return result, errors.New(result.Error)
	}

	result.Status = domain.RunCompleted
	return result, nil
}

func validateWorkingDirectory(directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", errors.New("runner: working directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("runner: resolve working directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("runner: inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("runner: working directory %q is not a directory", absolute)
	}
	return absolute, nil
}

func configureProcessGroup(command *exec.Cmd) {
	// Nabu targets macOS and Linux. A dedicated group lets cancellation reach
	// helpers spawned by Codex without ever signaling Nabu's own process group.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessGroup(pid int, done <-chan error, grace time.Duration) error {
	if pid <= 0 {
		return <-done
	}
	select {
	case err := <-done:
		return err
	default:
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return <-done
	}
}

func statusFromContext(ctx context.Context) domain.RunStatus {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return domain.RunTimedOut
	}
	return domain.RunCancelled
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func copyFinalAttempt(result *ExecutionResult, attempt AttemptResult) {
	result.Attempt = attempt.Attempt
	result.PID = attempt.PID
	result.WorkingDirectory = attempt.WorkingDirectory
	result.Command = append([]string(nil), attempt.Command...)
	result.StartedAt = attempt.StartedAt
	result.EndedAt = attempt.EndedAt
	result.ExitCode = attempt.ExitCode
	result.Signal = attempt.Signal
	result.Status = attempt.Status
	result.Stdout = attempt.Stdout
	result.Stderr = attempt.Stderr
	result.Error = attempt.Error
}

// DefaultRetryDecider only retries failures that look transient. It avoids
// repeating arbitrary task execution after generic errors, which could duplicate
// side effects in the approved workspace.
func DefaultRetryDecider(attempt AttemptResult, err error) bool {
	if err == nil || attempt.Status == domain.RunCancelled || attempt.Status == domain.RunTimedOut {
		return false
	}
	if attempt.ExitCode != nil {
		switch *attempt.ExitCode {
		case 69, 70, 75: // unavailable, software/internal error, temporary failure
			return true
		case 126, 127:
			return false
		}
	}
	if attempt.Signal != "" && attempt.Signal != syscall.SIGTERM.String() && attempt.Signal != syscall.SIGKILL.String() {
		return true
	}

	message := strings.ToLower(attempt.Stderr + "\n" + attempt.Stdout + "\n" + err.Error())
	permanentMarkers := []string{
		"authentication required", "not authenticated", "unauthorized", "forbidden",
		"invalid argument", "unknown option", "permission denied", "executable file not found",
		"no such file or directory", "bootstrap_check_in", "sandbox: deny", "mach-lookup",
	}
	for _, marker := range permanentMarkers {
		if strings.Contains(message, marker) {
			return false
		}
	}
	transientMarkers := []string{
		"temporarily unavailable", "temporary failure", "connection reset", "connection refused",
		"connection closed", "network is unreachable", "service unavailable", "internal server error",
		"stream disconnected", "rate limit", "too many requests", "try again",
	}
	for _, marker := range transientMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func actionableCommandFailure(stdout, stderr string, cause error) string {
	message := strings.ToLower(stdout + "\n" + stderr + "\n" + cause.Error())
	if containsBrowserSandboxDenial(message) {
		return "runner: browser QA needs attention: macOS denied Chromium bootstrap inside the Codex workspace sandbox; configure an enabled read-only registered browser verifier for this workspace and retry. Nabu did not broaden the default task sandbox"
	}
	return fmt.Sprintf("runner: command failed: %v", cause)
}

func containsBrowserSandboxDenial(message string) bool {
	browser := strings.Contains(message, "chromium") || strings.Contains(message, "playwright") || strings.Contains(message, "chrome")
	denied := strings.Contains(message, "bootstrap_check_in") || strings.Contains(message, "sandbox: deny") ||
		strings.Contains(message, "mach-lookup") || strings.Contains(message, "operation not permitted")
	return browser && denied
}

type callbackDispatcher struct {
	callback OutputCallback
	mu       sync.Mutex
}

func (d *callbackDispatcher) emit(event OutputEvent) {
	if d == nil || d.callback == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.callback(event)
}

type lineCapture struct {
	attempt    int
	stream     OutputStream
	dispatcher *callbackDispatcher
	captured   bytes.Buffer
	pending    bytes.Buffer
	truncated  bool
	mu         sync.Mutex
	redactions [][]byte
}

func newLineCapture(attempt int, stream OutputStream, dispatcher *callbackDispatcher, redactions ...[][]byte) *lineCapture {
	var values [][]byte
	if len(redactions) > 0 {
		values = redactions[0]
	}
	return &lineCapture{attempt: attempt, stream: stream, dispatcher: dispatcher, redactions: values}
}

func (w *lineCapture) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(data)
	data = redactEnvironmentOutput(data, w.redactions)
	w.appendCaptured(data)
	_, _ = w.pending.Write(data)
	for {
		pending := w.pending.Bytes()
		index := bytes.IndexByte(pending, '\n')
		if index < 0 {
			break
		}
		line := append([]byte(nil), pending[:index]...)
		w.pending.Next(index + 1)
		w.emit(line)
	}
	if w.pending.Len() > maximumPendingBytes {
		pending := w.pending.Bytes()
		tail := append([]byte(nil), pending[len(pending)-maximumPendingBytes:]...)
		w.pending.Reset()
		_, _ = w.pending.Write(tail)
	}
	// io.Writer must report consumption of the caller's original bytes. The
	// redacted representation may be shorter, but that is an internal copy.
	return written, nil
}

var safeSecretEnvironmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

var reservedSecretEnvironment = map[string]struct{}{
	"BASHOPTS": {}, "BASH_ENV": {}, "CDPATH": {}, "ENV": {}, "GLOBIGNORE": {}, "HOME": {}, "IFS": {},
	"NODE_OPTIONS": {}, "OLDPWD": {}, "PATH": {}, "PERL5LIB": {}, "PERL5OPT": {}, "PROMPT_COMMAND": {},
	"PS4": {}, "PWD": {}, "PYTHONHOME": {}, "PYTHONPATH": {}, "RUBYLIB": {}, "RUBYOPT": {}, "SHELL": {}, "SHELLOPTS": {},
}

func prepareEnvironmentSecrets(secrets []EnvironmentSecret) ([]string, [][]byte, error) {
	environment := make([]string, 0, len(secrets))
	values := make([][]byte, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for index := range secrets {
		name := strings.TrimSpace(secrets[index].Name)
		if !safeSecretEnvironmentName.MatchString(name) || strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") ||
			strings.HasPrefix(name, "BASH_FUNC_") || strings.HasPrefix(name, "GIT_CONFIG_") || strings.HasSuffix(name, "_ASKPASS") {
			return nil, nil, fmt.Errorf("runner: unsafe secret environment variable %q", name)
		}
		if _, reserved := reservedSecretEnvironment[name]; reserved {
			return nil, nil, fmt.Errorf("runner: reserved secret environment variable %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, nil, fmt.Errorf("runner: duplicate secret environment variable %q", name)
		}
		seen[name] = struct{}{}
		if len(secrets[index].Value) == 0 || len(secrets[index].Value) > 64*1024 || bytes.IndexByte(secrets[index].Value, 0) >= 0 || bytes.IndexByte(secrets[index].Value, '\n') >= 0 {
			return nil, nil, fmt.Errorf("runner: invalid secret value for %q", name)
		}
		value := append([]byte(nil), secrets[index].Value...)
		values = append(values, value)
		environment = append(environment, name+"="+string(value))
	}
	return environment, values, nil
}

func redactEnvironmentOutput(data []byte, values [][]byte) []byte {
	result := append([]byte(nil), data...)
	sorted := append([][]byte(nil), values...)
	sort.Slice(sorted, func(left, right int) bool { return len(sorted[left]) > len(sorted[right]) })
	for _, value := range sorted {
		if len(value) > 0 {
			result = bytes.ReplaceAll(result, value, []byte("[REDACTED]"))
		}
	}
	return result
}

func destroyEnvironmentSecrets(secrets []EnvironmentSecret) {
	for index := range secrets {
		for valueIndex := range secrets[index].Value {
			secrets[index].Value[valueIndex] = 0
		}
		secrets[index].Value = nil
	}
}

func wipeEnvironmentValues(values [][]byte) {
	for _, value := range values {
		for index := range value {
			value[index] = 0
		}
	}
}

func clearEnvironmentStrings(values []string) {
	for index := range values {
		values[index] = ""
	}
}

func (w *lineCapture) appendCaptured(data []byte) {
	if len(data) >= maximumCapturedBytes {
		w.captured.Reset()
		_, _ = w.captured.Write(data[len(data)-maximumCapturedBytes:])
		w.truncated = true
		return
	}
	if w.captured.Len()+len(data) > maximumCapturedBytes {
		excess := w.captured.Len() + len(data) - maximumCapturedBytes
		retained := append([]byte(nil), w.captured.Bytes()[excess:]...)
		w.captured.Reset()
		_, _ = w.captured.Write(retained)
		w.truncated = true
	}
	_, _ = w.captured.Write(data)
}

func (w *lineCapture) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() > 0 {
		line := append([]byte(nil), w.pending.Bytes()...)
		w.pending.Reset()
		w.emit(line)
	}
}

func (w *lineCapture) emit(line []byte) {
	if len(line) == 0 {
		return
	}
	event := OutputEvent{
		Attempt: w.attempt,
		Stream:  w.stream,
		Data:    string(line),
		At:      time.Now(),
	}
	trimmed := bytes.TrimSpace(line)
	if json.Valid(trimmed) {
		event.JSON = append(json.RawMessage(nil), trimmed...)
	}
	w.dispatcher.emit(event)
}

func (w *lineCapture) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		return "[output truncated; showing the most recent 10 MiB]\n" + w.captured.String()
	}
	return w.captured.String()
}

var _ io.Writer = (*lineCapture)(nil)
