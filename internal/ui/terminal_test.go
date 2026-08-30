package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// typedKeys are the key types a terminal delivers for keys that are not
// characters.
//
// press builds every key as runes, which is enough for the dashboard's own
// single-letter commands and is exactly wrong here: the whole question is
// whether a real Enter becomes the name Enter rather than the text "enter".
var typedKeys = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"ctrl+c": tea.KeyCtrlC, "ctrl+q": tea.KeyCtrlQ, "f5": tea.KeyF5,
}

// pressTyped delivers one key as a terminal would, rather than as runes.
func pressTyped(t *testing.T, model Model, name string) Model {
	t.Helper()

	kind, ok := typedKeys[name]
	if !ok {
		t.Fatalf("no key type for %q", name)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: kind})
	next := updated.(Model)
	if cmd != nil {
		if message := cmd(); message != nil {
			applied, _ := next.Update(message)
			return applied.(Model)
		}
	}
	return next
}

// terminalDashboard is a dashboard whose first task list has arrived, which is
// what starts the poll.
func terminalDashboard(t *testing.T, backend *fakeBackend, tasks ...api.Task) Model {
	t.Helper()

	model := sized(dashboard(backend, tasks...), 120, 32)
	if model.activeTab() != tabTerminal {
		t.Fatalf("the dashboard opened on %v, want the terminal", model.activeTab())
	}
	return model
}

// TestTheMainRegionDrawsWhatTmuxRendered is ADR-042 at the renderer: the pane
// arrives already drawn, and the dashboard places it.
func TestTheMainRegionDrawsWhatTmuxRendered(t *testing.T) {
	backend := newFakeBackend()
	backend.frame = api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{{Pane: "%11", Width: 80, Height: 24, Active: true, Content: []string{"\x1b[32mall tests passed\x1b[m", "> "}}}}

	model := terminalDashboard(t, backend, liveTask())
	updated, _ := model.Update(terminalFrameMsg{
		task:  liveTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{{Pane: "%11", Width: 80, Height: 24, Active: true, Content: []string{"\x1b[32mall tests passed\x1b[m", "> "}}}},
	})

	view := updated.(Model).View()
	if !strings.Contains(ansi.Strip(view), "all tests passed") {
		t.Errorf("the pane's contents are not on screen:\n%s", view)
	}
}

// TestAFrameIsAskedForAtTheSizeItWillBeDrawn is why the daemon resizes: a pane
// at another size arrives wrapped at a column this region does not have.
func TestAFrameIsAskedForAtTheSizeItWillBeDrawn(t *testing.T) {
	backend := newFakeBackend()
	model := terminalDashboard(t, backend, liveTask())

	width, height := model.mainRegionSize()
	// The dashboard already asked for a frame when its tasks arrived, and this
	// harness drops the command rather than running it, so nothing has replied
	// and the request is still recorded as outstanding. Clearing it is what a
	// reply would have done.
	model.terminal.inFlight = false

	// Run the request itself rather than the batch it travels in: the batch also
	// carries the poll's timer, and executing that would sleep.
	if cmd := model.requestFrame(width, height); cmd != nil {
		cmd()
	}

	views := backend.terminalViews()
	if len(views) == 0 {
		t.Fatal("no frame was asked for")
	}
	if views[0].Width != width || views[0].Height != height {
		t.Errorf("asked for %dx%d, want the region's %dx%d",
			views[0].Width, views[0].Height, width, height)
	}
	if width >= 120 {
		t.Errorf("the requested width %d does not account for the rail", width)
	}
}

// TestAFrameForAnotherTaskIsDropped keeps one task's terminal from being drawn
// under another's name, which a frame in flight when the selection moves would
// otherwise do.
func TestAFrameForAnotherTaskIsDropped(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask(), otherTask())

	updated, _ := model.Update(terminalFrameMsg{
		task:  otherTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{{Pane: "%11", Width: 80, Height: 24, Active: true, Content: []string{"someone else's pane"}}}},
	})

	if strings.Contains(updated.(Model).View(), "someone else's pane") {
		t.Errorf("a frame for an unselected task was drawn:\n%s", updated.(Model).View())
	}
}

// TestTypingReachesTheAgentOnlyWhenFocused is the whole point of an explicit
// focus: the same key is a dashboard command or a character depending on it.
func TestTypingReachesTheAgentOnlyWhenFocused(t *testing.T) {
	backend := newFakeBackend()
	model := terminalDashboard(t, backend, liveTask())

	// Unfocused, "n" opens task preparation rather than typing an n.
	if opened := press(t, model, "n"); opened.screen != screenPrepare {
		t.Errorf("unfocused, n did not open preparation: screen is %v", opened.screen)
	}
	if sent := backend.terminalInputs(); len(sent) != 0 {
		t.Fatalf("an unfocused key press reached the agent: %+v", sent)
	}

	focused := press(t, model, "i")
	if !focused.terminal.focused {
		t.Fatal("i did not give the pane the keyboard")
	}
	typed := press(t, focused, "n")
	if typed.screen == screenPrepare {
		t.Error("a focused key press still opened preparation")
	}

	sent := backend.terminalInputs()
	if len(sent) != 1 || sent[0].Text != "n" {
		t.Errorf("the agent received %+v, want the typed character", sent)
	}
}

// TestOneKeyAlwaysTakesTheKeyboardBack checks the escape hatch. A user whose
// every key reaches the agent has no other way to say so.
func TestOneKeyAlwaysTakesTheKeyboardBack(t *testing.T) {
	backend := newFakeBackend()
	focused := press(t, terminalDashboard(t, backend, liveTask()), "i")

	back := pressTyped(t, focused, "ctrl+q")
	if back.terminal.focused {
		t.Error("ctrl+q did not take the keyboard back")
	}
	if sent := backend.terminalInputs(); len(sent) != 0 {
		t.Errorf("the key that takes the keyboard back was forwarded: %+v", sent)
	}
}

