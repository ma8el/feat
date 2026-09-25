package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// refreshInterval is how often the dashboard re-reads state without being
// prompted. State changes arrive as events, so this is a backstop: it keeps
// elapsed times moving and recovers a client whose stream ended, which v0.1
// answers by re-reading current state rather than by resuming (ADR-027).
const refreshInterval = 2 * time.Second

// eventBuffer is how many stream items may be queued for the dashboard. The
// dashboard re-reads state on every item rather than applying it, so a queue
// that overflows costs nothing: the read after a dropped item sees the state it
// described.
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
	// Ticket is the reference `feat implement --ticket` named. Preparation
	// matches it against what the project's tracker command emitted, once the
	// project is known.
	Ticket string
	// PlanFirst presets the review step's start mode, from
	// `feat implement --plan`. The step still appears and the toggle still
	// moves: the flag answers the question rather than removing it, because
	// nothing is created until the user confirms.
	PlanFirst bool
	// Now supplies the current time. A nil value uses the wall clock.
	Now func() time.Time
}

// screen is which view has the keyboard.
type screen int

const (
	screenTerminal screen = iota
	// screenTask is one task read and acted on: what it is, what it has changed,
	// and what to decide about that. ADR-042 made it one screen, because detail
	// and review shared their subject, header, workflow, and repository list.
	screenTask
	// screenBrief is the document a task was launched from, read on a tab of its
	// own. It has no daemon call behind it: the brief travels on the task, and
	// what this screen adds is the room to read it (ADR-086).
	screenBrief
	screenPrepare
	// screenWizard is a project being configured. It is an overlay for the same
	// reason preparation is: it is answered before work continues, and the tasks
	// the user was watching stay on the screen behind it (ADR-041).
	screenWizard
	screenRuntime
	screenCleanup
	// screenPublication is a task's work on its way to a forge: what publishing
	// would do, the draft read and edited, and what came of sending it. It is an
	// overlay rather than a tab for the reason cleanup is: it is a sequence
	// answered before work continues, and what it does reaches somebody else's
	// server and is not undone (ADR-073).
	screenPublication
	// screenDiagnosis is what `feat doctor` found, read on the dashboard. It is
	// an overlay because it is about a project rather than the selected task, and
	// because it is read and left rather than worked in (ADR-064).
	screenDiagnosis
	screenKeys
	// screenRecovery is everything the last reconciliation pass wants looked at.
	// It is an overlay because it is not about the selected task and because a
	// finding is three lines — what, where, and what to do — which is more than a
	// footer holds and more than a rail is wide enough for.
	screenRecovery
	// screenDaemon is the offer to start a daemon when none is answering. It is
	// an overlay for the reason preparation is: it is answered before work
	// continues. Every key reaches the daemon, so nothing else works while it is
	// open, and the tasks behind it are the last state anything knew about them.
	screenDaemon
)

// mainRegion reports whether this screen is a view of the selected task that
// the main region draws, rather than an overlay over the whole dashboard. The
// division is ADR-041's: a tab is a view you leave and come back to, and an
// overlay is something that is not about the selected task, or that must be
// answered before work continues.
func (s screen) mainRegion() bool {
	switch s {
	case screenPrepare, screenWizard, screenCleanup, screenPublication, screenDiagnosis,
		screenKeys, screenRecovery, screenDaemon:
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
	case screenTask:
		return tabTask, true
	case screenBrief:
		return tabBrief, true
	case screenRuntime:
		return tabRuntime, true
	default:
		return tabTerminal, false
	}
}

// screenFor is the screen that has the keyboard when a tab is selected.
func screenFor(active tab) screen {
	switch active {
	case tabTerminal:
		return screenTerminal
	case tabTask:
		return screenTask
	case tabBrief:
		return screenBrief
	case tabRuntime:
		return screenRuntime
	default:
		return screenTerminal
	}
}

