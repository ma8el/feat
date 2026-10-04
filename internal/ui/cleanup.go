package ui

import (
	"context"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// cleanupModel is the state of the cleanup dialog. It holds the plan it was
// shown, because the plan's token and warnings are what the removal carries
// back to the daemon (ADR-037).
type cleanupModel struct {
	// task is the task whose resources the dialog removes.
	task string
	key  string
	// plan is what the question is about.
	plan   api.CleanupPlan
	loaded bool
	// working reports that a request is in flight.
	working bool
	// removing reports that the removal itself is in flight. It keeps the
	// keyboard shut, because a removal in flight will not be abandoned.
	removing bool
	// err is a failed plan, shown rather than thrown.
	err error
	// failure is a removal that failed. It is kept apart from err because a failed
	// removal fires a resolve, and that resolve's success must not erase it.
	failure error
}

// cleanupPlanMsg carries a resolved plan.
type cleanupPlanMsg struct {
	plan api.CleanupPlan
	err  error
}

// cleanupDoneMsg carries the result of an executed cleanup.
type cleanupDoneMsg struct {
	status api.CleanupStatus
	err    error
}

// openCleanup resolves what the task owns and asks once whether to remove all
// of it and archive the task (ADR-110).
func (m Model) openCleanup() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if isDraft(task) {
		m.status = "task " + task.Key + " is a draft; nothing was created for it, so there is nothing to clean up"
		return m, nil
	}

	m.rememberTab()
	m.screen = screenCleanup
	m.selected = task.ID
	m.cleanup = cleanupModel{task: task.ID, key: task.Key, working: true}
	return m, m.cleanupPlan()
}

// cleanupPlan asks the daemon what the task owns.
func (m Model) cleanupPlan() tea.Cmd {
	backend, id := m.backend, m.cleanup.task
	return func() tea.Msg {
		plan, err := backend.CleanupPlan(context.Background(), id)
		return cleanupPlanMsg{plan: plan, err: err}
	}
}

// cleanupExecute removes everything the plan names and archives the task.
func (m Model) cleanupExecute() tea.Cmd {
	backend, id := m.backend, m.cleanup.task
	selection := m.cleanup.plan.Everything()
	return func() tea.Msg {
		status, err := backend.Cleanup(context.Background(), id, selection)
		return cleanupDoneMsg{status: status, err: err}
	}
}

// asking reports whether the question is on the screen and y would answer it.
func (c cleanupModel) asking() bool {
	return c.loaded && c.err == nil && !c.working && c.plan.Archivable
}

// cleanupKey routes a key press on the cleanup dialog.
func (m Model) cleanupKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cleanup.removing {
		if key.String() == "ctrl+c" {
			m.quitting = true
			m.stopStream()
			return m, tea.Quit
		}
		return m, nil
	}

	switch key.String() {
	case "y", "Y":
		if !m.cleanup.asking() {
			return m, nil
		}
		m.cleanup.failure = nil
		m.cleanup.working, m.cleanup.removing = true, true
		return m, m.cleanupExecute()

	case "ctrl+c", "q":
		m.quitting = true
		m.stopStream()
		return m, tea.Quit

	default:
		// Anything else is a no, as the [y/N] says. A plan is inert until it is
		// confirmed, so closing costs nothing.
		m.status = "nothing was removed"
		m.screen = screenFor(m.tab)
		return m, m.load()
	}
}

// applyCleanupPlan records a resolved plan.
func (m Model) applyCleanupPlan(message cleanupPlanMsg) (tea.Model, tea.Cmd) {
	m.cleanup.working = false
	m.cleanup.err = message.err
	if message.err != nil {
		return m, nil
	}
	m.cleanup.plan = message.plan
	m.cleanup.loaded = true
	return m, nil
}

// applyCleanupResult records what a cleanup removed. Success closes the dialog,
// because an archived task is one the daemon will no longer resolve.
//
// A failure keeps it open and resolves again, so the question that follows is
// about what is left and what it now costs. That also covers the daemon refusing
// a warning that appeared after the question was asked (ADR-037).
func (m Model) applyCleanupResult(message cleanupDoneMsg) (tea.Model, tea.Cmd) {
	m.cleanup.working, m.cleanup.removing = false, false
	m.cleanup.failure = message.err
	if message.err != nil {
		m.cleanup.working = true
		return m, tea.Batch(m.cleanupPlan(), m.load(), m.reconcile())
	}

	m.status = m.cleanup.summary(message.status)
	m.screen = screenFor(m.tab)
	m.cleanup = cleanupModel{}
	return m, tea.Batch(m.load(), m.reconcile())
}

