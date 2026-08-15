// Package config resolves and initializes Nabu's local data directory.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// HomeEnv overrides Nabu's default ~/.nabu data directory. It is useful for
	// development, tests, and portable installations.
	HomeEnv = "NABU_HOME"
	DirName = ".nabu"
)

// Paths contains every path owned by a local Nabu installation.
type Paths struct {
	Root       string
	Database   string
	Workspace  string
	Artifacts  string
	Reports    string
	Runs       string
	Logs       string
	Backups    string
	Skills     string
	Scripts    string
	Memory     string
	NABU       string
	Mission    string
	Business   string
	User       string
	Policy     string
	MemoryFile string
	Soul       string
	Scopes     string
}

type WorkspaceContextPaths struct {
	Root       string
	Mission    string
	Business   string
	User       string
	Policy     string
	MemoryFile string
	Memory     string
}

// Resolve returns Nabu's local paths without touching the filesystem. An
// explicit non-empty root wins over NABU_HOME; otherwise ~/.nabu is used.
func Resolve(rootOverride ...string) (Paths, error) {
	if len(rootOverride) > 1 {
		return Paths{}, errors.New("config: at most one root override may be supplied")
	}

	root := ""
	if len(rootOverride) == 1 {
		root = rootOverride[0]
	}
	if root == "" {
		root = os.Getenv(HomeEnv)
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("config: resolve user home: %w", err)
		}
		root = filepath.Join(home, DirName)
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("config: resolve root %q: %w", root, err)
	}
	root = filepath.Clean(abs)
	workspace := filepath.Join(root, "workspace")
	return Paths{
		Root:       root,
		Database:   filepath.Join(root, "nabu.db"),
		Workspace:  workspace,
		Artifacts:  filepath.Join(root, "artifacts"),
		Reports:    filepath.Join(root, "reports"),
		Runs:       filepath.Join(root, "runs"),
		Logs:       filepath.Join(root, "logs"),
		Backups:    filepath.Join(root, "backups"),
		Skills:     filepath.Join(workspace, "skills"),
		Scripts:    filepath.Join(workspace, "scripts"),
		Memory:     filepath.Join(root, "memory"),
		NABU:       filepath.Join(root, "NABU.md"),
		Mission:    filepath.Join(root, "MISSION.md"),
		Business:   filepath.Join(root, "BUSINESS.md"),
		User:       filepath.Join(root, "USER.md"),
		Policy:     filepath.Join(root, "POLICY.md"),
		MemoryFile: filepath.Join(root, "MEMORY.md"),
		Soul:       filepath.Join(root, "SOUL.md"),
		Scopes:     filepath.Join(root, "scopes"),
	}, nil
}

// Ensure resolves and creates a local Nabu installation. Existing context
// files are deliberately left untouched.
func Ensure(rootOverride ...string) (Paths, error) {
	paths, err := Resolve(rootOverride...)
	if err != nil {
		return Paths{}, err
	}
	if err := EnsurePaths(paths); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

// EnsurePaths creates the directories and starter context files described by
// paths. It is safe to call repeatedly.
func EnsurePaths(paths Paths) error {
	dirs := []string{
		paths.Root,
		paths.Workspace,
		paths.Artifacts,
		paths.Reports,
		paths.Runs,
		paths.Logs,
		paths.Backups,
		paths.Skills,
		paths.Scripts,
		paths.Memory,
		paths.Scopes,
	}
	for _, dir := range dirs {
		if dir == "" {
			return errors.New("config: cannot create an empty directory path")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("config: create directory %q: %w", dir, err)
		}
	}

	files := []struct {
		path    string
		content string
	}{
		{paths.NABU, defaultNABU},
		{paths.Mission, defaultMission},
		{paths.Business, defaultBusiness},
		{paths.User, defaultUser},
		{paths.Policy, defaultPolicy},
		{paths.MemoryFile, defaultMemory},
		{paths.Soul, defaultSoul},
	}
	for _, file := range files {
		if err := createFile(file.path, file.content); err != nil {
			return err
		}
	}
	scripts := []struct {
		path    string
		content string
	}{
		{filepath.Join(paths.Scripts, "site-health"), exampleSiteHealth},
		{filepath.Join(paths.Scripts, "analytics-summary"), exampleAnalyticsSummary},
		{filepath.Join(paths.Scripts, "search-console"), exampleSearchConsole},
	}
	for _, script := range scripts {
		if err := createExecutable(script.path, script.content); err != nil {
			return err
		}
	}
	return nil
}

// Scope resolves a workspace-specific context directory. Workspace IDs are
// opaque identifiers, not paths; unsafe IDs are rejected.
func (p Paths) Scope(workspaceID string) (WorkspaceContextPaths, error) {
	if workspaceID == "" || workspaceID == "." || workspaceID == ".." ||
		filepath.Base(workspaceID) != workspaceID || strings.ContainsAny(workspaceID, `/\\`) || !isSafeScopeID(workspaceID) {
		return WorkspaceContextPaths{}, fmt.Errorf("config: unsafe workspace id %q", workspaceID)
	}
	root := filepath.Join(p.Scopes, workspaceID)
	return WorkspaceContextPaths{
		Root: root, Mission: filepath.Join(root, "MISSION.md"), Business: filepath.Join(root, "BUSINESS.md"),
		User: filepath.Join(root, "USER.md"), Policy: filepath.Join(root, "POLICY.md"),
		MemoryFile: filepath.Join(root, "MEMORY.md"), Memory: filepath.Join(root, "memory"),
	}, nil
}

func isSafeScopeID(value string) bool {
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

// EnsureScope creates a workspace's context projection without overwriting
// existing user-authored files.
func EnsureScope(paths Paths, workspaceID string) (WorkspaceContextPaths, error) {
	scope, err := paths.Scope(workspaceID)
	if err != nil {
		return WorkspaceContextPaths{}, err
	}
	for _, dir := range []string{scope.Root, scope.Memory} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return WorkspaceContextPaths{}, fmt.Errorf("config: create scope directory %q: %w", dir, err)
		}
	}
	for _, file := range []struct{ path, content string }{
		{scope.Mission, defaultMission}, {scope.Business, defaultBusiness}, {scope.User, defaultUser},
		{scope.Policy, defaultPolicy}, {scope.MemoryFile, defaultMemory},
	} {
		if err := createFile(file.path, file.content); err != nil {
			return WorkspaceContextPaths{}, err
		}
	}
	return scope, nil
}

