package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	widget "github.com/ma8el/feat/internal/ui/ask"
	"github.com/ma8el/feat/internal/wizard"
)

// newWidget opens the question widget on one question, as the asker does.
func newWidget(question wizard.Question) widget.Model { return widget.New().Ask(question) }

// environment is what this machine looks like to a command.
func (m *wizardMachine) environment() *environment {
	return &environment{interactive: true, layout: &m.layout, process: &m.env, runner: m.runner}
}

// flow builds the wizard a conversation on this machine drives.
func (m *wizardMachine) flow(t *testing.T) *wizard.Wizard {
	t.Helper()

	flow, err := m.environment().wizard("")
	if err != nil {
		t.Fatalf("building the wizard: %v", err)
	}
	return flow
}

// TestTheAskerIsChosenByTheReaderAndByTerm checks the rule at each of its two
// clauses (ADR-084).
//
// The terminal is passed in rather than borrowed. A test cannot make one
// portably — the master side of a pseudo-terminal answers isatty on Linux and
// not on macOS, which is a fact about Darwin's ptys and not about this rule —
// and what the rule is about is which asker the two facts choose.
func TestTheAskerIsChosenByTheReaderAndByTerm(t *testing.T) {
	for _, want := range []struct {
		name     string
		terminal bool
		term     string
		drawn    bool
	}{
		{name: "a terminal, and one that draws", terminal: true, term: "xterm-256color", drawn: true},
		{name: "a script on a drawing terminal", term: "xterm-256color"},
		{name: "a terminal that says it is dumb", terminal: true, term: "dumb"},
		{name: "a terminal that has not said what it is", terminal: true},
	} {
		t.Run(want.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key == "TERM" {
					return want.term
				}
				return ""
			}
			drawn := questionsCanBeDrawn(want.terminal, getenv)
			if drawn != want.drawn {
				t.Fatalf("the questions are drawn: %v, want %v", drawn, want.drawn)
			}

			var out bytes.Buffer
			lines := prompter{in: bufio.NewReader(strings.NewReader("")), out: &out}
			chosen := pickAsker(drawn, strings.NewReader(""), &out, &lines)
			if _, widget := chosen.(widgetAsker); widget != want.drawn {
				t.Fatalf("the asker is %T, want the widget to be %v", chosen, want.drawn)
			}
			// And the sentence about how an answer is given follows the asker
			// that takes it, because brackets are one asker's device.
			if got := strings.Contains(chosen.opening(), "brackets"); got == want.drawn {
				t.Errorf("the opening sentence is %q for the widget=%v asker", chosen.opening(), want.drawn)
			}
		})
	}
}

// TestTheReaderAskedAboutIsTheOneTheCommandHolds is the other half of the input
// clause, and the half that can be wrong.
//
// Whether a reader is a terminal is a question only an *os.File can be asked,
// so what matters is that the command holds one in a real run and holds the
// test's script in a scripted one. A reader wrapping the process's stdin would
// answer no to the type question and leave the widget unreachable however good
// the terminal was.
func TestTheReaderAskedAboutIsTheOneTheCommandHolds(t *testing.T) {
	if got := NewRootCommand(Options{}).InOrStdin(); got != os.Stdin {
		t.Errorf("a real run reads its answers from %T, which is not the process's own stdin", got)
	}

	script := strings.NewReader("app\n")
	if got := NewRootCommand(Options{Input: script}).InOrStdin(); got != script {
		t.Errorf("a scripted run reads its answers from %T, which is not the script", got)
	}
	if terminalReader(script) {
		t.Error("a scripted conversation was taken for a terminal")
	}

	// A file is the first question and not the last: a pipe is an *os.File and
	// nobody presses a key into one.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("opening a pipe: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	if terminalReader(reader) {
		t.Error("a pipe was taken for a terminal")
	}
}

// TestTheWidgetLeavesTheAnsweredQuestionBehind is what makes the widget inline
// rather than a screen: the transcript accumulates in the scrollback exactly as
// it does under the line asker, and this is the line it accumulates (ADR-062).
func TestTheWidgetLeavesTheAnsweredQuestionBehind(t *testing.T) {
	for _, want := range []struct {
		name     string
		question wizard.Question
		answer   string
		line     string
	}{
		{
			name:     "an option",
			question: wizard.Question{Kind: wizard.KindChoice, Prompt: "Execution mode", Proposed: "host"},
			answer:   "devcontainer",
			line:     "  Execution mode: devcontainer",
		},
		{
			name:     "a confirm, in the words it was drawn with",
			question: wizard.Question{Kind: wizard.KindConfirm, Prompt: "Add another repository?", Proposed: "n"},
			answer:   "y",
			line:     "  Add another repository? yes",
		},
		{
			name:     "what was typed",
			question: wizard.Question{Kind: wizard.KindText, Prompt: "Project identifier", Proposed: "repo"},
			answer:   "app",
			line:     "  Project identifier: app",
		},
		{
			// The flow reads an empty answer as its proposal, so the transcript
			// records the value the flow was given rather than the key pressed.
			name:     "the proposal an empty answer takes",
			question: wizard.Question{Kind: wizard.KindText, Prompt: "Project identifier", Proposed: "repo"},
			line:     "  Project identifier: repo",
		},
		{
			name:     "an empty answer that proposes nothing",
			question: wizard.Question{Kind: wizard.KindText, Prompt: "Environment file", Optional: true},
			line:     "  Environment file: none",
		},
	} {
		t.Run(want.name, func(t *testing.T) {
			drawn := asking{question: want.question, indent: "  "}
			if got := drawn.transcript(want.answer); got != want.line {
				t.Errorf("the answered question reads %q, want %q", got, want.line)
			}
		})
	}
}

