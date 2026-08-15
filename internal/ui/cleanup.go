package ui

import (
	"context"
	"fmt"
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
	// archive asks for the task's metadata to be archived afterwards.
	archive bool
	// executing reports that the removal's confirmation is outstanding.
	//
	// It is the only question the screen asks, and it carries the warnings of
	// everything selected. FR-CLEAN-003 asks for an explicit confirmation of work
	// that would be lost, which is a property of the removal rather than of each
	// tick that led to it: a question per class interrupts the selection it is
	// part of, and asks about a decision the user has not finished making
	// (ADR-061).
	executing bool
	// result is what the last cleanup removed.
	result api.CleanupStatus
	done   bool
	// working reports that a request is in flight.
	working bool
	// pending reports that a confirmation is waiting on the plan in flight.
	//
	// Enter resolves before it asks, so that the question is put against what is
	// true now rather than against what was true when the screen opened. A task
	// being cleaned up is often one whose agent is still working in it, and the
	// warnings are what move while a user reads (ADR-061).
	pending bool
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
		task:    task.ID,
		key:     task.Key,
		chosen:  make(map[string]bool),
		working: true,
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

// removable reports whether there is anything to remove.
//
// Only that something is selected. What removing it would cost is put to the
// user by the confirmation rather than gating the key that opens it: a class is
// selected by one press and removed by two more, and the second of those is
// where the warnings are (ADR-061).
func (c cleanupModel) removable() bool {
	for _, class := range c.plan.Classes {
		if c.chosen[class.Class] {
			return true
		}
	}
	return false
}

// pendingWarnings is every distinct warning of everything selected, in the order
// the plan removes the classes in.
//
// Distinct across classes as well as within one, because the volumes of three
// repositories carry the same standing sentence and a confirmation that says it
// three times is one a user reads once.
func (c cleanupModel) pendingWarnings() []string {
	var warnings []string
	for _, class := range c.plan.Classes {
		if !c.chosen[class.Class] {
			continue
		}
		for _, warning := range class.Warnings {
			if !slices.Contains(warnings, warning) {
				warnings = append(warnings, warning)
			}
		}
	}
	return warnings
}

// archiveRow is where the cursor finds the archive choice, and -1 for a plan
// that has none.
//
// It is a row of the screen rather than a key of its own, so that everything on
// the screen with a checkbox is reached the same way: down to it, space to tick
// it. It sits after the classes because that is where it is drawn, and it is
// drawn there because it is what happens once they are gone (ADR-061).
func (c cleanupModel) archiveRow() int {
	if !c.plan.Archivable || len(c.plan.Classes) == 0 {
		return -1
	}
	return len(c.plan.Classes)
}

// lastRow is the furthest down the cursor goes.
func (c cleanupModel) lastRow() int {
	if row := c.archiveRow(); row >= 0 {
		return row
	}
	return len(c.plan.Classes) - 1
}

// archivable reports whether archiving may be taken.
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
	// A question being prepared takes it too. The selection this is resolving
	// against is the one enter was pressed on, and a tick landing in between would
	// put a class into the confirmation that the plan under it was never checked
	// for. Escape abandons the question rather than the screen, because a user who
	// changed their mind about asking has not asked to leave.
	if m.cleanup.pending {
		if key.String() == "esc" {
			m.cleanup.pending = false
			m.status = "nothing was removed"
		}
		return m, nil
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
		if m.cleanup.cursor < m.cleanup.lastRow() {
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
		return m.toggleCleanupRow()

	case "enter":
		if !m.cleanup.removable() {
			m.status = "select what to remove first"
			return m, nil
		}
		// The question is asked of a plan resolved now. Everything on the screen
		// is an observation of the moment it was taken, and the moment a user
		// decides is this one.
		m.cleanup.working, m.cleanup.pending = true, true
		m.status = ""
		return m, m.cleanupPlan()
	}
	return m, nil
}

// toggleCleanupRow ticks whatever the cursor is on.
//
// One key for every checkbox on the screen. The archive choice used to have a
// key of its own, which meant a row the cursor could not reach and a key that
// did nothing for most of the interaction — a second way of doing the one thing
// this screen does (ADR-061).
//
// Ticking asks nothing. It is a user assembling a decision, not making one, and
// the screen already draws what each class would cost beside the resources it is
// true of, so a question here would be about something still being read.
func (m Model) toggleCleanupRow() (tea.Model, tea.Cmd) {
	if m.cleanup.cursor == m.cleanup.archiveRow() {
		if !m.cleanup.archivable() {
			m.status = "archiving needs every class selected: a task Feat stops tracking must own nothing"
			return m, nil
		}
		m.cleanup.archive = !m.cleanup.archive
		return m, nil
	}
	if m.cleanup.cursor >= len(m.cleanup.plan.Classes) {
		return m, nil
	}
	class := m.cleanup.plan.Classes[m.cleanup.cursor]

	m.cleanup.chosen[class.Class] = !m.cleanup.chosen[class.Class]
	if !m.cleanup.chosen[class.Class] {
		// Deselecting anything invalidates an archive: it would no longer be
		// removing everything the plan names.
		m.cleanup.archive = false
	}
	return m, nil
}

// applyCleanupPlan records a resolved inventory.
func (m Model) applyCleanupPlan(message cleanupPlanMsg) (tea.Model, tea.Cmd) {
	asking, before := m.cleanup.pending, m.cleanup.plan.Token
	substance := cleanupSubstance(m.cleanup.plan)
	m.cleanup.working, m.cleanup.pending = false, false
	m.cleanup.err = message.err
	if message.err != nil {
		return m, nil
	}
	m.cleanup.plan = message.plan
	m.cleanup.loaded = true
	m.cleanup.done = false
	m.cleanup.forgetMissing()
	// A re-resolved plan is a shorter one once something has been removed, and a
	// cursor left past its end is a cursor on nothing.
	if m.cleanup.cursor > m.cleanup.lastRow() {
		m.cleanup.cursor, m.cleanup.scroll = 0, 0
	}
	if !asking {
		return m, nil
	}

	// The question enter asked, put against what came back. The two axes a plan
	// moves along are answered differently, because the token is deliberately
	// blind to one of them: it covers the identity of every target and not the
	// warnings, so that an agent writing a file is not reported as a stale plan
	// (ADR-037).
	key := m.cleanup.key
	switch {
	// Emptied before the token is consulted, because a class can only leave the
	// plan by changing it: both are true at once, and "read it and press enter
	// again" is poor advice for a screen with nothing left to press it for.
	case !m.cleanup.removable():
		m.status = "nothing you selected is still there; task " + key + " owns none of it now"
	case message.plan.Token != before:
		// A resource gained or lost is a different plan from the one that was
		// read, and confirming here would be confirming a list nobody has seen.
		// The inventory below is already the new one; the question waits for
		// another enter.
		m.status = "what task " + key + " owns has changed since you looked; " +
			"the inventory is what it is now, so read it and press enter again"
	default:
		// The same resources, whatever they now cost. A cost that moved is said
		// here and listed in the confirmation itself, which is where it is read.
		if cleanupSubstance(message.plan) != substance {
			m.status = "what removing this would cost has changed since you looked; the question below says how"
		}
		m.cleanup.executing = true
	}
	return m, nil
}

// forgetMissing drops choices for classes a re-resolved plan no longer names.
//
// A tick is a choice about a resource, and a resource that has gone takes its
// choice with it. Left behind it would be a selection the screen cannot draw and
// the daemon would refuse, reported as neither.
func (c *cleanupModel) forgetMissing() {
	named := make(map[string]bool, len(c.plan.Classes))
	for _, class := range c.plan.Classes {
		named[class.Class] = true
	}
	for class := range c.chosen {
		if !named[class] {
			delete(c.chosen, class)
		}
	}
	if !c.archivable() {
		c.archive = false
	}
}

// cleanupSubstance is everything the inventory says about a plan, as a value one
// plan can be compared with another by.
//
// The token will not serve for this. It covers what a plan would remove so that
// executing a stale one is refused, and it excludes what removing it would cost
// precisely so that a dirty worktree does not invalidate a plan. Both are right,
// and neither answers "has anything on this screen changed" — which is the only
// question a user pressing `r` is asking.
func cleanupSubstance(plan api.CleanupPlan) string {
	var out strings.Builder
	for _, class := range plan.Classes {
		out.WriteString(class.Class + "\x00")
		for _, target := range class.Targets {
			out.WriteString(target.Identity + "\x00")
			if !target.Present {
				out.WriteString("gone\x00")
			}
			for _, warning := range target.Warnings {
				out.WriteString(warning + "\x00")
			}
		}
	}
	for _, problem := range plan.Problems {
		out.WriteString(problem + "\x00")
	}
	return out.String()
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

// cleanupSubject says what is being cleaned up, and when Feat last looked.
//
// The project and the workflow, because a task still working on something is a
// different decision from an approved one — which is the reason the plan carries
// its workflow at all, and which `feat task cleanup` has said in its first line
// since the command existed. The border names the task; this names its situation.
//
// The moment the inventory was taken belongs here for the reason the recovery
// overlay says when it last checked: everything below is an observation of that
// moment and not a live view, and `r` is how a user takes another one. It is also
// what makes that key visibly do something on a task nothing has touched.
func (m Model) cleanupSubject(width int) string {
	if !m.cleanup.loaded {
		return ""
	}
	subject := "project " + m.cleanup.plan.ProjectID
	if workflow := m.cleanup.plan.Workflow; workflow != "" {
		subject += "  ·  " + workflow
	}
	switch at := m.cleanup.plan.ResolvedAt; {
	case m.cleanup.working:
		subject += "  ·  resolving…"
	case !at.IsZero():
		subject += "  ·  resolved " + at.Local().Format("15:04:05")
	}
	return mutedStyle.Render(truncate(subject, width)) + "\n\n"
}

// cleanupTail is what is drawn under the inventory: the archive choice, whatever
// the plan refused to resolve, what the last cleanup removed, and the question
// that is outstanding.
func (m Model) cleanupTail(width int) string {
	var out strings.Builder

	out.WriteString(m.cleanupArchive(width))
	for _, problem := range m.cleanup.plan.Problems {
		out.WriteString("\n" + failureStyle.Render(truncate(plainLine("! "+problem), width)) + "\n")
	}
	if m.cleanup.done {
		out.WriteString("\n" + m.cleanupResult())
	}

	out.WriteString(m.cleanupPrompt(width))
	return out.String()
}

// cleanupArchive renders the archive choice, a row of the screen like any other.
//
// Drawn wherever the plan could ever archive rather than only once it may. It is
// a line under the inventory, and the inventory is sized by what the tail takes,
// so a row that came and went as classes were ticked moved the list it was being
// ticked in. It is also a cursor stop, and a cursor stop that disappears is one
// the cursor falls off.
//
// Unavailable it says what it waits for rather than vanishing, so that pressing
// space on it has an answer written where the press happened.
func (m Model) cleanupArchive(width int) string {
	if m.cleanup.archiveRow() < 0 {
		return ""
	}

	pointer := "  "
	if m.cleanup.cursor == m.cleanup.archiveRow() {
		pointer = "> "
	}

	// Both forms are about fifty cells, so neither is truncated in a dialog on a
	// terminal at the layout's minimum width, and neither moves the other's
	// checkbox when the selection changes which one is drawn.
	const label = "%s[%s] archive the task's metadata"
	if !m.cleanup.archivable() {
		return "\n" + mutedStyle.Render(truncate(
			fmt.Sprintf(label, pointer, " ")+" — select every class", width)) + "\n"
	}

	marker := " "
	if m.cleanup.archive {
		marker = "x"
	}
	return "\n" + truncate(fmt.Sprintf(label, pointer, marker)+"; its record is kept", width) + "\n"
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
	// What is out of sight is said whatever is happening; how to reach it only
	// while the keys that reach it mean anything. The confirmation takes the
	// keyboard, and a note offering a key that is inert is the same defect as a
	// key map offering one.
	note := strings.Join(parts, ", ")
	if !m.cleanup.executing {
		note += "  ·  pgup/pgdn to scroll"
	}

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
//
// A cursor on the archive row matches no class and so spans nothing at the foot
// of the list, which puts the window at the end of the inventory: the row itself
// is pinned below and needs no scrolling to reach, and what belongs above it is
// the last of what would be removed.
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
	// A class that would cost something says so on its title as well as beside the
	// resources it would cost it on. The title is what stays visible when the
	// window is scrolled to the foot of a long class, and a class marked only by
	// lines the user has scrolled past is one that looks free.
	if len(class.Warnings) > 0 {
		head += failureStyle.Render("  (would lose work)")
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

// cleanupPrompt renders the confirmation, which is the one question the screen
// asks.
//
// It is where FR-CLEAN-003's explicit confirmation lives: the removal is named,
// what it would cost is listed under it, and nothing has been sent yet. Asking
// here rather than at each tick means a user reads the warnings of everything
// they chose in one place, against the removal they are actually authorising
// (ADR-061).
//
// The question is the first line and the warnings follow it, because a region
// too small for both must keep the line that says what answering does. The
// warnings are drawn twice over — beside their targets and here — and the
// question is drawn nowhere else.
func (m Model) cleanupPrompt(width int) string {
	if !m.cleanup.executing {
		return ""
	}

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

	// Truncated before the question rather than through it: seven class titles is
	// wider than the dialog, and a question cut off at "[y/" is one the keys under
	// it no longer belong to.
	const question = "? [y/N]"
	out := "\n" + failureStyle.Render(truncate(line, width-len(question))+question) + "\n"
	for _, warning := range m.cleanup.pendingWarnings() {
		out += failureStyle.Render(truncate("  ! "+warning, width)) + "\n"
	}
	return out
}

// cleanupHints renders the key map.
//
// It is one line inside three quarters of the terminal, and the line it used to
// be was ninety cells of it: on a terminal at the layout's own minimum width the
// last two hints were truncated away, which is two keys nobody could find. The
// page keys are not here — they are named in the note that appears when there is
// something to scroll to, which is the only time they mean anything.
//
// Enter says what it acts on. It takes the whole selection and not the row the
// cursor is on, and "remove" beside a cursor resting on one class read as an
// offer to remove that one. Sixty-three cells against the sixty-eight a dialog
// has at the layout's minimum width, so saying it costs nothing.
func (m Model) cleanupHints() string {
	// While the confirmation is up it answers that, because every key below is
	// inert until it has been: a key map advertising "enter remove" beside a
	// question that enter does not answer is one that has to be tried to be
	// disbelieved.
	if m.cleanup.executing {
		return keyHints(keyHint("y", "remove"), keyHint("n", "cancel"))
	}
	return keyHints(
		keyHint("space", "select"),
		keyHint("enter", "cleanup selected"),
		keyHint("esc", "back"),
	)
}
