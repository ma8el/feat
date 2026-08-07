package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/store/fs"
	"github.com/ma8el/feat/internal/tmux"
)

// Server timeouts.
const (
	// readHeaderTimeout bounds how long a client may take to send its request
	// headers.
	readHeaderTimeout = 10 * time.Second
	// idleTimeout closes a connection that is between requests. It does not
	// apply during a response, so it does not cut an event stream.
	idleTimeout = 2 * time.Minute
	// shutdownTimeout bounds draining in-flight requests. Event streams are
	// ended first, so this only covers ordinary requests.
	shutdownTimeout = 5 * time.Second
)

// Options configure a daemon.
type Options struct {
	// Layout locates the state directory and the runtime directory. It is
	// required.
	Layout paths.Layout
	// Build identifies this binary in health responses and in the endpoint
	// record.
	Build Build
	// Environment is what project configuration is resolved against, chiefly
	// the home directory a leading "~" expands to. A zero value resolves the
	// running process's environment.
	Environment paths.Environment
	// Git runs the Git commands that create and observe task worktrees. A nil
	// value drives the real Git executable; a test supplies its own so that it
	// can arrange failures a real repository makes hard to produce.
	Git git.Runner
	// Tmux runs non-interactive control commands against the dedicated terminal
	// server. A nil value drives the real tmux executable.
	Tmux tmux.Runner
	// Agent runs probe commands for a host-native agent. A devcontainer project
	// probes inside its own container instead, through the execution
	// environment. A nil value probes this host.
	Agent agent.Runner
	// Docker runs the container commands that create and observe a task's
	// devcontainer. A nil value drives the real Docker CLI; a test supplies its
	// own so that a container that refuses to start, or turns out to run as
	// root, can be arranged without a machine in that state.
	Docker compose.Runner
	// RuntimeDocker runs the container commands of a task's application
	// services. It is separate from Docker because the agent's environment and
	// the application under development are separate concepts (ADR-034), and
	// because a test may want an application that fails to start beside an agent
	// container that is perfectly healthy. A nil value drives the real Docker CLI.
	RuntimeDocker runtime.Runner
	// RuntimeInterval is how often application services are observed. Zero uses
	// the default; a negative value disables observation, which only a test that
	// pins what a poll would publish wants.
	RuntimeInterval time.Duration
	// Timer schedules the idle grace period. A nil value uses the wall clock.
	Timer Timer
	// PollInterval is how often control workspaces are read. Zero uses the
	// default.
	PollInterval time.Duration
	// Logger receives the daemon's log. A nil logger discards it.
	Logger *slog.Logger
	// Now supplies the current time. A nil value uses the wall clock.
	Now func() time.Time
	// EventBuffer is how many events one client may fall behind before its
	// stream is ended. Zero uses the default.
	EventBuffer int
	// Heartbeat is the event-stream keepalive interval. Zero uses the default.
	Heartbeat time.Duration
	// Ready is called once the daemon is listening and its endpoint record is
	// published. It lets a caller wait for readiness without polling.
	Ready func(Endpoint)
}

// Daemon is the local orchestration process.
//
// It owns persistent state, publishes state events, and serves the local API
// over a Unix-domain socket. Clients — the TUI and the CLI — reach it through
// that socket and never touch the state directory themselves (ADR-008).
type Daemon struct {
	opts    Options
	logger  *slog.Logger
	now     func() time.Time
	store   store.Store
	bus     *Bus
	service *service
}

