package eventbus

import (
	"sync"

	"github.com/nabu-sh/nabu/internal/domain"
)

type Bus struct {
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[uint64]chan domain.Event
	closed      bool
}

func New() *Bus {
	return &Bus{subscribers: make(map[uint64]chan domain.Event)}
}

func (b *Bus) Publish(event domain.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for _, subscriber := range b.subscribers {
		select {
		case subscriber <- event:
		default:
			// Live events are hints; the durable API remains authoritative.
			// A slow browser reloads current state instead of blocking Nabu.
		}
	}
}

func (b *Bus) Subscribe() (<-chan domain.Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	channel := make(chan domain.Event, 64)
	if b.closed {
		close(channel)
		return channel, func() {}
	}
	b.nextID++
	id := b.nextID
	b.subscribers[id] = channel
	var once sync.Once
	return channel, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if existing, ok := b.subscribers[id]; ok {
				delete(b.subscribers, id)
				close(existing)
			}
		})
	}
}

func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, subscriber := range b.subscribers {
		delete(b.subscribers, id)
		close(subscriber)
	}
}
