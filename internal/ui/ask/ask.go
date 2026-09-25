// Package ask draws one of the project wizard's questions and takes its answer.
//
// ADR-063 split the wizard into one flow and two askers, so a question added
// once appears in both. The flow describes each question richly enough to be
// drawn well — a closed question's options, a text question's candidates, a
// step back out of the answer before — and the command line used about a third
// of that. This package is the interface, extracted from the dashboard's dialog
// so that `feat project init` draws the second rendering of a question rather
// than a third (ADR-084).
//
// It is a Bubble Tea sub-model over one wizard.Question and it decides nothing:
// which question comes next, what it proposes, and whether an answer is
// acceptable are internal/wizard's. It owns a cursor, a field, the keys that
// move them, and the styles it draws with, which live here so the two callers
// cannot drift apart.
package ask

import (
	"slices"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/wizard"
)

// Outcome is what a key press did to a question.
type Outcome int

const (
	// Continued is a key that moved the cursor or the field and decided
	// nothing. It is the zero value, so a result nobody filled in is the
	// harmless one.
	Continued Outcome = iota
	// Answered is a question with an answer to hand to the flow.
	Answered
	// SteppedBack is the user asking for the question before this one. The
	// caller decides whether there is one: this model knows about a question,
	// not about a conversation.
	SteppedBack
)

// Result is what one key press decided.
type Result struct {
	// Outcome is what the key did.
	Outcome Outcome
	// Answer is what to give the flow, and is set only when the outcome is
	// Answered. It is what is in the field or under the cursor, which is what
	// the user was looking at when they pressed Enter.
	Answer string
}

// Model is one question, drawn and answered.
type Model struct {
	// question is what is being asked. It is a whole wizard.Question rather
	// than the parts this model draws, because what the flow says about a
	// question is what makes it drawable: the notes it is asked in light of,
	// the candidates it can be completed to, whether an empty answer is one.
	question wizard.Question
	// input is the field a text answer is typed into.
	input textinput.Model
	// cursor is which of a closed question's options is under it.
	cursor int
	// context is how many cells the prose around the question is folded into. It
	// is not the field's width: the field is one line inside the block and is
	// capped where the block is not. Nought means a caller that was not told, and
	// the prose is drawn as the flow wrote it.
	context int
}

// New builds the question widget.
func New() Model {
	field := textinput.New()
	field.Prompt = ""
	field.CharLimit = 500
	// On for every question, and given nothing to complete on most of them. A
	// flag that has to be turned off again leaves a question holding the last
	// one's candidates, so Tab would answer with a value from a different part of
	// the file.
	field.ShowSuggestions = true

	return Model{input: field}
}

// Ask puts a question, replacing whatever was being answered before it. The
// proposal is a placeholder rather than the field's contents, so Enter takes it
// and typing replaces it; in the field, typing appends, and an identifier
// proposed from the working directory grows the answer onto its end. Tab is how
// the proposal gets into the field deliberately, for the answer a user wants
// with three characters changed (ADR-077).
func (m Model) Ask(question wizard.Question) Model {
	m.question = question
	m.cursor = choiceIndex(question)
	m.input.SetValue("")
	m.input.Placeholder = question.Proposed
	m.input.SetSuggestions(question.Candidates)
	if question.Kind == wizard.KindText {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	return m
}

// SetWidth sets how many cells the field is drawn in. A caller that has not
// been told how wide it is leaves this alone, and the field draws itself. This
// is the field and not the block around it; see SetContextWidth.
func (m *Model) SetWidth(cells int) { m.input.Width = cells }

// SetContextWidth sets how many cells the prose around the question is folded
// into, which is what Context draws.
//
// `Detail` is authored as pre-wrapped lines; a `Notes` entry is one long
// string, and the note listing which services a repository builds runs to a
// hundred and sixty cells. A dialog that truncates such a line also reads it as
// exactly as wide as it is allowed, so the box took its full allowance to show
// a sentence it had already cut short. This is separate from SetWidth because
// the dashboard's field is the block less its own indent and capped at a
// hundred cells, and `feat project init`'s is the terminal less the question's
// indent.
//
// Nought is the default and means the prose is drawn as the flow wrote it,
// which is what `feat project init` uses: it prints these fields itself so they
// stay in the scrollback after the widget has exited (ADR-084).
func (m *Model) SetContextWidth(cells int) { m.context = cells }

// Value is the answer as it stands, which is what Enter would send.
func (m Model) Value() string { return m.input.Value() }

// CurrentSuggestion is the candidate the field is completing what was typed to,
// or empty where there is none.
func (m Model) CurrentSuggestion() string { return m.input.CurrentSuggestion() }

// Update applies one key press and says what it decided. The returned command
// is the field's own — the cursor blink — and is nil for everything else, so a
// caller with no use for one may drop it.
func (m Model) Update(key tea.KeyMsg) (Model, Result, tea.Cmd) {
	switch key.String() {
	case "esc":
		return m, Result{Outcome: SteppedBack}, nil

	case "enter":
		return m.answer()

	case "tab":
		// Only where the field holds a candidate already or holds nothing at all;
		// anything else is a prefix, and the widget completes those itself.
		if taken, ok := m.take(); ok {
			return taken, Result{}, nil
		}
	}

	if m.question.Kind == wizard.KindText {
		field, cmd := m.input.Update(key)
		m.input = field
		return m, Result{}, cmd
	}

	// A closed question is a list, and the plain movement keys move it. Nothing
	// is typed into one, so they cannot be anything else here.
	options := m.options()
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(options)-1 {
			m.cursor++
		}
	}
	return m, Result{}, nil
}

