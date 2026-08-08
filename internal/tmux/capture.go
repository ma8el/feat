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
// interprets the program's output, and maintains the screen; -e asks for that
// screen with its colour attributes and -J joins a line tmux wrapped, so that a
// caller measuring width sees what the terminal shows.
//
// Only the visible pane is captured. Scrollback needs -S and -E, and a user who
// wants the real terminal attaches to it, which ADR-030 left unchanged.
func (t *Tmux) CapturePane(ctx context.Context, pane string) (PaneFrame, error) {
	if !paneID.MatchString(pane) {
		return PaneFrame{}, fmt.Errorf("capturing a pane needs a tmux pane identifier, but got %q", pane)
	}

	measured, err := t.runner.Run(ctx, t.socket, "display-message", "-p", "-t", pane, frameFormat)
	if err != nil {
		return PaneFrame{}, fmt.Errorf("measuring pane %s: %w", pane, err)
	}
	frame, err := parseFrame(pane, measured)
	if err != nil {
		return PaneFrame{}, err
	}

	content, err := t.runner.Run(ctx, t.socket, "capture-pane", "-p", "-e", "-J", "-t", pane)
	if err != nil {
		return PaneFrame{}, fmt.Errorf("capturing pane %s: %w", pane, err)
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

// PasteText delivers typed text to a pane through a buffer.
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

// ResizeWindow sets a window's size in cells.
//
// A pane rendered into a region of a different size wraps its own output at the
// wrong column, so the program's idea of the width has to be told rather than
// inferred. window-size manual stops tmux resizing the window back to fit
// whichever client is attached, which is the setting that makes the size Feat
// asks for the size it gets.
func (t *Tmux) ResizeWindow(ctx context.Context, window string, width, height int) error {
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
