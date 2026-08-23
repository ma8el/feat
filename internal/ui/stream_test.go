package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// streamBackend behaves the way the daemon behaves.
//
// The detail that matters is the first item: the daemon opens every stream with
// a hello, so that a client learns the connection is live before anything has
// happened (ADR-027). A fake that published nothing until asked lets a dashboard
// that reconnects per event look correct, because the reconnection produces no
// event to reconnect for. That is why the defect these tests pin survived a
// suite that already had a fake event stream in it.
type streamBackend struct {
	*fakeBackend

	mu       sync.Mutex
	opened   int
	incoming chan api.Event
}

func newStreamBackend() *streamBackend {
	return &streamBackend{fakeBackend: newFakeBackend(), incoming: make(chan api.Event, 256)}
}

func (b *streamBackend) Events(ctx context.Context, handle func(api.Event) error) error {
	b.mu.Lock()
	b.opened++
	b.mu.Unlock()

	if err := handle(api.Event{Kind: api.KindHello, Detail: "connected"}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-b.incoming:
			if err := handle(event); err != nil {
				return err
			}
		}
	}
}

func (b *streamBackend) publish(event api.Event) { b.incoming <- event }

func (b *streamBackend) streams() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opened
}

// TestReceivingAnEventOpensNoEventStream is the defect, stated exactly.
//
// A dashboard that answers an event by subscribing again is driven by its own
// reconnections, because the answer to a connection is a hello and the answer to
// a hello would be another connection. It ran at the speed of the socket, leaked
// a connection, a goroutine, and a subscriber on each side of it every time
// round, and exhausted the machine's file descriptors within a minute — which
// then surfaced as unrelated-looking failures elsewhere, including a Git command
// that could not be started being reported as a missing repository.
func TestReceivingAnEventOpensNoEventStream(t *testing.T) {
	backend := newStreamBackend()
	model := New(Options{Backend: backend, Context: t.Context()})

	// Whatever the dashboard does about an event, none of it may be a
	// connection. The commands are run, because a command that opens one opens
	// it when it runs rather than when it is built.
	_, cmd := model.Update(eventMsg{event: api.Event{Kind: api.KindTask}})
	if cmd == nil {
		t.Fatal("an event produced no command")
	}
	runCommands(t, cmd)

	if opened := backend.streams(); opened != 0 {
		t.Errorf("reacting to one event opened %d event streams, want 0: "+
			"a dashboard that reconnects on every event is a loop", opened)
	}
}

// TestTheDashboardHoldsOneEventStreamOpen checks the whole arrangement: one
// connection, however much the daemon publishes over it.
func TestTheDashboardHoldsOneEventStreamOpen(t *testing.T) {
	backend := newStreamBackend()
	model := New(Options{Backend: backend, Context: t.Context()})

	// connect is what Init runs, once, and it lasts as long as the stream does.
	ended := make(chan tea.Msg, 1)
	go func() { ended <- model.connect()() }()

	const published = 50
	for range published {
		backend.publish(api.Event{Kind: api.KindTask})
	}

	// Follow the chain the program follows: an item becomes a message, the
	// message is applied, and the dashboard waits for the next item. The hello
	// is the first of them, so there is one more item than was published.
	for i := range published {
		message := waitFor(t, model.awaitEvent())
		event, ok := message.(eventMsg)
		if !ok {
			t.Fatalf("item %d arrived as %T, want an event", i, message)
		}
		updated, _ := model.Update(event)
		model = updated.(Model)
	}

	if opened := backend.streams(); opened != 1 {
		t.Errorf("the dashboard opened %d event streams to receive %d events, want 1",
			opened, published)
	}

	select {
	case message := <-ended:
		t.Fatalf("the stream ended while the dashboard was still using it: %v", message)
	default:
	}
}

// TestAnEndedStreamIsNotReopened records the deliberate choice.
//
// v0.1 answers a lost stream by re-reading current state rather than by resuming
// (ADR-027). Reconnecting needs a decision about how often, and the absence of
// that decision is what made the reconnect-per-event loop possible; a test says
// so, so that adding one is a change somebody makes on purpose.
func TestAnEndedStreamIsNotReopened(t *testing.T) {
	backend := newStreamBackend()
	model := New(Options{Backend: backend, Context: t.Context()})

	updated, cmd := model.Update(streamMsg{err: errTest})
	model = updated.(Model)

	if cmd != nil {
		runCommands(t, cmd)
	}
	if opened := backend.streams(); opened != 0 {
		t.Errorf("an ended stream was reopened %d times, want 0", opened)
	}
	if !strings.Contains(model.status, "refreshing every") {
		t.Errorf("an ended stream did not say what happens instead: %q", model.status)
	}
}

// runCommands runs every command a batch holds, giving each one a moment to do
// whatever it was going to do.
//
// A command that has not finished is left running rather than waited for: the
// dashboard's commands block by design — awaitEvent waits for an item that may
// never come — and what is under test is what they did, not whether they
// returned.
//
// A batch inside a batch is followed into, as Bubble Tea's own loop follows one:
// an update that batches its work and has that batched again with the loading
// indicator's first frame produces exactly that shape, and a helper that stopped
// at the first level would report that the work never ran.
func runCommands(t *testing.T, cmd tea.Cmd) {
	t.Helper()

	batch, ok := run(cmd).(tea.BatchMsg)
	if !ok {
		return
	}
	for _, command := range batch {
		runCommands(t, command)
	}
}

// settleFor bounds a test rather than describing anything real.
const settleFor = 250 * time.Millisecond

// run executes one command, giving up on a command that blocks.
//
// A nil command is nothing to run rather than a panic: an update that decided to
// do nothing returns one, and a test that drives several messages should not
// have to know which of them did.
func run(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	messages := make(chan tea.Msg, 1)
	go func() { messages <- cmd() }()

	select {
	case message := <-messages:
		return message
	case <-time.After(settleFor):
		return nil
	}
}

// waitFor executes one command that is expected to answer.
func waitFor(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()

	messages := make(chan tea.Msg, 1)
	go func() { messages <- cmd() }()

	select {
	case message := <-messages:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("the dashboard never received the item the daemon published")
		return nil
	}
}
