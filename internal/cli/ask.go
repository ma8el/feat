package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	// Aliased because this package already has an ask of its own: the line
	// confirmation `feat cleanup` puts, which keeps the prompter it has.
	widget "github.com/ma8el/feat/internal/ui/ask"
	"github.com/ma8el/feat/internal/wizard"
)

// reply is what one question came back with.
type reply struct {
	// value is the answer, as the flow wants it given: what was typed, or the
	// option the cursor was on.
	value string
	// back reports that the user asked for the question before this one instead
	// of answering this one. Only an asker with a key for it ever sets it.
	back bool
}

// asker puts the questions of one `feat project init` run.
//
// There are two. The widget draws a question and takes the answer with a
// cursor; the line conversation prints a prompt and reads a line. Which one
// runs is decided once, from the terminal the command was given, and neither
// decides what is asked: the flow owns the question, its proposal, and whether
// an answer is acceptable (ADR-063, ADR-084).
type asker interface {
	// question puts one of the flow's questions, indented as the conversation
	// indents it.
	question(ctx context.Context, question wizard.Question, indent string) (reply, error)
	// offer puts a yes-or-no question of the conversation's own: whether to
	// write the file, whether to check it, whether to register it. They are not
	// the flow's, and there is nothing behind them to step back to.
	offer(ctx context.Context, prompt string, proposed bool) (bool, error)
	// opening is the sentence saying how an answer is given. It is the one line
	// of the conversation's own prose that depends on which asker is running,
	// because brackets are a thing only one of them draws.
	opening() string
}

// pickAsker chooses how this run's questions are put.
//
// The rich asker whenever the command's input is a terminal and TERM is neither
// dumb nor empty. The reader is the command's rather than the process's because
// that is what a scripted conversation replaces, and the TERM clause is what
// gives the line asker a user who is not a test: an Emacs shell buffer, a
// stripped CI terminal, anything that mangles raw mode (ADR-084).
func pickAsker(in io.Reader, out io.Writer, getenv func(string) string, lines *prompter) asker {
	if !terminalReader(in) || !drawableTerminal(getenv) {
		return lineAsker{prompter: lines}
	}
	return widgetAsker{in: in, out: out}
}

// terminalReader reports whether what the command reads answers from is a
// terminal. A widget takes its answer a key at a time, and a key press is
// something only a terminal has.
func terminalReader(in io.Reader) bool {
	file, ok := in.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// drawableTerminal reports whether TERM names a terminal a widget can be drawn
// on. An empty TERM is a terminal that has not said what it is, and dumb is one
// saying it can do nothing; both are answered with the conversation that needs
// no raw mode at all.
func drawableTerminal(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch getenv("TERM") {
	case "", "dumb":
		return false
	}
	return true
}

// lineAsker prints a prompt and reads a line, which is what this command has
// always done and what it still does where a widget cannot be drawn.
type lineAsker struct{ *prompter }

func (l lineAsker) opening() string {
	return "Press Enter to accept the value in brackets. Nothing is written until you confirm.\n"
}

func (l lineAsker) offer(_ context.Context, prompt string, proposed bool) (bool, error) {
	return l.confirm(prompt, proposed)
}

func (l lineAsker) question(_ context.Context, question wizard.Question, indent string) (reply, error) {
	prompt := indent + question.Prompt

	switch question.Kind {
	case wizard.KindConfirm:
		// The answer goes back as the word the flow asked for. The prompt is
		// where "y" and "yes" are the same thing, and where a word that is
		// neither is asked again.
		yes, err := l.confirm(prompt, question.Proposed == "y")
		if err != nil {
			return reply{}, err
		}
		if yes {
			return reply{value: "y"}, nil
		}
		return reply{value: "n"}, nil
	case wizard.KindChoice:
		value, err := l.ask(fmt.Sprintf("%s (%s)", prompt, strings.Join(question.Options, "/")), question.Proposed)
		return reply{value: value}, err
	default:
		value, err := l.ask(prompt, question.Proposed)
		return reply{value: value}, err
	}
}

// widgetAsker draws each question with internal/ui/ask, inline.
//
// One program per question, and no alternate screen: it draws the question,
// takes the answer, prints the answered question as a permanent line, and
// exits. The transcript therefore accumulates in the scrollback exactly as it
// does under the line asker, which is the property ADR-062 chose a conversation
// for and the reason this widget is inline rather than a screen (ADR-084).
type widgetAsker struct {
	in  io.Reader
	out io.Writer
}

func (w widgetAsker) opening() string {
	return "Press Enter to accept what a question proposes. Nothing is written until you confirm.\n"
}

func (w widgetAsker) question(ctx context.Context, question wizard.Question, indent string) (reply, error) {
	return w.run(ctx, asking{
		field:    widget.New().Ask(question),
		question: question,
		indent:   indent,
		// Stepping back is offered on every one of the flow's questions.
		// Whether there is anything behind this one is the flow's to answer:
		// esc returns a step back, and the conversation asks Wizard.Back.
		back: true,
	})
}

func (w widgetAsker) offer(ctx context.Context, prompt string, proposed bool) (bool, error) {
	question := wizard.Question{Kind: wizard.KindConfirm, Prompt: prompt, Proposed: "n"}
	if proposed {
		question.Proposed = "y"
	}
	answer, err := w.run(ctx, asking{field: widget.New().Ask(question), question: question})
	if err != nil {
		return false, err
	}
	return answer.value == "y", nil
}

// run draws one question and waits for it to be answered.
func (w widgetAsker) run(ctx context.Context, model asking) (reply, error) {
	program := tea.NewProgram(model,
		tea.WithContext(ctx), tea.WithInput(w.in), tea.WithOutput(w.out))

	final, err := program.Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, context.Canceled) {
			return reply{}, errQuestionLeft
		}
		return reply{}, fmt.Errorf("drawing the question: %w", err)
	}
	answered, ok := final.(asking)
	if !ok || !answered.done {
		// A program that ended without deciding anything is one that was
		// stopped. Reading its zero value as an answer would answer a question
		// nobody answered, which is the one reading that cannot be taken back.
		return reply{}, errQuestionLeft
	}
	return answered.reply, nil
}

