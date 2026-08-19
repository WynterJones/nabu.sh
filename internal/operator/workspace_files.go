package operator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nabu-sh/nabu/internal/api"
)

const maximumEditableFileBytes = 4 * 1024 * 1024
const maximumWorkspaceDirectoryEntries = 500

var _ api.WorkspaceFileBackend = (*Operator)(nil)

func (o *Operator) WorkspaceFile(ctx context.Context, requestedPath string, includeContent bool) (api.WorkspaceFile, error) {
	resolved, relative, err := o.resolveWorkspaceFile(ctx, requestedPath)
	if err != nil {
		return api.WorkspaceFile{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return api.WorkspaceFile{}, api.ErrNotFound
		}
		return api.WorkspaceFile{}, err
	}
	if info.IsDir() {
		return workspaceDirectory(resolved, relative, info)
	}
	if !info.Mode().IsRegular() {
		return api.WorkspaceFile{}, fmt.Errorf("%w: path must identify a regular file", api.ErrInvalid)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return api.WorkspaceFile{}, err
	}
	defer file.Close()
	probe := make([]byte, 512)
	count, readErr := file.Read(probe)
	if readErr != nil && !errors.Is(readErr, io.EOF) && count == 0 {
		return api.WorkspaceFile{}, readErr
	}
	probe = probe[:count]
	mimeType, known := workspaceTextTypes[strings.ToLower(filepath.Ext(resolved))]
	if !known {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(resolved)))
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(probe)
	}
	mimeType = strings.Split(mimeType, ";")[0]
	kind, editable := classifyWorkspaceFile(resolved, mimeType, probe, info.Size())
	if kind == "text" && !strings.HasPrefix(mimeType, "text/") && mimeType != "application/json" {
		mimeType = "text/plain"
	}
	value := api.WorkspaceFile{
		Path: filepath.ToSlash(relative), Name: filepath.Base(resolved), Kind: kind, MIMEType: mimeType,
		Size: info.Size(), Editable: editable, ResolvedPath: resolved, ModifiedAt: info.ModTime(),
	}
	if includeContent && editable {
		content, readErr := os.ReadFile(resolved)
		if readErr != nil {
			return api.WorkspaceFile{}, readErr
		}
		value.Content = string(content)
	}
	return value, nil
}

func workspaceDirectory(resolved, relative string, info os.FileInfo) (api.WorkspaceFile, error) {
	directoryEntries, err := os.ReadDir(resolved)
	if err != nil {
		return api.WorkspaceFile{}, err
	}
	entries := make([]api.WorkspaceFileEntry, 0, min(len(directoryEntries), maximumWorkspaceDirectoryEntries))
	for _, entry := range directoryEntries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		entryInfo, entryErr := entry.Info()
		if entryErr != nil || (!entryInfo.IsDir() && !entryInfo.Mode().IsRegular()) {
			continue
		}
		kind := "file"
		if entryInfo.IsDir() {
			kind = "directory"
		}
		entries = append(entries, api.WorkspaceFileEntry{
			Path:       filepath.ToSlash(filepath.Join(relative, entry.Name())),
			Name:       entry.Name(),
			Kind:       kind,
			Size:       entryInfo.Size(),
			ModifiedAt: entryInfo.ModTime(),
		})
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].Kind != entries[right].Kind {
			return entries[left].Kind == "directory"
		}
		return strings.ToLower(entries[left].Name) < strings.ToLower(entries[right].Name)
	})
	truncated := len(entries) > maximumWorkspaceDirectoryEntries
	if truncated {
		entries = entries[:maximumWorkspaceDirectoryEntries]
	}
	return api.WorkspaceFile{
		Path:         filepath.ToSlash(relative),
		Name:         filepath.Base(resolved),
		Kind:         "directory",
		MIMEType:     "application/x-directory",
		ModifiedAt:   info.ModTime(),
		ResolvedPath: resolved,
		Entries:      entries,
		Truncated:    truncated,
	}, nil
}