// TestTheWidgetStopsDrawingOnceItIsAnswered checks that what the widget drew to
// take an answer does not stay behind beside the line it left.
func TestTheWidgetStopsDrawingOnceItIsAnswered(t *testing.T) {
	question := wizard.Question{Kind: wizard.KindChoice, Prompt: "Execution mode",
		Options: []string{"host", "devcontainer"}, Proposed: "host"}
	drawn := asking{field: newWidget(question), question: question, indent: "  ", back: true}

	if view := drawn.View(); !strings.Contains(view, "▸ host") || !strings.Contains(view, "esc") {
		t.Fatalf("the question is not drawn with its options and its keys:\n%s", view)
	}
	for _, line := range strings.Split(drawn.View(), "\n") {
		if line != "" && !strings.HasPrefix(line, "  ") {
			t.Errorf("a drawn line is not under the conversation's indent: %q", line)
		}
	}

	updated, _ := drawn.Update(tea.KeyMsg{Type: tea.KeyEnter})
	answered := updated.(asking)
	if !answered.done || answered.reply.value != "host" {
		t.Fatalf("Enter left %+v, want the option under the cursor", answered.reply)
	}
	if view := answered.View(); view != "" {
		t.Errorf("the answered widget still draws:\n%s", view)
	}
}

// TestEscStepsBackOnlyWhereThereIsSomethingBehind checks the key on both kinds
// of question the widget draws: the flow's, which have an answer before them,
// and the conversation's own offers, which have not.
func TestEscStepsBackOnlyWhereThereIsSomethingBehind(t *testing.T) {
	question := wizard.Question{Kind: wizard.KindText, Prompt: "Project identifier"}

	flowQuestion := asking{field: newWidget(question), question: question, back: true}
	stepped := pressed(t, flowQuestion, tea.KeyMsg{Type: tea.KeyEsc})
	if !stepped.done || !stepped.reply.back {
		t.Errorf("esc left %+v, want a step back", stepped.reply)
	}

	offer := asking{field: newWidget(question), question: question}
	held := pressed(t, offer, tea.KeyMsg{Type: tea.KeyEsc})
	if held.done {
		t.Errorf("esc ended an offer with nothing behind it: %+v", held.reply)
	}
	if hints := held.View(); strings.Contains(hints, "esc") {
		t.Errorf("an offer names a key it has not got:\n%s", hints)
	}
}

// pressed applies one key and returns the widget it produced.
func pressed(t *testing.T, model asking, key tea.KeyMsg) asking {
	t.Helper()

	updated, _ := model.Update(key)
	answered, ok := updated.(asking)
	if !ok {
		t.Fatalf("the update returned %T", updated)
	}
	return answered
}

// TestLeavingAQuestionIsNotAnAnswer checks the one key that ends the command
// where it stands: what it leaves is a transcript, and what it must never leave
// is an answer nobody gave.
func TestLeavingAQuestionIsNotAnAnswer(t *testing.T) {
	question := wizard.Question{Kind: wizard.KindConfirm, Prompt: "Write it?", Proposed: "y"}

	left := pressed(t, asking{field: newWidget(question), question: question},
		tea.KeyMsg{Type: tea.KeyCtrlC})
	if left.done {
		t.Errorf("ctrl+c decided the question: %+v", left.reply)
	}
	if !left.left {
		t.Error("ctrl+c did not record that the conversation was left")
	}
	// The frame goes with it. A question with a cursor in it, left on the screen
	// after the command has gone, reads as one still waiting for an answer.
	if view := left.View(); view != "" {
		t.Errorf("the widget is still drawing after it was left:\n%s", view)
	}
}

// scriptedAsker answers with a written script and records what it was asked.
//
// It stands in for the widget so that the conversation's own half of stepping
// back — the marker, and asking the flow to undo an answer — can be driven
// without a terminal.
type scriptedAsker struct {
	// out is where it leaves the answered question, as the widget does, so that
	// a test about the shape of the transcript is about the real shape of it.
	out     io.Writer
	replies []reply
	asked   []string
	at      int
}

func (s *scriptedAsker) opening() string { return "Answer with the keys under each question.\n" }

func (s *scriptedAsker) question(_ context.Context, question wizard.Question, indent string) (reply, error) {
	s.asked = append(s.asked, question.ID)
	if s.at >= len(s.replies) {
		return reply{}, errAnswersEnded
	}
	s.at++

	answer := s.replies[s.at-1]
	if !answer.back && s.out != nil {
		drawn := asking{question: question, indent: indent}
		printf(s.out, "%s\n", drawn.transcript(answer.value))
	}
	return answer, nil
}

