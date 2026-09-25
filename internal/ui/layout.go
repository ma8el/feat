package ui

import "strings"

// The three-region layout needs room for a rail and a main region that are both
// worth reading. Below either measure the dashboard draws a single column,
// because a rail and a main region inside eighty columns starve each other and
// a stacked screen at least fits (ADR-041).
const (
	minimumWidth  = 96
	minimumHeight = 18
)

// footerHeight is how many lines the footer occupies: the rule that separates
// it from the regions above, a status line, the worktree and resource line, and
// the key hints.
const footerHeight = 4

// regionGap is the blank column between the two cards. One cell rather than
// none, because two boxes sharing an edge read as one box with a line down it
// (ADR-051).
const regionGap = 1

// tab is which view of the selected task the main region shows.
type tab int

const (
	// tabTerminal is first because it is what the main region is for: the
	// dashboard's own views tell a user about a task, and this one shows them
	// the task (ADR-042).
	tabTerminal tab = iota
	// tabTask holds what detail and review both showed: they shared most of their
	// content and neither filled the region on its own (ADR-042, evidence 1).
	tabTask
	// tabBrief is the document the task was launched from. It is unbounded — a
	// task's fields are a screen and a brief is however long somebody wrote — and
	// on a tab of its own the panel beside it stops scrolling, so a field is
	// where it was last time (ADR-086, ADR-041 evidence 4).
	tabBrief
	tabRuntime
)

// tabs is the order the tab bar draws them in and the order the tab key cycles.
// Every tab is about the selected task. The wide cross-task table ADR-041 kept
// provisionally is not here: the rail answers which task to go to next, and the
// panel answers what the table's remaining columns did.
var tabs = []tab{tabTerminal, tabTask, tabBrief, tabRuntime}

func (t tab) title() string {
	switch t {
	case tabTask:
		return "task"
	case tabBrief:
		return "brief"
	case tabRuntime:
		return "runtime"
	default:
		return "terminal"
	}
}

// narrow reports whether this terminal is too small for the three regions. A
// zero size is not narrow but a terminal that has not reported yet, and falling
// back before the first tea.WindowSizeMsg would make the layout depend on
// message order.
func (m Model) narrow() bool {
	if m.width == 0 || m.height == 0 {
		return false
	}
	return m.width < minimumWidth || m.height < minimumHeight
}

// frame composes the rail, the main region, and the footer. The three keep
// their positions whatever is happening, so the row a user's eye learned does
// not move on the day reconciliation finds something (ADR-041). Each region is
// a card, and the footer is ruled off from both (ADR-051).
func (m Model) frame() string {
	width, _ := m.frameSize()

	bodyHeight := m.bodyHeight()
	mainWidth, mainHeight := m.mainRegionSize()

	rail := card(m.railHeader(railWidth), m.railView(bodyHeight-cardVerticalChrome-cardHeaderHeight),
		railWidth+cardChrome, bodyHeight, false)
	main := card(m.mainHeader(mainWidth), m.mainBody(mainWidth, mainHeight),
		mainWidth+cardChrome, bodyHeight, m.mainHoldsKeyboard())

	return joinRows(rail, main, regionGap) + "\n" + m.frameFooter(width)
}

// mainHoldsKeyboard reports whether keystrokes are going to the main region's
// own content rather than to the dashboard.
func (m Model) mainHoldsKeyboard() bool {
	return m.screen == screenTerminal && m.terminal.focused
}

