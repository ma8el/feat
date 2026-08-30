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
	case !ok && m.selected != "":
		return mutedStyle.Render("the selected task is no longer listed")
	case !ok:
		return mutedStyle.Render("no task selected")
	case task.Session == nil:
		return mutedStyle.Render("task " + task.Key + " has no terminal yet")
	// A frame, or a failure, belonging to another task is not drawn under this
	// one's name. applyFrame drops what arrives after the selection moved; this
	// is the other half — what was already held when it moved, which `esc` from
	// the task panel put back on the screen without asking for a new one.
	case m.terminal.task != task.ID:
		return m.awaitingFrame(task, width, height)
	}

	switch {
	case api.IsTerminalMissing(m.terminal.err):
		return missingTerminal(task)
	case api.IsShellMissing(m.terminal.err):
		return missingShell()
	case m.terminal.err != nil:
		return failureStyle.Render(m.terminal.err.Error())
	case !m.terminal.loaded:
		return m.awaitingFrame(task, width, height)
	}

	// A dead pane wins the note row. A pane whose program has exited is not one
	// whose agent is about to paint, so the startup sentence there would be a
	// lie, and the two notes cannot share a row that only one of them fits in.
	//
	// It keeps the foot of the region whatever else is true. It is an explanation
	// of a terminal that is over, read beside whatever that terminal last showed,
	// rather than the subject of a region nobody is waiting at.
	if dead := deadPanes(m.terminal.frame); dead > 0 {
		return withNote(m.composeWindow(width, height-1),
			failureStyle.Render(count(dead, "pane's program has", "panes' programs have")+
				" exited; the terminal is retained so it can be read"), height)
	}

	note := m.startupLine(task, width)
	if note == "" {
		return m.composeWindow(width, height)
	}

	// The window is composed and measured before the note is placed, and it is
	// only a window that drew nothing at all that gives up its middle. Nothing is
	// read out of the capture to decide this (ADR-042): the question asked is
	// whether the region has anything in it, which is the same question fitRows
	// already asks of what tmux reported, and it is asked of Feat's own rendering
	// rather than of the bytes inside it.
	window := m.composeWindow(width, height-1)
	if blockWidth(window) == 0 {
		return middleRow(note, height)
	}
	return withNote(window, note, height)
}

// middleRow puts a line in the middle of a region that has nothing else in it.
//
// For the length of a startup the note is the only thing in the region, and the
// middle of an empty box is where a reader looks for what the box is doing. It
// sits a row above the exact centre on an even height, which is where
// centreOverlay puts a dialog and for the same reason.
//
// It is only ever reached when the composed window drew nothing, so it covers
// nothing. That condition is what makes vertical centring safe at all: a
// workspace-trust prompt is drawn while the task is still `preparing`, and a
// line across the middle of the region would take a row of the question the
// launch is waiting on. The moment anything is drawn, the note goes back to a
// row of its own (see withNote).
func middleRow(line string, height int) string {
	if height < 1 {
		height = 1
	}
	rows := make([]string, height)
	rows[(height-1)/2] = line
	return strings.Join(rows, "\n")
}

// withNote draws a body under the region's one note, which is then the region's
// last row.
//
// The note takes a row of the region rather than a row after it: the region is a
// card with a rule under it, and a line written past the last row is a line
// nobody sees — which is what a note explaining a terminal that has stopped, or
// one that has not started yet, must not be.
//
// The row is fixed here rather than left to whatever the body filled, and that
// is the defect this closes. Before the first capture there is no body, so the
// note was the region's only line and was drawn at the top of it; the moment a
// pane arrived underneath, the same note moved to the foot. A user watching a
// launch saw the indicator once in the top corner and then, for the rest of the
// wait, in the bottom one. A note about waiting that moves while you wait for it
// is the same defect as an indicator that does not move at all: both are read as
// the dashboard doing something other than what it says.
func withNote(body, note string, height int) string {
	if height < 1 {
		height = 1
	}
	rows := make([]string, height-1)
	if body != "" {
		copy(rows, strings.Split(body, "\n"))
	}
	return strings.Join(append(rows, note), "\n")
}

