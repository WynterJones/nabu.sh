package remoteaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxCommandOutput = 256 << 10

const nabuTarget = "http://127.0.0.1:7777"

var ErrUnavailable = errors.New("tailscale is unavailable")

type TailscaleStatus struct {
	Installed        bool   `json:"installed"`
	Connected        bool   `json:"connected"`
	Version          string `json:"version,omitempty"`
	MachineName      string `json:"machine_name,omitempty"`
	DNSName          string `json:"dns_name,omitempty"`
	TailnetName      string `json:"tailnet_name,omitempty"`
	PrivateURL       string `json:"private_url,omitempty"`
	ServeConfigured  bool   `json:"serve_configured"`
	FunnelConfigured bool   `json:"funnel_configured"`
	AuthorizationURL string `json:"authorization_url,omitempty"`
}

type SetupResult struct {
	Status           TailscaleStatus `json:"status"`
	AuthorizationURL string          `json:"authorization_url,omitempty"`
}

type tailscaleSnapshot struct {
	Version      string `json:"Version"`
	BackendState string `json:"BackendState"`
	Self         struct {
		HostName string `json:"HostName"`
		DNSName  string `json:"DNSName"`
	} `json:"Self"`
}

func ProbeTailscale(ctx context.Context) TailscaleStatus {
	path, err := tailscalePath()
	if err != nil {
		return TailscaleStatus{}
	}
	status := TailscaleStatus{Installed: true}
	commandContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	output, err := runBounded(commandContext, path, "status", "--json")
	if err != nil {
		return status
	}
	status = parseTailscaleStatus(output)
	status.Installed = true
	if !status.Connected {
		return status
	}
	if output, err = runBounded(commandContext, path, "serve", "status", "--json"); err == nil {
		status.ServeConfigured = nabuConfigured(output)
	}
	if output, err = runBounded(commandContext, path, "funnel", "status", "--json"); err == nil {
		status.FunnelConfigured = configured(output)
	}
	return status
}

// EnableNabuServe configures one fixed private HTTPS proxy. It never accepts
// user-provided commands or targets, and it does not enable public Funnel.
func EnableNabuServe(ctx context.Context) (SetupResult, error) {
	path, err := tailscalePath()
	if err != nil {
		return SetupResult{}, fmt.Errorf("%w: install Tailscale first", ErrUnavailable)
	}
	commandContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	status := ProbeTailscale(ctx)
	if !status.Connected {
		return SetupResult{}, fmt.Errorf("%w: sign in to Tailscale first", ErrUnavailable)
	}
	if status.FunnelConfigured {
		return SetupResult{}, fmt.Errorf("%w: public Tailscale Funnel is active; disable it before enabling private access", ErrUnavailable)
	}
	existing, statusErr := runBounded(commandContext, path, "serve", "status", "--json")
	if statusErr == nil {
		if nabuConfigured(existing) {
			status.ServeConfigured = true
			return SetupResult{Status: status}, nil
		}
		if configured(existing) {
			return SetupResult{}, fmt.Errorf("%w: another Tailscale Serve route already exists on this Mac; review it before connecting Nabu", ErrUnavailable)
		}
	}
	output, runErr := runBoundedCombined(commandContext, path, "serve", "--bg", "--yes", nabuTarget)
	if authorizationURL := parseAuthorizationURL(output); authorizationURL != "" {
		status := ProbeTailscale(ctx)
		status.AuthorizationURL = authorizationURL
		return SetupResult{Status: status, AuthorizationURL: authorizationURL}, nil
	}
	if runErr != nil {
		return SetupResult{}, fmt.Errorf("%w: %s", ErrUnavailable, commandMessage(output, runErr))
	}
	return SetupResult{Status: ProbeTailscale(ctx)}, nil
}

func DisableNabuServe(ctx context.Context) (TailscaleStatus, error) {
	path, err := tailscalePath()
	if err != nil {
		return TailscaleStatus{}, fmt.Errorf("%w: install Tailscale first", ErrUnavailable)
	}
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	existing, statusErr := runBounded(commandContext, path, "serve", "status", "--json")
	if statusErr != nil {
		return TailscaleStatus{}, fmt.Errorf("%w: could not inspect the existing Serve route", ErrUnavailable)
	}
	if !configured(existing) {
		return ProbeTailscale(ctx), nil
	}
	if !nabuConfigured(existing) {
		return TailscaleStatus{}, fmt.Errorf("%w: the existing Serve route is not managed by Nabu", ErrUnavailable)
	}
	if containsOtherProxyTarget(existing) {
		return TailscaleStatus{}, fmt.Errorf("%w: other private routes share this Serve configuration; remove Nabu in Tailscale instead", ErrUnavailable)
	}
	output, runErr := runBoundedCombined(commandContext, path, "serve", "reset")
	if runErr != nil {
		return TailscaleStatus{}, fmt.Errorf("%w: %s", ErrUnavailable, commandMessage(output, runErr))
	}
	return ProbeTailscale(ctx), nil
}

