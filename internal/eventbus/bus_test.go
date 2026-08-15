package eventbus

import (
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestPublishAndUnsubscribe(t *testing.T) {
	bus := New()
	stream, unsubscribe := bus.Subscribe()
	bus.Publish(domain.Event{Type: "task.created"})
	select {
	case event := <-stream:
		if event.Type != "task.created" {
			t.Fatalf("unexpected event: %s", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	unsubscribe()
	if _, ok := <-stream; ok {
		t.Fatal("subscription was not closed")
	}
}
