package tunnel

import (
	"sync"

	"github.com/kayaramazan/latch/proto/model"
)

// EventSink receives AccessEvents (the durable events.Store, or an EventBuffer
// in tests).
type EventSink interface {
	Append(ev model.AccessEvent)
}

// DefaultEventBufferSize is how many recent AccessEvents latchd keeps in memory
// (phase 7 adds durable storage).
const DefaultEventBufferSize = 2048

// EventBuffer is a bounded in-memory ring of AccessEvents with live
// subscriptions for `latchd events --follow`. It is safe for concurrent use.
type EventBuffer struct {
	mu    sync.Mutex
	ring  []model.AccessEvent
	size  int
	next  int
	count int
	subs  map[int]chan model.AccessEvent
	subID int
}

// NewEventBuffer builds a ring of the given capacity.
func NewEventBuffer(size int) *EventBuffer {
	if size <= 0 {
		size = DefaultEventBufferSize
	}
	return &EventBuffer{ring: make([]model.AccessEvent, size), size: size, subs: map[int]chan model.AccessEvent{}}
}

// Append stores an event and fans it out to subscribers (dropping if a
// subscriber is slow).
func (b *EventBuffer) Append(ev model.AccessEvent) {
	b.mu.Lock()
	b.ring[b.next] = ev
	b.next = (b.next + 1) % b.size
	if b.count < b.size {
		b.count++
	}
	subs := make([]chan model.AccessEvent, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Recent returns up to n most recent events, oldest first. n<=0 returns all.
func (b *EventBuffer) Recent(n int) []model.AccessEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > b.count {
		n = b.count
	}
	out := make([]model.AccessEvent, 0, n)
	start := (b.next - b.count + b.size) % b.size
	for i := b.count - n; i < b.count; i++ {
		out = append(out, b.ring[(start+i)%b.size])
	}
	return out
}

// Subscribe returns a channel of future events and a cancel function.
func (b *EventBuffer) Subscribe() (<-chan model.AccessEvent, func()) {
	ch := make(chan model.AccessEvent, 128)
	b.mu.Lock()
	b.subID++
	id := b.subID
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
}
