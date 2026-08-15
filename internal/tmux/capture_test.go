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
	queued  map[string][]string
	fail    map[string]error
	calls   [][]string
}

func newScriptedRunner() *scriptedRunner {
	return &scriptedRunner{
		replies: map[string]string{},
		queued:  map[string][]string{},
		fail:    map[string]error{},
	}
}

// script queues one reply per invocation, for the paths that ask twice and read
// a different shape each time.
func (r *scriptedRunner) script(command string, replies ...string) {
	r.queued[command] = append(r.queued[command], replies...)
}

func (r *scriptedRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	if err, found := r.fail[args[0]]; found {
		return "", err
	}
	if queue := r.queued[args[0]]; len(queue) > 0 {
		r.queued[args[0]] = queue[1:]
		return queue[0], nil
	}
	return r.replies[args[0]], nil
}

// ran counts the invocations of a subcommand.
func (r *scriptedRunner) ran(command string) int {
	count := 0
	for _, args := range r.calls {
		if args[0] == command {
			count++
		}
	}
	return count
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

// TestARenderThatNeedsNothingChangesNothing is the flicker.
//
// Resizing a zoomed window to the size it already has is not a no-op: tmux sets
// the zoomed pane's pty to the size it would have unzoomed and then back, so a
// full-screen program repaints at half the width and repaints again. Issued on
// every poll it flickered continuously; issued once per task switch it flickered
// once. tmux reports the pane at the zoomed width throughout, which is why this
// is invisible from outside the pane and has to be a rule here.
func TestARenderThatNeedsNothingChangesNothing(t *testing.T) {
	runner := newScriptedRunner()
	// A 100x30 window, zoomed on this pane and pinned at that size, which is the
	// state a settled dashboard leaves behind between polls.
	runner.replies["display-message"] = "100\t30\t1\t1\t2\t100\t30\t4\t2\t0\t00\tmanual"
	runner.replies["capture-pane"] = "working\n"

	frame, err := captureAdapter(t, runner).RenderPane(context.Background(), "@3", "%7", 100, 30, false)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	for _, command := range []string{"resize-window", "set-window-option", "resize-pane"} {
		if args, found := runner.call(command); found {
			t.Errorf("a settled frame ran %q", strings.Join(args, " "))
		}
	}
	// And it costs one measurement rather than two: the state it decided on
	// carries the pane's own size, so nothing has to ask again.
	if measured := runner.ran("display-message"); measured != 1 {
		t.Errorf("a settled frame measured %d times, want 1", measured)
	}
	if frame.Width != 100 || frame.CursorX != 4 {
		t.Errorf("the frame is %dx%d with the cursor at %d,%d",
			frame.Width, frame.Height, frame.CursorX, frame.CursorY)
	}
}

// TestARenderSizesTheWindowWhenTheRegionChanged is the other half: the rule is
// to change nothing that is already right, not to stop changing anything.
//
// The frame is measured again afterwards, because the measurement that decided
// the resize was taken before it. Reporting the pre-resize width would tell the
// renderer to clip a full-width pane to its share of a split.
func TestARenderSizesTheWindowWhenTheRegionChanged(t *testing.T) {
	runner := newScriptedRunner()
	// An unzoomed 80x24 window whose pane holds half of it, and then the pane as
	// it stands once the window has been sized and the pane zoomed.
	runner.script("display-message", "80\t24\t0\t1\t2\t39\t24\t0\t0\t0\t00\tlatest", "100\t30\t0\t0\t0")
	runner.replies["capture-pane"] = "working\n"

	frame, err := captureAdapter(t, runner).RenderPane(context.Background(), "@3", "%7", 100, 30, false)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	sized, found := runner.call("resize-window")
	if !found {
		t.Fatal("a region of a different size did not resize the window")
	}
	if got := strings.Join(sized, " "); got != "resize-window -t @3 -x 100 -y 30" {
		t.Errorf("resized with %q", got)
	}
	if _, found := runner.call("resize-pane"); !found {
		t.Error("the pane was not zoomed to fill the window it now has")
	}
	if frame.Width != 100 || frame.Height != 30 {
		t.Errorf("the frame reports %dx%d, want the size it was just given", frame.Width, frame.Height)
	}
}

// TestARenderDoesNotShrinkAWindowHoldingAStoppedPane is the glitch a user
// reported: an agent's prompt drawn over two rows, and staying there.
//
// A resize is a request to repaint and a stopped pane cannot answer it. tmux
// reflows the screen it stopped on instead, so a full-width prompt comes apart
// onto the row below and stays there for as long as the pane is retained — which
// is for as long as the task is kept, because the retained pane is the account of
// what the agent did.
func TestARenderDoesNotShrinkAWindowHoldingAStoppedPane(t *testing.T) {
	runner := newScriptedRunner()
	// One dead pane in a 100x30 window, drawn into a region of 80x20.
	runner.replies["display-message"] = "100\t30\t0\t1\t1\t100\t30\t0\t0\t1\t1\tmanual"
	runner.replies["capture-pane"] = "│ > type here                    │\n"

	frame, err := captureAdapter(t, runner).RenderPane(context.Background(), "@3", "%7", 80, 20, false)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	for _, command := range []string{"resize-window", "set-window-option"} {
		if args, found := runner.call(command); found {
			t.Errorf("a stopped pane was reflowed by %q", strings.Join(args, " "))
		}
	}
	// And it is reported at the size it kept, so the renderer clips it rather
	// than believing it is the size of the region.
	if frame.Width != 100 || frame.Height != 30 {
		t.Errorf("the frame reports %dx%d, want the 100x30 the pane stopped at", frame.Width, frame.Height)
	}
}

// TestAStoppedPaneIsMeasuredByItsWindow keeps the rule about the window rather
// than about the pane being drawn.
//
// A resize reflows every pane of a window, including the ones the dashboard is
// not showing. A live shell beside a stopped agent must therefore not be the
// reason the agent's last screen is taken apart.
func TestAStoppedPaneIsMeasuredByItsWindow(t *testing.T) {
	runner := newScriptedRunner()
	// Two panes, this one live and zoomed, the other one dead.
	runner.replies["display-message"] = "100\t30\t1\t1\t2\t100\t30\t0\t0\t0\t10\tmanual"
	runner.replies["capture-pane"] = "$ \n"

	if _, err := captureAdapter(t, runner).RenderPane(
		context.Background(), "@3", "%7", 80, 20, false); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	if args, found := runner.call("resize-window"); found {
		t.Errorf("the window of a stopped pane was resized by %q", strings.Join(args, " "))
	}
}

// TestARenderGrowsAWindowHoldingAStoppedPane keeps the rule one-directional.
//
// Growing is the repair rather than more of the damage: tmux rejoins exactly the
// rows it split, so a screen a narrower region already took apart comes back
// whole as soon as there is room for it.
func TestARenderGrowsAWindowHoldingAStoppedPane(t *testing.T) {
	runner := newScriptedRunner()
	runner.script("display-message", "60\t20\t0\t1\t1\t60\t20\t0\t0\t1\t1\tmanual", "100\t30\t0\t0\t1")
	runner.replies["capture-pane"] = "working\n"

	frame, err := captureAdapter(t, runner).RenderPane(context.Background(), "@3", "%7", 100, 30, false)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	sized, found := runner.call("resize-window")
	if !found {
		t.Fatal("a stopped pane with room to spare was not grown back")
	}
	if got := strings.Join(sized, " "); got != "resize-window -t @3 -x 100 -y 30" {
		t.Errorf("resized with %q", got)
	}
	if frame.Width != 100 || frame.Height != 30 {
		t.Errorf("the frame reports %dx%d, want the size it was just given", frame.Width, frame.Height)
	}
}

// TestARenderStillShrinksAWindowOfLivePanes is the other half of the same rule:
// a program that can repaint is told about the region, whichever way it moved.
func TestARenderStillShrinksAWindowOfLivePanes(t *testing.T) {
	runner := newScriptedRunner()
	runner.script("display-message", "100\t30\t0\t1\t1\t100\t30\t0\t0\t0\t0\tmanual", "80\t20\t0\t0\t0")
	runner.replies["capture-pane"] = "working\n"

	if _, err := captureAdapter(t, runner).RenderPane(
		context.Background(), "@3", "%7", 80, 20, false); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	sized, found := runner.call("resize-window")
	if !found {
		t.Fatal("a live pane was not resized to the smaller region")
	}
	if got := strings.Join(sized, " "); got != "resize-window -t @3 -x 80 -y 20" {
		t.Errorf("resized with %q", got)
	}
}

// TestAWatchedRenderTouchesNothing keeps a rendering from resizing the terminal
// somebody is sitting in.
func TestAWatchedRenderTouchesNothing(t *testing.T) {
	runner := newScriptedRunner()
	// A 200x50 window sized by the client attached to it, so there is no pin of
	// Feat's to take off.
	runner.replies["display-message"] = "200\t50\t0\t1\t1\t200\t50\t0\t0\t0\t0\tlatest"
	runner.replies["capture-pane"] = "working\n"

	if _, err := captureAdapter(t, runner).RenderPane(
		context.Background(), "@3", "%7", 100, 30, true); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	for _, command := range []string{"resize-window", "set-window-option", "resize-pane"} {
		if args, found := runner.call(command); found {
			t.Errorf("rendering a watched window ran %q", strings.Join(args, " "))
		}
	}
}

// TestAWatchedRenderReleasesThePin is the half of the attach defect that the
// release at hand-over cannot reach.
//
// Sizing a window pins it, and tmux then holds it at that size however large the
// client attaching is. The size is released as the attach target is handed out,
// but the client takes tens of milliseconds to arrive and a frame drawn in that
// gap is told nobody is attached and pins the window again. The client then
// lands in a terminal showing the dashboard's main region with the rest filled
// in with dots — and stays there, because the dashboard that would notice is
// blocked for as long as it has given its terminal away.
//
// So the first frame that sees a client on a window Feat has pinned hands the
// size back, whichever way that client got there.
func TestAWatchedRenderReleasesThePin(t *testing.T) {
	runner := newScriptedRunner()
	// A window still pinned at the dashboard's 100x30, and the pane as it stands
	// once tmux has given the window back to its 200x50 client.
	runner.script("display-message", "100\t30\t0\t1\t1\t100\t30\t0\t0\t0\t0\tmanual", "200\t50\t0\t0\t0")
	runner.replies["capture-pane"] = "working\n"

	frame, err := captureAdapter(t, runner).RenderPane(context.Background(), "@3", "%7", 100, 30, true)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	released, found := runner.call("set-window-option")
	if !found {
		t.Fatal("a watched window was left pinned at the region's size")
	}
	if got := strings.Join(released, " "); got != "set-window-option -u -t @3 window-size" {
		t.Errorf("released with %q", got)
	}
	// Released, not resized: a rendering must not choose the size of a terminal
	// somebody is sitting in, and resizing would pin it again.
	for _, command := range []string{"resize-window", "resize-pane"} {
		if args, found := runner.call(command); found {
			t.Errorf("rendering a watched window ran %q", strings.Join(args, " "))
		}
	}
	// And the frame is measured again, because releasing the pin is what makes
	// tmux resize the window to its client: reporting the pinned size would tell
	// the renderer to draw the client's screen as the region's.
	if frame.Width != 200 || frame.Height != 50 {
		t.Errorf("the frame reports %dx%d, want the 200x50 the client gave it",
			frame.Width, frame.Height)
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
