package daemon

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/store/storetest"
)

// running is a daemon serving in a test.
type running struct {
	daemon   *Daemon
	layout   paths.Layout
	endpoint Endpoint
	served   chan error
}

// serve starts a daemon and waits until it is listening.
//
// The daemon is stopped and its result checked when the test ends, so a test
// cannot pass while leaving a daemon behind or hiding a shutdown failure.
func serve(t *testing.T, opts Options) *running {
	t.Helper()

	if opts.Layout.Runtime == "" {
		opts.Layout = testLayout(t)
	}
	if opts.Build == (Build{}) {
		opts.Build = testBuild
	}
	// Heartbeats are not wanted in a test: they would add timing to assertions
	// about ordering.
	if opts.Heartbeat == 0 {
		opts.Heartbeat = -1
	}

	ready := make(chan Endpoint, 1)
	userReady := opts.Ready
	opts.Ready = func(endpoint Endpoint) {
		if userReady != nil {
			userReady(endpoint)
		}
		ready <- endpoint
	}

	instance, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- instance.Serve(ctx) }()

	var endpoint Endpoint
	select {
	case endpoint = <-ready:
	case err := <-served:
		t.Fatalf("Serve returned before listening: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not start listening")
	}

	live := &running{daemon: instance, layout: opts.Layout, endpoint: endpoint, served: served}
	t.Cleanup(func() {
		stop()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the daemon did not shut down")
		}
	})
	return live
}

// client returns a client for the running daemon.
//
// It is closed when the test ends: a client that keeps a connection open makes
// the daemon's shutdown wait for it, which turns every test into a slow one.
func (r *running) client(t *testing.T) *client.Client {
	t.Helper()

	caller := client.New(r.layout.Socket)
	t.Cleanup(caller.Close)
	return caller
}

// TestTwoClientsQueryConcurrently is the slice 2 acceptance criterion that two
// clients can query the daemon at the same time. Two operating-system processes
// doing it are covered by the opt-in test in integration_test.go; this checks the
// daemon under concurrent load, which is the part that can break silently.
func TestTwoClientsQueryConcurrently(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), storetest.Task())

	first, second := live.client(t), live.client(t)

	const requests = 25
	var group sync.WaitGroup
	failures := make(chan error, 4*requests)

	for _, caller := range []*client.Client{first, second} {
		for i := 0; i < requests; i++ {
			group.Add(2)
			go func() {
				defer group.Done()
				if _, err := caller.Health(context.Background()); err != nil {
					failures <- err
				}
			}()
			go func() {
				defer group.Done()
				tasks, err := caller.Tasks(context.Background())
				switch {
				case err != nil:
					failures <- err
				case len(tasks) != 1:
					failures <- errors.New("a concurrent read saw the wrong number of tasks")
				}
			}()
		}
	}

	group.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("concurrent request failed: %v", err)
	}
}

func TestHealthReportsTheRunningDaemon(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), nil)

	health, err := live.client(t).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	if health.Status != api.StatusOK {
		t.Errorf("status = %q, want %q (detail: %s)", health.Status, api.StatusOK, health.Detail)
	}
	if health.APIVersion != api.Version {
		t.Errorf("api version = %q, want %q", health.APIVersion, api.Version)
	}
	if health.Daemon.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", health.Daemon.PID, os.Getpid())
	}
	if health.Daemon.Version != testBuild.Version {
		t.Errorf("version = %q, want %q", health.Daemon.Version, testBuild.Version)
	}
	if health.Daemon.Socket != live.layout.Socket {
		t.Errorf("socket = %q, want %q", health.Daemon.Socket, live.layout.Socket)
	}
	if health.State.Directory != live.layout.State {
		t.Errorf("state directory = %q, want %q", health.State.Directory, live.layout.State)
	}
	if health.State.Projects != 1 {
		t.Errorf("projects = %d, want 1", health.State.Projects)
	}
}

