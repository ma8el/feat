package daemon

import (
	"context"
	"sync"

	"github.com/ma8el/feat/internal/api"
)

// defaultEventBuffer is how many events one subscriber may fall behind before
// its stream is ended. A client that is merely busy catches up well inside this;
// a client that is stuck does not, and that is the case worth reporting.
const defaultEventBuffer = 128

// Bus fans recorded state changes out to connected clients.
//
// Three properties matter, and they are in tension:
//
//   - publishing must never block the daemon, or one stuck client would stop
//     every task's state from being recorded;
//   - a subscriber must see events in the order they were published, or a client
//     could apply an older state after a newer one;
//   - a subscriber that cannot keep up must be told, because a stream that
//     silently skips events is a claim about state rather than a record of it
//     (the choice ADR-026 already made for a damaged event log).
//
// The sequence number is assigned and delivered under one mutex, which gives the
// order; the per-subscriber buffer keeps publication non-blocking; and a
// subscriber whose buffer is full has its channel closed, which the event stream
// reports as a lost stream.
type Bus struct {
	// capacity is the per-subscriber buffer size.
	capacity int

	mu sync.Mutex
	// sequence is the last assigned stream sequence. The opening hello of a
	// stream is 0, so recorded events start at 1.
	sequence uint64
	// nextID names subscribers so one can be removed without identifying it by
	// its channel.
	nextID uint64
	// subscribers holds every connected client.
	subscribers map[uint64]*subscriber
}

// subscriber is one connected client.
type subscriber struct {
	events chan api.Event
	// closeOnce guards the channel close, which both a dropped subscriber and a
	// disconnected client reach.
	closeOnce sync.Once
}

func (s *subscriber) close() {
	s.closeOnce.Do(func() { close(s.events) })
}

// NewBus creates a bus. A capacity of zero uses the default.
func NewBus(capacity int) *Bus {
	if capacity <= 0 {
		capacity = defaultEventBuffer
	}
	return &Bus{capacity: capacity, subscribers: make(map[uint64]*subscriber)}
}

// Publish assigns the next stream sequence and delivers the event to every
// subscriber. It returns the assigned sequence.
//
// It never blocks: a subscriber that cannot accept the event is dropped, and its
// stream ends with a report rather than a gap.
func (b *Bus) Publish(event api.Event) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sequence++
	event.StreamSequence = b.sequence

	for id, sub := range b.subscribers {
		select {
		case sub.events <- event:
		default:
			// Deleting under the same lock that sends is what makes closing the
			// channel safe: no later Publish can reach it.
			delete(b.subscribers, id)
			sub.close()
		}
	}
	return b.sequence
}

// Subscribe registers a client and returns its event channel.
//
// The channel is closed when the context ends or when the subscriber is dropped.
// The caller distinguishes the two by looking at its context, and reports a drop
// as a lost stream.
func (b *Bus) Subscribe(ctx context.Context) <-chan api.Event {
	sub := &subscriber{events: make(chan api.Event, b.capacity)}

	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.subscribers[id] = sub
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.unsubscribe(id)
	}()

	return sub.events
}

// unsubscribe removes a subscriber and closes its channel.
func (b *Bus) unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub, ok := b.subscribers[id]
	if !ok {
		return
	}
	delete(b.subscribers, id)
	sub.close()
}

// Subscribers reports how many clients are connected.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}

// Sequence reports the last assigned stream sequence.
func (b *Bus) Sequence() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sequence
}
