package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/brief"
	"github.com/ma8el/feat/internal/paths"
)

// step is where the user is in task preparation.
//
// The order is FR-TASK-003's: what the task is, which repositories it touches,
// and then everything Feat resolved from that, which is the last thing shown
// before anything is created.
type step int

const (
	stepProject step = iota
	// stepSource is where a task's brief comes from, asked once and after the
	// project, because two of its three answers need the project to mean
	// anything: the tracker is configured per project (ADR-071) and the file
	// completion is seeded from the project's own checkouts (ADR-083).
	stepSource
	stepBrief
	stepRepositories
	stepReview
	// stepTickets is the project's tickets offered as a selection, and
	// stepImport is the file screen. Both are last rather than in sequence
	// because neither is a stage of preparation: each is one answer to the
	// source question being worked out, so each returns to that step and the
	// trail says the user is on it.
	stepTickets
	stepImport
)

// sourceOption is one answer to where a task's brief comes from.
type sourceOption struct {
	// kind is the source api.Source records for it.
	kind string
	// label is what the option is called, and note what it does.
	label string
	note  string
}

// sourceOptions are the answers, in the order they are offered.
//
// "write it here" is first and the cursor opens on it, so Enter-Enter reproduces
// the preparation this screen has always had and nobody's habit breaks.
var sourceOptions = []sourceOption{
	{kind: "prompt", label: "write it here", note: "type it, or press ctrl+e for $EDITOR"},
	{kind: "ticket", label: "from a ticket", note: "run this project's tracker and choose one"},
	{kind: "markdown", label: "from a Markdown file", note: "import a file you have already written"},
}

// access modes a repository can be selected with, in the order the selection
// screen cycles them.
var accessModes = []string{"omitted", "read_only", "read_write"}

// selectionRow is one repository's line on the selection screen.
type selectionRow struct {
	// repository identifies the repository within the project.
	repository string
	// access is omitted, read_only, or read_write.
	access string
	// permitted are the modes the project's configuration allows.
	permitted []string
	// declared is the project's configured default access, shown so the user
	// can see why a repository cannot be promoted.
	declared string
	// ref is a revision for a project whose base policy is explicit.
	ref string
}

// prepareModel is the task preparation screen.
type prepareModel struct {
	backend Backend

	step     step
	projects []api.Project
	project  string
	// draft is the record the daemon holds, created when the user resolves.
	draft *api.Task
	plan  *api.DraftPlan

	title textinput.Model
	brief textarea.Model
	// source records where the brief came from: a typed prompt, a ticket, or a
	// Markdown file.
	source api.Source
	// imported is a brief supplied by `feat implement --file`.
	imported string
	// preselected reports that a flag answered the source question. A flag is an
	// answer, so the step is skipped — and esc from the brief then keeps the
	// meaning it had before the step existed, because there is no answer of the
	// user's own to return to (ADR-083).
	preselected bool

	// path is the file the import screen names, and candidates what tab
	// completes it to. The candidates are rebuilt from the field after every key
	// that could have changed what directory it names.
	path       textinput.Model
	candidates []string

	// tickets is what the project's tracker printed, read when the user asked
	// for it. Nothing holds it between screens: Feat passes the command no
	// filter, so a held list could not be re-filtered, and a ticket that changed
	// is found by running the command again (ADR-071).
	tickets []api.Ticket
	// ticketsReadAt is when that list was read, which is what a snapshot taken
	// from one of them records.
	ticketsReadAt time.Time
	// ticket is the reference `feat implement --ticket` named, matched against
	// what the command emitted once the project is known and cleared after.
	ticket string

	selection []selectionRow
	cursor    int
	focus     int

	// planFirst asks the task's agent to plan before it acts, and travels with
	// the confirmation. It is local model state and never a request: a value the
	// user just chose cannot drift underneath the screen, and a re-plan to record
	// it would be a fetch and a base resolution in every repository.
	planFirst bool

	busy   bool
	err    error
	status string

	width, height int
}

// preparedMsg ends preparation: a launched task, a failure, or a cancellation.
type preparedMsg struct {
	task *api.Task
	err  error
}

// Messages preparation sends itself.
type (
	projectsMsg struct {
		projects []api.Project
		err      error
	}
	planMsg struct {
		draft *api.Task
		plan  *api.DraftPlan
		err   error
	}
	editedMsg struct {
		brief string
		err   error
	}
	draftDiscardedMsg struct{ err error }
	ticketsMsg        struct {
		list api.TicketList
		err  error
		// want is the reference `--ticket` named, empty for a list the user
		// asked to browse.
		want string
	}
)

