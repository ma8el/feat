package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// refreshInterval is how often the dashboard re-reads state without being
// prompted.
//
// State changes arrive as events, so this is a backstop rather than the
// mechanism: it keeps elapsed times moving and recovers a client whose stream
// ended, which v0.1 answers by re-reading current state rather than by resuming
// (ADR-027).
const refreshInterval = 2 * time.Second

// eventBuffer is how many stream items may be queued for the dashboard.
//
// The dashboard re-reads state on every item rather than applying it, so a
// queue that overflows costs nothing: the read that follows a dropped item sees
// the state that item described.
const eventBuffer = 64

// Options configure the dashboard.
type Options struct {
	// Backend reaches the daemon. It is required.
	Backend Backend
	// Context bounds the event subscription. A nil value uses the background
	// context. Run passes the one that ends when the user interrupts.
	Context context.Context
	// Daemon identifies the daemon, for the footer.
	Daemon Daemon
	// Project preselects a project for task preparation, from --project.
	Project string
	// Prepare opens task preparation immediately, which `feat implement` does.
	Prepare bool
	// Review opens the review screen for this task immediately, which
	// `feat review <task>` does.
	Review string
	// Brief is an imported Markdown brief, read by the caller so that no path
	// crosses the socket.
	Brief string
	// Source records where an imported brief came from.
	Source api.Source
	// Now supplies the current time. A nil value uses the wall clock.
	Now func() time.Time
}

// screen is which view has the keyboard.
type screen int

const (
	screenDashboard screen = iota
	screenDetail
	screenPrepare
	screenRuntime
	screenReview
)

// Model is the dashboard.
type Model struct {
	backend Backend
	daemon  Daemon
	now     func() time.Time

	screen screen
	tasks  []api.Task
	cursor int
	// selected is the task the detail screen shows, held by identifier so that
	// a refresh cannot make the screen show a different task.
	selected string
	archived int

	// resources is the daemon's most recent sample, and resourceErr is why there
	// is none. A failure here never hides the task list: metrics are
	// observational, and a dashboard that refused to draw because it could not
	// measure memory would be the opposite of what they are for (FR-UI-005).
	resources   api.ResourceReport
	resourceErr error

	prepare prepareModel
	// runtime is the application-runtime screen's own state: what the last
	// action observed, what is in flight, and whether a destroy is waiting for a
	// confirmation.
	runtime runtimeModel
	// review is the review screen's own state: the comparison it was given, the
	// repository under the cursor, and what is in flight.
	review reviewModel

	// events carries what the daemon's stream delivered. It is created once and
	// read repeatedly, because receiving an event must cost a channel read
	// rather than a connection: see connect below.
	events chan api.Event
	// streamCtx bounds the subscription and stopStream ends it.
	//
	// A context on a model is not the shape Bubble Tea prefers, but the stream
	// outlives every individual command and something has to be able to end it.
	// The alternative — a background context — is a subscription the daemon
	// keeps serving after the dashboard is gone.
	streamCtx  context.Context
	stopStream context.CancelFunc

	// err is a failure the user has not dismissed. It is shown rather than
	// thrown: a dashboard that exits because one request failed takes the view
	// of every running task with it.
	err    error
	status string

	width, height int
	loaded        bool
	quitting      bool
}

// New builds the dashboard model.
func New(opts Options) Model {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	parent := opts.Context
	if parent == nil {
		parent = context.Background()
	}
	streamCtx, stopStream := context.WithCancel(parent)

	model := Model{
		backend:    opts.Backend,
		daemon:     opts.Daemon,
		now:        now,
		prepare:    newPrepare(opts.Backend, opts.Project, opts.Brief, opts.Source),
		events:     make(chan api.Event, eventBuffer),
		streamCtx:  streamCtx,
		stopStream: stopStream,
	}
	if opts.Prepare {
		model.screen = screenPrepare
	}
	if opts.Review != "" {
		// `feat review <task>` opens straight onto the review of the task it
		// names. The comparison itself is asked for once the model starts, so
		// that opening the screen and reading a worktree stay separate steps.
		model.screen = screenReview
		model.selected = opts.Review
		model.review = reviewModel{task: opts.Review}
	}
	return model
}

