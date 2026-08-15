// Package scriptrunner executes deterministic local checks registered in
// Nabu's private script directory. It deliberately does not invoke Codex.
package scriptrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const (
	defaultTimeout  = 2 * time.Minute
	maximumTimeout  = 30 * time.Minute
	maximumLogBytes = 4 * 1024 * 1024
)

// Request identifies a registered script invocation and its durable log
// destination. Script paths may be absolute or relative to ScriptsRoot.
type Request struct {
	RunID       string
	ScheduleID  string
	Script      domain.Script
	ScriptsRoot string
	// WorkingDirectory is an already-authorized workspace selected by Nabu.
	// The executable must still resolve inside ScriptsRoot. Empty uses ScriptsRoot.
	WorkingDirectory string
	RunsRoot         string
	Environment      []string
	Secrets          []EnvironmentSecret
	OnStart          func(pid int, startedAt time.Time)
}

// EnvironmentSecret is a credential value owned by one invocation. Value is
// wiped immediately after the child is spawned (and on every earlier error).
// Name is never derived from the secret and must pass the environment allowlist.
type EnvironmentSecret struct {
	Name  string
	Value []byte
}

var environmentName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

var reservedEnvironment = map[string]struct{}{
	"BASHOPTS": {}, "BASH_ENV": {}, "CDPATH": {}, "ENV": {}, "GLOBIGNORE": {},
	"HOME": {}, "IFS": {}, "NODE_OPTIONS": {}, "OLDPWD": {}, "PATH": {},
	"PERL5LIB": {}, "PERL5OPT": {}, "PROMPT_COMMAND": {}, "PS4": {}, "PWD": {},
	"PYTHONHOME": {}, "PYTHONPATH": {}, "RUBYLIB": {}, "RUBYOPT": {}, "SHELL": {},
	"SHELLOPTS": {},
}

// Execution contains the normalized run plus bounded output for event
// publication. Complete stdout and stderr are written to the paths on Run.
type Execution struct {
	Run    domain.ScriptRun
	Stdout string
	Stderr string
}

// Runner supervises a platform script without a shell. This prevents shell
// interpolation and makes the registered file itself the security boundary.
type Runner struct{}

func New() *Runner { return &Runner{} }

