package ui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// runtimeModel is the state of the runtime screen.
//
// It holds what the last action observed rather than deriving it from the task
// list, because the services, the notes, and the retained volumes are what that
// action saw and the task snapshot carries only the summary.
type runtimeModel struct {
	// task is the task whose services the screen manages.
	task string
	// status is what the last action reported, and whether one has run at all.
	status api.RuntimeStatus
	loaded bool
	// pending is the action in flight, so the screen says what it is waiting for.
	pending api.RuntimeAction
	// observing reports that the read the screen opened with is in flight.
	//
	// It is separate from pending, which is the action a key press asked for and
	// which the keys below are gated on: an opening read recorded there would
	// refuse the first `u` a user pressed on a screen they had only just opened,
	// for a request nobody made. What it is for is the loading indicator, because
	// walking a project's Compose state is seconds spent under a line that used
	// to be perfectly still.
	observing bool
	// confirming reports that destroy is waiting for a yes.
	confirming bool
	// err is a failed action, shown rather than thrown: a dashboard that exited
	// because Docker refused would take the view of every other task with it.
	err error
}

// runtimeMsg carries the result of one runtime action.
//
// task is the task the request was made about. A response says nothing about
// which task it is for on its own, and an observe walks a project's Compose
// state, so one issued before the user moved on can arrive after: without this
// the screen drew one task's service table, allocated ports and retained volume
// names under another task's heading, which is the case ADR-041 wrote the
// re-open-on-selection rule for.
type runtimeMsg struct {
	task   string
	action api.RuntimeAction
	status api.RuntimeStatus
	err    error
}

// openRuntime shows the runtime screen for the task an action applies to.
//
// A draft opens it and is told there is nothing there yet, rather than being
// refused. A tab that declines to open is a tab the cycle cannot pass, and a
// user whose only task is a draft could otherwise reach neither the tab after it
// nor the one before.
func (m Model) openRuntime() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		m.screen = screenRuntime
		return m, nil
	}

	m.screen = screenRuntime
	m.selected = task.ID
	m.runtime = runtimeModel{task: task.ID}
	if isDraft(task) {
		return m, nil
	}
	// Opening the screen asks what is running. Nothing is started, and nothing
	// would be: v0 starts application services only when a user asks (FR-RUN-005).
	m.runtime.observing = true
	return m, m.runtimeAction(api.RuntimeObserve)
}

// runtimeAction performs one action against the daemon.
func (m Model) runtimeAction(action api.RuntimeAction) tea.Cmd {
	backend, id := m.backend, m.runtime.task
	return func() tea.Msg {
		status, err := backend.Runtime(context.Background(), id, action)
		return runtimeMsg{task: id, action: action, status: status, err: err}
	}
}

// runtimeKey routes a key press on the runtime screen.
func (m Model) runtimeKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A pending confirmation takes the keyboard: while Feat is asking whether to
	// remove something, no other key means anything.
	if m.runtime.confirming {
		switch key.String() {
		case "y", "Y":
			m.runtime.confirming = false
			return m.startRuntime(api.RuntimeDestroy)
		default:
			m.runtime.confirming = false
			m.status = "nothing was removed"
			return m, nil
		}
	}

	switch key.String() {
	case "ctrl+c", "q":
		m.quitting = true
		m.stopStream()
		return m, tea.Quit

	case "c":
		return m.startRuntime(api.RuntimeCreate)

	case "u":
		return m.startRuntime(api.RuntimeStart)

	case "t":
		return m.startRuntime(api.RuntimeStop)

	case "r":
		return m.startRuntime(api.RuntimeObserve)

	case "d":
		// Asked rather than done. Removing something is the one action on this
		// screen that a stray key press must not carry out.
		if m.runtime.pending != "" {
			return m.busyRuntime()
		}
		m.runtime.confirming = true
		return m, nil

	case "o":
		// Opening the logs was on l until ADR-046 reserved h, j, k, and l for
		// moving within the main region. Runtime has nothing to move through yet,
		// so the key was free in practice and reserved in principle — and a rule
		// with one exception is the thing this dashboard's keys were being fixed
		// for.
		return m.runtimeLogs()
	}
	// Everything this view does not claim is the dashboard's, including `a` and
	// `s` — which the task panel implements itself and this one had no answer for
	// at all, so attaching to the agent from the runtime view did nothing.
	return m.dashboardKey(key)
}

