package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// scriptedRunner answers each tmux subcommand from a script and records what it
// was asked, so that a test can assert on the argument vector rather than on a
// shell string.
type scriptedRunner struct {
	replies map[string]string
	fail    map[string]error
	calls   [][]string
}

func newScriptedRunner() *scriptedRunner {
	return &scriptedRunner{replies: map[string]string{}, fail: map[string]error{}}
}

func (r *scriptedRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	if err, found := r.fail[args[0]]; found {
		return "", err
	}
	return r.replies[args[0]], nil
}

// call returns the argument vector of the first invocation of a subcommand.
func (r *scriptedRunner) call(command string) ([]string, bool) {
	for _, args := range r.calls {
		if args[0] == command {
			return args, true
		}
	}
	return nil, false
}

func captureAdapter(t *testing.T, runner Runner) *Tmux {
	t.Helper()

	adapter, err := New("/run/feat/tmux.sock", runner)
	if err != nil {
		t.Fatalf("building the adapter: %v", err)
	}
	return adapter
}

// TestCapturingAPaneAsksForWhatTmuxAlreadyDrew is ADR-042's boundary in one
// assertion: -e keeps the colour tmux rendered, so what comes back is a finished
// screen rather than a stream to interpret.
//
// And not -J: joining a wrapped line makes it wider than the pane, and a caller
// drawing into a region that wide clips the join off, losing the text tmux had
// put on the next row.
func TestCapturingAPaneAsksForWhatTmuxAlreadyDrew(t *testing.T) {
	runner := newScriptedRunner()
	runner.replies["display-message"] = "80\t24\t12\t3\t0"
	runner.replies["capture-pane"] = "\x1b[32mready\x1b[m\n> \n"

	frame, err := captureAdapter(t, runner).CapturePane(context.Background(), "%7")
	if err != nil {
		t.Fatalf("capturing: %v", err)
	}

	args, found := runner.call("capture-pane")
	if !found {
		t.Fatal("no capture-pane was run")
	}
	if got := strings.Join(args, " "); got != "capture-pane -p -e -t %7" {
		t.Errorf("captured with %q", got)
	}

	if frame.Width != 80 || frame.Height != 24 {
		t.Errorf("frame is %dx%d, want 80x24", frame.Width, frame.Height)
	}
	if frame.CursorX != 12 || frame.CursorY != 3 {
		t.Errorf("cursor at %d,%d, want 12,3", frame.CursorX, frame.CursorY)
	}
	if frame.Dead {
		t.Error("a live pane was reported dead")
	}
	if len(frame.Content) != 2 || !strings.Contains(frame.Content[0], "\x1b[32m") {
		t.Errorf("content did not survive the capture: %q", frame.Content)
	}
}

// TestADeadPaneIsReportedRatherThanDrawnAsLive keeps the distinction the adapter
// already draws for a process that exited.
func TestADeadPaneIsReportedRatherThanDrawnAsLive(t *testing.T) {
	runner := newScriptedRunner()
	runner.replies["display-message"] = "80\t24\t0\t0\t1"
	runner.replies["capture-pane"] = "the process exited\n"

	frame, err := captureAdapter(t, runner).CapturePane(context.Background(), "%7")
	if err != nil {
		t.Fatalf("capturing: %v", err)
	}
	if !frame.Dead {
		t.Error("a retained dead pane was reported as live")
	}
}

// TestEveryOperationRefusesAName is ADR-030's identity rule at this surface. A
// display name or an index is not identity and must not be accepted as one.
func TestEveryOperationRefusesAName(t *testing.T) {
	adapter := captureAdapter(t, newScriptedRunner())
	ctx := context.Background()

	for name, run := range map[string]func(string) error{
		"CapturePane": func(target string) error {
			_, err := adapter.CapturePane(ctx, target)
			return err
		},
		"SendKeys":  func(target string) error { return adapter.SendKeys(ctx, target, "Enter") },
		"PasteText": func(target string) error { return adapter.PasteText(ctx, target, "hello") },
	} {
		for _, target := range []string{"agent", "0", "%", "@3", "feat:0.1", ""} {
			if err := run(target); err == nil {
				t.Errorf("%s accepted %q as a pane", name, target)
			}
		}
	}

	for _, target := range []string{"task", "1", "%3", ""} {
		if err := adapter.ResizeWindow(ctx, target, 80, 24); err == nil {
			t.Errorf("ResizeWindow accepted %q as a window", target)
		}
	}
}

