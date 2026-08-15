package credentials

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const keychainService = "sh.nabu.credentials"

const keychainWriteProgram = `
log_user 0
set timeout 15
set secret [gets stdin]
set account [gets stdin]
set service [gets stdin]
spawn -noecho /usr/bin/security add-generic-password -a $account -s $service -U -w
expect {
    -re {(?i)password[^:]*:} { send -- "$secret\r"; exp_continue }
    eof {}
    timeout { unset secret; exit 124 }
}
set result [wait]
unset secret
exit [lindex $result 3]
`

type commandRunner interface {
	Run(context.Context, string, []string, []byte, bool) ([]byte, error)
}

type Keychain struct{ runner commandRunner }

func NewPlatform() Backend {
	if runtime.GOOS != "darwin" {
		return Unsupported{}
	}
	backend, err := NewKeychain()
	if err != nil {
		return Unsupported{}
	}
	return backend
}

func NewKeychain() (*Keychain, error) {
	if runtime.GOOS != "darwin" {
		return nil, ErrUnsupported
	}
	if _, err := exec.LookPath("/usr/bin/security"); err != nil {
		return nil, fmt.Errorf("credentials: locate macOS Keychain tool: %w", err)
	}
	if _, err := exec.LookPath("/usr/bin/expect"); err != nil {
		return nil, fmt.Errorf("credentials: locate macOS pseudo-terminal helper: %w", err)
	}
	return &Keychain{runner: osCommandRunner{}}, nil
}

func (k *Keychain) Put(ctx context.Context, ref Ref, secret *Secret) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if k == nil || k.runner == nil {
		return ErrUnsupported
	}
	value, err := secret.Bytes()
	if err != nil {
		return err
	}
	defer wipe(value)
	accountName := account(ref)
	stdin := make([]byte, 0, len(value)+len(accountName)+len(keychainService)+3)
	stdin = append(stdin, value...)
	stdin = append(stdin, '\n')
	stdin = append(stdin, accountName...)
	stdin = append(stdin, '\n')
	stdin = append(stdin, keychainService...)
	stdin = append(stdin, '\n')
	defer wipe(stdin)
	// macOS security requires a terminal for its -w prompt. The fixed expect
	// program supplies only that pseudo-terminal; it reads the secret from
	// stdin with logging disabled. Account metadata follows on separate lines;
	// expect -c does not provide Tcl argv variables on the system version.
	_, err = k.runner.Run(ctx, "/usr/bin/expect", []string{
		"-c", keychainWriteProgram,
	}, stdin, false)
	if err != nil {
		return fmt.Errorf("credentials: store Keychain item: %w", err)
	}
	return nil
}

func (k *Keychain) Get(ctx context.Context, ref Ref) (*Secret, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if k == nil || k.runner == nil {
		return nil, ErrUnsupported
	}
	value, err := k.runner.Run(ctx, "/usr/bin/security", []string{
		"find-generic-password", "-a", account(ref), "-s", keychainService, "-w",
	}, nil, true)
	if err != nil {
		if isMissing(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("credentials: read Keychain item: %w", err)
	}
	raw := value
	value = trimOneNewline(value)
	secret, createErr := NewSecret(value)
	wipe(raw)
	return secret, createErr
}

func (k *Keychain) Delete(ctx context.Context, ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if k == nil || k.runner == nil {
		return ErrUnsupported
	}
	_, err := k.runner.Run(ctx, "/usr/bin/security", []string{
		"delete-generic-password", "-a", account(ref), "-s", keychainService,
	}, nil, false)
	if err != nil {
		if isMissing(err) {
			return ErrNotFound
		}
		return fmt.Errorf("credentials: delete Keychain item: %w", err)
	}
	return nil
}

func account(ref Ref) string {
	raw := ref.WorkspaceID + "\x00" + ref.Integration + "\x00" + ref.Name
	return "v1:" + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, name string, args []string, stdin []byte, captureStdout bool) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	if captureStdout {
		command.Stdout = &stdout
	}
	var stderr boundedBuffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		// stdout may be secret output and is intentionally discarded on every
		// error. stderr is bounded and never receives a supplied secret.
		wipe(stdout.Bytes())
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return nil, err
		}
		return nil, &commandError{cause: err, message: message}
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

type commandError struct {
	cause   error
	message string
}

func (e *commandError) Error() string { return e.message }
func (e *commandError) Unwrap() error { return e.cause }

func isMissing(err error) bool {
	return errors.Is(err, ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "could not be found") ||
		strings.Contains(strings.ToLower(err.Error()), "item not found")
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if remaining := 4096 - b.Len(); remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return original, nil
}
