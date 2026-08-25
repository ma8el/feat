package ui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// Publication is the other half of the end of a task, and it is here rather
// than on the review panel for the reason cleanup is its own screen: it is a
// sequence — read, edit, approve — that has to be finished rather than glanced
// at, and what it does reaches somebody else's server.
//
// The one rule the screen enforces is that the draft is opened before it can be
// sent. The agent wrote those words and they can carry anything it read, so a
// person reading them is the only control there is; a title in a table is not
// reading them (ADR-070).

// publicationModel is the state of the publication screen.
type publicationModel struct {
	// task is the task being published, and key is how a person names it.
	task string
	key  string
	// status is what the last action reported.
	status api.PublicationStatus
	loaded bool
	// approved is what came back from the editor, empty until the draft has
	// been opened. It is what is sent: the daemon composes each merge request
	// from these words rather than from the agent's own message.
	approved []api.ApprovedPublication
	// read reports that the draft has been through the editor. Nothing is sent
	// before it has.
	read bool
	// confirming reports that the last question — open these merge requests —
	// is outstanding.
	confirming bool
	// working reports that a request is in flight, and publishing that the
	// request in flight is the one that reaches the forges.
	working    bool
	publishing bool
	// done reports that a publication has run, so the screen shows what came of
	// it rather than what would.
	done bool
	// err is a failed action, shown rather than thrown.
	err error
}

// publicationPlanMsg carries a composed plan.
type publicationPlanMsg struct {
	task   string
	status api.PublicationStatus
	err    error
}

// publicationEditedMsg carries what came back from the editor.
type publicationEditedMsg struct {
	task     string
	approved []api.ApprovedPublication
	err      error
}

// publicationDoneMsg carries the result of a publication.
type publicationDoneMsg struct {
	task   string
	status api.PublicationStatus
	err    error
}

// openPublication shows the publication screen for the selected task.
//
// Opening it composes the plan and sends nothing, which is what makes it safe to
// reach with one key press.
func (m Model) openPublication() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if isDraft(task) {
		m.status = "task " + task.Key + " is a draft; nothing has been created for it to publish"
		return m, nil
	}

	m.rememberTab()
	m.screen = screenPublication
	m.selected = task.ID
	m.publication = publicationModel{task: task.ID, key: task.Key, working: true}
	return m, m.publicationPlan()
}

// publicationPlan asks the daemon what publishing would do.
func (m Model) publicationPlan() tea.Cmd {
	backend, id := m.backend, m.publication.task
	return func() tea.Msg {
		status, err := backend.PlanPublication(context.Background(), id)
		return publicationPlanMsg{task: id, status: status, err: err}
	}
}

// publicationEdit hands the terminal to the user's editor with the draft in it.
func (m Model) publicationEdit() tea.Cmd {
	backend, id, status := m.backend, m.publication.task, m.publication.status
	return func() tea.Msg {
		editor, err := backend.EditPublication(status)
		if err != nil {
			return publicationEditedMsg{task: id, err: err}
		}
		return tea.Exec(editor.Command(), func(err error) tea.Msg {
			defer editor.Close()
			if err != nil {
				return publicationEditedMsg{task: id, err: err}
			}
			approved, err := editor.Read()
			return publicationEditedMsg{task: id, approved: approved, err: err}
		})()
	}
}

// publicationApply sends what the user approved.
func (m Model) publicationApply() tea.Cmd {
	backend, id := m.backend, m.publication.task
	request := api.PublishRequest{Repositories: m.publication.approved}
	return func() tea.Msg {
		status, err := backend.ApplyPublication(context.Background(), id, request)
		return publicationDoneMsg{task: id, status: status, err: err}
	}
}

// publicationKey routes a key press on the publication screen.
func (m Model) publicationKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.publication.working {
		// Nothing while a request is in flight. The one that matters is the
		// publication itself, which is pushing branches and opening requests one
		// repository at a time, and a second press must not start it again.
		if key.String() == "ctrl+c" {
			return m.quit()
		}
		return m, nil
	}

	if m.publication.confirming {
		switch key.String() {
		case "y", "Y", "enter":
			m.publication.confirming = false
			m.publication.working, m.publication.publishing = true, true
			m.publication.err = nil
			return m, m.publicationApply()
		case "ctrl+c":
			return m.quit()
		default:
			m.publication.confirming = false
			m.status = "nothing was published"
			return m, nil
		}
	}

	switch key.String() {
	case "esc":
		return m.closePublication()

	case "ctrl+c", "q":
		return m.quit()

	case "e":
		if len(api.OfferedDrafts(m.publication.status.Drafts)) == 0 {
			// Nothing this screen shows is the user's to edit: it has all
			// published, or its drafts are stale. The document would be empty,
			// and an editor opening on nothing says less than the screen behind
			// it already does.
			return m, nil
		}
		m.publication.err = nil
		return m, m.publicationEdit()

	case "r":
		m.publication.working = true
		m.publication.err = nil
		m.publication.read, m.publication.approved = false, nil
		return m, m.publicationPlan()

	case "enter":
		return m.startPublication()
	}
	return m, nil
}

