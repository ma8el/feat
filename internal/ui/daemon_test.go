package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
)

// gone is what a read fails with when nothing is listening on the socket. It is
// wrapped, as the real client wraps it, so that the dashboard is tested through
// errors.Is rather than through an equality that the transport never produces.
func gone() error {
	return fmt.Errorf("%w on /run/feat/feat.sock", client.ErrDaemonNotRunning)
}

// stopped is a dashboard whose daemon has just stopped answering: the fixture
// task was read once, and the read after it failed.
func stopped(t *testing.T, backend *fakeBackend) Model {
	t.Helper()

	model := dashboard(backend, liveTask())
	backend.tasksErr = gone()

	updated, _ := model.Update(tasksMsg{err: backend.tasksErr})
	return updated.(Model)
}

// refresh is the periodic read, which is what discovers an absent daemon.
func refresh(t *testing.T, model Model) Model {
	t.Helper()

	updated, cmd := model.Update(tickMsg{})
	next := updated.(Model)
	if cmd == nil {
		return next
	}
	// The tick batches three reads; only the task read decides anything here,
	// and running the batch would apply all of them.
	applied, _ := next.Update(tasksMsg{err: gone()})
	return applied.(Model)
}

// TestALostDaemonAsksBeforeStartingOne is the rule the whole screen exists for.
//
// Starting a background process is not something a dashboard may do because a
// read failed. A daemon that stopped may have been stopped deliberately, and it
// may have died leaving tmux sessions and containers behind that the next one's
// reconciliation pass will report — which is worth seeing rather than papering
// over. So the dashboard asks, and starts nothing until it has been answered.
func TestALostDaemonAsksBeforeStartingOne(t *testing.T) {
	backend := newFakeBackend()
	model := stopped(t, backend)

	if model.screen != screenDaemon {
		t.Errorf("screen = %v, want the daemon dialog", model.screen)
	}
	if backend.daemonStarts != 0 {
		t.Errorf("the dashboard started %d daemons without being asked", backend.daemonStarts)
	}

	body := flowed(model.daemonBody(60))
	for _, want := range []string{
		// Where, which the title cannot carry, and the one consequence that
		// decides the answer: the dashboard behind the dialog is not live.
		"No daemon is listening on /run/feat/feat.sock",
		"the dashboard is not usable",
		"no longer current",
		"Start one now?",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dialog does not say %q:\n%s", want, body)
		}
	}
}

// TestTheFooterSaysItOnceCounts the lines the footer spends on one fact.
//
// Three of its rows can carry the same absence: the error line, the machine note
// beside the worktree, and the rail's own row. The error line is the only one
// that names the key.
func TestTheFooterSaysItOnce(t *testing.T) {
	model := stopped(t, newFakeBackend())
	updated, _ := model.Update(resourcesMsg{err: gone()})
	model = sized(updated.(Model), 120, 40)

	footer := flowed(model.frameFooter(120))
	if occurrences := strings.Count(footer, "no feat daemon is listening"); occurrences != 1 {
		t.Errorf("the footer says the daemon is gone %d times:\n%s", occurrences, footer)
	}
	if !strings.Contains(footer, "press "+startDaemonKey+" to start one") {
		t.Errorf("the footer no longer names the key:\n%s", footer)
	}
}

// TestTheDialogIsSizedToItsContent keeps the box the size of its text.
//
// dialogBox shrinks to the widest line it is handed, and lipgloss pads every
// wrapped line out to the width it wrapped to — so a body folded to what it was
// allowed reported itself as exactly that wide, and a dialog holding two
// sentences took three quarters of the terminal.
func TestTheDialogIsSizedToItsContent(t *testing.T) {
	model := sized(stopped(t, newFakeBackend()), 200, 50)

	drawn, _ := blockSize(model.dialogView())
	allowed, _ := model.dialogLimits()

	if drawn >= allowed {
		t.Errorf("the dialog took all %d cells it was allowed rather than sizing to its text", allowed)
	}
	if ceiling := daemonBodyWidest + dialogChrome; drawn > ceiling {
		t.Errorf("the dialog is %d cells wide, and its prose is folded to %d", drawn, daemonBodyWidest)
	}
}

// TestTheDialogStillFitsANarrowTerminal checks the other end: the measure is a
// ceiling, not a width, so a terminal narrower than it is not overrun.
func TestTheDialogStillFitsANarrowTerminal(t *testing.T) {
	model := sized(stopped(t, newFakeBackend()), 90, 30)

	drawn, _ := blockSize(model.dialogView())
	allowed, _ := model.dialogLimits()

	if drawn > allowed {
		t.Errorf("the dialog is %d cells wide in a box allowed %d", drawn, allowed)
	}
}

