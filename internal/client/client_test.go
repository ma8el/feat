package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// serveOnSocket runs a handler on a Unix socket and returns a client for it.
func serveOnSocket(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	socket := filepath.Join(shortDir(t), "feat.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on %s: %v", socket, err)
	}

	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	caller := New(socket)
	t.Cleanup(caller.Close)
	return caller
}

// shortDir returns a temporary directory with a short path, because a socket path
// has a length limit that t.TempDir() can exceed on macOS.
func shortDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "feat")
	if err != nil {
		t.Fatalf("creating a temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestAbsentDaemonIsReportedAsSuch pins the one transport failure a caller can
// act on. "connection refused" would leave the user to work out that they need to
// start a daemon.
func TestAbsentDaemonIsReportedAsSuch(t *testing.T) {
	t.Run("no socket at all", func(t *testing.T) {
		caller := New(filepath.Join(shortDir(t), "feat.sock"))

		_, err := caller.Health(context.Background())

		if !errors.Is(err, ErrDaemonNotRunning) {
			t.Fatalf("error = %v, want ErrDaemonNotRunning", err)
		}
		if !contains(err.Error(), caller.Socket()) {
			t.Errorf("the error does not name the socket: %v", err)
		}
	})

	t.Run("a socket nobody listens on", func(t *testing.T) {
		// This is what a daemon that was killed leaves behind.
		socket := filepath.Join(shortDir(t), "feat.sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatalf("listening: %v", err)
		}
		unix, ok := listener.(*net.UnixListener)
		if !ok {
			t.Fatalf("listener is a %T", listener)
		}
		unix.SetUnlinkOnClose(false)
		if err := unix.Close(); err != nil {
			t.Fatalf("closing: %v", err)
		}

		_, err = New(socket).Health(context.Background())

		if !errors.Is(err, ErrDaemonNotRunning) {
			t.Fatalf("error = %v, want ErrDaemonNotRunning", err)
		}
	})
}

func TestHealthIsDecoded(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Errorf("path = %q, want /v1/health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"ok","api_version":"v1","daemon":{"pid":7,"version":"v9"},"state":{"projects":2}}`)
	}))

	health, err := caller.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	if health.Status != api.StatusOK || health.Daemon.PID != 7 || health.State.Projects != 2 {
		t.Errorf("health = %+v", health)
	}
}

func TestAttachInfoIsRequestedAndDecoded(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/v1/tasks/12345678-1234-4234-8234-123456789abc/attach-info"
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"socket":"/run/feat/tmux.sock","session":"$3","window":"@8","pane":"%13"}`)
	}))

	info, err := caller.AttachInfo(context.Background(), "12345678-1234-4234-8234-123456789abc")
	if err != nil {
		t.Fatalf("AttachInfo: %v", err)
	}
	if info.Socket != "/run/feat/tmux.sock" || info.Session != "$3" || info.Window != "@8" || info.Pane != "%13" {
		t.Errorf("AttachInfo = %+v", info)
	}
}

func TestErrorResponsesBecomeStatusErrors(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error":{"code":"not_found","message":"no task 7f3a1c2e in any registered project"}}`)
	}))

	_, err := caller.Task(context.Background(), "7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c")

	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if !status.NotFound() {
		t.Errorf("status = %d, want 404", status.Status)
	}
	if status.Code != api.CodeNotFound {
		t.Errorf("code = %q, want %q", status.Code, api.CodeNotFound)
	}
	// The daemon's message is what the user sees, because it names the task.
	if !contains(status.Error(), "7f3a1c2e") {
		t.Errorf("the error loses the daemon's explanation: %v", status)
	}
}

// TestAMissingTerminalSurvivesTheSocket is what the dashboard's empty state
// depends on.
//
// The sentinel the daemon wrapped cannot cross a socket, so the classification
// travels as a code and is put back together here. A client that lost it would
// leave the recovery offer to be decided by matching on message text.
func TestAMissingTerminalSurvivesTheSocket(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w,
			`{"error":{"code":"terminal_missing","message":"not found: task 7f3a1c2e has no live tagged terminal"}}`)
	}))

	_, err := caller.TerminalFrame(context.Background(), "7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c",
		api.TerminalView{Width: 80, Height: 24})

	if !api.IsTerminalMissing(err) {
		t.Fatalf("error = %v, want it to report a missing terminal", err)
	}
	// And an ordinary absence still is not one, or the offer would be made for
	// every task identifier a user mistypes.
	other := &StatusError{Status: http.StatusNotFound, Code: api.CodeNotFound, Message: "no task"}
	if api.IsTerminalMissing(other) {
		t.Error("a plain not-found was read as a missing terminal")
	}
}

// TestErrorWithoutABodyStillExplainsItself covers a failure from something that
// is not the daemon, such as a proxy or a half-written response.
func TestErrorWithoutABodyStillExplainsItself(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := caller.Projects(context.Background())

	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if status.Error() == "" {
		t.Error("the error has no message")
	}
}

