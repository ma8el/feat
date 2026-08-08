package tmux

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// paneID and windowID are tmux's immutable object identifiers.
//
// Every operation here takes one rather than a name or an index, for the reason
// ADR-030 gave: a user's configuration may rename or renumber anything, and only
// these survive it.
var (
	paneID   = regexp.MustCompile(`^%[0-9]+$`)
	windowID = regexp.MustCompile(`^@[0-9]+$`)
)

// inputBuffer is the tmux buffer Feat pastes through.
//
// A named buffer rather than the anonymous stack, so that Feat never disturbs
// what the user has copied: paste-buffer -d deletes this one and leaves theirs.
const inputBuffer = "feat-input"

// frameFormat is what one query returns beside the pane's content.
const frameFormat = "#{pane_width}\t#{pane_height}\t#{cursor_x}\t#{cursor_y}\t#{pane_dead}"

// zoomFormat is what a zoom decision needs to know.
const zoomFormat = "#{window_zoomed_flag}\t#{pane_active}\t#{window_panes}"

// renderFormat is everything RenderPane needs, in one query.
//
// The window's size joins the zoom state and the pane's own measurements so that
// a frame with nothing to change costs two tmux invocations rather than five.
// Each one is a process, and while the pane has the keyboard this runs sixteen
// times a second: measured against tmux 3.7b, 30.5 ms a frame became 16.3 ms.
const renderFormat = "#{window_width}\t#{window_height}\t" + zoomFormat + "\t" + frameFormat

// PaneFrame is one pane as tmux has already drawn it.
//
// Content holds the escape sequences tmux emitted, and Feat passes them through
// without interpreting them: what it reads out of this is cell width, in order
// to place and clip the rectangle. Deriving task, agent, attention, or workflow
// state from these bytes is refused by ADR-042 and remains the job of provider
// hooks.
type PaneFrame struct {
	// Pane is the pane this was captured from.
	Pane string
	// Content is the visible pane, one line per row, with colour intact.
	Content []string
	// Width and Height are the pane's size in cells, which is what the caller
	// must match for the program's own wrapping to line up with the display.
	Width, Height int
	// CursorX and CursorY are the cursor's position, which the capture does not
	// carry and a focused pane needs.
	CursorX, CursorY int
	// Dead reports a pane whose program has exited and which tmux is retaining.
	Dead bool
}

// CapturePane returns the pane's visible content as tmux has rendered it.
//
// This is display and never a source of truth (ADR-042). tmux owns the pty,
// interprets the program's output, and maintains the screen, and -e asks for
// that screen with its colour attributes.
//
// Deliberately not -J. Joining a wrapped line returns one line wider than the
// pane, and a caller drawing into a region the width of that pane then clips it
// — so the wrapped part is discarded rather than shown on the row tmux put it
// on. Measured against tmux 3.5a in a 40-cell pane: -J returns one 68-cell line
// where the terminal shows 40 cells and 28. Whether a given line is wrapped
// changes as a program redraws, so the text it costs appears and disappears.
//
// Only the visible pane is captured. Scrollback needs -S and -E, and a user who
// wants the real terminal attaches to it, which ADR-030 left unchanged.
func (t *Tmux) CapturePane(ctx context.Context, pane string) (PaneFrame, error) {
	return t.capturePane(ctx, pane)
}

func (t *Tmux) capturePane(ctx context.Context, pane string) (PaneFrame, error) {
	frame, err := t.measurePane(ctx, pane)
	if err != nil {
		return PaneFrame{}, err
	}
	return t.captureInto(ctx, frame)
}

// measurePane reads a pane's size, cursor, and liveness.
func (t *Tmux) measurePane(ctx context.Context, pane string) (PaneFrame, error) {
	if !paneID.MatchString(pane) {
		return PaneFrame{}, fmt.Errorf("capturing a pane needs a tmux pane identifier, but got %q", pane)
	}

	measured, err := t.runner.Run(ctx, t.socket, "display-message", "-p", "-t", pane, frameFormat)
	if err != nil {
		return PaneFrame{}, fmt.Errorf("measuring pane %s: %w", pane, err)
	}
	return parseFrame(pane, measured)
}