// prepareStart is what a caller opens preparation with.
//
// It is a value rather than a list of arguments because every field is
// optional and most of them are strings, so a call site that swapped two of
// them would compile.
type prepareStart struct {
	// project preselects a project, from --project.
	project string
	// brief is an imported Markdown brief, read by the caller.
	brief string
	// source records where that brief came from.
	source api.Source
	// ticket is the reference --ticket named.
	ticket string
	// planFirst presets the review step's start mode, from --plan.
	planFirst bool
}

func newPrepare(backend Backend, start prepareStart) prepareModel {
	title := textinput.New()
	title.Placeholder = "what the task is, in a few words"
	title.CharLimit = 120
	title.Prompt = ""

	path := textinput.New()
	path.Placeholder = "the Markdown file the brief is in"
	path.CharLimit = 500
	path.Prompt = ""
	// The completion is the wizard dialog's, on the same widget: a path is a
	// value the user takes into the field and edits rather than retypes
	// (ADR-077).
	path.ShowSuggestions = true

	body := textarea.New()
	body.Placeholder = "what the agent should do, in Markdown"
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.SetHeight(10)

	model := prepareModel{
		backend:   backend,
		project:   start.project,
		title:     title,
		brief:     body,
		path:      path,
		source:    start.source,
		imported:  start.brief,
		ticket:    start.ticket,
		planFirst: start.planFirst,
		// --file was read before this screen opened and --ticket is looked up as
		// soon as the project is known. Either way the question has an answer.
		preselected: start.brief != "" || start.ticket != "",
	}
	if start.brief != "" {
		model.brief.SetValue(start.brief)
		model.title.SetValue(titleFrom(start.brief))
	}
	if model.source.Kind == "" {
		model.source.Kind = "prompt"
	}
	return model
}

// restart returns a fresh preparation screen, so that opening one from the
// dashboard never shows the previous task's answers.
//
// It carries no flags, so nothing has answered the source question and the fresh
// run reaches that step as soon as the project is known.
func (p prepareModel) restart() prepareModel {
	fresh := newPrepare(p.backend, prepareStart{project: p.project, source: api.Source{Kind: "prompt"}})
	fresh.projects = p.projects
	fresh.width, fresh.height = p.width, p.height
	fresh.resize(p.width, p.height)
	return fresh
}

func (p prepareModel) Init() tea.Cmd {
	backend := p.backend
	return func() tea.Msg {
		projects, err := backend.Projects(context.Background())
		return projectsMsg{projects: projects, err: err}
	}
}

func (p *prepareModel) resize(width, height int) {
	p.width, p.height = width, height
	if width > 4 {
		p.title.Width = min(width-4, 100)
		p.brief.SetWidth(min(width-4, 100))
		p.path.Width = min(width-10, 100)
	}
	if height > 16 {
		p.brief.SetHeight(min(height-14, 20))
	}
}

func (p prepareModel) Update(message tea.Msg) (prepareModel, tea.Cmd) {
	switch message := message.(type) {
	case projectsMsg:
		if message.err != nil {
			p.err = message.err
			return p, nil
		}
		p.projects = message.projects
		return p.chooseProject()

	case planMsg:
		p.busy = false
		p.draft = message.draft
		if message.err != nil {
			p.err = message.err
			// The draft still exists and is still editable, so the user goes
			// back to the selection rather than losing what they typed.
			p.step = stepRepositories
			return p, nil
		}
		p.err = nil
		p.plan = message.plan
		p.step = stepReview
		return p, nil

	case editedMsg:
		if message.err != nil {
			p.err = message.err
			return p, nil
		}
		p.brief.SetValue(message.brief)
		if strings.TrimSpace(p.title.Value()) == "" {
			p.title.SetValue(titleFrom(message.brief))
		}
		return p, nil

	case ticketsMsg:
		return p.ticketsRead(message)

	case draftDiscardedMsg:
		if message.err != nil {
			// The draft is still recorded and this screen is no longer the one
			// that owns it, so the user is told rather than left with a record
			// nothing will mention again.
			p.err = message.err
		}
		return p, nil

	case tea.KeyMsg:
		return p.key(message)
	}
	return p, nil
}

func (p prepareModel) key(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	if p.busy {
		// Resolving fetches, which can take a moment. Only cancelling is
		// accepted while it runs, so a second key press cannot start a second
		// fetch of the user's repositories.
		if key.String() == "ctrl+c" {
			return p, func() tea.Msg { return preparedMsg{} }
		}
		return p, nil
	}

	switch key.String() {
	case "ctrl+c":
		return p, p.abandon()
	case "esc":
		return p.back()
	}

	switch p.step {
	case stepProject:
		return p.projectKey(key)
	case stepSource:
		return p.sourceKey(key)
	case stepBrief:
		return p.briefKey(key)
	case stepRepositories:
		return p.repositoryKey(key)
	case stepReview:
		return p.reviewKey(key)
	case stepTickets:
		return p.ticketKey(key)
	case stepImport:
		return p.pathKey(key)
	}
	return p, nil
}

