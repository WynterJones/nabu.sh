package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/service"
	"github.com/nabu-sh/nabu/internal/store"
	"github.com/nabu-sh/nabu/internal/version"
	"github.com/nabu-sh/nabu/webassets"
)

const defaultURL = "http://127.0.0.1:7777"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nabu:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	manager, err := serviceManager(paths)
	if err != nil {
		return err
	}
	switch args[0] {
	case "setup":
		return setup(paths, manager)
	case "open":
		return openBrowser(defaultURL)
	case "status":
		return showStatus()
	case "start":
		if err := manager.Start(); err != nil {
			return err
		}
		fmt.Println("Nabu started.")
		return nil
	case "stop":
		if err := manager.Stop(); err != nil {
			return err
		}
		fmt.Println("Nabu stopped. Durable state was preserved.")
		return nil
	case "restart":
		if err := manager.Restart(); err != nil {
			return err
		}
		fmt.Println("Nabu restarted.")
		return nil
	case "logs":
		return showLogs(paths)
	case "doctor":
		return doctor(paths)
	case "uninstall":
		if err := manager.Uninstall(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Printf("Nabu's background service was removed. Your durable data remains at %s.\n", paths.Root)
		return nil
	case "version", "--version", "-v":
		fmt.Println("nabu", version.String())
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Print(`Nabu — local, always-on AI operator

Usage: nabu <command>

  setup      Initialize Nabu and install the user service
  open       Open the local web interface
  status     Show the daemon and mission status
  start      Start the user service
  stop       Stop the user service
  restart    Restart the user service
  logs       Show recent daemon logs
  doctor     Check the local installation
  uninstall Remove the user service (durable data is preserved)
  version    Print the installed version
`)
}

func setup(paths config.Paths, manager *service.Manager) error {
	if _, err := config.Ensure(paths.Root); err != nil {
		return err
	}
	path, err := manager.Install()
	if err != nil {
		return fmt.Errorf("install background service: %w", err)
	}
	fmt.Println("Installed background service:", path)
	if err := manager.Restart(); err != nil {
		return fmt.Errorf("start updated background service: %w", err)
	}
	if err := waitForDaemon(8 * time.Second); err != nil {
		return err
	}
	fmt.Println("Nabu is ready at", defaultURL)
	return openBrowser(defaultURL)
}

func serviceManager(paths config.Paths) (*service.Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	daemon := filepath.Join(filepath.Dir(executable), "nabud")
	if _, err := os.Stat(daemon); err != nil {
		if found, lookErr := exec.LookPath("nabud"); lookErr == nil {
			daemon, err = filepath.Abs(found)
		}
	}
	return service.New(home, paths.Root, daemon), nil
}

func showStatus() error {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(defaultURL + "/api/status")
	if err != nil {
		return fmt.Errorf("daemon is not reachable at %s", defaultURL)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %s", response.Status)
	}
	var status domain.StatusSnapshot
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return fmt.Errorf("read daemon status: %w", err)
	}
	fmt.Printf("%-18s %s\n", "Status", displayStatus(status.Status))
	fmt.Printf("%-18s %t\n", "Setup complete", status.SetupComplete)
	fmt.Printf("%-18s %t\n", "Mission started", status.MissionStarted)
	fmt.Printf("%-18s %t\n", "Codex available", status.CodexAvailable)
	fmt.Printf("%-18s %d\n", "Ready tasks", status.ReadyCount)
	if status.ActiveTask != nil {
		fmt.Printf("%-18s %s\n", "Current task", status.ActiveTask.Title)
	}
	return nil
}

func displayStatus(status domain.GlobalStatus) string {
	return strings.ReplaceAll(strings.Title(strings.ReplaceAll(string(status), "_", " ")), "For", "for") //nolint:staticcheck
}