// TestEventsReadsTheStream checks the parser against the shapes SSE allows:
// comments, multi-line data, and fields the client does not use.
func TestEventsReadsTheStream(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// A heartbeat comment, an event whose data is split across two lines,
		// and a plain event.
		_, _ = fmt.Fprint(w, ": heartbeat\n\n")
		_, _ = fmt.Fprint(w, "id: 1\nevent: task_event\ndata: {\"stream_sequence\":1,\n")
		_, _ = fmt.Fprint(w, "data: \"kind\":\"task_event\"}\n\n")
		_, _ = fmt.Fprint(w, "id: 2\nevent: task_event\ndata: {\"stream_sequence\":2,\"kind\":\"task_event\"}\n\n")
		w.(http.Flusher).Flush()
	}))

	var seen []uint64
	err := caller.Events(context.Background(), func(event api.Event) error {
		seen = append(seen, event.StreamSequence)
		if len(seen) == 2 {
			return io.EOF
		}
		return nil
	})

	if !errors.Is(err, io.EOF) {
		t.Fatalf("Events: %v", err)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Errorf("sequences = %v, want [1 2]", seen)
	}
}

// TestEventsReportsALostStream checks that losing events is an error a caller
// cannot overlook, and that the final item still reaches the handler.
func TestEventsReportsALostStream(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "id: 5\nevent: stream_lost\ndata: {\"stream_sequence\":5,\"kind\":\"stream_lost\","+
			"\"detail\":\"this stream fell behind\"}\n\n")
		w.(http.Flusher).Flush()
	}))

	var kinds []api.EventKind
	err := caller.Events(context.Background(), func(event api.Event) error {
		kinds = append(kinds, event.Kind)
		return nil
	})

	if !errors.Is(err, ErrStreamLost) {
		t.Fatalf("error = %v, want ErrStreamLost", err)
	}
	if len(kinds) != 1 || kinds[0] != api.KindStreamLost {
		t.Errorf("kinds = %v, want the final item to reach the handler", kinds)
	}
	if !contains(err.Error(), "fell behind") {
		t.Errorf("the error drops the daemon's explanation: %v", err)
	}
}

func TestEventsStopsWhenTheHandlerFails(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for sequence := 1; sequence <= 100; sequence++ {
			_, _ = fmt.Fprintf(w, "id: %d\nevent: task_event\ndata: {\"stream_sequence\":%d}\n\n", sequence, sequence)
		}
		w.(http.Flusher).Flush()
	}))

	stop := errors.New("enough")
	handled := 0
	err := caller.Events(context.Background(), func(api.Event) error {
		handled++
		return stop
	})

	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the handler's error", err)
	}
	if handled != 1 {
		t.Errorf("the handler ran %d times after failing once", handled)
	}
}

func TestEventsEndsWithItsContext(t *testing.T) {
	release := make(chan struct{})
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- caller.Events(ctx, func(api.Event) error { return nil })
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end with its context")
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// TestARuntimeActionOutwaitsAnOrdinaryRequest is the regression for a first
// `runtime start` failing with `context deadline exceeded` and working when the
// user tried it again.
//
// Ten seconds is right for a request the daemon answers out of what it already
// knows and wrong for one where it is pulling images and running builds. The
// budgets are shrunk here so that the rule under test is which one the runtime
// endpoint gets, rather than how long either of them is.
func TestARuntimeActionOutwaitsAnOrdinaryRequest(t *testing.T) {
	const work = 300 * time.Millisecond

	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The daemon is busy for longer than an ordinary request may take, which
		// is what a Compose that is pulling an image looks like from here.
		select {
		case <-time.After(work):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"task":{"id":"12345678-1234-4234-8234-123456789abc"},"services":[],"notes":[]}`)
	}))
	caller.timeout = work / 4
	caller.runtimeTimeout = 10 * work

	id := "12345678-1234-4234-8234-123456789abc"
	if _, err := caller.Runtime(context.Background(), id, api.RuntimeStart); err != nil {
		t.Fatalf("a start the daemon was still working on was abandoned: %v", err)
	}

	// And the ordinary budget is still short, so this is a difference between the
	// endpoints rather than a client that waits for ever.
	_, err := caller.Task(context.Background(), id)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a deadline", err)
	}
	// What that deadline reads as: the daemon's socket, how long this client
	// waited, and what became of the work — none of which "context deadline
	// exceeded" says on its own.
	for _, required := range []string{caller.Socket(), caller.timeout.String(), "stopped part way through"} {
		if !contains(err.Error(), required) {
			t.Errorf("the deadline does not mention %q: %v", required, err)
		}
	}
}

// TestTheRuntimeBudgetCoversTheDaemons keeps the two ends of one request in
// agreement.
//
// The daemon stops waiting for Docker at api.RuntimeTimeout. A client that gave
// up first would cancel a request the daemon is still serving, and cancelling
// one kills the `docker compose up` it is waiting on part way through — so the
// client's patience has to be the longer of the two.
func TestTheRuntimeBudgetCoversTheDaemons(t *testing.T) {
	if runtimeTimeout <= api.RuntimeTimeout {
		t.Errorf("the client waits %s for an action the daemon may spend %s on",
			runtimeTimeout, api.RuntimeTimeout)
	}
	if runtimeTimeout <= requestTimeout {
		t.Errorf("a runtime action waits %s, no longer than an ordinary request's %s",
			runtimeTimeout, requestTimeout)
	}
}

// TestTheAgentBudgetCoversTheDaemons is the same agreement for the three
// requests that create or stop a task's agent environment.
//
// It is the regression for the launch that found the rule: `POST
// /v1/task-drafts/{id}/launch` failed after 10.018 seconds with `context
// canceled` while the daemon went on serving it, because the project's own
// Compose file had changed and the service had to be recreated. What the
// cancelled launch left behind — a container the record no longer named — is
// what makes this a data problem rather than a slow one.
func TestTheAgentBudgetCoversTheDaemons(t *testing.T) {
	if agentTimeout <= api.AgentTimeout {
		t.Errorf("the client waits %s for work the daemon may spend %s on",
			agentTimeout, api.AgentTimeout)
	}
	if agentTimeout <= requestTimeout {
		t.Errorf("a launch, resume, or stop waits %s, no longer than an ordinary request's %s",
			agentTimeout, requestTimeout)
	}
}

// TestEveryAgentEnvironmentRequestOutwaitsAnOrdinaryOne checks that all three
// carry the longer budget.
//
// One of the three having been left on the ordinary budget is exactly the defect
// this exists for, and it is invisible until the day a container has to be
// built: the five launches before the one that failed took between 0.46 and 3.13
// seconds.
func TestEveryAgentEnvironmentRequestOutwaitsAnOrdinaryOne(t *testing.T) {
	const work = 300 * time.Millisecond
	const id = "12345678-1234-4234-8234-123456789abc"

	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(work):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
	}))
	caller.timeout = work / 4
	caller.agentTimeout = 10 * work

	for name, request := range map[string]func() error{
		"launch": func() error {
			_, err := caller.LaunchDraft(context.Background(), id, api.Confirmation{Fingerprint: "fingerprint"})
			return err
		},
		"resume": func() error { _, err := caller.Resume(context.Background(), id); return err },
		"stop":   func() error { _, err := caller.Stop(context.Background(), id); return err },
	} {
		if err := request(); err != nil {
			t.Errorf("a %s the daemon was still working on was abandoned: %v", name, err)
		}
	}
}