// TestKeysTranslateToTmuxNames checks the table, because a key forwarded under
// the wrong name is one the agent acts on and the user did not press.
func TestKeysTranslateToTmuxNames(t *testing.T) {
	backend := newFakeBackend()
	focused := press(t, terminalDashboard(t, backend, liveTask()), "i")

	for key, want := range map[string]string{
		"enter":  "Enter",
		"esc":    "Escape",
		"tab":    "Tab",
		"up":     "Up",
		"ctrl+c": "C-c",
		"pgup":   "PPage",
		"f5":     "F5",
	} {
		backend.inputs = nil
		pressTyped(t, focused, key)

		sent := backend.terminalInputs()
		if len(sent) != 1 || len(sent[0].Keys) != 1 || sent[0].Keys[0] != want {
			t.Errorf("%q was sent as %+v, want the key name %q", key, sent, want)
		}
	}
}

// TestEveryTranslatedKeyIsOneTheDaemonAccepts ties the translation to the
// validation, so that a name added here cannot be one the daemon refuses.
func TestEveryTranslatedKeyIsOneTheDaemonAccepts(t *testing.T) {
	for from, name := range tmuxKeys {
		if err := (api.TerminalInput{Keys: []string{name}}).Validate(); err != nil {
			t.Errorf("%q translates to %q, which the daemon refuses: %v", from, name, err)
		}
	}
	for _, letter := range []string{"a", "c", "z"} {
		if err := (api.TerminalInput{Keys: []string{"C-" + letter}}).Validate(); err != nil {
			t.Errorf("C-%s is refused by the daemon: %v", letter, err)
		}
	}
}

// TestAPaneThatCannotBeReadSaysSo keeps a failure to one line rather than a
// dashboard that stops drawing.
func TestAPaneThatCannotBeReadSaysSo(t *testing.T) {
	backend := newFakeBackend()
	backend.frameErr = errors.New("the dedicated tmux server is not running")

	model := terminalDashboard(t, backend, liveTask())
	updated, _ := model.Update(terminalFrameMsg{
		task: liveTask().ID,
		err:  backend.frameErr,
	})

	view := updated.(Model).View()
	if !strings.Contains(view, "not running") {
		t.Errorf("the failure is not on screen:\n%s", view)
	}
	if !strings.Contains(view, "7f3a1c2e") {
		t.Errorf("a failed frame took the task list with it:\n%s", view)
	}
}

// TestAMissingTerminalOffersTheRecoveryForIt is where a user meets this.
//
// Killing the task's window from tmux left the main region printing the
// resolver's sentence, while the key that rebuilds it was named only in the key
// overlay and in a reconciliation finding on another view. A failure that names
// its own remedy is the rule everywhere else in Feat, and this is the view a
// user is looking at when it happens.
func TestAMissingTerminalOffersTheRecoveryForIt(t *testing.T) {
	task := liveTask()
	task.Session.ProviderSessionID = "e3f1a0c2-0000-4000-8000-1234567890ab"

	model := terminalDashboard(t, newFakeBackend(), task)
	updated, _ := model.Update(terminalFrameMsg{
		task: task.ID,
		err: fmt.Errorf("%w: task %s has no live tagged terminal on /run/feat/tmux.sock",
			api.ErrTerminalMissing, task.ID),
	})

	view := ansi.Strip(updated.(Model).View())
	if !strings.Contains(view, "z resumes it here") {
		t.Errorf("the missing terminal does not name the key that rebuilds it:\n%s", view)
	}
	if !strings.Contains(view, "claude session") {
		t.Errorf("the offer does not say what would be continued:\n%s", view)
	}
	// And the key is in the footer of the view it applies to, not only in the
	// overlay a user has to know to open. The footer is asked directly, because
	// the body now says "resumes" and a search of the whole screen would pass
	// whether or not the hint is there.
	if hints := ansi.Strip(updated.(Model).hints()); !strings.Contains(hints, "resume") {
		t.Errorf("the terminal's own keys do not include the resume: %s", hints)
	}

	// The whole of the remedy survives the narrowest terminal the three-region
	// layout supports, where the main region is sixty-three cells. A region
	// truncates what it is given, so a line written too long is a sentence that
	// stops halfway on the machines least able to spare it.
	narrow := sized(dashboard(newFakeBackend(), task), minimumWidth, minimumHeight)
	shown, _ := narrow.Update(terminalFrameMsg{
		task: task.ID,
		err:  fmt.Errorf("%w: task %s has no live tagged terminal", api.ErrTerminalMissing, task.ID),
	})
	if body := ansi.Strip(shown.(Model).View()); !strings.Contains(body, "rather than opening an empty one") {
		t.Errorf("the offer is truncated at the narrowest supported terminal:\n%s", body)
	}
}

// TestATerminalWithNoRecordedSessionIsNotOfferedAResume keeps the offer honest.
//
// Resuming continues a recorded provider session; a task whose agent never
// reported one has nothing to continue, and the daemon refuses. Offering the
// key anyway would send a user to a refusal.
func TestATerminalWithNoRecordedSessionIsNotOfferedAResume(t *testing.T) {
	task := liveTask()
	task.Session.ProviderSessionID = ""

	model := terminalDashboard(t, newFakeBackend(), task)
	updated, _ := model.Update(terminalFrameMsg{
		task: task.ID,
		err:  fmt.Errorf("%w: task %s has no live tagged terminal", api.ErrTerminalMissing, task.ID),
	})

	view := ansi.Strip(updated.(Model).View())
	if strings.Contains(view, "z resumes it here") {
		t.Errorf("a task with nothing to continue was offered a resume:\n%s", view)
	}
	if !strings.Contains(view, "no claude session to continue") {
		t.Errorf("the dead end is not explained:\n%s", view)
	}
}

