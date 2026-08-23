package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// event builds a distinguishable stream item.
func event(detail string) api.Event {
	return api.Event{Kind: api.KindTask, Detail: detail, TaskEvent: &api.TaskEvent{Detail: detail}}
}

func TestBusAssignsSequencesFromOne(t *testing.T) {
	bus := NewBus(0)

	for want := uint64(1); want <= 3; want++ {
		if got := bus.Publish(event("x")); got != want {
			t.Errorf("Publish returned %d, want %d", got, want)
		}
	}
	if got := bus.Sequence(); got != 3 {
		t.Errorf("Sequence = %d, want 3", got)
	}
}

// TestBusDeliversEveryEventInOrder is the delivery half of the rule that state
// events arrive in order. The transport half is
// covered over a real socket in TestServeStreamsEventsInOrder.
func TestBusDeliversEveryEventInOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewBus(64)
	first := bus.Subscribe(ctx)
	second := bus.Subscribe(ctx)

	const count = 50
	for i := 0; i < count; i++ {
		bus.Publish(event("event"))
	}

	// Both subscribers see the same sequence, in the same order, with no gaps:
	// two clients of one daemon must not disagree about what happened.
	for name, events := range map[string]<-chan api.Event{"first": first, "second": second} {
		for want := uint64(1); want <= count; want++ {
			select {
			case got, open := <-events:
				if !open {
					t.Fatalf("%s subscriber was dropped at event %d", name, want)
				}
				if got.StreamSequence != want {
					t.Fatalf("%s subscriber received sequence %d, want %d", name, got.StreamSequence, want)
				}
			case <-time.After(time.Second):
				t.Fatalf("%s subscriber did not receive event %d", name, want)
			}
		}
	}
}

// TestBusEndsAStreamThatFallsBehind pins the choice in ADR-027: a subscriber
// that cannot keep up is dropped, and its channel closes, so the stream can
// report the loss. Silently skipping events would make the stream a claim about
// state rather than a record of it.
func TestBusEndsAStreamThatFallsBehind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const capacity = 4
	bus := NewBus(capacity)
	events := bus.Subscribe(ctx)

	// Nothing reads, so the buffer fills and the next publish drops it.
	for i := 0; i < capacity+3; i++ {
		bus.Publish(event("event"))
	}

	delivered := 0
	for range events {
		delivered++
	}

	if delivered != capacity {
		t.Errorf("delivered %d events before the drop, want %d", delivered, capacity)
	}
	if got := bus.Subscribers(); got != 0 {
		t.Errorf("Subscribers = %d after a drop, want 0", got)
	}
}

// TestBusPublishDoesNotBlockOnAStuckSubscriber checks the property that makes
// the daemon safe to publish from: one client that stops reading must not stop
// the daemon from recording what happened.
func TestBusPublishDoesNotBlockOnAStuckSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewBus(1)
	bus.Subscribe(ctx)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10_000; i++ {
			bus.Publish(event("event"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked on a subscriber that never reads")
	}
}

func TestSubscriptionEndsWithItsContext(t *testing.T) {
	bus := NewBus(4)

	ctx, cancel := context.WithCancel(context.Background())
	events := bus.Subscribe(ctx)
	if got := bus.Subscribers(); got != 1 {
		t.Fatalf("Subscribers = %d, want 1", got)
	}

	cancel()

	// The channel closes, which is how a disconnected client releases its
	// buffer.
	select {
	case _, open := <-events:
		if open {
			t.Error("an event arrived after the subscription ended")
		}
	case <-time.After(time.Second):
		t.Fatal("the subscription did not end with its context")
	}

	deadline := time.Now().Add(time.Second)
	for bus.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("Subscribers = %d, want 0", bus.Subscribers())
		}
		time.Sleep(time.Millisecond)
	}
}