// captureInto fills a measured frame with the pane's visible content.
func (t *Tmux) captureInto(ctx context.Context, frame PaneFrame) (PaneFrame, error) {
	content, err := t.runner.Run(ctx, t.socket, "capture-pane", "-p", "-e", "-t", frame.Pane)
	if err != nil {
		return PaneFrame{}, fmt.Errorf("capturing pane %s: %w", frame.Pane, err)
	}
	frame.Content = strings.Split(strings.TrimRight(content, "\n"), "\n")
	return frame, nil
}

// parseFrame reads the measurements that accompany a capture.
func parseFrame(pane, measured string) (PaneFrame, error) {
	fields := strings.Split(strings.TrimRight(measured, "\n"), "\t")
	if len(fields) != 5 {
		return PaneFrame{}, fmt.Errorf("measuring pane %s returned %d fields, want 5: %q",
			pane, len(fields), measured)
	}

	frame := PaneFrame{Pane: pane, Dead: fields[4] == "1"}
	for i, target := range []*int{&frame.Width, &frame.Height, &frame.CursorX, &frame.CursorY} {
		value, err := strconv.Atoi(fields[i])
		if err != nil {
			return PaneFrame{}, fmt.Errorf("measuring pane %s: field %d is %q, which is not a number",
				pane, i+1, fields[i])
		}
		*target = value
	}
	return frame, nil
}

// SendKeys delivers key names to a pane.
//
// The names are tmux's own — Enter, Escape, C-c, Up — and they are passed after
// a terminator so that a name beginning with a dash cannot be read as a flag.
// Typed text does not come through here; see PasteText.
func (t *Tmux) SendKeys(ctx context.Context, pane string, keys ...string) error {
	if !paneID.MatchString(pane) {
		return fmt.Errorf("sending keys needs a tmux pane identifier, but got %q", pane)
	}
	if len(keys) == 0 {
		return nil
	}
	for _, key := range keys {
		if key == "" {
			return fmt.Errorf("sending keys to pane %s: one of them is empty", pane)
		}
	}

	args := append([]string{"send-keys", "-t", pane, "--"}, keys...)
	if _, err := t.runner.Run(ctx, t.socket, args...); err != nil {
		return fmt.Errorf("sending keys to pane %s: %w", pane, err)
	}
	return nil
}

// TypeText delivers text to a pane as though it were typed.
//
// -l sends the characters literally, which is what a keystroke is. The bracketed
// paste PasteText uses is not: an application that has asked for bracketed paste
// mode — which a full-screen agent does — is told by the markers that what
// arrives was pasted rather than typed, and may insert it without running the
// handling a typed character goes through. Sending every keystroke that way made
// ordinary keys behave oddly.
func (t *Tmux) TypeText(ctx context.Context, pane, text string) error {
	if !paneID.MatchString(pane) {
		return fmt.Errorf("typing needs a tmux pane identifier, but got %q", pane)
	}
	if text == "" {
		return nil
	}

	if _, err := t.runner.Run(ctx, t.socket, "send-keys", "-t", pane, "-l", "--", text); err != nil {
		return fmt.Errorf("typing into pane %s: %w", pane, err)
	}
	return nil
}

