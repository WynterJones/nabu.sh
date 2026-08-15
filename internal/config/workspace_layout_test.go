package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnsureWorkspaceLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "business")
	want := []string{"inbox", "documents", "media", "research", "data", "repos", "reports", "deliverables", "archive"}
	if got := WorkspaceLayoutDirectories(); !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceLayoutDirectories = %#v", got)
	}
	if err := EnsureWorkspaceLayout(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Errorf("stat %s: %v", name, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", name)
		}
	}
	marker := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceLayout(root); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("existing file changed: %q, %v", got, err)
	}
	got := WorkspaceLayoutDirectories()
	got[0] = "changed"
	if WorkspaceLayoutDirectories()[0] != "inbox" {
		t.Fatal("WorkspaceLayoutDirectories exposed mutable internal state")
	}
}

func TestEnsureWorkspaceLayoutRejectsUnsafeRoots(t *testing.T) {
	for _, root := range []string{"", "relative/path", string(filepath.Separator)} {
		if err := EnsureWorkspaceLayout(root); err == nil {
			t.Errorf("EnsureWorkspaceLayout(%q) succeeded", root)
		}
	}
}