// TestServeStreamsEventsInOrder is the slice 2 acceptance criterion that state
// events arrive through SSE in order, checked over a real socket with two
// connected clients.
//
// Slice 2 has no writer of persistent state, so the events are published
// directly; the bus, the SSE encoding, the socket, and the client parser in the
// path are the real ones (ADR-027).
func TestServeStreamsEventsInOrder(t *testing.T) {
	live := serve(t, Options{})

	const count = 40
	type stream struct {
		sequences []uint64
		details   []string
		err       error
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	results := make(chan stream, 2)
	var connected sync.WaitGroup
	connected.Add(2)

	for i := 0; i < 2; i++ {
		go func() {
			var seen stream
			opened := false
			err := live.client(t).Events(ctx, func(e api.Event) error {
				switch e.Kind {
				case api.KindHello:
					if !opened {
						opened = true
						connected.Done()
					}
				case api.KindTask:
					seen.sequences = append(seen.sequences, e.StreamSequence)
					seen.details = append(seen.details, e.TaskEvent.Detail)
					if len(seen.sequences) == count {
						return errStopReading
					}
				default:
					seen.err = errors.New("unexpected event kind " + string(e.Kind))
				}
				return nil
			})
			if err != nil && !errors.Is(err, errStopReading) && seen.err == nil {
				seen.err = err
			}
			results <- seen
		}()
	}

	// Publishing before both clients are connected would leave one of them
	// short, which says nothing about ordering.
	connected.Wait()

	for i := 0; i < count; i++ {
		live.daemon.Publish(domain.Event{
			ProjectID:  storetest.ProjectID,
			TaskID:     storetest.TaskID,
			Type:       domain.EventWorkflowChanged,
			Detail:     "change " + itoa(i),
			OccurredAt: time.Now(),
		})
	}

	for i := 0; i < 2; i++ {
		select {
		case seen := <-results:
			if seen.err != nil {
				t.Fatalf("stream failed: %v", seen.err)
			}
			if len(seen.sequences) != count {
				t.Fatalf("received %d events, want %d", len(seen.sequences), count)
			}
			for index, sequence := range seen.sequences {
				if want := uint64(index + 1); sequence != want {
					t.Fatalf("event %d has sequence %d, want %d", index, sequence, want)
				}
				if want := "change " + itoa(index); seen.details[index] != want {
					t.Fatalf("event %d is %q, want %q", index, seen.details[index], want)
				}
			}
		case <-time.After(15 * time.Second):
			t.Fatal("a stream did not deliver its events")
		}
	}
}

// errStopReading ends a test's event loop without failing it.
var errStopReading = errors.New("test has seen enough events")

func TestServeRefusesASecondDaemonOnTheSameSocket(t *testing.T) {
	live := serve(t, Options{})

	second, err := New(Options{Layout: live.layout, Build: testBuild})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = second.Serve(context.Background())

	var already *AlreadyRunningError
	if !errors.As(err, &already) {
		t.Fatalf("error = %v, want an *AlreadyRunningError", err)
	}
	// The first daemon is untouched.
	if _, healthErr := live.client(t).Health(context.Background()); healthErr != nil {
		t.Errorf("the running daemon stopped answering: %v", healthErr)
	}
}

// TestShutdownReleasesOwnership checks that a daemon which is asked to stop
// leaves the runtime directory claimable, including while a client is streaming
// events: an event stream must not hold shutdown open.
func TestShutdownReleasesOwnership(t *testing.T) {
	layout := testLayout(t)

	instance, err := New(Options{Layout: layout, Build: testBuild, Heartbeat: -1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ready := make(chan Endpoint, 1)
	instance.opts.Ready = func(endpoint Endpoint) { ready <- endpoint }

	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- instance.Serve(ctx) }()

	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not start listening")
	}

	streaming, endStreaming := context.WithCancel(context.Background())
	defer endStreaming()
	connected := make(chan struct{})
	go func() {
		_ = client.New(layout.Socket).Events(streaming, func(e api.Event) error {
			if e.Kind == api.KindHello {
				close(connected)
			}
			return nil
		})
	}()
	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("the event stream did not open")
	}

	stop()

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown waited for the event stream")
	}

	for _, path := range []string{layout.Socket, layout.EndpointFile()} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived shutdown; err = %v", path, err)
		}
	}
	if Answering(layout.Socket) {
		t.Error("the socket still answers after shutdown")
	}
}

// seed writes state directly through the daemon's own store, which is how a test
// arranges state that slices 3 and 6 will write for real.
func seed(t *testing.T, live *running, project *domain.Project, task *domain.Task) {
	t.Helper()

	ctx := context.Background()
	if project != nil {
		if err := live.daemon.Store().Projects().Save(ctx, project); err != nil {
			t.Fatalf("saving the project fixture: %v", err)
		}
	}
	if task != nil {
		if err := live.daemon.Store().Tasks().Save(ctx, task); err != nil {
			t.Fatalf("saving the task fixture: %v", err)
		}
	}
}

// itoa avoids a fmt import in the assertions above.
func itoa(value int) string { return strconv.Itoa(value) }