// startPublication puts the last question, or says what is missing.
func (m Model) startPublication() (tea.Model, tea.Cmd) {
	if m.publication.done {
		return m.closePublication()
	}
	if !m.publication.read {
		// The draft has not been opened. What would be sent is the agent's own
		// words, and a person reading them before they are sent is the only
		// control that exists (ADR-070).
		m.status = "open the draft first: e reads and edits what would be sent"
		return m, nil
	}
	if len(m.publication.approved) == 0 {
		m.status = "every repository was removed from the draft, so there is nothing to publish"
		return m, nil
	}
	// Asked of what came back from the editor rather than of the whole plan. A
	// stale draft is not offered for editing, so this is the guard for a
	// document that disagrees with its plan — and one repository's stale draft
	// never stops the others, which is the answer the daemon gives too.
	if stale := api.StaleApprovals(m.publication.status.Drafts, m.publication.approved); len(stale) > 0 {
		m.status = "the agent's draft for " + strings.Join(stale, ", ") +
			" describes a commit that is no longer current; ask for a fresh draft"
		return m, nil
	}
	m.publication.confirming = true
	return m, nil
}

// closePublication returns to the tab the screen opened over.
//
// Closing costs nothing: a plan is inert until it is approved, and what reaches
// a forge is the apply rather than the screen that shows it — which is the same
// reason cleanup's own esc is free.
func (m Model) closePublication() (tea.Model, tea.Cmd) {
	m.publication = publicationModel{}
	m.screen = screenFor(m.tab)
	return m, m.load()
}

// applyPublicationPlan records a composed plan.
func (m Model) applyPublicationPlan(message publicationPlanMsg) (tea.Model, tea.Cmd) {
	if message.task != m.publication.task {
		return m, nil
	}
	m.publication.working = false
	if message.err != nil {
		m.publication.err = message.err
		return m, nil
	}
	m.publication.err = nil
	m.publication.status = message.status
	m.publication.loaded = true
	// A composed plan is what would happen, which is the other of the two things
	// this screen draws. A publication that ran leaves the record it produced —
	// and after a partial one, looking again is how the repositories it never
	// reached are published (ADR-073). The record is not lost by clearing this:
	// the plan carries it, and it is drawn under the drafts either way.
	m.publication.done = false
	return m, nil
}

// applyPublicationEdited records what came back from the editor.
func (m Model) applyPublicationEdited(message publicationEditedMsg) (tea.Model, tea.Cmd) {
	if message.task != m.publication.task {
		return m, nil
	}
	if message.err != nil {
		m.publication.err = message.err
		return m, nil
	}
	m.publication.err = nil
	m.publication.approved = message.approved
	m.publication.read = true
	return m, nil
}

// applyPublicationDone records what a publication produced.
func (m Model) applyPublicationDone(message publicationDoneMsg) (tea.Model, tea.Cmd) {
	if message.task != m.publication.task {
		return m, nil
	}
	m.publication.working, m.publication.publishing = false, false
	if message.err != nil {
		m.publication.err = message.err
		return m, nil
	}
	m.publication.err = nil
	m.publication.status = message.status
	m.publication.done = true
	// A publication changes nothing about the task's workflow, and the task list
	// still wants the record: the panel behind this screen shows it.
	return m, m.load()
}

// publicationBody renders the screen.
func (m Model) publicationBody() string {
	var out strings.Builder
	out.WriteString(titleStyle.Render("publish task "+m.publication.key) + "\n\n")

	switch {
	case m.publication.publishing:
		out.WriteString(mutedStyle.Render(
			"pushing and opening merge requests, one repository at a time…") + "\n")
	case m.publication.working:
		out.WriteString(mutedStyle.Render("composing what publishing would do…") + "\n")
	}
	// Before the loaded check, because a plan that failed is the case with
	// nothing else to draw and the most to say.
	if m.publication.err != nil {
		out.WriteString(failureStyle.Render("  "+publicationLine(m.publication.err.Error())) + "\n\n")
	}
	if !m.publication.loaded {
		return out.String()
	}

	if m.publication.done {
		out.WriteString(m.publicationRecord())
		out.WriteString(m.publicationNotes())
		return out.String()
	}

	if len(m.publication.status.Drafts) == 0 {
		out.WriteString(mutedStyle.Render("  there is nothing to publish for this task") + "\n")
		out.WriteString(m.publicationRecord())
		out.WriteString(m.publicationNotes())
		return out.String()
	}

	for _, draft := range m.publication.status.Drafts {
		out.WriteString(m.publicationEntry(draft))
	}
	if len(api.OfferedDrafts(m.publication.status.Drafts)) == 0 {
		// Every repository is above, and none of them is one this screen can
		// send: they have published, or their draft describes a commit that is
		// no longer current. Saying so is the difference between a screen that
		// offers a key that does nothing and one that says why.
		out.WriteString("\n" + mutedStyle.Render(
			"  none of these is left to publish; the lines above say why") + "\n")
	}
	out.WriteString(m.publicationRecord())
	out.WriteString(m.publicationNotes())

	if m.publication.confirming {
		out.WriteString("\n" + attentionStyle.Render(
			"  open "+publicationCount(len(m.publication.approved))+
				" now? this cannot be undone") + "\n")
		out.WriteString(mutedStyle.Render("      y to publish, anything else to leave it") + "\n")
	}
	return out.String()
}