// summary is the footer line a finished cleanup leaves behind. A resource that
// was already gone is counted rather than named; the event log names each one.
func (c cleanupModel) summary(status api.CleanupStatus) string {
	var classes []string
	absent := 0
	for _, removal := range status.Removed {
		if !removal.Removed {
			absent++
			continue
		}
		if title := c.title(removal.Class); !slices.Contains(classes, title) {
			classes = append(classes, title)
		}
	}

	summary := "task " + c.key + ": "
	switch {
	case len(classes) > 0 && absent > 0:
		summary += "removed the " + strings.Join(classes, ", ") + ", and " +
			count(absent, "resource was already gone", "resources were already gone")
	case len(classes) > 0:
		summary += "removed the " + strings.Join(classes, ", ")
	case absent > 0:
		summary += count(absent, "resource was already gone", "resources were already gone") +
			", so nothing was removed"
	default:
		summary += "nothing was removed"
	}
	if status.Archived {
		summary += "; it is archived, and Feat has stopped tracking it"
	}
	return summary
}

// title renders a class the way the plan named it.
func (c cleanupModel) title(class string) string {
	for _, entry := range c.plan.Classes {
		if entry.Class == class {
			return entry.Title
		}
	}
	return class
}

// cleanupView renders cleanup as a whole terminal, which is what the narrow
// fallback draws when there is no room for the three regions.
func (m Model) cleanupView() string {
	return titleStyle.Render("Cleanup — task "+m.cleanup.key) + "\n\n" +
		m.cleanupBody() + m.footer(m.cleanupHints())
}

// cleanupTitle names the task the dialog is about, for its border.
func (m Model) cleanupTitle() string { return "task " + m.cleanup.key }

// cleanupFailureLines is the fewest lines a failure is given, whatever the
// terminal. A failed `git worktree remove` runs to some five hundred cells.
const cleanupFailureLines = 4

// cleanupWidth is the width the body is drawn in.
func (m Model) cleanupWidth() int {
	if m.narrow() {
		width, _ := m.frameSize()
		return width
	}
	widest, _ := m.dialogLimits()
	return max(widest, dialogSmallest) - dialogChrome
}

// cleanupBody renders the dialog's content: the question and what it would
// cost, or why it cannot be asked.
func (m Model) cleanupBody() string {
	width := m.cleanupWidth()
	var out strings.Builder

	switch {
	case m.cleanup.removing:
		out.WriteString(mutedStyle.Render(truncate(
			m.activity.mark("removing everything task "+m.cleanup.key+" owns…"), width)) + "\n")
	case m.cleanup.working:
		out.WriteString(mutedStyle.Render(m.activity.mark("resolving what this task owns…")) + "\n")
	case m.cleanup.err != nil:
		out.WriteString(m.cleanupMessage(m.cleanup.err, width) + "\n")
	case !m.cleanup.plan.Archivable:
		for _, problem := range m.cleanup.plan.Problems {
			out.WriteString(failureStyle.Render(truncate(plainLine("! "+problem), width)) + "\n")
		}
		out.WriteString("\n" + mutedStyle.Render(wrapNote(
			"Feat cannot archive this task until these are resolved; nothing was removed.", width)) + "\n")
	default:
		out.WriteString(m.cleanupQuestion(width))
	}

	if m.cleanup.failure != nil && !m.cleanup.removing {
		out.WriteString("\n" + m.cleanupMessage(m.cleanup.failure, width) + "\n")
	}
	return out.String()
}

// cleanupQuestion is the one question cleanup asks, with every risk under it.
func (m Model) cleanupQuestion(width int) string {
	var titles []string
	for _, class := range m.cleanup.plan.Classes {
		titles = append(titles, class.Title)
	}

	out := failureStyle.Render(truncate("Clean up and archive task "+m.cleanup.key+"? [y/N]", width)) + "\n"
	if len(titles) > 0 {
		out += mutedStyle.Render(wrapNote("This removes its "+strings.Join(titles, ", ")+".", width)) + "\n"
	}
	if risks := m.cleanup.plan.Risks(); len(risks) > 0 {
		out += "\n"
		for _, risk := range risks {
			out += failureStyle.Render(wrapNote("! "+risk+" (potential data loss)", width)) + "\n"
		}
	}
	return out
}

// cleanupMessage renders a daemon error without the wire's classification,
// because the part worth reading is at the end of the sentence.
func (m Model) cleanupMessage(err error, width int) string {
	message := wrapNote(plainText(daemonMessage(err, api.Task{})), width)
	_, tallest := m.dialogLimits()
	return clampHeight(failureStyle.Render(message), max(cleanupFailureLines, tallest/2), width)
}

// cleanupHints renders the key map.
func (m Model) cleanupHints() string {
	switch {
	case m.cleanup.removing:
		return mutedStyle.Render("removing…")
	case m.cleanup.asking():
		return keyHints(keyHint("y", "clean up and archive"), keyHint("n", "cancel"))
	default:
		return keyHints(keyHint("esc", "back"))
	}
}
