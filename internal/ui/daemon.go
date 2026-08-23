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

// startDaemonKey offers to start a daemon when none is answering.
//
// It is a shifted letter because the plain one opens the selected task's shell,
// and because this is not an action on a task: it is the dashboard repairing its
// own connection to the thing that answers every other key.
const startDaemonKey = "S"

// daemonBodyNarrowest is the width the dialog's prose is folded into when the
// caller allows less. Below it a sentence is one word per line, which is worse
// than a line that overruns.
const daemonBodyNarrowest = 24

// daemonGone reports whether a failure means nothing is listening on the
// daemon's socket.
//
// It is the one transport failure the dashboard can do something about, which is
// why internal/client separates it from every other one. Everything else — a
// refused request, a daemon that answered with an error — is shown and not acted
// on.
func daemonGone(err error) bool { return errors.Is(err, client.ErrDaemonNotRunning) }

// noDaemonError is what the footer shows while no daemon is answering.
//
// It names the key rather than the command, because the user is inside a
// full-screen dashboard: `feat daemon start` is advice they would have to quit to
// take, and quitting is the thing this whole path exists to avoid. The command is
// still what `feat daemon status` says, where there is a shell to type it in.
//
// The key comes before the socket because the footer is one line and cuts what
// does not fit. A runtime directory under $TMPDIR is ninety characters on this
// machine, which put the whole of "press S to start one" past the ellipsis: the
// half a user can act on was the half being dropped. The socket is named in the
// dialog, in full and wrapped, and by `feat daemon status`.
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

// startDaemon asks the adapter to start a daemon and wait until it answers.
//
// The dashboard does not start it itself: internal/ui may not import
// internal/daemon, and spawning a process is what an adapter does (ADR-031). What
// happens here is a question and an answer, like every other backend call.
func (m Model) startDaemon() tea.Cmd {
	backend := m.backend
	return func() tea.Msg {
		return daemonStartedMsg{err: backend.StartDaemon(context.Background())}
	}
}

// noteDaemonGone records that nothing is answering, and asks once whether to
// start a daemon.
//
// Once, not on every refresh. The read that discovers this runs every two
// seconds, and a dialog that reopened on each of them would be one the user
// cannot dismiss. After a no, the footer carries the key instead, and the
// question is put again only when a daemon has answered since — which is what
// makes the next outage a new one rather than the same one still being refused.
//
// It never takes the keyboard from an overlay that is already open. A user
// halfway through a task brief or a cleanup confirmation is answering a question
// of their own, and closing this one would return them to the tab underneath
// rather than to what they were doing. The error is set either way, so the next
// refresh after they close it asks.
func (m Model) noteDaemonGone() (tea.Model, tea.Cmd) {
	m.daemonGone = true
	m.err = &noDaemonError{Socket: m.daemon.Socket}

	if m.daemonAsked || !m.screen.mainRegion() {
		return m, nil
	}
	// A question that was waiting for a yes is dropped, because nothing could
	// carry it out: a stop and a cancel are both requests to a daemon that is not
	// there. Leaving one pending would leave its yes pending too — the dialog
	// takes the keyboard, so the key that clears a confirmation never arrives —
	// and the first `y` after this dialog closed would stop an agent the user
	// had stopped asking about several minutes earlier.
	m.stopping, m.cancelling = "", ""

	m.daemonAsked = true
	m.rememberTab()
	m.screen = screenDaemon
	return m, nil
}

// noteDaemonAnswered records a read that succeeded.
//
// It clears the memory of the last outage as well as the state of it, so that a
// daemon going away again asks rather than assuming the user's earlier no still
// stands. They answered about a daemon that has since come back.
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
		// out and a closed channel delivers nothing.
		//
		// Reopening the stream here is not the automatic reconnection ADR-027
		// declined. That one had to decide how often to retry; this follows a key
		// the user pressed, happens once, and reopens the subscription the daemon
		// that just started has no record of.
		m.streamEnded = false
		m.events = make(chan api.Event, eventBuffer)
		commands = append(commands, m.connect(), m.awaitEvent())
	}
	return m, tea.Batch(commands...)
}

// daemonKey answers the keys of the daemon dialog.
//
// It answers only its own. The dialog is a question that has to be answered
// before the dashboard behind it means anything — every key on it reaches a
// daemon that is not there — so the frame's movement keys are not passed
// through, which is the rule ADR-041 gives for an overlay.
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

// offerDaemonStart is what the S key does.
//
// It reopens the question after a no, which is the whole reason the key exists:
// the dashboard asks once, and this is how a user who declined changes their
// mind without quitting.
func (m Model) offerDaemonStart() (tea.Model, tea.Cmd) {
	if !m.daemonGone {
		// Said rather than done. Starting a second daemon is refused by the
		// daemon itself, and a key that appeared to do nothing would be worse
		// than one that says why.
		m.status = "a daemon is answering; there is nothing to start"
		return m, nil
	}
	m.rememberTab()
	m.daemonErr = nil
	m.screen = screenDaemon
	return m, nil
}

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

// daemonBody is what the dialog says.
//
// The prose is folded here rather than left to the box, which composites by cell
// and truncates: a sentence that has to be read in full is the one thing on this
// screen that must not end in an ellipsis.
func (m Model) daemonBody(width int) string {
	fold := lipgloss.NewStyle().Width(max(daemonBodyNarrowest, width)).Render

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

	out.WriteString("\n\n" + mutedStyle.Render(fold(
		"It may have been stopped deliberately, or it may have failed. Either way "+
			"nothing it was supervising has been touched: worktrees, containers, and "+
			"tmux sessions are where they were. What is listed behind this dialog is "+
			"what was true when it stopped answering.")))
	out.WriteString("\n\n" + fold("Start one now?"))
	return out.String()
}

// daemonSituation is the first line of the dialog, which names the socket when
// there is one to name.
func (m Model) daemonSituation() string {
	if m.daemon.Socket == "" {
		return "No feat daemon is listening."
	}
	return "No feat daemon is listening on " + m.daemon.Socket + "."
}
