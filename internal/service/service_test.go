package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteAtomicReplacesCompleteServiceFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nabu.service")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte("complete replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "complete replacement" {
		t.Fatalf("service content = %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Fatalf("service mode = %v", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".nabu-service-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary service files remain: %v", matches)
	}
}

func TestServiceEscaping(t *testing.T) {
	xml := xmlEscape(`<path a="b">&'`)
	for _, unsafe := range []string{"<", ">", `"`, "'"} {
		if strings.Contains(xml, unsafe) {
			t.Fatalf("XML escape %q still contains %q", xml, unsafe)
		}
	}
	systemd := systemdEscape(`C:\Nabu\"100%"`)
	if !strings.Contains(systemd, `\\`) || !strings.Contains(systemd, `\"`) || !strings.Contains(systemd, `%%`) {
		t.Fatalf("systemd escape = %q", systemd)
	}
}

func TestInstallRejectsUnsafeDaemonDefinition(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, ".nabu")
	if _, err := New(home, data, filepath.Join(home, "missing-nabud")).Install(); err == nil {
		t.Fatal("missing daemon binary unexpectedly installed")
	}
	notExecutable := filepath.Join(home, "nabud")
	if err := os.WriteFile(notExecutable, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if _, err := New(home, data, notExecutable).Install(); err == nil || !strings.Contains(err.Error(), "not executable") {
			t.Fatalf("non-executable daemon install error = %v", err)
		}
	}
}

func TestIgnoreNotRunningRecognizesLaunchdMissingProcess(t *testing.T) {
	if err := ignoreNotRunning(os.ErrNotExist); err != nil {
		t.Fatal(err)
	}
	if err := ignoreNotRunning(&serviceTestError{"launchctl: Boot-out failed: 3: No such process"}); err != nil {
		t.Fatal(err)
	}
}

type serviceTestError struct{ message string }

func (e *serviceTestError) Error() string { return e.message }
