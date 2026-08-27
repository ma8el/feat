package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// wizardScreen opens the project wizard on a dashboard, with a machine under it
// that has one repository on it.
func wizardScreen(t *testing.T, backend *fakeBackend) Model {
	t.Helper()

	backend.root = t.TempDir()
	model := press(t, sized(dashboard(backend, liveTask()), 120, 32), "p")
	if model.screen != screenWizard {
		t.Fatalf("p left the screen at %v, want the wizard", model.screen)
	}
	return model
}

// answerWizard applies one answer, whatever the question wants: text is typed
// and a list is answered where its cursor is.
func answerWizard(t *testing.T, model Model, text string) Model {
	t.Helper()

	for _, r := range text {
		model = press(t, model, string(r))
	}
	return pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
}

// pressKey applies one non-rune key and follows the commands it produces.
//
// The chain matters here: answering the last question composes the
// configuration, which is a second command behind the first, and a helper that
// ran only one would leave the screen a step behind what the user would see.
func pressKey(t *testing.T, model Model, key tea.KeyMsg) Model {
	t.Helper()

	updated, cmd := model.Update(key)
	next := updated.(Model)

	for depth := 0; cmd != nil && depth < 16; depth++ {
		message := cmd()
		if message == nil {
			return next
		}
		applied, following := next.Update(message)
		next, cmd = applied.(Model), following
	}
	return next
}

// enter answers the question in front of the user with what it proposes.
func enter(t *testing.T, model Model, times int) Model {
	t.Helper()

	for range times {
		model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	}
	return model
}

// answered is how many questions the wizard has left to ask, which is what a
// test asserts progress with rather than counting key presses.
func question(t *testing.T, model Model) string {
	t.Helper()

	if !model.wizard.asked {
		t.Fatal("the wizard has no question")
	}
	return model.wizard.question.ID
}

// TestTheWizardAsksTheFlowsQuestions checks that the dialog draws what the flow
// says to ask, including what the machine answered for the user (ADR-063).
func TestTheWizardAsksTheFlowsQuestions(t *testing.T) {
	backend := newFakeBackend()
	model := wizardScreen(t, backend)

	if backend.wizards != 1 {
		t.Fatalf("the dashboard built %d wizards, want one", backend.wizards)
	}
	if got := question(t, model); got != "project.id" {
		t.Errorf("the first question is %s, want the identifier", got)
	}

	// The proposal is in the field, so Enter accepts it as it does at a shell.
	view := content(model)
	if !strings.Contains(view, "project") || !strings.Contains(view, "repositories") {
		t.Errorf("the trail does not show where the answers are:\n%s", view)
	}

	// Identifier, name, and then the checkout, which is the answer Git is asked
	// about.
	model = enter(t, model, 3)
	if got := question(t, model); got != "repository.id" {
		t.Fatalf("after three answers the question is %s, want the repository identifier", got)
	}
	if view := content(model); !strings.Contains(view, "remote origin, default branch main") {
		t.Errorf("the screen does not report what Git answered:\n%s", view)
	}
}

// TestTypingReplacesTheProposal is the rule the whole conversation rests on,
// checked at the screen that nearly broke it.
//
// The proposal was the field's contents to begin with, so typing appended to it
// and a project proposed as "repo" became "repoapp". It is a placeholder now,
// which is what it is at a shell: Enter takes it, and typing replaces it.
func TestTypingReplacesTheProposal(t *testing.T) {
	model := answerWizard(t, wizardScreen(t, newFakeBackend()), "app")

	if got := model.wizard.flow.ID(); got != "app" {
		t.Errorf("the project is %q, want what was typed", got)
	}
	// And the answer that is only Enter still takes what was proposed.
	proposed := enter(t, wizardScreen(t, newFakeBackend()), 1)
	if got := proposed.wizard.flow.ID(); got != "repo" {
		t.Errorf("the project is %q, want the proposal", got)
	}
}

