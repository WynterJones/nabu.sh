package operator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestBackup(t *testing.T) {
	directory := t.TempDir()
	older := filepath.Join(directory, "older.db")
	newer := filepath.Join(directory, "newer.db")
	if err := os.WriteFile(older, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if err := os.Chtimes(newer, want, want); err != nil {
		t.Fatal(err)
	}
	oldTime := want.Add(-time.Hour)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	got := latestBackup(directory)
	if got == nil || got.Truncate(time.Second) != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