// home is what closing a view returns to: the agent's terminal, which is what
// the main region is for. The narrow fallback draws the task list for it
// instead, because below the layout's minimum there is no rail and nothing else
// chooses a task.
func (m Model) home() screen { return screenTerminal }

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
	// selected is the task the rail's cursor is on and the task panel shows, held
	// by identifier rather than by row. tasks is re-sorted newest-first on every
	// read, so a row disagrees with the selection the moment a task appears: the
	// rail draws its marker on one task, the main region draws another, and every
	// destructive key follows the marker. Naming the task is what makes a refresh
	// unable to re-point the selection, which FR-UI-001 requires the list to be
	// right about.
	//
	// It may name a task the list does not hold: one a cleanup archived, or the
	// reference `feat review <task>` opened on before the first response resolved
	// it to an identifier. That is a state the dashboard reports rather than
	// repairs.
	selected string
	archived int
	// folded is the projects whose tasks the rail is not listing, by project
	// identifier. It is replaced rather than written to: the model is a value,
	// copied on every message, and a map shared between the copies would make
	// folding a project reach backwards into every model already returned.
	folded map[string]bool

	// resources is the daemon's most recent sample, and resourceErr is why there
	// is none. A failure here never hides the task list: metrics are
	// observational, and a dashboard that refused to draw because it could not
	// measure memory would be the opposite of what they are for (FR-UI-005).
	resources   api.ResourceReport
	resourceErr error

	prepare prepareModel
	// wizard is the project wizard's own state: the questions, where the answers
	// have reached, and what was written. It holds no dashboard state, and the
	// dashboard holds none of its (ADR-063).
	wizard wizardModel
	// diagnosis is the last `feat doctor` run the user asked for, and what it
	// found. It is never run on its own: the checks shell out to Git, Compose,
	// and the container runtime, and a dashboard that ran them on a timer would
	// be a dashboard nobody could leave open.
	diagnosis diagnosisModel
	// runtime is the application-runtime screen's own state: what the last
	// action observed, what is in flight, and whether a destroy is waiting for a
	// confirmation.
	runtime runtimeModel
	// review is the review screen's own state: the comparison it was given, the
	// repository under the cursor, and what is in flight.
	review reviewModel
	// brief is the brief tab's own state: whose brief is being read and how far
	// down it. Its own offset rather than the panel's, because two tabs sharing
	// one would each move the other's position.
	brief briefModel
	// reconciliation is the daemon's most recent recovery pass, read on the
	// periodic refresh rather than on every event: a pass is not published, and
	// what it found changes on the scale of a restart rather than a keystroke.
	//
	// reconciling reports that one was asked for and has not come back. A pass
	// walks every task's worktrees, tmux windows, and containers, so it takes
	// long enough that a user who pressed the key needs to be told it is running.
	reconciliation api.Reconciliation
	reconciling    bool

	// terminal is the pane the main region draws and whether it has the
	// keyboard.
	terminal terminalModel

	// cleanup is the cleanup screen's own state: the inventory it was shown, the
	// classes selected, the warnings confirmed, and what is in flight. It holds
	// the plan rather than deriving one, because the plan's token is what an
	// execution carries back.
	cleanup cleanupModel

	// publication is the publication screen's own state: what publishing would
	// do, what came back from the editor, and what came of sending it. It holds
	// the approved words rather than deriving them, because those words are what
	// is sent: what the user read is what reaches the forge (ADR-070).
	publication publicationModel

	// activity is the loading indicator, animating exactly while waiting says a
	// screen is waiting for the daemon. It is the dashboard's rather than any one
	// screen's, so that starting and stopping it is one rule rather than a pair
	// of calls each screen has to remember.
	activity activity

	// events carries what the daemon's stream delivered. It is created once and
	// read repeatedly, because receiving an event must cost a channel read
	// rather than a connection: see connect below.
	events chan api.Event
	// streamCtx bounds the subscription and stopStream ends it. A context on a
	// model is not the shape Bubble Tea prefers, but the stream outlives every
	// individual command, and a background context leaves the daemon serving a
	// subscription after the dashboard is gone.
	streamCtx  context.Context
	stopStream context.CancelFunc

	// err is a failure the user has not dismissed. It is shown rather than
	// thrown: a dashboard that exits because one request failed takes the view
	// of every running task with it.
	err    error
	status string

	// stopping is the task a stop is waiting for a yes about, by identifier. Only
	// a task whose agent is mid-turn asks: stopping one interrupts what it was
	// doing, and a key is easier to hit by accident than a typed command. A task
	// that is idle or already stopped has nothing to interrupt, and a question
	// with an obvious answer teaches people to answer without reading it (the
	// rule ADR-037 applies to cleanup's warnings).
	stopping string

	// daemonGone reports that the last read failed because nothing is listening
	// on the daemon's socket, and daemonAsked that the offer to start one has
	// already been made about this absence.
	//
	// The pair is what makes the question a question rather than a loop: the read
	// that discovers an absent daemon runs every two seconds. daemonStarting is a
	// start the user asked for and that has not come back, and daemonErr is why
	// the last one did not work — kept beside the dialog rather than in the
	// footer, because a failed start says what it says in a quoted daemon log.
	daemonGone     bool
	daemonAsked    bool
	daemonStarting bool
	daemonErr      error

	// streamEnded reports that the event subscription is over, which is what
	// makes reopening it after a restart safe: replacing a live channel would
	// leave a subscriber on both sides of a socket nobody reads.
	streamEnded bool

	// cancelling is the draft a cancel is waiting for a yes about, by identifier.
	// It asks always, where stopping asks only when there is something to
	// interrupt, because no state a draft can be in makes losing it harmless: a
	// brief is text somebody wrote, and Feat holds the only copy. A refresh
	// between the decision and the key press moves the selection, so `x`
	// destroyed a draft that was never chosen, with nothing on screen naming what
	// was about to go (G2-04).
	//
	// The identifier is held rather than re-read on the yes, for the same reason:
	// the answer applies to the task the question named.
	cancelling string

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
		backend: opts.Backend,
		daemon:  opts.Daemon,
		now:     now,
		prepare: newPrepare(opts.Backend, prepareStart{
			project:   opts.Project,
			brief:     opts.Brief,
			source:    opts.Source,
			ticket:    opts.Ticket,
			planFirst: opts.PlanFirst,
		}),
		activity:   newActivity(),
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
		// `feat review <task>` opens straight onto the task it names, and the
		// comparison is asked for once the model starts, so opening the screen and
		// reading a worktree stay separate steps.
		//
		// What is held here is what the user typed, which may be a task's
		// eight-character short key or any prefix of its identifier: the daemon
		// resolves those and the dashboard does not, so there is one resolver.
		// applyReview adopts the identifier the first response carries.
		model.screen = screenTask
		model.selected = opts.Review
		model.review = reviewModel{task: opts.Review, observing: true}
	}
	return model
}

// Run opens the dashboard. Its lifetime is deliberately its own rather than the
// process-wide interrupt context's; see dashboardContext.
func Run(ctx context.Context, opts Options) error {
	opts.Context = dashboardContext(ctx)
	model := New(opts)
	defer model.stopStream()

	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		// An interrupt is an ordinary shutdown, not a failure.
		if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, tea.ErrInterrupted) {
			return nil
		}
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

// dashboardContext detaches the dashboard from the process-wide interrupt. The
// dashboard hands its terminal to other programs — the agent's tmux client, the
// project's diff tool, `docker compose logs --follow` — and an interrupt
// belongs to whichever holds it. The terminal driver delivers Ctrl-C to every
// process in the foreground group, so with the interrupt context wired in,
// leaving the logs quit Feat and there was no other way out of them.
//
// Bubble Tea already knows the difference: it ignores signals while the
// terminal is released to another program and quits on them while it owns the
// terminal, which is the whole policy this needs (ADR-049). The stream this
// context bounds ends with Run either way, because Run stops it on the way out.
func dashboardContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
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

// Init starts the first read, the event subscription, and the periodic refresh.
// Preparation is started only when it is the screen the user asked for: its
// first act is to read the project list, and a message no screen is waiting for
// is dropped.
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

// loadResources reads the daemon's most recent resource sample. It follows the
// periodic refresh rather than the event stream, because a sample is not a
// state change: a figure that moves every two seconds would make every
// dashboard re-read through the stream as well (ADR-035).
func (m Model) loadResources() tea.Cmd {
	return func() tea.Msg {
		report, err := m.backend.Resources(context.Background())
		return resourcesMsg{report: report, err: err}
	}
}

