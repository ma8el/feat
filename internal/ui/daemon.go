package ui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
)

// startDaemonKey offers to start a daemon when none is answering. It is a
// shifted letter because the plain one opens the selected task's shell, and
// because this is the dashboard repairing its own connection rather than acting
// on a task.
const startDaemonKey = "S"

// What the dialog's prose is folded into. The widest is a measure rather than
// an allowance, because the eye loses its place returning to the start of a
// line a hundred and twenty cells long. Below the narrowest, folding gives one
// word per line, which is worse than a line that overruns.
const (
	daemonBodyNarrowest = 24
	daemonBodyWidest    = 60
)

// daemonGone reports whether a failure means nothing is listening on the
// daemon's socket. It is the one transport failure the dashboard can do
// something about, which is why internal/client separates it; everything else
// is shown and not acted on.
func daemonGone(err error) bool { return errors.Is(err, client.ErrDaemonNotRunning) }

// noDaemonError is what the footer shows while no daemon is answering. It names
// the key rather than the command, because `feat daemon start` is advice a user
// inside a full-screen dashboard would have to quit to take.
//
// The key comes before the socket because the footer is one line and cuts what
// does not fit: a runtime directory under $TMPDIR runs to ninety characters,
// which put the whole of "press S to start one" past the ellipsis. The socket
// is named in full in the dialog and by `feat daemon status`.
type noDaemonError struct {
	// Socket is where a daemon would be listening.
	Socket string
}

func (e *noDaemonError) Error() string {
	message := "no feat daemon is listening; press " + startDaemonKey + " to start one"
	if e.Socket != "" {
		message += " — " + e.Socket
	}
	return message
}

// daemonStartedMsg reports what came of an attempt to start a daemon.
type daemonStartedMsg struct{ err error }

// startDaemon asks the adapter to start a daemon and wait until it answers. The
// dashboard does not start it itself: internal/ui may not import
// internal/daemon, and spawning a process is an adapter's work (ADR-031).
func (m Model) startDaemon() tea.Cmd {
	backend := m.backend
	return func() tea.Msg {
		return daemonStartedMsg{err: backend.StartDaemon(context.Background())}
	}
}

// noteDaemonGone records that nothing is answering, and asks once whether to
// start a daemon. Once, because the read that discovers this runs every two
// seconds and a dialog reopening on each of them could not be dismissed. After
// a no the footer carries the key, and the question is put again only once a
// daemon has answered since.
//
// It never takes the keyboard from an overlay that is already open: closing
// that one would return the user to the tab underneath rather than to what they
// were doing. The error is set either way, so the next refresh after they close
// it asks.
func (m Model) noteDaemonGone() (tea.Model, tea.Cmd) {
	m.daemonGone = true
	m.err = &noDaemonError{Socket: m.daemon.Socket}

	if m.daemonAsked || !m.screen.mainRegion() {
		return m, nil
	}
	// A question waiting for a yes is dropped, because a stop and a cancel are
	// both requests to a daemon that is not there. The dialog takes the keyboard,
	// so the key that clears a confirmation never arrives, and the first `y`
	// after it closed would stop an agent the user asked about minutes earlier.
	m.stopping, m.cancelling = "", ""

	m.daemonAsked = true
	m.rememberTab()
	m.screen = screenDaemon
	return m, nil
}

// noteDaemonAnswered records a read that succeeded. It clears the memory of the
// last outage as well as its state, so a daemon going away again asks rather
// than holding the user to a no they gave about a daemon that has since come
// back.
func (m *Model) noteDaemonAnswered() {
	m.daemonGone, m.daemonAsked, m.daemonErr = false, false, nil
}

// applyDaemonStart applies the result of a start the user asked for.
func (m Model) applyDaemonStart(message daemonStartedMsg) (tea.Model, tea.Cmd) {
	m.daemonStarting = false
	if message.err != nil {
		// Kept in the dialog rather than put in the footer: the footer flattens
		// an error to one line, and what a failed start has to say is a quoted
		// daemon log.
		m.daemonErr = message.err
		return m, nil
	}

	m.daemonErr = nil
	m.err = nil
	m.noteDaemonAnswered()
	m.screen = screenFor(m.tab)
	m.status = "a daemon was started; reading state again"

	commands := []tea.Cmd{m.load(), m.loadResources(), m.loadReconciliation()}
	if m.streamEnded {
		// A new channel, because connect closes the one it was given on its way
		// out and a closed channel delivers nothing. This is not the automatic
		// reconnection ADR-027 declined: it follows a key the user pressed and
		// happens once, so nothing here decides how often to retry.
		m.streamEnded = false
		m.events = make(chan api.Event, eventBuffer)
		commands = append(commands, m.connect(), m.awaitEvent())
	}
	return m, tea.Batch(commands...)
}

