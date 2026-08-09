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
	// screenTask is one task read and acted on: what it is, what it has
	// changed, and what to decide about that. Detail and review were separate
	// screens until ADR-042, and shared their subject, their header, their
	// workflow, and their repository list.
	screenTask
	screenPrepare
	screenRuntime
	screenCleanup
	screenKeys
	screenTerminal
)

// mainRegion reports whether this screen is a view of the selected task that the
// main region draws, rather than an overlay over the whole dashboard.
//
// The division is ADR-041's: a tab is a view you leave and come back to, and an
// overlay is something that is not about the selected task, or that must be
// answered before work continues.
func (s screen) mainRegion() bool {
	switch s {
	case screenPrepare, screenCleanup, screenKeys:
		return false
	default:
		return true
	}
}

// tab is which view the main region shows.
func (s screen) tab() (tab, bool) {
	switch s {
	case screenTerminal:
		return tabTerminal, true
	case screenDashboard:
		return tabOverview, true
	case screenTask:
		return tabTask, true
	case screenRuntime:
		return tabRuntime, true
	default:
		return tabOverview, false
	}
}

// screenFor is the screen that has the keyboard when a tab is selected.
func screenFor(active tab) screen {
	switch active {
	case tabTerminal:
		return screenTerminal
	case tabTask:
		return screenTask
	case tabRuntime:
		return screenRuntime
	default:
		return screenDashboard
	}
}

