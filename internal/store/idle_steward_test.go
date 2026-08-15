package store

import (
	"context"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestIdleStewardMinimumDurationCooldownAndRestartState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "idle-steward", Name: "Idle steward", Path: "/idle-steward"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	due, err := s.RecordIdleCheck(ctx, workspace.ID, now, 15*time.Minute, 30*time.Minute)
	if err != nil || due {
		t.Fatalf("first check = due %t, error %v", due, err)
	}
	for check := 0; check < 100; check++ {
		due, err = s.RecordIdleCheck(ctx, workspace.ID, now.Add(14*time.Minute), 15*time.Minute, 30*time.Minute)
		if err != nil || due {
			t.Fatalf("early wake %d = due %t, error %v", check, due, err)
		}
	}
	due, err = s.RecordIdleCheck(ctx, workspace.ID, now.Add(15*time.Minute), 15*time.Minute, 30*time.Minute)
	if err != nil || !due {
		t.Fatalf("duration check = due %t, error %v", due, err)
	}
	state, err := s.GetIdleStewardState(ctx, workspace.ID)
	if err != nil || state.EmptyChecks != 0 || state.NextRunAt == nil || !state.NextRunAt.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("leased state = %#v, error %v", state, err)
	}

	for check := 0; check < 40; check++ {
		due, err = s.RecordIdleCheck(ctx, workspace.ID, now.Add(20*time.Minute), 15*time.Minute, 30*time.Minute)
		if err != nil || due {
			t.Fatalf("cooldown check %d = due %t, error %v", check, due, err)
		}
	}
	if err := s.CompleteIdleSteward(ctx, workspace.ID, now, now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	state, err = s.GetIdleStewardState(ctx, workspace.ID)
	if err != nil || state.LastRunAt == nil || state.NextRunAt == nil || !state.NextRunAt.Equal(now.Add(6*time.Hour)) {
		t.Fatalf("completed state = %#v, error %v", state, err)
	}
}

func TestResetIdleChecksPreservesCooldown(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "idle-reset", Name: "Idle reset", Path: "/idle-reset"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	if _, err := s.RecordIdleCheck(ctx, workspace.ID, now, 15*time.Minute, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetIdleChecks(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	state, err := s.GetIdleStewardState(ctx, workspace.ID)
	if err != nil || state.EmptyChecks != 0 || state.NextRunAt != nil {
		t.Fatalf("active idle window was not cleared: %#v, %v", state, err)
	}
	if err := s.CompleteIdleSteward(ctx, workspace.ID, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetIdleChecks(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	state, err = s.GetIdleStewardState(ctx, workspace.ID)
	if err != nil || state.EmptyChecks != 0 || state.NextRunAt == nil || !state.NextRunAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("reset state = %#v, error %v", state, err)
	}
}
