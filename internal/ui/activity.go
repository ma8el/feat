package ui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// activity is the dashboard's loading indicator: one spinner, shown wherever a
// screen is waiting for a request it has already told the user about.
//
// Preparing a task and cleaning one up both spend seconds inside the daemon —
// resolving a base in every repository, creating the worktrees and the task
// terminal, walking what a task owns and then removing it — and the dashboard
// said so in a line of muted text that never moved. A sentence that does not
// move cannot be told apart from a dashboard that has stopped, and these are
// precisely the waits where nothing else moves either: the dialog is up, the
// task list behind it is unchanged until the work lands, and the agent's pane is
// not the region being drawn. Users read that as frozen and press keys at it.
//
// An advancing frame is the smallest honest thing to draw here. It says that
// Feat is still waiting and nothing else — no proportion, no estimate — because
// none of these requests reports its progress, and a bar drawn against nothing
// would be inventing one: the rule `absent` states for a value Feat has not
// measured applies to a measure of how far along something is as much as to a
// diff stat.
//
// There is one indicator rather than one per screen, and what it animates is
// decided in a single place from what the screens say they are waiting for (see
// Model.waiting). A screen therefore cannot start a spinner it forgets to stop,
// which would leave the dashboard redrawing twelve times a second for the rest
// of the session with nothing outstanding.
type activity struct {
	// frames is the spinner Bubbles animates for us.
	//
	// It is left unstyled, so that a caller can colour and measure the whole
	// line the indicator sits in rather than reason about a colour that starts
	// in the middle of one. Every line it appears on is cut to a region.
	frames spinner.Model
	// running reports that the frames are being advanced.
	//
	// It is also how a run ends: a tea.Tick cannot be recalled, so stopping
	// drops the tick already in flight instead, and a dropped tick asks for no
	// successor.
	running bool
}

// newActivity builds the indicator.
//
// The braille dot is one cell wide, which is what the layout needs of it: the
// lines it joins are padded, truncated, and counted against a region, and a
// glyph the terminal draws wider than Feat measured is a line drawn through the
// border beside it (see plainText for the same rule applied to text Feat did not
// write).
func newActivity() activity {
	return activity{frames: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
}

// start begins animating, returning the command that delivers the first frame.
//
// Starting one that is already running is nothing and returns no command. Two
// chains of ticks would otherwise advance the same spinner at twice the rate it
// was designed to be read at, and the caller here is a rule applied after every
// message rather than a decision taken once.
func (a *activity) start() tea.Cmd {
	if a.running {
		return nil
	}
	a.running = true
	return a.frames.Tick
}

// stop ends the animation. The frame it stopped on is never drawn, because
// every caller draws the indicator only while it is waiting.
func (a *activity) stop() { a.running = false }

// advance moves the spinner on one frame and asks for the next.
//
// A tick that arrives after the wait ended is dropped rather than drawn, which
// is what stops the chain: nothing schedules a successor for it.
func (a activity) advance(message spinner.TickMsg) (activity, tea.Cmd) {
	if !a.running {
		return a, nil
	}
	updated, cmd := a.frames.Update(message)
	a.frames = updated
	return a, cmd
}

// mark puts the current frame in front of a line, and returns the line
// unchanged when nothing is being waited for.
//
// It returns plain text. The caller styles and measures what it builds, and a
// mark that carried its own colour would be a second colour inside a line the
// palette already decided (ADR-053).
func (a activity) mark(text string) string {
	if !a.running {
		return text
	}
	return a.frames.View() + " " + text
}