func waitForDaemon(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", "127.0.0.1:7777", 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return errors.New("daemon did not become ready; run `nabu logs` for details")
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "linux":
		command = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("open %s in your browser", url)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func showLogs(paths config.Paths) error {
	files := []string{filepath.Join(paths.Logs, "nabud.log"), filepath.Join(paths.Logs, "nabud.stderr.log"), filepath.Join(paths.Logs, "nabud.stdout.log")}
	found := false
	for _, path := range files {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		found = true
		fmt.Println("==>", path, "<==")
		if err := tail(path, 120); err != nil {
			return err
		}
	}
	if !found {
		fmt.Println("No daemon logs yet.")
	}
	return nil
}

func tail(path string, limit int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	lines := make([]string, 0, limit)
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		if len(lines) == limit {
			copy(lines, lines[1:])
			lines[len(lines)-1] = scanner.Text()
		} else {
			lines = append(lines, scanner.Text())
		}
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return scanner.Err()
}

type doctorCheck struct {
	name   string
	detail string
	err    error
}

func doctor(paths config.Paths) error {
	checks := make([]doctorCheck, 0, 10)
	if _, err := config.Ensure(paths.Root); err != nil {
		checks = append(checks, doctorCheck{"Workspace", paths.Root, err})
	} else {
		checks = append(checks, doctorCheck{"Workspace", paths.Root, nil})
	}
	database, err := store.Open(paths.Database)
	var approved []domain.Workspace
	if err == nil {
		err = database.Ping(context.Background())
		if err == nil {
			approved, err = database.ListWorkspaces(context.Background())
		}
		_ = database.Close()
	}
	checks = append(checks, doctorCheck{"SQLite", paths.Database, err})
	for _, workspace := range approved {
		info, statErr := os.Stat(workspace.Path)
		if statErr == nil && !info.IsDir() {
			statErr = errors.New("path is not a directory")
		}
		checks = append(checks, doctorCheck{"Workspace path", workspace.Path, statErr})
	}
	_, err = fs.Stat(webassets.FS(), "index.html")
	checks = append(checks, doctorCheck{"Frontend", "embedded assets", err})
	checks = append(checks, binaryCheck("Codex CLI", "codex", "--version"))
	checks = append(checks, binaryCheck("Codex login", "codex", "login", "status"))
	checks = append(checks, binaryCheck("Git", "git", "--version"))
	checks = append(checks, portCheck())
	checks = append(checks, serviceCheck())
	checks = append(checks, diskCheck(paths.Root))

	failed := 0
	for _, check := range checks {
		if check.err != nil {
			failed++
			fmt.Printf("FAIL  %-16s %v\n", check.name, check.err)
		} else {
			fmt.Printf("OK    %-16s %s\n", check.name, check.detail)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d check(s) need attention", failed)
	}
	return nil
}

func serviceCheck() doctorCheck {
	home, err := os.UserHomeDir()
	if err != nil {
		return doctorCheck{"User service", "", err}
	}
	var path string
	switch runtime.GOOS {
	case "darwin":
		path = filepath.Join(home, "Library", "LaunchAgents", "sh.nabu.daemon.plist")
	case "linux":
		path = filepath.Join(home, ".config", "systemd", "user", "nabu.service")
	default:
		return doctorCheck{"User service", runtime.GOOS, errors.New("unsupported platform")}
	}
	_, err = os.Stat(path)
	return doctorCheck{"User service", path, err}
}

func binaryCheck(label, binary string, args ...string) doctorCheck {
	path, err := exec.LookPath(binary)
	if err != nil {
		return doctorCheck{label, "", err}
	}
	output, err := exec.Command(path, args...).CombinedOutput()
	return doctorCheck{label, strings.TrimSpace(string(output)), err}
}

func portCheck() doctorCheck {
	connection, err := net.DialTimeout("tcp", "127.0.0.1:7777", 500*time.Millisecond)
	if err != nil {
		return doctorCheck{"Daemon", defaultURL, errors.New("not reachable; run `nabu start`")}
	}
	_ = connection.Close()
	return doctorCheck{"Daemon", defaultURL, nil}
}

func diskCheck(path string) doctorCheck {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return doctorCheck{"Disk space", path, err}
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	detail := fmt.Sprintf("%.1f GiB available", float64(available)/(1024*1024*1024))
	if available < 512*1024*1024 {
		return doctorCheck{"Disk space", detail, errors.New("less than 512 MiB available")}
	}
	return doctorCheck{"Disk space", detail, nil}
}