func parseTailscaleStatus(input []byte) TailscaleStatus {
	var snapshot tailscaleSnapshot
	if json.Unmarshal(input, &snapshot) != nil {
		return TailscaleStatus{}
	}
	dnsName := strings.TrimSuffix(strings.TrimSpace(snapshot.Self.DNSName), ".")
	tailnetName := ""
	if separator := strings.IndexByte(dnsName, '.'); separator >= 0 && separator+1 < len(dnsName) {
		tailnetName = dnsName[separator+1:]
	}
	privateURL := ""
	if dnsName != "" {
		privateURL = "https://" + dnsName
	}
	return TailscaleStatus{
		Connected:   snapshot.BackendState == "Running",
		Version:     snapshot.Version,
		MachineName: snapshot.Self.HostName,
		DNSName:     dnsName,
		TailnetName: tailnetName,
		PrivateURL:  privateURL,
	}
}

func configured(input []byte) bool {
	trimmed := bytes.TrimSpace(input)
	return len(trimmed) > 2 && !bytes.Equal(trimmed, []byte("null"))
}

func nabuConfigured(input []byte) bool {
	return configured(input) && bytes.Contains(input, []byte(nabuTarget))
}

func containsOtherProxyTarget(input []byte) bool {
	var value any
	if json.Unmarshal(input, &value) != nil {
		return true
	}
	var inspect func(any) bool
	inspect = func(item any) bool {
		switch typed := item.(type) {
		case map[string]any:
			for _, child := range typed {
				if inspect(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if inspect(child) {
					return true
				}
			}
		case string:
			candidate := strings.TrimSpace(typed)
			isTarget := strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") || strings.HasPrefix(candidate, "https+insecure://") || strings.HasPrefix(candidate, "unix:")
			return isTarget && candidate != nabuTarget
		}
		return false
	}
	return inspect(value)
}

func parseAuthorizationURL(output []byte) string {
	for _, field := range strings.Fields(string(output)) {
		candidate := strings.TrimSpace(field)
		if strings.HasPrefix(candidate, "https://login.tailscale.com/f/") {
			return candidate
		}
	}
	return ""
}

func commandMessage(output []byte, fallback error) string {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fallback.Error()
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func tailscalePath() (string, error) {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path, nil
	}
	for _, candidate := range []string{
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
		"/usr/local/bin/tailscale",
		"/opt/homebrew/bin/tailscale",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return filepath.Clean(candidate), nil
		}
	}
	return "", errors.New("tailscale executable not found")
}

func runBounded(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		return nil, err
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxCommandOutput+1))
	waitErr := command.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if len(output) > maxCommandOutput {
		return nil, errors.New("tailscale output exceeded limit")
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return output, nil
}

func runBoundedCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	configureProcessGroup(command)
	output := &limitedBuffer{remaining: maxCommandOutput, authorizationFound: make(chan struct{}, 1)}
	command.Stdout = output
	command.Stderr = output
	err := command.Start()
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case err = <-done:
		case <-output.authorizationFound:
			terminateProcessGroup(command)
			<-done
			err = nil
		case <-ctx.Done():
			terminateProcessGroup(command)
			<-done
			err = ctx.Err()
		}
	}
	if output.isTruncated() {
		return nil, errors.New("tailscale output exceeded limit")
	}
	return output.bytes(), err
}

type limitedBuffer struct {
	mutex              sync.Mutex
	buffer             bytes.Buffer
	remaining          int
	truncated          bool
	authorizationFound chan struct{}
}

func (writer *limitedBuffer) Write(input []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	originalLength := len(input)
	if writer.remaining <= 0 {
		writer.truncated = true
		return originalLength, nil
	}
	if len(input) > writer.remaining {
		writer.truncated = true
		input = input[:writer.remaining]
	}
	written, err := writer.buffer.Write(input)
	writer.remaining -= written
	if parseAuthorizationURL(writer.buffer.Bytes()) != "" {
		select {
		case writer.authorizationFound <- struct{}{}:
		default:
		}
	}
	if err != nil {
		return written, err
	}
	return originalLength, nil
}

func (writer *limitedBuffer) bytes() []byte {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return bytes.Clone(writer.buffer.Bytes())
}

func (writer *limitedBuffer) isTruncated() bool {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.truncated
}