// TestKeysArePassedAfterATerminator stops a key name that begins with a dash
// being read as a flag by tmux.
func TestKeysArePassedAfterATerminator(t *testing.T) {
	runner := newScriptedRunner()

	if err := captureAdapter(t, runner).SendKeys(context.Background(), "%7", "C-c", "Enter"); err != nil {
		t.Fatalf("sending keys: %v", err)
	}

	args, _ := runner.call("send-keys")
	if got := strings.Join(args, " "); got != "send-keys -t %7 -- C-c Enter" {
		t.Errorf("sent %q", got)
	}
}

// TestPastingBracketsTheTextAndCleansUpAfterItself is why typed text does not go
// through send-keys: a long string is truncated, and an unbracketed paste can be
// submitted by the application reading it.
func TestPastingBracketsTheTextAndCleansUpAfterItself(t *testing.T) {
	runner := newScriptedRunner()

	if err := captureAdapter(t, runner).PasteText(context.Background(), "%7", "run the tests"); err != nil {
		t.Fatalf("pasting: %v", err)
	}

	staged, found := runner.call("set-buffer")
	if !found {
		t.Fatal("nothing was staged into a buffer")
	}
	if got := strings.Join(staged, " "); got != "set-buffer -b feat-input -- run the tests" {
		t.Errorf("staged with %q", got)
	}

	pasted, found := runner.call("paste-buffer")
	if !found {
		t.Fatal("the buffer was staged and never pasted")
	}
	joined := strings.Join(pasted, " ")
	if !strings.Contains(joined, "-p") {
		t.Errorf("the paste was not bracketed: %q", joined)
	}
	if !strings.Contains(joined, "-d") {
		t.Errorf("the paste left Feat's buffer in the user's stack: %q", joined)
	}
}