// TestLaunchSendsTheWholeConfirmation keeps the client from dropping a decision
// the user made on the review screen.
//
// The confirmation is read once, by the launch it accompanies, so a value lost
// here is a task that starts differently from the way the screen said it would
// and nothing later that could tell the user why.
func TestLaunchSendsTheWholeConfirmation(t *testing.T) {
	const id = "12345678-1234-4234-8234-123456789abc"

	var sent string
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
	}))

	if _, err := caller.LaunchDraft(context.Background(), id,
		api.Confirmation{Fingerprint: "abc", PlanFirst: true}); err != nil {
		t.Fatalf("LaunchDraft: %v", err)
	}
	if !strings.Contains(sent, `"fingerprint":"abc"`) || !strings.Contains(sent, `"plan_first":true`) {
		t.Errorf("request body = %s, want the fingerprint and the plan-first decision", sent)
	}

	if _, err := caller.LaunchDraft(context.Background(), id,
		api.Confirmation{Fingerprint: "abc"}); err != nil {
		t.Fatalf("LaunchDraft: %v", err)
	}
	if strings.Contains(sent, "plan_first") {
		t.Errorf("request body = %s, want no plan-first key when it was not asked for", sent)
	}
}

// TestACallersOwnDeadlineIsLeftAlone keeps the client from claiming a deadline
// as its own.
//
// A caller that bounds its own request is told what it asked for, in the words
// context uses, because the number it ran out of is theirs and this client's
// budget never fired.
func TestACallersOwnDeadlineIsLeftAlone(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := caller.Task(ctx, "12345678-1234-4234-8234-123456789abc")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a deadline", err)
	}
	if contains(err.Error(), "did not answer within") {
		t.Errorf("the client claims the caller's own deadline as its own: %v", err)
	}
}

// TestANoContentReplyIsASuccess is the regression for an error on every
// keystroke.
//
// The terminal input endpoint answers 204, which carries no body. Decoding it
// anyway reported the empty body as a broken one, so a user typing into a
// focused pane saw ".../terminal/input: EOF" flash on the screen for every
// character they typed.
func TestANoContentReplyIsASuccess(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/v1/tasks/12345678-1234-4234-8234-123456789abc/terminal/input"
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	err := caller.SendTerminalInput(context.Background(), "12345678-1234-4234-8234-123456789abc",
		api.TerminalInput{Keys: []string{"Enter"}})
	if err != nil {
		t.Errorf("a 204 was reported as a failure: %v", err)
	}
}

// TestATruncatedReplyIsStillAFailure keeps the fix above from swallowing a
// response that really was cut short: an empty body is only success where the
// daemon said there would be no body.
func TestATruncatedReplyIsStillAFailure(t *testing.T) {
	caller := serveOnSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))

	if _, err := caller.AttachInfo(context.Background(),
		"12345678-1234-4234-8234-123456789abc"); err == nil {
		t.Error("an empty 200 was accepted as a decoded response")
	}
}