// TestTheShellViewOfATaskWithNoShellNamesTheKeyThatOpensOne is the same rule
// applied to the other pane.
//
// Switching to the shell view is how a user discovers there is no shell, so
// this is not a failure to report but a state to explain. It also says what the
// key does, because opening a shell hands the terminal to native tmux and the
// rest of the dashboard's keys do not.
func TestTheShellViewOfATaskWithNoShellNamesTheKeyThatOpensOne(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	switched := press(t, model, "w")
	if !switched.terminal.shell {
		t.Fatal("w did not switch to the shell pane")
	}

	updated, _ := switched.Update(terminalFrameMsg{
		task:  liveTask().ID,
		shell: true,
		err: fmt.Errorf("%w: task %s has not been given a shell pane yet",
			api.ErrShellMissing, liveTask().ID),
	})

	view := ansi.Strip(updated.(Model).View())
	if !strings.Contains(view, "no shell pane yet") {
		t.Errorf("the shell view does not explain itself:\n%s", view)
	}
	if !strings.Contains(view, "s opens one") {
		t.Errorf("the shell view does not name the key that opens one:\n%s", view)
	}
	// The resolver's own sentence is not what a user reads.
	if strings.Contains(view, "open one first") {
		t.Errorf("the daemon's phrasing reached the screen:\n%s", view)
	}
}

// TestADeadPaneIsExplainedRatherThanLeftBlank keeps a terminal whose program
// exited from reading as a quiet one.
func TestADeadPaneIsExplainedRatherThanLeftBlank(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	updated, _ := model.Update(terminalFrameMsg{
		task:  liveTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{{Pane: "%11", Width: 80, Height: 24, Active: true, Dead: true, Content: []string{"exit 1"}}}},
	})

	if !strings.Contains(updated.(Model).View(), "has exited") {
		t.Errorf("a dead pane is not explained:\n%s", updated.(Model).View())
	}
}

// TestAPaneTallerThanTheRegionShowsItsFoot is what a region has to do with a
// window it is not the one sizing.
//
// Two of them arrive that way: the window a native client is attached to keeps
// its client's size, and the window of a pane whose program has ended is never
// made smaller, because a resize would reflow a screen nothing will repaint. The
// rows to drop are then the ones above — a terminal's newest output is at its
// foot, and the prompt a user is reading is the last row of all.
func TestAPaneTallerThanTheRegionShowsItsFoot(t *testing.T) {
	content := make([]string, 24)
	for row := range content {
		content[row] = fmt.Sprintf("row %d", row)
	}
	content[23] = "│ > the prompt │"

	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{
		task: liveTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{
			{Pane: "%11", Width: 80, Height: 24, Active: true, Content: content},
		}},
	})

	body := ansi.Strip(loaded.(Model).composeWindow(80, 6))
	if !strings.Contains(body, "│ > the prompt │") {
		t.Errorf("the foot of the pane was clipped away:\n%s", body)
	}
	if strings.Contains(body, "row 0\n") {
		t.Errorf("the region showed the head of a pane it had no room for:\n%s", body)
	}
	if rows := strings.Count(body, "\n") + 1; rows != 6 {
		t.Errorf("the pane was drawn in %d rows, want the 6 the region has", rows)
	}
}

// TestAPaneThatHasNotFilledItsWindowIsDrawnFromTheTop is the other half of the
// same clip.
//
// A window sized for forty rows of an agent that has printed ten holds thirty
// blank ones underneath, and a rendering anchored on the window's foot would show
// the blanks. The clip ends at what the panes wrote rather than at the window's
// own height.
func TestAPaneThatHasNotFilledItsWindowIsDrawnFromTheTop(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{
		task: liveTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{
			{Pane: "%11", Width: 80, Height: 24, Active: true, Content: []string{"the agent started", "> "}},
		}},
	})

	body := ansi.Strip(loaded.(Model).composeWindow(80, 6))
	if !strings.Contains(body, "the agent started") {
		t.Errorf("a pane with room to spare was drawn from its blank foot:\n%s", body)
	}
}

// TestTheCursorSurvivesTheClip keeps the one row a user typing cannot lose.
//
// A capture stops at the last row with something on it, and the cursor may
// already be on the blank row after it — which is where it sits the moment a
// program clears its prompt. Clipping a taller-than-the-region window to what was
// written would then cut off the block that says where the keystrokes are going.
func TestTheCursorSurvivesTheClip(t *testing.T) {
	frame := api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{
		{Pane: "%11", Width: 80, Height: 24, Active: true, CursorX: 0, CursorY: 12,
			Content: []string{"the agent stopped for input"}},
	}}

	backend := newFakeBackend()
	backend.frame = frame
	model := terminalDashboard(t, backend, liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: frame})
	focused := press(t, loaded.(Model), "i")

	body := focused.composeWindow(80, 6)
	if !strings.Contains(body, "\x1b[7m") {
		t.Errorf("the cursor was clipped out of the region:\n%q", ansi.Strip(body))
	}
}

// TestSwitchingPanesDiscardsTheFrameWithIt stops the agent's contents being
// drawn under the shell's name for a tick.
func TestSwitchingPanesDiscardsTheFrameWithIt(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{
		task:  liveTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{{Pane: "%11", Width: 80, Height: 24, Active: true, Content: []string{"the agent's pane"}}}},
	})

	switched := press(t, loaded.(Model), "w")
	if !switched.terminal.shell {
		t.Fatal("w did not switch to the shell pane")
	}
	if strings.Contains(switched.View(), "the agent's pane") {
		t.Errorf("the agent's frame was drawn under the shell's name:\n%s", switched.View())
	}
}

// TestTheTerminalPollsOnlyWhileItIsOnScreen keeps a dashboard on another view
// from asking the daemon for frames nobody is looking at.
func TestTheTerminalPollsOnlyWhileItIsOnScreen(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())

	elsewhere := press(t, model, "tab")
	if elsewhere.activeTab() == tabTerminal {
		t.Fatal("tab did not leave the terminal")
	}

	updated, cmd := elsewhere.Update(terminalTickMsg{})
	if cmd != nil {
		t.Error("a tick off the terminal tab scheduled more work")
	}
	if updated.(Model).terminal.polling {
		t.Error("the poll is still marked as running after leaving the tab")
	}
}

// TestEscapeSequencesSurviveRendering is ADR-042's pass-through, checked at the
// only place it can break.
//
// tmux emits a finished screen with its colour attributes. Everything between
// there and the terminal — clipping, the region, the frame around it — must
// carry those bytes without touching them: Feat reads cell width out of them and
// nothing else, and a layer that re-encoded them would be Feat deciding what
// they mean.
func TestEscapeSequencesSurviveRendering(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	updated, _ := model.Update(terminalFrameMsg{
		task:  liveTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{{Pane: "%11", Width: 80, Height: 24, Active: true, Content: []string{"\x1b[2m› Reading auth/session.go\x1b[m"}}}},
	})

	view := updated.(Model).View()
	if !strings.Contains(view, "\x1b[2m") {
		t.Error("the dim attribute tmux emitted did not survive rendering")
	}
	if !strings.Contains(ansi.Strip(view), "› Reading auth/session.go") {
		t.Errorf("the pane's text did not survive rendering:\n%s", ansi.Strip(view))
	}
}