// abandon leaves preparation, cancelling a draft the daemon already holds.
//
// A draft only exists from the moment the user resolves one, and cancelling it
// removes nothing: nothing has been created for a draft (FR-TASK-003).
func (p prepareModel) abandon() tea.Cmd {
	if p.draft == nil {
		return func() tea.Msg { return preparedMsg{} }
	}

	backend, id := p.backend, p.draft.ID
	return func() tea.Msg {
		if _, err := backend.CancelDraft(context.Background(), id); err != nil {
			return preparedMsg{err: err}
		}
		return preparedMsg{}
	}
}

// back steps one screen towards the beginning, or leaves preparation.
func (p prepareModel) back() (prepareModel, tea.Cmd) {
	switch p.step {
	case stepReview:
		p.step = stepRepositories
		return p, nil
	case stepTickets, stepImport:
		// Back out without taking one. Both screens belong to the source step
		// and there is one way into each, so backing out of either returns to
		// the question it was answering — and nothing has to remember where the
		// user came from, because there is nowhere else they can have come from
		// (ADR-083).
		return p.enterSourceStep()
	case stepRepositories:
		p.step = stepBrief
		return p, p.focusBrief()
	case stepBrief:
		// This destroys what is in the editor, deliberately and with no guard.
		// Every forward path back to the brief passes through a selection, and
		// every selection resets it, so the key means "start over" and a key
		// whose whole purpose is that does not need to ask (ADR-083).
		if !p.preselected {
			return p.enterSourceStep()
		}
		// Where a flag answered the source question there is no answer of the
		// user's own to return to, so the rule this screen has always had
		// stands.
		if len(p.projects) > 1 {
			p.step = stepProject
			return p, nil
		}
	case stepSource:
		if len(p.projects) > 1 {
			p.step = stepProject
			return p, nil
		}
	}
	return p, p.abandon()
}

// chooseProject moves past the project step when there is nothing to choose.
func (p prepareModel) chooseProject() (prepareModel, tea.Cmd) {
	if len(p.projects) == 0 {
		// The first step is writing a configuration, not registering one: a user
		// with nothing configured has nothing for `feat project add` to take, and
		// the wizard offers registration itself once the file exists.
		//
		// It is named as a key rather than as a command, because this screen has
		// the keyboard and the wizard is one press away from the screen behind it
		// (ADR-063). `feat project init` is the same conversation for somebody who
		// would rather leave.
		p.err = errors.New("no project is registered; press esc, then p, to configure one")
		return p, nil
	}
	if p.project != "" {
		if _, ok := p.registered(p.project); !ok {
			p.err = fmt.Errorf("no project %s is registered", p.project)
			p.project = ""
			p.step = stepProject
			return p, nil
		}
		return p.enterSource()
	}
	if len(p.projects) == 1 {
		p.project = p.projects[0].ID
		return p.enterSource()
	}

	p.step = stepProject
	return p, nil
}

func (p prepareModel) registered(id string) (api.Project, bool) {
	for _, project := range p.projects {
		if project.ID == id {
			return project, true
		}
	}
	return api.Project{}, false
}

func (p prepareModel) projectKey(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.projects)-1 {
			p.cursor++
		}
	case "enter":
		if p.cursor < len(p.projects) {
			p.project = p.projects[p.cursor].ID
			return p.enterSource()
		}
	}
	return p, nil
}

// enterSource asks where the task's brief comes from.
//
// The question is asked once and after the project, because two of its three
// answers need the project to mean anything: the tracker is configured per
// project (ADR-071) and the file completion is seeded from the project's own
// checkouts (ADR-083).
//
// A flag is an answer to it, so a run that passed one skips the step exactly as
// --project skips the project step. --file was read before this screen opened;
// a run that named a ticket asks for the project's tickets here, because this is
// the first moment the project is known.
func (p prepareModel) enterSource() (prepareModel, tea.Cmd) {
	p.err = nil
	if p.ticket != "" {
		p.step = stepBrief
		return p.readTickets(p.ticket)
	}
	if p.preselected {
		return p.enterBrief()
	}
	return p.enterSourceStep()
}

// enterSourceStep shows the question itself.
func (p prepareModel) enterSourceStep() (prepareModel, tea.Cmd) {
	p.err = nil
	p.step = stepSource
	p.cursor = 0
	p.title.Blur()
	p.brief.Blur()
	p.path.Blur()
	return p, nil
}