// Run opens the dashboard.
func Run(ctx context.Context, opts Options) error {
	opts.Context = ctx
	model := New(opts)
	defer model.stopStream()

	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		// A cancelled context is an ordinary shutdown, not a failure.
		if ctx.Err() != nil || errors.Is(err, tea.ErrProgramKilled) {
			return nil
		}
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

// Messages the dashboard sends itself.
type (
	// tasksMsg carries a completed read of task state.
	tasksMsg struct {
		tasks []api.Task
		err   error
	}
	// resourcesMsg carries a completed read of the daemon's resource sample. It
	// is its own message because it fails on its own: a machine whose memory
	// cannot be read still has tasks worth showing.
	resourcesMsg struct {
		report api.ResourceReport
		err    error
	}
	// eventMsg reports that the daemon published a state change, so that the
	// dashboard re-reads rather than trying to apply the event itself.
	eventMsg struct{ event api.Event }
	// streamMsg reports that the event stream ended, and why when there was a
	// reason beyond the daemon closing it.
	streamMsg struct{ err error }
	// tickMsg is the periodic backstop refresh.
	tickMsg time.Time
	// execMsg reports that a native command the dashboard ran has finished.
	execMsg struct{ err error }
)

// Init starts the first read, the event subscription, and the periodic
// refresh.
//
// Preparation is started only when it is the screen the user asked for. Its
// first act is to read the project list, and a message no screen is waiting for
// would be dropped by the router below.
func (m Model) Init() tea.Cmd {
	commands := []tea.Cmd{m.load(), m.loadResources(), m.connect(), m.awaitEvent(), tick()}
	if m.screen == screenPrepare {
		commands = append(commands, m.prepare.Init())
	}
	if m.screen == screenReview {
		commands = append(commands, m.reviewAction(api.ReviewObserve))
	}
	return tea.Batch(commands...)
}

// load reads current task state.
func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		tasks, err := m.backend.Tasks(context.Background())
		return tasksMsg{tasks: tasks, err: err}
	}
}

// loadResources reads the daemon's most recent resource sample.
//
// It follows the periodic refresh rather than the event stream, because a sample
// is not a state change: the daemon does not publish one, deliberately, since a
// figure that moves every two seconds would make every dashboard re-read every
// two seconds through the stream as well (ADR-035).
func (m Model) loadResources() tea.Cmd {
	return func() tea.Msg {
		report, err := m.backend.Resources(context.Background())
		return resourcesMsg{report: report, err: err}
	}
}

// connect follows the daemon's event stream for the life of the dashboard.
//
// Each event becomes a message that makes the dashboard re-read state rather
// than apply the change itself. The stream reports what changed, and the
// snapshot is what it changed to; deriving one from the other would give the
// dashboard a second, divergent copy of the daemon's state.
//
// Exactly one stream is opened, and it is held open. Receiving an event costs a
// channel read rather than a connection, which is not an optimisation: the
// daemon opens every stream with a hello item so that a client learns the stream
// is live before anything happens (ADR-027, docs/06-technical-architecture.md).
// A dashboard that opened a stream per event would therefore be driven by its
// own reconnections — each hello would produce the event that opened the next
// connection — and would leak a connection, a goroutine, and a subscriber on
// both sides of the socket every time round. That is not a slow leak: it runs at
// the speed of the socket.
func (m Model) connect() tea.Cmd {
	backend, events, ctx := m.backend, m.events, m.streamCtx
	return func() tea.Msg {
		err := backend.Events(ctx, func(event api.Event) error {
			// Buffered and dropping, so that the daemon's stream is never held
			// up by a dashboard that is redrawing.
			select {
			case events <- event:
			default:
				// The dashboard re-reads on every event, so dropping one while
				// several are queued loses nothing: the read that follows sees
				// the state all of them describe.
			}
			return nil
		})
		// Closing releases awaitEvent, which is otherwise waiting for an item
		// that can no longer arrive. The handler above cannot still be running:
		// Events has returned.
		close(events)
		return streamMsg{err: err}
	}
}