func (s *scriptedAsker) offer(context.Context, string, bool) (bool, error) { return false, nil }

// TestSteppingBackReopensTheQuestionBeforeIt is the capability the conversation
// dropped: Wizard.Back has existed since the flow was written and only the
// dashboard ever called it (ADR-084).
func TestSteppingBackReopensTheQuestionBeforeIt(t *testing.T) {
	m := prepareWizard(t)

	var out bytes.Buffer
	answers := &scriptedAsker{out: &out, replies: []reply{
		{value: "app"},               // project identifier
		{value: "Example"},           // display name
		{value: m.repository("api")}, // the checkout
		{value: "api"},               // repository identifier
		{value: "read_write"},        // how it takes part by default
		{value: "n"},                 // no second repository
		{back: true},                 // at the execution mode, back out of that answer
		{value: "n"},                 // no second repository, again
		{value: "host"},              // where the agent runs
		{value: "n"},                 // no application services
	}}
	talk := &conversation{
		prompter: prompter{in: bufio.NewReader(strings.NewReader("")), out: &out},
		asker:    answers,
		wizard:   m.flow(t),
		env:      m.environment(),
		layout:   m.layout,
		dryRun:   true,
	}

	if err := talk.run(context.Background()); err != nil {
		t.Fatalf("the conversation failed: %v\n%s", err, out.String())
	}

	want := []string{
		"project.id", "project.name", "repository.path", "repository.id",
		"repository.access", "repository.another", "agent.mode",
		// What the step back reached, and everything it undid asked again.
		"repository.another", "agent.mode", "runtime.wanted",
	}
	if got := strings.Join(answers.asked, " "); got != strings.Join(want, " ") {
		t.Errorf("the questions asked were\n%s\nwant\n%s", got, strings.Join(want, " "))
	}

	// The restored question is announced below what is already there, under a
	// marker naming what it returned to. Nothing above it is rewritten: a
	// transcript holding two answers to one question has to read as a
	// correction rather than as a contradiction.
	transcript := out.String()
	if !strings.Contains(transcript, "↩ back to: Add another repository?") {
		t.Errorf("the transcript does not say what the step back returned to:\n%s", transcript)
	}
	// With one blank line over it and not two. The question stepped back out of
	// had opened a section, so the transcript already ended in a blank, and a
	// second one reads as something dropped rather than as a separator.
	if !strings.Contains(transcript, "the task.\n\n  ↩ back to: ") {
		t.Errorf("the marker is not one blank line under what came before it:\n%s", transcript)
	}
	if !strings.Contains(transcript, "mode: host") {
		t.Errorf("the answers after the step back did not compose a configuration:\n%s", transcript)
	}
}

// TestEachPartOfTheTranscriptIsSeparatedFromTheLast is what a conversation
// needs once every question in it has been answered rather than asked.
//
// A heading is a bare line at the left margin, and so is the path of the file
// the answers composed; the answered questions above them are indented lines
// that look much like them. The blank line that used to be the only boundary is
// what the transcript is already full of.
func TestEachPartOfTheTranscriptIsSeparatedFromTheLast(t *testing.T) {
	m := prepareWizard(t)

	var out bytes.Buffer
	answers := &scriptedAsker{out: &out, replies: []reply{
		{value: "app"}, {value: "Example"}, {value: m.repository("api")},
		{value: "api"}, {value: "read_write"}, {value: "n"},
		{value: "host"}, {value: "n"},
	}}
	talk := &conversation{
		prompter: prompter{in: bufio.NewReader(strings.NewReader("")), out: &out},
		asker:    answers,
		wizard:   m.flow(t),
		env:      m.environment(),
		layout:   m.layout,
		dryRun:   true,
	}
	if err := talk.run(context.Background()); err != nil {
		t.Fatalf("the conversation failed: %v\n%s", err, out.String())
	}

	transcript := out.String()
	rule := strings.Repeat("─", ruleWidth)
	for _, opened := range []string{
		"Repositories", "Where the agent runs", "Application services",
		// The file is the largest part of all, and the last thing the user is
		// asked to read before they are asked to keep it.
		filepath.Join(m.layout.ProjectConfigDir(), "app.yaml"),
	} {
		if !strings.Contains(transcript, "\n\n"+rule+"\n"+opened+"\n") {
			t.Errorf("%q does not open under a rule and a blank line:\n%s", opened, transcript)
		}
	}

	// And what explains a decision is parted from the decisions themselves,
	// which is the same break the dialog draws under a question's detail.
	if !strings.Contains(transcript, "the task.\n\n  Execution mode: host\n") {
		t.Errorf("a section's detail runs into the answers under it:\n%s", transcript)
	}
	// One blank line and never two. A second is not twice the separator; it is
	// a gap that reads as something dropped.
	questions, _, _ := strings.Cut(transcript, rule+"\n"+m.layout.ProjectConfigDir())
	if strings.Contains(questions, "\n\n\n") {
		t.Errorf("the questions are separated by more blank line than one:\n%s", questions)
	}
}