// loadReconciliation reads the daemon's most recent recovery pass. It reads
// rather than runs one: a pass observes every task's tmux window, worktrees,
// and containers, so a timer would ask Docker about every task several times a
// minute. The daemon runs its pass at startup, and re-running it is a key the
// user presses.
func (m Model) loadReconciliation() tea.Cmd {
	return func() tea.Msg {
		report, err := m.backend.Reconciliation(context.Background())
		return reconciliationMsg{report: report, err: err}
	}
}

// reconcile asks the daemon to look again, rather than re-reading what it last
// found. Everything the recovery band names can be acted on from this
// dashboard, so a band that could never be refreshed would go on reporting
// resources the user had dealt with. Reading is the periodic refresh's job;
// this follows a key press or an action.
func (m Model) reconcile() tea.Cmd {
	return func() tea.Msg {
		report, err := m.backend.Reconcile(context.Background())
		return reconciliationMsg{report: report, err: err}
	}
}

// reconciliationMsg carries the daemon's recovery report. A failure carries no
// error into the model: the report is a band above the task list, and a
// dashboard that showed an error because it could not read one would hide the
// tasks behind the thing that explains them.
type reconciliationMsg struct {
	report api.Reconciliation
	err    error
}

// connect follows the daemon's event stream for the life of the dashboard. Each
// event becomes a message that makes the dashboard re-read state rather than
// apply the change itself: the stream reports what changed and the snapshot is
// what it changed to, and deriving one from the other would give the dashboard
// a second, divergent copy of the daemon's state.
//
// Exactly one stream is opened and it is held open, which is not an
// optimisation. The daemon opens every stream with a hello item so a client
// learns the stream is live before anything happens (ADR-027,
// docs/06-technical-architecture.md), so a dashboard that opened a stream per
// event would be driven by its own reconnections and would leak a connection, a
// goroutine, and a subscriber on each round, at the speed of the socket.
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

// awaitEvent waits for the next item the open stream delivered. It is re-issued
// for each item and opens nothing: the connection it reads from was made once,
// by connect.
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

// Update applies one message and leaves the loading indicator in step with it.
// Every message goes through the same two steps: the screens apply it, then the
// indicator is set to whatever they are now waiting for. Doing it here makes it
// impossible to start a spinner and forget to stop it, and no screen knows
// there is one.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.apply(message)
	model, ok := updated.(Model)
	if !ok {
		return updated, cmd
	}
	return model.animate(cmd)
}

// animate starts or stops the indicator to match what the dashboard is waiting
// for, carrying the update's own command with it.
func (m Model) animate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if !m.waiting() {
		m.activity.stop()
		return m, cmd
	}
	if start := m.activity.start(); start != nil {
		return m, tea.Batch(cmd, start)
	}
	return m, cmd
}

// waiting reports whether the screen with the keyboard is waiting for a request
// it has told the user about. It asks the open screen rather than every screen,
// because a dialog closed over a request still in flight has nothing left to
// draw a spinner in and the answer lands in the footer.
//
// The terminal's case widens what this answers. Every other wait here is a
// request the dashboard has outstanding with the daemon; that one waits on an
// event from the agent, which nothing on this side asked for. For the reader it
// is the same fact: Feat is waiting, and this is what for.
func (m Model) waiting() bool {
	switch m.screen {
	case screenPrepare:
		return m.prepare.busy
	case screenCleanup:
		return m.cleanup.working
	case screenPublication:
		return m.publication.working
	case screenTask:
		return m.review.observing || m.review.pending != ""
	case screenRuntime:
		return m.runtime.observing || m.runtime.pending != ""
	case screenTerminal:
		task, ok := m.subject()
		return ok && startingNote(task) != ""
	}
	return false
}

// apply is Update's first step: one message, given to whatever it belongs to.
func (m Model) apply(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		m.prepare.resize(m.preparationSize())
		m.wizard.resize(m.wizardSize())
		// A window that grew shows more of the publication draft than the last
		// one did, and what has been shown is what may be published: the frame
		// that follows this message is drawn at the new size, so this is where
		// it is known (ADR-076).
		m.publication = m.witnessPublication()
		return m, nil

	case tasksMsg:
		m.loaded = true
		if message.err != nil {
			// Every action funnels its failure through this message, so this is
			// the one place a daemon that stopped answering has to be recognised.
			if daemonGone(message.err) {
				return m.noteDaemonGone()
			}
			m.err = message.err
			return m, nil
		}
		m.err = nil
		m.noteDaemonAnswered()
		listed := m.tasks
		m.tasks, m.archived = activeTasks(sortTasks(message.tasks))
		m.anchorSelection(listed)
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
		commands := []tea.Cmd{m.load(), m.awaitEvent()}
		// The task list is the answer to every event, and it is not the whole
		// answer while the task panel is open: it carries the workflow and the
		// check counts, and the check results themselves come from an
		// observation.
		if m.reviewOutdatedBy(message.event) {
			m.review.observing = true
			commands = append(commands, m.reviewAction(api.ReviewObserve))
		}
		return m, tea.Batch(commands...)

	case streamMsg:
		// The stream is a convenience; the periodic read is what keeps the
		// dashboard correct when it ends. It is deliberately not reopened here:
		// v0.1 answers a lost stream by re-reading current state rather than by
		// resuming (ADR-027), and a dashboard that reconnected on its own would
		// have to decide how often.
		//
		// Recording that it ended is what lets a daemon the user restarts get a
		// subscription again, once, on the way back.
		m.streamEnded = true
		m.status = "the daemon's event stream ended; refreshing every " +
			refreshInterval.String() + " instead"
		if message.err != nil {
			m.status += " (" + message.err.Error() + ")"
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.load(), m.loadResources(), m.loadReconciliation(), tick())

	case spinner.TickMsg:
		// One frame of the loading indicator. It is answered here rather than by
		// the screen showing it, because the indicator is the dashboard's: a
		// screen that closed while a request was in flight would otherwise stop
		// answering the ticks it started, and there would be nothing left to end
		// the chain.
		updated, cmd := m.activity.advance(message)
		m.activity = updated
		return m, cmd

	case execMsg:
		if message.err != nil {
			m.err = message.err
		}
		// The terminal was handed to another program; redraw everything.
		return m, tea.Batch(tea.ClearScreen, m.load())

	case preparedMsg:
		return m.finishPreparation(message)

	case wizardClosedMsg:
		return m.finishWizard()

	case diagnosedMsg:
		// The wizard has a diagnosis of its own, drawn inside its dialog, so a
		// pass it asked for belongs to it.
		if m.screen == screenWizard {
			updated, cmd := m.wizard.Update(message)
			m.wizard = updated
			return m, cmd
		}
		m.diagnosis = m.diagnosis.apply(message)
		return m, nil

	case daemonStartedMsg:
		return m.applyDaemonStart(message)

	case runtimeMsg:
		return m.applyRuntime(message)

	case reviewMsg:
		return m.applyReview(message)

	case reconciliationMsg:
		m.reconciling = false
		if message.err == nil {
			m.reconciliation = message.report
		}
		return m, nil

	case cleanupPlanMsg:
		return m.applyCleanupPlan(message)

	case cleanupDoneMsg:
		return m.applyCleanupResult(message)

	case publicationPlanMsg:
		return m.applyPublicationPlan(message)

	case publicationEditedMsg:
		return m.applyPublicationEdited(message)

	case publicationDoneMsg:
		return m.applyPublicationDone(message)

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
	if m.screen == screenWizard {
		updated, cmd := m.wizard.Update(message)
		m.wizard = updated
		return m, cmd
	}
	return m, nil
}

