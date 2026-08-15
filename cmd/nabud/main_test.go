package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/store"
)

func TestValidateLocalAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7777", "localhost:7777", "[::1]:7777"} {
		if err := validateLocalAddress(address); err != nil {
			t.Errorf("validateLocalAddress(%q) = %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:7777", "192.168.1.5:7777", "example.com:7777"} {
		if err := validateLocalAddress(address); err == nil {
			t.Errorf("validateLocalAddress(%q) unexpectedly succeeded", address)
		}
	}
}

func TestBackupLoopCreatesBackupAndStops(t *testing.T) {
	root := t.TempDir()
	database, err := store.Open(filepath.Join(root, "nabu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		backupLoop(ctx, database, filepath.Join(root, "backups"), slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	path := filepath.Join(root, "backups", "nabu-"+time.Now().UTC().Format("2006-01-02")+".db")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daily backup was not created")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("backup loop did not stop after cancellation")
	}
	copy, err := store.Open(path)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer copy.Close()
	if err := copy.Ping(context.Background()); err != nil {
		t.Fatalf("backup ping: %v", err)
	}
}

func TestWaitForBackground(t *testing.T) {
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		time.Sleep(10 * time.Millisecond)
	}()
	if !waitForBackground(&group, time.Second) {
		t.Fatal("completed background worker timed out")
	}
}