func (o *Operator) SaveWorkspaceFile(ctx context.Context, requestedPath, content string) (api.WorkspaceFile, error) {
	if len(content) > maximumEditableFileBytes {
		return api.WorkspaceFile{}, fmt.Errorf("%w: editable files cannot exceed 4 MB", api.ErrInvalid)
	}
	current, err := o.WorkspaceFile(ctx, requestedPath, false)
	if err != nil {
		return api.WorkspaceFile{}, err
	}
	if !current.Editable || !utf8.ValidString(content) {
		return api.WorkspaceFile{}, fmt.Errorf("%w: this file is not editable text", api.ErrInvalid)
	}
	info, err := os.Stat(current.ResolvedPath)
	if err != nil {
		return api.WorkspaceFile{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(current.ResolvedPath), ".nabu-edit-*")
	if err != nil {
		return api.WorkspaceFile{}, err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return api.WorkspaceFile{}, err
	}
	if _, err := temporary.WriteString(content); err != nil {
		return api.WorkspaceFile{}, err
	}
	if err := temporary.Sync(); err != nil {
		return api.WorkspaceFile{}, err
	}
	if err := temporary.Close(); err != nil {
		return api.WorkspaceFile{}, err
	}
	if err := os.Rename(temporaryPath, current.ResolvedPath); err != nil {
		return api.WorkspaceFile{}, err
	}
	removeTemporary = false
	workspace, _ := o.store.ActiveWorkspace(ctx)
	o.emitForWorkspace(ctx, workspace.ID, "workspace.file.updated", current.Path, map[string]any{"path": current.Path, "size": len(content)})
	return o.WorkspaceFile(ctx, current.Path, true)
}

func (o *Operator) resolveWorkspaceFile(ctx context.Context, requestedPath string) (string, string, error) {
	workspace, err := o.store.ActiveWorkspace(ctx)
	if err != nil {
		return "", "", translateNotFound(err)
	}
	root, err := filepath.EvalSymlinks(workspace.Path)
	if err != nil {
		return "", "", fmt.Errorf("%w: active workspace is unavailable", api.ErrUnavailable)
	}
	value := strings.TrimSpace(requestedPath)
	if parsed, parseErr := url.Parse(value); parseErr == nil && parsed.Scheme == "file" {
		value = parsed.Path
	}
	if value == "" || strings.ContainsRune(value, '\x00') {
		return "", "", fmt.Errorf("%w: file path is required", api.ErrInvalid)
	}
	candidate := filepath.Clean(value)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", api.ErrNotFound
		}
		return "", "", fmt.Errorf("%w: file cannot be resolved", api.ErrInvalid)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", api.ErrNotFound
	}
	return resolved, relative, nil
}

// workspaceTextTypes fixes the media type reported for the file kinds a
// workspace actually contains, instead of asking the operating system.
//
// mime.TypeByExtension reads the machine's own database, which disagrees
// across systems: Ubuntu maps .ts to text/vnd.trolltech.linguist and other
// systems map it to video/mp2t, so the same TypeScript file was described
// differently depending on where the daemon happened to run, and was served
// with a Content-Type that makes a browser download it rather than show it.
var workspaceTextTypes = map[string]string{
	".css":         "text/css",
	".csv":         "text/csv",
	".env.example": "text/plain",
	".gitignore":   "text/plain",
	".go":          "text/plain",
	".html":        "text/html",
	".java":        "text/plain",
	".js":          "text/javascript",
	".json":        "application/json",
	".jsonl":       "application/json",
	".jsx":         "text/javascript",
	".md":          "text/markdown",
	".mdx":         "text/markdown",
	".py":          "text/plain",
	".rb":          "text/plain",
	".rs":          "text/plain",
	".sh":          "text/plain",
	".sql":         "text/plain",
	".toml":        "text/plain",
	".ts":          "text/plain",
	".tsv":         "text/tab-separated-values",
	".tsx":         "text/plain",
	".txt":         "text/plain",
	".xml":         "text/xml",
	".yaml":        "text/plain",
	".yml":         "text/plain",
	".zsh":         "text/plain",
}

func classifyWorkspaceFile(path, mimeType string, probe []byte, size int64) (string, bool) {
	extension := strings.ToLower(filepath.Ext(path))
	_, knownText := workspaceTextTypes[extension]
	text := knownText || strings.HasPrefix(mimeType, "text/") || (utf8.Valid(probe) && !bytes.ContainsRune(probe, '\x00'))
	if text && size <= maximumEditableFileBytes {
		return "text", true
	}
	switch {
	case mimeType == "application/pdf":
		return "pdf", false
	case strings.HasPrefix(mimeType, "image/"):
		return "image", false
	case strings.HasPrefix(mimeType, "video/"):
		return "video", false
	}
	return "unsupported", false
}