// startRuntime records what the screen is waiting for and asks for it.
//
// One at a time. A first start pulls images and runs builds, so the wait is
// minutes rather than the moment it is for a task whose services have been up
// before (ADR-034 evidence 14), and every key press during it would be another
// request queueing behind the same task's lock in the daemon — a user who
// pressed `u` twice would be starting the services and then starting them again.
func (m Model) startRuntime(action api.RuntimeAction) (tea.Model, tea.Cmd) {
	if m.runtime.pending != "" {
		return m.busyRuntime()
	}

	m.runtime.pending = action
	m.runtime.err = nil
	m.status = ""
	return m, m.runtimeAction(action)
}

// busyRuntime says why a key did nothing.
func (m Model) busyRuntime() (tea.Model, tea.Cmd) {
	m.status = "waiting for " + string(m.runtime.pending) + " to finish first"
	return m, nil
}

// runtimeLogs yields this terminal to the task's normal Compose logs.
func (m Model) runtimeLogs() (tea.Model, tea.Cmd) {
	backend, id := m.backend, m.runtime.task
	return m, func() tea.Msg {
		command, err := backend.LogsCommand(context.Background(), id)
		if err != nil {
			return execMsg{err: err}
		}
		return tea.Exec(command, func(err error) tea.Msg { return execMsg{err: err} })()
	}
}

// applyRuntime records what an action reported.
//
// A response for a task the screen is no longer about is dropped rather than
// drawn, as applyFrame drops a frame whose pane belongs to another task. Nothing
// is cleared for it either: the pending marker belongs to whatever this screen
// is waiting for now, and clearing it for a response the screen has moved past
// would report an action that has not finished as finished.
func (m Model) applyRuntime(message runtimeMsg) (tea.Model, tea.Cmd) {
	if message.task != m.runtime.task {
		return m, nil
	}

	m.runtime.pending, m.runtime.observing = "", false
	if message.err != nil {
		m.runtime.err = message.err
		return m, nil
	}

	m.runtime.err = nil
	m.runtime.status = message.status
	m.runtime.loaded = true
	if message.action != api.RuntimeObserve {
		m.status = "runtime " + string(message.action) + " on task " + message.status.Task.Key
	}
	// The task list carries the runtime summary too, so a completed action
	// refreshes it rather than leaving the dashboard behind this screen.
	return m, m.load()
}

// runtimeView renders the runtime screen as a whole terminal, which is what the
// narrow fallback draws when there is no room for the three regions.
func (m Model) runtimeView() string {
	if _, ok := m.task(m.selected); !ok {
		return m.runtimeBody() + m.footer(keyHints(keyHint("A", "task list"), keyHint("q", "quit")))
	}
	return m.runtimeBody() + m.footer(runtimeHints())
}

// runtimeBody renders the runtime tab's content, without a footer: in the
// three-region layout the frame owns the footer, so a tab that drew its own
// would put a second one in the middle of the screen.
func (m Model) runtimeBody() string {
	task, ok := m.task(m.selected)
	if !ok {
		return headingStyle.Render("runtime") + "\n\n" +
			mutedStyle.Render("this task is no longer listed")
	}

	var out strings.Builder
	out.WriteString(headingStyle.Render(task.Key+"  application runtime") + "\n")
	out.WriteString(mutedStyle.Render(task.ProjectID+" · "+task.Title) + "\n\n")

	switch {
	case isDraft(task):
		out.WriteString(mutedStyle.Render(
			"this task is still a draft; nothing has been created for it to run") + "\n")
	case task.Runtime == nil && !m.runtime.loaded:
		out.WriteString(mutedStyle.Render(m.activity.mark("reading what is running…")) + "\n")
	case task.Runtime == nil:
		out.WriteString(field("state", "absent  "+
			mutedStyle.Render("(nothing has been created; Feat starts services only when you ask)")))
	default:
		out.WriteString(m.runtimeSummary(task))
	}

	for _, note := range m.runtime.status.Notes {
		out.WriteString("\n" + attentionStyle.Render("note") + " " + note + "\n")
	}
	if m.runtime.err != nil {
		// Wrapped to the region, which this body has to ask for: it is the one tab
		// that is not re-flowed before it is drawn, so a long sentence was cut at
		// the edge — and the sentence that sends a user to their configuration ends
		// in the path.
		width, _ := m.mainRegionSize()
		out.WriteString("\n" + daemonNote(m.runtime.err, task, width) + "\n")
	}
	if m.runtime.pending != "" {
		waiting := "waiting for " + string(m.runtime.pending) + "…"
		if m.runtime.pending == api.RuntimeCreate || m.runtime.pending == api.RuntimeStart {
			// Said rather than left to be wondered about: the first one on a
			// machine pulls the project's images and runs its builds, and a wait
			// nobody explained is a wait a user reads as a hang.
			waiting += "  (the first one pulls images and runs builds, which takes minutes)"
		}
		out.WriteString("\n" + mutedStyle.Render(m.activity.mark(waiting)) + "\n")
	}
	if m.runtime.confirming {
		out.WriteString("\n" + attentionStyle.Render(
			"Remove the containers and networks of this task? Volumes are retained.  y to confirm") + "\n")
	}

	return out.String()
}

