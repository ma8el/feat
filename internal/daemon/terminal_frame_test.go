package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// theClientNeverArrived moves the clock past the handover grace.
//
// Asking for an attach target hands the window to a client, and rendering leaves
// it alone until that client is either attached or judged not to be coming. A
// test that wants the dashboard's own sizing back says so with this rather than
// by having no attach in its history, because the attach is usually the thing it
// is testing the far side of.
func theClientNeverArrived(service *service) {
	at := service.now().Add(attachGrace)
	service.now = func() time.Time { return at }
}

// TestAFrameIsSizedBeforeItIsCaptured is the reason the daemon resizes rather
// than the renderer coping.
//
// A program wraps its own output. A pane left at another size comes back wrapped
// at a column the display does not have, and no care in the renderer would
// straighten it.
func TestAFrameIsSizedBeforeItIsCaptured(t *testing.T) {
	service, arranged, server := launched(t)
	task, err := service.Task(context.Background(), arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	server.SetPaneContent(task.Session.Tmux.Pane, "\x1b[32mready\x1b[m\n> ")

	frame, err := service.TerminalFrame(context.Background(), arranged.ref.Task,
		api.TerminalView{Width: 100, Height: 30})
	if err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}

	size, resized := server.PaneSize(task.Session.Tmux.Window)
	if !resized || size != [2]int{100, 30} {
		t.Errorf("the window was sized to %v (set: %t), want 100x30", size, resized)
	}
	if frame.Width != 100 || frame.Height != 30 {
		t.Errorf("the frame reports %dx%d, want the size it was set to", frame.Width, frame.Height)
	}
	if len(frame.Panes) == 0 {
		t.Fatal("the frame carries no panes")
	}
	agent := frame.Panes[0]
	if len(agent.Content) == 0 || !strings.Contains(agent.Content[0], "\x1b[32m") {
		t.Errorf("the frame lost what tmux had rendered: %q", agent.Content)
	}
	if agent.Pane != task.Session.Tmux.Pane {
		t.Errorf("the frame came from pane %s, want the task's agent pane %s",
			agent.Pane, task.Session.Tmux.Pane)
	}
}

// TestANewTaskWindowIsCreatedAtTheSizeTheDashboardDraws is the defect a dogfood
// screenshot showed: an agent's whole first screen — the provider's banner, its
// "do you trust this folder" prompt, the first turn of work — wrapped at 80
// columns inside a region more than twice that wide, and staying there, because
// a terminal's committed lines do not reflow when it is resized afterwards.
//
// The window was made at tmux's default and sized only when the dashboard first
// drew it, which was already too late. The frame is the one place a client's
// dimensions reach the daemon, so it is where the next task's window learns
// them.
func TestANewTaskWindowIsCreatedAtTheSizeTheDashboardDraws(t *testing.T) {
	service, arranged, server := launched(t)

	// The first task of a daemon that has drawn nothing gets tmux's own default,
	// because there is nothing better to give it and a guess would be worse.
	first, err := service.Task(context.Background(), arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the first task: %v", err)
	}
	if size, sized := server.PaneSize(first.Session.Tmux.Window); sized {
		t.Errorf("the first window was sized to %v before any client had drawn one", size)
	}

	if _, err := service.TerminalFrame(context.Background(), arranged.ref.Task,
		api.TerminalView{Width: 171, Height: 49}); err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}

	second, err := service.PrepareTerminal(context.Background(),
		alsoPrepared(t, service, "Add a health check"), placeholder(arranged.home))
	if err != nil {
		t.Fatalf("preparing the second terminal: %v", err)
	}
	size, sized := server.PaneSize(second.Session.Tmux.Window)
	if !sized || size != [2]int{171, 49} {
		t.Errorf("the second task's window is %v (sized: %t), want the 171x49 the dashboard draws",
			size, sized)
	}
}