// startingNote is the sentence for a task whose terminal exists and whose agent
// has not painted in it yet.
//
// It is the gap between the preparation overlay closing and the agent's first
// frame: `LaunchDraft` runs `ensureTerminal` synchronously, so the tmux window
// and the pane are already there when the overlay goes, and what follows is the
// provider starting up inside them. Measured over the daemon's own event log,
// that stretch has a median of 1.51s and a maximum of 12.79s, and it is longest
// under host execution — where nothing had to be started, so the window appears
// almost at once and the whole wait lands here.
//
// It is decided from the task's own state and never from whether the capture
// looks empty (ADR-042). That is not only the rule: a workspace-trust prompt is
// drawn while the task is still `preparing`, so a capture with something in it
// is exactly the case where the user has to read what is there.
//
// It has one home because two places say it. The task panel has said this
// sentence since terminalNote was written, and the terminal tab — the tab a
// launch lands on — said nothing at all.
func startingNote(task api.Task) string {
	if task.Session == nil || task.Workflow != "preparing" {
		return ""
	}
	return "the terminal is running; the agent has not reported starting yet"
}

// startupLine is that sentence as the terminal region draws it, or the honest
// line once the daemon has stopped believing the agent is merely slow.
//
// A task can sit in `preparing` indefinitely, and the daemon's `armStartup`
// exists because of the commonest reason: Claude asks for workspace trust on a
// directory it has not seen before, and every task worktree is one. After the
// startup grace the daemon raises attention and leaves the workflow where it is,
// so attention is what bounds this note — not a clock kept here. There is no
// elapsed figure for the same reason: the rail carries one already.
//
// It is centred across the region. For the length of a startup this line is the
// only thing in the region, and a lone sentence against the left edge of an
// otherwise empty box reads as something left behind rather than as the box's
// subject. It stays centred once a pane has drawn something and the note has
// moved to a row of its own, so that what changes then is the row and not also
// the column.
//
// The centring is stable while the indicator runs: MiniDot is one cell and mark
// always spends two, so the sentence does not shift as the spinner advances.
func (m Model) startupLine(task api.Task, width int) string {
	note := startingNote(task)
	if note == "" {
		return ""
	}
	// An unset attention is treated as none, as every other reading of it in this
	// package does: a fixture or an older record that carries no value has not
	// said that anything needs the user.
	if task.Attention != "" && task.Attention != "none" {
		return centreLine(attentionStyle.Render(
			"nothing has been heard from the agent, and the pane may be asking something"), width)
	}
	// Marked with the dashboard's one indicator, which Model.waiting turns on for
	// this screen. A sentence about a wait that does not move is the thing
	// activity was written to stop.
	return centreLine(mutedStyle.Render(m.activity.mark(note)), width)
}

// awaitingFrame is what the region says before it has a frame for this task.
//
// A user who has just launched is not waiting on tmux — the pane is there and
// the agent has not painted in it — so telling them Feat is asking tmux a
// question is a second wrong answer to the one question they have.
//
// The startup line is placed as it is placed over a window that drew nothing,
// because that is what the next capture will be: this branch and the frame after
// it are a quarter of a second apart, and a note that changed place between them
// is a note that flickers.
func (m Model) awaitingFrame(task api.Task, width, height int) string {
	if line := m.startupLine(task, width); line != "" {
		return middleRow(line, height)
	}
	return mutedStyle.Render("asking tmux what this pane shows…")
}

// missingTerminal explains a task whose tmux window is not there, and what can
// be done about it.
//
// It is the recovery entry's shape — what is wrong, then what to do — because
// that is what this is: the same finding a reconciliation pass reports, arriving
// through the view where a user actually meets it. Printing the resolver's
// sentence instead left the remedy reachable only from the task panel or the
// recovery overlay, which are places you look after you already suspect what
// happened.
//
// The two cases are told apart rather than merged. A task whose agent recorded a
// provider session can have that session continued; one whose agent never
// reported starting cannot, and offering it a key that would refuse would be
// worse than saying so.
//
// The lines are wrapped where they are written rather than left to the region,
// which truncates: the main region is fifty-five cells at the narrowest terminal
// the three-region layout supports, and a remedy cut off halfway is one nobody
// can act on. It is the same hand-wrapping executionDetail does.
func missingTerminal(task api.Task) string {
	var out strings.Builder
	out.WriteString(failureStyle.Render("  missing  terminal") + "\n")
	out.WriteString(mutedStyle.Render("      the tmux window of this task is gone, and") + "\n")
	out.WriteString(mutedStyle.Render("      nothing was started in its place") + "\n")

	if task.Session.ProviderSessionID != "" {
		out.WriteString(mutedStyle.Render("      → z resumes it here, continuing the recorded") + "\n")
		out.WriteString(mutedStyle.Render("        "+task.Session.Provider+
			" session rather than opening an empty one") + "\n")
		return out.String()
	}
	out.WriteString(mutedStyle.Render("      Feat recorded no "+task.Session.Provider+
		" session to continue, so") + "\n")
	out.WriteString(mutedStyle.Render("      a new terminal would start an empty session") + "\n")
	out.WriteString(mutedStyle.Render("      → C cleans up what this task owns") + "\n")
	return out.String()
}