func (p prepareModel) sourceKey(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(sourceOptions)-1 {
			p.cursor++
		}
	case "enter":
		if p.cursor < len(sourceOptions) {
			return p.chooseSource(sourceOptions[p.cursor])
		}
	}
	return p, nil
}

// chooseSource takes an answer to the source question.
//
// Every answer resets the brief and fills it from the source it names: a ticket
// with the composed document, a file with its text, and "write it here" with
// nothing. No confirmation and no exceptions, because every source converges on
// the same editor — so the editor is where a brief is reviewed and adjusted, and
// returning to this step can only mean starting over. A user who wants to keep
// what they have never leaves the editor (ADR-083).
//
// Any selection also discards a draft the daemon already holds, rather than only
// a selection that changes the kind. A draft records where its brief came from
// when it is created and nothing later replaces that: updating one replaces its
// title, brief, and repositories. So a task could be launched whose brief came
// from a file and whose recorded source says "prompt", which is a record nothing
// can act on — and ticket A to ticket B is the same defect with the kind
// unchanged, because the reference is what a merge request names and what a
// change is compared against (ADR-071, ADR-083).
//
// Discarding removes nothing: nothing is created for a draft (FR-TASK-003).
//
// The discard is built before anything else runs, because it is a pointer
// receiver that clears the draft off this model: Go orders calls among
// themselves but leaves the read of p relative to them unspecified, so building
// it in the return statement would return either the model that forgot the draft
// or the one that still names it, whichever the compiler chose. What this screen
// needs is the first — the cancel is already in flight, and a model that still
// named that draft would try to cancel it again on the next resolve.
func (p prepareModel) chooseSource(option sourceOption) (prepareModel, tea.Cmd) {
	discard := p.discardDraft()

	p.err = nil
	p.title.SetValue("")
	p.brief.SetValue("")
	p.focus = 0
	// The kind is recorded by whatever fills the brief, so a ticket the user
	// backs out of and a file that could not be read leave a prompt behind
	// rather than a source naming something that was never chosen.
	p.source = api.Source{Kind: "prompt"}

	var (
		model prepareModel
		cmd   tea.Cmd
	)
	switch option.kind {
	case "ticket":
		model, cmd = p.readTickets("")
	case "markdown":
		model, cmd = p.enterImport()
	default:
		model, cmd = p.enterBrief()
	}
	return model, tea.Batch(cmd, discard)
}

// enterBrief moves to the title and brief.
func (p prepareModel) enterBrief() (prepareModel, tea.Cmd) {
	p.step = stepBrief
	p.err = nil
	return p, p.focusBrief()
}

func (p *prepareModel) focusBrief() tea.Cmd {
	if p.focus == 1 {
		p.title.Blur()
		return p.brief.Focus()
	}
	p.focus = 0
	p.brief.Blur()
	return p.title.Focus()
}

func (p prepareModel) briefKey(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	switch key.String() {
	case "tab":
		p.focus = (p.focus + 1) % 2
		return p, p.focusBrief()

	case "shift+tab":
		p.focus = (p.focus + 1) % 2
		return p, p.focusBrief()

	case "ctrl+e":
		return p, p.edit()

	case "ctrl+s":
		return p.enterRepositories()
	}

	var cmd tea.Cmd
	if p.focus == 0 {
		p.title, cmd = p.title.Update(key)
	} else {
		p.brief, cmd = p.brief.Update(key)
	}
	return p, cmd
}

// edit hands the brief to the user's editor.
//
// A task brief is a Markdown document, and the editor the user already knows is
// a better place to write one than a widget with no keybindings. It is the same
// choice review makes for diffs and files (FR-REV-002).
func (p prepareModel) edit() tea.Cmd {
	// Named document rather than brief, which is the package that reads one.
	backend, document := p.backend, p.brief.Value()
	return func() tea.Msg {
		file, err := os.CreateTemp("", "feat-brief-*.md")
		if err != nil {
			return editedMsg{err: fmt.Errorf("creating a file for the brief: %w", err)}
		}
		path := file.Name()
		_, writeErr := file.WriteString(document)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return editedMsg{err: fmt.Errorf("writing the brief to %s: %w", path, err)}
		}

		command, err := backend.EditorCommand(path)
		if err != nil {
			return editedMsg{err: err}
		}

		return tea.Exec(command, func(err error) tea.Msg {
			defer func() { _ = os.Remove(path) }()
			if err != nil {
				return editedMsg{err: fmt.Errorf("running the editor: %w", err)}
			}
			edited, err := os.ReadFile(path) // #nosec G304 -- a file this process just created
			if err != nil {
				return editedMsg{err: fmt.Errorf("reading the edited brief: %w", err)}
			}
			return editedMsg{brief: string(edited)}
		})()
	}
}

