package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryHelpersAndWorkspaceScopes(t *testing.T) {
	paths, err := Ensure(filepath.Join(t.TempDir(), "nabu"))
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 8, 12, 12, 0, 0, 0, time.FixedZone("Toronto", -4*60*60))
	dailyPath, err := AppendDailyMemory(paths, date, "Completed the audit.\nTests passed.")
	if err != nil {
		t.Fatal(err)
	}
	if dailyPath != filepath.Join(paths.Memory, "2026-08-12.md") {
		t.Fatalf("daily path = %q", dailyPath)
	}
	daily, err := os.ReadFile(dailyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(daily), "- Completed the audit.\n  Tests passed.") {
		t.Fatalf("daily memory = %q", daily)
	}
	if err := AppendMemory(paths, "Stable preference"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMemory(paths, "One concise fact."); err != nil {
		t.Fatal(err)
	}
	memory, err := os.ReadFile(paths.MemoryFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(memory) != "# Memory\n\nOne concise fact.\n" {
		t.Fatalf("MEMORY.md = %q", memory)
	}
	if err := AppendSoul(paths, "Prefer a direct question when context is missing."); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSoul(paths, "Be thoughtful and concise."); err != nil {
		t.Fatal(err)
	}
	soul, err := os.ReadFile(paths.Soul)
	if err != nil {
		t.Fatal(err)
	}
	if string(soul) != "# Soul\n\nBe thoughtful and concise.\n" {
		t.Fatalf("SOUL.md = %q", soul)
	}

	scope, err := EnsureScope(paths, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{scope.Mission, scope.Business, scope.User, scope.Policy, scope.MemoryFile} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("scope file %s: %v", path, err)
		}
	}
	if _, err := paths.Scope("../escape"); err == nil {
		t.Fatal("Scope accepted unsafe workspace ID")
	}
	if err := os.WriteFile(scope.Mission, []byte("custom scope mission"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureScope(paths, "workspace-1"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(scope.Mission)
	if err != nil || string(got) != "custom scope mission" {
		t.Fatalf("scope mission overwritten: %q, %v", got, err)
	}
}
