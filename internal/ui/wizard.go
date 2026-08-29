package ui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/ui/ask"
	"github.com/ma8el/feat/internal/wizard"
)

// wizardStep is which part of configuring a project the dialog is on.
//
// The questions are the wizard's and it says how many there are; these are the
// three things that happen around them, and they are the dashboard's because
// each is a decision with a screen of its own.
type wizardStep int

const (
	// wizardAsking is a question from the flow.
	wizardAsking wizardStep = iota
	// wizardReviewing is the composed file, before it is written.
	wizardReviewing
	// wizardChecking is the written project against this machine. The questions
	// could not ask the host anything — whether the Compose service exists,
	// whether the agent is installed, whether a remote resolves — so this is
	// where those answers arrive (ADR-064).
	wizardChecking
	// wizardRegistering is the offer that follows a written file.
	wizardRegistering
	// wizardDone is what happened, and nothing left to answer.
	wizardDone
)

// wizardModel configures a project by asking the same questions
// `feat project init` asks (ADR-063).
//
// It holds the flow and draws it. Which question comes next, what it proposes,
// and whether an answer is acceptable are decided in internal/wizard, so this
// screen can be read as what it is: a renderer with a cursor.
type wizardModel struct {
	backend Backend
	// flow is the questions. It is a pointer, and it is mutated only inside the
	// commands below — never while a key is being handled — which is what busy
	// enforces: one answer is in flight at a time, and the keyboard is closed
	// while it is.
	flow *wizard.Wizard

	step     wizardStep
	question wizard.Question
	// asked reports that a question has arrived. Until one has, the dialog is
	// waiting for the machine rather than for the user.
	asked bool

	// input is the question widget: the field, the option list, and the keys
	// that move them. It is the same one `feat project init` draws, so a
	// question is rendered once and asked twice (ADR-084).
	input  ask.Model
	cursor int
	// scroll is the first line of the review that is drawn. A configuration is
	// longer than a dialog, and the user is being asked to confirm all of it.
	scroll int

	review wizard.Review
	// check is what `feat doctor` found about the project once it existed. It is
	// the same report the dashboard's own diagnosis screen draws, from the same
	// backend call.
	check diagnosisModel
	// path is the file that was written, and project the one the daemon
	// recorded. Both are empty until they are true.
	path    string
	project string

	busy   bool
	err    error
	status string

	width, height int
}

// Messages the wizard sends itself.
type (
	// wizardReadyMsg carries the built flow, or the reason there is none.
	wizardReadyMsg struct {
		flow *wizard.Wizard
		err  error
	}
	// wizardAnsweredMsg reports that an answer was applied, or refused. A
	// refusal is the user's to correct and never ends anything.
	wizardAnsweredMsg struct{ err error }
	// wizardReviewMsg carries the composed configuration.
	wizardReviewMsg struct {
		review wizard.Review
		err    error
	}
	// wizardWrittenMsg carries the file that was written.
	wizardWrittenMsg struct {
		path string
		err  error
	}
	// wizardRegisteredMsg carries what the daemon recorded.
	wizardRegisteredMsg struct {
		project string
		err     error
	}
	// wizardClosedMsg ends the dialog. The dashboard reads state again when it
	// arrives, because a project may have appeared while it was open.
	wizardClosedMsg struct{}
)

func newWizard(backend Backend) wizardModel {
	return wizardModel{backend: backend, input: ask.New()}
}

// Init builds the flow, which is the one thing the dashboard cannot do itself.
func (w wizardModel) Init() tea.Cmd {
	backend := w.backend
	return func() tea.Msg {
		flow, err := backend.NewWizard()
		return wizardReadyMsg{flow: flow, err: err}
	}
}

func (w *wizardModel) resize(width, height int) {
	w.width, w.height = width, height
	if width > 4 {
		w.input.SetWidth(min(width-6, 100))
	}
}