// Run executes one registered script, captures durable logs, validates its
// optional JSON result, and terminates its process group on cancellation.
func (r *Runner) Run(ctx context.Context, request Request) (Execution, error) {
	defer destroySecrets(request.Secrets)
	if ctx == nil {
		return Execution{}, errors.New("script runner: nil context")
	}
	if strings.TrimSpace(request.RunID) == "" {
		return Execution{}, errors.New("script runner: run ID is required")
	}
	path, err := resolveScript(request.ScriptsRoot, request.Script.Path)
	if err != nil {
		return Execution{}, err
	}
	if strings.TrimSpace(request.RunsRoot) == "" {
		return Execution{}, errors.New("script runner: runs root is required")
	}
	runDirectory := filepath.Join(request.RunsRoot, "scripts", request.RunID)
	if err := os.MkdirAll(runDirectory, 0o700); err != nil {
		return Execution{}, fmt.Errorf("script runner: create run directory: %w", err)
	}

	startedAt := time.Now().UTC()
	run := domain.ScriptRun{
		ID:         request.RunID,
		ScriptID:   request.Script.ID,
		ScheduleID: request.ScheduleID,
		Status:     domain.ScriptRunRunning,
		StartedAt:  startedAt,
		StdoutPath: filepath.Join(runDirectory, "stdout.log"),
		StderrPath: filepath.Join(runDirectory, "stderr.log"),
	}
	timeout := time.Duration(request.Script.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > maximumTimeout {
		timeout = maximumTimeout
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	redactionValues, secretEnvironment, err := prepareSecrets(request.Secrets)
	if err != nil {
		return Execution{}, err
	}
	defer wipeValues(redactionValues)
	command := exec.Command(path)
	command.Dir, err = resolveWorkingDirectory(request.WorkingDirectory, request.ScriptsRoot)
	if err != nil {
		return Execution{}, err
	}
	command.Env = append(os.Environ(), request.Environment...)
	command.Env = append(command.Env, secretEnvironment...)
	defer clearStrings(secretEnvironment)
	configureProcessGroup(command)
	stdout := newBoundedBuffer(maximumLogBytes)
	stderr := newBoundedBuffer(maximumLogBytes)
	defer stdout.Destroy()
	defer stderr.Destroy()
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		command.Env = nil
		destroySecrets(request.Secrets)
		endedAt := time.Now().UTC()
		run.EndedAt = &endedAt
		run.Status = domain.ScriptRunFailed
		run.Error = err.Error()
		_ = writeLogs(run, stdout.Bytes(), stderr.Bytes())
		return Execution{Run: run, Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("script runner: start %q: %w", path, err)
	}
	// exec.Cmd has copied the environment into the spawned process. Drop all
	// parent references and wipe the mutable credential buffers immediately.
	command.Env = nil
	clearStrings(secretEnvironment)
	destroySecrets(request.Secrets)
	run.PID = command.Process.Pid
	if request.OnStart != nil {
		request.OnStart(run.PID, startedAt)
	}

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var runErr error
	select {
	case runErr = <-done:
	case <-runContext.Done():
		terminateProcessGroup(command)
		runErr = runContext.Err()
		<-done
	}
	endedAt := time.Now().UTC()
	run.EndedAt = &endedAt
	if command.ProcessState != nil {
		exitCode := command.ProcessState.ExitCode()
		run.ExitCode = &exitCode
	}
	redactedStdout := redactOutput(stdout.Bytes(), redactionValues)
	redactedStderr := redactOutput(stderr.Bytes(), redactionValues)
	// The raw capture may contain echoed credentials. Keep only the redacted
	// copy for persistence, result parsing, and the returned execution.
	stdout.Destroy()
	stderr.Destroy()
	if err := writeLogs(run, redactedStdout, redactedStderr); err != nil {
		run.Status = domain.ScriptRunFailed
		run.Error = err.Error()
		return Execution{Run: run, Stdout: string(redactedStdout), Stderr: string(redactedStderr)}, err
	}

	if runContext.Err() != nil {
		run.Error = runContext.Err().Error()
		if errors.Is(runContext.Err(), context.DeadlineExceeded) {
			run.Status = domain.ScriptRunTimedOut
		} else {
			run.Status = domain.ScriptRunCancelled
		}
		return Execution{Run: run, Stdout: string(redactedStdout), Stderr: string(redactedStderr)}, runContext.Err()
	}
	if runErr != nil {
		run.Status = domain.ScriptRunFailed
		run.Error = runErr.Error()
		return Execution{Run: run, Stdout: string(redactedStdout), Stderr: string(redactedStderr)}, fmt.Errorf("script runner: script failed with exit code %d", *run.ExitCode)
	}

	result, err := parseResult(redactedStdout)
	if err != nil {
		run.Status = domain.ScriptRunFailed
		run.Error = err.Error()
		return Execution{Run: run, Stdout: string(redactedStdout), Stderr: string(redactedStderr)}, err
	}
	run.Result = &result
	run.Status = domain.ScriptRunCompleted
	return Execution{Run: run, Stdout: string(redactedStdout), Stderr: string(redactedStderr)}, nil
}

func prepareSecrets(secrets []EnvironmentSecret) ([][]byte, []string, error) {
	values := make([][]byte, 0, len(secrets))
	environment := make([]string, 0, len(secrets))
	fail := func(err error) ([][]byte, []string, error) {
		wipeValues(values)
		clearStrings(environment)
		return nil, nil, err
	}
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		name := strings.TrimSpace(secret.Name)
		if !environmentName.MatchString(name) || strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") ||
			strings.HasPrefix(name, "BASH_FUNC_") || strings.HasPrefix(name, "GIT_CONFIG_") || strings.HasSuffix(name, "_ASKPASS") {
			return fail(fmt.Errorf("script runner: secret environment name %q is not allowlisted", name))
		}
		if _, denied := reservedEnvironment[name]; denied {
			return fail(fmt.Errorf("script runner: secret environment name %q is reserved", name))
		}
		if _, duplicate := seen[name]; duplicate {
			return fail(fmt.Errorf("script runner: duplicate secret environment name %q", name))
		}
		if len(secret.Value) == 0 || len(secret.Value) > 64*1024 || bytes.IndexByte(secret.Value, 0) >= 0 {
			return fail(fmt.Errorf("script runner: secret for %q is empty, oversized, or contains NUL", name))
		}
		seen[name] = struct{}{}
		value := append([]byte(nil), secret.Value...)
		values = append(values, value)
		environment = append(environment, name+"="+string(value))
	}
	// Replace longer values first so one credential that is a prefix of another
	// cannot leave the latter's suffix visible.
	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values, environment, nil
}

func redactOutput(output []byte, secrets [][]byte) []byte {
	redacted := append([]byte(nil), output...)
	for _, secret := range secrets {
		if len(secret) > 0 {
			redacted = bytes.ReplaceAll(redacted, secret, []byte("[REDACTED]"))
		}
	}
	return redacted
}

func wipeValues(values [][]byte) {
	for _, value := range values {
		for index := range value {
			value[index] = 0
		}
	}
}

func clearStrings(values []string) {
	for index := range values {
		values[index] = ""
	}
}

func destroySecrets(secrets []EnvironmentSecret) {
	for index := range secrets {
		for valueIndex := range secrets[index].Value {
			secrets[index].Value[valueIndex] = 0
		}
		secrets[index].Value = nil
	}
}

func resolveScript(root, value string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("script runner: scripts root is required")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("script runner: resolve scripts root: %w", err)
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootPath, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("script runner: resolve script: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", fmt.Errorf("script runner: resolve scripts root links: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("script runner: resolve script links: %w", err)
	}
	relative, err := filepath.Rel(realRoot, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("script runner: script %q is outside the registered scripts directory", value)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("script runner: inspect script: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("script runner: %q is not a regular file", value)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("script runner: %q is not executable", value)
	}
	return realPath, nil
}

func resolveWorkingDirectory(value, fallback string) (string, error) {
	if strings.TrimSpace(value) == "" {
		value = fallback
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("script runner: resolve working directory: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("script runner: resolve working directory links: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("script runner: inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("script runner: working directory is not a directory")
	}
	return path, nil
}

func parseResult(output []byte) (domain.ScriptResult, error) {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return domain.ScriptResult{Status: "completed", Summary: "Script completed successfully."}, nil
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	last := bytes.TrimSpace(lines[len(lines)-1])
	var result domain.ScriptResult
	if err := json.Unmarshal(last, &result); err != nil {
		return domain.ScriptResult{Status: "completed", Summary: string(last)}, nil
	}
	result.Status = strings.TrimSpace(strings.ToLower(result.Status))
	if result.Status == "" {
		result.Status = "completed"
	}
	if result.Status != "completed" && result.Status != "attention" {
		return domain.ScriptResult{}, fmt.Errorf("script runner: unsupported structured status %q", result.Status)
	}
	if strings.TrimSpace(result.Summary) == "" {
		return domain.ScriptResult{}, errors.New("script runner: structured result summary is required")
	}
	if result.Status == "attention" {
		result.Interesting = true
	}
	return result, nil
}

func writeLogs(run domain.ScriptRun, stdout, stderr []byte) error {
	if err := os.WriteFile(run.StdoutPath, stdout, 0o600); err != nil {
		return fmt.Errorf("script runner: write stdout: %w", err)
	}
	if err := os.WriteFile(run.StderrPath, stderr, 0o600); err != nil {
		return fmt.Errorf("script runner: write stderr: %w", err)
	}
	return nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func newBoundedBuffer(limit int) *boundedBuffer { return &boundedBuffer{limit: limit} }

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *boundedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedBuffer) String() string { return b.buffer.String() }

func (b *boundedBuffer) Destroy() {
	value := b.buffer.Bytes()
	for index := range value {
		value[index] = 0
	}
	b.buffer.Reset()
}

func configureProcessGroup(command *exec.Cmd) {
	if runtime.GOOS != "windows" {
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func terminateProcessGroup(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	if runtime.GOOS != "windows" {
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM); err == nil {
			time.Sleep(100 * time.Millisecond)
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			return
		}
	}
	_ = command.Process.Kill()
}