// publicationEntry renders one repository's draft.
func (m Model) publicationEntry(draft api.PublicationDraft) string {
	var out strings.Builder

	head := "  " + draft.RepositoryID + "  " +
		mutedStyle.Render(draft.Branch+" → "+draft.BaseBranch+"  "+draft.Forge+"  "+shortCommit(draft.Commit))
	out.WriteString(head + "\n")

	title := m.publicationTitle(draft)
	switch {
	case draft.Published != nil:
		out.WriteString(mutedStyle.Render("      already published as "+draft.Published.URL) + "\n")
	case draft.Stale:
		out.WriteString(failureStyle.Render("      the draft describes "+shortCommit(draft.DraftCommit)+
			", which is no longer current") + "\n")
	case title == "":
		out.WriteString(attentionStyle.Render("      no title yet; e to write one") + "\n")
	default:
		out.WriteString("      " + title + "\n")
	}
	for _, skipped := range draft.Skipped {
		out.WriteString(attentionStyle.Render("      "+skipped) + "\n")
	}
	return out.String()
}

// publicationTitle is the title that would be sent: what the user left in the
// editor where they have opened it, and the agent's draft where they have not.
func (m Model) publicationTitle(draft api.PublicationDraft) string {
	for _, approved := range m.publication.approved {
		if approved.RepositoryID == draft.RepositoryID {
			return approved.Title
		}
	}
	if m.publication.read {
		// Opened, and this repository is not in what came back: the user
		// removed it.
		return ""
	}
	return strings.TrimSpace(draft.Title)
}

// publicationRecord renders what the task has recorded, which after an
// interruption is also what was never attempted.
func (m Model) publicationRecord() string {
	publication := m.publication.status.Task.Publication
	if publication == nil || len(publication.Repositories) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("\n" + mutedStyle.Render("  recorded") + "\n")
	for _, entry := range publication.Repositories {
		line := "      " + entry.RepositoryID + "  " + publicationEntryState(entry)
		if entry.State == "failed" {
			out.WriteString(failureStyle.Render(line) + "\n")
			continue
		}
		out.WriteString(mutedStyle.Render(line) + "\n")
	}
	return out.String()
}

// publicationEntryState renders what one recorded repository amounts to.
//
// It is shared with the task panel, which shows the same record in its own
// layout: a screen and a panel disagreeing about what "planned" meant would be
// two answers to what a task published, and there is one record (ADR-073).
func publicationEntryState(entry api.PublicationRepository) string {
	switch {
	case entry.Request != nil:
		return entry.State + "  " + entry.Request.URL
	case entry.Failure != "":
		return entry.State + "  " + publicationLine(entry.Failure)
	case entry.State == "planned":
		// Recorded before anything was attempted and never reached, which is
		// what an interrupted publication leaves behind: publishing again
		// finishes exactly these.
		return entry.State + "  not attempted"
	}
	return entry.State
}

// publicationNotes renders what a user should know.
func (m Model) publicationNotes() string {
	if len(m.publication.status.Notes) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("\n")
	for _, note := range m.publication.status.Notes {
		out.WriteString(mutedStyle.Render("  note: "+note) + "\n")
	}
	return out.String()
}

// publicationHints are the keys the footer offers.
func (m Model) publicationHints() string {
	if m.publication.done {
		// Looking again is offered here rather than only before a publication,
		// because this is where a partial one is read: nothing is rolled back,
		// and a fresh plan is what names the repositories still to publish.
		return keyHints(keyHint("r", "look again"), keyHint("esc", "close"))
	}
	if m.publication.confirming {
		return keyHints(keyHint("y", "publish"), keyHint("esc", "leave it"))
	}
	var hints []string
	if len(api.OfferedDrafts(m.publication.status.Drafts)) > 0 {
		hints = append(hints, keyHint("e", "read and edit the draft"))
	}
	if m.publication.read {
		hints = append(hints, keyHint("enter", "publish"))
	}
	return keyHints(append(hints, keyHint("r", "look again"), keyHint("esc", "close"))...)
}

// publicationCount names how many merge requests a confirmation is about.
func publicationCount(count int) string {
	if count == 1 {
		return "1 merge request"
	}
	return strconv.Itoa(count) + " merge requests"
}

// publicationLine reduces a message to the one line a screen has room for.
//
// A forge's own refusal is the one string here Feat did not write, and it can
// carry the several lines a CLI prints around it.
func publicationLine(message string) string {
	for _, line := range strings.Split(message, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