// TestPastingNothingRunsNothing keeps an empty edit from disturbing the buffer
// stack at all.
func TestPastingNothingRunsNothing(t *testing.T) {
	runner := newScriptedRunner()

	if err := captureAdapter(t, runner).PasteText(context.Background(), "%7", ""); err != nil {
		t.Fatalf("pasting nothing: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("pasting nothing ran %v", runner.calls)
	}
}

// TestResizingTakesManualControlFirst checks the setting without which tmux
// resizes the window back to fit whichever client is attached, leaving the
// program wrapping at a width the display does not have.
func TestResizingTakesManualControlFirst(t *testing.T) {
	runner := newScriptedRunner()

	if err := captureAdapter(t, runner).ResizeWindow(context.Background(), "@3", 100, 30); err != nil {
		t.Fatalf("resizing: %v", err)
	}

	if len(runner.calls) < 2 || runner.calls[0][0] != "set-window-option" {
		t.Fatalf("manual sizing was not set before the resize: %v", runner.calls)
	}
	if got := strings.Join(runner.calls[1], " "); got != "resize-window -t @3 -x 100 -y 30" {
		t.Errorf("resized with %q", got)
	}
}

// TestAFailedCaptureNamesThePane keeps the error actionable, which is the rule
// every adapter in this package follows.
func TestAFailedCaptureNamesThePane(t *testing.T) {
	runner := newScriptedRunner()
	runner.replies["display-message"] = "80\t24\t0\t0\t0"
	runner.fail["capture-pane"] = errors.New("no such pane")

	_, err := captureAdapter(t, runner).CapturePane(context.Background(), "%7")
	if err == nil {
		t.Fatal("a failed capture returned no error")
	}
	if !strings.Contains(err.Error(), "%7") {
		t.Errorf("the error does not name the pane: %v", err)
	}
}

// TestAMalformedMeasurementIsReportedRatherThanGuessed keeps a partial answer
// from becoming a frame with plausible zeroes in it.
func TestAMalformedMeasurementIsReportedRatherThanGuessed(t *testing.T) {
	runner := newScriptedRunner()
	runner.replies["display-message"] = "80\t24\n"

	if _, err := captureAdapter(t, runner).CapturePane(context.Background(), "%7"); err == nil {
		t.Error("a short measurement was accepted")
	}

	runner.replies["display-message"] = "80\twide\t0\t0\t0"
	if _, err := captureAdapter(t, runner).CapturePane(context.Background(), "%7"); err == nil {
		t.Error("a non-numeric measurement was accepted")
	}
}

// TestReleasingASizeUndoesThePin is the regression test for what rendering did
// to a native attach.
//
// Sizing a window pins it, and tmux keeps it pinned however large the terminal
// attaching to it is. A user who looked at a pane in the dashboard and then
// attached to it got a terminal the size of the dashboard's main region, with
// the rest of their screen blank.
func TestReleasingASizeUndoesThePin(t *testing.T) {
	runner := newScriptedRunner()

	if err := captureAdapter(t, runner).ReleaseWindowSize(context.Background(), "@3"); err != nil {
		t.Fatalf("releasing: %v", err)
	}

	unset, found := runner.call("set-window-option")
	if !found {
		t.Fatal("the window-size option was never unset")
	}
	if got := strings.Join(unset, " "); got != "set-window-option -u -t @3 window-size" {
		t.Errorf("unset with %q", got)
	}
}

// TestReleasingDoesNotResize is the second half of the same regression, and the
// reason it needs a test of its own: the first attempt at this released the
// option and then called resize-window -A, which re-set window-size to manual
// and pinned the window again at the server's default — smaller than the size it
// was undoing. A release that resizes is not a release.
func TestReleasingDoesNotResize(t *testing.T) {
	runner := newScriptedRunner()

	if err := captureAdapter(t, runner).ReleaseWindowSize(context.Background(), "@3"); err != nil {
		t.Fatalf("releasing: %v", err)
	}

	if args, found := runner.call("resize-window"); found {
		t.Errorf("releasing resized the window: %q", strings.Join(args, " "))
	}
}

func TestReleasingRefusesAName(t *testing.T) {
	adapter := captureAdapter(t, newScriptedRunner())

	for _, target := range []string{"task", "1", "%3", ""} {
		if err := adapter.ReleaseWindowSize(context.Background(), target); err == nil {
			t.Errorf("ReleaseWindowSize accepted %q as a window", target)
		}
	}
}

// TestZoomingIsIdempotent keeps a poll from toggling the zoom four times a
// second, which is what a bare resize-pane -Z would do: it toggles.
func TestZoomingIsIdempotent(t *testing.T) {
	runner := newScriptedRunner()
	// Already zoomed, and on this pane.
	runner.replies["display-message"] = "1\t1\t2"

	if err := captureAdapter(t, runner).ZoomPane(context.Background(), "%7"); err != nil {
		t.Fatalf("zooming: %v", err)
	}
	if args, found := runner.call("resize-pane"); found {
		t.Errorf("an already-zoomed pane was toggled: %q", strings.Join(args, " "))
	}
}

// TestZoomingASinglePaneWindowDoesNothing avoids a state change with nothing to
// show for it.
func TestZoomingASinglePaneWindowDoesNothing(t *testing.T) {
	runner := newScriptedRunner()
	runner.replies["display-message"] = "0\t1\t1"

	if err := captureAdapter(t, runner).ZoomPane(context.Background(), "%7"); err != nil {
		t.Fatalf("zooming: %v", err)
	}
	if _, found := runner.call("resize-pane"); found {
		t.Error("a window with one pane was zoomed")
	}
}

// TestZoomingReleasesAnotherPanesZoomFirst is the toggle trap: zooming while a
// different pane is zoomed would otherwise unzoom the window rather than zoom
// this pane.
func TestZoomingReleasesAnotherPanesZoomFirst(t *testing.T) {
	runner := newScriptedRunner()
	runner.replies["display-message"] = "1\t0\t2"

	if err := captureAdapter(t, runner).ZoomPane(context.Background(), "%7"); err != nil {
		t.Fatalf("zooming: %v", err)
	}

	zooms := 0
	for _, args := range runner.calls {
		if args[0] == "resize-pane" {
			zooms++
		}
	}
	if zooms != 2 {
		t.Errorf("zooming over another pane's zoom ran %d toggles, want 2", zooms)
	}
}

// TestUnzoomingAnUnzoomedWindowDoesNothing keeps the release from zooming a
// window that was not zoomed, which a bare toggle would do.
func TestUnzoomingAnUnzoomedWindowDoesNothing(t *testing.T) {
	runner := newScriptedRunner()
	runner.replies["display-message"] = "0"

	if err := captureAdapter(t, runner).UnzoomWindow(context.Background(), "@3"); err != nil {
		t.Fatalf("unzooming: %v", err)
	}
	if args, found := runner.call("resize-pane"); found {
		t.Errorf("an unzoomed window was toggled into zoom: %q", strings.Join(args, " "))
	}
}
