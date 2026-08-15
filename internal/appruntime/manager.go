// Package appruntime supervises owner-registered local development apps.
// Commands are direct argv vectors executed from a validated workspace folder;
// no shell interpolation is supported.
package appruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const (
	maximumLogBytes          = 256 * 1024
	maximumPersistedLogBytes = 4 * 1024 * 1024
)

type Status string

const (
	StatusStopped Status = "stopped"
	StatusRunning Status = "running"
	StatusFailed  Status = "failed"
)

type State struct {
	AppID     string     `json:"app_id"`
	Status    Status     `json:"status"`
	PID       int        `json:"pid,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Error     string     `json:"error,omitempty"`
	LogPath   string     `json:"-"`
}

type process struct {
	command *exec.Cmd
	log     *rollingLog
	done    chan struct{}
	state   State
}

type Manager struct {
	logsRoot string

	mu        sync.Mutex
	processes map[string]*process
	last      map[string]State
}

func New(logsRoot string) (*Manager, error) {
	if strings.TrimSpace(logsRoot) == "" {
		return nil, errors.New("app runtime: logs root is required")
	}
	root := filepath.Join(logsRoot, "local-apps")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("app runtime: create logs root: %w", err)
	}
	return &Manager{logsRoot: root, processes: make(map[string]*process), last: make(map[string]State)}, nil
}

func (m *Manager) Start(app domain.LocalApp, directory string) (State, error) {
	if strings.TrimSpace(app.ID) == "" || len(app.Command) == 0 {
		return State{}, errors.New("app runtime: app ID and command are required")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return State{}, fmt.Errorf("app runtime: application folder is unavailable")
	}
	m.mu.Lock()
	if current := m.processes[app.ID]; current != nil {
		state := current.state
		m.mu.Unlock()
		return state, fmt.Errorf("app runtime: application is already running")
	}
	m.mu.Unlock()

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(app.Port)))
	if err != nil {
		return State{}, fmt.Errorf("app runtime: port %d is already in use", app.Port)
	}
	_ = listener.Close()

	executable, err := resolveExecutable(app.Command[0], directory)
	if err != nil {
		return State{}, err
	}
	logPath := filepath.Join(m.logsRoot, app.ID+".log")
	logFile, err := newRollingLog(logPath)
	if err != nil {
		return State{}, fmt.Errorf("app runtime: open log: %w", err)
	}
	started := time.Now().UTC()
	_, _ = fmt.Fprintf(logFile, "\n[%s] starting %s\n", started.Format(time.RFC3339), app.Name)
	command := exec.Command(executable, app.Command[1:]...)
	configureCommand(command)
	command.Dir = directory
	command.Env = append(os.Environ(), "HOST=127.0.0.1", "PORT="+strconv.Itoa(app.Port))
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return State{}, fmt.Errorf("app runtime: start application: %w", err)
	}
	entry := &process{command: command, log: logFile, done: make(chan struct{}), state: State{
		AppID: app.ID, Status: StatusRunning, PID: command.Process.Pid, StartedAt: &started, LogPath: logPath,
	}}
	m.mu.Lock()
	if current := m.processes[app.ID]; current != nil {
		m.mu.Unlock()
		_ = killProcess(command.Process)
		_ = logFile.Close()
		return current.state, fmt.Errorf("app runtime: application is already running")
	}
	m.processes[app.ID] = entry
	m.last[app.ID] = entry.state
	m.mu.Unlock()
	go m.wait(app.ID, entry)
	return entry.state, nil
}

func (m *Manager) wait(appID string, entry *process) {
	err := entry.command.Wait()
	stopped := time.Now().UTC()
	state := entry.state
	state.PID = 0
	state.StoppedAt = &stopped
	state.Status = StatusStopped
	if entry.command.ProcessState != nil {
		exit := entry.command.ProcessState.ExitCode()
		state.ExitCode = &exit
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		state.Status = StatusFailed
		state.Error = boundedError(err)
	}
	_, _ = fmt.Fprintf(entry.log, "[%s] stopped\n", stopped.Format(time.RFC3339))
	_ = entry.log.Close()
	m.mu.Lock()
	if m.processes[appID] == entry {
		delete(m.processes, appID)
		m.last[appID] = state
	}
	m.mu.Unlock()
	close(entry.done)
}

func (m *Manager) Stop(ctx context.Context, appID string) (State, error) {
	m.mu.Lock()
	entry := m.processes[appID]
	m.mu.Unlock()
	if entry == nil {
		state := m.State(appID)
		state.Status = StatusStopped
		return state, nil
	}
	if err := interruptProcess(entry.command.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return entry.state, fmt.Errorf("app runtime: stop application: %w", err)
	}
	select {
	case <-entry.done:
		return m.State(appID), nil
	case <-ctx.Done():
		_ = killProcess(entry.command.Process)
		select {
		case <-entry.done:
			return m.State(appID), nil
		case <-time.After(2 * time.Second):
			return entry.state, ctx.Err()
		}
	}
}

type rollingLog struct {
	mu   sync.Mutex
	file *os.File
	size int64
}

func newRollingLog(path string) (*rollingLog, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	log := &rollingLog{file: file, size: info.Size()}
	if log.size > maximumPersistedLogBytes {
		if err := file.Truncate(0); err != nil {
			_ = file.Close()
			return nil, err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, err
		}
		log.size = 0
	}
	return log, nil
}

func (l *rollingLog) Write(value []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	originalLength := len(value)
	if int64(len(value))+l.size > maximumPersistedLogBytes {
		if err := l.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := l.file.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		l.size = 0
		marker := []byte("[Nabu rotated local app logs]\n")
		if _, err := l.file.Write(marker); err != nil {
			return 0, err
		}
		l.size = int64(len(marker))
		if len(value) > maximumPersistedLogBytes-len(marker) {
			value = value[len(value)-(maximumPersistedLogBytes-len(marker)):]
		}
	}
	written, err := l.file.Write(value)
	l.size += int64(written)
	if err != nil {
		return written, err
	}
	return originalLength, nil
}

func (l *rollingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

func (m *Manager) State(appID string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry := m.processes[appID]; entry != nil {
		return entry.state
	}
	if state, ok := m.last[appID]; ok {
		return state
	}
	return State{AppID: appID, Status: StatusStopped, LogPath: filepath.Join(m.logsRoot, appID+".log")}
}

func (m *Manager) RunningIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.processes))
	for id := range m.processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) Logs(appID string) (string, error) {
	path := m.State(appID).LogPath
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("app runtime: open logs: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("app runtime: inspect logs: %w", err)
	}
	start := info.Size() - maximumLogBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", fmt.Errorf("app runtime: seek logs: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(file, maximumLogBytes))
	if err != nil {
		return "", fmt.Errorf("app runtime: read logs: %w", err)
	}
	return string(content), nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	ids := m.RunningIDs()
	var failures []string
	for _, id := range ids {
		if _, err := m.Stop(ctx, id); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func resolveExecutable(value, directory string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsRune(value, '\x00') {
		return "", errors.New("app runtime: executable is required")
	}
	if strings.ContainsRune(value, filepath.Separator) {
		candidate := value
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(directory, candidate)
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("app runtime: executable is unavailable")
		}
		relative, err := filepath.Rel(directory, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", errors.New("app runtime: executable escapes the application folder")
		}
		return resolved, nil
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return "", fmt.Errorf("app runtime: executable %q is unavailable", value)
	}
	return resolved, nil
}

func boundedError(err error) string {
	value := strings.TrimSpace(err.Error())
	if len(value) > 1000 {
		return value[:1000] + "…"
	}
	return value
}