// New prepares a daemon: it opens the state store but claims nothing in the
// runtime directory, so constructing one does not interfere with a daemon that
// is already running.
func New(opts Options) (*Daemon, error) {
	if opts.Layout.State == "" || opts.Layout.Runtime == "" {
		return nil, fmt.Errorf("daemon options need a resolved path layout")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	state, err := fs.Open(opts.Layout.State)
	if err != nil {
		return nil, err
	}

	// Configuration is resolved against the daemon's own environment, so that a
	// "~" in project YAML is the user who owns the daemon.
	env := opts.Environment
	if env.Home == "" {
		current, err := paths.Current()
		if err != nil {
			return nil, err
		}
		env = current
	}

	bus := NewBus(opts.EventBuffer)
	terminals, err := tmux.New(opts.Layout.TmuxSocket(), opts.Tmux)
	if err != nil {
		return nil, err
	}

	probe := opts.Agent
	if probe == nil {
		probe = agent.HostRunner{}
	}
	// The opt-in that lets an agent run outside the boundary its project
	// configures is read here, from the daemon's own environment, and never from
	// a request (ADR-032).
	hostAgent := false
	if env.Getenv != nil {
		hostAgent = truthy(env.Getenv(EnvHostAgent))
	}
	if hostAgent {
		logger.Warn("agents will run on this host rather than in a configured devcontainer",
			slog.String("variable", EnvHostAgent))
	}

	return &Daemon{
		opts:   opts,
		logger: logger,
		now:    now,
		store:  state,
		bus:    bus,
		service: &service{
			store:         state,
			bus:           bus,
			git:           git.New(opts.Git),
			terminals:     terminals,
			agent:         claude.New(),
			runner:        probe,
			docker:        opts.Docker,
			runtimeDocker: opts.RuntimeDocker,

			layout:     opts.Layout,
			env:        env,
			hostAgent:  hostAgent,
			build:      opts.Build,
			now:        now,
			logger:     logger,
			idle:       newIdleTimers(opts.Timer),
			startup:    newIdleTimers(opts.Timer),
			workspaces: make(map[domain.TaskID]*control.Workspace),
			pollNow:    make(chan struct{}, 1),
		},
	}, nil
}

// truthy reads an opt-in environment variable.
//
// Only an explicit affirmative counts. A variable someone exported empty, or
// set to "0" while turning the option off, must not move an agent outside the
// boundary its project configured.
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Publish records a state change on the event stream and returns its stream
// sequence.
//
// It is how a later slice reports what it changed: the writer publishes after it
// has persisted, so a client that reacts to an event finds the state the event
// describes.
func (d *Daemon) Publish(event domain.Event) uint64 { return d.service.Publish(event) }

// Store returns the persistent state. It is the daemon's own store, and the
// daemon is its only writer.
func (d *Daemon) Store() store.Store { return d.store }

// Serve claims the runtime directory and serves the local API until the context
// is cancelled.
//
// Shutdown is deliberate rather than abrupt: event streams are ended first, so
// that draining in-flight requests does not wait for connections that are meant
// to stay open, and ownership is released last, so no other daemon can claim the
// socket while this one is still answering on it.
func (d *Daemon) Serve(ctx context.Context) (err error) {
	ownership, err := Acquire(d.opts.Layout, d.opts.Build, d.now(), d.logger)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, ownership.Release())
	}()

	// The endpoint is what health reports about the process, and it exists only
	// once ownership is established. Nothing is serving yet, so this is not a
	// concurrent write.
	d.service.endpoint = ownership.Endpoint()

	// tmux outlives the daemon. Reconcile after taking runtime ownership (so
	// the dedicated socket's parent is known safe) and before clients can read
	// state. A failed observation must not make the recovery UI unavailable;
	// it is logged and the daemon still serves the last recorded state.
	if reconcileErr := d.service.reconcileTmux(ctx); reconcileErr != nil {
		d.logger.ErrorContext(ctx, "reconciling tmux terminals", slog.Any("error", reconcileErr))
	}

	// Control messages also outlive the daemon: an agent that ended a turn
	// while Feat was stopped wrote a file, and that file is still there. Reading
	// them before serving means a client's first request sees the state those
	// messages describe rather than the state from before the restart. The idle
	// grace period is measured from when the turn ended, so a turn that ended
	// long ago becomes idle at once rather than restarting the clock.
	d.service.pollControl(ctx)

	poller := &controlPoller{}
	poller.start(ctx, d.service, d.opts.PollInterval)

	// Application services are observed on their own, much slower schedule. It
	// starts here rather than in the control poller because the two answer
	// different questions and a task with no runtime costs nothing at all: a
	// negative interval turns it off entirely, which only a test wants.
	var runtimes *runtimePoller
	if d.opts.RuntimeInterval >= 0 {
		runtimes = &runtimePoller{}
		runtimes.start(ctx, d.service, d.opts.RuntimeInterval)
	}
	defer func() {
		if runtimes != nil {
			runtimes.stop()
		}
		poller.stop()
		// A pending transition must not fire into a daemon that has stopped and
		// can no longer write what it decided.
		d.service.idle.cancelAll()
		d.service.startup.cancelAll()
	}()

	// Request contexts derive from this one, so cancelling it ends the event
	// streams. Without it, Shutdown would wait for every connected client.
	streams, endStreams := context.WithCancel(context.Background())
	defer endStreams()

	server := &http.Server{
		Handler: api.NewHandler(api.Options{
			Service:   d.service,
			Logger:    d.logger,
			Heartbeat: d.opts.Heartbeat,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		// ReadTimeout and WriteTimeout stay unset on purpose: both apply to a
		// whole request or response, and an event stream is meant to outlive any
		// bound worth setting. Ordinary responses set their own write deadline
		// in internal/api.
		BaseContext: func(net.Listener) context.Context { return streams },
		ErrorLog:    slog.NewLogLogger(d.logger.Handler(), slog.LevelWarn),
	}

	endpoint := ownership.Endpoint()
	d.logger.Info("daemon listening",
		slog.String("socket", endpoint.Socket),
		slog.Int("pid", endpoint.PID),
		slog.String("version", endpoint.Version),
		slog.String("state", d.opts.Layout.State))
	if d.opts.Ready != nil {
		d.opts.Ready(endpoint)
	}

	serving := make(chan error, 1)
	go func() { serving <- server.Serve(ownership.Listener()) }()

	select {
	case serveErr := <-serving:
		// The listener failed on its own. A closed server here means something
		// else shut it down, which is not an error.
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serving the local API on %s: %w", endpoint.Socket, serveErr)

	case <-ctx.Done():
		d.logger.Info("daemon shutting down", slog.String("reason", context.Cause(ctx).Error()))
	}

	endStreams()

	drain, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelDrain()

	shutdownErr := server.Shutdown(drain)
	if errors.Is(shutdownErr, context.DeadlineExceeded) {
		// In-flight requests had their chance. What is left is a connection that
		// never sent a request, or a client that stopped reading its response,
		// and neither is worth keeping a daemon alive for: net/http leaves an
		// unused connection alone for several seconds, which would otherwise
		// make every shutdown wait for it.
		d.logger.Warn("closing connections that did not finish draining",
			slog.Duration("grace", shutdownTimeout))
		shutdownErr = server.Close()
	}
	// Serve always returns once Shutdown has been called, so this cannot block
	// indefinitely.
	<-serving

	if shutdownErr != nil {
		return fmt.Errorf("shutting down the local API: %w", shutdownErr)
	}
	return nil
}

// Run prepares a daemon and serves until the context is cancelled.
func Run(ctx context.Context, opts Options) error {
	instance, err := New(opts)
	if err != nil {
		return err
	}
	return instance.Serve(ctx)
}