// answer is what Enter sends: what is in the field, or what is under the
// cursor.
func (m Model) answer() (Model, Result, tea.Cmd) {
	value := m.input.Value()
	if m.question.Kind != wizard.KindText {
		options := m.options()
		if m.cursor < 0 || m.cursor >= len(options) {
			// A closed question with nothing under the cursor is not answerable,
			// and answering it with an empty string would be an answer the flow
			// would have to refuse.
			return m, Result{}, nil
		}
		value = options[m.cursor]
	}
	return m, Result{Outcome: Answered, Answer: value}, nil
}

// take puts a candidate in the field, and reports whether it had one to put
// there. The widget's own completion works on a prefix, so it cannot help the
// empty field, which is where the proposal is and where an absolute path had to
// be typed from its first character. An empty field therefore takes the
// proposal, a field holding one candidate exactly steps to the next, and
// everything between is the widget's prefix completion. Enter still sends what
// is in the field: Tab moves a value into it and never past it (ADR-077).
func (m Model) take() (Model, bool) {
	if m.question.Kind != wizard.KindText || len(m.question.Candidates) == 0 {
		return m, false
	}
	value := m.input.Value()
	at := slices.Index(m.question.Candidates, value)
	if value != "" && at < 0 {
		return m, false
	}
	// An empty field is at -1, so the first Tab takes the proposal and each one
	// after it steps along the list and around it.
	m.input.SetValue(m.question.Candidates[(at+1)%len(m.question.Candidates)])
	m.input.CursorEnd()
	return m, true
}

// options are the answers a closed question offers, as the flow wants them
// given. What the user reads is Label below: a confirm question is answered
// with a letter and is worth drawing as a word.
func (m Model) options() []string {
	if m.question.Kind == wizard.KindConfirm {
		return []string{"y", "n"}
	}
	return m.question.Options
}

// Label is how one of a closed question's answers is drawn. It is exported
// because the answer outlives the widget: `feat project init` leaves the
// answered question in its transcript, and a transcript recording "y" where the
// user chose "yes" would be a second vocabulary for one answer.
func Label(kind wizard.Kind, option string) string {
	if kind != wizard.KindConfirm {
		return option
	}
	if option == "y" {
		return "yes"
	}
	return "no"
}

// choiceIndex is where the cursor starts on a closed question: on what the flow
// proposes, so that Enter accepts it as it does everywhere else.
func choiceIndex(question wizard.Question) int {
	if question.Kind == wizard.KindText {
		return 0
	}
	if question.Kind == wizard.KindConfirm {
		if question.Proposed == "n" {
			return 1
		}
		return 0
	}
	for i, option := range question.Options {
		if option == question.Proposed {
			return i
		}
	}
	return 0
}