// PasteText delivers a block of text to a pane through a buffer.
//
// Not send-keys, for two measured reasons that agent-manager's implementation
// records and this follows: send-keys truncates a long string, and an
// application reading a paste without bracketing can consume the trailing
// newline as a submission the user did not make. -p brackets it and -d removes
// the buffer afterwards, so nothing Feat pastes stays in the user's buffer
// stack.
//
// set-buffer rather than load-buffer because the buffer's contents arrive as an
// argument, and the Runner this adapter is built on passes argument vectors
// rather than standard input.
func (t *Tmux) PasteText(ctx context.Context, pane, text string) error {
	if !paneID.MatchString(pane) {
		return fmt.Errorf("pasting needs a tmux pane identifier, but got %q", pane)
	}
	if text == "" {
		return nil
	}

	if _, err := t.runner.Run(ctx, t.socket, "set-buffer", "-b", inputBuffer, "--", text); err != nil {
		return fmt.Errorf("staging input for pane %s: %w", pane, err)
	}
	if _, err := t.runner.Run(ctx, t.socket,
		"paste-buffer", "-p", "-d", "-b", inputBuffer, "-t", pane); err != nil {
		return fmt.Errorf("pasting into pane %s: %w", pane, err)
	}
	return nil
}

// RenderPane prepares a pane for display and captures it, as one operation.
//
// The steps are held under the adapter's lock because zoom is a toggle and two
// callers racing on it cancel each other: both read an unzoomed window, both
// issue a toggle, and the second undoes the first.
//
// Nothing is changed that is already as it should be. That is not a saving; it
// is the whole correctness of this function. Resizing a zoomed window sets the
// zoomed pane's pty to the size it would have unzoomed and then back again, so a
// full-screen program repaints itself at its share of a split and repaints again
// at the window's width. Sizing on every poll made an agent flicker between the
// region's width and half of it, and sizing once per task switch made it happen
// exactly once. Measured against tmux 3.7b, with stty read inside the pane of a
// zoomed two-pane window sized 179x52:
//
//	left alone                 resize-window to the same 179x52
//	52 179                     52 90   <- the program is told the unzoomed share
//	52 179                     52 179
//
// tmux reports pane_width as 179 throughout, which is why sampling the window
// from outside shows a state that never moves while the display flickers.
//
// Sizing and zooming are skipped entirely for a window somebody is attached to,
// which keeps a rendering from resizing a real client's terminal.
func (t *Tmux) RenderPane(ctx context.Context, window, pane string, width, height int, prepare bool) (PaneFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !prepare {
		return t.capturePane(ctx, pane)
	}
	if !windowID.MatchString(window) {
		return PaneFrame{}, fmt.Errorf("rendering needs a tmux window identifier, but got %q", window)
	}

	state, err := t.renderState(ctx, pane)
	if err != nil {
		return PaneFrame{}, err
	}

	moved := false
	if state.windowWidth != width || state.windowHeight != height {
		if err := t.resizeWindow(ctx, window, width, height); err != nil {
			return PaneFrame{}, err
		}
		moved = true
	}
	zoomed, err := t.applyZoom(ctx, pane, state.zoom)
	if err != nil {
		return PaneFrame{}, err
	}

	// The measurement came with the state, and stands unless one of the steps
	// above has just invalidated it.
	frame := state.frame
	if moved || zoomed {
		if frame, err = t.measurePane(ctx, pane); err != nil {
			return PaneFrame{}, err
		}
	}
	return t.captureInto(ctx, frame)
}

// renderState is a pane and the window around it, as one measurement.
type renderState struct {
	windowWidth, windowHeight int
	zoom                      zoomState
	frame                     PaneFrame
}

