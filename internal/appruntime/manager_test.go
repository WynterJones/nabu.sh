package appruntime

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestManagerStartsStopsAndCapturesLogs(t *testing.T) {
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := availablePort(t)
	app := domain.LocalApp{ID: "app-one", Name: "Test app", Command: []string{"sh", "-c", "echo ready; trap 'exit 0' INT; while :; do sleep 1; done"}, Port: port}
	state, err := manager.Start(app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning || state.PID == 0 {
		t.Fatalf("start state = %#v", state)
	}
	if _, err := manager.Start(app, t.TempDir()); err == nil {
		t.Fatal("duplicate start unexpectedly succeeded")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		logs, logErr := manager.Logs(app.ID)
		if logErr != nil {
			t.Fatal(logErr)
		}
		if strings.Contains(logs, "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("logs never became ready: %q", logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stopped, err := manager.Stop(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != StatusStopped || stopped.PID != 0 {
		t.Fatalf("stop state = %#v", stopped)
	}
}

func TestManagerRejectsOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(domain.LocalApp{ID: "blocked", Name: "Blocked", Command: []string{"sh", "-c", "exit 0"}, Port: port}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Fatalf("occupied port error = %v", err)
	}
}

func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
