package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// frame is one item read off an event stream, in wire terms.
type frame struct {
	// id is the SSE id field, which carries the stream sequence.
	id string
	// event is the SSE event field, which carries the kind.
	event string
	// data is the JSON payload.
	data string
	// comment is set for a comment frame, such as a heartbeat, which is not an
	// event at all.
	comment bool
}

// stream is a connected event stream in a test.
type stream struct {
	service *fakeService
	reader  *bufio.Reader
	cancel  context.CancelFunc
}

// openStream serves the handler on a Unix socket and connects to /v1/events.
//
// The socket is real, because the framing, the flushing, and the headers are the
// thing being tested; a recorder would answer questions about a buffer instead.
func openStream(t *testing.T, heartbeat time.Duration, header http.Header) *stream {
	t.Helper()

	service := newFakeService()
	service.events = make(chan Event, 8)
	handler := NewHandler(Options{Service: service, Heartbeat: heartbeat})

	dir, err := os.MkdirTemp("", "feat")
	if err != nil {
		t.Fatalf("creating a temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "api.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on %s: %v", socket, err)
	}

	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://feat/v1/events", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	for key, values := range header {
		request.Header[key] = values
	}

	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type = %q, want text/event-stream", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache control = %q, want no-store", got)
	}

	return &stream{service: service, reader: bufio.NewReader(response.Body), cancel: cancel}
}

// next reads the next frame, failing the test if none arrives.
func (s *stream) next(t *testing.T) frame {
	t.Helper()

	type result struct {
		frame frame
		err   error
	}
	done := make(chan result, 1)
	go func() {
		read, err := readFrame(s.reader)
		done <- result{frame: read, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("reading a frame: %v", got.err)
		}
		return got.frame
	case <-time.After(5 * time.Second):
		t.Fatal("no frame arrived")
		return frame{}
	}
}

// readFrame reads lines until a blank line ends one frame.
func readFrame(reader *bufio.Reader) (frame, error) {
	var read frame

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return read, err
		}
		line = strings.TrimRight(line, "\n")

		switch {
		case line == "":
			return read, nil
		case strings.HasPrefix(line, ":"):
			read.comment = true
			read.data = strings.TrimSpace(strings.TrimPrefix(line, ":"))
		case strings.HasPrefix(line, "id:"):
			read.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			read.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			read.data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
}

// decode reads a frame's payload.
func (f frame) decode(t *testing.T) Event {
	t.Helper()

	var event Event
	if err := json.Unmarshal([]byte(f.data), &event); err != nil {
		t.Fatalf("decoding %q: %v", f.data, err)
	}
	return event
}

// TestStreamOpensWithHello checks that a client learns the stream is live, and
// that its view of state may be stale, before anything happens.
func TestStreamOpensWithHello(t *testing.T) {
	events := openStream(t, -1, nil)

	opening := events.next(t)

	if opening.event != string(KindHello) {
		t.Errorf("event = %q, want %q", opening.event, KindHello)
	}
	if opening.id != "0" {
		t.Errorf("id = %q, want 0: recorded events start at 1", opening.id)
	}

	hello := opening.decode(t)
	if hello.Kind != KindHello {
		t.Errorf("kind = %q, want %q", hello.Kind, KindHello)
	}
	// The client is told that resuming is not supported, rather than finding out
	// by silently missing events.
	if !strings.Contains(hello.Detail, "resum") {
		t.Errorf("the hello does not mention resuming: %q", hello.Detail)
	}
}

// TestStreamAnswersAResumeAttemptWithResync pins the honest answer to
// Last-Event-ID: Feat keeps no replay buffer, so it says so instead of pretending
// the position was honoured (ADR-027).
func TestStreamAnswersAResumeAttemptWithResync(t *testing.T) {
	events := openStream(t, -1, http.Header{"Last-Event-Id": []string{"41"}})

	if got := events.next(t).event; got != string(KindHello) {
		t.Fatalf("first frame = %q, want hello", got)
	}

	resync := events.next(t)
	if resync.event != string(KindResync) {
		t.Fatalf("second frame = %q, want %q", resync.event, KindResync)
	}
	payload := resync.decode(t)
	if !strings.Contains(payload.Detail, "41") {
		t.Errorf("the resync does not name the requested position: %q", payload.Detail)
	}
}

// TestStreamCarriesEventsWithTheirSequence checks the wire encoding of a
// recorded event, including that the SSE id is the stream sequence.
func TestStreamCarriesEventsWithTheirSequence(t *testing.T) {
	events := openStream(t, -1, nil)
	events.next(t) // hello

	for sequence := uint64(1); sequence <= 3; sequence++ {
		events.service.events <- Event{
			StreamSequence: sequence,
			Kind:           KindTask,
			TaskEvent:      &TaskEvent{Sequence: sequence, Type: "task_workflow_changed", To: "working"},
		}
	}

	for want := uint64(1); want <= 3; want++ {
		got := events.next(t)
		if got.event != string(KindTask) {
			t.Fatalf("event = %q, want %q", got.event, KindTask)
		}
		if got.id != itoa(want) {
			t.Errorf("id = %q, want %q", got.id, itoa(want))
		}
		payload := got.decode(t)
		if payload.StreamSequence != want {
			t.Errorf("stream sequence = %d, want %d", payload.StreamSequence, want)
		}
		if payload.TaskEvent == nil || payload.TaskEvent.Sequence != want {
			t.Errorf("the task event did not survive the wire: %+v", payload.TaskEvent)
		}
	}
}

// TestStreamReportsThatItLostEvents is the reporting half of the backpressure
// policy: the daemon drops a subscriber that falls behind, and the stream says so
// as its last item.
func TestStreamReportsThatItLostEvents(t *testing.T) {
	events := openStream(t, -1, nil)
	events.next(t) // hello

	events.service.events <- Event{StreamSequence: 1, Kind: KindTask, TaskEvent: &TaskEvent{Sequence: 1}}
	if got := events.next(t).id; got != "1" {
		t.Fatalf("id = %q, want 1", got)
	}

	// Closing the channel is what the bus does to a subscriber it dropped.
	close(events.service.events)

	lost := events.next(t)
	if lost.event != string(KindStreamLost) {
		t.Fatalf("event = %q, want %q", lost.event, KindStreamLost)
	}
	payload := lost.decode(t)
	if !strings.Contains(payload.Detail, "1") {
		t.Errorf("the report does not say how far the client got: %q", payload.Detail)
	}
	if !strings.Contains(payload.Detail, "reconnect") {
		t.Errorf("the report does not say what to do: %q", payload.Detail)
	}

	// The stream ends after the report.
	if _, err := readFrame(events.reader); err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("reading past the final item: %v", err)
	} else if err == nil {
		t.Error("the stream continued after reporting that it was lost")
	}
}

// TestStreamSendsHeartbeats checks that an idle stream stays observable, and that
// a heartbeat is a comment rather than an event with a sequence.
func TestStreamSendsHeartbeats(t *testing.T) {
	events := openStream(t, 20*time.Millisecond, nil)
	events.next(t) // hello

	beat := events.next(t)
	if !beat.comment {
		t.Fatalf("frame = %+v, want a comment", beat)
	}
	if beat.id != "" || beat.event != "" {
		t.Errorf("a heartbeat carries an id or event: %+v", beat)
	}
}

// itoa renders a stream sequence for an assertion.
func itoa(value uint64) string { return strconv.FormatUint(value, 10) }
