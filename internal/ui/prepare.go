package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
)

func newPrepare(backend Backend, project, brief string, source api.Source) prepareModel {
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
		project:  project,
		title:    title,
		brief:    body,
		source:   source,
		imported: brief,
	}
	if brief != "" {
		model.brief.SetValue(brief)
		model.title.SetValue(titleFrom(brief))
	}
	if model.source.Kind == "" {
		model.source.Kind = "prompt"
	}
	return model
}

// restart returns a fresh preparation screen, so that opening one from the
// dashboard never shows the previous task's answers.
func (p prepareModel) restart() prepareModel {
	fresh := newPrepare(p.backend, p.project, "", api.Source{Kind: "prompt"})
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