// frameKey answers the keys that belong to the layout rather than to whichever
// tab has the keyboard: moving between tabs, and moving between tasks. It
// reports whether it handled the key, so a tab still sees everything else.
//
// The division is the shift key: shifted keys move the frame — which task,
// which view — and plain keys move within whatever the main region is drawing.
// Plain arrows doing both means the same key moves the rail on one tab and a
// repository on the next, which a user cannot tell without pressing it
// (ADR-046).
//
// Each direction has three spellings. Uppercase letters are the primary
// binding, because a terminal has no modifier bit for a shifted letter and
// delivers shift+j as J. The shifted arrows are the same movement for a user
// who does not think in hjkl, and they are the one spelling a terminal can fail
// to deliver: a modified arrow needs an escape sequence of its own, and not
// every terminal emits one.
//
// The control pair is not a fallback — a terminal that eats shifted arrows
// still delivers `J`. It is here for the readline reflex, where ctrl+n and
// ctrl+p are next and previous line, which is why it is not on the key map. It
// shares an arm with the documented spelling, so the two cannot diverge.
func (m Model) frameKey(key tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "L", "shift+right", "tab":
		updated, cmd := m.selectTab(nextTab(m.activeTab(), 1))
		return updated, cmd, true

	case "H", "shift+left", "shift+tab":
		updated, cmd := m.selectTab(nextTab(m.activeTab(), -1))
		return updated, cmd, true

	case "K", "shift+up", "ctrl+p":
		updated, cmd := m.selectTask(-1)
		return updated, cmd, true

	case "J", "shift+down", "ctrl+n":
		updated, cmd := m.selectTask(1)
		return updated, cmd, true

	// The same movement named rather than stepped: one shifted letter per view,
	// so reaching one is not counting how far it is from here. They are the
	// frame's keys for the reason L and H are — a view must not swallow the key
	// that leaves it — and a dialog excludes them, because moving the frame under
	// an unanswered question would change what the answer applies to.
	//
	// `enter` is not among them: it means "confirm" in every dialog the dashboard
	// has, and the frame was the one place it did not.
	//
	// `A` is the agent's terminal and is deliberately the shifted form of `a`,
	// which attaches to it: the pair is the view of the pane and the keyboard
	// handed to it, and it is the one pair here where shift changes what happens
	// rather than where it happens. The terminal is not `H`, which would spell
	// home, because `H` is half of the pair that steps between views.
	case "A":
		updated, cmd := m.selectTab(tabTerminal)
		return updated, cmd, true

	case "T":
		updated, cmd := m.selectTab(tabTask)
		return updated, cmd, true

	case "B":
		updated, cmd := m.selectTab(tabBrief)
		return updated, cmd, true

	case "R":
		updated, cmd := m.selectTab(tabRuntime)
		return updated, cmd, true
	}
	return m, nil, false
}

// selectTask moves the rail's cursor and brings the open tab with it.
// Re-opening the tab for the new task is the point: a review or a runtime view
// holds what it was told about one task, and leaving it behind would show one
// task's services under another task's name.
func (m Model) selectTask(delta int) (tea.Model, tea.Cmd) {
	next, ok := m.nextStop(delta)
	if !ok {
		return m, nil
	}

	m.selected = next.ID
	m.status = ""
	return m.selectTab(m.activeTab())
}

// nextStop is the task delta steps away in the rail, wrapping at both ends. It
// returns a task rather than the row, because the row is only true of the list
// this key press landed on and the next refresh may move the same task.
//
// A folded project is one stop rather than a run of hidden tasks: the fold is
// what the cursor moves to, because the fold is what space opens. Stepping over
// folded projects makes folding a one-way door, with nothing able to put the
// cursor back on one (ADR-052).
func (m Model) nextStop(delta int) (api.Task, bool) {
	if len(m.tasks) == 0 || delta == 0 {
		return api.Task{}, false
	}

	stops, at := m.railStops()
	from := m.selectedIndex()
	here, ok := at[from]
	if !ok {
		// The selection names a task the list no longer holds, so there is no
		// row to move from. Moving enters the rail from the end the movement
		// comes towards, which is how the user gets a marker back.
		if delta > 0 {
			return m.tasks[stops[0]], true
		}
		return m.tasks[stops[len(stops)-1]], true
	}
	next := stops[((here+delta)%len(stops)+len(stops))%len(stops)]
	if next == from {
		// One stop, which is a rail whose every project is folded into the one
		// holding the cursor. The selection stays where it is, and the folded
		// header it belongs to keeps saying where that is.
		return api.Task{}, false
	}
	return m.tasks[next], true
}