func (w wizardModel) Update(message tea.Msg) (wizardModel, tea.Cmd) {
	switch message := message.(type) {
	case wizardReadyMsg:
		w.busy = false
		if message.err != nil {
			w.err = message.err
			return w, nil
		}
		w.flow = message.flow
		return w.advance()

	case wizardAnsweredMsg:
		w.busy = false
		if message.err != nil {
			// A refused answer is the user's to correct: the same question comes
			// back, with what they typed still in it.
			w.err = message.err
			return w, nil
		}
		w.err = nil
		return w.advance()

	case wizardReviewMsg:
		w.busy = false
		if message.err != nil {
			w.err = message.err
			return w, nil
		}
		w.err, w.review, w.scroll, w.step = nil, message.review, 0, wizardReviewing
		return w, nil

	case wizardWrittenMsg:
		w.busy = false
		if message.err != nil {
			w.err = message.err
			return w, nil
		}
		w.err, w.path, w.cursor, w.step = nil, message.path, 0, wizardChecking
		// Run rather than offered. The user has just asked for this project to
		// exist and is waiting for it either way; the checks change nothing, and
		// what they find is the difference between a file and a project that
		// works (ADR-064).
		w.check = w.check.start(w.flow.ID())
		return w, diagnose(w.backend, w.flow.ID())

	case diagnosedMsg:
		w.check = w.check.apply(message)
		return w, nil

	case wizardRegisteredMsg:
		w.busy = false
		w.step = wizardDone
		if message.err != nil {
			// The file exists whatever the daemon said, so this is not a failed
			// configuration: it is a project that is written and not registered,
			// and the last screen says so and says what registers it.
			w.err = message.err
			return w, nil
		}
		w.err, w.project = nil, message.project
		return w, nil

	case tea.KeyMsg:
		return w.key(message)
	}
	return w, nil
}

// advance reads the next question, or moves to the review when there is none.
func (w wizardModel) advance() (wizardModel, tea.Cmd) {
	question, ok := w.flow.Step()
	if !ok {
		return w.compose()
	}

	w.question, w.asked = question, true
	w.input = w.input.Ask(question)
	return w, nil
}

// compose asks for the configuration the answers make.
func (w wizardModel) compose() (wizardModel, tea.Cmd) {
	flow := w.flow
	w.busy, w.status = true, "composing the configuration…"
	return w, func() tea.Msg {
		review, err := flow.Review()
		return wizardReviewMsg{review: review, err: err}
	}
}

func (w wizardModel) key(key tea.KeyMsg) (wizardModel, tea.Cmd) {
	if w.busy {
		// An answer that reaches the machine — a path Git is being asked about —
		// takes a moment, and a second key press must not start a second one.
		// Leaving is still allowed, because waiting is not a commitment.
		if key.String() == "ctrl+c" {
			return w, closeWizard
		}
		return w, nil
	}
	if key.String() == "ctrl+c" {
		return w, closeWizard
	}

	switch w.step {
	case wizardAsking:
		return w.askingKey(key)
	case wizardReviewing:
		return w.reviewingKey(key)
	case wizardChecking:
		return w.checkingKey(key)
	case wizardRegistering:
		return w.registeringKey(key)
	default:
		return w, closeWizard
	}
}

// askingKey answers a question, or steps back out of the previous one.
func (w wizardModel) askingKey(key tea.KeyMsg) (wizardModel, tea.Cmd) {
	if !w.asked {
		// Nothing has been asked yet, so there is nothing to answer and nothing
		// to step back to.
		if key.String() == "esc" {
			return w, closeWizard
		}
		return w, nil
	}

	field, result, cmd := w.input.Update(key)
	w.input = field
	switch result.Outcome {
	case ask.Answered:
		return w.answer(result.Answer)
	case ask.SteppedBack:
		return w.back()
	}
	return w, cmd
}

// answer hands what the user gave to the flow.
func (w wizardModel) answer(value string) (wizardModel, tea.Cmd) {
	flow := w.flow
	w.busy, w.status, w.err = true, "", nil
	return w, func() tea.Msg {
		return wizardAnsweredMsg{err: flow.Answer(context.Background(), value)}
	}
}