// Model is the dashboard.
type Model struct {
	backend Backend
	daemon  Daemon
	now     func() time.Time

	// screen is what has the keyboard. tab is what the main region draws, which
	// is not the same thing once an overlay can be open over it: a user
	// confirming a cleanup is still looking at the tab they left.
	screen screen
	tab    tab
	tasks  []api.Task
	cursor int
	// selected is the task the task panel shows, held by identifier so that a
	// refresh cannot make the panel show a different task.
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
	// reconciliation is the daemon's most recent recovery pass, read on the
	// periodic refresh rather than on every event: a pass is not published, and
	// what it found changes on the scale of a restart rather than a keystroke.
	reconciliation api.Reconciliation

	// terminal is the pane the main region draws and whether it has the
	// keyboard.
	terminal terminalModel

	// cleanup is the cleanup screen's own state: the inventory it was shown, the
	// classes selected, the warnings confirmed, and what is in flight. It holds
	// the plan rather than deriving one, because the plan's token is what an
	// execution carries back.
	cleanup cleanupModel

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
	// The dashboard opens on the agent's terminal, which is what the main region
	// is for. Nothing is asked of the daemon until the first task list arrives,
	// because there is no selected task to draw before then.
	model.screen = screenTerminal
	if opts.Prepare {
		model.screen = screenPrepare
	}
	if opts.Review != "" {
		// `feat review <task>` opens straight onto the task it names. The
		// comparison itself is asked for once the model starts, so that opening
		// the screen and reading a worktree stay separate steps.
		model.screen = screenTask
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
	commands := []tea.Cmd{m.load(), m.loadResources(), m.loadReconciliation(), m.connect(), m.awaitEvent(), tick()}
	if m.screen == screenPrepare {
		commands = append(commands, m.prepare.Init())
	}
	if m.screen == screenTask {
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

// loadReconciliation reads the daemon's most recent recovery pass.
//
// It reads rather than runs one: a pass observes every task's tmux window,
// worktrees, and containers, and a dashboard that triggered that on a timer
// would ask Docker about every task several times a minute. The daemon runs its
// pass at startup, and re-running it is a key the user presses.
func (m Model) loadReconciliation() tea.Cmd {
	return func() tea.Msg {
		report, err := m.backend.Reconciliation(context.Background())
		return reconciliationMsg{report: report, err: err}
	}
}

// reconcile asks the daemon to look again, rather than re-reading what it last
// found.
//
// The recovery band describes a moment, and everything it names can be acted on
// from this dashboard — so a band that could never be refreshed would go on
// reporting resources the user had already dealt with. Reading is what the
// periodic refresh does; this follows a key press or an action that changed
// something.
func (m Model) reconcile() tea.Cmd {
	return func() tea.Msg {
		report, err := m.backend.Reconcile(context.Background())
		return reconciliationMsg{report: report, err: err}
	}
}

// reconciliationMsg carries the daemon's recovery report.
//
// A failure carries no error into the model: the report is a band above the task
// list, and a dashboard that showed an error because it could not read one would
// be hiding the tasks behind the thing that explains them.
type reconciliationMsg struct {
	report api.Reconciliation
	err    error
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
		// The first task list is what makes a pane drawable: before it there is
		// no selected task, so the poll starts here rather than at startup.
		if m.activeTab() == tabTerminal && !m.terminal.polling && len(m.tasks) > 0 {
			m.terminal.polling = true
			width, height := m.mainRegionSize()
			// Asked for before the model is returned: requestFrame records that
			// one is outstanding, and a return statement may copy the model
			// before its arguments run.
			frame := m.requestFrame(width, height)
			return m, tea.Batch(frame, terminalTick(m.terminal.focused))
		}
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
		return m, tea.Batch(m.load(), m.loadResources(), m.loadReconciliation(), tick())

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

	case reconciliationMsg:
		if message.err == nil {
			m.reconciliation = message.report
		}
		return m, nil

	case cleanupPlanMsg:
		return m.applyCleanupPlan(message)

	case cleanupDoneMsg:
		return m.applyCleanupResult(message)

	case terminalFrameMsg:
		return m.applyFrame(message)

	case terminalTickMsg:
		// The poll stops when the tab does. Nothing else asks for a frame, so a
		// dashboard on any other view costs the daemon nothing.
		if m.activeTab() != tabTerminal {
			m.terminal.polling = false
			return m, nil
		}
		width, height := m.mainRegionSize()
		frame := m.requestFrame(width, height)
		return m, tea.Batch(frame, terminalTick(m.terminal.focused))

	case terminalInputMsg:
		// A key that could not be delivered is said in the status line rather
		// than in place of the pane. Putting it in the terminal's own error blanks
		// the pane until the next frame replaces it, which turned one failed
		// keystroke into the whole view flickering.
		if message.err != nil {
			m.status = "that key did not reach the agent: " + message.err.Error()
			return m, nil
		}
		// A frame at once rather than at the next tick. What a user typed has
		// already reached the pane, and waiting a poll interval to draw it is the
		// whole of the lag they feel: the echo is theirs, not the agent's.
		width, height := m.mainRegionSize()
		frame := m.requestFrame(width, height)
		return m, frame

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

// frameKey answers the keys that belong to the layout rather than to whichever
// tab has the keyboard: moving between tabs, and moving between tasks.
//
// It reports whether it handled the key, so that a tab still sees everything
// else. Task selection needs a pair of its own because the plain arrows are
// already spoken for inside a tab — review moves a repository with them — and a
// user who cannot reach the rail from the review tab cannot change task without
// leaving it first.
func (m Model) frameKey(key tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "tab":
		updated, cmd := m.selectTab(nextTab(m.activeTab(), 1))
		return updated, cmd, true

	case "shift+tab":
		updated, cmd := m.selectTab(nextTab(m.activeTab(), -1))
		return updated, cmd, true

	// Both pairs, because a terminal that does not deliver a shifted arrow would
	// otherwise leave the rail unreachable, and that is the defect this fixes.
	case "shift+up", "ctrl+p":
		updated, cmd := m.selectTask(-1)
		return updated, cmd, true

	case "shift+down", "ctrl+n":
		updated, cmd := m.selectTask(1)
		return updated, cmd, true
	}
	return m, nil, false
}

// selectTask moves the rail's cursor and brings the open tab with it.
//
// Re-opening the tab for the new task is the point: a review or a runtime view
// holds what it was told about one task, and leaving it behind after the
// selection moved would show one task's services under another task's name.
func (m Model) selectTask(delta int) (tea.Model, tea.Cmd) {
	if len(m.tasks) == 0 {
		return m, nil
	}

	m.cursor = (m.cursor + delta%len(m.tasks) + len(m.tasks)) % len(m.tasks)
	task := m.tasks[m.cursor]
	m.selected = task.ID
	m.status = ""
	return m.selectTab(m.activeTab())
}

// nextTab is the tab delta places along from active, wrapping at both ends.
func nextTab(active tab, delta int) tab {
	at := 0
	for i, candidate := range tabs {
		if candidate == active {
			at = i
			break
		}
	}
	return tabs[((at+delta)%len(tabs)+len(tabs))%len(tabs)]
}

// selectTab moves the main region to a tab and asks for whatever it shows.
//
// The task panel and runtime go through the same entry points their keys use,
// because a tab that read state a different way from the key beside it would be
// a second implementation of the same screen.
func (m Model) selectTab(active tab) (tea.Model, tea.Cmd) {
	switch active {
	case tabTask:
		return m.openTask()
	case tabRuntime:
		return m.openRuntime()
	}

	if task, ok := m.current(); ok && active == tabTerminal {
		m.selected = task.ID
	}
	m.screen = screenFor(active)

	if active == tabTerminal {
		// A frame at once, so the region is not blank while the first tick
		// waits, and a poll behind it unless one is already running.
		m.terminal.loaded, m.terminal.err = false, nil
		width, height := m.mainRegionSize()
		frame := m.requestFrame(width, height)
		if m.terminal.polling {
			return m, frame
		}
		m.terminal.polling = true
		return m, tea.Batch(frame, terminalTick(m.terminal.focused))
	}
	return m, nil
}

// key routes a key press to the screen that has the keyboard.
func (m Model) key(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The key map answers only the keys that close it. An overlay that passed
	// every other key through would act on a task the user cannot see while they
	// are reading about how to act on it.
	if m.screen == screenKeys {
		switch key.String() {
		case "esc", "?":
			m.screen = screenFor(m.tab)
			return m, nil
		case "ctrl+c", "q":
			m.quitting = true
			m.stopStream()
			return m, tea.Quit
		}
		return m, nil
	}
	if m.screen == screenPrepare {
		updated, cmd := m.prepare.Update(key)
		m.prepare = updated
		return m, cmd
	}

	// A focused pane takes the keyboard before anything else, including the
	// frame's own keys: a user typing at an agent must be able to type the
	// characters the dashboard would otherwise treat as commands.
	if m.screen == screenTerminal && m.terminal.focused {
		return m.terminalInput(key)
	}

	// The frame's own keys are answered before the tab's, because a tab's key
	// handler returns for everything it does not recognise and would otherwise
	// swallow them. That is what stopped `tab` at the review tab: review and
	// runtime never passed it on, so the cycle ended wherever a screen with its
	// own keyboard began.
	//
	// They are not answered while a dialog is open. An overlay is something that
	// must be answered before work continues, and moving the tab or the task
	// underneath it would change what the answer applies to.
	if m.screen != screenCleanup {
		if updated, cmd, handled := m.frameKey(key); handled {
			return updated, cmd
		}
	}

	if m.screen == screenRuntime {
		return m.runtimeKey(key)
	}
	if m.screen == screenTask {
		return m.taskPanelKey(key)
	}
	if m.screen == screenCleanup {
		return m.cleanupKey(key)
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
		if m.screen == screenKeys {
			m.screen = screenDashboard
		}
		return m, nil

	case "?":
		m.rememberTab()
		m.screen = screenKeys
		return m, nil

	case "i":
		if m.screen == screenTerminal {
			return m.focusTerminal()
		}
		return m, nil

	case "w":
		if m.screen == screenTerminal {
			// Switching between the agent's pane and the shell's discards the
			// frame with it, so that one pane's contents are never drawn under
			// the other's name for a tick.
			m.terminal.shell = !m.terminal.shell
			m.terminal.loaded, m.terminal.err = false, nil
			width, height := m.mainRegionSize()
			frame := m.requestFrame(width, height)
			return m, frame
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
		return m.openTask()

	case "n":
		m.rememberTab()
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
		return m.openTask()

	case "C":
		return m.openCleanup()

	case "z":
		return m.resume()

	case "x":
		return m.cancel()

	case "r":
		// An explicit refresh looks again rather than re-reading: a user who
		// pressed it wants what is true now, and the cost of a pass is theirs to
		// spend. The periodic tick still only reads.
		m.status = ""
		return m, tea.Batch(m.load(), m.loadResources(), m.reconcile())
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

// resume continues a task's recorded agent session.
//
// It is a key the user presses, and nothing else reaches it. Reconciliation
// reports that a session can be resumed and never resumes one, which is what
// keeps recovery an offer rather than a restart (FR-STATE-004, ADR-037).
func (m Model) resume() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if task.Session == nil {
		m.status = "task " + task.Key + " has no agent session to resume"
		return m, nil
	}

	backend := m.backend
	id, key := task.ID, task.Key
	m.status = "resuming the recorded session of task " + key + "…"
	// A new pass follows, because a resume is one of the two things that
	// resolves what the recovery band was reporting.
	return m, tea.Batch(func() tea.Msg {
		if _, err := backend.Resume(context.Background(), id); err != nil {
			return tasksMsg{err: err}
		}
		tasks, err := backend.Tasks(context.Background())
		return tasksMsg{tasks: tasks, err: err}
	}, m.reconcile())
}

// cancel abandons a draft.
//
// Only a draft can be cancelled here. Removing the resources of a launched task
// is cleanup, which resolves exact targets and asks for confirmation per
// resource class.
func (m Model) cancel() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if !isDraft(task) {
		m.status = "task " + task.Key + " is " + task.Workflow +
			"; removing a launched task's resources is cleanup, on C"
		return m, nil
	}

	backend := m.backend
	id := task.ID
	m.status = "cancelled draft " + task.Key
	if m.screen == screenTask {
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

// subject is the task an action applies to: the one open in the task panel, or
// the one under the cursor.
func (m Model) subject() (api.Task, bool) {
	if m.screen == screenTask {
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

// View renders the dashboard: three regions, with a dialog over them when one is
// open (ADR-041).
//
// A terminal too small for the three regions gets the single stacked column the
// dashboard drew before, because a rail and a main region inside eighty columns
// leave neither enough to be read.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.narrow() {
		return m.stackedView()
	}

	frame := m.frame()
	dialog := m.dialogView()
	if dialog == "" {
		return frame
	}

	// Centred within the body rather than within the terminal, so that a dialog
	// cannot reach the footer.
	width, height := m.frameSize()
	return centreOverlay(frame, dialog, width, height-footerHeight)
}

// stackedView is the pre-layout dashboard, one screen at a time.
func (m Model) stackedView() string {
	switch m.screen {
	case screenPrepare:
		return m.prepare.View()
	case screenTask:
		return m.taskView()
	case screenRuntime:
		return m.runtimeView()
	case screenCleanup:
		return m.cleanupView()
	case screenKeys:
		return m.keyMap() + m.footer(keyHints(keyHint("esc", "close")))
	default:
		return m.dashboardView()
	}
}

// dialogView renders the open overlay, or nothing when none is open.
func (m Model) dialogView() string {
	width, height := m.frameSize()
	// A dialog takes most of the terminal but never all of it. What is left is
	// the task list and the resources behind it, which is the reason ADR-041
	// chose an overlay over a screen that replaces them. It never reaches the
	// footer, which is the part of the frame that holds still.
	inner := width * 3 / 4
	tallest := height - footerHeight

	switch m.screen {
	case screenPrepare:
		return dialogBox("prepare a task", m.prepare.View(), inner, tallest)
	case screenCleanup:
		// The dialog carries cleanup's own keys, because the frame's footer says
		// how to close a dialog and this says what this one can do.
		return dialogBox("clean up "+m.cleanupTitle(),
			m.cleanupBody()+"\n"+m.cleanupHints(), inner, tallest)
	case screenKeys:
		return dialogBox("keys", m.keyMap(), inner, tallest)
	default:
		return ""
	}
}

// activeTab is what the main region draws.
//
// With an overlay open the screen is not a tab, and the main region keeps
// showing whatever the user was reading before they opened it.
func (m Model) activeTab() tab {
	if active, ok := m.screen.tab(); ok {
		return active
	}
	return m.tab
}

// rememberTab records the main region's tab before an overlay takes the
// keyboard, so that closing the overlay returns to what was underneath.
func (m *Model) rememberTab() {
	if active, ok := m.screen.tab(); ok {
		m.tab = active
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
