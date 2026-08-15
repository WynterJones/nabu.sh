package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	launchAgentLabel = "sh.nabu.daemon"
	systemdUnit      = "nabu.service"
)

type Manager struct {
	Home      string
	DataDir   string
	DaemonBin string
}

func New(home, dataDir, daemonBin string) *Manager {
	return &Manager{Home: home, DataDir: dataDir, DaemonBin: daemonBin}
}

func (m *Manager) Install() (string, error) {
	if !filepath.IsAbs(m.DaemonBin) {
		return "", fmt.Errorf("daemon path must be absolute: %s", m.DaemonBin)
	}
	if !filepath.IsAbs(m.Home) || !filepath.IsAbs(m.DataDir) {
		return "", errors.New("service home and data directory must be absolute")
	}
	info, err := os.Stat(m.DaemonBin)
	if err != nil {
		return "", fmt.Errorf("inspect daemon binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("daemon path is not a regular file: %s", m.DaemonBin)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("daemon path is not executable: %s", m.DaemonBin)
	}
	switch runtime.GOOS {
	case "darwin":
		path := filepath.Join(m.Home, "Library", "LaunchAgents", launchAgentLabel+".plist")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
  <key>EnvironmentVariables</key><dict><key>NABU_HOME</key><string>%s</string><key>PATH</key><string>%s</string></dict>
</dict></plist>
`, launchAgentLabel, xmlEscape(m.DaemonBin), xmlEscape(filepath.Join(m.DataDir, "logs", "nabud.stdout.log")), xmlEscape(filepath.Join(m.DataDir, "logs", "nabud.stderr.log")), xmlEscape(m.DataDir), xmlEscape(os.Getenv("PATH")))
		return path, writeAtomic(path, []byte(content), 0o644)
	case "linux":
		path := filepath.Join(m.Home, ".config", "systemd", "user", systemdUnit)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		content := fmt.Sprintf(`[Unit]
Description=Nabu local AI operator
After=network.target

[Service]
Type=simple
ExecStart="%s"
Environment="NABU_HOME=%s"
Environment="PATH=%s"
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, systemdEscape(m.DaemonBin), systemdEscape(m.DataDir), systemdEscape(os.Getenv("PATH")))
		if err := writeAtomic(path, []byte(content), 0o644); err != nil {
			return "", err
		}
		if _, err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return path, err
		}
		if _, err := run("systemctl", "--user", "enable", systemdUnit); err != nil {
			return path, err
		}
		return path, nil
	default:
		return "", fmt.Errorf("service installation is not supported on %s", runtime.GOOS)
	}
}

func (m *Manager) Start() error {
	switch runtime.GOOS {
	case "darwin":
		path := filepath.Join(m.Home, "Library", "LaunchAgents", launchAgentLabel+".plist")
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("launch agent is not installed: %w", err)
		}
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		// Reload the plist so an updated binary path, data directory, or PATH is
		// never left stale in launchd. bootout is idempotent for an unloaded job.
		if _, err := run("launchctl", "bootout", domain+"/"+launchAgentLabel); ignoreNotRunning(err) != nil {
			return err
		}
		_, err := run("launchctl", "bootstrap", domain, path)
		return err
	case "linux":
		_, err := run("systemctl", "--user", "start", systemdUnit)
		return err
	default:
		return fmt.Errorf("service control is not supported on %s", runtime.GOOS)
	}
}

func (m *Manager) Stop() error {
	switch runtime.GOOS {
	case "darwin":
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		_, err := run("launchctl", "bootout", domain+"/"+launchAgentLabel)
		return ignoreNotRunning(err)
	case "linux":
		_, err := run("systemctl", "--user", "stop", systemdUnit)
		return ignoreNotRunning(err)
	default:
		return fmt.Errorf("service control is not supported on %s", runtime.GOOS)
	}
}

func (m *Manager) Restart() error {
	switch runtime.GOOS {
	case "darwin":
		return m.Start()
	case "linux":
		_, err := run("systemctl", "--user", "restart", systemdUnit)
		return err
	default:
		return fmt.Errorf("service control is not supported on %s", runtime.GOOS)
	}
}

func (m *Manager) Uninstall() error {
	_ = m.Stop()
	switch runtime.GOOS {
	case "darwin":
		return os.Remove(filepath.Join(m.Home, "Library", "LaunchAgents", launchAgentLabel+".plist"))
	case "linux":
		_, _ = run("systemctl", "--user", "disable", systemdUnit)
		path := filepath.Join(m.Home, ".config", "systemd", "user", systemdUnit)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_, err := run("systemctl", "--user", "daemon-reload")
		return err
	default:
		return fmt.Errorf("service control is not supported on %s", runtime.GOOS)
	}
}

func run(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return output.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return output.String(), nil
}

func ignoreNotRunning(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if errors.Is(err, os.ErrNotExist) || strings.Contains(message, "not found") || strings.Contains(message, "not loaded") ||
		strings.Contains(message, "not running") || strings.Contains(message, "no such process") {
		return nil
	}
	return err
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func systemdEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "%", "%%")
}

func writeAtomic(path string, content []byte, mode os.FileMode) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".nabu-service-*")
	if err != nil {
		return fmt.Errorf("create temporary service file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(mode); err != nil {
		return fmt.Errorf("protect temporary service file: %w", err)
	}
	if _, err = temporary.Write(content); err != nil {
		return fmt.Errorf("write temporary service file: %w", err)
	}
	if err = temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary service file: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return fmt.Errorf("close temporary service file: %w", err)
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace service file: %w", err)
	}
	return nil
}
