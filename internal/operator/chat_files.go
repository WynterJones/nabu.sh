package operator

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/steering"
)

const (
	maximumChatWorkspaceFiles     = 3
	maximumChatWorkspaceFileBytes = 128 * 1024
)

var quotedChatPathPattern = regexp.MustCompile(`[\x60"']([^\x60"'\r\n]+)[\x60"']`)

// chatWorkspaceFiles supplies small, relevant text files as verified context.
// It never follows a symlink outside the active workspace and never persists
// file contents in conversation history. During setup a top-level PLAN.md is a
// conventional source of truth, so it is included even when referenced by a
// later shorthand such as "go ahead".
func chatWorkspaceFiles(workspace domain.Workspace, message string) []steering.WorkspaceFile {
	root, err := filepath.EvalSymlinks(workspace.Path)
	if err != nil {
		return []steering.WorkspaceFile{}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return []steering.WorkspaceFile{}
	}

	candidates := []string{}
	if !workspace.ContextReady || strings.Contains(strings.ToLower(message), "plan.md") {
		candidates = append(candidates, "PLAN.md")
	}
	for _, match := range quotedChatPathPattern.FindAllStringSubmatch(message, -1) {
		if len(match) == 2 {
			candidates = append(candidates, strings.TrimSpace(match[1]))
		}
	}
	for _, field := range strings.Fields(message) {
		candidate := strings.Trim(field, "\x60\"'()[]{}<>,;:!?")
		if candidate != "" && !strings.Contains(candidate, "://") && filepath.Ext(candidate) != "" {
			candidates = append(candidates, candidate)
		}
	}

	files := make([]steering.WorkspaceFile, 0, maximumChatWorkspaceFiles)
	seen := map[string]struct{}{}
	remaining := int64(maximumChatWorkspaceFileBytes)
	for _, candidate := range candidates {
		if len(files) == maximumChatWorkspaceFiles || remaining <= 0 {
			break
		}
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || len(candidate) > 1_024 {
			continue
		}
		path := candidate
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path, err = filepath.EvalSymlinks(filepath.Clean(path))
		if err != nil {
			continue
		}
		path, err = filepath.Abs(path)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(file, remaining+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(content) == 0 || !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
			continue
		}
		truncated := int64(len(content)) > remaining
		if truncated {
			content = content[:remaining]
		}
		files = append(files, steering.WorkspaceFile{Path: filepath.ToSlash(relative), Content: redactSecrets(string(content)), Truncated: truncated})
		seen[path] = struct{}{}
		remaining -= int64(len(content))
	}
	return files
}