// TestInputReachesTheTaskOwnPane checks that text and keys arrive in the order
// that makes them mean what the user meant: the Enter has to follow what it
// submits.
func TestInputReachesTheTaskOwnPane(t *testing.T) {
	service, arranged, server := launched(t)
	task, err := service.Task(context.Background(), arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	pane := task.Session.Tmux.Pane

	if err := service.SendTerminalInput(context.Background(), arranged.ref.Task,
		api.TerminalInput{Text: "run the tests", Keys: []string{"Enter"}}); err != nil {
		t.Fatalf("SendTerminalInput: %v", err)
	}

	if typed := server.Typed(pane); len(typed) != 1 || typed[0] != "run the tests" {
		t.Errorf("the pane received typing %q", typed)
	}
	if pastes := server.Pastes(pane); len(pastes) != 0 {
		t.Errorf("typing was delivered as a paste: %q", pastes)
	}
	if keys := server.Keys(pane); len(keys) != 1 || keys[0] != "Enter" {
		t.Errorf("the pane received keys %q", keys)
	}
	// Typing stages nothing, so nothing can be left in the user's buffer stack.
	if buffers := server.Buffers(); len(buffers) != 0 {
		t.Errorf("typing left a staged buffer behind: %q", buffers)
	}
}

// TestAPasteIsDeliveredAsAPaste keeps the other half of the distinction: a block
// of text a user pasted is bracketed, so the program reading it knows it was not
// typed and cannot take a trailing newline as a submission.
func TestAPasteIsDeliveredAsAPaste(t *testing.T) {
	service, arranged, server := launched(t)
	task, err := service.Task(context.Background(), arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	pane := task.Session.Tmux.Pane

	if err := service.SendTerminalInput(context.Background(), arranged.ref.Task,
		api.TerminalInput{Text: "a pasted brief", Paste: true}); err != nil {
		t.Fatalf("SendTerminalInput: %v", err)
	}

	if pastes := server.Pastes(pane); len(pastes) != 1 || pastes[0] != "a pasted brief" {
		t.Errorf("the pane received pastes %q", pastes)
	}
	if typed := server.Typed(pane); len(typed) != 0 {
		t.Errorf("a paste was delivered as typing: %q", typed)
	}
	if buffers := server.Buffers(); len(buffers) != 0 {
		t.Errorf("a staged buffer outlived the paste: %q", buffers)
	}
}

// TestTheTwoAbsencesAreToldApart is what lets a client answer each of them with
// the key that resolves it.
//
// A task whose window was killed and a task that was never given a shell are
// both absent panes with different remedies — one is resumed and one is opened —
// and a client that received the same classification for both could only tell
// them apart by reading the message.
func TestTheTwoAbsencesAreToldApart(t *testing.T) {
	service, arranged, _ := launched(t)
	ctx := context.Background()

	// The shell view of a task nobody has opened a shell for.
	_, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 100, Height: 30, Shell: true})
	if !api.IsShellMissing(err) {
		t.Errorf("the shell view of a task with no shell = %v, want a missing shell", err)
	}
	if api.IsTerminalMissing(err) {
		t.Errorf("a missing shell was classified as a missing terminal: %v", err)
	}

	// And the agent's own view once the window is gone.
	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	if _, err := service.terminals.RemoveTask(ctx, task.ProjectID, task.ID); err != nil {
		t.Fatalf("removing the task's window: %v", err)
	}
	_, err = service.TerminalFrame(ctx, arranged.ref.Task, api.TerminalView{Width: 100, Height: 30})
	if !api.IsTerminalMissing(err) {
		t.Errorf("the agent view of a killed window = %v, want a missing terminal", err)
	}
	if api.IsShellMissing(err) {
		t.Errorf("a missing terminal was classified as a missing shell: %v", err)
	}
}

// TestAnInvalidRequestIsRefusedByTheDaemonToo keeps the check on the daemon as
// well as on the transport. The service is an interface anything in-process can
// call, and a rule enforced only at the edge is a rule with one caller.
func TestAnInvalidRequestIsRefusedByTheDaemonToo(t *testing.T) {
	service, arranged, _ := launched(t)
	ctx := context.Background()

	if _, err := service.TerminalFrame(ctx, arranged.ref.Task, api.TerminalView{}); err == nil {
		t.Error("a frame with no size was rendered")
	}
	if err := service.SendTerminalInput(ctx, arranged.ref.Task,
		api.TerminalInput{Keys: []string{"Enter; rm -rf /"}}); err == nil {
		t.Error("a key that is not a key name was delivered")
	}
}