// railStops is the cursor positions the rail offers, in the order it draws
// them, and where in that sequence each task index sits. Every task of an open
// project is its own stop; a folded project contributes one, which is the task
// the cursor is already on when the fold holds it, so moving onto a fold and
// off it re-selects nothing.
func (m Model) railStops() ([]int, map[int]int) {
	stops := make([]int, 0, len(m.tasks))
	at := make(map[int]int, len(m.tasks))

	for _, group := range groupByProject(m.tasks) {
		if m.folded[group.project] {
			stop := group.indexes[0]
			if m.holdsCursor(group) {
				stop = m.selectedIndex()
			}
			for _, index := range group.indexes {
				at[index] = len(stops)
			}
			stops = append(stops, stop)
			continue
		}
		for _, index := range group.indexes {
			at[index] = len(stops)
			stops = append(stops, index)
		}
	}
	return stops, at
}

// foldProject folds or unfolds the project of the task under the cursor. The
// cursor stays where it is, so space is the same control in both directions.
// The task it holds goes on being the selected one and the folded header names
// it, which is what makes the fold something a user can move back to (ADR-052).
func (m Model) foldProject() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}

	folded := make(map[string]bool, len(m.folded)+1)
	for project, hidden := range m.folded {
		folded[project] = hidden
	}
	folded[task.ProjectID] = !folded[task.ProjectID]
	if !folded[task.ProjectID] {
		delete(folded, task.ProjectID)
	}
	m.folded = folded
	m.status = ""
	return m, nil
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