// renderState reads everything RenderPane decides on.
func (t *Tmux) renderState(ctx context.Context, pane string) (renderState, error) {
	if !paneID.MatchString(pane) {
		return renderState{}, fmt.Errorf("rendering needs a tmux pane identifier, but got %q", pane)
	}

	measured, err := t.runner.Run(ctx, t.socket, "display-message", "-p", "-t", pane, renderFormat)
	if err != nil {
		return renderState{}, fmt.Errorf("measuring pane %s and its window: %w", pane, err)
	}
	fields := strings.Split(strings.TrimRight(measured, "\n"), "\t")
	if len(fields) != 10 {
		return renderState{}, fmt.Errorf(
			"measuring pane %s and its window returned %d fields, want 10: %q", pane, len(fields), measured)
	}

	state := renderState{}
	for i, target := range []*int{&state.windowWidth, &state.windowHeight} {
		value, err := strconv.Atoi(fields[i])
		if err != nil {
			return renderState{}, fmt.Errorf("measuring the window of pane %s: field %d is %q, which is not a number",
				pane, i+1, fields[i])
		}
		*target = value
	}
	if state.zoom, err = parseZoom(pane, fields[2:5]); err != nil {
		return renderState{}, err
	}
	if state.frame, err = parseFrame(pane, strings.Join(fields[5:], "\t")); err != nil {
		return renderState{}, err
	}
	return state, nil
}

// ZoomPane makes one pane fill its window.
//
// It is how the dashboard shows an agent at the width of the region rather than
// at its share of a split window. A task that has opened a shell holds two panes
// side by side, and a window sized to the region then gives the agent half of
// it.
//
// Zoom rather than arithmetic on the layout, because tmux already has the
// concept and doing it by hand means guessing a split ratio. It is display-only:
// the other pane keeps running, and nothing about the window's identity or its
// @feat_* metadata changes.
//
// A user who attaches gets the window unzoomed, which UnzoomWindow does as the
// size is released, so a shell opened beside an agent is still there when they
// look at the real terminal.
func (t *Tmux) ZoomPane(ctx context.Context, pane string) error {
	return t.zoomPane(ctx, pane)
}

func (t *Tmux) zoomPane(ctx context.Context, pane string) error {
	if !paneID.MatchString(pane) {
		return fmt.Errorf("zooming needs a tmux pane identifier, but got %q", pane)
	}

	measured, err := t.runner.Run(ctx, t.socket, "display-message", "-p", "-t", pane, zoomFormat)
	if err != nil {
		return fmt.Errorf("reading the zoom of pane %s: %w", pane, err)
	}
	fields := strings.Split(strings.TrimRight(measured, "\n"), "\t")
	if len(fields) != 3 {
		return fmt.Errorf("reading the zoom of pane %s returned %q", pane, measured)
	}
	state, err := parseZoom(pane, fields)
	if err != nil {
		return err
	}

	_, err = t.applyZoom(ctx, pane, state)
	return err
}

// zoomState is what a zoom decision rests on.
type zoomState struct {
	zoomed, active bool
	panes          int
}

// parseZoom reads the three fields of zoomFormat.
func parseZoom(pane string, fields []string) (zoomState, error) {
	panes, err := strconv.Atoi(fields[2])
	if err != nil {
		return zoomState{}, fmt.Errorf("reading the zoom of pane %s: %q panes is not a number", pane, fields[2])
	}
	return zoomState{zoomed: fields[0] == "1", active: fields[1] == "1", panes: panes}, nil
}

// applyZoom makes the pane the one filling its window, and reports whether it
// had to change anything to do so.
func (t *Tmux) applyZoom(ctx context.Context, pane string, state zoomState) (bool, error) {
	// A window with one pane is already the whole window, and zooming it would
	// be a state change with nothing to show for it.
	if state.panes == 1 {
		return false, nil
	}
	if state.zoomed && state.active {
		return false, nil
	}
	// Another pane is zoomed. Zoom toggles, so that one is released first rather
	// than toggled into an unzoomed window by the call meant to zoom ours.
	if state.zoomed {
		if _, err := t.runner.Run(ctx, t.socket, "resize-pane", "-Z", "-t", pane); err != nil {
			return false, fmt.Errorf("releasing the zoom before zooming pane %s: %w", pane, err)
		}
	}
	if _, err := t.runner.Run(ctx, t.socket, "resize-pane", "-Z", "-t", pane); err != nil {
		return false, fmt.Errorf("zooming pane %s: %w", pane, err)
	}
	return true, nil
}

