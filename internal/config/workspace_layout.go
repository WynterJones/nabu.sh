package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var workspaceLayoutDirectories = []string{
	"inbox",
	"documents",
	"media",
	"research",
	"data",
	"repos",
	"reports",
	"deliverables",
	"archive",
}

// WorkspaceLayoutDirectories returns the standard directories for a
// Nabu-created business workspace. The returned slice is independent and may
// be modified by the caller.
func WorkspaceLayoutDirectories() []string {
	return append([]string(nil), workspaceLayoutDirectories...)
}

// EnsureWorkspaceLayout creates the standard layout under a new
// Nabu-managed workspace. It must not be used to connect an established repo.
// Existing directories and files outside the named layout are untouched.
func EnsureWorkspaceLayout(root string) error {
	if strings.TrimSpace(root) == "" || strings.IndexByte(root, 0) >= 0 {
		return errors.New("config: workspace root is empty or invalid")
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("config: workspace root %q is not absolute", root)
	}
	root = filepath.Clean(root)
	volume := filepath.VolumeName(root)
	if root == string(filepath.Separator) || root == volume+string(filepath.Separator) {
		return fmt.Errorf("config: unsafe workspace root %q", root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("config: create workspace root %q: %w", root, err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("config: canonicalize workspace root %q: %w", root, err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return fmt.Errorf("config: resolve workspace root %q: %w", root, err)
	}
	canonical = filepath.Clean(canonical)
	if canonical == string(filepath.Separator) || canonical == filepath.VolumeName(canonical)+string(filepath.Separator) {
		return fmt.Errorf("config: unsafe canonical workspace root %q", canonical)
	}
	for _, name := range workspaceLayoutDirectories {
		directory := filepath.Join(canonical, name)
		if filepath.Dir(directory) != canonical {
			return fmt.Errorf("config: unsafe workspace directory %q", name)
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("config: create workspace directory %q: %w", directory, err)
		}
	}
	return nil
}