// TestARefusedAnswerIsShownWhereItWasTyped checks that the dialog keeps the
// question rather than failing out of it.
func TestARefusedAnswerIsShownWhereItWasTyped(t *testing.T) {
	backend := newFakeBackend()
	model := enter(t, wizardScreen(t, backend), 2)

	// A directory the fake host knows nothing about.
	model = answerWizard(t, model, "/nowhere")

	if got := question(t, model); got != "repository.path" {
		t.Errorf("a refused answer moved on to %s", got)
	}
	if model.wizard.err == nil || !strings.Contains(model.wizard.err.Error(), "not a Git repository") {
		t.Errorf("the screen does not say why the answer was refused: %v", model.wizard.err)
	}
	if view := content(model); !strings.Contains(view, "not a Git repository") {
		t.Errorf("the reason is not drawn:\n%s", view)
	}
}

// TestSteppingBackReachesTheAnswerBefore is the whole reason the dialog is worth
// having over a conversation that cannot be scrolled back.
func TestSteppingBackReachesTheAnswerBefore(t *testing.T) {
	model := enter(t, wizardScreen(t, newFakeBackend()), 2)
	if got := question(t, model); got != "repository.path" {
		t.Fatalf("the question is %s, want the checkout", got)
	}

	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if got := question(t, model); got != "project.name" {
		t.Errorf("esc reached %s, want the answer before it", got)
	}

	// And stepping back out of the first question closes the dialog, because
	// there is nothing behind it.
	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.screen == screenWizard {
		t.Error("stepping back past the first question left the wizard open")
	}
}

// atReview opens the wizard, answers every question, and stops at the file.
func atReview(t *testing.T, width int) Model {
	t.Helper()

	model := enter(t, sized(wizardScreen(t, newFakeBackend()), width, 44), 9)
	if model.wizard.step != wizardReviewing {
		t.Fatalf("after every question the step is %v, want the review", model.wizard.step)
	}
	return model
}

// roomFor is a terminal wide enough that a dialog of this width is not clamped.
//
// dialogLimits allows three quarters of the terminal, so a dialog is drawn at its
// own size only where three quarters of the screen is at least that. The width is
// derived rather than written down because what the review holds is a path, and
// how long a path is depends on the machine the test is running on — which is how
// this test first failed on somebody else's.
func roomFor(cells int) int { return cells*4/3 + 8 }

// TestTheWizardIsSizedToItsContent keeps the dialog off the width of the screen.
//
// dialogBox shrinks to the widest line it is handed, and lipgloss pads every
// wrapped line out to the width it wrapped to — so from the review step onwards
// the wizard reported itself as exactly as wide as it was allowed, and took
// three quarters of the terminal to show a file whose lines are half that.
//
// It is not capped at a measure, because this dialog is a form and a file: what
// decides its width is what is on it, and a longer path or a wider question is
// entitled to more room, up to the allowance.
func TestTheWizardIsSizedToItsContent(t *testing.T) {
	// Wide enough that nothing is clamped, so what is measured is what the
	// content wants rather than what the screen would allow.
	model := atReview(t, 400)

	natural, _ := blockSize(model.dialogView())
	allowed, _ := model.dialogLimits()
	if natural >= allowed {
		t.Fatalf("the review took all %d cells it was allowed rather than sizing to the file", allowed)
	}

	// A smaller terminal that still has room for it draws the same dialog: the
	// width follows what is on the screen, not what the screen has.
	roomy := roomFor(natural)
	if got, _ := blockSize(sized(model, roomy, 44).dialogView()); got != natural {
		t.Errorf("the same review is %d cells at 400 columns and %d at %d", natural, got, roomy)
	}

	// And a terminal with no room for it is still not overrun: the allowance is
	// a ceiling, and the file's lines are cut to it.
	cramped := sized(model, natural, 44)
	crampedAllowed, _ := cramped.dialogLimits()
	if got, _ := blockSize(cramped.dialogView()); got > crampedAllowed {
		t.Errorf("the review is %d cells in a box allowed %d", got, crampedAllowed)
	}
}

