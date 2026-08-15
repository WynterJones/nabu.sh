package operator

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nabu-sh/nabu/internal/api"
)

func TestWorkspaceFileReadsEditsAndClassifiesWithinActiveWorkspace(t *testing.T) {
	service, _, _, workspace := testOperator(t, fakeExecutor{})
	textPath := filepath.Join(workspace.Path, "docs", "brief.md")
	if err := os.MkdirAll(filepath.Dir(textPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(textPath, []byte("# Before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	value, err := service.WorkspaceFile(context.Background(), "docs/brief.md", true)
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind != "text" || !value.Editable || value.Content != "# Before\n" || value.ResolvedPath == "" {
		t.Fatalf("workspace file = %#v", value)
	}
	saved, err := service.SaveWorkspaceFile(context.Background(), value.Path, "# After\n")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Content != "# After\n" {
		t.Fatalf("saved content = %q", saved.Content)
	}
	info, err := os.Stat(textPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}

	imagePath := filepath.Join(workspace.Path, "media", "pixel.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	image, err := service.WorkspaceFile(context.Background(), imagePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if image.Kind != "image" || image.Editable || image.Content != "" {
		t.Fatalf("image file = %#v", image)
	}

	typeScriptPath := filepath.Join(workspace.Path, "src", "index.ts")
	if err := os.MkdirAll(filepath.Dir(typeScriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(typeScriptPath, []byte("export const ready = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	typeScript, err := service.WorkspaceFile(context.Background(), typeScriptPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if typeScript.Kind != "text" || !typeScript.Editable {
		t.Fatalf("TypeScript file = %#v", typeScript)
	}
	if typeScript.MIMEType != "text/plain" {
		t.Fatalf("TypeScript MIME type = %q", typeScript.MIMEType)
	}
}

func TestWorkspaceFileRejectsTraversalAndSymlinkEscape(t *testing.T) {
	service, _, paths, workspace := testOperator(t, fakeExecutor{})
	outside := filepath.Join(paths.Root, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside.txt", outside} {
		if _, err := service.WorkspaceFile(context.Background(), path, true); err == nil {
			t.Fatalf("WorkspaceFile(%q) unexpectedly succeeded", path)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(workspace.Path, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WorkspaceFile(context.Background(), link, true); err == nil {
		t.Fatal("symlink escape unexpectedly succeeded")
	}
	if _, err := service.SaveWorkspaceFile(context.Background(), "missing.txt", "x"); err == nil || err == api.ErrInvalid {
		t.Fatalf("missing save error = %v", err)
	}
}

func TestWorkspaceFileListsDirectoriesFoldersFirstWithoutFollowingSymlinks(t *testing.T) {
	service, _, paths, workspace := testOperator(t, fakeExecutor{})
	directory := filepath.Join(workspace.Path, "repos", "webapp")
	if err := os.MkdirAll(filepath.Join(directory, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("# Web app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(paths.Root, "outside.txt"), filepath.Join(directory, "escape")); err != nil {
			t.Fatal(err)
		}
	}

	value, err := service.WorkspaceFile(context.Background(), "repos/webapp", true)
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind != "directory" || value.Editable || value.Content != "" || value.Path != "repos/webapp" {
		t.Fatalf("directory = %#v", value)
	}
	if len(value.Entries) != 2 {
		t.Fatalf("entries = %#v", value.Entries)
	}
	if value.Entries[0].Kind != "directory" || value.Entries[0].Name != "src" {
		t.Fatalf("first entry = %#v", value.Entries[0])
	}
	if value.Entries[1].Kind != "file" || value.Entries[1].Name != "README.md" {
		t.Fatalf("second entry = %#v", value.Entries[1])
	}
}