// missingShell explains the shell view of a task that has no shell pane.
//
// It is drawn in the ordinary styles rather than as a failure, because nothing
// has gone wrong: a shell is opened on demand (FR-TMUX-003), so most tasks have
// none for most of their lives and switching to this view is how a user finds
// out. The daemon's sentence — "open one first" — named neither the key that
// opens one nor what pressing it does.
//
// What it does is worth saying, because it is not only a pane: opening a shell
// hands this terminal to native tmux until the user detaches, which is a
// different thing from the rest of the dashboard's keys.
func missingShell() string {
	var out strings.Builder
	out.WriteString(headingStyle.Render("  no shell pane yet") + "\n")
	out.WriteString(mutedStyle.Render("      a task is given one on demand, beside its agent") + "\n")
	out.WriteString(mutedStyle.Render("      and in the same environment and worktree") + "\n")
	out.WriteString(mutedStyle.Render("      → s opens one and hands this terminal to it;") + "\n")
	out.WriteString(mutedStyle.Render("        detach and it is drawn here") + "\n")
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

	// written is the row after the last one any pane put something on. It is
	// arithmetic on what tmux reported rather than anything read out of the
	// content: a capture ends at the last row the program wrote, and the rows
	// after it are the blank ones this canvas was made of.
	//
	// The cursor counts as written. A capture stops at the last row with
	// something on it and the cursor may already be on the blank one after it,
	// which is where it sits the moment a program clears its prompt — and a
	// cursor clipped off the bottom is the one thing a user typing must not lose.
	written := 0
	for _, pane := range frame.Panes {
		block := strings.Join(pane.Content, "\n")
		if pane.Width > 0 {
			block = clampBlock(block, pane.Width)
		}
		canvas = overlayOn(canvas, block, pane.Left, pane.Top)
		written = max(written, pane.Top+len(pane.Content))
		if m.terminal.focused && pane.Active && pane.CursorY >= 0 {
			written = max(written, pane.Top+pane.CursorY+1)
		}

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
	// before it. The clip is vertical as well as horizontal, because the window
	// is whatever tmux reported and the region is what there is to draw it in: a
	// frame that arrived taller than the region would otherwise push whatever
	// follows it out of the card.
	return terminate(clampBlock(fitRows(canvas, written, height), width))
}

// fitRows takes the rows of a window a region has room for, ending at the last
// one the panes wrote on.
//
// The end matters as much as the count. A window is taller than the region
// whenever Feat is not the one sizing it — a native client owns the window it is
// attached to, and a window holding a pane whose program has ended is never made
// smaller (tmux.RenderPane) — and the rows to drop are then the ones above. It is
// the opposite of what a dialog does with a body that does not fit: a dialog is
// read from the top and its first line is its title, while a terminal's newest
// output is at its foot and the prompt a user would type into is the last row of
// all.
//
// A pane that has not filled its window is still drawn from its first row, which
// is why this ends at what was written rather than at the window's own height. A
// window sized for forty rows of an agent that has printed ten holds thirty blank
// ones underneath, and a rendering anchored on those would show the blanks.
func fitRows(block string, written, height int) string {
	if height < 1 {
		height = 1
	}
	lines := strings.Split(block, "\n")
	if len(lines) <= height {
		return block
	}
	end := min(max(written, height), len(lines))
	return strings.Join(lines[end-height:end], "\n")
}

// paneDivider is the line tmux draws between two panes.
func paneDivider(height int) string {
	if height <= 0 {
		height = 1
	}
	return strings.TrimSuffix(strings.Repeat(ruleStyle.Render(cardVertical)+"\n", height), "\n")
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
