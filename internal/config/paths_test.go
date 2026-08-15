package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrecedenceAndLayout(t *testing.T) {
	envRoot := filepath.Join(t.TempDir(), "from-env")
	t.Setenv(HomeEnv, envRoot)

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != envRoot {
		t.Fatalf("Root = %q, want %q", got.Root, envRoot)
	}
	if got.Skills != filepath.Join(envRoot, "workspace", "skills") {
		t.Fatalf("Skills = %q", got.Skills)
	}

	explicit := filepath.Join(t.TempDir(), "explicit")
	got, err = Resolve(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != explicit {
		t.Fatalf("explicit Root = %q, want %q", got.Root, explicit)
	}
}

func TestEnsureCreatesLayoutAndPreservesFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nabu")
	paths, err := Ensure(root)
	if err != nil {
		t.Fatal(err)
	}

	dirs := []string{paths.Workspace, paths.Artifacts, paths.Reports, paths.Runs, paths.Logs, paths.Backups, paths.Skills, paths.Scripts, paths.Memory, paths.Scopes}
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("stat %s: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}

	files := []string{paths.NABU, paths.Mission, paths.Business, paths.User, paths.Policy, paths.MemoryFile, paths.Soul}
	for _, path := range files {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
		}
		if len(contents) == 0 {
			t.Errorf("%s is empty", path)
		}
	}

	const custom = "# My mission\n\nKeep this.\n"
	if err := os.WriteFile(paths.Mission, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(root); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(paths.Mission)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != custom {
		t.Fatalf("Ensure overwrote MISSION.md: %q", contents)
	}
}

func TestEnsureSeedsExecutableScriptsWithoutOverwriting(t *testing.T) {
	paths, err := Ensure(filepath.Join(t.TempDir(), "nabu"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"site-health", "analytics-summary", "search-console"} {
		path := filepath.Join(paths.Scripts, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0o100 == 0 {
			t.Errorf("%s is not executable: %v", name, info.Mode())
		}
	}
	customPath := filepath.Join(paths.Scripts, "site-health")
	if err := os.WriteFile(customPath, []byte("custom\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(paths.Root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(customPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "custom\n" {
		t.Fatalf("Ensure overwrote example script: %q", got)
	}
}

func TestResolveRejectsMultipleOverrides(t *testing.T) {
	if _, err := Resolve("one", "two"); err == nil {
		t.Fatal("Resolve accepted multiple overrides")
	}
}