// TestTheDialogIsTitledAsAWarning keeps the heading saying what is wrong.
//
// Every other overlay is titled with what the user asked for — "prepare a task",
// "keys", "recovery". This one opens because something broke, and a title that
// read like the others would be the only line on the screen that did not say so.
func TestTheDialogIsTitledAsAWarning(t *testing.T) {
	model := sized(stopped(t, newFakeBackend()), 120, 40)

	drawn := flowed(model.View())
	if !strings.Contains(drawn, daemonTitle) {
		t.Errorf("the dialog is not titled %q:\n%s", daemonTitle, drawn)
	}
}

// TestSayingNoLeavesTheKeyInTheErrorMessage checks the way back.
//
// A user who declines is left on a dashboard where nothing works, and the only
// thing on screen after the dialog closes is the footer. So the footer carries
// the key rather than the command: `feat daemon start` is advice that can only
// be taken by quitting, which is what this whole path exists to avoid.
func TestSayingNoLeavesTheKeyInTheErrorMessage(t *testing.T) {
	backend := newFakeBackend()
	model := press(t, stopped(t, backend), "n")

	if model.screen == screenDaemon {
		t.Fatal("answering no left the dialog open")
	}
	if backend.daemonStarts != 0 {
		t.Errorf("answering no started %d daemons", backend.daemonStarts)
	}
	if model.err == nil {
		t.Fatal("the dashboard reports no failure after refusing to start a daemon")
	}

	message := model.err.Error()
	for _, want := range []string{"no feat daemon is listening", "/run/feat/feat.sock", startDaemonKey} {
		if !strings.Contains(message, want) {
			t.Errorf("the error %q does not mention %q", message, want)
		}
	}
}

// TestTheKeySurvivesTheFooterCut is the same message read where it is drawn.
//
// The footer is one line and truncates. A runtime directory under $TMPDIR is
// ninety characters, and with the socket first the whole of "press S to start
// one" was past the ellipsis: the dashboard said something was wrong and cut off
// the only thing the user could do about it.
func TestTheKeySurvivesTheFooterCut(t *testing.T) {
	long := "/var/folders/kq/2s1n7d9j40q0m3xzcp7hbb2h0000gn/T/feat-501/feat.sock"

	model := press(t, stopped(t, newFakeBackend()), "n")
	model.daemon.Socket = long
	model.err = &noDaemonError{Socket: long}
	model = sized(model, 100, 30)

	footer := flowed(model.frameFooter(100))
	if !strings.Contains(footer, "press "+startDaemonKey+" to start one") {
		t.Errorf("the footer cut the key off a %d-cell socket:\n%s", len(long), footer)
	}
}

// TestTheOfferIsMadeOnceUntilADaemonAnswersAgain keeps the question a question.
//
// The read that discovers an absent daemon runs every two seconds. A dialog that
// reopened on each of them could not be dismissed, and the footer's key would be
// unreachable because the overlay would take the keyboard back before it could
// be pressed.
func TestTheOfferIsMadeOnceUntilADaemonAnswersAgain(t *testing.T) {
	backend := newFakeBackend()
	model := press(t, stopped(t, backend), "n")

	for range 3 {
		model = refresh(t, model)
		if model.screen == screenDaemon {
			t.Fatal("the dialog reopened on a refresh after the user declined")
		}
	}

	// A daemon that came back and went away again is a new situation, and gets
	// a new question.
	answered, _ := model.Update(tasksMsg{tasks: []api.Task{liveTask()}})
	model = refresh(t, answered.(Model))

	if model.screen != screenDaemon {
		t.Error("a daemon that came back and stopped again did not ask")
	}
}

