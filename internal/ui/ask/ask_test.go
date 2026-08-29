package ask

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/wizard"
)

// opened puts one question to a fresh widget.
func opened(t *testing.T, question wizard.Question) Model {
	t.Helper()
	return New().Ask(question)
}

// press applies one key and returns the widget with what the key decided.
func press(t *testing.T, model Model, key tea.KeyMsg) (Model, Result) {
	t.Helper()

	updated, result, _ := model.Update(key)
	return updated, result
}

// stroke is one printable key, as a user types it.
func stroke(letter rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{letter}}
}

// typing applies a string of them.
func typing(t *testing.T, model Model, text string) Model {
	t.Helper()

	for _, letter := range text {
		model, _ = press(t, model, stroke(letter))
	}
	return model
}

// choice is the question that made this widget worth extracting: five options
// the conversation joined with slashes and answered by retyping one exactly.
func choice() wizard.Question {
	return wizard.Question{
		ID: "repository.access", Section: wizard.SectionRepositories, Kind: wizard.KindChoice,
		Heading: "Repositories",
		Detail:  []string{"How this repository takes part in a task."},
		Notes:   []string{"remote origin, default branch main"},
		Prompt:  "How does it take part in a task by default?",
		Options: []string{"read_write", "selectable", "read_only", "stable_read_only", "omitted"},
	}
}

