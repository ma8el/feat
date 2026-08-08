package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
)

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
	if len(frame.Content) == 0 || !strings.Contains(frame.Content[0], "\x1b[32m") {
		t.Errorf("the frame lost what tmux had rendered: %q", frame.Content)
	}
	if frame.Pane != task.Session.Tmux.Pane {
		t.Errorf("the frame came from pane %s, want the task's agent pane %s",
			frame.Pane, task.Session.Tmux.Pane)
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

	if pastes := server.Pastes(pane); len(pastes) != 1 || pastes[0] != "run the tests" {
		t.Errorf("the pane received pastes %q", pastes)
	}
	if keys := server.Keys(pane); len(keys) != 1 || keys[0] != "Enter" {
		t.Errorf("the pane received keys %q", keys)
	}
	// The paste must take its own buffer with it rather than leaving Feat's text
	// in the user's stack.
	if buffers := server.Buffers(); len(buffers) != 0 {
		t.Errorf("a staged buffer outlived the paste: %q", buffers)
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

// TestAskingForAShellPaneThatDoesNotExistSaysSo keeps the answer actionable
// rather than returning an empty frame that looks like a quiet terminal.
func TestAskingForAShellPaneThatDoesNotExistSaysSo(t *testing.T) {
	service, arranged, _ := launched(t)

	_, err := service.TerminalFrame(context.Background(), arranged.ref.Task,
		api.TerminalView{Width: 80, Height: 24, Shell: true})
	if err == nil {
		t.Fatal("a task with no shell pane returned a frame")
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Errorf("the error does not say what is missing: %v", err)
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