func runtimeHints() string {
	return keyHints(
		keyHint("c", "create"),
		keyHint("u", "start"),
		keyHint("t", "stop"),
		keyHint("o", "logs"),
		keyHint("d", "destroy"),
		keyHint("r", "refresh"),
		keyHint("q", "quit"),
	)
}

// runtimeSummary renders what a task's services are and what they own.
func (m Model) runtimeSummary(task api.Task) string {
	runtime := task.Runtime

	var out strings.Builder
	state := runtime.State
	if runtime.Health != "" && runtime.Health != "unknown" {
		state += ", health " + runtime.Health
	} else if runtime.State == "running" {
		// The honest answer where no health check is configured, said in words
		// rather than left to be inferred from a missing value (FR-RUN-007).
		state += mutedStyle.Render("  (health unknown)")
	}
	out.WriteString(field("state", state))
	out.WriteString(field("compose project", runtime.Identity))

	if services := m.runtime.status.Services; len(services) > 0 {
		// The source column appears only when there is something to say: a
		// column reading "configured" all the way down is a column nobody reads.
		dependencies := runtimeDependencies(services)

		out.WriteString("\n" + headingStyle.Render("services") + "\n")
		rows := make([][]string, 0, len(services))
		for _, service := range services {
			cells := []string{
				service.Name,
				valueOr(service.State, absent),
				valueOr(service.Health, absent),
				valueOr(service.Status, absent),
			}
			if dependencies {
				cells = append(cells, runtimeSource(service))
			}
			rows = append(rows, cells)
		}
		columns := []column{
			{title: "SERVICE", width: 16},
			{title: "STATE", width: 10},
			{title: "HEALTH", width: 9},
			{title: "STATUS", width: 28},
		}
		if dependencies {
			columns = append(columns, column{title: "SOURCE", width: 11})
		}
		out.WriteString(renderTable(columns, rows) + "\n")

		if dependencies {
			out.WriteString(mutedStyle.Render(
				"a dependency is a service Compose started because a configured one needs it; "+
					"Feat starts, stops, and removes it with the rest") + "\n")
		}
	}

	// The allocated addresses rather than the observed publications: they are
	// the same ports once the services are up, and they exist before anything
	// is started, which is when a user most wants to know where this task's
	// application will be.
	if len(runtime.Allocations) > 0 {
		out.WriteString("\n" + headingStyle.Render("ports") +
			mutedStyle.Render("  allocated for this task") + "\n")
		for _, allocation := range runtime.Allocations {
			// The address and the binding are two answers, because they are two
			// questions: the first is where to dial from this machine, which for a
			// binding on every interface is still localhost, and the second is what
			// the port is open to, which localhost says nothing about.
			out.WriteString("  " + allocation.Service + "  " +
				strconv.Itoa(allocation.ContainerPort) + " → " + allocation.Address +
				mutedStyle.Render("  bound on "+allocation.Binding()) + "\n")
		}
		if runtime.BoundEverywhere() {
			// Three lines rather than one, because the panel is narrow and a
			// sentence that runs off the right of it is one nobody finishes.
			for _, line := range []string{
				"a port bound on every address answers on every network this machine is joined to,",
				"and on the bridge every container is on. runtime.bind_address sets the default;",
				"an address a repository's own Compose file names is kept.",
			} {
				out.WriteString(mutedStyle.Render(line) + "\n")
			}
		}
	}
	if len(runtime.Volumes) > 0 {
		// Named because destroy retains every one of them: a resource nobody can
		// see is a resource nobody will remove (FR-CLEAN-004).
		out.WriteString("\n" + headingStyle.Render("volumes") +
			mutedStyle.Render("  retained by every destroy") + "\n")
		for _, volume := range runtime.Volumes {
			out.WriteString("  " + volume + "\n")
		}
	}
	return out.String()
}

// runtimeDependencies reports whether anything in the task's Compose project is
// there because a configured service needs it.
func runtimeDependencies(services []api.RuntimeService) bool {
	for _, service := range services {
		if !service.Managed {
			return true
		}
	}
	return false
}

// runtimeSource says where a service came from, in the user's terms.
func runtimeSource(service api.RuntimeService) string {
	if service.Managed {
		return "configured"
	}
	return "dependency"
}

// valueOr renders a value, or the absent marker when it is empty.
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
