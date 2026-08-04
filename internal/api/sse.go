package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// events serves GET /v1/events as a Server-Sent Events stream.
//
// The stream reports its own health. It opens with a hello, answers a resume
// attempt with an explicit resync rather than a pretended replay, and ends a
// subscriber that fell behind with a stream_lost item instead of quietly
// skipping events (ADR-027).
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	events, err := s.service.Subscribe(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	controller := http.NewResponseController(w)
	// An event stream is open for as long as the client wants it, so the
	// response deadline that protects ordinary requests must be cleared here.
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		s.fail(w, r, fmt.Errorf("this connection cannot stream events: %w", err))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := controller.Flush(); err != nil {
		return
	}

	// The opening item tells the client the stream is live before anything has
	// happened, which is also how it learns that its state may be stale.
	if !s.send(w, controller, Event{StreamSequence: 0, Kind: KindHello, Detail: describeStream()}) {
		return
	}
	if position := r.Header.Get("Last-Event-ID"); position != "" {
		if !s.send(w, controller, Event{
			StreamSequence: 0,
			Kind:           KindResync,
			Detail: "resuming from event " + strconv.Quote(position) +
				" is not supported; read current state and continue from this stream",
		}) {
			return
		}
	}

	heartbeat := s.newHeartbeat()
	defer heartbeat.Stop()

	var delivered uint64
	for {
		select {
		case <-ctx.Done():
			// The client disconnected or the daemon is shutting down. There is
			// nobody left to inform.
			return

		case event, open := <-events:
			if !open {
				// A closed channel with a live context means the subscriber
				// fell too far behind and the daemon dropped it.
				s.send(w, controller, Event{
					StreamSequence: delivered,
					Kind:           KindStreamLost,
					Detail: "this stream fell behind and was ended after event " +
						strconv.FormatUint(delivered, 10) + "; read current state and reconnect",
				})
				return
			}
			if !s.send(w, controller, event) {
				return
			}
			delivered = event.StreamSequence

		case <-heartbeat.C():
			// A comment keeps the connection observable while nothing is
			// happening. It is not an event and carries no sequence.
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}

// send writes one event and reports whether the stream is still usable.
func (s *server) send(w http.ResponseWriter, controller *http.ResponseController, event Event) bool {
	// Event data must be one line, so the compact encoding is required here
	// rather than the indented one used for ordinary responses.
	data, err := json.Marshal(event)
	if err != nil {
		s.logger.Error("dropping an event that cannot be encoded",
			"kind", string(event.Kind), "error", err)
		return true
	}

	var frame []byte
	frame = append(frame, "id: "...)
	frame = strconv.AppendUint(frame, event.StreamSequence, 10)
	frame = append(frame, "\nevent: "...)
	frame = append(frame, event.Kind...)
	frame = append(frame, "\ndata: "...)
	frame = append(frame, data...)
	frame = append(frame, "\n\n"...)

	if _, err := w.Write(frame); err != nil {
		return false
	}
	return controller.Flush() == nil
}

// heartbeat is a ticker that may be disabled.
type heartbeat struct {
	ticker *time.Ticker
}

func (s *server) newHeartbeat() heartbeat {
	if s.heartbeat < 0 {
		return heartbeat{}
	}
	return heartbeat{ticker: time.NewTicker(s.heartbeat)}
}

// C returns the tick channel, or nil when heartbeats are disabled. A receive
// from a nil channel blocks forever, which is exactly the wanted behaviour in
// the select above.
func (h heartbeat) C() <-chan time.Time {
	if h.ticker == nil {
		return nil
	}
	return h.ticker.C
}

func (h heartbeat) Stop() {
	if h.ticker != nil {
		h.ticker.Stop()
	}
}
