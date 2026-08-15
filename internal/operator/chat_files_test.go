package operator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestChatWorkspaceFilesIncludesSetupPlanAndExplicitWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "PLAN.md"), []byte("# Plan\n\nResearch the market first."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("Useful owner notes."), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{Path: root, ContextReady: false}
	files := chatWorkspaceFiles(workspace, "Read `notes.txt` and follow it")
	if len(files) != 2 || files[0].Path != "PLAN.md" || files[1].Path != "notes.txt" {
		t.Fatalf("workspace files = %#v", files)
	}
	if !strings.Contains(files[0].Content, "Research the market") || !strings.Contains(files[1].Content, "owner notes") {
		t.Fatalf("workspace file contents = %#v", files)
	}
}

func TestChatWorkspaceFilesRejectsOutsideAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("private outside content"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	message := "Read `" + outside + "` and `linked.md`"
	files := chatWorkspaceFiles(domain.Workspace{Path: root, ContextReady: true}, message)
	if len(files) != 0 {
		t.Fatalf("outside content crossed workspace boundary: %#v", files)
	}
}

func TestChatWorkspaceFilesRedactsSecretsAndBoundsContent(t *testing.T) {
	root := t.TempDir()
	content := "api_key: highly_sensitive_value_123456\n" + strings.Repeat("x", maximumChatWorkspaceFileBytes+100)
	if err := os.WriteFile(filepath.Join(root, "context.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	files := chatWorkspaceFiles(domain.Workspace{Path: root, ContextReady: true}, "Read context.md")
	if len(files) != 1 || !files[0].Truncated || len(files[0].Content) > maximumChatWorkspaceFileBytes {
		t.Fatalf("bounded file = %#v", files)
	}
	if strings.Contains(files[0].Content, "highly_sensitive") || !strings.Contains(files[0].Content, "[REDACTED]") {
		t.Fatalf("secret was not redacted: %q", files[0].Content[:min(len(files[0].Content), 120)])
	}
}