// back returns to the previous question, undoing what its answer changed.
//
// A dialog that could only go forwards would be a worse conversation than the
// one at a shell, where the whole of it is in the scrollback: here the answers
// are the only record, so stepping back is what reading back looks like.
func (w wizardModel) back() (wizardModel, tea.Cmd) {
	if !w.flow.Back() {
		return w, closeWizard
	}
	w.err = nil
	return w.advance()
}

// reviewingKey confirms or scrolls the configuration.
func (w wizardModel) reviewingKey(key tea.KeyMsg) (wizardModel, tea.Cmd) {
	switch key.String() {
	case "enter":
		flow, backend := w.flow, w.backend
		w.busy, w.status = true, "writing the configuration…"
		return w, func() tea.Msg {
			path, err := backend.WriteProject(flow)
			return wizardWrittenMsg{path: path, err: err}
		}

	case "esc":
		w.step = wizardAsking
		return w.back()

	case "up", "k":
		if w.scroll > 0 {
			w.scroll--
		}
	case "down", "j":
		if w.scroll < w.reviewLines()-1 {
			w.scroll++
		}
	case "pgup":
		w.scroll = max(0, w.scroll-w.reviewHeight())
	case "pgdown":
		w.scroll = min(max(0, w.reviewLines()-1), w.scroll+w.reviewHeight())
	}
	return w, nil
}

// checkingKey reads the diagnosis, or moves past it.
//
// Nothing here can fail the setup. The file exists whatever the checks found,
// and what they found is a list of things to fix rather than a reason to undo
// the project — so the only way through is forward.
func (w wizardModel) checkingKey(key tea.KeyMsg) (wizardModel, tea.Cmd) {
	switch key.String() {
	case "enter", "esc":
		w.cursor, w.step = 0, wizardRegistering
		return w, nil

	case "r":
		if w.check.running {
			return w, nil
		}
		w.check = w.check.start(w.flow.ID())
		return w, diagnose(w.backend, w.flow.ID())

	case "up", "k":
		w.check = w.check.scrollBy(-1)
	case "down", "j":
		w.check = w.check.scrollBy(1)
	case "pgup":
		w.check = w.check.scrollBy(-w.reviewHeight())
	case "pgdown":
		w.check = w.check.scrollBy(w.reviewHeight())
	}
	return w, nil
}

// registeringKey answers the offer that follows a written file.
func (w wizardModel) registeringKey(key tea.KeyMsg) (wizardModel, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		w.cursor = 0
	case "down", "j":
		w.cursor = 1
	case "esc":
		w.step = wizardDone
		return w, nil
	case "enter":
		if w.cursor != 0 {
			w.step = wizardDone
			return w, nil
		}
		backend, id := w.backend, w.flow.ID()
		w.busy, w.status = true, "registering "+id+"…"
		return w, func() tea.Msg {
			registration, err := backend.RegisterProject(context.Background(), id)
			return wizardRegisteredMsg{project: registration.Project.ID, err: err}
		}
	}
	return w, nil
}

// closeWizard ends the dialog.
func closeWizard() tea.Msg { return wizardClosedMsg{} }

// registration reports what the last screen has to say about the daemon.
func (w wizardModel) registration() string {
	switch {
	case w.project != "":
		return "registered " + w.project
	case w.err != nil:
		return "not registered: " + w.err.Error()
	default:
		return "not registered"
	}
}

// reviewLines is how many lines the composed configuration has.
func (w wizardModel) reviewLines() int {
	return strings.Count(strings.TrimRight(string(w.review.Text), "\n"), "\n") + 1
}

// reviewHeight is how many of them the dialog can draw at once.
//
// The count below is what every other line of this screen spends: the trail and
// the blank under it, the path and the blank under it, the "more lines" note,
// the blank and the sentence saying nothing has been written, the status line
// with a blank on each side, the blank above the hints and the hints, and what
// is left of the dialog's own heading and rule once preparationSize has taken
// the card's share of it. Getting it wrong is not cosmetic — the box clamps from
// the bottom, so an overrun costs the user the assurance that nothing has been
// written and the keys that write it.
func (w wizardModel) reviewHeight() int {
	const chrome = 14
	return max(3, w.height-chrome)
}