// errQuestionLeft reports a conversation the user stopped at a question.
//
// It wraps context.Canceled so that the exit code says the run was interrupted
// rather than that the command failed, and so that nothing is printed over the
// transcript the user pressed Ctrl-C to keep.
var errQuestionLeft = fmt.Errorf(
	"stopped at a question, so nothing was written: %w", context.Canceled)

// asking is one question, drawn inline and answered with a cursor.
type asking struct {
	// field is the question widget, which is the one the dashboard's dialog
	// draws with too.
	field widget.Model
	// question is what is being asked, kept for the line this leaves behind.
	question wizard.Question
	// indent is what the conversation puts in front of this question's lines.
	indent string
	// back reports whether esc offers the question before this one. The offers
	// after the file is written have nothing behind them.
	back bool

	// done reports that the program decided something, and reply is what.
	done  bool
	reply reply
	// left reports that the user stopped the conversation here. It is separate
	// from done because a reply nobody gave must never be read as an answer.
	left bool
}

func (a asking) Init() tea.Cmd { return nil }

func (a asking) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		// The field is drawn under the question's own indent, and the cursor
		// sits one cell past the value.
		if width := message.Width - len(a.indent) - 4; width > 0 {
			a.field.SetWidth(min(width, 100))
		}
		return a, nil

	case tea.KeyMsg:
		if message.String() == "ctrl+c" {
			// Left rather than answered, and the frame goes with it: a question
			// with a cursor in it, left on the screen after the command has
			// gone, reads as one still waiting for an answer. What stays is
			// everything that was already permanent.
			a.left = true
			return a, tea.Quit
		}

		field, result, cmd := a.field.Update(message)
		a.field = field
		switch result.Outcome {
		case widget.Answered:
			a.done, a.reply = true, reply{value: result.Answer}
			// The answered question, as one permanent line above the frame this
			// is about to stop drawing. Nothing already printed is erased or
			// rewritten: rewriting emitted lines is the part of terminal
			// handling that breaks differently on every emulator (ADR-084).
			return a, tea.Sequence(tea.Println(a.transcript(result.Answer)), tea.Quit)
		case widget.SteppedBack:
			if !a.back {
				return a, nil
			}
			a.done, a.reply = true, reply{back: true}
			return a, tea.Quit
		}
		return a, cmd
	}
	return a, nil
}

func (a asking) View() string {
	if a.done || a.left {
		// Finished with, either way. What is left behind is whatever was
		// printed above the frame and nothing that was drawn to take an answer.
		return ""
	}
	body := indentBlock(strings.TrimRight(a.field.View(), "\n"), a.indent)
	return body + "\n\n" + a.indent + a.field.Hints(a.back)
}

// transcript is the line the widget leaves behind: the question, answered.
//
// It is what the conversation would have shown had the answer been typed, and
// it is what somebody debugging their own configuration reads back — so it
// records the value the flow was given rather than the keys that chose it. An
// empty answer takes what was proposed, which is what the flow does with it;
// an empty answer to a question that proposes nothing is a decision too, and
// says so in a word rather than in an absence.
func (a asking) transcript(answer string) string {
	value := widget.Label(a.question.Kind, answer)
	if a.question.Kind == wizard.KindText {
		switch {
		case answer != "":
			value = answer
		case a.question.Proposed != "":
			value = a.question.Proposed
		default:
			value = "none"
		}
	}

	prompt := a.indent + a.question.Prompt
	if strings.HasSuffix(prompt, "?") {
		// A question that asks itself needs no colon after its question mark.
		return prompt + " " + value
	}
	return prompt + ": " + value
}

// indentBlock puts the conversation's indent in front of every drawn line.
//
// An empty line is left empty, because indenting one writes trailing spaces
// into the scrollback, and the blank between a paragraph and the next is the
// one thing on the screen that has to be nothing at all.
func indentBlock(block, indent string) string {
	if indent == "" {
		return block
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}
