package ui

import "strings"

// The three-region layout needs room for a rail and a main region that are both
// worth reading. Below either measure the dashboard draws the single column it
// drew before, because a rail and a main region inside eighty columns starve
// each other and a stacked screen at least fits (ADR-041).
const (
	minimumWidth  = 96
	minimumHeight = 18
)

// footerHeight is how many lines the footer occupies: the rule that separates it
// from the regions above, a status line, the worktree and resource line, and the
// key hints.
const footerHeight = 4

// regionGap is the blank column between the two cards.
//
// One cell rather than none, because two boxes sharing an edge read as one box
// with a line down it — which is what the layout had before the cards, and what
// a user reads as one region rather than two (ADR-051).
const regionGap = 1

// tab is which view of the selected task the main region shows.
type tab int

const (
	// tabTerminal is first because it is what the main region is for: the
	// dashboard's own views tell a user about a task, and this one shows them
	// the task (ADR-042).
	tabTerminal tab = iota
	// tabTask is what detail and review became. They were conceptually
	// different and shared most of their content, and neither filled the region
	// on its own (ADR-042, evidence 1).
	tabTask
	tabRuntime
)

// tabs is the order the tab bar draws them in and the order the tab key cycles.
//
// Every tab is about the selected task. The overview was the one that was not —
// a wide cross-task table ADR-041 kept provisionally — and use decided against
// it: the rail answers which task to go to next, and the panel answers
// everything the table's remaining columns did, for the task you went to.
var tabs = []tab{tabTerminal, tabTask, tabRuntime}

func (t tab) title() string {
	switch t {
	case tabTask:
		return "task"
	case tabRuntime:
		return "runtime"
	default:
		return "terminal"
	}
}

// narrow reports whether this terminal is too small for the three regions.
//
// A zero size is not narrow: it is a terminal that has not reported yet, and
// falling back before the first tea.WindowSizeMsg would make the layout depend
// on which message arrived first.
func (m Model) narrow() bool {
	if m.width == 0 || m.height == 0 {
		return false
	}
	return m.width < minimumWidth || m.height < minimumHeight
}

// frame composes the rail, the main region, and the footer.
//
// The three keep their positions whatever is happening, which is the point of
// the layout: the row a user's eye learned does not move on the day
// reconciliation finds something (ADR-041). Each of the two regions is a card —
// a rounded box with its own header and a rule under it — and the footer is
// ruled off from both (ADR-051).
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
// pane must be sized to before it is captured.
//
// It subtracts what the frame spends around it: the rail, the gap, and both
// cards' borders and gutters horizontally; the footer, the card's own rules, and
// the tab bar with the rule under it vertically. A caller asking tmux for a
// frame needs this exact number, because the pane wraps its own output at
// whatever width it is told.
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
	case tabRuntime:
		return m.runtimeBody()
	default:
		return m.terminalBody(width, height)
	}
}

// mainHeader is the card header of the main region: the tabs, and what task they
// are all views of.
func (m Model) mainHeader(width int) string {
	return cardHeader(m.tabBar(width), m.headerSubject(width), width)
}

// headerSubject names the task every tab is about.
//
// The rail says which task is selected by moving a marker, which answers the
// question while the eye is in the rail. The main region is where the eye
// actually is, and a view of one task among several that does not say which one
// is a view a user has to look away from to trust.
func (m Model) headerSubject(width int) string {
	task, ok := m.subject()
	if !ok {
		return ""
	}
	subject := task.Key
	if task.Title != "" {
		subject += " · " + task.Title
	}
	// Half the header at most: the tabs are the part a user acts on, and a long
	// title must not be what decides whether they are all visible.
	return mutedStyle.Render(truncate(subject, width/2))
}

// tabBar renders the tabs, marking the one with the main region.
//
// The active tab carries the accent as a background rather than as a colour. The
// bar is the main region's header, and a header whose selected item differs from
// the others only in shade is one a user compares rather than sees (ADR-051).
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
// note about the machine sample, and the keys for what has the keyboard.
//
// The worktree path is here because it is the value a user would otherwise look
// up and paste. The machine's figures were here too until they moved to the foot
// of the rail, where a bar can show a proportion; what is left is the sentence
// that says why one of those figures is absent, which needs the width.
func (m Model) frameFooter(width int) string {
	var out strings.Builder

	// The footer is ruled off from the regions above it. It is the part of the
	// frame that holds still while they change, and a line is what says so
	// (ADR-051).
	out.WriteString(ruleStyle.Render(strings.Repeat(cardHorizontal, width)) + "\n")

	switch {
	case m.err != nil:
		out.WriteString(truncate(failureStyle.Render(m.err.Error()), width))
	case m.status != "":
		out.WriteString(truncate(mutedStyle.Render(m.status), width))
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

// hints are the keys for whatever has the keyboard.
//
// The footer shows what is reachable from here rather than every key the
// dashboard has, which is what the key overlay on "?" is for.
func (m Model) hints() string {
	if m.screen == screenRecovery {
		return keyHints(keyHint("r", "look again"), keyHint("esc", "close"))
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
			keyHint("z", "resume"),
		)
	case screenTask:
		return m.railHints() + mutedStyle.Render("   ") + taskPanelHints()
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
// its own keyboard.
//
// They lead every view's hints, because they are the ones with no other route to
// discovery: a view's own keys are on the view, and the frame's are not. The
// shifted arrows and the control pair do the same thing and are not named here —
// the footer is one line and truncates, and the full list is on `?`.
//
// Folding is named only where there is more than one project to fold, which is
// where the control is worth anything: on a single-project rail the fold is
// still there and the marker on the header still says so, and the footer's cells
// are better spent on the keys of whatever view is open.
func (m Model) railHints() string {
	hints := []string{keyHint("J K", "task"), keyHint("H L", "view")}
	if len(groupByProject(m.tasks)) > 1 {
		hints = append(hints, keyHint("space", "fold"))
	}
	return keyHints(append(hints, keyHint("?", "keys"))...)
}