// TestSayingYesStartsExactlyOneDaemon checks the count as well as the act.
//
// Two daemons on one socket is refused by the second, which is a failure the
// user did not cause; and the key that starts one is the same key that confirms
// every other dialog, so it is easy to press twice while the first is still
// waiting for its socket.
func TestSayingYesStartsExactlyOneDaemon(t *testing.T) {
	backend := newFakeBackend()
	model := stopped(t, backend)

	// The yes, without running the command it produced: this is the state the
	// user is in while a start is in flight.
	updated, cmd := model.Update(key("y"))
	model = updated.(Model)
	if !model.daemonStarting {
		t.Fatal("the dialog does not report that a start is in flight")
	}
	if !strings.Contains(flowed(model.daemonBody(60)), "Starting one and waiting") {
		t.Errorf("the dialog does not say a start is under way:\n%s", model.daemonBody(60))
	}

	// A second yes while the first has not answered.
	again, _ := model.Update(key("y"))
	model = again.(Model)

	if message := cmd(); message != nil {
		applied, _ := model.Update(message)
		model = applied.(Model)
	}

	if backend.daemonStarts != 1 {
		t.Errorf("the dashboard started %d daemons, want exactly 1", backend.daemonStarts)
	}
	if model.screen == screenDaemon {
		t.Error("the dialog stayed open after a daemon started")
	}
	if model.err != nil {
		t.Errorf("the dashboard still reports a failure after a daemon started: %v", model.err)
	}
}

// TestAStartedDaemonGetsTheEventStreamBack checks that recovery is complete.
//
// The subscription is closed when the daemon it was made to goes away, and the
// channel it delivered into is closed with it. A dashboard that read state again
// without resubscribing would be correct every two seconds and never in between,
// which is the difference between a dashboard and a report.
func TestAStartedDaemonGetsTheEventStreamBack(t *testing.T) {
	backend := newFakeBackend()
	model := stopped(t, backend)

	// The stream ends, as it does when the daemon serving it stops.
	ended, _ := model.Update(streamMsg{err: errors.New("unexpected EOF")})
	model = ended.(Model)
	close(model.events)
	before := model.events

	started, _ := model.Update(daemonStartedMsg{})
	model = started.(Model)

	if model.streamEnded {
		t.Error("the dashboard still believes its event stream has ended")
	}
	if model.events == before {
		t.Error("the dashboard reused the closed event channel, which delivers nothing")
	}
}

// TestAStartThatFailedSaysWhy keeps the reason where the user is looking.
//
// A spawn that never began serving explains itself in the end of the daemon log,
// which is several lines. The footer flattens an error to one line and cuts it to
// the width of the terminal, so the dialog stays open and holds it instead.
func TestAStartThatFailedSaysWhy(t *testing.T) {
	backend := newFakeBackend()
	backend.daemonErr = errors.New(
		"the daemon did not start listening within 10s\n" +
			"the end of /state/logs/daemon.log says:\nruntime directory is not writable")

	model := press(t, stopped(t, backend), "y")

	if model.screen != screenDaemon {
		t.Fatal("the dialog closed on a start that failed")
	}
	if model.daemonStarting {
		t.Error("the dialog still reports a start in flight after it failed")
	}

	body := flowed(model.daemonBody(60))
	for _, want := range []string{
		"Starting one did not work",
		"runtime directory is not writable",
		"Try again?",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dialog does not say %q:\n%s", want, body)
		}
	}
}

// TestTheStartKeyReopensTheOfferAfterANo is why the key is in the error message.
func TestTheStartKeyReopensTheOfferAfterANo(t *testing.T) {
	backend := newFakeBackend()
	model := press(t, stopped(t, backend), "n")

	model = press(t, model, startDaemonKey)
	if model.screen != screenDaemon {
		t.Fatalf("%s did not reopen the offer; screen = %v", startDaemonKey, model.screen)
	}
	if backend.daemonStarts != 0 {
		t.Errorf("%s started a daemon rather than asking", startDaemonKey)
	}
}

// TestTheStartKeyStartsNothingWhileADaemonIsAnswering checks the other half.
//
// The key is on the dashboard whatever the daemon is doing, because it is listed
// on `?` and a key that is documented has to do something. What it must not do is
// spawn a second daemon on a socket the first one owns.
func TestTheStartKeyStartsNothingWhileADaemonIsAnswering(t *testing.T) {
	backend := newFakeBackend()
	model := press(t, dashboard(backend, liveTask()), startDaemonKey)

	if model.screen == screenDaemon {
		t.Error("the offer opened while a daemon was answering")
	}
	if backend.daemonStarts != 0 {
		t.Errorf("%s started %d daemons while one was answering", startDaemonKey, backend.daemonStarts)
	}
	if !strings.Contains(model.status, "nothing to start") {
		t.Errorf("status = %q, want it to say there is nothing to start", model.status)
	}
}

