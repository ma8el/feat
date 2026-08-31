package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// briefModel is the brief tab's own state.
type briefModel struct {
	// task is whose brief the offset belongs to.
	//
	// The offset survives leaving the tab and coming back, which is what a tab
	// is, and is discarded when the selection moves: half way down one task's
	// brief is nowhere in particular in another's.
	task string
	// scroll is how far down the brief the reader is. It is the brief's own and
	// not the task panel's, because two tabs sharing one offset would each move
	// the other's position every time they were scrolled.
	scroll int
}

// openBrief shows the selected task's brief.
//
// Nothing is asked of the daemon: the brief travels on the task the rail is
// already drawing. The tab opens whatever it finds, including nothing at all,
// because a tab that declines to open is a tab the cycle cannot pass (ADR-041).
func (m Model) openBrief() (tea.Model, tea.Cmd) {
	m.screen = screenBrief
	if task, ok := m.subject(); ok && m.brief.task != task.ID {
		m.brief = briefModel{task: task.ID}
	}
	return m, nil
}

// briefView renders the brief as a whole terminal, which is what the narrow
// fallback draws when there is no room for the three regions.
func (m Model) briefView() string {
	width, height := m.frameSize()
	body := m.briefBody(width, height-stackedFooterHeight)

	if _, ok := m.task(m.selected); !ok {
		return body + m.footer(keyHints(keyHint("A", "task list"), keyHint("q", "quit")))
	}
	return body + m.footer(briefHints())
}

// briefBody renders the brief into a region, scrolled to where the user is.
func (m Model) briefBody(width, height int) string {
	return scrollWindow(m.wrappedBrief(width), m.brief.scroll, width, height)
}

// wrappedBrief is the brief re-flowed to the width it will be drawn at.
//
// Made measurable before it is measured, as the task panel is, and for a
// stronger reason: nearly the whole of this body is text Feat did not write. A
// brief carries tabs and carriage returns, which are worth nothing to the wrap
// and everything to the terminal (ADR-054), and prose cut at the region's edge
// loses the half of the sentence that says what to do about it. Wrapping before
// the split is also what keeps the scroll honest: the lines counted are the
// lines drawn.
func (m Model) wrappedBrief(width int) string {
	body := plainText(m.briefPanel(width))
	if width <= 0 {
		return body
	}
	return ansi.Wrap(body, width, "")
}

// briefPanel is one task's brief under a heading that says whose it is and where
// it came from.
//
// A task with no brief and a task that is no longer listed each say so rather
// than drawing an empty region: this tab is reached by cycling, so it is opened
// by users who were on their way somewhere else, and a region that goes blank
// reads as a dashboard that broke.
func (m Model) briefPanel(width int) string {
	task, ok := m.task(m.selected)
	if !ok {
		if m.selected == "" {
			return headingStyle.Render("brief") + "\n\n" + mutedStyle.Render("no task selected")
		}
		return headingStyle.Render("brief") + "\n\n" +
			mutedStyle.Render("this task is no longer listed")
	}

	// Where the brief came from goes beside the heading rather than into the
	// body. It is a fact about the document and not part of it, and the header's
	// rule is the one this needs: an aside that does not fit is dropped rather
	// than truncated, because half of a path says nothing the document does not.
	header := cardHeader(headingStyle.Render(task.Key+"  brief"),
		mutedStyle.Render(plainLine(sourceDetail(task.Source))), width)

	if strings.TrimSpace(task.Brief) == "" {
		return header + "\n\n" + mutedStyle.Render("this task has no brief")
	}
	return header + "\n\n" + task.Brief
}

// briefScroll is where the page keys leave the brief, bounded by its length.
//
// The bound is applied here rather than while rendering, for the reason the
// panel's is: rendering cannot write back, so without it holding pgdn past the
// end would build up an offset that took as many presses to undo.
func (m Model) briefScroll(delta int) int {
	width, height := m.mainRegionSize()
	total := len(strings.Split(m.wrappedBrief(width), "\n"))
	return clampScroll(m.brief.scroll+delta, total, height)
}

// briefKey answers the brief tab's own keys.
func (m Model) briefKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c", "q":
		return m.quit()

	case "pgup":
		m.brief.scroll = m.briefScroll(-panelPage)
		return m, nil

	case "pgdown":
		m.brief.scroll = m.briefScroll(panelPage)
		return m, nil
	}
	// Everything this tab does not claim is the dashboard's, which is the rule
	// the task panel follows: `?` and `!` open their overlays from here, `n`
	// prepares a task, and `a` attaches to the one being read about.
	return m.dashboardKey(key)
}

// briefHints are the brief tab's own keys, which are the one it has.
func briefHints() string {
	return keyHints(keyHint("pgup/pgdn", "scroll"))
}