// readTickets asks the daemon to run the project's tracker command.
//
// It runs on an answer the user gave rather than when a screen opens, for the
// reason resolving a draft is its own request: the command reaches somebody's
// tracker over a network, and a screen should not do that because a field was
// edited (ADR-031).
//
// The want is the reference `--ticket` named, and is empty for a list the user
// asked to browse. Either way the same command runs and the same list comes
// back: Feat passes no filter, so there is nothing else to ask for (ADR-071).
func (p prepareModel) readTickets(want string) (prepareModel, tea.Cmd) {
	p.busy = true
	p.err = nil
	p.status = "reading the project's tickets…"
	p.title.Blur()
	p.brief.Blur()

	backend, project := p.backend, p.project
	return p, func() tea.Msg {
		list, err := backend.Tickets(context.Background(), project)
		return ticketsMsg{list: list, err: err, want: want}
	}
}

// ticketsRead takes what the tracker printed.
func (p prepareModel) ticketsRead(message ticketsMsg) (prepareModel, tea.Cmd) {
	p.busy = false
	// Asked for once. A run that named a ticket and could not find it goes on as
	// an ordinary preparation rather than asking the tracker again.
	p.ticket = ""

	if message.err != nil {
		p.err = message.err
		return p.afterTickets(message.want)
	}

	p.tickets = message.list.Tickets
	p.ticketsReadAt = message.list.ReadAt
	if message.want != "" {
		return p.chooseByReference(message.want)
	}
	if len(p.tickets) == 0 {
		// An empty list is an answer rather than a failure: which tickets are
		// the user's is the command's decision, and none is one of them.
		p.err = errors.New("the project's tracker printed no tickets")
		return p.afterTickets(message.want)
	}

	p.err = nil
	p.cursor = 0
	p.step = stepTickets
	return p, nil
}

// afterTickets is where a tracker run that produced no brief leaves the user.
//
// A run that named a ticket had the source question answered by a flag and never
// saw the step, so a failure leaves it on the brief the run would have opened —
// which is still there to be written by hand. A user who chose "from a ticket"
// returns to the step they chose it on, where the other two answers are: a
// project with no tracker, an empty list, and a command that failed are all
// answers to the source question rather than to the brief (ADR-083).
//
// It leaves the failure in place, because the failure is what the user is being
// returned to the step to read.
func (p prepareModel) afterTickets(want string) (prepareModel, tea.Cmd) {
	if want != "" {
		p.step = stepBrief
		return p, p.focusBrief()
	}
	p.step = stepSource
	p.cursor = 0
	return p, nil
}

// chooseByReference finds the ticket `--ticket` named.
//
// Feat parses no reference. What it does is match what the user typed against
// what the command emitted, so a tracker whose references are issue numbers,
// story keys, or anything else works without Feat knowing the shape of one
// (ADR-071). A reference the command did not emit is reported with what it did,
// because the command decides what the user's tickets are and the answer may
// simply be that this one is not among them.
func (p prepareModel) chooseByReference(want string) (prepareModel, tea.Cmd) {
	var matched []api.Ticket
	for _, ticket := range p.tickets {
		if ticket.Reference == want {
			matched = append(matched, ticket)
		}
	}

	switch len(matched) {
	case 1:
		return p.composeFrom(matched[0])
	case 0:
		p.err = fmt.Errorf("the project's tracker printed no ticket %s; it printed %s",
			want, references(p.tickets))
	default:
		// A merged command labels each ticket with the tracker it came from, and
		// two trackers can use the same key. Feat picks neither.
		p.err = fmt.Errorf("the project's tracker printed %d tickets called %s, from %s; "+
			"select one from the list instead", len(matched), want, sources(matched))
	}
	return p.afterTickets(want)
}

// composeFrom fills the title and brief from a ticket and returns to the brief.
//
// The composed brief goes into the same field a typed prompt is written in, so
// the confirmation, the fingerprint, and every other invariant of preparation
// apply to it unchanged. What the confirmation displays is therefore this
// document rather than the ticket it came from, which is what keeps the approval
// from being a formality (ADR-070).
// A draft recorded before the ticket was chosen was discarded by chooseSource,
// which does it for every answer to the source question rather than for this one
// — the reasoning is there.
func (p prepareModel) composeFrom(ticket api.Ticket) (prepareModel, tea.Cmd) {
	reference := api.NewTicketReference(ticket, p.ticketsReadAt)
	title, composed := reference.ComposeBrief()

	p.title.SetValue(title)
	p.brief.SetValue(composed)
	p.source = api.Source{Kind: "ticket", Ticket: &reference}
	return p.enterBrief()
}