// frameSize is the terminal, or a usable default before it has reported one.
func (m Model) frameSize() (width, height int) {
	width, height = m.width, m.height
	if width <= 0 {
		width = minimumWidth
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

// bodyHeight is how many lines the two cards occupy: everything above the
// footer, and never less than a card with one line of content in it.
func (m Model) bodyHeight() int {
	_, height := m.frameSize()

	body := height - footerHeight
	if smallest := cardVerticalChrome + cardHeaderHeight + 1; body < smallest {
		return smallest
	}
	return body
}

// mainRegionSize is the space the main region's content has, which is what a
// pane must be sized to before it is captured. A caller asking tmux for a frame
// needs this exact number, because the pane wraps its own output at whatever
// width it is told.
func (m Model) mainRegionSize() (width, height int) {
	frameWidth, _ := m.frameSize()

	width = frameWidth - (railWidth + cardChrome) - regionGap - cardChrome
	height = m.bodyHeight() - cardVerticalChrome - cardHeaderHeight
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}

// mainBody renders whichever tab has the main region.
func (m Model) mainBody(width, height int) string {
	switch m.activeTab() {
	case tabTask:
		return m.taskBody(width, height)
	case tabBrief:
		return m.briefBody(width, height)
	case tabRuntime:
		return m.runtimeBody(width, height)
	default:
		return m.terminalBody(width, height)
	}
}

// mainHeader is the card header of the main region: the tabs, and what task
// they are all views of.
func (m Model) mainHeader(width int) string {
	return cardHeader(m.tabBar(width), m.headerSubject(width), width)
}

// headerSubject names the task every tab is about. The rail answers that with a
// marker, which works while the eye is in the rail; the main region is where
// the eye is, and a view of one task among several has to say which one.
func (m Model) headerSubject(width int) string {
	task, ok := m.subject()
	if !ok {
		return ""
	}
	subject := task.Key
	if title := plainLine(task.Title); title != "" {
		subject += " · " + title
	}
	// Half the header at most: the tabs are the part a user acts on, and a long
	// title must not be what decides whether they are all visible.
	return mutedStyle.Render(truncate(subject, width/2))
}

// tabBar renders the tabs, marking the one with the main region. The active tab
// carries the accent as a background rather than a colour, because a header
// whose selected item differs only in shade has to be compared rather than seen
// (ADR-051).
func (m Model) tabBar(width int) string {
	rendered := make([]string, 0, len(tabs))
	for _, candidate := range tabs {
		if candidate == m.activeTab() {
			rendered = append(rendered, activeTabStyle.Render(candidate.title()))
			continue
		}
		rendered = append(rendered, tabStyle.Render(candidate.title()))
	}
	return truncate(strings.Join(rendered, " "), width)
}

// frameFooter renders the status line, the selected task's worktree beside any
// note about the machine sample, and the keys for what has the keyboard. The
// worktree path is here because it is the value a user would otherwise look up
// and paste. The machine's figures are at the foot of the rail, where a bar can
// show a proportion; the sentence saying why one is absent needs this width.
func (m Model) frameFooter(width int) string {
	var out strings.Builder

	// The footer is ruled off from the regions above it. It is the part of the
	// frame that holds still while they change, and a line is what says so
	// (ADR-051).
	out.WriteString(ruleStyle.Render(strings.Repeat(cardHorizontal, width)) + "\n")

	// Flattened before it is cut. The cut is by display width and a line break is
	// worth none of it, so a wrapped error carrying a command's output passed
	// through whole and pushed the footer past the rows the regions were sized
	// against (ADR-054).
	switch {
	// A pending question outranks both, because it is the only line here the
	// user has to answer before any other key means anything.
	case m.stopping != "":
		out.WriteString(truncate(attentionStyle.Render(
			"Stop the agent of this task? It is working, and stopping it ends the turn.  y to confirm"), width))
	// Named, unlike the one above it, because a refresh between the key press and
	// the answer can move the rail's marker (G2-04), and because what this
	// destroys is the only copy of something somebody wrote.
	case m.cancelling != "":
		out.WriteString(truncate(attentionStyle.Render(
			"Cancel draft "+m.taskKey(m.cancelling)+"? Its brief is not kept anywhere else.  y to confirm"), width))
	case m.err != nil:
		out.WriteString(truncate(failureStyle.Render(plainLine(m.err.Error())), width))
	case m.status != "":
		out.WriteString(truncate(mutedStyle.Render(plainLine(m.status)), width))
	}
	out.WriteString("\n")

	worktree := mutedStyle.Render("no task selected")
	if task, ok := m.subject(); ok {
		worktree = worktreeNote(task)
	}
	if note := m.machineNote(); note != "" {
		worktree += mutedStyle.Render("   ") + note
	}
	out.WriteString(truncate(worktree, width))
	out.WriteString("\n")
	out.WriteString(truncate(m.hints(), width))
	return out.String()
}

// hints are the keys for whatever has the keyboard. The footer shows what is
// reachable from here rather than every key the dashboard has, which is what
// the overlay on "?" is for.
func (m Model) hints() string {
	// The daemon dialog is a question, so the footer carries the answers rather
	// than the key that would dismiss it.
	if m.screen == screenDaemon {
		return m.daemonHints()
	}
	if m.screen == screenRecovery {
		return keyHints(keyHint("r", "refresh"), keyHint("esc", "close"))
	}
	// A screen that has shut its keyboard while it waits for a request it cannot
	// take back names the one key it still answers, rather than the "esc close"
	// true of every other moment in the same dialog. These are waits of several
	// seconds, which is when somebody reads a footer looking for a way out.
	switch {
	case m.screen == screenPrepare && m.prepare.busy:
		return keyHints(keyHint("ctrl+c", "cancel"))
	case m.screen == screenCleanup && m.cleanup.removing:
		return keyHints(keyHint("ctrl+c", "quit"))
	case m.screen == screenPublication && m.publication.publishing:
		return keyHints(keyHint("ctrl+c", "quit"))
	}
	if !m.screen.mainRegion() {
		return keyHints(keyHint("esc", "close"))
	}
	// A view with its own keyboard gets the frame's keys first, then its own.
	// The footer truncates at the terminal's width, and what must survive that
	// is what has no other route to discovery: the view's keys are on the view,
	// and moving between tasks and views is not.
	if m.screen == screenTerminal && m.terminal.focused {
		// While the pane has the keyboard the dashboard's keys do not fire, so
		// the only one worth naming is the one that takes them back.
		return keyHints(keyHint("ctrl+q", "take the keyboard back"))
	}

	switch m.screen {
	case screenTerminal:
		return m.railHints() + mutedStyle.Render("   ") + keyHints(
			keyHint("i", "type here"),
			keyHint("w", "agent/shell"),
			keyHint("a", "attach"),
			// The pair, on the view that draws the pane they move. A resume named
			// without its inverse teaches that the only way to stop a task's agent
			// is to clean the task up.
			keyHint("z", "resume"),
			keyHint("t", "stop"),
		)
	case screenTask:
		return m.railHints() + mutedStyle.Render("   ") + taskPanelHints()
	case screenBrief:
		return m.railHints() + mutedStyle.Render("   ") + briefHints()
	case screenRuntime:
		return m.railHints() + mutedStyle.Render("   ") + runtimeHints()
	}
	return m.railHints() + mutedStyle.Render("   ") + keyHints(
		keyHint("a", "attach"),
		keyHint("s", "shell"),
		keyHint("n", "new"),
		keyHint("q", "quit"),
	)
}

// railHints are the keys that reach the rail and the tab bar from a view with
// its own keyboard. They lead every view's hints, because a view's own keys are
// on the view and the frame's are not. The shifted arrows and the control pair
// do the same thing and are not named here: the footer is one line, and the
// full list is on `?`.
//
// Folding is named only where there is more than one project to fold. It is
// named for what the key would do where the cursor is, because space is one
// control in two directions and the marker beside the cursor is the only other
// sign of which.
func (m Model) railHints() string {
	hints := []string{keyHint("J K", "task"), keyHint("H L", "view")}
	if len(groupByProject(m.tasks)) > 1 {
		fold := "fold"
		if task, ok := m.subject(); ok && m.folded[task.ProjectID] {
			fold = "open"
		}
		hints = append(hints, keyHint("space", fold))
	}
	return keyHints(append(hints, keyHint("?", "keys"))...)
}