// TestTheRegionShowsOnePaneFillingIt is the decision that replaced drawing the
// whole window.
//
// A task that has opened a shell holds two panes side by side, and a window
// sized to the region gives the agent half of it. Both were drawn for a while,
// and the reason that does not work is that tmux's own keys cannot move between
// them: nothing is attached, so a prefix reaches the program in the pane rather
// than tmux. A pane a user can see and cannot leave is worse than one pane.
func TestTheRegionShowsOnePaneFillingIt(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	if _, err := service.OpenShell(ctx, arranged.ref.Task); err != nil {
		t.Fatalf("OpenShell: %v", err)
	}
	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	// Opening a shell hands the terminal to a native client, and this is about
	// what the dashboard draws once that client is out of the picture.
	theClientNeverArrived(service)

	frame, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 100, Height: 30})
	if err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}

	if len(frame.Panes) != 1 {
		t.Fatalf("the region was given %d panes, want the one it is for", len(frame.Panes))
	}
	if frame.Panes[0].Pane != task.Session.Tmux.Pane {
		t.Errorf("the region shows pane %s, want the agent's %s",
			frame.Panes[0].Pane, task.Session.Tmux.Pane)
	}
	if zoomed := server.Zoomed(task.Session.Tmux.Window); zoomed != task.Session.Tmux.Pane {
		t.Errorf("the agent's pane is not the one filling the window: %q", zoomed)
	}
}

// TestAttachingShowsEveryPaneAgain is the other half of that decision. The shell
// is still there for a user who wants it; it is the dashboard that shows one.
func TestAttachingShowsEveryPaneAgain(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	if _, err := service.OpenShell(ctx, arranged.ref.Task); err != nil {
		t.Fatalf("OpenShell: %v", err)
	}
	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	theClientNeverArrived(service)
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 100, Height: 30}); err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}
	if server.Zoomed(task.Session.Tmux.Window) == "" {
		t.Fatal("rendering did not zoom a pane, so this proves nothing")
	}

	if _, err := service.AttachInfo(ctx, arranged.ref.Task); err != nil {
		t.Fatalf("AttachInfo: %v", err)
	}
	if zoomed := server.Zoomed(task.Session.Tmux.Window); zoomed != "" {
		t.Errorf("attaching left the window zoomed on %s, hiding the shell", zoomed)
	}
}

// TestATaskWithNoTerminalIsNotDrawn checks a draft, which owns no pane at all.
func TestATaskWithNoTerminalIsNotDrawn(t *testing.T) {
	arranged := prepared(t)

	_, err := arranged.service.TerminalFrame(context.Background(), arranged.ref.Task,
		api.TerminalView{Width: 80, Height: 24})
	if err == nil {
		t.Fatal("a task with no terminal returned a frame")
	}
}

// TestAttachingReleasesTheSizeRenderingPinned is the regression at the level a
// user met it.
//
// Drawing a pane in the dashboard sizes its window to the main region. A native
// attach then inherits that size and leaves the rest of the terminal blank,
// because tmux keeps a sized window at its size however large the client is. The
// size has to be released as the client takes over.
func TestAttachingReleasesTheSizeRenderingPinned(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	window := task.Session.Tmux.Window

	if _, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 87, Height: 21}); err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}
	if size, pinned := server.PaneSize(window); !pinned || size != [2]int{87, 21} {
		t.Fatalf("rendering did not size the window: %v %t", size, pinned)
	}

	if _, err := service.AttachInfo(ctx, arranged.ref.Task); err != nil {
		t.Fatalf("AttachInfo: %v", err)
	}
	if size, pinned := server.PaneSize(window); pinned {
		t.Errorf("attaching left the window pinned at %v", size)
	}
}