// UnzoomWindow returns a window to showing every pane it has.
func (t *Tmux) UnzoomWindow(ctx context.Context, window string) error {
	if !windowID.MatchString(window) {
		return fmt.Errorf("unzooming needs a tmux window identifier, but got %q", window)
	}

	zoomed, err := t.runner.Run(ctx, t.socket, "display-message", "-p", "-t", window,
		"#{window_zoomed_flag}")
	if err != nil {
		return fmt.Errorf("reading the zoom of window %s: %w", window, err)
	}
	if strings.TrimSpace(zoomed) != "1" {
		return nil
	}

	if _, err := t.runner.Run(ctx, t.socket, "resize-pane", "-Z", "-t", window); err != nil {
		return fmt.Errorf("unzooming window %s: %w", window, err)
	}
	return nil
}

// ReleaseWindowSize returns a window to the size its own clients ask for.
//
// It is the other half of ResizeWindow and exists because of what that one does
// to a native attach. Sizing a window pins it: tmux sets window-size to manual —
// with or without being asked — and then keeps the window at that size however
// large the terminal attaching to it is, leaving the rest of the screen blank.
// A user who rendered a pane in the dashboard and then attached to it got a
// terminal the size of the dashboard's main region.
//
// Unsetting the option is the whole of it, and resizing here would undo it.
// Measured against tmux 3.5a with a real client attached in a pty:
//
//	release             window before attach   what a 200x50 client then sees
//	-u window-size      87x21 (still pinned)   200x49
//	-u then -A          80x24                  80x24
//
// -A re-sets window-size to manual, which pins the window again at the server's
// default size — smaller than what it was trying to undo. The window keeps its
// pinned size here only until a client arrives, and nothing is looking at it in
// the meantime.
//
// Unsetting rather than setting a value restores the user's own preference:
// ADR-030 has Feat load their normal configuration, so a global window-size of
// largest stays largest.
func (t *Tmux) ReleaseWindowSize(ctx context.Context, window string) error {
	if !windowID.MatchString(window) {
		return fmt.Errorf("releasing a size needs a tmux window identifier, but got %q", window)
	}

	if _, err := t.runner.Run(ctx, t.socket,
		"set-window-option", "-u", "-t", window, "window-size"); err != nil {
		return fmt.Errorf("returning window %s to automatic sizing: %w", window, err)
	}
	return nil
}

// ResizeWindow sets a window's size in cells.
//
// A pane rendered into a region of a different size wraps its own output at the
// wrong column, so the program's idea of the width has to be told rather than
// inferred. window-size manual stops tmux resizing the window back to fit
// whichever client is attached, which is the setting that makes the size Feat
// asks for the size it gets. tmux sets it implicitly on any resize-window that
// names a size; it is set here as well so that the pinning is visible at the
// call site rather than being a side effect, and so that ReleaseWindowSize has
// something it is plainly the opposite of.
//
// Resizing to the size a window already has is not the no-op it looks like from
// tmux's side: see RenderPane for what it does to a zoomed pane's pty. Callers
// should ask only when the size has changed.
func (t *Tmux) ResizeWindow(ctx context.Context, window string, width, height int) error {
	return t.resizeWindow(ctx, window, width, height)
}

func (t *Tmux) resizeWindow(ctx context.Context, window string, width, height int) error {
	if !windowID.MatchString(window) {
		return fmt.Errorf("resizing needs a tmux window identifier, but got %q", window)
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("resizing window %s to %dx%d: both must be positive", window, width, height)
	}

	if _, err := t.runner.Run(ctx, t.socket,
		"set-window-option", "-t", window, "window-size", "manual"); err != nil {
		return fmt.Errorf("taking manual control of window %s: %w", window, err)
	}
	if _, err := t.runner.Run(ctx, t.socket, "resize-window", "-t", window,
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height)); err != nil {
		return fmt.Errorf("resizing window %s: %w", window, err)
	}
	return nil
}
