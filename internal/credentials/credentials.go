// Package credentials stores integration secrets outside Nabu's operational
// database. Secret values deliberately cannot be formatted or JSON encoded.
package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
)

var (
	ErrNotFound    = errors.New("credentials: not found")
	ErrUnsupported = errors.New("credentials: secure storage is unsupported on this platform")
	identifier     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type Ref struct {
	WorkspaceID string `json:"workspace_id"`
	Integration string `json:"integration"`
	Name        string `json:"name"`
}

func (r Ref) Validate() error {
	for label, value := range map[string]string{
		"workspace ID": r.WorkspaceID, "integration": r.Integration, "name": r.Name,
	} {
		if !identifier.MatchString(value) {
			return fmt.Errorf("credentials: invalid %s", label)
		}
	}
	return nil
}

func (r Ref) key() string { return r.WorkspaceID + "\x00" + r.Integration + "\x00" + r.Name }

// Secret owns mutable secret bytes. Formatting is always redacted and JSON
// serialization is rejected, preventing accidental persistence or logging.
type Secret struct {
	mu    sync.Mutex
	value []byte
}

func NewSecret(value []byte) (*Secret, error) {
	if len(value) == 0 {
		return nil, errors.New("credentials: secret is empty")
	}
	if len(value) > 64*1024 {
		return nil, errors.New("credentials: secret exceeds 64 KiB")
	}
	return &Secret{value: append([]byte(nil), value...)}, nil
}

func (s *Secret) Bytes() ([]byte, error) {
	if s == nil {
		return nil, errors.New("credentials: nil secret")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.value) == 0 {
		return nil, errors.New("credentials: secret was destroyed")
	}
	return append([]byte(nil), s.value...), nil
}

func (s *Secret) Destroy() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	wipe(s.value)
	s.value = nil
}

func (*Secret) String() string   { return "[REDACTED]" }
func (*Secret) GoString() string { return "credentials.Secret([REDACTED])" }
func (*Secret) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[REDACTED]")
}
func (*Secret) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credentials: secrets cannot be JSON encoded")
}

var _ fmt.Stringer = (*Secret)(nil)
var _ fmt.GoStringer = (*Secret)(nil)
var _ json.Marshaler = (*Secret)(nil)

type Backend interface {
	Put(context.Context, Ref, *Secret) error
	Get(context.Context, Ref) (*Secret, error)
	Delete(context.Context, Ref) error
}

// Memory is a concurrency-safe backend for tests and ephemeral sessions.
type Memory struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMemory() *Memory { return &Memory{values: make(map[string][]byte)} }

func (m *Memory) Put(_ context.Context, ref Ref, secret *Secret) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	value, err := secret.Bytes()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if previous := m.values[ref.key()]; previous != nil {
		wipe(previous)
	}
	m.values[ref.key()] = value
	return nil
}

func (m *Memory) Get(_ context.Context, ref Ref) (*Secret, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	value, ok := m.values[ref.key()]
	if ok {
		value = append([]byte(nil), value...)
	}
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	secret, err := NewSecret(value)
	wipe(value)
	return secret, err
}

func (m *Memory) Delete(_ context.Context, ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[ref.key()]
	if !ok {
		return ErrNotFound
	}
	wipe(value)
	delete(m.values, ref.key())
	return nil
}

type Unsupported struct{}

func (Unsupported) Put(context.Context, Ref, *Secret) error   { return ErrUnsupported }
func (Unsupported) Get(context.Context, Ref) (*Secret, error) { return nil, ErrUnsupported }
func (Unsupported) Delete(context.Context, Ref) error         { return ErrUnsupported }

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func trimOneNewline(value []byte) []byte {
	if len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
	}
	if len(value) > 0 && value[len(value)-1] == '\r' {
		value = value[:len(value)-1]
	}
	return value
}