// discardDraft cancels a draft the daemon already holds, without leaving
// preparation.
func (p *prepareModel) discardDraft() tea.Cmd {
	if p.draft == nil {
		return nil
	}

	backend, id := p.backend, p.draft.ID
	p.draft, p.plan = nil, nil
	return func() tea.Msg {
		if _, err := backend.CancelDraft(context.Background(), id); err != nil {
			return draftDiscardedMsg{err: fmt.Errorf("discarding the draft recorded before "+
				"the brief's source was chosen again: %w", err)}
		}
		return draftDiscardedMsg{}
	}
}

func (p prepareModel) ticketKey(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.tickets)-1 {
			p.cursor++
		}
	case "enter":
		if p.cursor < len(p.tickets) {
			return p.composeFrom(p.tickets[p.cursor])
		}
	}
	return p, nil
}

// enterImport opens the file screen.
func (p prepareModel) enterImport() (prepareModel, tea.Cmd) {
	p.err = nil
	p.step = stepImport
	p.title.Blur()
	p.brief.Blur()
	p.path.SetValue("")
	p.refreshCandidates()
	return p, p.path.Focus()
}

func (p prepareModel) pathKey(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	switch key.String() {
	case "enter":
		return p.importFile()

	case "tab":
		// Only where the field holds a candidate already or holds nothing at
		// all; anything else is a prefix, and the widget completes those itself
		// (ADR-077).
		if p.take() {
			p.refreshCandidates()
			return p, nil
		}
	}

	var cmd tea.Cmd
	p.path, cmd = p.path.Update(key)
	// What is offered depends on what the field names, so it is rebuilt after
	// every key that could have changed it: typing a separator is what turns a
	// directory into the list of what is in it.
	p.refreshCandidates()
	return p, cmd
}

// take puts a candidate in the field, and reports whether it had one to put
// there.
//
// It is the wizard dialog's rule on the same widget: an empty field takes the
// first candidate, a field holding one exactly steps to the next and around the
// list, and everything between the two is the widget's own prefix completion.
// Tab moves a value into the field and never past it, so what Enter imports is
// what is on the screen (ADR-077).
func (p *prepareModel) take() bool {
	if len(p.candidates) == 0 {
		return false
	}
	value := p.path.Value()
	at := slices.Index(p.candidates, value)
	if value != "" && at < 0 {
		return false
	}
	// An empty field is at -1, so the first tab takes the first candidate and
	// each one after it steps along the list and around it.
	p.path.SetValue(p.candidates[(at+1)%len(p.candidates)])
	p.path.CursorEnd()
	return true
}

// refreshCandidates rebuilds what tab offers from what the field holds.
func (p *prepareModel) refreshCandidates() {
	project, _ := p.registered(p.project)
	p.candidates = pathCandidates(project, p.path.Value())
	p.path.SetSuggestions(p.candidates)
}

// importFile reads the file the field names and fills the brief with it.
//
// The client reads it and the daemon is never told the path, which is ADR-028's
// rule and what `feat implement --file` does with the same policy, out of the
// same package. Its text goes into the same editable field a typed prompt is
// written in, so the confirmation, the fingerprint, and every other invariant of
// preparation apply to it unchanged — a Markdown file is text somebody else may
// have written, exactly as a ticket is (ADR-070).
//
// It reads on the key rather than through a command: the file is bounded before
// it is opened, and the completion under this field already reads a directory on
// every keystroke. A failure keeps the screen, because this is where the path is
// and where the user can correct it.
func (p prepareModel) importFile() (prepareModel, tea.Cmd) {
	text, path, err := brief.Read(p.path.Value())
	if err != nil {
		p.err = err
		return p, nil
	}
	if strings.TrimSpace(text) == "" {
		// Refused here rather than at the repository step, because this is the
		// screen the user can do something about it on.
		p.err = fmt.Errorf("%s holds no text, and the agent receives the brief exactly as written", path)
		return p, nil
	}

	p.brief.SetValue(text)
	// Only where the title is empty, which is the rule the $EDITOR round trip
	// follows: a title the user typed is theirs, and this is a document they may
	// be importing over one they have already named.
	if strings.TrimSpace(p.title.Value()) == "" {
		p.title.SetValue(titleFrom(text))
	}
	p.source = api.Source{Kind: "markdown", Reference: path}
	p.path.Blur()
	return p.enterBrief()
}

