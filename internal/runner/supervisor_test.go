package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestDefaultConfigAllowsNetworkInsideWorkspaceSandbox(t *testing.T) {
	config := DefaultConfig()
	joined := strings.Join(config.Args, " ")
	if !strings.Contains(joined, "--sandbox workspace-write") {
		t.Fatalf("DefaultConfig sandbox args = %q", joined)
	}
	if !strings.Contains(joined, "sandbox_workspace_write.network_access=true") {
		t.Fatalf("DefaultConfig does not enable workspace-scoped network access: %q", joined)
	}
}

func TestSupervisorCapturesProcessAndStreamsOutput(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	executable := fakeExecutable(t, `
printf 'cwd=%s arg=%s\n' "$PWD" "$1"
IFS= read -r prompt
printf '{"type":"answer","prompt":"%s"}\n' "$prompt"
printf 'diagnostic\n' >&2
`)

	var mu sync.Mutex
	var events []OutputEvent
	var started ProcessStarted
	supervisor := NewSupervisor(Config{RetryDelay: -1})
	result, err := supervisor.Run(context.Background(), Request{
		WorkingDirectory: directory,
		Prompt:           "do-the-task\n",
		Command:          executable,
		Args:             []string{"injected"},
		MaxAttempts:      1,
		OnStart: func(event ProcessStarted) {
			started = event
		},
		OnOutput: func(event OutputEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != domain.RunCompleted {
		t.Fatalf("status = %q, want %q", result.Status, domain.RunCompleted)
	}
	if result.PID <= 0 {
		t.Fatalf("PID = %d, want positive", result.PID)
	}
	if started.PID != result.PID || started.Attempt != 1 || started.WorkingDirectory != directory {
		t.Fatalf("start event = %#v, result PID = %d", started, result.PID)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", result.ExitCode)
	}
	if result.StartedAt.IsZero() || result.EndedAt.Before(result.StartedAt) {
		t.Fatalf("invalid timestamps: %v to %v", result.StartedAt, result.EndedAt)
	}
	if result.WorkingDirectory != directory {
		t.Fatalf("working directory = %q, want %q", result.WorkingDirectory, directory)
	}
	if !strings.Contains(result.Stdout, " arg=injected") || !strings.Contains(result.Stdout, filepath.Base(directory)) || !strings.Contains(result.Stdout, `"prompt":"do-the-task"`) {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if result.Stderr != "diagnostic\n" {
		t.Fatalf("stderr = %q", result.Stderr)
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(result.Attempts))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3: %#v", len(events), events)
	}
	var foundJSON bool
	for _, event := range events {
		if event.Attempt != 1 || event.At.IsZero() {
			t.Fatalf("invalid event metadata: %#v", event)
		}
		if event.Stream == OutputStdout && len(event.JSON) > 0 {
			var value map[string]any
			if err := json.Unmarshal(event.JSON, &value); err != nil {
				t.Fatalf("event JSON = %s: %v", event.JSON, err)
			}
			foundJSON = true
		}
	}
	if !foundJSON {
		t.Fatal("no structured stdout event received")
	}
}

func TestSupervisorInjectsRedactsAndDestroysSecretEnvironment(t *testing.T) {
	directory := t.TempDir()
	executable := fakeExecutable(t, `printf '%s\n' "$MCP_TOKEN"; printf '%s\n' "$MCP_TOKEN" >&2`)
	value := []byte("mcp-secret-value")
	result, err := NewSupervisor(Config{RetryDelay: -1}).Run(context.Background(), Request{
		WorkingDirectory: directory, Command: executable, Args: []string{},
		SecretEnvironment: []EnvironmentSecret{{Name: "MCP_TOKEN", Value: value}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Stdout, "mcp-secret-value") || strings.Contains(result.Stderr, "mcp-secret-value") ||
		!strings.Contains(result.Stdout, "[REDACTED]") || !strings.Contains(result.Stderr, "[REDACTED]") {
		t.Fatalf("secret output was not redacted: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
	for _, item := range value {
		if item != 0 {
			t.Fatalf("caller secret buffer retained value: %q", value)
		}
	}
}

func TestLineCaptureBoundsUnterminatedOutput(t *testing.T) {
	capture := newLineCapture(1, OutputStdout, &callbackDispatcher{})
	chunk := strings.Repeat("x", maximumCapturedBytes+maximumPendingBytes+1024)
	if _, err := capture.Write([]byte(chunk)); err != nil {
		t.Fatal(err)
	}
	if capture.captured.Len() > maximumCapturedBytes {
		t.Fatalf("captured output exceeded limit: %d", capture.captured.Len())
	}
	if capture.pending.Len() > maximumPendingBytes {
		t.Fatalf("pending line exceeded limit: %d", capture.pending.Len())
	}
	if !strings.HasPrefix(capture.String(), "[output truncated") {
		t.Fatal("bounded output did not disclose truncation")
	}
}

func TestSupervisorRetriesOneTransientFailure(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	statePath := filepath.Join(directory, "attempt")
	executable := fakeExecutable(t, `
if [ ! -f "$STATE_FILE" ]; then
  printf 'first' > "$STATE_FILE"
  printf 'service unavailable; try again\n' >&2
  exit 1
fi
printf '{"status":"completed","summary":"recovered"}\n'
`)
	supervisor := NewSupervisor(Config{RetryDelay: -1})
	result, err := supervisor.Run(context.Background(), Request{
		WorkingDirectory: directory,
		Command:          executable,
		Args:             []string{},
		Environment:      []string{"STATE_FILE=" + statePath},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Attempt != 2 || len(result.Attempts) != 2 {
		t.Fatalf("attempts = %#v, want two", result.Attempts)
	}
	if result.Attempts[0].Status != domain.RunFailed || result.Attempts[1].Status != domain.RunCompleted {
		t.Fatalf("attempt statuses = %q, %q", result.Attempts[0].Status, result.Attempts[1].Status)
	}
	if result.Attempts[0].PID == result.Attempts[1].PID {
		t.Fatalf("retry reused PID %d", result.Attempts[0].PID)
	}
	if strings.Contains(result.Stdout, "service unavailable") || !strings.Contains(result.Stdout, "recovered") {
		t.Fatalf("top-level output should be final attempt only: %q", result.Stdout)
	}
}

func TestSupervisorDoesNotRetryArbitraryFailure(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	executable := fakeExecutable(t, `
printf 'task validation failed\n' >&2
exit 1
`)
	supervisor := NewSupervisor(Config{RetryDelay: -1})
	result, err := supervisor.Run(context.Background(), Request{
		WorkingDirectory: directory,
		Command:          executable,
		Args:             []string{},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want failure")
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(result.Attempts))
	}
}

func TestSupervisorClassifiesChromiumSandboxDenialAsActionable(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	executable := fakeExecutable(t, `
printf 'Playwright Chromium bootstrap_check_in denied by sandbox\n' >&2
exit 1
`)
	result, err := NewSupervisor(Config{RetryDelay: -1}).Run(context.Background(), Request{
		WorkingDirectory: directory, Command: executable, Args: []string{},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want actionable failure")
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("sandbox denial was retried: %#v", result.Attempts)
	}
	for _, expected := range []string{"browser QA needs attention", "registered browser verifier", "did not broaden"} {
		if !strings.Contains(result.Error, expected) || !strings.Contains(err.Error(), expected) {
			t.Fatalf("actionable error missing %q: result=%q err=%v", expected, result.Error, err)
		}
	}
}

func TestSupervisorTimeoutTerminatesProcessGroup(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	survivorPath := filepath.Join(directory, "survived")
	executable := fakeExecutable(t, `
(
  sleep 1
  printf 'survived' > "$SURVIVOR_FILE"
) &
wait
`)
	supervisor := NewSupervisor(Config{
		TerminationGrace: 40 * time.Millisecond,
		RetryDelay:       -1,
	})
	result, err := supervisor.Run(context.Background(), Request{
		WorkingDirectory: directory,
		Command:          executable,
		Args:             []string{},
		Environment:      []string{"SURVIVOR_FILE=" + survivorPath},
		Timeout:          60 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
	if result.Status != domain.RunTimedOut {
		t.Fatalf("status = %q, want %q", result.Status, domain.RunTimedOut)
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(result.Attempts))
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(survivorPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived process-group termination: stat error = %v", err)
	}
}

func TestSupervisorCancellation(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	executable := fakeExecutable(t, "sleep 30\n")
	supervisor := NewSupervisor(Config{TerminationGrace: 40 * time.Millisecond, RetryDelay: -1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := supervisor.Run(ctx, Request{
		WorkingDirectory: directory,
		Command:          executable,
		Args:             []string{},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want canceled", err)
	}
	if result.Status != domain.RunCancelled {
		t.Fatalf("status = %q, want %q", result.Status, domain.RunCancelled)
	}
}

func TestSupervisorRequiresExplicitWorkingDirectory(t *testing.T) {
	t.Parallel()
	_, err := NewSupervisor(Config{}).Run(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "working directory is required") {
		t.Fatalf("Run() error = %v", err)
	}
}

func fakeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	return path
}
