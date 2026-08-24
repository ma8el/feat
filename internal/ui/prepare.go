package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// step is where the user is in task preparation.
//
// The order is FR-TASK-003's: what the task is, which repositories it touches,
// and then everything Feat resolved from that, which is the last thing shown
// before anything is created.
type step int

const (
	stepProject step = iota
	stepBrief
	stepRepositories
	stepReview
	// stepTickets is the project's tickets offered as a selection. It is last
	// rather than in sequence because it is not a stage of preparation: it is a
	// way of filling the brief, reached from the brief step and returning to it,
	// and a project with no tracker never sees it.
	stepTickets
)

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
	// source records where the brief came from: a typed prompt, or a file the
	// caller imported and read for us.
	source api.Source
	// imported is a brief supplied by `feat implement --file`.
	imported string

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
// It is a value rather than a list of arguments because the four are all
// optional and all strings, and a call site that swapped two of them would
// compile.
type prepareStart struct {
	// project preselects a project, from --project.
	project string
	// brief is an imported Markdown brief, read by the caller.
	brief string
	// source records where that brief came from.
	source api.Source
	// ticket is the reference --ticket named.
	ticket string
}

func newPrepare(backend Backend, start prepareStart) prepareModel {
	title := textinput.New()
	title.Placeholder = "what the task is, in a few words"
	title.CharLimit = 120
	title.Prompt = ""

	body := textarea.New()
	body.Placeholder = "what the agent should do, in Markdown"
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.SetHeight(10)

	model := prepareModel{
		backend:  backend,
		project:  start.project,
		title:    title,
		brief:    body,
		source:   start.source,
		imported: start.brief,
		ticket:   start.ticket,
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
	case stepBrief:
		return p.briefKey(key)
	case stepRepositories:
		return p.repositoryKey(key)
	case stepReview:
		return p.reviewKey(key)
	case stepTickets:
		return p.ticketKey(key)
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
	case stepTickets:
		// Back out of the list without taking one. The brief is whatever it was
		// before, which for a screen reached by mistake is what the user typed.
		p.step = stepBrief
		return p, p.focusBrief()
	case stepRepositories:
		p.step = stepBrief
		return p, p.focusBrief()
	case stepBrief:
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
		return p.enterBrief()
	}
	if len(p.projects) == 1 {
		p.project = p.projects[0].ID
		return p.enterBrief()
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
			return p.enterBrief()
		}
	}
	return p, nil
}

// enterBrief moves to the title and brief.
//
// A run that named a ticket asks for the project's tickets here, because this is
// the first moment the project is known: the tracker is configured per project,
// and which project a task is in is answered before what the task is (ADR-071).
func (p prepareModel) enterBrief() (prepareModel, tea.Cmd) {
	p.step = stepBrief
	p.err = nil
	if p.ticket != "" {
		return p.readTickets(p.ticket)
	}
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

	case "ctrl+t":
		return p.readTickets("")

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
	backend, brief := p.backend, p.brief.Value()
	return func() tea.Msg {
		file, err := os.CreateTemp("", "feat-brief-*.md")
		if err != nil {
			return editedMsg{err: fmt.Errorf("creating a file for the brief: %w", err)}
		}
		path := file.Name()
		_, writeErr := file.WriteString(brief)
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
// It runs on a key the user pressed rather than when the screen opens, for the
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
		p.step = stepBrief
		return p, p.focusBrief()
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
		p.step = stepBrief
		return p, p.focusBrief()
	}

	p.err = nil
	p.cursor = 0
	p.step = stepTickets
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
	p.step = stepBrief
	return p, p.focusBrief()
}

// composeFrom fills the title and brief from a ticket and returns to the brief.
//
// The composed brief goes into the same field a typed prompt is written in, so
// the confirmation, the fingerprint, and every other invariant of preparation
// apply to it unchanged. What the confirmation displays is therefore this
// document rather than the ticket it came from, which is what keeps the approval
// from being a formality (ADR-070).
func (p prepareModel) composeFrom(ticket api.Ticket) (prepareModel, tea.Cmd) {
	reference := api.NewTicketReference(ticket, p.ticketsReadAt)
	title, brief := reference.ComposeBrief()

	p.title.SetValue(title)
	p.brief.SetValue(brief)
	p.source = api.Source{Kind: "ticket", Ticket: &reference}
	p.err = nil
	p.step = stepBrief

	// A draft records where its brief came from when it is created, and nothing
	// later replaces that: updating one replaces its title, brief, and
	// repositories. So a draft recorded before this ticket was chosen is
	// discarded rather than edited, and the next resolve records a new one. A
	// task whose brief is one ticket's and whose recorded source is a prompt, or
	// another ticket, would be a record nothing could act on — it is what a
	// merge request names and what a change is compared against (ADR-071).
	//
	// Discarding removes nothing: nothing is created for a draft (FR-TASK-003).
	return p, tea.Batch(p.focusBrief(), p.discardDraft())
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
				"the ticket was chosen: %w", err)}
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
	project, title, brief, source := p.project, strings.TrimSpace(p.title.Value()), p.brief.Value(), p.source
	existing := p.draft

	return p, func() tea.Msg {
		ctx := context.Background()

		draft := existing
		if draft == nil {
			created, err := backend.CreateDraft(ctx, api.CreateDraft{
				ProjectID: project,
				Title:     title,
				Brief:     brief,
				Source:    source,
			})
			if err != nil {
				return planMsg{err: err}
			}
			draft = &created
		}

		updated, err := backend.UpdateDraft(ctx, draft.ID, api.UpdateDraft{
			Title:        title,
			Brief:        brief,
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
	case "x":
		return p, p.abandon()
	}
	return p, nil
}

// confirm launches the task.
//
// The fingerprint of the plan on screen goes with the request. A draft that
// changed since it was displayed produces a different one and the daemon
// refuses, so what is created is what the user read (ADR-031).
func (p prepareModel) confirm() (prepareModel, tea.Cmd) {
	if p.plan == nil || p.draft == nil {
		p.err = errors.New("resolve the draft before confirming it")
		return p, nil
	}

	p.busy = true
	p.status = "creating worktrees and the task terminal…"

	backend, id, fingerprint := p.backend, p.draft.ID, p.plan.Fingerprint
	return p, func() tea.Msg {
		task, err := backend.LaunchDraft(context.Background(), id, fingerprint)
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
func titleFrom(brief string) string {
	for _, line := range strings.Split(brief, "\n") {
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
