package ui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// activity is the dashboard's loading indicator: one spinner, shown wherever a
// screen is waiting for a request it has already told the user about. Preparing
// a task and cleaning one up spend seconds inside the daemon, and a line of
// muted text that never moves cannot be told apart from a dashboard that has
// stopped. An advancing frame says that Feat is still waiting and nothing more,
// because none of these requests reports its progress and a bar drawn against
// nothing would invent one. There is one indicator, animated from what the
// screens say they are waiting for (see Model.waiting), so no screen can start a
// spinner it forgets to stop and leave the dashboard redrawing twelve times a
// second.
type activity struct {
	// frames is the spinner Bubbles animates. It is left unstyled, so a caller
	// can colour and measure the whole line the indicator sits in rather than a
	// colour that starts in the middle of one.
	frames spinner.Model
	// running reports that the frames are being advanced. It is also how a run
	// ends: a tea.Tick cannot be recalled, so stopping drops the tick already in
	// flight instead, and a dropped tick asks for no successor.
	running bool
}

// newActivity builds the indicator. The braille dot is one cell wide, which is
// what the layout needs: a glyph the terminal draws wider than Feat measured is
// a line drawn through the border beside it (see plainText for the same rule
// applied to text Feat did not write).
func newActivity() activity {
	return activity{frames: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
}

// start begins animating, returning the command that delivers the first frame.
// Starting one that is already running returns no command: the caller applies a
// rule after every message, and two chains of ticks would advance one spinner
// at twice the rate it is meant to be read at.
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

// advance moves the spinner on one frame and asks for the next. A tick arriving
// after the wait ended is dropped, which is what stops the chain: nothing
// schedules a successor for it.
func (a activity) advance(message spinner.TickMsg) (activity, tea.Cmd) {
	if !a.running {
		return a, nil
	}
	updated, cmd := a.frames.Update(message)
	a.frames = updated
	return a, cmd
}

// mark puts the current frame in front of a line, and returns the line
// unchanged when nothing is being waited for. It returns plain text, because a
// mark carrying its own colour would be a second colour inside a line the
// palette already decided (ADR-053).
func (a activity) mark(text string) string {
	if !a.running {
		return text
	}
	return a.frames.View() + " " + text
}
