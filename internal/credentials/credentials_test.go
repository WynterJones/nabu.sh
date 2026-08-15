package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSecretRedactsAndRejectsSerialization(t *testing.T) {
	secret, err := NewSecret([]byte("highly-sensitive"))
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Destroy()
	formatted := fmt.Sprintf("%s %v %#v", secret, secret, secret)
	if strings.Contains(formatted, "highly-sensitive") || !strings.Contains(formatted, "REDACTED") {
		t.Fatalf("secret formatting = %q", formatted)
	}
	if _, err := json.Marshal(secret); err == nil {
		t.Fatal("secret unexpectedly serialized")
	}
	copy, err := secret.Bytes()
	if err != nil || string(copy) != "highly-sensitive" {
		t.Fatalf("secret bytes = %q, %v", copy, err)
	}
	copy[0] = 'x'
	again, _ := secret.Bytes()
	if string(again) != "highly-sensitive" {
		t.Fatal("Bytes exposed internal storage")
	}
	secret.Destroy()
	if _, err := secret.Bytes(); err == nil {
		t.Fatal("destroyed secret remained readable")
	}
}

func TestMemoryScopesCredentialsAndCopiesValues(t *testing.T) {
	backend := NewMemory()
	ctx := context.Background()
	first := Ref{WorkspaceID: "workspace-1", Integration: "analytics", Name: "token"}
	second := Ref{WorkspaceID: "workspace-2", Integration: "analytics", Name: "token"}
	secretOne, _ := NewSecret([]byte("one"))
	secretTwo, _ := NewSecret([]byte("two"))
	defer secretOne.Destroy()
	defer secretTwo.Destroy()
	if err := backend.Put(ctx, first, secretOne); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, second, secretTwo); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Get(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Destroy()
	value, _ := got.Bytes()
	if string(value) != "one" {
		t.Fatalf("scoped value = %q", value)
	}
	if err := backend.Delete(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Get(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted Get error = %v", err)
	}
	if _, err := backend.Get(ctx, second); err != nil {
		t.Fatalf("delete crossed workspace scope: %v", err)
	}
}

func TestMemoryConcurrentAccess(t *testing.T) {
	backend := NewMemory()
	ref := Ref{WorkspaceID: "workspace", Integration: "integration", Name: "token"}
	var group sync.WaitGroup
	for index := range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			secret, _ := NewSecret([]byte(fmt.Sprintf("value-%d", index)))
			defer secret.Destroy()
			_ = backend.Put(context.Background(), ref, secret)
			value, err := backend.Get(context.Background(), ref)
			if err == nil {
				value.Destroy()
			}
		}()
	}
	group.Wait()
}

func TestRefValidation(t *testing.T) {
	for _, ref := range []Ref{
		{},
		{WorkspaceID: "../escape", Integration: "ok", Name: "token"},
		{WorkspaceID: "ok", Integration: "bad space", Name: "token"},
		{WorkspaceID: "ok", Integration: "ok", Name: "bad/name"},
	} {
		if err := ref.Validate(); err == nil {
			t.Fatalf("invalid ref accepted: %#v", ref)
		}
	}
}

func TestKeychainNeverPlacesSecretInArguments(t *testing.T) {
	runner := &recordingRunner{}
	backend := &Keychain{runner: runner}
	ref := Ref{WorkspaceID: "workspace", Integration: "provider", Name: "token"}
	secret, _ := NewSecret([]byte("never-in-argv"))
	defer secret.Destroy()
	if err := backend.Put(context.Background(), ref, secret); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(runner.args, " "), "never-in-argv") {
		t.Fatalf("secret leaked into argv: %q", runner.args)
	}
	wantStdin := "never-in-argv\n" + account(ref) + "\n" + keychainService + "\n"
	if string(runner.stdin) != wantStdin || len(runner.args) != 2 || runner.args[0] != "-c" || !strings.Contains(runner.args[1], "/usr/bin/security add-generic-password") {
		t.Fatalf("Keychain prompt contract args=%q stdin=%q", runner.args, runner.stdin)
	}
	runner.output = []byte("looked-up-secret\n")
	got, err := backend.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Destroy()
	value, _ := got.Bytes()
	if string(value) != "looked-up-secret" {
		t.Fatalf("lookup value = %q", value)
	}
}

type recordingRunner struct {
	args   []string
	stdin  []byte
	output []byte
}

func (r *recordingRunner) Run(_ context.Context, _ string, args []string, stdin []byte, _ bool) ([]byte, error) {
	r.args = append([]string(nil), args...)
	r.stdin = append([]byte(nil), stdin...)
	return append([]byte(nil), r.output...), nil
}