// pathCandidates are the paths the file screen completes to.
//
// The project's own checkouts come first, because a brief written before the
// task usually sits beside the code it is about; then the directory the client
// was started in; then the entries of whatever directory the field names, so
// that a completed directory is somewhere to keep typing. A directory carries a
// trailing separator, which is what says it is one.
//
// Entries are offered under the text the user typed rather than under what it
// resolves to, so that a "~" they wrote stays written: the widget completes by
// prefix, and a candidate that had replaced the "~" with a home directory would
// match nothing.
func pathCandidates(project api.Project, value string) []string {
	candidates := make([]string, 0, len(project.Repositories)+8)
	add := func(path string) {
		if path == "" || slices.Contains(candidates, path) {
			return
		}
		candidates = append(candidates, path)
	}

	for _, repository := range project.Repositories {
		add(withSeparator(repository.HostPath))
	}
	if working, err := os.Getwd(); err == nil {
		add(withSeparator(working))
	}

	typed, listed := namedDirectory(value)
	if listed == "" {
		return candidates
	}
	entries, err := os.ReadDir(listed)
	if err != nil {
		// A directory that cannot be read is not a failure of this screen. The
		// field holds a path the user is still typing, and most of what is typed
		// on the way to a file names nothing yet.
		return candidates
	}
	for _, entry := range entries {
		name := typed + entry.Name()
		if entry.IsDir() {
			name = withSeparator(name)
		}
		add(name)
	}
	return candidates
}

// withSeparator marks a directory as one.
func withSeparator(path string) string {
	if path == "" || strings.HasSuffix(path, string(os.PathSeparator)) {
		return path
	}
	return path + string(os.PathSeparator)
}

// namedDirectory splits what the field holds into the text up to its last
// separator and the directory that text names.
//
// The first is what a candidate is built on and the second is what is read. A
// value with no separator at all names the directory the client was started in,
// which is what a bare file name means everywhere else; an empty field names
// nothing, and is answered by the checkouts above.
func namedDirectory(value string) (typed, listed string) {
	separator := string(os.PathSeparator)
	at := strings.LastIndex(value, separator)
	if at < 0 {
		if value == "" {
			return "", ""
		}
		return "", "."
	}

	typed = value[:at+1]
	if !strings.HasPrefix(typed, "~") {
		return typed, typed
	}
	// A "~" is expanded the way every other path Feat reads is, and a "~other"
	// is refused rather than resolved, so it lists nothing (paths.Expand).
	env, err := paths.Current()
	if err != nil {
		return typed, ""
	}
	expanded, err := env.Expand(typed)
	if err != nil {
		return typed, ""
	}
	return typed, expanded
}

// references lists what a tracker printed, for a reference it did not.
//
// It is bounded because the list is somebody's whole backlog and this is one
// line of an error message.
func references(tickets []api.Ticket) string {
	if len(tickets) == 0 {
		return "nothing"
	}
	const shown = 5
	named := make([]string, 0, shown)
	for _, ticket := range tickets[:min(shown, len(tickets))] {
		named = append(named, ticket.Reference)
	}
	listed := strings.Join(named, ", ")
	if len(tickets) > shown {
		return fmt.Sprintf("%s and %d more", listed, len(tickets)-shown)
	}
	return listed
}

// sources names the trackers a merged command drew an ambiguous reference from.
func sources(tickets []api.Ticket) string {
	named := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket.Source == "" {
			named = append(named, "an unlabelled tracker")
			continue
		}
		named = append(named, ticket.Source)
	}
	return strings.Join(named, " and ")
}

// enterRepositories moves to the access selection, seeding it from the
// project's configured defaults.
func (p prepareModel) enterRepositories() (prepareModel, tea.Cmd) {
	if strings.TrimSpace(p.title.Value()) == "" {
		p.err = errors.New("the task needs a title")
		return p, nil
	}
	if strings.TrimSpace(p.brief.Value()) == "" {
		p.err = errors.New("the task needs a brief; the agent receives exactly what is written here")
		return p, nil
	}

	project, ok := p.registered(p.project)
	if !ok {
		p.err = fmt.Errorf("no project %s is registered", p.project)
		return p, nil
	}

	p.err = nil
	p.title.Blur()
	p.brief.Blur()
	if p.selection == nil {
		p.selection = defaultSelection(project)
	}
	p.cursor = 0
	p.step = stepRepositories
	return p, nil
}

// defaultSelection is what the project's configuration selects without being
// asked, and what each repository may be selected as.
func defaultSelection(project api.Project) []selectionRow {
	rows := make([]selectionRow, 0, len(project.Repositories))
	for _, repository := range project.Repositories {
		row := selectionRow{
			repository: repository.ID,
			declared:   repository.DefaultAccess,
			access:     "omitted",
			permitted:  accessModes,
		}
		switch repository.DefaultAccess {
		case "read_write":
			row.access = "read_write"
		case "read_only":
			row.access = "read_only"
			// A repository the project declares read-only never becomes
			// writable because one task asked.
			row.permitted = []string{"omitted", "read_only"}
		}
		rows = append(rows, row)
	}
	return rows
}

