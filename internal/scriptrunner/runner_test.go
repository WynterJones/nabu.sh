package scriptrunner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestRunStructuredResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	root := t.TempDir()
	runs := t.TempDir()
	path := filepath.Join(root, "check.sh")
	content := "#!/bin/sh\nprintf '%s\\n' 'checking' '{\"status\":\"attention\",\"summary\":\"Latency increased\",\"interesting\":true}'\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	execution, err := New().Run(context.Background(), Request{
		RunID: "run-1", ScriptsRoot: root, RunsRoot: runs,
		Script: domain.Script{ID: "script-1", Path: "check.sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Run.Status != domain.ScriptRunCompleted || execution.Run.Result == nil || !execution.Run.Result.Interesting {
		t.Fatalf("unexpected run: %#v", execution.Run)
	}
	if execution.Run.Result.Summary != "Latency increased" {
		t.Fatalf("unexpected summary: %q", execution.Run.Result.Summary)
	}
	if _, err := os.Stat(execution.Run.StdoutPath); err != nil {
		t.Fatalf("stdout log missing: %v", err)
	}
}

func TestRejectsScriptOutsideRegistry(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := resolveScript(root, outside)
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected registry boundary error, got %v", err)
	}
}

func TestTimeoutTerminatesScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	root := t.TempDir()
	path := filepath.Join(root, "slow.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	execution, err := New().Run(ctx, Request{
		RunID: "run-timeout", ScriptsRoot: root, RunsRoot: t.TempDir(),
		Script: domain.Script{ID: "script-timeout", Path: path},
	})
	if err == nil || execution.Run.Status != domain.ScriptRunTimedOut {
		t.Fatalf("expected timed out run, got status=%s error=%v", execution.Run.Status, err)
	}
}

func TestSecretEnvironmentIsInjectedRedactedAndDestroyed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	root := t.TempDir()
	path := filepath.Join(root, "secret.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$API_TOKEN\" >&2\nprintf '%s\\n' \"$API_TOKEN\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := []byte("super-private-token")
	execution, err := New().Run(context.Background(), Request{
		RunID: "run-secret", ScriptsRoot: root, RunsRoot: t.TempDir(),
		Script:  domain.Script{ID: "script-secret", Path: path},
		Secrets: []EnvironmentSecret{{Name: "API_TOKEN", Value: secret}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(execution.Stdout, "super-private-token") || strings.Contains(execution.Stderr, "super-private-token") ||
		!strings.Contains(execution.Stdout, "[REDACTED]") || !strings.Contains(execution.Stderr, "[REDACTED]") {
		t.Fatalf("execution output was not redacted: stdout=%q stderr=%q", execution.Stdout, execution.Stderr)
	}
	for _, logPath := range []string{execution.Run.StdoutPath, execution.Run.StderrPath} {
		content, readErr := os.ReadFile(logPath)
		if readErr != nil || strings.Contains(string(content), "super-private-token") || !strings.Contains(string(content), "[REDACTED]") {
			t.Fatalf("log %s leaked secret: %q, %v", logPath, content, readErr)
		}
	}
	for _, value := range secret {
		if value != 0 {
			t.Fatalf("caller secret buffer was not destroyed: %q", secret)
		}
	}
}

func TestRegisteredScriptCanRunFromAuthorizedWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	scriptsRoot := t.TempDir()
	workspace := t.TempDir()
	path := filepath.Join(scriptsRoot, "workspace.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	execution, err := New().Run(context.Background(), Request{
		RunID: "run-workspace", ScriptsRoot: scriptsRoot, WorkingDirectory: workspace, RunsRoot: t.TempDir(),
		Script: domain.Script{ID: "script-workspace", Path: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(execution.Stdout) != expected {
		t.Fatalf("script working directory = %q, want %q", execution.Stdout, expected)
	}
}

func TestSecretEnvironmentRejectsReservedAndLoaderNames(t *testing.T) {
	for _, name := range []string{"PATH", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "bad-name", "lowercase"} {
		t.Run(name, func(t *testing.T) {
			secret := []byte("secret-value")
			_, _, err := prepareSecrets([]EnvironmentSecret{{Name: name, Value: secret}})
			if err == nil {
				t.Fatal("unsafe environment name accepted")
			}
		})
	}
}

func TestFailedScriptCannotLeakSecretThroughOutputOrError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	root := t.TempDir()
	path := filepath.Join(root, "failed-secret.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$API_TOKEN\" >&2\nprintf '%s\\n' \"$API_TOKEN\"\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	secretText := "failure-secret-token"
	execution, err := New().Run(context.Background(), Request{
		RunID: "run-failed-secret", ScriptsRoot: root, RunsRoot: t.TempDir(),
		Script:  domain.Script{ID: "script-failed-secret", Path: path},
		Secrets: []EnvironmentSecret{{Name: "API_TOKEN", Value: []byte(secretText)}},
	})
	if err == nil || strings.Contains(err.Error(), secretText) || strings.Contains(execution.Run.Error, secretText) {
		t.Fatalf("failure leaked secret or returned no error: run=%q error=%v", execution.Run.Error, err)
	}
	if strings.Contains(execution.Stdout, secretText) || strings.Contains(execution.Stderr, secretText) ||
		!strings.Contains(execution.Stdout, "[REDACTED]") || !strings.Contains(execution.Stderr, "[REDACTED]") {
		t.Fatalf("failed execution output was not redacted: stdout=%q stderr=%q", execution.Stdout, execution.Stderr)
	}
	for _, logPath := range []string{execution.Run.StdoutPath, execution.Run.StderrPath} {
		content, readErr := os.ReadFile(logPath)
		if readErr != nil || strings.Contains(string(content), secretText) || !strings.Contains(string(content), "[REDACTED]") {
			t.Fatalf("failed log %s leaked secret: %q, %v", logPath, content, readErr)
		}
	}
}

func TestRedactionHandlesOverlappingSecretValues(t *testing.T) {
	values, environment, err := prepareSecrets([]EnvironmentSecret{
		{Name: "SHORT_TOKEN", Value: []byte("token")},
		{Name: "LONG_TOKEN", Value: []byte("token-with-sensitive-suffix")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer wipeValues(values)
	defer clearStrings(environment)
	redacted := string(redactOutput([]byte("token token-with-sensitive-suffix"), values))
	if redacted != "[REDACTED] [REDACTED]" {
		t.Fatalf("overlapping secrets were not fully redacted: %q", redacted)
	}
}