// TestScrollingTheFileDoesNotResizeTheDialog is the cost of the fix above, paid
// where it would otherwise show.
//
// Once the box follows its widest line, the widest line must not be whichever
// part of the file happens to be on screen — a dialog that grew and shrank as the
// user scrolled through a configuration would be worse than one that was always
// too wide.
func TestScrollingTheFileDoesNotResizeTheDialog(t *testing.T) {
	model := atReview(t, 400)

	before, _ := blockSize(model.dialogView())
	allowed, _ := model.dialogLimits()
	if before >= allowed {
		// Clamped to the allowance, the width cannot move whatever this does,
		// and the test would pass having proved nothing.
		t.Fatalf("the review is clamped at %d cells, so scrolling could not move it anyway", allowed)
	}

	for range 8 {
		model = press(t, model, "j")
		if after, _ := blockSize(model.dialogView()); after != before {
			t.Fatalf("scrolling the file moved the dialog from %d cells to %d", before, after)
		}
	}
	if model.wizard.scroll == 0 {
		t.Error("the file did not scroll, so this proved nothing")
	}
}

// TestNothingIsWrittenBeforeTheFileIsConfirmed is FR-PROJ-005 at the screen that
// implements it: the whole file is displayed, and writing it is a separate act.
func TestNothingIsWrittenBeforeTheFileIsConfirmed(t *testing.T) {
	backend := newFakeBackend()
	model := enter(t, wizardScreen(t, backend), 9)

	if model.wizard.step != wizardReviewing {
		t.Fatalf("after every question the step is %v, want the review", model.wizard.step)
	}
	if len(backend.written) != 0 {
		t.Fatal("the configuration was written before it was confirmed")
	}

	view := content(model)
	// It opens on the file's first line, because the file is what is being
	// confirmed and a review that started in the middle would be a different
	// promise.
	for _, want := range []string{"# Feat project configuration", "repo.yaml", "nothing has been written yet"} {
		if !strings.Contains(view, want) {
			t.Errorf("the review does not show %q:\n%s", want, view)
		}
	}
	// A configuration is longer than a dialog, so what is not on the screen is
	// said to be there and can be scrolled to.
	if !strings.Contains(view, "more lines") {
		t.Errorf("the review does not say that it continues:\n%s", view)
	}
	if !strings.Contains(string(model.wizard.review.Text), "mode: host") {
		t.Errorf("the composed configuration is not the whole file:\n%s", model.wizard.review.Text)
	}
	scrolled := content(pressKey(t, model, tea.KeyMsg{Type: tea.KeyPgDown}))
	if scrolled == view {
		t.Fatal("the review cannot be scrolled")
	}
	if !strings.Contains(scrolled, "id: repo") {
		t.Errorf("scrolling does not reach what the answers decided:\n%s", scrolled)
	}
	// And it fits the dialog it is drawn in. The box clamps from the bottom, so
	// a review one line too tall costs the user the hints that write the file.
	if drawn := model.View(); strings.Contains(drawn, "than fit here") {
		t.Errorf("the review overruns the dialog:\n%s", drawn)
	}

	// Confirming writes it, and what follows is the check against this machine.
	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(backend.written) != 1 || backend.written[0] != "repo" {
		t.Fatalf("confirming wrote %v, want the project that was answered", backend.written)
	}
	if model.wizard.step != wizardChecking {
		t.Fatalf("after writing the step is %v, want the check", model.wizard.step)
	}
	if len(backend.registered) != 0 {
		t.Error("the project was registered without being asked about")
	}
}

