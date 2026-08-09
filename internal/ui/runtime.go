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
	// confirming reports that destroy is waiting for a yes.
	confirming bool
	// err is a failed action, shown rather than thrown: a dashboard that exited
	// because Docker refused would take the view of every other task with it.
	err error
}

// runtimeMsg carries the result of one runtime action.
type runtimeMsg struct {
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
	return m, m.runtimeAction(api.RuntimeObserve)
}

// runtimeAction performs one action against the daemon.
func (m Model) runtimeAction(action api.RuntimeAction) tea.Cmd {
	backend, id := m.backend, m.runtime.task
	return func() tea.Msg {
		status, err := backend.Runtime(context.Background(), id, action)
		return runtimeMsg{action: action, status: status, err: err}
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
			m.runtime.pending = api.RuntimeDestroy
			return m, m.runtimeAction(api.RuntimeDestroy)
		default:
			m.runtime.confirming = false
			m.status = "nothing was removed"
			return m, nil
		}
	}

	switch key.String() {
	case "esc":
		m.screen = screenTask
		return m, nil

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
func (m Model) startRuntime(action api.RuntimeAction) (tea.Model, tea.Cmd) {
	m.runtime.pending = action
	m.runtime.err = nil
	m.status = ""
	return m, m.runtimeAction(action)
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
func (m Model) applyRuntime(message runtimeMsg) (tea.Model, tea.Cmd) {
	m.runtime.pending = ""
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
		return m.runtimeBody() + m.footer(keyHints(keyHint("esc", "back"), keyHint("q", "quit")))
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
		out.WriteString(mutedStyle.Render("reading what is running…") + "\n")
	case task.Runtime == nil:
		out.WriteString(field("state", "absent  "+
			mutedStyle.Render("(nothing has been created; Feat starts services only when you ask)")))
	default:
		out.WriteString(m.runtimeSummary(task))
	}

	if approvalOffer(task) != "" {
		out.WriteString("\n" + attentionStyle.Render(approvalOffer(task)) + "\n")
	}
	for _, note := range m.runtime.status.Notes {
		out.WriteString("\n" + attentionStyle.Render("note") + " " + note + "\n")
	}
	if m.runtime.err != nil {
		out.WriteString("\n" + failureStyle.Render(m.runtime.err.Error()) + "\n")
	}
	if m.runtime.pending != "" {
		out.WriteString("\n" + mutedStyle.Render("waiting for "+string(m.runtime.pending)+"…") + "\n")
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
		keyHint("esc", "back"),
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

	if len(runtime.Ports) > 0 {
		out.WriteString("\n" + headingStyle.Render("ports") + "\n")
		for _, port := range runtime.Ports {
			out.WriteString("  " + port.Service + "  " +
				strconv.Itoa(port.ContainerPort) + " → " + strconv.Itoa(port.HostPort) + "\n")
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
	if len(runtime.External) > 0 {
		out.WriteString("\n" + headingStyle.Render("external") +
			mutedStyle.Render("  never created or destroyed by Feat") + "\n")
		for _, resource := range runtime.External {
			line := "  " + resource.ID
			if resource.Kind != "" {
				line += mutedStyle.Render("  " + resource.Kind)
			}
			if resource.Selector != "" {
				line += "  " + mutedStyle.Render("this task selects "+resource.Selector)
			}
			out.WriteString(line + "\n")
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

// approvalOffer is the fifth acceptance criterion of slice 9, in words.
//
// An approved task whose services are still running is offered the stop and
// never given it: approval is a statement about the work, and the environment
// the user was testing it in is theirs to keep or to end
// (docs/02-user-workflows.md §7).
func approvalOffer(task api.Task) string {
	if task.Workflow != "approved" || task.Runtime == nil {
		return ""
	}
	switch task.Runtime.State {
	case "running", "degraded", "starting":
		return "this task is approved and its services are still running — press t to stop them; " +
			"Feat never stops them for you"
	default:
		return ""
	}
}

// valueOr renders a value, or the absent marker when it is empty.
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