// TestTheCursorIsDrawnOnlyWhenTheKeyboardIsThere keeps a user able to tell whose
// the keyboard is. The capture does not carry a cursor, so Feat draws one.
func TestTheCursorIsDrawnOnlyWhenTheKeyboardIsThere(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{
		task:  liveTask().ID,
		frame: api.TerminalFrame{Width: 80, Height: 24, Panes: []api.TerminalPane{{Pane: "%11", Width: 80, Height: 24, Active: true, CursorX: 2, CursorY: 0, Content: []string{"> ready"}}}},
	})

	if strings.Contains(loaded.(Model).View(), "\x1b[7m") {
		t.Error("an unfocused pane drew a cursor")
	}
	if !strings.Contains(press(t, loaded.(Model), "i").View(), "\x1b[7m") {
		t.Error("a focused pane drew no cursor")
	}
}

// twoPaneFrame is a window holding an agent and a shell beside it, which is what
// a task looks like once a shell has been opened.
func twoPaneFrame() api.TerminalFrame {
	return api.TerminalFrame{Width: 87, Height: 6, Panes: []api.TerminalPane{
		{Pane: "%1", Left: 0, Top: 0, Width: 43, Height: 6, Active: true,
			CursorX: 2, CursorY: 1, Content: []string{"agent line one", "> "}},
		{Pane: "%2", Left: 44, Top: 0, Width: 43, Height: 6,
			Content: []string{"$ git status", "$ "}},
	}}
}

// TestAWindowIsComposedFromItsPanes is the defect a user reported: the terminal
// filled half its container.
//
// The window is sized to the region, and a window holding an agent and a shell
// splits that between them. Drawing one pane of it leaves the other half blank,
// so every pane is drawn at the place tmux gave it.
func TestAWindowIsComposedFromItsPanes(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: twoPaneFrame()})

	body := ansi.Strip(loaded.(Model).composeWindow(87, 6))
	first, _, _ := strings.Cut(body, "\n")

	if !strings.Contains(first, "agent line one") {
		t.Errorf("the agent's pane is missing:\n%s", body)
	}
	if !strings.Contains(first, "$ git status") {
		t.Errorf("the shell's pane is missing, which is the reported defect:\n%s", body)
	}
	// Both panes on one line means they are side by side rather than stacked.
	if strings.Index(first, "agent line one") > strings.Index(first, "$ git status") {
		t.Errorf("the panes are in the wrong order:\n%s", body)
	}
}

// TestAComposedWindowPlacesEachPaneWhereTmuxPutIt is the same defect measured
// rather than read.
//
// It checks placement rather than total width: a pane whose lines are shorter
// than the pane is leaves blanks at the end, and padding them would change
// nothing a user sees. What matters is that the second pane starts at the column
// tmux gave it, because a composition that ignored Left would stack the panes on
// top of each other at the left edge — which is the half-empty region reported.
func TestAComposedWindowPlacesEachPaneWhereTmuxPutIt(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: twoPaneFrame()})

	first, _, _ := strings.Cut(ansi.Strip(loaded.(Model).composeWindow(87, 6)), "\n")

	at := strings.Index(first, "$ git status")
	if at < 0 {
		t.Fatalf("the shell's pane is missing:\n%s", first)
	}
	if column := ansi.StringWidth(first[:at]); column != 44 {
		t.Errorf("the shell's pane starts at column %d, want the 44 tmux gave it:\n%s", column, first)
	}
}

// TestAComposedWindowKeepsTheDividerBetweenPanes checks the line tmux draws
// itself, which belongs to no pane's capture.
func TestAComposedWindowKeepsTheDividerBetweenPanes(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: twoPaneFrame()})

	first, _, _ := strings.Cut(ansi.Strip(loaded.(Model).composeWindow(87, 6)), "\n")
	if !strings.Contains(first, "│") {
		t.Errorf("no divider between the panes:\n%s", first)
	}
}

// TestTheCursorFollowsTheActivePane checks the offset. A cursor drawn at the
// pane's own coordinates would land in the wrong pane once one is not at the
// window's left edge.
func TestTheCursorFollowsTheActivePane(t *testing.T) {
	frame := twoPaneFrame()
	frame.Panes[0].Active, frame.Panes[1].Active = false, true
	frame.Panes[1].CursorX, frame.Panes[1].CursorY = 2, 1

	// Focusing asks for a frame at once, and this harness runs that command, so
	// the backend must answer with the same frame the test arranged or its reply
	// would replace it.
	backend := newFakeBackend()
	backend.frame = frame

	model := terminalDashboard(t, backend, liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: frame})
	focused := press(t, loaded.(Model), "i")

	lines := strings.Split(focused.composeWindow(87, 6), "\n")
	if len(lines) < 2 {
		t.Fatalf("composed to %d lines", len(lines))
	}
	// The active pane starts at column 44, so its cursor belongs at 46.
	before := ansi.StringWidth(ansi.Cut(lines[1], 0, 46))
	if !strings.Contains(lines[1], "\x1b[7m") || before != 46 {
		t.Errorf("the cursor is not in the active pane:\n%q", lines[1])
	}
}

