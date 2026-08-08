package ui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// cleanupModel is the state of the cleanup screen.
//
// It holds the plan it was shown rather than re-deriving it, because the plan's
// token is what the execution carries back: a screen that rebuilt its own list
// could send a selection for something the user never read.
type cleanupModel struct {
	// task is the task whose resources the screen removes.
	task string
	key  string
	// plan is the inventory the user is looking at.
	plan   api.CleanupPlan
	loaded bool
	// cursor is the class the keyboard is on.
	cursor int
	// chosen records which classes are selected, by class identifier.
	chosen map[string]bool
	// accepted records which classes had their warnings confirmed. A class with
	// warnings is not removable until it is in here, which is what makes the
	// confirmation explicit rather than implied by the selection (FR-CLEAN-003).
	accepted map[string]bool
	// archive asks for the task's metadata to be archived afterwards.
	archive bool
	// confirming is the class whose warnings are being put to the user, empty
	// when none is.
	confirming string
	// executing reports that the final confirmation is outstanding.
	executing bool
	// result is what the last cleanup removed.
	result api.CleanupStatus
	done   bool
	// working reports that a request is in flight.
	working bool
	// err is a failed plan or cleanup, shown rather than thrown.
	err error
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

// openCleanup shows the cleanup screen for the task an action applies to.
//
// Opening it resolves the inventory and removes nothing, which is what makes it
// safe to reach with one key press: what a task owns is a question, and only the
// answers a user selects are ever acted on.
func (m Model) openCleanup() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if isDraft(task) {
		m.status = "task " + task.Key + " is a draft; nothing was created for it, so there is nothing to clean up"
		return m, nil
	}

	m.screen = screenCleanup
	m.selected = task.ID
	m.cleanup = cleanupModel{
		task:     task.ID,
		key:      task.Key,
		chosen:   make(map[string]bool),
		accepted: make(map[string]bool),
		working:  true,
	}
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

// cleanupExecute sends the selection.
func (m Model) cleanupExecute() tea.Cmd {
	backend, id := m.backend, m.cleanup.task
	selection := m.cleanup.selection()
	return func() tea.Msg {
		status, err := backend.Cleanup(context.Background(), id, selection)
		return cleanupDoneMsg{status: status, err: err}
	}
}

// selection turns what the screen holds into a request.
//
// The confirmed warnings are the plan's own strings rather than a flag, so that
// the daemon can compare them with what is true at the moment of removal and
// refuse a confirmation that has been overtaken (ADR-037).
func (c cleanupModel) selection() api.CleanupSelection {
	selection := api.CleanupSelection{Token: c.plan.Token, Archive: c.archive}
	for _, class := range c.plan.Classes {
		if !c.chosen[class.Class] {
			continue
		}
		selection.Classes = append(selection.Classes, api.CleanupChoice{
			Class:             class.Class,
			ConfirmedWarnings: class.Warnings,
		})
	}
	return selection
}

// removable reports whether every selected class is ready to be removed.
func (c cleanupModel) removable() bool {
	chosen := false
	for _, class := range c.plan.Classes {
		if !c.chosen[class.Class] {
			continue
		}
		chosen = true
		if len(class.Warnings) > 0 && !c.accepted[class.Class] {
			return false
		}
	}
	return chosen
}

// archivable reports whether archiving may be offered.
//
// Only when every class the plan names is selected, because the daemon refuses
// it otherwise: an archived task that still owns a running container is exactly
// the orphan reconciliation exists to report.
func (c cleanupModel) archivable() bool {
	if !c.plan.Archivable || len(c.plan.Classes) == 0 {
		return false
	}
	for _, class := range c.plan.Classes {
		if !c.chosen[class.Class] {
			return false
		}
	}
	return true
}

// cleanupKey routes a key press on the cleanup screen.
func (m Model) cleanupKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A pending question takes the keyboard, as it does on the runtime screen:
	// while Feat is asking whether to remove something, no other key means
	// anything.
	if m.cleanup.confirming != "" {
		class := m.cleanup.confirming
		m.cleanup.confirming = ""
		switch key.String() {
		case "y", "Y":
			m.cleanup.accepted[class] = true
		default:
			m.cleanup.chosen[class] = false
			m.status = "left the " + m.cleanup.title(class) + " alone"
		}
		return m, nil
	}
	if m.cleanup.executing {
		m.cleanup.executing = false
		switch key.String() {
		case "y", "Y":
			m.cleanup.working = true
			return m, m.cleanupExecute()
		default:
			m.status = "nothing was removed"
			return m, nil
		}
	}

	switch key.String() {
	case "esc":
		m.screen = screenDetail
		return m, m.load()

	case "ctrl+c", "q":
		m.quitting = true
		m.stopStream()
		return m, tea.Quit

	case "up", "k":
		if m.cleanup.cursor > 0 {
			m.cleanup.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cleanup.cursor < len(m.cleanup.plan.Classes)-1 {
			m.cleanup.cursor++
		}
		return m, nil

	case " ", "x":
		return m.toggleCleanupClass()

	case "A":
		if m.cleanup.archivable() {
			m.cleanup.archive = !m.cleanup.archive
		} else {
			m.status = "archiving needs every class selected: a task Feat stops tracking must own nothing"
		}
		return m, nil

	case "enter":
		if !m.cleanup.removable() {
			m.status = "select a class first; a class with warnings needs its confirmation too"
			return m, nil
		}
		m.cleanup.executing = true
		return m, nil

	case "r":
		m.cleanup.working = true
		m.status = ""
		return m, m.cleanupPlan()
	}
	return m, nil
}

// toggleCleanupClass selects or deselects the class under the cursor, asking
// about its warnings when it is selected.
func (m Model) toggleCleanupClass() (tea.Model, tea.Cmd) {
	if m.cleanup.cursor >= len(m.cleanup.plan.Classes) {
		return m, nil
	}
	class := m.cleanup.plan.Classes[m.cleanup.cursor]

	if m.cleanup.chosen[class.Class] {
		m.cleanup.chosen[class.Class] = false
		m.cleanup.accepted[class.Class] = false
		// Deselecting anything invalidates an archive: it would no longer be
		// removing everything the plan names.
		m.cleanup.archive = false
		return m, nil
	}

	m.cleanup.chosen[class.Class] = true
	if len(class.Warnings) > 0 && !m.cleanup.accepted[class.Class] {
		m.cleanup.confirming = class.Class
	}
	return m, nil
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

// applyCleanupPlan records a resolved inventory.
func (m Model) applyCleanupPlan(message cleanupPlanMsg) (tea.Model, tea.Cmd) {
	m.cleanup.working = false
	m.cleanup.err = message.err
	if message.err != nil {
		return m, nil
	}
	m.cleanup.plan = message.plan
	m.cleanup.loaded = true
	m.cleanup.done = false
	if m.cleanup.cursor >= len(message.plan.Classes) {
		m.cleanup.cursor = 0
	}
	return m, nil
}

// applyCleanupResult records what a cleanup removed.
func (m Model) applyCleanupResult(message cleanupDoneMsg) (tea.Model, tea.Cmd) {
	m.cleanup.working = false
	m.cleanup.err = message.err
	if message.err != nil {
		return m, nil
	}
	m.cleanup.result = message.status
	m.cleanup.done = true
	m.cleanup.chosen = make(map[string]bool)
	m.cleanup.accepted = make(map[string]bool)
	m.cleanup.archive = false
	// The inventory is read again, because what a task owns has just changed and
	// the token that named the old set is no longer the token of anything. The
	// recovery band is refreshed with it, for the same reason: a cleanup is
	// precisely the thing that resolves what the band was reporting, and a band
	// still naming it afterwards would be describing the past.
	m.cleanup.working = true
	return m, tea.Batch(m.cleanupPlan(), m.load(), m.reconcile())
}

// cleanupView renders the screen.
func (m Model) cleanupView() string {
	var out strings.Builder
	out.WriteString(titleStyle.Render("Cleanup — task "+m.cleanup.key) + "\n\n")

	switch {
	case m.cleanup.err != nil:
		out.WriteString(failureStyle.Render(m.cleanup.err.Error()) + "\n")
	case m.cleanup.working && !m.cleanup.loaded:
		out.WriteString(mutedStyle.Render("resolving what this task owns…") + "\n")
	case !m.cleanup.loaded:
		out.WriteString(mutedStyle.Render("nothing has been resolved yet") + "\n")
	case len(m.cleanup.plan.Classes) == 0:
		out.WriteString(mutedStyle.Render("Feat resolved no resources for this task.") + "\n")
	default:
		out.WriteString(m.cleanupClasses())
	}

	for _, problem := range m.cleanup.plan.Problems {
		out.WriteString("\n" + failureStyle.Render("! "+problem) + "\n")
	}
	if m.cleanup.done {
		out.WriteString("\n" + m.cleanupResult())
	}

	out.WriteString(m.cleanupPrompt())
	return out.String() + m.footer(m.cleanupHints())
}

// cleanupClasses renders the inventory with the selection.
func (m Model) cleanupClasses() string {
	var out strings.Builder
	for i, class := range m.cleanup.plan.Classes {
		marker := " "
		if m.cleanup.chosen[class.Class] {
			marker = "x"
		}
		pointer := "  "
		if i == m.cleanup.cursor {
			pointer = "> "
		}
		out.WriteString(pointer + "[" + marker + "] " + class.Title + "\n")

		for _, target := range class.Targets {
			state := ""
			if !target.Present {
				state = mutedStyle.Render("  (already gone)")
			}
			out.WriteString("        " + target.Identity + state + "\n")
		}
		for _, warning := range class.Warnings {
			line := "        ! " + warning
			if m.cleanup.accepted[class.Class] {
				line += " (confirmed)"
			}
			out.WriteString(failureStyle.Render(line) + "\n")
		}
	}

	if m.cleanup.archivable() {
		marker := " "
		if m.cleanup.archive {
			marker = "x"
		}
		out.WriteString("\n  [" + marker + "] archive the task's metadata; its record and history are kept\n")
	}
	return out.String()
}

// cleanupResult renders what the last cleanup removed.
func (m Model) cleanupResult() string {
	var out strings.Builder
	for _, removal := range m.cleanup.result.Removed {
		if removal.Removed {
			out.WriteString("removed " + removal.Identity + "\n")
			continue
		}
		out.WriteString(mutedStyle.Render(removal.Identity+" was already gone") + "\n")
	}
	if m.cleanup.result.Archived {
		out.WriteString("task " + m.cleanup.key + " is archived\n")
	}
	return out.String()
}

// cleanupPrompt renders whichever question is outstanding.
func (m Model) cleanupPrompt() string {
	if m.cleanup.confirming != "" {
		warnings := ""
		for _, class := range m.cleanup.plan.Classes {
			if class.Class == m.cleanup.confirming {
				warnings = strings.Join(class.Warnings, "; ")
			}
		}
		return "\n" + failureStyle.Render(warnings+". Remove the "+
			m.cleanup.title(m.cleanup.confirming)+" anyway? [y/N]") + "\n"
	}
	if m.cleanup.executing {
		var chosen []string
		for _, class := range m.cleanup.plan.Classes {
			if m.cleanup.chosen[class.Class] {
				chosen = append(chosen, class.Title)
			}
		}
		line := "Remove the " + strings.Join(chosen, ", ") + " of task " + m.cleanup.key
		if m.cleanup.archive {
			line += ", and archive it"
		}
		return "\n" + failureStyle.Render(line+"? [y/N]") + "\n"
	}
	return ""
}

// cleanupHints renders the key map.
func (m Model) cleanupHints() string {
	return mutedStyle.Render(
		"[space] select  [A] archive  [enter] remove selected  [r] re-resolve  [esc] back  [q] quit")
}