// TestPollingASettledPaneLeavesTmuxAlone is the flicker, at the level a user met
// it.
//
// The terminal tab polls, four times a second and sixteen while it has the
// keyboard. Each poll used to size the window and each size disturbed a zoomed
// pane's pty, so a full-screen agent repainted at half the region's width and
// repainted back. Once nothing needs changing, a poll must change nothing.
func TestPollingASettledPaneLeavesTmuxAlone(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	// A shell beside the agent, so the window has two panes and the zoom this is
	// about is doing something.
	if _, err := service.OpenShell(ctx, arranged.ref.Task); err != nil {
		t.Fatalf("OpenShell: %v", err)
	}
	view := api.TerminalView{Width: 100, Height: 30}
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task, view); err != nil {
		t.Fatalf("the first frame: %v", err)
	}

	settled := len(server.Calls())
	for i := range 3 {
		if _, err := service.TerminalFrame(ctx, arranged.ref.Task, view); err != nil {
			t.Fatalf("frame %d: %v", i+2, err)
		}
	}

	for _, args := range server.Calls()[settled:] {
		switch args[0] {
		case "resize-window", "resize-pane", "set-window-option":
			t.Errorf("a poll at an unchanged size ran %q", strings.Join(args, " "))
		}
	}
}

// TestAResizedRegionStillReachesTheWindow keeps the rule above from becoming
// "never resize": a user who resized their terminal must see the pane follow.
func TestAResizedRegionStillReachesTheWindow(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 100, Height: 30}); err != nil {
		t.Fatalf("the first frame: %v", err)
	}

	frame, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 120, Height: 40})
	if err != nil {
		t.Fatalf("the frame after a resize: %v", err)
	}

	if size, _ := server.PaneSize(task.Session.Tmux.Window); size != [2]int{120, 40} {
		t.Errorf("the window is %v, want the region's new 120x40", size)
	}
	if frame.Width != 120 || frame.Height != 40 {
		t.Errorf("the frame reports %dx%d, want the size it was just given", frame.Width, frame.Height)
	}
}

// TestAStoppedAgentKeepsTheScreenItStoppedOn is the reported glitch at the level
// a user met it: for some tasks the agent's prompt was drawn over two rows in
// the terminal tab, and stayed there.
//
// Those were the tasks whose agent had stopped. Feat keeps their pane on purpose
// — it is the account of what the session did — and a kept pane has nobody left
// to repaint it, so sizing its window to the region made tmux reflow the screen
// it stopped on and nothing ever put it back.
func TestAStoppedAgentKeepsTheScreenItStoppedOn(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	window := task.Session.Tmux.Window

	// The screen was painted at the region the dashboard was showing it in.
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 100, Height: 30}); err != nil {
		t.Fatalf("the frame before the agent stopped: %v", err)
	}
	server.Died(task.Session.Tmux.Pane, 0)

	// And then the same task is looked at from a narrower terminal.
	frame, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 80, Height: 20})
	if err != nil {
		t.Fatalf("the frame after the agent stopped: %v", err)
	}

	if size, _ := server.PaneSize(window); size != [2]int{100, 30} {
		t.Errorf("the window of a stopped agent was resized to %v, want the 100x30 its screen was painted at", size)
	}
	// The frame says how big it really is, so that the renderer clips it rather
	// than drawing it as though it fitted.
	if frame.Width != 100 || frame.Height != 30 {
		t.Errorf("the frame reports %dx%d, want the size the pane kept", frame.Width, frame.Height)
	}
}

// TestAWatchedWindowIsNotResizedByTheDashboard closes the other route to the
// same defect.
//
// Releasing the size on attach fixes a user who attaches and comes back. It does
// nothing for a dashboard left open on the terminal tab while the user attaches
// from another window: the poll would re-pin the window to the main region four
// times a second, shrinking the terminal they are sitting in. A window with a
// viewer keeps the size its viewer gives it.
func TestAWatchedWindowIsNotResizedByTheDashboard(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	server.Watch(task.Session.Tmux.Window, 1)

	if _, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 87, Height: 21}); err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}

	if size, pinned := server.PaneSize(task.Session.Tmux.Window); pinned {
		t.Errorf("a watched window was resized to %v by a dashboard poll", size)
	}
}