// TestTheWrittenProjectIsCheckedAgainstTheMachine is the answer to what the
// questions could not ask (ADR-064).
//
// The checks run once the file exists, because that is what they are about, and
// they run rather than being offered: the user is waiting either way, and
// nothing they find changes anything on the machine.
func TestTheWrittenProjectIsCheckedAgainstTheMachine(t *testing.T) {
	backend := newFakeBackend()
	backend.diagnosis = api.Diagnosis{
		Environment: "this terminal",
		Projects: []api.ProjectDiagnosis{{
			ID: "repo", File: "/config/repo.yaml",
			Findings: []api.Finding{
				{Check: "repositories.repo", Severity: api.SeverityOK, Summary: "a Git repository"},
				{
					Check: "agent.execution.service", Severity: api.SeverityError,
					Summary: "no service dev in the Compose files",
					Action:  "name a service the Compose files define",
				},
			},
		}},
		Host: []api.Finding{{Check: "git", Severity: api.SeverityOK, Summary: "git version 2.52.0"}},
	}

	model := enter(t, wizardScreen(t, backend), 10)
	if model.wizard.step != wizardChecking {
		t.Fatalf("the step is %v, want the check", model.wizard.step)
	}
	if len(backend.diagnosed) != 1 || backend.diagnosed[0] != "repo" {
		t.Fatalf("the checks ran for %v, want the project that was written", backend.diagnosed)
	}

	view := content(model)
	for _, want := range []string{
		"agent.execution.service", "no service dev", "name a service the Compose files define",
		"git version 2.52.0",
		// What the checks are true of, which is this process and not the daemon.
		"checked from this terminal",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the check does not show %q:\n%s", want, view)
		}
	}

	// Looking again is one key, which is the point of having the findings here
	// rather than in a command.
	model = press(t, model, "r")
	if len(backend.diagnosed) != 2 {
		t.Errorf("r ran the checks %d times, want a second pass", len(backend.diagnosed))
	}

	// And nothing about the findings stops the setup: the file exists, so the
	// only way through is forward.
	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.wizard.step != wizardRegistering {
		t.Errorf("after the check the step is %v, want the registration offer", model.wizard.step)
	}
}

// TestRegisteringIsOfferedAndAnswered checks the step that closes the first-run
// path, from a dashboard with no project to one with a registered project.
func TestRegisteringIsOfferedAndAnswered(t *testing.T) {
	backend := newFakeBackend()
	model := enter(t, wizardScreen(t, backend), 11)

	if model.wizard.step != wizardRegistering {
		t.Fatalf("the step is %v, want the registration offer", model.wizard.step)
	}
	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	if len(backend.registered) != 1 || backend.registered[0] != "repo" {
		t.Fatalf("registered %v, want the project that was written", backend.registered)
	}
	view := content(model)
	if !strings.Contains(view, filepath.Join("config", "repo.yaml")) {
		t.Errorf("the last screen does not say what was written:\n%s", view)
	}
	if !strings.Contains(view, "registered repo") {
		t.Errorf("the last screen does not say what was registered:\n%s", view)
	}

	// Closing returns to the dashboard, which reads state again because a
	// project it did not know about now exists.
	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.screen == screenWizard {
		t.Error("the wizard stayed open after its last screen")
	}
}

// TestAFailedRegistrationStillReportsTheFile checks the half-done state: the
// file exists whatever the daemon said, and a user who is told nothing would
// write it again.
func TestAFailedRegistrationStillReportsTheFile(t *testing.T) {
	backend := newFakeBackend()
	backend.registerErr = errors.New("the daemon is not running")

	model := enter(t, wizardScreen(t, backend), 12)

	view := content(model)
	if !strings.Contains(view, "repo.yaml") {
		t.Errorf("the file that was written is not reported:\n%s", view)
	}
	if !strings.Contains(view, "the daemon is not running") {
		t.Errorf("the reason it was not registered is not reported:\n%s", view)
	}
	if !strings.Contains(view, "feat project add repo") {
		t.Errorf("the screen does not say what registers it later:\n%s", view)
	}
}

// TestTheWizardCanBeLeftAtAnyQuestion checks that cancelling is always
// available, and that leaving writes nothing.
func TestTheWizardCanBeLeftAtAnyQuestion(t *testing.T) {
	backend := newFakeBackend()

	for _, answered := range []int{0, 3, 9} {
		model := enter(t, wizardScreen(t, backend), answered)
		model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyCtrlC})

		if model.screen == screenWizard {
			t.Errorf("ctrl+c after %d answers left the wizard open", answered)
		}
		if len(backend.written) != 0 {
			t.Fatalf("leaving after %d answers wrote %v", answered, backend.written)
		}
	}
}
