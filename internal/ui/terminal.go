package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// frameInterval is how often the terminal tab asks for a new frame.
//
// It is a poll, which ADR-042 records as the interim: tmux can push a
// notification whenever a pane paints, and a control-mode connection would turn
// this into an event. The endpoint is the same shape either way. Until then this
// is fast enough to read an agent working and slow enough not to ask the daemon
// five times a second.
const frameInterval = 250 * time.Millisecond

// focusedInterval is the same while the pane has the keyboard.
//
// A user typing sees their own characters appear, so the delay is theirs rather
// than an agent's. A quarter of a second of it reads as a broken terminal, which
// is what a poll rate chosen for watching an agent work does to typing.
const focusedInterval = 60 * time.Millisecond

// terminalModel is the state of the pane the main region draws.
type terminalModel struct {
	// task is whose pane this frame belongs to, so that a frame arriving after
	// the user moved on is discarded rather than drawn under the wrong name.
	task string
	// frame is the last rendering tmux returned.
	frame api.TerminalFrame
	// shell reports that the shell pane is being shown rather than the agent's.
	shell bool
	// focused reports that the keyboard belongs to the pane.
	focused bool
	loaded  bool
	// err is why the last frame or keystroke failed, shown rather than thrown:
	// a terminal that cannot be read is one line of explanation, not a dashboard
	// that stops drawing.
	err error
	// polling reports that a tick is already scheduled, so that entering the tab
	// twice does not start two of them.
	polling bool
	// inFlight reports that a frame has been asked for and not yet arrived.
	//
	// A second request while one is outstanding is dropped rather than queued.
	// Two of them racing is what made the agent flicker between the region's
	// width and half of it, and a frame nobody waited for is work the daemon and
	// tmux do for a rendering that is already stale.
	inFlight bool
}

// terminalFrameMsg carries one rendered pane.
type terminalFrameMsg struct {
	task  string
	shell bool
	frame api.TerminalFrame
	err   error
}

// terminalTickMsg asks for the next frame.
type terminalTickMsg struct{}

// terminalInputMsg carries the result of delivering a keystroke.
type terminalInputMsg struct{ err error }

// terminalTick schedules the next frame request.
func terminalTick(focused bool) tea.Cmd {
	interval := frameInterval
	if focused {
		interval = focusedInterval
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return terminalTickMsg{} })
}

// requestFrame asks the daemon for the selected task's pane at this size.
//
// The size is the region it will be drawn into, and the daemon sets the pane to
// it before capturing: a program wraps its own output, so a pane at another size
// would arrive wrapped at a column this region does not have.
func (m *Model) requestFrame(width, height int) tea.Cmd {
	task, ok := m.subject()
	if !ok || task.Session == nil {
		return nil
	}
	if width <= 0 || height <= 0 || m.terminal.inFlight {
		return nil
	}
	m.terminal.inFlight = true

	backend, id, shell := m.backend, task.ID, m.terminal.shell
	view := api.TerminalView{Width: width, Height: height, Shell: shell}
	return func() tea.Msg {
		frame, err := backend.TerminalFrame(context.Background(), id, view)
		return terminalFrameMsg{task: id, shell: shell, frame: frame, err: err}
	}
}

// applyFrame records a rendering, or why there is none.
func (m Model) applyFrame(message terminalFrameMsg) (tea.Model, tea.Cmd) {
	// The request is over whatever it carried, so the flag is cleared before any
	// of the reasons to discard it: leaving it set would stop every later frame.
	m.terminal.inFlight = false

	// A frame for a task the user has moved away from is dropped rather than
	// drawn: it would otherwise appear under the newly selected task's name.
	if task, ok := m.subject(); !ok || task.ID != message.task {
		return m, nil
	}
	if message.shell != m.terminal.shell {
		return m, nil
	}

	m.terminal.task = message.task
	m.terminal.err = message.err
	if message.err == nil {
		m.terminal.frame = message.frame
		m.terminal.loaded = true
	}
	return m, nil
}

// terminalBody renders the pane into the main region.
//
// Every line arrives already rendered by tmux, escape sequences and all. This
// clips by cell so that a line cannot run past the region, and reads nothing
// else out of it: no task, agent, attention, or workflow state is derived from a
// terminal's contents (ADR-042).
func (m Model) terminalBody(width, height int) string {
	task, ok := m.subject()
	switch {
	case !ok:
		return mutedStyle.Render("no task selected")
	case task.Session == nil:
		return mutedStyle.Render("task " + task.Key + " has no terminal yet")
	}

	var out strings.Builder

	switch {
	case m.terminal.err != nil:
		out.WriteString(failureStyle.Render(m.terminal.err.Error()))
		return out.String()
	case !m.terminal.loaded:
		out.WriteString(mutedStyle.Render("asking tmux what this pane shows…"))
		return out.String()
	}

	out.WriteString(m.composeWindow(width, height))

	if dead := deadPanes(m.terminal.frame); dead > 0 {
		out.WriteString("\n" + failureStyle.Render(
			count(dead, "pane's program has", "panes' programs have")+
				" exited; the terminal is retained so it can be read"))
	}
	return out.String()
}