// TestAPaneStyleDoesNotBleedIntoTheOneBesideIt is the defect a user saw as a
// coloured bar running across the whole dashboard.
//
// tmux clears to end of line as it draws. A capture holds the colour and not the
// clearing, so a pane line that sets a background and never resets it carries
// that background across the divider, through the pane beside it, and on to the
// edge of the screen.
func TestAPaneStyleDoesNotBleedIntoTheOneBesideIt(t *testing.T) {
	frame := twoPaneFrame()
	// A highlighted row that never clears its background, which is what a table
	// row or a selected line in an agent's output looks like.
	frame.Panes[0].Content = []string{"\x1b[48;5;30mhighlighted row", "> "}
	frame.Panes[1].Content = []string{"$ git status", "$ "}

	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: frame})

	first, _, _ := strings.Cut(loaded.(Model).composeWindow(87, 6), "\n")

	background := strings.Index(first, "\x1b[48;5;30m")
	neighbour := strings.Index(first, "$ git status")
	if background < 0 || neighbour < 0 {
		t.Fatalf("the fixture did not compose as expected: %q", first)
	}

	reset := strings.Index(first[background:], ansi.ResetStyle)
	if reset < 0 || background+reset > neighbour {
		t.Errorf("the first pane's background is still open where the second begins:\n%q", first)
	}
}

// TestAComposedLineEndsItsStyling stops a pane's colour running past the window
// into the rail, the footer, and the rest of the terminal.
func TestAComposedLineEndsItsStyling(t *testing.T) {
	frame := twoPaneFrame()
	frame.Panes[1].Content = []string{"\x1b[41mred to the edge", ""}

	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: frame})

	for i, line := range strings.Split(loaded.(Model).composeWindow(87, 6), "\n") {
		if !strings.Contains(line, "\x1b") {
			continue
		}
		if !strings.HasSuffix(line, ansi.ResetStyle) {
			t.Errorf("line %d leaves a style open at its end: %q", i, line)
		}
	}
}

// TestAFailedKeystrokeDoesNotBlankThePane keeps one undelivered key from
// replacing the terminal.
//
// The pane's own error field is what the region draws instead of the pane, so an
// input failure recorded there removes the terminal until the next frame puts it
// back — which reads as the whole view flickering rather than as one key failing.
func TestAFailedKeystrokeDoesNotBlankThePane(t *testing.T) {
	backend := newFakeBackend()
	backend.inputErr = errors.New("terminal/input: EOF")
	backend.frame = twoPaneFrame()

	model := terminalDashboard(t, backend, liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: twoPaneFrame()})
	focused := press(t, loaded.(Model), "i")

	typed := pressTyped(t, focused, "enter")
	view := typed.View()

	if !strings.Contains(ansi.Strip(view), "agent line one") {
		t.Errorf("a failed keystroke blanked the pane:\n%s", ansi.Strip(view))
	}
	if !strings.Contains(ansi.Strip(view), "did not reach the agent") {
		t.Errorf("the failure is not reported anywhere:\n%s", ansi.Strip(view))
	}
}

// TestFocusIsShownOnTheTaskItAppliesTo replaces the heading that used to say it.
//
// The heading carried the task key, which the rail already had, and two hints
// the footer already had. The one thing only it said was which side has the
// keyboard, and that belongs beside the task it applies to.
func TestFocusIsShownOnTheTaskItAppliesTo(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask(), otherTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: twoPaneFrame()})

	unfocused := loaded.(Model).railView(28)
	focused := press(t, loaded.(Model), "i").railView(28)

	if unfocused == focused {
		t.Fatal("focusing the terminal changed nothing in the rail")
	}

	// The background is a block across the whole rail, so the entry's lines are
	// padded to its width. Colour itself cannot be asserted here: lipgloss drops
	// it when nothing is attached to a terminal, and the padding is the part of
	// the styling that survives.
	// The detail line is the one that tells them apart: a long title already
	// fills the first line either way, and the detail is always shorter than the
	// rail unless a background has been painted across it.
	marked := entryLines(t, focused, "7f3a1c2e")[1]
	plain := entryLines(t, unfocused, "7f3a1c2e")[1]

	if width := ansi.StringWidth(marked); width != railWidth {
		t.Errorf("the focused entry is %d cells, want the rail's %d: %q", width, railWidth, marked)
	}
	if ansi.StringWidth(plain) == railWidth {
		t.Errorf("an unfocused entry is painted like a focused one: %q", plain)
	}
}

// entryLines returns the two rail lines of one task.
func entryLines(t *testing.T, rail, key string) []string {
	t.Helper()

	lines := strings.Split(rail, "\n")
	for i, line := range lines {
		if strings.Contains(line, key) {
			if i+1 >= len(lines) {
				t.Fatalf("entry for %s has no second line", key)
			}
			return lines[i : i+2]
		}
	}
	t.Fatalf("no entry for %s in:\n%s", key, rail)
	return nil
}

// TestOnlyTheFocusedTaskIsMarked keeps the mark on one entry: it answers where
// keystrokes are going, and two answers would be no answer.
func TestOnlyTheFocusedTaskIsMarked(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask(), otherTask())
	focused := press(t, model, "i")

	if !focused.holdsKeyboard(liveTask()) {
		t.Error("the selected task is not marked as holding the keyboard")
	}
	if focused.holdsKeyboard(otherTask()) {
		t.Error("an unselected task is marked as holding the keyboard")
	}
	// tab does not leave the terminal while it has the keyboard: it is a key the
	// agent gets, which is the point of focus. Taking the keyboard back first is
	// what moves the tab, and the mark goes with it.
	away := press(t, pressTyped(t, focused, "ctrl+q"), "tab")
	if away.holdsKeyboard(liveTask()) {
		t.Error("a task off the terminal tab is still marked as holding the keyboard")
	}
}

// TestTheTerminalTabSpendsItsRowsOnTheTerminal checks that the heading's two
// rows went back to the pane rather than to blank space.
func TestTheTerminalTabSpendsItsRowsOnTheTerminal(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	loaded, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: twoPaneFrame()})

	body := ansi.Strip(loaded.(Model).terminalBody(87, 6))
	first, _, _ := strings.Cut(body, "\n")

	if !strings.Contains(first, "agent line one") {
		t.Errorf("the pane does not start on the first row of the region:\n%s", body)
	}
	for _, gone := range []string{"i to type here", "a to attach", "agent + 1 pane"} {
		if strings.Contains(body, gone) {
			t.Errorf("the terminal still repeats %q, which the footer already carries", gone)
		}
	}
}