// selectTab moves the main region to a tab and asks for whatever it shows. The
// task panel and runtime go through the same entry points their keys use,
// because a tab that read state a different way would be a second
// implementation of one screen.
func (m Model) selectTab(active tab) (tea.Model, tea.Cmd) {
	switch active {
	case tabTask:
		return m.openTask()
	case tabBrief:
		return m.openBrief()
	case tabRuntime:
		return m.openRuntime()
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
	// The daemon dialog is answered before anything else, because everything
	// else needs the daemon it is asking about.
	if m.screen == screenDaemon {
		return m.daemonKey(key)
	}
	// A reading overlay answers only the keys that close it. One that passed
	// every other key through would act on a task the user cannot see while they
	// are reading about how to act on it.
	if m.screen == screenDiagnosis {
		return m.diagnosisKey(key)
	}
	if m.screen == screenKeys || m.screen == screenRecovery {
		switch key.String() {
		case "esc", "?", "!":
			m.screen = screenFor(m.tab)
			return m, nil
		case "ctrl+c", "q":
			return m.quit()
		case "r":
			// Looking again from here is the one action this overlay offers, and
			// it is why the pass has a time on it: a user who has just resumed a
			// task is reading what was true before they did. The overlay stays
			// open, because what they asked for is the answer to appear in it.
			m.reconciling = true
			return m, m.reconcile()
		}
		return m, nil
	}
	if m.screen == screenPrepare {
		updated, cmd := m.prepare.Update(key)
		m.prepare = updated
		return m, cmd
	}
	// The wizard takes every key, including the frame's. It is a form with a
	// text field in it, and a user typing a repository path types J and K.
	if m.screen == screenWizard {
		updated, cmd := m.wizard.Update(key)
		m.wizard = updated
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
	// swallow them: that is what stops `tab` at a screen with its own keyboard.
	//
	// They are not answered while a dialog is open. An overlay must be answered
	// before work continues, and moving the tab or the task underneath it would
	// change what the answer applies to.
	if m.screen != screenCleanup && m.screen != screenPublication {
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
	if m.screen == screenBrief {
		return m.briefKey(key)
	}
	if m.screen == screenCleanup {
		return m.cleanupKey(key)
	}
	if m.screen == screenPublication {
		return m.publicationKey(key)
	}
	return m.dashboardKey(key)
}

// quit leaves the dashboard. The daemon serves a subscription until its client
// goes away, so the dashboard says so on the way out rather than leaving one to
// be collected when the process exits. Every screen that offers `q` goes
// through here.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	m.stopStream()
	return m, tea.Quit
}

// dashboardKey answers the keys that belong to the dashboard rather than to one
// of its views: opening an overlay, acting on the selected task, quitting. A
// view with its own keyboard falls through to this for every key it does not
// claim, which is what makes `?` work from the task panel and runtime.
//
// Falling through rather than being answered first is the opposite of what
// frameKey does. Movement must beat a view, because a view that swallowed it
// would trap the user in itself; an action must not, because `r` refreshes the
// comparison on the task panel and the runtime's state on runtime. A view that
// claims a key keeps it, and everything else lands on the dashboard's own
// meaning.
func (m Model) dashboardKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A pending confirmation takes the keyboard, as it does on the runtime
	// screen: while Feat is asking whether to interrupt a working agent, no
	// other key means anything.
	if m.stopping != "" {
		id := m.stopping
		m.stopping = ""
		switch key.String() {
		case "y", "Y":
			return m.stopAgent(id)
		default:
			m.status = "the agent was left running"
			return m, nil
		}
	}
	if m.cancelling != "" {
		id := m.cancelling
		m.cancelling = ""
		switch key.String() {
		case "y", "Y":
			return m.cancelDraft(id)
		default:
			// Named, as the question was. A user who answered about one task and
			// is told "nothing was cancelled" has been told nothing about which.
			m.status = "draft " + m.taskKey(id) + " was kept"
			return m, nil
		}
	}

	switch key.String() {
	case "ctrl+c", "q":
		return m.quit()

	case startDaemonKey:
		return m.offerDaemonStart()

	// esc is not navigation. It closes an overlay, and every overlay answers it
	// before a key reaches here; on a tab it means nothing at all. A tab that
	// closed on it would be a second way to move between the same four views,
	// disagreeing with the tab bar's own order about what is next to what
	// (ADR-046). It is answered here rather than left to fall off the end of the
	// switch so that what it does is written down.
	case "esc":
		return m, nil

	case "?":
		m.rememberTab()
		m.screen = screenKeys
		return m, nil

	case "!":
		m.rememberTab()
		m.screen = screenRecovery
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

	// The plain keys move within the main region, and on the terminal tab there
	// is nothing to move through: an unfocused pane has no cursor of its own, and
	// a focused one takes every key before this. So they do nothing here, and the
	// rail is on J and K as it is from every other view.
	//
	// The narrow fallback is not an exception. Below the layout's minimum there
	// is no rail, so the task list is what the single column draws and moving
	// within it is what these keys mean everywhere else. The terminal screen is
	// what draws that list; runtime reaches here by falling through, and a narrow
	// terminal showing runtime would be moving a selection the user cannot see.
	case "up", "k":
		if m.narrow() && m.screen == screenTerminal {
			return m.selectTask(-1)
		}
		return m, nil

	case "down", "j":
		if m.narrow() && m.screen == screenTerminal {
			return m.selectTask(1)
		}
		return m, nil

	// Folding is the rail's own control, and the marker on every project header
	// has been offering it since the rail was written (ADR-051). It is on the
	// space bar rather than on a letter because the letters are actions on a task
	// and this is not one: nothing is created, resumed, or removed by it.
	case " ", "space":
		return m.foldProject()

	case "n":
		m.rememberTab()
		m.screen = screenPrepare
		m.prepare = m.prepare.restart()
		return m, m.prepare.Init()

	case "p":
		return m.configureProject()

	case "D":
		return m.openDiagnosis()

	case "a":
		return m.attach()

	case "s":
		return m.shell()

	case "C":
		return m.openCleanup()

	case "z":
		return m.resume()

	case "t":
		return m.stop()

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

// openDiagnosis checks the selected task's project against this machine. The
// subject is the project rather than the task, because a check that a Compose
// service exists or that the agent is installed is true of every task in the
// project or of none. With no task selected, every configured project is
// checked, as `feat doctor` does.
func (m Model) openDiagnosis() (tea.Model, tea.Cmd) {
	project := ""
	if task, ok := m.subject(); ok {
		project = task.ProjectID
	}

	m.rememberTab()
	m.screen = screenDiagnosis
	m.diagnosis = m.diagnosis.start(project)
	return m, diagnose(m.backend, project)
}

// diagnosisKey answers the report: read it, run it again, or leave.
func (m Model) diagnosisKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The window the report is drawn in, not the dialog it sits inside: paging by
	// the dialog's height moved the document further than the screen showed.
	_, height := m.diagnosisSize()

	switch key.String() {
	case "esc", "D":
		m.screen = screenFor(m.tab)
		return m, nil

	case "ctrl+c", "q":
		m.quitting = true
		m.stopStream()
		return m, tea.Quit

	case "r":
		// Looking again is the action this overlay offers, and it is why the
		// findings are worth having here rather than in a command: a user who has
		// just fixed a path wants the same screen to answer differently.
		if m.diagnosis.running {
			return m, nil
		}
		project := m.diagnosis.project
		m.diagnosis = m.diagnosis.start(project)
		return m, diagnose(m.backend, project)

	case "up", "k":
		m.diagnosis = m.diagnosis.scrollBy(-1, height)
	case "down", "j":
		m.diagnosis = m.diagnosis.scrollBy(1, height)
	case "pgup":
		m.diagnosis = m.diagnosis.scrollBy(-height, height)
	case "pgdown":
		m.diagnosis = m.diagnosis.scrollBy(height, height)
	}
	return m, nil
}

// configureProject opens the project wizard. The questions are `feat project
// init`'s own, in internal/wizard, and this screen is a second asker rather
// than a second wizard: what it adds is a cursor, a step back out of an answer,
// and the dashboard behind it (ADR-063).
func (m Model) configureProject() (tea.Model, tea.Cmd) {
	m.rememberTab()
	m.screen = screenWizard
	m.wizard = newWizard(m.backend)
	m.wizard.resize(m.wizardSize())
	return m, m.wizard.Init()
}

// finishWizard closes the wizard and looks again. A project may have been
// written and registered while it was open and the rail is grouped by project,
// so reading state again is how the dashboard finds out.
func (m Model) finishWizard() (tea.Model, tea.Cmd) {
	m.screen = screenFor(m.tab)
	m.wizard = wizardModel{}
	return m, tea.Batch(m.load(), m.loadReconciliation())
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

// resume continues a task's recorded agent session. It is a key the user
// presses and nothing else reaches it: reconciliation reports that a session
// can be resumed and never resumes one, which keeps recovery an offer rather
// than a restart (FR-STATE-004, ADR-037).
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

// stop puts the selected task's agent to sleep. It asks first only when there
// is something to interrupt: an agent that is running is mid-turn and stopping
// it ends that turn wherever it had got to, while an idle, stopped, or failed
// one is not doing anything a user would want warning about.
func (m Model) stop() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if task.Session == nil {
		m.status = "task " + task.Key + " has no agent session to stop"
		return m, nil
	}
	if task.Session.Execution == nil {
		m.status = "the agent of task " + task.Key + " runs on this machine, " +
			"where Feat owns no container to stop"
		return m, nil
	}
	// The string rather than a domain constant, because the dashboard reads the
	// API's task and never imports the domain (ui-is-a-client).
	if task.Session.Process == "running" {
		m.stopping = task.ID
		return m, nil
	}
	return m.stopAgent(task.ID)
}

// stopAgent sends the stop and reads the result back.
func (m Model) stopAgent(id string) (tea.Model, tea.Cmd) {
	backend := m.backend
	m.status = "stopping the agent environment…"
	// A new pass follows for the reason a resume runs one: a stop is the other
	// act that changes what the recovery band is reporting about a container.
	return m, tea.Batch(func() tea.Msg {
		if _, err := backend.Stop(context.Background(), id); err != nil {
			return tasksMsg{err: err}
		}
		tasks, err := backend.Tasks(context.Background())
		return tasksMsg{tasks: tasks, err: err}
	}, m.reconcile())
}

// cancel asks whether to abandon a draft. Only a draft can be cancelled here:
// removing the resources of a launched task is cleanup, which resolves exact
// targets and confirms per resource class.
//
// It asks rather than doing it, which is the half of G2-04 the selection fix
// did not cover. Holding the selection by identifier stops `x` acting on a task
// the rail is not marking; it names nothing the key is about to destroy. A
// draft is a brief somebody typed and Feat holds the only copy, while cleanup
// confirms per class for things a repository can recreate.
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

	m.cancelling = task.ID
	return m, nil
}

// cancelDraft abandons the draft the question named.
func (m Model) cancelDraft(id string) (tea.Model, tea.Cmd) {
	backend := m.backend
	m.status = "cancelled draft " + m.taskKey(id)
	if m.screen == screenTask {
		m.screen = m.home()
	}
	return m, func() tea.Msg {
		if _, err := backend.CancelDraft(context.Background(), id); err != nil {
			return tasksMsg{err: err}
		}
		tasks, err := backend.Tasks(context.Background())
		return tasksMsg{tasks: tasks, err: err}
	}
}

// taskKey is the short key a message names a task by, or the identifier when
// the task has left the list. A confirmation can outlive its subject — the
// refresh between the question and the answer is what this area is about — and
// a sentence with an empty name in it would be worse than a long one.
func (m Model) taskKey(id string) string {
	if task, ok := m.task(id); ok {
		return task.Key
	}
	return id
}

// finishPreparation returns to the dashboard once preparation ends. The
// launched task becomes the selection, and because the selection names a task
// the rail's marker follows it: a marker held as a row lands on whatever the
// newest-first list moved into that position.
//
// It closes onto the tab it opened over, which is what an overlay does. Closing
// onto the terminal instead moves a user who cancelled from the task panel or
// the brief to a view they had not asked for. Preparation the dashboard opens
// on, for `feat implement`, was never over a tab and has nothing to remember.
//
// A launch is the exception, and it is a result rather than a movement: the
// terminal is the pane of the task that was created.
//
// Both go through selectTab rather than by assigning the screen, because a
// launch moves the selection: the panel and runtime hold one task's answer and
// are not re-opened by being drawn, so assigning the screen would put the new
// task's name over the old task's repositories, checks, and services (ADR-041).
func (m Model) finishPreparation(message preparedMsg) (tea.Model, tea.Cmd) {
	opened := m.tab
	switch {
	case message.err != nil:
		m.err = message.err
	case message.task != nil:
		m.status = "launched task " + message.task.Key + " — " + message.task.Title
		m.selected = message.task.ID
		opened = tabTerminal
	default:
		m.status = "task preparation cancelled; nothing was created"
	}

	updated, cmd := m.selectTab(opened)
	return updated, tea.Batch(cmd, m.load())
}

// subject is the task an action applies to: the selected one, which is what the
// rail's marker is on and what the main region is drawing. There is one
// selection, so there is one answer, whatever has the keyboard.
func (m Model) subject() (api.Task, bool) { return m.task(m.selected) }

// task finds a task by identifier.
func (m Model) task(id string) (api.Task, bool) { return findTask(m.tasks, id) }

// findTask finds a task by identifier in a list.
func findTask(tasks []api.Task, id string) (api.Task, bool) {
	if id == "" {
		return api.Task{}, false
	}
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return api.Task{}, false
}

// selectedIndex is where the selected task sits in the list this refresh
// delivered, or -1 when the list does not hold it. A row is derived here and
// nowhere else: it is true of one list, and the list is replaced whenever the
// daemon says anything.
func (m Model) selectedIndex() int {
	for i, task := range m.tasks {
		if task.ID == m.selected {
			return i
		}
	}
	return -1
}

// anchorSelection keeps the selection naming a task rather than a position. A
// refresh may do exactly two things to it. A dashboard that has never had a
// task list selects the first one, because a rail with no marker is not a
// dashboard anybody can start from. And a selection whose task has left the
// list — archived by a cleanup here, or cancelled from another terminal — is
// reported in the words of the task that went.
//
// It never moves the selection to a different task. Clamping an index against a
// list re-sorted newest-first re-points the selection at whichever task now
// occupies the row, while the user's next key press — `x`, `t`, `C`, or a
// keystroke into a focused pane — is already on its way to it.
//
// previous is the list this refresh replaced, so a task that was never in it is
// not reported as having gone away: the reference `feat review <task>` opens on
// is not a listed task until the first response resolves it.
func (m *Model) anchorSelection(previous []api.Task) {
	if _, listed := m.task(m.selected); listed {
		return
	}
	if m.selected == "" {
		if len(m.tasks) > 0 {
			m.selected = m.tasks[0].ID
		}
		return
	}
	if gone, was := findTask(previous, m.selected); was {
		m.status = "task " + gone.Key + " is no longer listed; J and K select another"
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

// View renders the dashboard: three regions, with a dialog over them when one
// is open (ADR-041). A terminal too small for the three regions gets a single
// stacked column, because a rail and a main region inside eighty columns leave
// neither enough to read.
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
		return m.prepare.View(m.activity)
	case screenWizard:
		return m.wizard.View()
	case screenDiagnosis:
		width, height := m.diagnosisSize()
		return m.diagnosis.body(width, height) + m.footer(m.diagnosisHints())
	case screenTask:
		return m.taskView()
	case screenBrief:
		return m.briefView()
	case screenRuntime:
		return m.runtimeView()
	case screenCleanup:
		return m.cleanupView()
	case screenPublication:
		return m.publicationView()
	case screenKeys:
		width, _ := m.frameSize()
		return m.keyMap(width) + m.footer(keyHints(keyHint("esc", "close")))
	case screenRecovery:
		return m.recoveryList() + m.footer(keyHints(keyHint("r", "refresh"), keyHint("esc", "close")))
	case screenDaemon:
		width, _ := m.frameSize()
		return m.daemonBody(width) + m.footer(m.daemonHints())
	default:
		return m.listView()
	}
}

// dialogLimits are the widest and tallest a dialog may be drawn. A dialog takes
// most of the terminal but never all of it: what is left is the task list and
// the resources ADR-041 chose an overlay to keep. It never reaches the footer,
// which is the part of the frame that holds still.
func (m Model) dialogLimits() (widest, tallest int) {
	width, height := m.frameSize()
	return width * 3 / 4, height - footerHeight
}

// wizardLimits are the widest and tallest `configure a project` is drawn. Half
// the terminal, where every other overlay has three quarters, because it is the
// one overlay whose content is prose rather than the paths, identifiers, and
// lists the others hold. ADR-088 gave the question widget the box to fold its
// paragraphs into, and the box's own measure is a paragraph set to a hundred
// and sixteen cells on a wide terminal, which is past where a line stops being
// read and starts being scanned back to.
//
// The tallest is unchanged. A dialog never reaches the footer, and how long a
// line is says nothing about how many of them there is room for.
func (m Model) wizardLimits() (widest, tallest int) {
	width, height := m.frameSize()
	// Never below what its own keys need, and never above what it would have been
	// allowed before: the floor is a floor rather than a licence to be wider than
	// every other overlay on a terminal too narrow for either.
	return min(max(width/2, wizardSmallest), width*3/4), height - footerHeight
}

// wizardSmallest is the narrowest `configure a project` is drawn, whatever half
// the terminal comes to. The review step names the most keys of any step —
// write it, the four that read the file, step back, and cancel — and they come
// to sixty-five cells, with the box spending four more on its border and
// gutters. A hint that is cut is a key nobody can press, so this is a floor the
// narrower measure may not squeeze (TestEveryWizardStepCanNameItsKeys).
//
// It binds below about a hundred and forty columns and does nothing above it.
const wizardSmallest = 65 + dialogChrome

// dialogView renders the open overlay, or nothing when none is open.
func (m Model) dialogView() string {
	inner, tallest := m.dialogLimits()

	switch m.screen {
	case screenPrepare:
		return dialogBox("prepare a task", m.prepare.View(m.activity), inner, tallest)
	case screenWizard:
		// Its own allowance, which is narrower than every other overlay's; see
		// wizardLimits for why this is the one that reads better with less.
		widest, tall := m.wizardLimits()
		return dialogBox("configure a project", m.wizard.View(), widest, tall)
	case screenDiagnosis:
		width, height := m.diagnosisSize()
		return dialogBox(m.diagnosis.title(),
			m.diagnosis.body(width, height)+"\n"+m.diagnosisHints(), inner, tallest)
	case screenCleanup:
		// The dialog carries cleanup's own keys, because the frame's footer says
		// how to close a dialog and this says what this one can do.
		return dialogBox("clean up "+m.cleanupTitle(),
			m.cleanupBody()+"\n"+m.cleanupHints(), inner, tallest)
	case screenPublication:
		// The same shape as cleanup, and for the same reasons: a sequence that is
		// answered rather than glanced at, drawn over the task list it is about,
		// carrying its own keys because the frame's footer only says how to
		// close an overlay. A screen that is drawn in the narrow fallback and
		// nowhere else is a screen nobody sees (ADR-076).
		return dialogBox(m.publicationTitle(),
			m.publicationBody()+"\n"+m.publicationHints(), inner, tallest)
	case screenKeys:
		// The key map is given the same three quarters as every other dialog, and
		// lays itself out inside them. A reference sheet is a good reason to want
		// more width and not a good enough one to take it: the rail behind this is
		// what ADR-041 chose an overlay to keep, and a dialog wide enough for two
		// roomy columns covers the task keys it is drawn over.
		return dialogBox("keys", m.keyMap(inner-4), inner, tallest)
	case screenRecovery:
		return dialogBox("recovery", m.recoveryList(), inner, tallest)
	case screenDaemon:
		// The dialog carries its own keys, as cleanup's does: the frame's footer
		// says how to close an overlay, and this one is answered rather than
		// closed.
		return dialogBox(daemonTitle,
			m.daemonBody(inner-cardChrome)+"\n\n"+m.daemonHints(), inner, tallest)
	default:
		return ""
	}
}

// preparationSize is the space task preparation is drawn in: the inside of the
// dialog where the frame is drawn, and the whole terminal where it is not. It
// is the dialog's because the fields size themselves to what they are told, and
// a text area given the terminal's width in three quarters of it ends every
// line in an ellipsis.
func (m Model) preparationSize() (width, height int) {
	if m.narrow() {
		return m.frameSize()
	}
	widest, tallest := m.dialogLimits()
	// What the box itself spends: a border and a gutter on each side, and a
	// border above and below.
	return widest - cardChrome, tallest - cardVerticalChrome
}

// wizardSize is the space `configure a project` is drawn in. It is
// preparationSize's shape over the wizard's own allowance, and a second
// function rather than a parameter because the two overlays no longer share a
// width. The field, the prose around it, and the widget that folds a question's
// paragraphs are all told this (ADR-088).
func (m Model) wizardSize() (width, height int) {
	if m.narrow() {
		return m.frameSize()
	}

	widest, tallest := m.wizardLimits()
	return widest - cardChrome, tallest - cardVerticalChrome
}

// activeTab is what the main region draws. With an overlay open the screen is
// not a tab, and the main region keeps showing whatever the user was reading
// before they opened it.
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
	width, _ := m.frameSize()

	var out strings.Builder
	// Ruled off from the content above it, as the three-region footer is: the
	// fallback is a different layout of the same dashboard, not a different one
	// (ADR-051).
	out.WriteString("\n" + ruleStyle.Render(strings.Repeat(cardHorizontal, width)))
	// Both are cut to the frame and flattened to one line. The footer is a fixed
	// number of rows that the regions above it are sized against, and an error is
	// the one string here Feat did not write: a wrapped one carries the output it
	// wrapped, line breaks and all, and would push the frame apart from the
	// bottom (ADR-054).
	switch {
	case m.err != nil:
		out.WriteString("\n" + failureStyle.Render(truncate(plainLine(m.err.Error()), width)) + "\n")
	case m.status != "":
		out.WriteString("\n" + mutedStyle.Render(truncate(plainLine(m.status), width)) + "\n")
	default:
		out.WriteString("\n")
	}

	out.WriteString("\n" + hints)
	if m.daemon.Socket != "" {
		out.WriteString("\n" + mutedStyle.Render("daemon "+m.daemon.Version+" on "+m.daemon.Socket))
	}
	return out.String()
}
