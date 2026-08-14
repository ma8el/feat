package ui

import (
	"context"
	"slices"
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
	// scroll is the first line of the inventory the region shows. A plan for a
	// three-repository task is longer than the dialog it is drawn in, and an
	// inventory that is merely clipped is one a user has to leave for the command
	// line to finish reading (FR-CLEAN-001).
	scroll int
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

	m.rememberTab()
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
		// Back to the tab the dialog opened over. Closing costs nothing here
		// because a plan is inert until it is confirmed: what ADR-037 made
		// deliberate is executing one, not opening the screen that lists it.
		m.screen = screenFor(m.tab)
		return m, m.load()

	case "ctrl+c", "q":
		m.quitting = true
		m.stopStream()
		return m, tea.Quit

	case "up", "k":
		if m.cleanup.cursor > 0 {
			m.cleanup.cursor--
			m.cleanup.scroll = m.cleanupFollow()
		}
		return m, nil

	case "down", "j":
		if m.cleanup.cursor < len(m.cleanup.plan.Classes)-1 {
			m.cleanup.cursor++
			m.cleanup.scroll = m.cleanupFollow()
		}
		return m, nil

	// The page keys move the window without moving the choice, as they do on the
	// task panel. They are what reaches the rest of a class whose own targets are
	// more than the region holds, which the cursor cannot do: the cursor stops at
	// classes, and one class can be longer than the window.
	case "pgup":
		m.cleanup.scroll = m.cleanupPage(-panelPage)
		return m, nil

	case "pgdown":
		m.cleanup.scroll = m.cleanupPage(panelPage)
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
		m.cleanup.cursor, m.cleanup.scroll = 0, 0
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

// cleanupView renders cleanup as a whole terminal, which is what the narrow
// fallback draws when there is no room for the three regions.
func (m Model) cleanupView() string {
	return titleStyle.Render("Cleanup — task "+m.cleanup.key) + "\n\n" +
		m.cleanupBody() + m.footer(m.cleanupHints())
}

// cleanupTitle names the task the dialog is about, for its border.
func (m Model) cleanupTitle() string { return "task " + m.cleanup.key }

// What the screen draws around its body, which the body has less room for.
const (
	// cleanupTitleHeight is the narrow fallback's own title and the blank line
	// under it.
	cleanupTitleHeight = 2
	// cleanupHintsHeight is the blank line and the key map the dialog puts under
	// the body.
	cleanupHintsHeight = 2
)

// cleanupRegion is the space the body is drawn in, in cells and lines.
//
// The body needs its own size because the inventory scrolls, and it is resolved
// here rather than passed in so that the keyboard and the renderer agree on it:
// a cursor that moves below the window has to bring the window with it, which is
// a decision the key handler makes and rendering cannot write back.
func (m Model) cleanupRegion() (width, height int) {
	if m.narrow() {
		width, height = m.frameSize()
		return width, height - stackedFooterHeight - cleanupTitleHeight
	}

	widest, tallest := m.dialogLimits()
	if widest < dialogSmallest {
		widest = dialogSmallest
	}
	return widest - dialogChrome, tallest - dialogVerticalChrome - cleanupHintsHeight
}

// cleanupInventorySize is what is left of the region once everything drawn
// around the class list has taken its own lines.
//
// Never less than a line of the list and the note that says there is more of it.
// A region that small is one whose dialog is about to be clamped whatever this
// returns, and a window with nothing in it would report an inventory without
// showing any of it.
func (m Model) cleanupInventorySize() (width, height int) {
	width, height = m.cleanupRegion()
	height -= drawnLines(m.cleanupSubject(width)) + drawnLines(m.cleanupTail(width))
	if height < 2 {
		height = 2
	}
	return width, height
}

// drawnLines is how many lines a newline-terminated block occupies.
//
// Counted by its terminators rather than by splitting it, so that a block with
// nothing in it — a prompt with no question outstanding — measures nought lines
// rather than one.
func drawnLines(block string) int { return strings.Count(block, "\n") }

// cleanupBody renders the cleanup dialog's content.
//
// Cleanup is an overlay rather than a screen because it is a transaction the
// user opened and can cancel, and because the task list it is about stays
// readable behind it (ADR-041). Its own hints stay with it: the frame's footer
// says how to close a dialog, and this says what the dialog can do.
//
// Everything around the inventory is drawn whatever the room — what is being
// cleaned up, what the plan could not resolve, what the last removal did, and
// whichever question is outstanding — and the class list takes what is left. A
// question a user cannot see is one nobody can answer, and the class list is the
// one part of this that a task with three repositories makes longer than the
// terminal.
func (m Model) cleanupBody() string {
	width, height := m.cleanupInventorySize()

	var out strings.Builder
	out.WriteString(m.cleanupSubject(width))

	switch {
	case m.cleanup.err != nil:
		// Flattened to one line, as the footer flattens the errors it shows: this
		// one comes from the daemon and may carry a command's output, and a block
		// that wraps here is a block drawn over the question below it (ADR-054).
		out.WriteString(failureStyle.Render(truncate(plainLine(m.cleanup.err.Error()), width)) + "\n")
	case m.cleanup.working && !m.cleanup.loaded:
		out.WriteString(mutedStyle.Render("resolving what this task owns…") + "\n")
	case !m.cleanup.loaded:
		out.WriteString(mutedStyle.Render("nothing has been resolved yet") + "\n")
	case len(m.cleanup.plan.Classes) == 0:
		out.WriteString(mutedStyle.Render("Feat resolved no resources for this task.") + "\n")
	default:
		out.WriteString(m.cleanupInventory(width, height))
	}

	out.WriteString(m.cleanupTail(width))
	return out.String()
}

// cleanupSubject says what is being cleaned up.
//
// The project and the workflow, because a task still working on something is a
// different decision from an approved one — which is the reason the plan carries
// its workflow at all, and which `feat task cleanup` has said in its first line
// since the command existed. The border names the task; this names its situation.
func (m Model) cleanupSubject(width int) string {
	if !m.cleanup.loaded {
		return ""
	}
	subject := "project " + m.cleanup.plan.ProjectID
	if workflow := m.cleanup.plan.Workflow; workflow != "" {
		subject += "  ·  " + workflow
	}
	return mutedStyle.Render(truncate(subject, width)) + "\n\n"
}

// cleanupTail is what is drawn under the inventory: the archive choice, whatever
// the plan refused to resolve, what the last cleanup removed, and the question
// that is outstanding.
func (m Model) cleanupTail(width int) string {
	var out strings.Builder

	if m.cleanup.archivable() {
		marker := " "
		if m.cleanup.archive {
			marker = "x"
		}
		out.WriteString("\n" + truncate(
			"  ["+marker+"] archive the task's metadata; its record and history are kept", width) + "\n")
	}
	for _, problem := range m.cleanup.plan.Problems {
		out.WriteString("\n" + failureStyle.Render(truncate(plainLine("! "+problem), width)) + "\n")
	}
	if m.cleanup.done {
		out.WriteString("\n" + m.cleanupResult())
	}

	out.WriteString(m.cleanupPrompt())
	return out.String()
}

// cleanupInventory draws the class list into the lines the region left it,
// scrolled to wherever the window is.
func (m Model) cleanupInventory(width, height int) string {
	lines := m.cleanupLines(width)
	if len(lines) <= height {
		return strings.Join(lines, "\n") + "\n"
	}

	// One line of the region belongs to the note, so the window is that much
	// shorter than the space. The note is what keeps this an inventory: a list
	// clipped in silence reads as a list that is short, and a user deciding what
	// to remove would be deciding about resources the screen never mentioned.
	visible := height - 1
	offset := clampScroll(m.cleanup.scroll, len(lines), height)
	window := lines[offset : offset+visible]

	var parts []string
	if offset > 0 {
		parts = append(parts, count(offset, "line above", "lines above"))
	}
	if below := len(lines) - offset - visible; below > 0 {
		parts = append(parts, count(below, "line below", "lines below"))
	}
	note := strings.Join(parts, ", ") + "  ·  pgup/pgdn to scroll"

	return strings.Join(window, "\n") + "\n" + mutedStyle.Render(truncate(note, width)) + "\n"
}

// cleanupLines is the whole inventory as the lines it will be drawn as.
func (m Model) cleanupLines(width int) []string {
	var lines []string
	for i := range m.cleanup.plan.Classes {
		lines = append(lines, m.cleanupClassLines(i, width)...)
	}
	return lines
}

// cleanupClassSpan is where the class under the cursor begins in the inventory
// and how many lines it takes.
func (m Model) cleanupClassSpan(width int) (at, span int) {
	for i := range m.cleanup.plan.Classes {
		block := m.cleanupClassLines(i, width)
		if i == m.cleanup.cursor {
			return at, len(block)
		}
		at += len(block)
	}
	return at, 0
}

// cleanupClassLines renders one class: the choice, what it would remove, and
// what that would cost.
//
// Every target carries all three of the things the plan says about it — what it
// is, the sentence describing it, and whether it is still there — because that
// is the difference between an inventory and a count. The screen used to draw
// the identity alone, so a worktree said only its path and a volume said only a
// name that begins with a Compose project: two lines a user would have to leave
// the dashboard and run `feat task cleanup` to expand (FR-CLEAN-001).
func (m Model) cleanupClassLines(index, width int) []string {
	class := m.cleanup.plan.Classes[index]

	marker := " "
	if m.cleanup.chosen[class.Class] {
		marker = "x"
	}
	pointer := "  "
	if index == m.cleanup.cursor {
		pointer = "> "
	}
	head := pointer + "[" + marker + "] " + class.Title
	// The consent belongs to the class, so its state is said on the class rather
	// than beside whichever of the warnings happens to be drawn first.
	if len(class.Warnings) > 0 {
		if m.cleanup.accepted[class.Class] {
			head += mutedStyle.Render("  (confirmed)")
		} else {
			head += failureStyle.Render("  (needs confirmation)")
		}
	}
	lines := []string{truncate(head, width)}

	shared := sharedWarnings(class)
	for _, warning := range shared {
		lines = append(lines, failureStyle.Render(truncate("      ! "+warning, width)))
	}

	for _, target := range class.Targets {
		identity := "        " + target.Identity
		if !target.Present {
			identity += mutedStyle.Render("  (already gone)")
		}
		lines = append(lines, truncate(identity, width))

		if target.Detail != "" {
			lines = append(lines, mutedStyle.Render(truncate("            "+target.Detail, width)))
		}
		for _, warning := range target.Warnings {
			if slices.Contains(shared, warning) {
				continue
			}
			lines = append(lines, failureStyle.Render(truncate("            ! "+warning, width)))
		}
	}
	return lines
}

// sharedWarnings are the class's warnings that every one of its targets carries.
//
// A warning true of all of them is a property of the class — every volume
// discards what it holds, whichever volume it is — and is said once, under the
// title the confirmation is attached to. One true of only some of them belongs
// beside the target it is true of: a class of three worktrees where one has
// uncommitted work has to say which, and a warning hoisted to the title says
// only that a worktree does.
func sharedWarnings(class api.CleanupClass) []string {
	if len(class.Targets) == 0 {
		return class.Warnings
	}

	var shared []string
	for _, warning := range class.Warnings {
		common := true
		for _, target := range class.Targets {
			if !slices.Contains(target.Warnings, warning) {
				common = false
				break
			}
		}
		if common {
			shared = append(shared, warning)
		}
	}
	return shared
}

// cleanupFollow is where the window goes once the cursor has moved.
//
// The class the cursor is on is drawn whole wherever there is room for it,
// because a class a user cannot see is one they cannot check before pressing
// enter. A class taller than the window is shown from its top, and the page keys
// reach the rest of it.
func (m Model) cleanupFollow() int {
	width, height := m.cleanupInventorySize()
	lines := m.cleanupLines(width)
	if len(lines) <= height {
		return 0
	}

	at, span := m.cleanupClassSpan(width)
	offset := m.cleanup.scroll
	if visible := height - 1; at+span > offset+visible {
		offset = at + span - visible
	}
	if at < offset {
		offset = at
	}
	return clampScroll(offset, len(lines), height)
}

// cleanupPage is where the page keys leave the window, bounded by the inventory.
//
// The bound is applied here rather than while rendering for the reason the task
// panel's is: rendering cannot write back, so holding a page key past the end
// would build up an offset that took as many presses to undo.
func (m Model) cleanupPage(delta int) int {
	width, height := m.cleanupInventorySize()
	return clampScroll(m.cleanup.scroll+delta, len(m.cleanupLines(width)), height)
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
//
// It is one line inside three quarters of the terminal, and the line it used to
// be was ninety cells of it: on a terminal at the layout's own minimum width the
// last two hints were truncated away, which is two keys nobody could find. The
// page keys are not here — they are named in the note that appears when there is
// something to scroll to, which is the only time they mean anything.
func (m Model) cleanupHints() string {
	return keyHints(
		keyHint("space", "select"),
		keyHint("A", "archive"),
		keyHint("enter", "remove"),
		keyHint("r", "re-resolve"),
		keyHint("esc", "back"),
	)
}
