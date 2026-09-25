package tmux

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// paneID and windowID are tmux's immutable object identifiers. Every operation
// here takes one rather than a name or an index, because a user's configuration
// may rename or renumber anything (ADR-030).
var (
	paneID   = regexp.MustCompile(`^%[0-9]+$`)
	windowID = regexp.MustCompile(`^@[0-9]+$`)
)

// inputBuffer is the tmux buffer Feat pastes through. It is named rather than
// on the anonymous stack, so paste-buffer -d deletes this one and leaves what
// the user has copied.
const inputBuffer = "feat-input"

// frameFormat is what one query returns beside the pane's content.
const frameFormat = "#{pane_width}\t#{pane_height}\t#{cursor_x}\t#{cursor_y}\t#{pane_dead}"

// zoomFormat is what a zoom decision needs to know.
const zoomFormat = "#{window_zoomed_flag}\t#{pane_active}\t#{window_panes}"

// frozenFormat asks whether any pane of the window has stopped.
//
// It loops over the window's panes rather than asking about the one being
// drawn, because a resize reflows every pane in the window, including the ones
// the dashboard is not showing. tmux renders one character per pane, so a "1"
// anywhere in the answer is a pane that will never repaint what a resize takes
// apart. Anything else is read as nothing frozen, including a tmux too old to
// know the loop.
const frozenFormat = "#{P:#{pane_dead}}"

// pinnedFormat asks whether Feat's own sizing is still on the window.
//
// tmux exposes an option's value under its own name, so this rides along with
// the measurements at no extra invocation. The value is the effective one, so a
// user whose configuration sets window-size manual globally always reads as
// pinned, and the release below then has nothing of Feat's to take off.
const pinnedFormat = "#{window-size}"

// renderFormat is everything RenderPane needs, in one query.
//
// The window's size joins the zoom state and the pane's own measurements, so a
// frame with nothing to change costs two tmux invocations rather than five.
// Each one is a process, and while the pane has the keyboard this runs sixteen
// times a second: measured against tmux 3.7b, 30.5 ms a frame became 16.3 ms.
//
// Fields are appended at the end, as discovery's formats are, so adding an
// observation cannot move the index another one is read from.
const renderFormat = "#{window_width}\t#{window_height}\t" + zoomFormat + "\t" +
	frameFormat + "\t" + frozenFormat + "\t" + pinnedFormat

// The field counts these parsers expect, derived from the formats rather than
// written beside them. A format that gains a field and a parser that keeps the
// old count is a wrong answer on a query every frame makes.
var (
	zoomFields   = fieldCount(zoomFormat)
	frameFields  = fieldCount(frameFormat)
	renderFields = fieldCount(renderFormat)
)

