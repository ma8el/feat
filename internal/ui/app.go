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

// Options configure the dashboard.
type Options struct {
	// Backend reaches the daemon. It is required.
	Backend Backend
	// Daemon identifies the daemon, for the footer.
	Daemon Daemon
	// Project preselects a project for task preparation, from --project.
	Project string
	// Prepare opens task preparation immediately, which `feat implement` does.
	Prepare bool
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

	prepare prepareModel

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

	model := Model{
		backend: opts.Backend,
		daemon:  opts.Daemon,
		now:     now,
		prepare: newPrepare(opts.Backend, opts.Project, opts.Brief, opts.Source),
	}
	if opts.Prepare {
		model.screen = screenPrepare
	}
	return model
}

// Run opens the dashboard.
func Run(ctx context.Context, opts Options) error {
	program := tea.NewProgram(New(opts), tea.WithContext(ctx), tea.WithAltScreen())

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
	// eventMsg reports that the daemon published a state change, so that the
	// dashboard re-reads rather than trying to apply the event itself.
	eventMsg struct{ event api.Event }
	// streamMsg reports that the event stream ended.
	streamMsg struct{}
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
	commands := []tea.Cmd{m.load(), m.subscribe(), tick()}
	if m.screen == screenPrepare {
		commands = append(commands, m.prepare.Init())
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

// subscribe follows the daemon's event stream.
//
// Each event becomes a message that makes the dashboard re-read state rather
// than apply the change itself. The stream reports what changed, and the
// snapshot is what it changed to; deriving one from the other would give the
// dashboard a second, divergent copy of the daemon's state.
func (m Model) subscribe() tea.Cmd {
	backend := m.backend
	return func() tea.Msg {
		// The channel is buffered so that the daemon's stream is never held up
		// by a dashboard that is redrawing.
		events := make(chan api.Event, 64)
		go func() {
			err := backend.Events(context.Background(), func(event api.Event) error {
				select {
				case events <- event:
				default:
					// The dashboard re-reads on every event, so dropping one
					// while several are queued loses nothing: the read that
					// follows sees the state all of them describe.
				}
				return nil
			})
			close(events)
			_ = err
		}()

		event, ok := <-events
		if !ok {
			return streamMsg{}
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

	case eventMsg:
		return m, tea.Batch(m.load(), m.subscribe())

	case streamMsg:
		// The stream is a convenience; the periodic read is what keeps the
		// dashboard correct when it ends.
		m.status = "the daemon's event stream ended; refreshing periodically instead"
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.load(), tick())

	case execMsg:
		if message.err != nil {
			m.err = message.err
		}
		// The terminal was handed to another program; redraw everything.
		return m, tea.Batch(tea.ClearScreen, m.load())

	case preparedMsg:
		return m.finishPreparation(message)

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

	switch key.String() {
	case "ctrl+c", "q":
		m.quitting = true
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

	case "x":
		return m.cancel()

	case "r":
		m.status = ""
		return m, m.load()
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