// awaitEvent waits for the next item the open stream delivered.
//
// It is re-issued for each item, and it opens nothing: the connection it reads
// from was made once, by connect.
func (m Model) awaitEvent() tea.Cmd {
	events := m.events
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			// The stream ended. connect reports that with its own message, so
			// there is nothing to say here.
			return nil
		}
		return eventMsg{event: event}
	}
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(now time.Time) tea.Msg { return tickMsg(now) })
}

// Update applies one message.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		m.prepare.resize(message.Width, message.Height)
		return m, nil

	case tasksMsg:
		m.loaded = true
		if message.err != nil {
			m.err = message.err
			return m, nil
		}
		m.err = nil
		m.tasks, m.archived = activeTasks(sortTasks(message.tasks))
		m.clampCursor()
		return m, nil

	case resourcesMsg:
		// A failed read is remembered and shown where the figures would be, and
		// nowhere else: it is not the dashboard's error, because it would
		// otherwise replace the report of a task that actually failed.
		m.resources, m.resourceErr = message.report, message.err
		return m, nil

	case eventMsg:
		return m, tea.Batch(m.load(), m.awaitEvent())

	case streamMsg:
		// The stream is a convenience; the periodic read is what keeps the
		// dashboard correct when it ends. It is deliberately not reopened here:
		// v0.1 answers a lost stream by re-reading current state rather than by
		// resuming (ADR-027), and a dashboard that reconnected on its own would
		// have to decide how often — which is exactly the decision that was
		// missing when it reconnected on every event.
		m.status = "the daemon's event stream ended; refreshing every " +
			refreshInterval.String() + " instead"
		if message.err != nil {
			m.status += " (" + message.err.Error() + ")"
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.load(), m.loadResources(), tick())

	case execMsg:
		if message.err != nil {
			m.err = message.err
		}
		// The terminal was handed to another program; redraw everything.
		return m, tea.Batch(tea.ClearScreen, m.load())

	case preparedMsg:
		return m.finishPreparation(message)

	case runtimeMsg:
		return m.applyRuntime(message)

	case reviewMsg:
		return m.applyReview(message)

	case tea.KeyMsg:
		return m.key(message)
	}

	if m.screen == screenPrepare {
		updated, cmd := m.prepare.Update(message)
		m.prepare = updated
		return m, cmd
	}
	return m, nil
}

// key routes a key press to the screen that has the keyboard.
func (m Model) key(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.screen == screenPrepare {
		updated, cmd := m.prepare.Update(key)
		m.prepare = updated
		return m, cmd
	}
	if m.screen == screenRuntime {
		return m.runtimeKey(key)
	}
	if m.screen == screenReview {
		return m.reviewKey(key)
	}

	switch key.String() {
	case "ctrl+c", "q":
		m.quitting = true
		// The daemon serves a subscription until its client goes away, so the
		// dashboard says so on the way out rather than leaving one to be
		// collected when the process happens to exit.
		m.stopStream()
		return m, tea.Quit

	case "esc":
		if m.screen == screenDetail {
			m.screen = screenDashboard
		}
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < len(m.tasks)-1 {
			m.cursor++
		}
		return m, nil

	case "enter":
		if task, ok := m.current(); ok {
			m.selected = task.ID
			m.screen = screenDetail
		}
		return m, nil

	case "n":
		m.screen = screenPrepare
		m.prepare = m.prepare.restart()
		return m, m.prepare.Init()

	case "a":
		return m.attach()

	case "s":
		return m.shell()

	case "R":
		return m.openRuntime()

	case "v":
		return m.openReview()

	case "x":
		return m.cancel()

	case "r":
		m.status = ""
		return m, tea.Batch(m.load(), m.loadResources())
	}
	return m, nil
}