func (p prepareModel) repositoryKey(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.selection)-1 {
			p.cursor++
		}
	case " ", "right", "l":
		p.cycle(1)
	case "left", "h":
		p.cycle(-1)
	case "enter":
		return p.resolve()
	}
	return p, nil
}

// cycle moves one repository to the next access mode it is allowed.
func (p *prepareModel) cycle(direction int) {
	if p.cursor >= len(p.selection) {
		return
	}
	row := &p.selection[p.cursor]

	current := 0
	for i, mode := range row.permitted {
		if mode == row.access {
			current = i
		}
	}
	next := (current + direction + len(row.permitted)) % len(row.permitted)
	row.access = row.permitted[next]
}

// resolve records the draft and asks the daemon to resolve it.
//
// This is the first request that reaches the user's repositories, and it
// creates nothing: it resolves bases, proposes branches and paths, and reports
// collisions. What comes back is what the review screen shows and what
// confirming will create.
func (p prepareModel) resolve() (prepareModel, tea.Cmd) {
	selected := make([]api.DraftRepository, 0, len(p.selection))
	for _, row := range p.selection {
		if row.access == "omitted" {
			continue
		}
		selected = append(selected, api.DraftRepository{
			RepositoryID: row.repository,
			Access:       row.access,
			Ref:          row.ref,
		})
	}
	if len(selected) == 0 {
		p.err = errors.New("select at least one repository for the task to work in")
		return p, nil
	}

	p.busy = true
	p.err = nil
	p.status = "resolving bases…"

	backend := p.backend
	project, title, source := p.project, strings.TrimSpace(p.title.Value()), p.source
	document := p.brief.Value()
	existing := p.draft

	return p, func() tea.Msg {
		ctx := context.Background()

		draft := existing
		if draft == nil {
			created, err := backend.CreateDraft(ctx, api.CreateDraft{
				ProjectID: project,
				Title:     title,
				Brief:     document,
				Source:    source,
			})
			if err != nil {
				return planMsg{err: err}
			}
			draft = &created
		}

		updated, err := backend.UpdateDraft(ctx, draft.ID, api.UpdateDraft{
			Title:        title,
			Brief:        document,
			Repositories: selected,
		})
		if err != nil {
			return planMsg{draft: draft, err: err}
		}

		plan, err := backend.PlanDraft(ctx, updated.ID)
		if err != nil {
			return planMsg{draft: &updated, err: err}
		}
		return planMsg{draft: &plan.Task, plan: &plan}
	}
}

func (p prepareModel) reviewKey(key tea.KeyMsg) (prepareModel, tea.Cmd) {
	switch key.String() {
	case "enter", "ctrl+s":
		return p.confirm()
	case "p":
		// No request. The value travels with the confirmation, so nothing on the
		// daemon has to know it before then — and re-resolving to record it would
		// put a fetch and a base resolution in every repository behind a key that
		// changed nothing about where the task starts (ADR-031).
		p.planFirst = !p.planFirst
		return p, nil
	case "x":
		return p, p.abandon()
	}
	return p, nil
}

// confirm launches the task.
//
// The fingerprint of the plan on screen goes with the request. A draft that
// changed since it was displayed produces a different one and the daemon
// refuses, so what is created is what the user read (ADR-031). The review step's
// own answers go with it in the same request, so what was displayed is what is
// sent.
func (p prepareModel) confirm() (prepareModel, tea.Cmd) {
	if p.plan == nil || p.draft == nil {
		p.err = errors.New("resolve the draft before confirming it")
		return p, nil
	}

	p.busy = true
	p.status = "creating worktrees and the task terminal…"

	backend, id := p.backend, p.draft.ID
	confirmation := api.Confirmation{Fingerprint: p.plan.Fingerprint, PlanFirst: p.planFirst}
	return p, func() tea.Msg {
		task, err := backend.LaunchDraft(context.Background(), id, confirmation)
		if err != nil {
			return preparedMsg{err: err}
		}
		return preparedMsg{task: &task}
	}
}

// titleFrom derives a title from an imported brief.
//
// The first Markdown heading is what the document calls itself; failing that,
// the first line of text is. Either way the user can change it, which is why
// guessing is worth doing at all.
func titleFrom(document string) string {
	for _, line := range strings.Split(document, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if heading := strings.TrimLeft(line, "#"); heading != line {
			return truncateTitle(strings.TrimSpace(heading))
		}
		return truncateTitle(line)
	}
	return ""
}

// titleLimit keeps a derived title to something a task row can show.
const titleLimit = 72

func truncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= titleLimit {
		return title
	}
	return strings.TrimSpace(string(runes[:titleLimit]))
}

// briefName is the file name an imported brief is reported under.
func briefName(reference string) string {
	if reference == "" {
		return ""
	}
	return filepath.Base(reference)
}