// preparingTask is a launched task whose terminal exists and whose agent has not
// painted in it yet.
//
// It is derived from liveTask rather than replacing it: the two differ in the
// workflow and in nothing else, and the point of every test below is what the
// region does with that one difference. liveTask is `working` and is shared, so
// it is copied rather than moved into this state.
func preparingTask() api.Task {
	task := liveTask()
	task.Workflow = "preparing"
	task.Attention = "none"
	return task
}

// blankPane is the capture that made the region blank: the pane exists, its
// program is running, and it has painted nothing yet.
func blankPane() api.TerminalFrame {
	return api.TerminalFrame{Width: 87, Height: 6, Panes: []api.TerminalPane{
		{Pane: "%1", Width: 87, Height: 6, Active: true, CursorX: 0, CursorY: 0},
	}}
}

// startingSentence is what the region says while the agent has not painted, and
// terminalNote's first branch says on the task panel. One wording, one home.
const startingSentence = "the terminal is running; the agent has not reported starting yet"

// terminalRegion renders the main region for one task and one capture.
func terminalRegion(t *testing.T, task api.Task, frame api.TerminalFrame) string {
	t.Helper()

	model := terminalDashboard(t, newFakeBackend(), task)
	loaded, _ := model.Update(terminalFrameMsg{task: task.ID, frame: frame})
	return ansi.Strip(loaded.(Model).terminalBody(87, 6))
}

// TestABlankPaneSaysTheAgentHasNotStarted is the reported defect: a user who
// launches a task lands on the terminal tab and watches an empty box for a few
// seconds with nothing on screen saying why.
//
// The overlay covers the launch itself and says so. What follows it — the
// provider starting inside a pane that already exists — had no sentence at all,
// and the daemon's own event log puts a median of 1.51s and a worst case of
// 12.79s in it.
func TestABlankPaneSaysTheAgentHasNotStarted(t *testing.T) {
	body := terminalRegion(t, preparingTask(), blankPane())

	if !strings.Contains(body, startingSentence) {
		t.Errorf("the blank region says nothing about why it is blank:\n%s", body)
	}
}

// TestAPreparingPaneIsStillDrawn is the test that matters most in this slice.
//
// Claude asks for workspace trust on a directory it has not seen before, and
// every task worktree is one — so the question is drawn while the task is still
// `preparing`. A note that replaced the capture would cover the question the
// user has to answer for the launch to finish at all, which is why the note
// takes a row beside the pane and never the pane's place (ADR-042: nothing here
// reads the capture to decide what to draw).
func TestAPreparingPaneIsStillDrawn(t *testing.T) {
	trust := api.TerminalFrame{Width: 87, Height: 6, Panes: []api.TerminalPane{
		{Pane: "%1", Width: 87, Height: 6, Active: true, Content: []string{
			"Do you trust the files in this folder?", "> 1. Yes, proceed",
		}},
	}}

	body := terminalRegion(t, preparingTask(), trust)

	if !strings.Contains(body, "Do you trust the files in this folder?") {
		t.Errorf("the note covered the question the launch is waiting on:\n%s", body)
	}
	if !strings.Contains(body, "1. Yes, proceed") {
		t.Errorf("the answer the user has to pick is not on screen:\n%s", body)
	}
	if !strings.Contains(body, startingSentence) {
		t.Errorf("the region drew the pane and dropped the note:\n%s", body)
	}
}

// TestADeadPaneWinsTheNoteRow checks the precedence between the two notes.
//
// A pane whose program has exited is not one whose agent is about to paint, so
// the startup sentence there would be a lie. There is one note row and the true
// note takes it.
func TestADeadPaneWinsTheNoteRow(t *testing.T) {
	dead := api.TerminalFrame{Width: 87, Height: 6, Panes: []api.TerminalPane{
		{Pane: "%1", Width: 87, Height: 6, Active: true, Dead: true,
			Content: []string{"claude: command not found"}},
	}}

	body := terminalRegion(t, preparingTask(), dead)

	if !strings.Contains(body, "exited; the terminal is retained so it can be read") {
		t.Errorf("the dead pane is not explained:\n%s", body)
	}
	if strings.Contains(body, startingSentence) {
		t.Errorf("a pane whose program has exited is described as still starting:\n%s", body)
	}
}

// TestAStartupThatStoppedBeingOneSaysSo is what bounds the sentence.
//
// A task can sit in `preparing` indefinitely, and after the startup grace the
// daemon raises attention and leaves the workflow where it is. From that moment
// "the agent has not reported starting yet" is a claim Feat has stopped
// believing, so it is dropped for the honest one. The bound is the attention the
// daemon publishes rather than a clock kept in the view.
func TestAStartupThatStoppedBeingOneSaysSo(t *testing.T) {
	task := preparingTask()
	task.Attention = "possibly_waiting"

	body := terminalRegion(t, task, blankPane())

	if strings.Contains(body, startingSentence) {
		t.Errorf("the region still claims the agent is starting:\n%s", body)
	}
	if !strings.Contains(body, "nothing has been heard from the agent") {
		t.Errorf("the region says nothing about a startup that has not arrived:\n%s", body)
	}
	if !strings.Contains(body, "the pane may be asking something") {
		t.Errorf("the region does not say what the pane might be doing:\n%s", body)
	}
	// No clock, deadline, or elapsed figure: the rail carries elapsed already.
	for _, invented := range []string{"seconds", "elapsed", "timed out"} {
		if strings.Contains(body, invented) {
			t.Errorf("the region invented %q from a state that carries no time:\n%s", invented, body)
		}
	}
}

// TestAWorkingTaskGetsNoStartupNote keeps the note to the state it is about. A
// task whose agent has reported starting spends every row of the region on the
// pane.
func TestAWorkingTaskGetsNoStartupNote(t *testing.T) {
	body := terminalRegion(t, liveTask(), twoPaneFrame())

	if strings.Contains(body, startingSentence) {
		t.Errorf("a working task is described as starting:\n%s", body)
	}
	if strings.Contains(body, "nothing has been heard from the agent") {
		t.Errorf("a working task is described as unheard from:\n%s", body)
	}
}