// TestAFrameDuringAnAttachLeavesTheWindowToTheClient is the defect that survived
// the first fix, at the level a user met it.
//
// Asking for an attach target releases the size rendering pinned. The client
// then takes tens of milliseconds to start, and the dashboard polls up to
// sixteen times a second, so a frame lands in the gap: tmux is asked who is
// attached, says nobody, and the window is pinned again before the client ever
// arrives. The user attaches into the dashboard's main region with tmux's fill
// characters over the rest of their terminal — and stays there, because a
// dashboard that has handed its terminal away is not polling to notice.
func TestAFrameDuringAnAttachLeavesTheWindowToTheClient(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	window := task.Session.Tmux.Window

	view := api.TerminalView{Width: 87, Height: 21}
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task, view); err != nil {
		t.Fatalf("the frame before the attach: %v", err)
	}
	if _, pinned := server.PaneSize(window); !pinned {
		t.Fatal("rendering did not pin the window, so this proves nothing")
	}

	// The user presses a. The client is on its way and has not arrived: nothing
	// is attached to this window yet.
	if _, err := service.AttachInfo(ctx, arranged.ref.Task); err != nil {
		t.Fatalf("AttachInfo: %v", err)
	}
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task, view); err != nil {
		t.Fatalf("the frame during the attach: %v", err)
	}

	if size, pinned := server.PaneSize(window); pinned {
		t.Errorf("a frame drawn while a client was attaching pinned the window at %v", size)
	}
	if zoomed := server.Zoomed(window); zoomed != "" {
		t.Errorf("a frame drawn while a client was attaching zoomed it onto %s", zoomed)
	}
}

// TestTheDashboardTakesTheWindowBackWhenNoClientCame keeps the handover a pause
// rather than a surrender.
//
// An attach can fail, or a user can change their mind before tmux starts. The
// window is nobody's then, and the dashboard has to be able to draw it properly
// again — otherwise one attach that never happened would leave the pane wrapping
// at a width the region does not have for as long as the daemon ran.
func TestTheDashboardTakesTheWindowBackWhenNoClientCame(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	if _, err := service.AttachInfo(ctx, arranged.ref.Task); err != nil {
		t.Fatalf("AttachInfo: %v", err)
	}
	theClientNeverArrived(service)

	if _, err := service.TerminalFrame(ctx, arranged.ref.Task,
		api.TerminalView{Width: 87, Height: 21}); err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}

	size, pinned := server.PaneSize(task.Session.Tmux.Window)
	if !pinned || size != [2]int{87, 21} {
		t.Errorf("the dashboard drew into a window sized %v (pinned: %t), want its own 87x21", size, pinned)
	}
}

// TestAWatchedWindowIsReleasedByTheDashboard is the repair for a client that
// arrived by a route the daemon never saw.
//
// A user can attach with tmux itself, or from a second terminal while this
// dashboard polls, and a window Feat pinned before any of that would hold them
// at the main region's size indefinitely. Whichever way a client got there, the
// first frame that sees one hands the size back.
func TestAWatchedWindowIsReleasedByTheDashboard(t *testing.T) {
	service, arranged, server := launched(t)
	ctx := context.Background()

	task, err := service.Task(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("reading the task: %v", err)
	}
	window := task.Session.Tmux.Window

	view := api.TerminalView{Width: 87, Height: 21}
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task, view); err != nil {
		t.Fatalf("the frame before the attach: %v", err)
	}
	if _, pinned := server.PaneSize(window); !pinned {
		t.Fatal("rendering did not pin the window, so this proves nothing")
	}

	// Somebody attaches, without going through the daemon to do it.
	server.Watch(window, 1)
	if _, err := service.TerminalFrame(ctx, arranged.ref.Task, view); err != nil {
		t.Fatalf("the frame after the attach: %v", err)
	}

	if size, pinned := server.PaneSize(window); pinned {
		t.Errorf("a window with a client on it was left pinned at %v", size)
	}
}