// PaneFrame is one pane as tmux has already drawn it.
//
// Content holds the escape sequences tmux emitted, and Feat passes them through
// without interpreting anything but cell width, which is what places and clips
// the rectangle. Deriving task, agent, attention, or workflow state from these
// bytes is refused by ADR-042 and remains the job of provider hooks.
type PaneFrame struct {
	// Pane is the pane this was captured from.
	Pane string
	// Content is the visible pane, one line per row, with colour intact.
	Content []string
	// Width and Height are the pane's size in cells, which the caller must match
	// for the program's own wrapping to line up with the display.
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
// pane, which a caller drawing into a region that wide then clips, so the
// wrapped part is discarded rather than shown on the row tmux put it on.
// Measured against tmux 3.5a in a 40-cell pane: -J returns one 68-cell line
// where the terminal shows 40 cells and 28. Whether a line is wrapped changes
// as a program redraws, so the text it costs appears and disappears.
//
// Only the visible pane is captured. Scrollback needs -S and -E, and a user who
// wants the real terminal attaches to it (ADR-030).
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
	if len(fields) != frameFields {
		return PaneFrame{}, fmt.Errorf("measuring pane %s returned %d fields, want %d: %q",
			pane, len(fields), frameFields, measured)
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
// a terminator, so a name beginning with a dash cannot be read as a flag. Typed
// text does not come through here; see PasteText.
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
// -l sends the characters literally, which is what a keystroke is. The
// bracketed paste PasteText uses is not: an application in bracketed paste
// mode, which a full-screen agent asks for, is told by the markers that what
// arrives was pasted, and may insert it without the handling a typed character
// goes through. Sending every keystroke that way made ordinary keys behave
// oddly.
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
// Not send-keys, for two measured reasons agent-manager's implementation
// records: send-keys truncates a long string, and an application reading a
// paste without bracketing can consume the trailing newline as a submission the
// user did not make. -p brackets it and -d removes the buffer afterwards, so
// nothing Feat pastes stays in the user's buffer stack.
//
// set-buffer rather than load-buffer, because the buffer's contents arrive as
// an argument and this adapter's Runner passes argument vectors rather than
// standard input.
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
// Nothing is changed that is already as it should be, which is correctness
// rather than economy. Resizing a zoomed window sets the zoomed pane's pty to
// the size it would have unzoomed and then back again, so a full-screen program
// repaints at its share of a split and repaints again at the window's width.
// Sizing on every poll made an agent flicker between the region's width and
// half of it. Measured against tmux 3.7b, with stty read inside the pane of a
// zoomed two-pane window sized 179x52:
//
//	left alone                 resize-window to the same 179x52
//	52 179                     52 90   <- the program is told the unzoomed share
//	52 179                     52 179
//
// tmux reports pane_width as 179 throughout, which is why sampling the window
// from outside shows a state that never moves while the display flickers.
//
// A window somebody is attached to is neither sized nor zoomed, so a rendering
// never resizes a real client's terminal. It gets the opposite operation
// instead: Feat's pin comes off it, whichever way the client reached the
// window. See ResizeWindow for what the pin does to a client. Measured against
// tmux 3.7b, a window pinned at 171x49 with a 200x60 client attached:
//
//	left alone          -u window-size
//	171x49              200x60
//
// The release is one-way. Nothing here pins a window a client owns, so an
// already unpinned window costs nothing and the option is unset once per attach
// rather than once per frame.
//
// A window holding a pane whose program has ended is released like any other,
// even though a resize takes a stopped pane's screen apart (frozenSize). The
// client is the one resizing it, and pinning a window against the user attached
// to it would leave them looking at a window their terminal does not fit.
func (t *Tmux) RenderPane(ctx context.Context, window, pane string, width, height int, watched bool) (PaneFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !windowID.MatchString(window) {
		return PaneFrame{}, fmt.Errorf("rendering needs a tmux window identifier, but got %q", window)
	}
	state, err := t.renderState(ctx, pane)
	if err != nil {
		return PaneFrame{}, err
	}

	changed := false
	if watched {
		if state.pinned {
			if err := t.releaseWindowSize(ctx, window); err != nil {
				return PaneFrame{}, err
			}
			changed = true
		}
	} else {
		if state.frozen {
			width, height = frozenSize(state, width, height)
		}
		if state.windowWidth != width || state.windowHeight != height {
			if err := t.resizeWindow(ctx, window, width, height); err != nil {
				return PaneFrame{}, err
			}
			changed = true
		}
		zoomed, err := t.applyZoom(ctx, pane, state.zoom)
		if err != nil {
			return PaneFrame{}, err
		}
		changed = changed || zoomed
	}

	// The measurement came with the state and stands unless a step above has just
	// invalidated it. Releasing a pin is one of them: tmux resizes the window to
	// its client the moment the option comes off, so the pane the state measured
	// is no longer the size it reported.
	frame := state.frame
	if changed {
		if frame, err = t.measurePane(ctx, pane); err != nil {
			return PaneFrame{}, err
		}
	}
	return t.captureInto(ctx, frame)
}

// frozenSize is the size a window holding a stopped pane may be given: this one
// or the one it has, whichever is larger.
//
// A resize is a request to repaint, and a pane whose program has ended cannot
// answer it. tmux reflows the screen it stopped on instead. The last thing the
// program drew then comes apart at the new width and stays that way for as long
// as the user keeps the task (ADR-030). Measured against tmux 3.5a, a dead pane
// holding a full-width prompt and sized from 20 columns to 14:
//
//	20 columns              14 columns
//	│ > type here      │    │ > type here
//	╰──────────────────╯         │
//	                        ╰─────────────
//	                        ─────╯
//
// Growing one is allowed and is the repair: the same measurement back at 20
// columns returns the box whole, because tmux rejoins exactly the rows it
// split. The rule is therefore one-directional rather than "never resize a dead
// pane", and a pane a narrower region took apart comes back when there is room.
//
// What the region cannot fit is then wider or taller than the region, and the
// renderer clips it, as it already does for a window a native client owns.
func frozenSize(state renderState, width, height int) (int, int) {
	return max(width, state.windowWidth), max(height, state.windowHeight)
}

// renderState is a pane and the window around it, as one measurement.
type renderState struct {
	windowWidth, windowHeight int
	zoom                      zoomState
	frame                     PaneFrame
	// frozen reports that some pane of this window has stopped, and that a
	// resize would therefore reflow a screen nothing will repaint.
	frozen bool
	// pinned reports that the window is held at a size of its own rather than at
	// whichever client's, which is what rendering it leaves behind.
	pinned bool
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
	if len(fields) != renderFields {
		return renderState{}, fmt.Errorf(
			"measuring pane %s and its window returned %d fields, want %d: %q",
			pane, len(fields), renderFields, measured)
	}

	// Where each format's fields begin, counted from the ones before it rather
	// than written down, so a format gaining a field moves every later index.
	const sizeFields = 2
	zoomAt := sizeFields
	frameAt := zoomAt + zoomFields
	frozenAt := frameAt + frameFields
	pinnedAt := frozenAt + 1

	state := renderState{}
	for i, target := range []*int{&state.windowWidth, &state.windowHeight} {
		value, err := strconv.Atoi(fields[i])
		if err != nil {
			return renderState{}, fmt.Errorf("measuring the window of pane %s: field %d is %q, which is not a number",
				pane, i+1, fields[i])
		}
		*target = value
	}
	if state.zoom, err = parseZoom(pane, fields[zoomAt:frameAt]); err != nil {
		return renderState{}, err
	}
	if state.frame, err = parseFrame(pane, strings.Join(fields[frameAt:frozenAt], "\t")); err != nil {
		return renderState{}, err
	}
	// One character per pane, so a stopped pane anywhere in the window is a "1"
	// anywhere in the field. Nothing is parsed out of it, because which pane
	// stopped is not a question a resize decision asks.
	state.frozen = strings.Contains(fields[frozenAt], "1")
	// Anything other than the pinning tmux applies is left alone. A tmux too old
	// to answer for an option in a format returns an empty field, and the window
	// is then read as unpinned.
	state.pinned = fields[pinnedAt] == manualSize
	return state, nil
}

// ZoomPane makes one pane fill its window.
//
// It is how the dashboard shows an agent at the width of the region rather than
// at its share of a split window. A task that has opened a shell holds two
// panes side by side, and a window sized to the region then gives the agent
// half of it.
//
// Zoom rather than arithmetic on the layout, because tmux already has the
// concept and doing it by hand means guessing a split ratio. It is display
// only: the other pane keeps running, and nothing about the window's identity
// or its @feat_* metadata changes.
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
	// A window with one pane is already the whole window, so zooming it would be
	// a state change with nothing to show for it.
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

// ReleaseWindowSize returns a window to the size its own clients ask for. It is
// the other half of ResizeWindow, and undoes the pin that one applies.
//
// Unsetting the option is the whole of it, and resizing here would undo it.
// Measured against tmux 3.5a with a real client attached in a pty:
//
//	release             window before attach   what a 200x50 client then sees
//	-u window-size      87x21 (still pinned)   200x49
//	-u then -A          80x24                  80x24
//
// -A re-sets window-size to manual, which pins the window again at the server's
// default size, smaller than what it was trying to undo. The window keeps its
// pinned size only until a client arrives, and nothing is looking at it in the
// meantime.
//
// Unsetting rather than setting a value restores the user's own preference,
// because ADR-030 has Feat load their normal configuration, so a global
// window-size of largest stays largest.
//
// Releasing is not on its own enough to keep an attach correct. The other half
// is in RenderPane: a poll landing between this release and the client arriving
// would size the window again, while tmux still reports nobody attached.
func (t *Tmux) ReleaseWindowSize(ctx context.Context, window string) error {
	if !windowID.MatchString(window) {
		return fmt.Errorf("releasing a size needs a tmux window identifier, but got %q", window)
	}
	return t.releaseWindowSize(ctx, window)
}

func (t *Tmux) releaseWindowSize(ctx context.Context, window string) error {
	if _, err := t.runner.Run(ctx, t.socket,
		"set-window-option", "-u", "-t", window, sizeOption); err != nil {
		return fmt.Errorf("returning window %s to automatic sizing: %w", window, err)
	}
	return nil
}

// sizeOption is the window option that decides who a window is sized for, and
// manualSize is the value that makes it Feat.
const (
	sizeOption = "window-size"
	manualSize = "manual"
)

// ResizeWindow sets a window's size in cells.
//
// A pane rendered into a region of a different size wraps its own output at the
// wrong column, so the program's idea of the width has to be told rather than
// inferred. window-size manual stops tmux resizing the window back to fit
// whichever client is attached. tmux sets it implicitly on any resize-window
// that names a size, and setting it here makes the pinning visible at the call
// site and gives ReleaseWindowSize something it is plainly the opposite of.
//
// This is what pinning costs, and why the release exists. tmux holds the window
// at that size however large the terminal attaching to it is. A user who
// rendered a pane and then attached got a terminal the size of the dashboard's
// main region, with the rest of the screen filled in with dots.
//
// Resizing to the size a window already has is not the no-op it looks like from
// tmux's side; see RenderPane for what it does to a zoomed pane's pty. Callers
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
		"set-window-option", "-t", window, sizeOption, manualSize); err != nil {
		return fmt.Errorf("taking manual control of window %s: %w", window, err)
	}
	if _, err := t.runner.Run(ctx, t.socket, "resize-window", "-t", window,
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height)); err != nil {
		return fmt.Errorf("resizing window %s: %w", window, err)
	}
	return nil
}