// TestTheRegionDoesNotBlameTmuxForAStartingAgent covers the branch before any
// frame has arrived.
//
// A user who has just launched is not waiting on tmux — the pane is there and
// the agent has not painted in it — so being told Feat is asking tmux a question
// is a second wrong answer to the one question they have.
func TestTheRegionDoesNotBlameTmuxForAStartingAgent(t *testing.T) {
	// No frame has been delivered, so the region has nothing recorded for this
	// task: the state the dashboard is in the moment the overlay closes.
	model := terminalDashboard(t, newFakeBackend(), preparingTask())

	body := ansi.Strip(model.terminalBody(87, 6))
	if strings.Contains(body, "asking tmux") {
		t.Errorf("the region blamed tmux for an agent that has not started:\n%s", body)
	}
	if !strings.Contains(body, startingSentence) {
		t.Errorf("the region says nothing before the first frame:\n%s", body)
	}

	// The other branch that says it: a frame was recorded for this task and none
	// has loaded.
	model.terminal.task = preparingTask().ID
	model.terminal.loaded = false
	if body := ansi.Strip(model.terminalBody(87, 6)); !strings.Contains(body, startingSentence) {
		t.Errorf("the unloaded branch still blames tmux:\n%s", body)
	}

	// A task nobody launched keeps the sentence about tmux, which is true of it.
	working := terminalDashboard(t, newFakeBackend(), liveTask())
	if body := ansi.Strip(working.terminalBody(87, 6)); !strings.Contains(body, "asking tmux") {
		t.Errorf("a task with no frame yet says nothing at all:\n%s", body)
	}
}

// rowOf is which row of a rendered region carries a string, and how many rows
// there are, so that a note's place can be compared between two renderings.
func rowOf(t *testing.T, body, want string) (row, rows int) {
	t.Helper()

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.Contains(line, want) {
			return i, len(lines)
		}
	}
	t.Fatalf("%q is not in the region:\n%s", want, body)
	return 0, 0
}

// TestTheStartupNoteKeepsItsPlace is the reported glitch.
//
// The note was drawn for one frame in the region's top corner and then, for the
// rest of the wait, in its bottom one: before the first capture it was the
// region's only line, and the moment a pane arrived underneath it moved to the
// foot. A note about waiting that moves while you wait for it is read the same
// way a frozen indicator is — as the dashboard doing something other than what
// it says.
//
// The two renderings either side of the first capture are the ones a user sees
// in that order, a quarter of a second apart, so they are compared directly.
func TestTheStartupNoteKeepsItsPlace(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), preparingTask())

	before, rowsBefore := rowOf(t, ansi.Strip(model.terminalBody(87, 6)), startingSentence)

	loaded, _ := model.Update(terminalFrameMsg{task: preparingTask().ID, frame: blankPane()})
	body := ansi.Strip(loaded.(Model).terminalBody(87, 6))
	after, rowsAfter := rowOf(t, body, startingSentence)

	if before != after {
		t.Errorf("the note moved from row %d to row %d when the first capture arrived", before, after)
	}
	if rowsBefore != 6 || rowsAfter != 6 {
		t.Errorf("the region drew %d rows before the capture and %d after, want six of both",
			rowsBefore, rowsAfter)
	}
	// The middle of a six-row region, a row above the exact centre on an even
	// height, which is where centreOverlay puts a dialog.
	if before != 2 {
		t.Errorf("the note is on row %d of a six-row region, want the middle:\n%s", before, body)
	}
}

// TestTheStartupNoteIsCentredInAnEmptyRegion is what a user watching a launch
// looks at: an empty box, and the one thing in it in the middle of it.
func TestTheStartupNoteIsCentredInAnEmptyRegion(t *testing.T) {
	for name, task := range map[string]api.Task{
		"starting": preparingTask(),
		"unheard from": func() api.Task {
			task := preparingTask()
			task.Attention = "possibly_waiting"
			return task
		}(),
	} {
		body := terminalRegion(t, task, blankPane())
		lines := strings.Split(body, "\n")

		row, rows := rowOf(t, body, strings.TrimSpace(lines[2]))
		if rows != 6 || row != 2 {
			t.Errorf("%s: the note is on row %d of %d, want the middle of six:\n%s",
				name, row, rows, body)
		}

		note := lines[row]
		text := strings.TrimLeft(note, " ")
		indent := ansi.StringWidth(note) - ansi.StringWidth(text)
		if want := (87 - ansi.StringWidth(text)) / 2; indent != want {
			t.Errorf("%s: the note starts at cell %d of 87, want %d", name, indent, want)
		}
		if indent == 0 {
			t.Errorf("%s: the note is still against the region's left edge: %q", name, note)
		}
	}
}

// TestTheStartupNoteLeavesTheMiddleWhenThePanePaints is what makes the vertical
// centring safe.
//
// The middle of the region is the note's only while there is nothing there to
// cover. A workspace-trust prompt is drawn while the task is still `preparing`,
// and a line across the middle would take a row of the question the launch is
// waiting on — so the moment anything is drawn, the note goes back to a row of
// its own at the foot.
func TestTheStartupNoteLeavesTheMiddleWhenThePanePaints(t *testing.T) {
	painted := api.TerminalFrame{Width: 87, Height: 6, Panes: []api.TerminalPane{
		{Pane: "%1", Width: 87, Height: 6, Active: true, Content: []string{
			"", "", "Do you trust the files in this folder?", "", "> 1. Yes, proceed",
		}},
	}}

	body := terminalRegion(t, preparingTask(), painted)
	row, rows := rowOf(t, body, startingSentence)

	if rows != 6 || row != rows-1 {
		t.Errorf("the note is on row %d of %d, want the last:\n%s", row, rows, body)
	}
	for _, kept := range []string{"Do you trust the files in this folder?", "1. Yes, proceed"} {
		if !strings.Contains(body, kept) {
			t.Errorf("the note covered %q:\n%s", kept, body)
		}
	}
	// Still centred across the row it moved to: what changes is the row, not also
	// the column.
	note := strings.Split(body, "\n")[row]
	text := strings.TrimLeft(note, " ")
	if want := (87 - ansi.StringWidth(text)) / 2; ansi.StringWidth(note)-ansi.StringWidth(text) != want {
		t.Errorf("the note lost its centring when it moved to the foot: %q", note)
	}
}