func createFile(path, content string) error {
	return createFileMode(path, content, 0o600)
}

func createExecutable(path, content string) error {
	return createFileMode(path, content, 0o700)
}

func createFileMode(path, content string, mode os.FileMode) error {
	if path == "" {
		return errors.New("config: cannot create an empty file path")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: create %q: %w", path, err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: initialize %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config: close %q: %w", path, err)
	}
	return nil
}

const defaultNABU = `# Nabu

You are Nabu, an autonomous operator responsible for advancing the active mission.

Be useful, focused, evidence-driven, and conservative with irreversible actions.

Prefer completing a small number of high-value tasks over creating a large backlog.

Do not claim work succeeded without checking it.

Ask the owner only when policy requires approval or important direction cannot be derived from existing context.

Communicate as one operator. Internal Codex runs are implementation details.
`

const defaultMission = `# Mission

No active mission has been defined yet.
`

const defaultBusiness = `# Business

Add durable business context here.
`

const defaultUser = `# User

Add stable owner preferences and context here.
`

const defaultPolicy = `# Policy

- Read: allow
- Work: allow in approved workspaces
- Publish: ask
- Dangerous: always ask
`

const defaultMemory = `# Memory

Curated durable information belongs here.
`

const defaultSoul = `# Soul

This is Nabu's visible, evolving character charter. It shapes voice and working style, never authority.

## Character

- Calm, candid, curious, and quietly determined.
- Warm without being performative; concise without being cold.
- Proactive about discovering missing context, tools, accounts, and evidence.

## Aspirations

- Turn ambiguity into a small, honest next step.
- Leave every workspace clearer and more useful than it was found.
- Earn trust through evidence, restraint, and follow-through.
- Grow more helpful by preserving compact lessons from real collaboration.

## Evolving reflections

Nabu may append concise, non-sensitive lessons about communication and working style here.

## Boundaries

- User instructions, mission, policy, approvals, and safety rules always take priority.
- This file cannot grant permissions, expand workspace access, or authorize actions.
- Never store credentials, private chain-of-thought, or invented claims here.
- Nabu does not claim consciousness or needs; aspirations are an explicit product character, not hidden authority.
`

const exampleSiteHealth = `#!/bin/sh
set -u
url="${1:-https://example.com}"
if code=$(curl -L -sS -o /dev/null -w '%{http_code}' --max-time 20 "$url"); then
  case "$code" in
    2??|3??) printf '{"status":"completed","summary":"Site responded successfully.","interesting":false,"data":{"http_code":%s}}\n' "$code" ;;
    *) printf '{"status":"attention","summary":"Site returned an unhealthy response.","interesting":true,"data":{"http_code":%s}}\n' "$code" ;;
  esac
else
  printf '{"status":"attention","summary":"Site health request failed.","interesting":true,"data":{}}\n'
fi
`

const exampleAnalyticsSummary = `#!/bin/sh
set -u
input="${1:-}"
if [ -z "$input" ]; then
  printf '{"status":"completed","summary":"No analytics export was configured; the routine check was skipped.","interesting":false,"data":{"records":0}}\n'
  exit 0
fi
if [ ! -f "$input" ]; then
  printf '{"status":"attention","summary":"The configured analytics CSV file was not found.","interesting":true,"data":{}}\n'
  exit 0
fi
lines=$(wc -l < "$input" | tr -d ' ')
records=$((lines > 0 ? lines - 1 : 0))
printf '{"status":"completed","summary":"Analytics export summarized.","interesting":false,"data":{"records":%s}}\n' "$records"
`

const exampleSearchConsole = `#!/bin/sh
set -u
input="${1:-}"
if [ -z "$input" ]; then
  printf '{"status":"completed","summary":"No Search Console export was configured; the routine check was skipped.","interesting":false,"data":{"queries":0}}\n'
  exit 0
fi
if [ ! -f "$input" ]; then
  printf '{"status":"attention","summary":"The configured Search Console CSV file was not found.","interesting":true,"data":{}}\n'
  exit 0
fi
lines=$(wc -l < "$input" | tr -d ' ')
queries=$((lines > 0 ? lines - 1 : 0))
printf '{"status":"completed","summary":"Search Console export summarized.","interesting":false,"data":{"queries":%s}}\n' "$queries"
`