// TestTheCursorStartsOnTheProposalAndAnswersWithIt is what the list is for:
// Enter accepts what the flow proposed, wherever in the list it is, so a closed
// question means the same thing here as it does in brackets at a shell.
func TestTheCursorStartsOnTheProposalAndAnswersWithIt(t *testing.T) {
	question := choice()
	question.Proposed = "stable_read_only"

	model := opened(t, question)
	if got := model.cursor; got != 3 {
		t.Fatalf("the cursor is on option %d, want the proposal at 3", got)
	}
	if view := model.View(); !strings.Contains(view, "▸ stable_read_only") {
		t.Errorf("the proposal is not under the cursor:\n%s", view)
	}

	_, result := press(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if result.Outcome != Answered || result.Answer != "stable_read_only" {
		t.Errorf("Enter gave %v %q, want the option under the cursor", result.Outcome, result.Answer)
	}
}

// TestTheCursorMovesAndStopsAtTheEnds checks the movement keys and that neither
// end of the list can be walked off, which is what would answer a closed
// question with nothing.
func TestTheCursorMovesAndStopsAtTheEnds(t *testing.T) {
	model := opened(t, choice())

	for range 2 {
		model, _ = press(t, model, tea.KeyMsg{Type: tea.KeyUp})
	}
	if got := model.cursor; got != 0 {
		t.Errorf("moving up off the top left the cursor at %d, want the first option", got)
	}

	for range 8 {
		model, _ = press(t, model, stroke('j'))
	}
	if got := model.cursor; got != 4 {
		t.Errorf("moving down off the bottom left the cursor at %d, want the last option", got)
	}
	_, result := press(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if result.Answer != "omitted" {
		t.Errorf("the answer is %q, want the option the cursor stopped on", result.Answer)
	}
}

// TestAConfirmIsReadAsWordsAndAnsweredAsALetter checks the one closed question
// whose options are not the words it is drawn with.
func TestAConfirmIsReadAsWordsAndAnsweredAsALetter(t *testing.T) {
	question := wizard.Question{
		ID: "runtime.repository", Kind: wizard.KindConfirm,
		Prompt: "Does api bring Compose files to the application?", Proposed: "n",
	}

	model := opened(t, question)
	view := model.View()
	for _, want := range []string{"  yes", "▸ no"} {
		if !strings.Contains(view, want) {
			t.Errorf("the confirm does not draw %q:\n%s", want, view)
		}
	}

	_, result := press(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if result.Answer != "n" {
		t.Errorf("the answer is %q, want the letter the flow asked for", result.Answer)
	}

	moved, _ := press(t, model, tea.KeyMsg{Type: tea.KeyUp})
	if _, result := press(t, moved, tea.KeyMsg{Type: tea.KeyEnter}); result.Answer != "y" {
		t.Errorf("the answer above it is %q, want %q", result.Answer, "y")
	}
}

// TestTypingReplacesTheProposal is the rule the whole conversation rests on,
// checked on the widget that draws it: a proposal is a placeholder, so Enter
// takes it and typing replaces it rather than appending to it.
func TestTypingReplacesTheProposal(t *testing.T) {
	question := wizard.Question{
		ID: "project.id", Kind: wizard.KindText,
		Prompt: "Project identifier", Proposed: "repo", Candidates: []string{"repo"},
	}

	empty := opened(t, question)
	if _, result := press(t, empty, tea.KeyMsg{Type: tea.KeyEnter}); result.Answer != "" {
		t.Errorf("an untouched field answers %q, want the empty answer the flow reads as its proposal", result.Answer)
	}

	typed := typing(t, empty, "app")
	if got := typed.Value(); got != "app" {
		t.Fatalf("the field holds %q, want what was typed", got)
	}
	if _, result := press(t, typed, tea.KeyMsg{Type: tea.KeyEnter}); result.Answer != "app" {
		t.Errorf("the answer is %q, want what was typed", result.Answer)
	}
}

// TestTabTakesTheProposalAndStepsThroughTheCandidates is the capability the
// command line never had: the flow derives lists, and a question has one
// proposal (ADR-077).
func TestTabTakesTheProposalAndStepsThroughTheCandidates(t *testing.T) {
	question := wizard.Question{
		ID: "runtime.compose", Kind: wizard.KindText,
		Prompt:     "Compose file for api",
		Proposed:   "/repo/compose.yaml",
		Candidates: []string{"/repo/compose.yaml", "/repo/compose.override.yaml"},
	}

	model := opened(t, question)
	for i, want := range []string{
		"/repo/compose.yaml", "/repo/compose.override.yaml", "/repo/compose.yaml",
	} {
		var result Result
		model, result = press(t, model, tea.KeyMsg{Type: tea.KeyTab})
		if result.Outcome != Continued {
			t.Fatalf("tab %d decided %v, want a value in the field and nothing else", i+1, result.Outcome)
		}
		if got := model.Value(); got != want {
			t.Fatalf("tab %d left the field holding %q, want %q", i+1, got, want)
		}
	}

	// And what is in the field is what Enter sends, whichever key put it there.
	if _, result := press(t, model, tea.KeyMsg{Type: tea.KeyEnter}); result.Answer != "/repo/compose.yaml" {
		t.Errorf("the answer is %q, want what tab left in the field", result.Answer)
	}
}

// TestEscIsAStepBackRatherThanAnAnswer checks the outcome the caller acts on.
//
// The widget knows about a question and not about a conversation: whether there
// is anything behind this one is the flow's to answer, so esc reports what the
// user asked for and decides nothing.
func TestEscIsAStepBackRatherThanAnAnswer(t *testing.T) {
	model := typing(t, opened(t, wizard.Question{
		ID: "project.id", Kind: wizard.KindText, Prompt: "Project identifier",
	}), "app")

	updated, result := press(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if result.Outcome != SteppedBack {
		t.Fatalf("esc decided %v, want a step back", result.Outcome)
	}
	if result.Answer != "" {
		t.Errorf("a step back carries the answer %q", result.Answer)
	}
	if got := updated.Value(); got != "app" {
		t.Errorf("esc changed the field to %q; what to do about it is the caller's", got)
	}
}

// TestTheQuestionIsDrawnFromWhatTheFlowSaysAboutIt is the whole argument for
// one widget over two: everything on the screen is read off the question, so a
// sentence the flow adds reaches both askers or neither (ADR-063).
func TestTheQuestionIsDrawnFromWhatTheFlowSaysAboutIt(t *testing.T) {
	model := opened(t, choice())

	context := model.Context()
	for _, want := range []string{"Repositories", "How this repository takes part", "remote origin"} {
		if !strings.Contains(context, want) {
			t.Errorf("the context does not draw %q:\n%s", want, context)
		}
	}
	if view := model.View(); !strings.Contains(view, "How does it take part") {
		t.Errorf("the question does not draw its own prompt:\n%s", view)
	}
	// The prose belongs to the context and not to the question, because one of
	// the two askers prints it itself and leaves it in the scrollback.
	if view := model.View(); strings.Contains(view, "Repositories") {
		t.Errorf("the question redraws what the caller may already have said:\n%s", view)
	}
}

// TestAFieldThatLooksEmptierThanItIsSaysSo checks the one sentence drawn under
// a field: an optional question is finished by leaving it empty, and nothing in
// an empty field says that.
func TestAFieldThatLooksEmptierThanItIsSaysSo(t *testing.T) {
	optional := opened(t, wizard.Question{
		ID: "runtime.compose", Kind: wizard.KindText,
		Prompt: "Compose override file for api (blank to finish)", Optional: true,
	})
	if view := optional.View(); !strings.Contains(view, "an empty answer is an answer here") {
		t.Errorf("an optional question does not say that it is:\n%s", view)
	}

	required := opened(t, wizard.Question{ID: "project.id", Kind: wizard.KindText, Prompt: "Project identifier"})
	if view := required.View(); strings.Contains(view, "an empty answer") {
		t.Errorf("a question that must be answered says otherwise:\n%s", view)
	}
}

// TestTheKeysSaidAreTheKeysThatWork checks the footer against the question it
// is under: tab is named only where there is something to complete, and esc
// only where the caller has somewhere to go.
func TestTheKeysSaidAreTheKeysThatWork(t *testing.T) {
	plain := opened(t, wizard.Question{ID: "project.name", Kind: wizard.KindText, Prompt: "Display name"})
	if hints := plain.Hints(true); strings.Contains(hints, "tab") {
		t.Errorf("a question with nothing to complete names tab: %s", hints)
	}

	completable := opened(t, wizard.Question{
		ID: "runtime.compose", Kind: wizard.KindText, Prompt: "Compose file",
		Candidates: []string{"/repo/compose.yaml"},
	})
	if hints := completable.Hints(true); !strings.Contains(hints, "tab") {
		t.Errorf("a question with candidates does not name tab: %s", hints)
	}

	closed := opened(t, choice())
	if hints := closed.Hints(true); !strings.Contains(hints, "↑↓") || !strings.Contains(hints, "esc") {
		t.Errorf("a closed question does not name the keys that move and leave it: %s", hints)
	}
	if hints := closed.Hints(false); strings.Contains(hints, "esc") {
		t.Errorf("a question with nothing behind it names esc: %s", hints)
	}
}