// TestTheStartupNoteIsAnimated is why waiting gained a terminal case.
//
// activity is explicit that a frozen glyph is worse than no glyph: an indicator
// that does not move cannot be told apart from a dashboard that has stopped,
// which is the thing it exists to answer. The mark is only honest if the one
// spinner is running while it is drawn.
func TestTheStartupNoteIsAnimated(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), preparingTask())

	if !model.waiting() {
		t.Fatal("the terminal tab does not report waiting while an agent starts")
	}
	if !model.activity.running {
		t.Error("the one indicator is not animating behind the startup note")
	}

	// And it stops. A task whose agent has reported starting is not a wait.
	started, _ := model.Update(tasksMsg{tasks: []api.Task{liveTask()}})
	if started.(Model).waiting() || started.(Model).activity.running {
		t.Error("the indicator kept running after the agent started")
	}
}

// TestTheStartupSentenceHasOneHome checks the extraction: the task panel's
// wording is the terminal region's, read out of the same function.
func TestTheStartupSentenceHasOneHome(t *testing.T) {
	if note := terminalNote(preparingTask()); note != startingSentence {
		t.Errorf("terminalNote = %q, want the sentence it has always shown", note)
	}
	if note := startingNote(preparingTask()); note != startingSentence {
		t.Errorf("startingNote = %q, want the same sentence", note)
	}

	// The other branches of terminalNote are untouched by the extraction.
	if note := terminalNote(liveTask()); note != "" {
		t.Errorf("a working devcontainer task has a note: %q", note)
	}
	host := liveTask()
	host.Session.ExecutionMode = "host"
	host.Repositories[0].ContainerPath = "/srv/checkout/core"
	if note := terminalNote(host); !strings.Contains(note, "directly on this host") {
		t.Errorf("the host-execution note went missing: %q", note)
	}

	// A draft has no terminal to be starting in.
	draft := preparingTask()
	draft.Session = nil
	if note := startingNote(draft); note != "" {
		t.Errorf("a task with no session is described as starting: %q", note)
	}
}

// TestSpaceReachesTheAgent is the reported defect.
//
// A space arrives as its own key type, not as runes, and its name is the
// character itself rather than "space" — so it fell through both the runes
// branch and the name table, and every space a user typed was dropped.
func TestSpaceReachesTheAgent(t *testing.T) {
	input, ok := translateKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})

	if !ok {
		t.Fatal("a space was dropped")
	}
	if input.Text != " " {
		t.Errorf("a space translated to %+v, want the character", input)
	}
}

// TestEveryOrdinaryKeyIsDelivered walks the keys a user presses while typing, so
// that one falling through the translation is a failure here rather than a
// report.
func TestEveryOrdinaryKeyIsDelivered(t *testing.T) {
	for label, key := range map[string]tea.KeyMsg{
		"a letter":  {Type: tea.KeyRunes, Runes: []rune{'a'}},
		"a space":   {Type: tea.KeySpace, Runes: []rune{' '}},
		"escape":    {Type: tea.KeyEsc},
		"backspace": {Type: tea.KeyBackspace},
		"delete":    {Type: tea.KeyDelete},
		"enter":     {Type: tea.KeyEnter},
		"tab":       {Type: tea.KeyTab},
		"up":        {Type: tea.KeyUp},
		"ctrl+c":    {Type: tea.KeyCtrlC},
	} {
		input, ok := translateKey(key)
		if !ok {
			t.Errorf("%s (%q) is dropped rather than delivered", label, key.String())
			continue
		}
		if err := input.Validate(); err != nil {
			t.Errorf("%s translates to something the daemon refuses: %v", label, err)
		}
	}
}

// TestTypingIsNotSentAsAPaste is the second half of the same report.
//
// An application that has enabled bracketed paste mode is told by the markers
// whether text was typed or pasted, and may insert a paste without running what
// a typed character runs. Every keystroke arriving as a paste is what made
// ordinary keys behave oddly.
func TestTypingIsNotSentAsAPaste(t *testing.T) {
	backend := newFakeBackend()
	focused := press(t, terminalDashboard(t, backend, liveTask()), "i")

	press(t, focused, "x")

	sent := backend.terminalInputs()
	if len(sent) != 1 {
		t.Fatalf("typing sent %d inputs, want 1", len(sent))
	}
	if sent[0].Paste {
		t.Errorf("a keystroke was sent as a paste: %+v", sent[0])
	}
	if sent[0].Text != "x" {
		t.Errorf("the character did not arrive: %+v", sent[0])
	}
}

// TestOnlyOneFrameIsAskedForAtATime is the flicker's other half.
//
// Zoom is a toggle, so two requests racing on it cancel each other: both read an
// unzoomed window, both toggle, and the agent lands back at half the region's
// width until the next round zooms it again. The daemon serialises the sequence;
// this stops the dashboard from queueing the second request at all.
func TestOnlyOneFrameIsAskedForAtATime(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask())
	model.terminal.inFlight = false
	width, height := model.mainRegionSize()

	if first := model.requestFrame(width, height); first == nil {
		t.Fatal("the first request was refused")
	}
	if second := model.requestFrame(width, height); second != nil {
		t.Error("a second frame was asked for while one was outstanding")
	}

	// A reply, whatever it carried, ends the request.
	settled, _ := model.Update(terminalFrameMsg{task: liveTask().ID, frame: twoPaneFrame()})
	next := settled.(Model)
	if next.terminal.inFlight {
		t.Fatal("a reply did not end the outstanding request")
	}
	if again := next.requestFrame(width, height); again == nil {
		t.Error("no frame could be asked for after the previous one arrived")
	}
}

// TestAFrameForAnotherTaskStillEndsTheRequest keeps a discarded reply from
// stopping every later frame.
func TestAFrameForAnotherTaskStillEndsTheRequest(t *testing.T) {
	model := terminalDashboard(t, newFakeBackend(), liveTask(), otherTask())

	settled, _ := model.Update(terminalFrameMsg{task: otherTask().ID, frame: twoPaneFrame()})
	if settled.(Model).terminal.inFlight {
		t.Error("a frame for an unselected task left the request outstanding forever")
	}
}