// daemonKey answers the keys of the daemon dialog, and only its own. Every key
// on the dashboard behind it reaches a daemon that is not there, so the frame's
// movement keys are not passed through (ADR-041).
func (m Model) daemonKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.daemonStarting {
		// Nothing but quitting, while a start is in flight. A second yes would
		// spawn a second daemon, and the answer to the first has not arrived.
		if pressed := key.String(); pressed == "ctrl+c" || pressed == "q" {
			return m.quit()
		}
		return m, nil
	}

	switch key.String() {
	case "ctrl+c", "q":
		return m.quit()

	case "y", "Y", "enter":
		m.daemonStarting = true
		m.daemonErr = nil
		return m, m.startDaemon()

	case "n", "N", "esc":
		m.screen = screenFor(m.tab)
		return m, nil
	}
	return m, nil
}

// offerDaemonStart is what the S key does. It reopens the question after a no,
// which is the reason the key exists: the dashboard asks once, and this is how
// a user who declined changes their mind without quitting.
func (m Model) offerDaemonStart() (tea.Model, tea.Cmd) {
	if !m.daemonGone {
		// Said rather than done: the daemon itself refuses a second one, and a key
		// that appeared to do nothing would say less than one that explains.
		m.status = "a daemon is answering; there is nothing to start"
		return m, nil
	}
	m.rememberTab()
	m.daemonErr = nil
	m.screen = screenDaemon
	return m, nil
}

// daemonTitle is the dialog's heading. It is a warning where the other dialogs
// are labels, because it is the only one that opens because something is wrong
// rather than because the user asked.
const daemonTitle = "Warning: No running daemon"

// daemonHints are the keys of the dialog.
func (m Model) daemonHints() string {
	if m.daemonStarting {
		return keyHints(keyHint("q", "quit"))
	}
	if m.daemonErr != nil {
		return keyHints(keyHint("y", "try again"), keyHint("n", "leave it stopped"))
	}
	return keyHints(keyHint("y", "start a daemon"), keyHint("n", "leave it stopped"))
}

// foldProse wraps prose to a readable measure and takes back the padding
// lipgloss adds. It is named for prose because a project in the rail folds
// away, and `fold` in a footer hint means that.
//
// dialogBox shrinks its box to the widest line it is handed, and lipgloss pads
// every line out to the width it wrapped to, so a body that kept that padding
// reported itself as exactly as wide as it was allowed and the shrink could
// never fire.
func foldProse(text string, width int) string {
	measure := min(max(daemonBodyNarrowest, width), daemonBodyWidest)
	wrapped := lipgloss.NewStyle().Width(measure).Render(text)

	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// daemonBody is what the dialog says. The prose is folded here rather than left
// to the box, which composites by cell and truncates, and these sentences have
// to be read in full.
func (m Model) daemonBody(width int) string {
	fold := func(text string) string { return foldProse(text, width) }

	var out strings.Builder
	out.WriteString(fold(m.daemonSituation()))

	if m.daemonStarting {
		out.WriteString("\n\n" + mutedStyle.Render(fold(
			"Starting one and waiting for it to answer…")))
		return out.String()
	}

	if m.daemonErr != nil {
		out.WriteString("\n\n" + failureStyle.Render(fold("Starting one did not work:")))
		// Cleaned of anything that would move the cursor, because the end of a
		// daemon log is text Feat did not write and the dialog is laid out by
		// counting cells.
		out.WriteString("\n" + fold(plainText(m.daemonErr.Error())))
		out.WriteString("\n\n" + fold("Try again?"))
		return out.String()
	}

	// One line, because the title has already said what is wrong and the keys
	// below say what to do about it. What is left is the fact neither of those
	// carries: the dashboard behind this dialog is not live.
	out.WriteString("\n\n" + mutedStyle.Render(fold(
		"Without one the dashboard is not usable, and what it lists is no longer current.")))
	out.WriteString("\n\n" + fold("Start one now?"))
	return out.String()
}

// daemonSituation is the first line of the dialog. It carries the socket, which
// the title cannot, and does not repeat "no feat daemon" from the line above
// it.
func (m Model) daemonSituation() string {
	if m.daemon.Socket == "" {
		return "No daemon is listening."
	}
	return "No daemon is listening on " + m.daemon.Socket + "."
}