// composeWindow tiles the window's panes into the region, each at the place tmux
// put it.
//
// tmux draws the panes and reports where each one sits; this puts them back
// together in the same arrangement, so a task showing an agent beside a shell
// looks in the dashboard the way it looks when attached. Nothing here reads the
// contents: the splice is by cell, and every escape sequence passes through
// (ADR-042).
func (m Model) composeWindow(width, height int) string {
	frame := m.terminal.frame
	if len(frame.Panes) == 0 {
		return mutedStyle.Render("this window has no panes")
	}

	// A canvas the size of the window, so a pane's Left and Top mean what tmux
	// meant by them. It is clipped to the region afterwards rather than before,
	// because clipping first would move every pane after the first.
	rows := frame.Height
	if rows <= 0 {
		rows = height
	}
	canvas := strings.TrimSuffix(strings.Repeat("\n", rows), "\n")

	for _, pane := range frame.Panes {
		block := strings.Join(pane.Content, "\n")
		if pane.Width > 0 {
			block = clampBlock(block, pane.Width)
		}
		canvas = overlayOn(canvas, block, pane.Left, pane.Top)

		// The divider tmux draws between panes is part of its own rendering and
		// not part of any pane's capture, so it is drawn here.
		if pane.Left > 0 {
			canvas = overlayOn(canvas, paneDivider(pane.Height), pane.Left-1, pane.Top)
		}
	}

	if m.terminal.focused {
		canvas = m.withCursor(canvas)
	}
	// Clip first, then end each line: a truncation can cut a line in the middle
	// of a styled run, and the reset has to come after the cut rather than
	// before it.
	return terminate(clampBlock(canvas, width))
}

// paneDivider is the line tmux draws between two panes.
func paneDivider(height int) string {
	if height <= 0 {
		height = 1
	}
	return strings.TrimSuffix(strings.Repeat(dividerStyle.Render("│")+"\n", height), "\n")
}

// deadPanes counts the panes whose program has exited.
func deadPanes(frame api.TerminalFrame) int {
	dead := 0
	for _, pane := range frame.Panes {
		if pane.Dead {
			dead++
		}
	}
	return dead
}

// withCursor draws a block where tmux says the cursor is.
//
// The capture does not carry it, and a focused pane with no visible cursor is
// one a user cannot tell is theirs. A block covers the character beneath it,
// which is what a block cursor does in any terminal.
func (m Model) withCursor(body string) string {
	for _, pane := range m.terminal.frame.Panes {
		if !pane.Active || pane.CursorX < 0 || pane.CursorY < 0 {
			continue
		}
		// The cursor's position is inside its own pane, so it is offset by where
		// that pane sits in the window.
		return overlayOn(body, "\x1b[7m \x1b[m", pane.Left+pane.CursorX, pane.Top+pane.CursorY)
	}
	return body
}

// focusTerminal gives the pane the keyboard.
func (m Model) focusTerminal() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok || task.Session == nil {
		m.status = "there is no terminal to type into yet"
		return m, nil
	}

	m.terminal.focused = true
	m.status = ""

	// A frame at once, so focusing does not wait out the slow tick already
	// scheduled. No second tick is started: the one that is pending will
	// reschedule itself at the focused cadence, and starting another here left
	// two chains running for every time a user focused and unfocused.
	width, height := m.mainRegionSize()
	frame := m.requestFrame(width, height)
	return m, frame
}

// terminalInput routes a key press that belongs to the focused pane.
//
// One key is deliberately not forwarded. Something has to take the keyboard
// back, and a user whose every key reaches the agent has no way to say so;
// ctrl+q is the one agent-manager reserves for the same reason.
func (m Model) terminalInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "ctrl+q" {
		// Nothing is said about it. Which side has the keyboard is on the rail,
		// where the task it applies to is, rather than in a line that has to be
		// read and then goes away.
		m.terminal.focused = false
		return m, nil
	}

	input, ok := translateKey(key)
	if !ok {
		return m, nil
	}

	task, found := m.subject()
	if !found {
		return m, nil
	}
	input.Shell = m.terminal.shell

	backend, id := m.backend, task.ID
	return m, func() tea.Msg {
		return terminalInputMsg{err: backend.SendTerminalInput(context.Background(), id, input)}
	}
}

// tmuxKeys maps this terminal's key names onto tmux's.
//
// Only names tmux recognises are sent, and anything not listed is dropped rather
// than guessed: a key forwarded under the wrong name is a key the agent acts on
// and the user did not press.
var tmuxKeys = map[string]string{
	"enter": "Enter", "esc": "Escape", "escape": "Escape", "tab": "Tab",
	"shift+tab": "BTab", "backspace": "BSpace", "delete": "DC", "insert": "IC",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "pgup": "PPage", "pgdown": "NPage",
	"f1": "F1", "f2": "F2", "f3": "F3", "f4": "F4", "f5": "F5", "f6": "F6",
	"f7": "F7", "f8": "F8", "f9": "F9", "f10": "F10", "f11": "F11", "f12": "F12",
}

// translateKey turns one key press into the input the daemon accepts.
//
// Typed characters travel as text and everything else as a key name, which is
// how tmux itself distinguishes them: text goes through a bracketed paste so
// that the application reading it cannot take a trailing newline as a
// submission, and a name goes through send-keys.
func translateKey(key tea.KeyMsg) (api.TerminalInput, bool) {
	// A space arrives as its own key type rather than as runes, and its name is
	// the character itself, so neither the runes branch nor the table below
	// caught it and every space a user typed was dropped.
	if key.Type == tea.KeySpace {
		return api.TerminalInput{Text: " "}, true
	}
	if key.Type == tea.KeyRunes {
		text := string(key.Runes)
		if key.Alt || text == "" {
			return api.TerminalInput{}, false
		}
		return api.TerminalInput{Text: text}, true
	}

	name := key.String()
	if mapped, ok := tmuxKeys[name]; ok {
		return api.TerminalInput{Keys: []string{mapped}}, true
	}
	// ctrl+a becomes C-a, which is the only shape this translates by rule
	// rather than by table: there are twenty-six of them and they are regular.
	if after, found := strings.CutPrefix(name, "ctrl+"); found && len(after) == 1 {
		return api.TerminalInput{Keys: []string{"C-" + after}}, true
	}
	return api.TerminalInput{}, false
}