// attach yields the terminal to the task's agent pane.
func (m Model) attach() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if task.Session == nil {
		m.status = "task " + task.Key + " has no terminal yet"
		return m, nil
	}

	backend := m.backend
	id := task.ID
	return m, func() tea.Msg {
		command, err := backend.AttachCommand(context.Background(), id)
		if err != nil {
			return execMsg{err: err}
		}
		return tea.Exec(command, func(err error) tea.Msg { return execMsg{err: err} })()
	}
}

// shell opens the task's shell pane in this terminal.
func (m Model) shell() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if task.Session == nil {
		m.status = "task " + task.Key + " has no terminal yet"
		return m, nil
	}

	backend := m.backend
	id := task.ID
	return m, func() tea.Msg {
		command, err := backend.ShellCommand(context.Background(), id)
		if err != nil {
			return execMsg{err: err}
		}
		return tea.Exec(command, func(err error) tea.Msg { return execMsg{err: err} })()
	}
}

// cancel abandons a draft.
//
// Only a draft can be cancelled here. Removing the resources of a launched task
// is cleanup, which resolves exact targets and asks for confirmation per
// resource class, and slice 12 delivers it.
func (m Model) cancel() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if !isDraft(task) {
		m.status = "task " + task.Key + " is " + task.Workflow +
			"; removing a launched task's resources is `feat cleanup`"
		return m, nil
	}

	backend := m.backend
	id := task.ID
	m.status = "cancelled draft " + task.Key
	if m.screen == screenDetail {
		m.screen = screenDashboard
	}
	return m, func() tea.Msg {
		if _, err := backend.CancelDraft(context.Background(), id); err != nil {
			return tasksMsg{err: err}
		}
		tasks, err := backend.Tasks(context.Background())
		return tasksMsg{tasks: tasks, err: err}
	}
}

// finishPreparation returns to the dashboard once preparation ends.
func (m Model) finishPreparation(message preparedMsg) (tea.Model, tea.Cmd) {
	m.screen = screenDashboard
	switch {
	case message.err != nil:
		m.err = message.err
	case message.task != nil:
		m.status = "launched task " + message.task.Key + " — " + message.task.Title
		m.selected = message.task.ID
	default:
		m.status = "task preparation cancelled; nothing was created"
	}
	return m, m.load()
}

// subject is the task an action applies to: the one open in the detail screen,
// or the one under the cursor.
func (m Model) subject() (api.Task, bool) {
	if m.screen == screenDetail {
		return m.task(m.selected)
	}
	return m.current()
}

// current is the task under the cursor.
func (m Model) current() (api.Task, bool) {
	if m.cursor < 0 || m.cursor >= len(m.tasks) {
		return api.Task{}, false
	}
	return m.tasks[m.cursor], true
}

// task finds a task by identifier.
func (m Model) task(id string) (api.Task, bool) {
	for _, task := range m.tasks {
		if task.ID == id {
			return task, true
		}
	}
	return api.Task{}, false
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.tasks) {
		m.cursor = len(m.tasks) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// sortTasks orders tasks so that the list does not reorder itself under the
// user's cursor between refreshes.
func sortTasks(tasks []api.Task) []api.Task {
	ordered := append([]api.Task(nil), tasks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered
}

// View renders whichever screen has the keyboard.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	switch m.screen {
	case screenPrepare:
		return m.prepare.View()
	case screenDetail:
		return m.detailView()
	case screenRuntime:
		return m.runtimeView()
	case screenReview:
		return m.reviewView()
	default:
		return m.dashboardView()
	}
}

// footer renders the status line, any error, and the daemon the dashboard is
// talking to.
func (m Model) footer(hints string) string {
	var out strings.Builder
	if m.err != nil {
		out.WriteString("\n" + failureStyle.Render(m.err.Error()) + "\n")
	} else if m.status != "" {
		out.WriteString("\n" + mutedStyle.Render(m.status) + "\n")
	} else {
		out.WriteString("\n")
	}

	out.WriteString("\n" + hints)
	if m.daemon.Socket != "" {
		out.WriteString("\n" + mutedStyle.Render("daemon "+m.daemon.Version+" on "+m.daemon.Socket))
	}
	return out.String()
}
