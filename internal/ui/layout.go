package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The three-region layout needs room for a rail and a main region that are both
// worth reading. Below either measure the dashboard draws the single column it
// drew before, because a rail and a main region inside eighty columns starve
// each other and a stacked screen at least fits (ADR-041).
const (
	minimumWidth  = 96
	minimumHeight = 18
)

// footerHeight is how many lines the footer occupies: a status line, the
// worktree and resource line, and the key hints.
const footerHeight = 3

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
// reconciliation finds something (ADR-041).
func (m Model) frame() string {
	width, height := m.frameSize()

	bodyHeight := height - footerHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	mainWidth := width - railWidth - 1
	if mainWidth < 1 {
		mainWidth = 1
	}

	rail := regionStyle(railWidth, bodyHeight).Render(m.railView(bodyHeight))
	divider := dividerStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyHeight), "\n"))
	main := regionStyle(mainWidth, bodyHeight).Render(m.mainView(mainWidth, bodyHeight))

	body := lipgloss.JoinHorizontal(lipgloss.Top, rail, divider, main)
	return body + "\n" + m.frameFooter(width)
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

// mainRegionSize is the space the main region's content has, which is what a
// pane must be sized to before it is captured.
//
// It subtracts what the frame spends around it: the rail and the divider
// horizontally, the footer and the tab bar with its blank line vertically. A
// caller asking tmux for a frame needs this exact number, because the pane wraps
// its own output at whatever width it is told.
func (m Model) mainRegionSize() (width, height int) {
	frameWidth, frameHeight := m.frameSize()

	width = frameWidth - railWidth - 1
	height = frameHeight - footerHeight - 2
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}

// regionStyle fixes a region's size so that its neighbours do not move when its
// content grows.
func regionStyle(width, height int) lipgloss.Style {
	return lipgloss.NewStyle().Width(width).Height(height).MaxWidth(width).MaxHeight(height)
}

var dividerStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#d0d0d0", Dark: "#3a3a3a"})

// mainView renders the tab bar and whichever tab has the main region.
func (m Model) mainView(width, height int) string {
	body := ""
	switch m.activeTab() {
	case tabTask:
		body = m.taskBody(width, height-2)
	case tabRuntime:
		body = m.runtimeBody()
	default:
		// The tab bar and the blank line beneath it are the region's, so the
		// pane gets what is left of it.
		body = m.terminalBody(width, height-2)
	}
	return m.tabBar(width) + "\n\n" + body
}

// tabBar renders the tabs, marking the one with the main region.
func (m Model) tabBar(width int) string {
	rendered := make([]string, 0, len(tabs))
	for _, candidate := range tabs {
		title := candidate.title()
		if candidate == m.activeTab() {
			rendered = append(rendered, selectedStyle.Render(title))
			continue
		}
		rendered = append(rendered, mutedStyle.Render(title))
	}
	bar := strings.Join(rendered, mutedStyle.Render("  ·  "))
	return truncate(bar, width)
}

// frameFooter renders the status line, the selected task's worktree beside the
// machine's resources, and the keys for what has the keyboard.
//
// The worktree path is here because it is the value a user would otherwise look
// up and paste, and the machine card is here because a fixed position is what
// evidence 4 of ADR-041 was about: it used to sit above the task list and push
// it down the screen.
func (m Model) frameFooter(width int) string {
	var out strings.Builder

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
	out.WriteString(truncate(worktree+mutedStyle.Render("   ")+m.machineLine(), width))
	out.WriteString("\n")
	out.WriteString(truncate(m.hints(), width))
	return out.String()
}

// machineLine is the machine card on one line, for the footer.
func (m Model) machineLine() string {
	if m.resourceErr != nil {
		return mutedStyle.Render(absent + " (" + m.resourceErr.Error() + ")")
	}
	if !m.resources.Sampled {
		return mutedStyle.Render(absent + " (no sample yet)")
	}
	machine := m.resources.Machine
	line := strings.Join([]string{loadField(machine), memoryField(machine), diskField(machine)},
		mutedStyle.Render("   "))

	// Why a figure is missing, beside the figures. A sample that could read the
	// disk and not the memory says which, rather than leaving one field absent
	// with no reason given: an unmeasured value is never shown as a measured one
	// and never shown without saying so (FR-UI-005).
	if note := strings.Join(m.resources.Notes, "; "); note != "" {
		line += mutedStyle.Render("   " + note)
	}
	return line
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
		return railHints() + mutedStyle.Render("   ") + keyHints(
			keyHint("i", "type here"),
			keyHint("w", "agent/shell"),
			keyHint("a", "attach"),
		)
	case screenTask:
		return railHints() + mutedStyle.Render("   ") + taskPanelHints()
	case screenRuntime:
		return railHints() + mutedStyle.Render("   ") + runtimeHints()
	}
	return keyHints(
		keyHint("↑↓", "select"),
		keyHint("tab", "view"),
		keyHint("a", "attach"),
		keyHint("s", "shell"),
		keyHint("n", "new"),
		keyHint("?", "keys"),
		keyHint("q", "quit"),
	)
}

// railHints are the keys that reach the rail from a view with its own keyboard.
//
// They are appended to the task panel's and runtime's own hints, because those
// views take the plain arrows for their own cursor and a user there needs to be
// told how to change task without leaving.
func railHints() string {
	return keyHints(keyHint("shift+↑↓", "task"), keyHint("tab", "view"), keyHint("?", "keys"))
}