// TestALostDaemonDoesNotInterruptAnOpenOverlay protects work in progress.
//
// Preparation holds a brief somebody is typing and cleanup holds a confirmation
// somebody is answering. Taking the keyboard from either would return them, when
// this dialog closed, to the tab underneath rather than to what they were doing —
// so the failure is recorded and the question waits until they are out.
func TestALostDaemonDoesNotInterruptAnOpenOverlay(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())
	model.screen = screenPrepare

	updated, _ := model.Update(tasksMsg{err: gone()})
	model = updated.(Model)

	if model.screen != screenPrepare {
		t.Fatalf("screen = %v, want preparation to have kept the keyboard", model.screen)
	}
	if !model.daemonGone || model.err == nil {
		t.Error("the dashboard did not record that its daemon had gone")
	}

	// Leaving the overlay lets the next refresh ask.
	model.screen = screenTerminal
	if model = refresh(t, model); model.screen != screenDaemon {
		t.Error("the question was never put after the overlay closed")
	}
}

// TestTheDialogRefusesTheKeysBehindIt keeps an unanswered question unanswerable
// by accident.
//
// Every key on the dashboard reaches the daemon, and there is not one. Moving the
// selection or opening a task under the dialog would act on a list that stopped
// being true when the daemon did.
func TestTheDialogRefusesTheKeysBehindIt(t *testing.T) {
	backend := newFakeBackend()
	model := stopped(t, backend)
	selected := model.selected

	// Every key the dashboard answers except the dialog's own y, n, and q.
	for _, pressed := range []string{"J", "K", "C", "!", "?", "a", "z", "t", "x"} {
		after := press(t, model, pressed)
		if after.screen != screenDaemon {
			t.Errorf("%q left the daemon dialog for %v", pressed, after.screen)
		}
		if after.selected != selected {
			t.Errorf("%q moved the selection while the daemon dialog was open", pressed)
		}
	}
	if backend.resumed != nil || backend.stopped != nil || backend.cancelled != nil {
		t.Errorf("a key under the dialog acted on a task: resumed %v, stopped %v, cancelled %v",
			backend.resumed, backend.stopped, backend.cancelled)
	}
	if backend.cleanupCalls != nil {
		t.Errorf("a key under the dialog reached the daemon: %v", backend.cleanupCalls)
	}
}

// TestAPendingConfirmationIsDroppedWhenTheDaemonGoes closes a way for one yes to
// be read as another.
//
// A stop and a cancel both wait for a `y`, and both are requests to the daemon.
// The dialog takes the keyboard, so the key press that clears a stale
// confirmation never arrives — and the first `y` after the dialog closed would be
// read as the answer to a question the user asked before the daemon went away.
func TestAPendingConfirmationIsDroppedWhenTheDaemonGoes(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())
	model.stopping = liveTask().ID

	updated, _ := model.Update(tasksMsg{err: gone()})
	model = updated.(Model)

	if model.stopping != "" {
		t.Error("a stop was left waiting for a yes it could not be given")
	}

	// The yes that answers the dialog is not also a yes to the stop.
	model = press(t, model, "y")
	if backend.stopped != nil {
		t.Errorf("starting a daemon also stopped %v", backend.stopped)
	}
}

// TestTheFooterCarriesTheAnswersWhileTheDialogIsOpen checks the hints.
//
// The footer says how to close an overlay, and this one is answered rather than
// closed: a user offered only "esc close" has been told how to refuse and not how
// to accept.
func TestTheFooterCarriesTheAnswersWhileTheDialogIsOpen(t *testing.T) {
	hints := flowed(stopped(t, newFakeBackend()).hints())

	for _, want := range []string{"y start a daemon", "n leave it stopped"} {
		if !strings.Contains(hints, want) {
			t.Errorf("the footer hints %q do not offer %q", hints, want)
		}
	}
}

// TestTheKeyMapNamesTheStartKey keeps the documented surface honest: the key in
// the error message has to be findable from `?` as well.
func TestTheKeyMapNamesTheStartKey(t *testing.T) {
	listed := flowed(keyMap(120))
	if !strings.Contains(listed, startDaemonKey+" start the daemon") {
		t.Errorf("the key map does not list %s:\n%s", startDaemonKey, listed)
	}
}

// TestOnlyALostDaemonIsOfferedAStart checks the classification.
//
// A daemon that answered with an error is running, and offering to start one
// would be an offer to do nothing about a problem the user has.
func TestOnlyALostDaemonIsOfferedAStart(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	refused := errors.New("the daemon refused /v1/tasks: internal error")
	updated, _ := model.Update(tasksMsg{err: refused})
	model = updated.(Model)

	if model.screen == screenDaemon {
		t.Error("a daemon that answered with an error was offered a restart")
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "internal error") {
		t.Errorf("err = %v, want the daemon's own refusal", model.err)
	}
}
