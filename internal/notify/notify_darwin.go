package notify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// osascriptProgram is macOS's own script runner, given as an absolute path: a
// daemon started in the background does not inherit an interactive shell's PATH.
const osascriptProgram = "/usr/bin/osascript"

// notifyTimeout bounds one delivery. Measured at 141 ms on the target machine,
// so anything near this is a system that has stopped answering.
const notifyTimeout = 10 * time.Second

// script is the AppleScript that shows a notification.
//
// The text is passed as arguments and read out of argv rather than interpolated
// into the script. That is the same rule CLAUDE.md states for Git, tmux, and
// Docker Compose, and it matters here for the same reason: a task title is text
// the user typed, and a title containing a quotation mark would otherwise end
// the string it was pasted into and leave the rest of it to be executed.
var script = []string{
	"on run argv",
	"display notification (item 2 of argv) with title (item 1 of argv)",
	"end run",
}

// Desktop delivers through macOS's notification centre.
type Desktop struct{}

var _ Notifier = Desktop{}

// Host returns the notifier for this platform.
func Host() Notifier { return Desktop{} }

// Available reports whether this machine can deliver a notification.
//
// It answers whether Feat can hand one over, which is not the same as whether
// the user will see it: macOS decides that per application, an unauthorised
// notification is dropped without saying so, and osascript exits 0 either way.
// Feat therefore reports that it delivered a notification and never that one was
// seen.
func (Desktop) Available() (bool, string) {
	if _, err := os.Stat(osascriptProgram); err != nil {
		return false, "desktop notifications need " + osascriptProgram + ", which this machine does not have"
	}
	return true, ""
}

// Notify shows one notification.
func (Desktop) Notify(ctx context.Context, notification Notification) error {
	if err := safeArgument("title", notification.Title); err != nil {
		return err
	}
	if err := safeArgument("body", notification.Body); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()

	arguments := make([]string, 0, 2*len(script)+2)
	for _, line := range script {
		arguments = append(arguments, "-e", line)
	}
	arguments = append(arguments, notification.Title, notification.Body)

	command := exec.CommandContext(ctx, osascriptProgram, arguments...)
	command.Stdin = nil
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output

	if err := command.Run(); err != nil {
		if reported := strings.TrimSpace(output.String()); reported != "" {
			return fmt.Errorf("delivering a desktop notification: %s", reported)
		}
		return fmt.Errorf("delivering a desktop notification: %w", err)
	}
	return nil
}

// safeArgument refuses text osascript would read as one of its own options, or
// that would end the line it is written on.
func safeArgument(field, value string) error {
	if value == "" {
		return fmt.Errorf("a notification %s must not be empty", field)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("a notification %s must not begin with %q, but %q does", field, "-", value)
	}
	if strings.ContainsAny(value, "\x00\n\r") {
		return fmt.Errorf("a notification %s must not contain a NUL or a newline, but %q does", field, value)
	}
	return nil
}
