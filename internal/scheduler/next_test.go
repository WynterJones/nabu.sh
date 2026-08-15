package scheduler

import (
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestNextInterval(t *testing.T) {
	start := time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)
	next, err := Next(domain.Schedule{Enabled: true, IntervalSeconds: 300}, start)
	if err != nil {
		t.Fatal(err)
	}
	if want := start.Add(5 * time.Minute); !next.Equal(want) {
		t.Fatalf("got %s, want %s", next, want)
	}
}

func TestNextCron(t *testing.T) {
	start := time.Date(2026, 8, 12, 10, 31, 20, 0, time.UTC)
	next, err := Next(domain.Schedule{Enabled: true, Expression: "*/15 9-17 * * 1-5"}, start)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 12, 10, 45, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("got %s, want %s", next, want)
	}
}

func TestNextDescriptors(t *testing.T) {
	start := time.Date(2026, 8, 12, 10, 31, 20, 0, time.UTC)
	next, err := Next(domain.Schedule{Enabled: true, Expression: "@daily"}, start)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("got %s, want %s", next, want)
	}
}

func TestRejectsInvalidExpression(t *testing.T) {
	_, err := Next(domain.Schedule{Enabled: true, Expression: "61 * * * *"}, time.Now())
	if err == nil {
		t.Fatal("expected validation error")
	}
}
