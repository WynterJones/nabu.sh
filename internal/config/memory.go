package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxMemoryEntryBytes = 16 << 10
	maxMemoryFileBytes  = 256 << 10
)

var memoryMu sync.Mutex

// DailyMemoryPath returns memory/YYYY-MM-DD.md using the calendar date in the
// supplied time's location.
func (p Paths) DailyMemoryPath(date time.Time) string {
	return filepath.Join(p.Memory, date.Format("2006-01-02")+".md")
}

func (p WorkspaceContextPaths) DailyMemoryPath(date time.Time) string {
	return filepath.Join(p.Memory, date.Format("2006-01-02")+".md")
}

func AppendScopeDailyMemory(paths WorkspaceContextPaths, date time.Time, note string) (string, error) {
	return AppendDailyMemory(Paths{Memory: paths.Memory}, date, note)
}

func AppendScopeMemory(paths WorkspaceContextPaths, note string) error {
	return AppendMemory(Paths{MemoryFile: paths.MemoryFile}, note)
}

func UpdateScopeMemory(paths WorkspaceContextPaths, content string) error {
	return UpdateMemory(Paths{MemoryFile: paths.MemoryFile}, content)
}

// AppendSoul adds one bounded reflection to the global, user-visible SOUL.md
// character charter. Soul is advisory context and never an authority source.
func AppendSoul(paths Paths, note string) error {
	entry, err := memoryEntry(note)
	if err != nil {
		return err
	}
	memoryMu.Lock()
	defer memoryMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(paths.Soul), 0o700); err != nil {
		return fmt.Errorf("config: create soul parent: %w", err)
	}
	if err := createFile(paths.Soul, defaultSoul); err != nil {
		return err
	}
	return appendFile(paths.Soul, entry)
}

// UpdateSoul atomically replaces SOUL.md while preserving its explicit title.
func UpdateSoul(paths Paths, content string) error {
	return updateContextDocument(paths.Soul, "# Soul", content)
}

// EnsureDailyMemory creates a daily memory file without replacing an existing
// one and returns its path.
func EnsureDailyMemory(paths Paths, date time.Time) (string, error) {
	path := paths.DailyMemoryPath(date)
	if err := os.MkdirAll(paths.Memory, 0o700); err != nil {
		return "", fmt.Errorf("config: create memory directory: %w", err)
	}
	header := "# " + date.Format("2006-01-02") + "\n\n"
	if err := createFile(path, header); err != nil {
		return "", err
	}
	return path, nil
}

// AppendDailyMemory appends one bounded operational note to the day's
// human-readable memory projection.
func AppendDailyMemory(paths Paths, date time.Time, note string) (string, error) {
	entry, err := memoryEntry(note)
	if err != nil {
		return "", err
	}
	memoryMu.Lock()
	defer memoryMu.Unlock()
	path, err := EnsureDailyMemory(paths, date)
	if err != nil {
		return "", err
	}
	if err := appendFile(path, entry); err != nil {
		return "", err
	}
	return path, nil
}

// AppendMemory appends one bounded durable note to MEMORY.md. SQLite remains
// authoritative for operational state; this file is curated context only.
func AppendMemory(paths Paths, note string) error {
	entry, err := memoryEntry(note)
	if err != nil {
		return err
	}
	memoryMu.Lock()
	defer memoryMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(paths.MemoryFile), 0o700); err != nil {
		return fmt.Errorf("config: create memory parent: %w", err)
	}
	if err := createFile(paths.MemoryFile, defaultMemory); err != nil {
		return err
	}
	return appendFile(paths.MemoryFile, entry)
}

// UpdateMemory atomically replaces the curated MEMORY.md projection. It is
// deliberately size-bounded so operational history belongs in SQLite rather
// than growing an unbounded prompt file.
func UpdateMemory(paths Paths, content string) error {
	return updateContextDocument(paths.MemoryFile, "# Memory", content)
}

func updateContextDocument(path, heading, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("config: memory content is empty")
	}
	if strings.IndexByte(content, 0) >= 0 {
		return errors.New("config: memory content contains a NUL byte")
	}
	if len(content) > maxMemoryFileBytes {
		return fmt.Errorf("config: memory content exceeds %d bytes", maxMemoryFileBytes)
	}
	if !strings.HasPrefix(content, heading) {
		content = heading + "\n\n" + content
	}
	content += "\n"

	memoryMu.Lock()
	defer memoryMu.Unlock()
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("config: create memory parent: %w", err)
	}
	temp, err := os.CreateTemp(parent, ".nabu-context-*")
	if err != nil {
		return fmt.Errorf("config: create temporary memory: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("config: protect temporary memory: %w", err)
	}
	if _, err := temp.WriteString(content); err != nil {
		cleanup()
		return fmt.Errorf("config: write temporary memory: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("config: sync temporary memory: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("config: close temporary memory: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("config: replace memory: %w", err)
	}
	return nil
}

func memoryEntry(note string) (string, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return "", errors.New("config: memory note is empty")
	}
	if strings.IndexByte(note, 0) >= 0 {
		return "", errors.New("config: memory note contains a NUL byte")
	}
	if len(note) > maxMemoryEntryBytes {
		return "", fmt.Errorf("config: memory note exceeds %d bytes", maxMemoryEntryBytes)
	}
	note = strings.ReplaceAll(note, "\n", "\n  ")
	return "- " + note + "\n", nil
}

func appendFile(path, entry string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("config: open %q for append: %w", path, err)
	}
	if _, err := f.WriteString(entry); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: append %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config: close %q: %w", path, err)
	}
	return nil
}
