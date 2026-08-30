package ui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ma8el/feat/internal/api"
)

// Publication is the other half of the end of a task, and it is here rather
// than on the review panel for the reason cleanup is its own screen: it is a
// sequence — read, edit, approve — that has to be finished rather than glanced
// at, and what it does reaches somebody else's server.
//
// The one rule the screen enforces is that the words are read before they are
// sent. The agent wrote them and they can carry anything it read, so a person
// reading them is the only control there is (ADR-070). This screen is where
// they are read: it draws the title and the description that would be sent, one
// repository at a time, and `enter` is refused until every line of them has been
// on the screen. `e` hands the same words to the user's editor for rewriting,
// and what comes back is what is drawn and what is sent (ADR-076).

// publicationModel is the state of the publication screen.
type publicationModel struct {
	// task is the task being published, and key is how a person names it.
	task string
	key  string
	// status is what the last action reported.
	status api.PublicationStatus
	loaded bool
	// approved is what came back from the editor, and edited reports that it
	// did. Where nothing was edited the words that are sent are composed from
	// the plan the screen drew, which is the same thing: what is displayed is
	// what is sent, whichever of the two the user read it in.
	approved []api.ApprovedPublication
	edited   bool
	// scroll is the first line of the document drawn, and seen is how far down
	// it the window has reached. seen is the reading gate — it counts the lines
	// that have been on the screen and never goes backwards — so it is recorded
	// where the window is known, which is the key handler rather than the
	// renderer.
	scroll int
	seen   int
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
	request := api.PublishRequest{Repositories: m.publicationApprovals()}
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

	// What is on the screen is recorded before the key is answered, because the
	// key may be the one that publishes and the gate is what has been displayed.
	// Here is where the window is known: the renderer cannot write it back, and
	// a terminal that was resized since the last press is accounted for by
	// measuring now rather than by remembering (ADR-076).
	m.publication = m.witnessPublication()

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

	case "up", "k":
		return m.scrollPublication(-1)

	case "down", "j":
		return m.scrollPublication(1)

	case "pgup":
		return m.scrollPublication(-m.publicationPage())

	case "pgdown":
		return m.scrollPublication(m.publicationPage())

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
		return m, m.publicationPlan()

	case "enter":
		return m.startPublication()
	}
	return m, nil
}

// scrollPublication moves the document under the window.
//
// The window records what it reached as it moves, rather than only when the
// next key arrives: scrolling to the end is the act of reading, and the gate
// must be satisfied by it and not by whatever is pressed afterwards.
func (m Model) scrollPublication(delta int) (tea.Model, tea.Cmd) {
	width, height := m.publicationDocumentSize()
	lines, _ := m.publicationLines(width)

	m.publication.scroll = min(max(0, m.publication.scroll+delta), max(0, len(lines)-height))
	m.publication = m.witnessPublication()
	return m, nil
}

// publicationPage is what pgup and pgdn move, which is a window less the line
// that carries over.
func (m Model) publicationPage() int {
	_, height := m.publicationDocumentSize()
	return max(1, height-1)
}

// witnessPublication records how far down the document the window has reached.
func (m Model) witnessPublication() publicationModel {
	published := m.publication
	if !published.loaded {
		return published
	}
	_, height := m.publicationDocumentSize()
	published.seen = max(published.seen, published.scroll+height)
	return published
}

// publicationRead reports that what would be sent has been read.
//
// Two things satisfy it, and they are the same thing: the words have been drawn
// here in full, or they have been through the user's editor. What it is not is
// a key press saying so — a screen that showed a title and asked for a
// keystroke would be reading a table of contents (ADR-070, ADR-076).
func (m Model) publicationRead() bool {
	if m.publication.edited {
		return true
	}
	width, _ := m.publicationRegion()
	_, words := m.publicationLines(width)
	return m.publication.seen >= words
}

// publicationApprovals is what would be sent: the words the editor came back
// with where it was opened, and the ones the screen drew where it was not.
func (m Model) publicationApprovals() []api.ApprovedPublication {
	if m.publication.edited {
		return m.publication.approved
	}

	offered := api.OfferedDrafts(m.publication.status.Drafts)
	approvals := make([]api.ApprovedPublication, 0, len(offered))
	for _, draft := range offered {
		// Trimmed exactly as the editor document's parser trims what comes back
		// through it, so that a draft nobody edited is the same request either
		// way. Two paths to one publication must not differ by a newline.
		approvals = append(approvals, api.ApprovedPublication{
			RepositoryID: draft.RepositoryID,
			Title:        strings.TrimSpace(draft.Title),
			Body:         strings.TrimSpace(draft.Body),
			Commit:       draft.Commit,
		})
	}
	return approvals
}

// startPublication puts the last question, or says what is missing.
func (m Model) startPublication() (tea.Model, tea.Cmd) {
	if m.publication.done {
		return m.closePublication()
	}

	approvals := m.publicationApprovals()
	if len(approvals) == 0 {
		m.status = "there is nothing left to publish for this task"
		return m, nil
	}
	if !m.publicationRead() {
		// The words have not all been on the screen. What would be sent is the
		// agent's own prose, and a person reading it before it is sent is the
		// only control that exists (ADR-070).
		m.status = "read to the end of the draft first: ↑↓ and pgup/pgdn scroll it, e opens it in your editor"
		return m, nil
	}
	if untitled := publicationUntitled(approvals); len(untitled) > 0 {
		// Refused here rather than sent for the daemon to refuse, and named
		// rather than dropped: publishing the rest and saying nothing would be
		// a publication the user believes covered every repository on the
		// screen.
		m.status = strings.Join(untitled, ", ") + " has no title, and a merge request needs one; " +
			"e writes one in the draft"
		return m, nil
	}
	// Asked of what would be sent rather than of the whole plan. A stale draft
	// is not offered, so this is the guard for a document that disagrees with
	// its plan — and one repository's stale draft never stops the others, which
	// is the answer the daemon gives too.
	if stale := api.StaleApprovals(m.publication.status.Drafts, approvals); len(stale) > 0 {
		m.status = "the agent's draft for " + strings.Join(stale, ", ") +
			" describes a commit that is no longer current; ask for a fresh draft"
		return m, nil
	}
	m.publication.confirming = true
	return m, nil
}

// publicationUntitled names the approved repositories a merge request cannot be
// opened for, because nobody has written a title.
func publicationUntitled(approvals []api.ApprovedPublication) []string {
	var untitled []string
	for _, one := range approvals {
		if strings.TrimSpace(one.Title) == "" {
			untitled = append(untitled, one.RepositoryID)
		}
	}
	return untitled
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
	// A fresh plan is fresh words, so what was read of the last ones counts for
	// nothing: the document starts at the top and the gate starts closed. An
	// approval from before it is dropped for the same reason — it describes a
	// document this screen is no longer showing.
	m.publication.scroll, m.publication.seen = 0, 0
	m.publication.edited, m.publication.approved = false, nil
	// A composed plan is what would happen, which is the other of the two things
	// this screen draws. A publication that ran leaves the record it produced —
	// and after a partial one, looking again is how the repositories it never
	// reached are published (ADR-073). The record is not lost by clearing this:
	// the plan carries it, and it is drawn under the drafts either way.
	m.publication.done = false
	// The frame that follows this message draws the first window of the new
	// document, so that window has been read. A draft that fits on the screen is
	// therefore read as soon as it arrives, which is the point: reading is what
	// has been in front of the user, not a key press saying it was.
	m.publication = m.witnessPublication()
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
	m.publication.edited = true
	// The document the screen draws is now the user's own words, so it is drawn
	// from the top: what is under the window changed while the terminal was
	// somebody else's.
	m.publication.scroll, m.publication.seen = 0, 0
	// Said in the footer as well as on the screen, because the user has just
	// come back from another program and their eye is not necessarily on the
	// line that changed.
	if len(message.approved) == 0 {
		m.status = "every repository was removed from the draft, so there is nothing left to publish"
		return m, nil
	}
	m.status = "the draft came back from the editor; enter opens " +
		publicationCount(len(message.approved)) + " with those words"
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
	m.publication.scroll, m.publication.seen = 0, 0
	// A publication changes nothing about the task's workflow, and the task list
	// still wants the record: the panel behind this screen shows it.
	return m, m.load()
}

// What the screen draws around the document, which the document has less room
// for.
const (
	// publicationTitleHeight is the narrow fallback's own title and the blank
	// line under it.
	publicationTitleHeight = 2
	// publicationHintsHeight is the blank line and the key map the dialog puts
	// under the body.
	publicationHintsHeight = 2
	// publicationMarkers is the pair of lines that say how much of the document
	// is above and below the window. They are reserved rather than counted,
	// because the window has to draw a known number of lines: the gate is
	// "these lines were displayed", and a note that quietly took one of their
	// rows would make it "these lines were displayed, less one".
	publicationMarkers = 2
)

// publicationRegion is the space this screen's body is drawn in.
//
// It is resolved here rather than passed in for the reason cleanup's is: the
// document scrolls, so the key handler and the renderer have to agree about how
// much of it is on the screen, and only one of the two can decide.
func (m Model) publicationRegion() (width, height int) {
	if m.narrow() {
		width, height = m.frameSize()
		return width, height - stackedFooterHeight - publicationTitleHeight
	}

	widest, tallest := m.dialogLimits()
	if widest < dialogSmallest {
		widest = dialogSmallest
	}
	return widest - dialogChrome, tallest - dialogVerticalChrome - publicationHintsHeight
}

// publicationDocumentSize is the window the draft scrolls under: the region less
// what is drawn above and below it.
func (m Model) publicationDocumentSize() (width, height int) {
	width, height = m.publicationRegion()
	height -= drawnLines(m.publicationHead(width)) + drawnLines(m.publicationTail(width)) + publicationMarkers
	if height < 3 {
		height = 3
	}
	return width, height
}

// publicationBody renders the screen.
func (m Model) publicationBody() string {
	width, height := m.publicationDocumentSize()

	var out strings.Builder
	out.WriteString(m.publicationHead(width))
	if !m.publication.loaded {
		return out.String()
	}
	out.WriteString(m.publicationWindow(width, height))
	out.WriteString(m.publicationTail(width))
	return out.String()
}

// publicationView is the narrow fallback, where the screen is a screen rather
// than a dialog and carries its own title.
func (m Model) publicationView() string {
	return titleStyle.Render("publish task "+m.publication.key) + "\n\n" +
		m.publicationBody() + m.footer(m.publicationHints())
}

// publicationTitle names the task the dialog is about, for its border.
func (m Model) publicationTitle() string { return "publish task " + m.publication.key }

// publicationHead is what is drawn above the document: a request in flight, and
// a failure that is shown rather than thrown.
func (m Model) publicationHead(width int) string {
	var out strings.Builder
	switch {
	case m.publication.publishing:
		out.WriteString(mutedStyle.Render(m.activity.mark(
			"  pushing and opening merge requests, one repository at a time…")) + "\n")
	case m.publication.working:
		out.WriteString(mutedStyle.Render(m.activity.mark("  composing what publishing would do…")) + "\n")
	}
	// Before the document, because a plan that failed is the case with nothing
	// else to draw and the most to say. Flattened to one line, as the footer
	// flattens the errors it shows: this one comes from the daemon and may carry
	// a provider CLI's output, and a block that wraps here is a block drawn over
	// the question below it (ADR-054).
	if m.publication.err != nil {
		out.WriteString(failureStyle.Render(truncate("  "+publicationLine(m.publication.err.Error()), width)) + "\n\n")
	}
	return out.String()
}

// publicationWindow draws the part of the document that is on the screen, with
// what is above and below it noted rather than silently cut.
func (m Model) publicationWindow(width, height int) string {
	lines, _ := m.publicationLines(width)
	if len(lines) == 0 {
		return mutedStyle.Render("  there is nothing to publish for this task") + "\n"
	}

	var out strings.Builder
	// The last window rather than the last line, and clamped here as well as
	// where the keys move it, so that the renderer and the key handler cannot
	// disagree about where the document ends.
	first := min(m.publication.scroll, max(0, len(lines)-max(1, height)))
	last := min(len(lines), first+max(1, height))

	// Every line is drawn in the width of the draft's widest, so that scrolling
	// through it does not resize the dialog around it. The measure is the whole
	// draft rather than the window, because the window is what changes: a long
	// line low in a document widened the box the moment it came into view.
	block := documentWidth(lines, max(1, width))

	// Both marker rows are drawn whether or not there is a marker for them, blank
	// where there is none. publicationDocumentSize reserves a constant two lines
	// for them rather than counting the ones that appear, and the two have to
	// agree: the alternative — subtracting only the markers actually drawn —
	// would work visually and change how much of the draft the gate believes was
	// read. ADR-076's gate is "these lines were displayed", and a note that
	// quietly took one of their rows would make it "these lines were displayed,
	// less one".
	//
	// It is also what stops the box wobbling by up to two lines as the reader
	// moves, and losing one exactly as they reach the end.
	above := ""
	if first > 0 {
		above = mutedStyle.Render("  … " + count(first, "line", "lines") + " above")
	}
	out.WriteString(above + "\n")

	for _, line := range lines[first:last] {
		out.WriteString(pad(line, block) + "\n")
	}

	below := ""
	if last < len(lines) {
		below = mutedStyle.Render("  … " + count(len(lines)-last, "more line", "more lines"))
	}
	out.WriteString(below + "\n")
	return out.String()
}

// publicationLines renders the whole document one line at a time, and reports
// how many of those lines are the words a publication would carry.
//
// Built rather than printed because the screen scrolls it, and counted because
// the count is the reading gate: the words come first, and the account of what
// this task has already published follows them. Feat's own record is not part of
// the gate — nobody has to scroll past a merge request that already exists to
// publish the ones that do not.
func (m Model) publicationLines(width int) (lines []string, words int) {
	for i, draft := range m.publication.status.Drafts {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.publicationEntry(draft, width)...)
		if draft.Offered() {
			words = len(lines)
		}
	}

	lines = append(lines, m.publicationRecordLines()...)
	lines = append(lines, m.publicationNoteLines(width)...)
	return lines, words
}

// publicationEntry renders one repository's draft: what Feat knows about it,
// and the words that would be sent for it.
func (m Model) publicationEntry(draft api.PublicationDraft, width int) []string {
	lines := []string{"  " + draft.RepositoryID + "  " +
		mutedStyle.Render(draft.Branch+" → "+draft.BaseBranch+"  "+draft.Forge+"  "+shortCommit(draft.Commit))}

	switch {
	case draft.Published != nil:
		lines = append(lines, mutedStyle.Render("      already published as "+draft.Published.URL))
	case draft.Stale:
		lines = append(lines, failureStyle.Render("      the draft describes "+shortCommit(draft.DraftCommit)+
			", which is no longer current"))
	default:
		lines = append(lines, m.publicationWords(draft, width)...)
	}

	// Set off from the words above them, because they are not the words: what
	// the push will not run is Feat's own line, and a sentence of the agent's
	// prose with a warning butted against it reads as one paragraph by two
	// authors.
	if len(draft.Skipped) > 0 {
		lines = append(lines, "")
	}
	for _, skipped := range draft.Skipped {
		lines = append(lines, attentionStyle.Render("      "+skipped))
	}
	return lines
}

// publicationWords renders the title and the description that would be sent for
// one repository — the whole of them, because reading them is the control.
func (m Model) publicationWords(draft api.PublicationDraft, width int) []string {
	title, body, removed := m.publicationDraftWords(draft)
	if removed {
		return []string{mutedStyle.Render("      removed from the draft; this repository will not be published")}
	}

	var lines []string
	if title == "" {
		lines = append(lines, attentionStyle.Render("      no title yet; e writes one"))
	} else {
		lines = append(lines, headingStyle.Render("      "+title))
	}

	if strings.TrimSpace(body) == "" {
		return append(lines, mutedStyle.Render("      the agent wrote no description"))
	}
	lines = append(lines, "")
	return append(lines, wrapLines(body, width, 6)...)
}

// publicationDraftWords is the title and the description that would be sent for
// one repository: what the user left in the editor where they have opened it,
// and the agent's draft where they have not. A repository the editor came back
// without was removed by the user, and is reported rather than drawn empty.
func (m Model) publicationDraftWords(draft api.PublicationDraft) (title, body string, removed bool) {
	if !m.publication.edited {
		return strings.TrimSpace(draft.Title), strings.TrimSpace(draft.Body), false
	}
	for _, approved := range m.publication.approved {
		if approved.RepositoryID == draft.RepositoryID {
			return strings.TrimSpace(approved.Title), strings.TrimSpace(approved.Body), false
		}
	}
	return "", "", true
}

// publicationRecordLines are what the task has recorded, which after an
// interruption is also what was never attempted.
func (m Model) publicationRecordLines() []string {
	publication := m.publication.status.Task.Publication
	if publication == nil || len(publication.Repositories) == 0 {
		return nil
	}

	lines := []string{"", mutedStyle.Render("  recorded")}
	for _, entry := range publication.Repositories {
		line := "      " + entry.RepositoryID + "  " + publicationEntryState(entry)
		if entry.State == "failed" {
			lines = append(lines, failureStyle.Render(line))
			continue
		}
		lines = append(lines, mutedStyle.Render(line))
	}
	return lines
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

// publicationNoteLines are what a user should know: a repository that cannot be
// published and why, or a hook the push will not run.
func (m Model) publicationNoteLines(width int) []string {
	if len(m.publication.status.Notes) == 0 {
		return nil
	}

	lines := []string{""}
	for _, note := range m.publication.status.Notes {
		for _, line := range wrapLines("note: "+note, width, 2) {
			lines = append(lines, mutedStyle.Render(line))
		}
	}
	return lines
}

// publicationTail is what the screen says under the document: what to do next,
// and the last question where it is outstanding.
func (m Model) publicationTail(width int) string {
	if !m.publication.loaded {
		return ""
	}

	var out strings.Builder
	if guidance := m.publicationGuidance(); guidance != "" {
		out.WriteString("\n")
		for _, line := range wrapLines(guidance, width, 2) {
			out.WriteString(mutedStyle.Render(line) + "\n")
		}
	}
	if m.publication.confirming {
		out.WriteString("\n" + attentionStyle.Render(
			"  open "+publicationCount(len(m.publicationApprovals()))+
				" now? this cannot be undone") + "\n")
		out.WriteString(mutedStyle.Render("      y to publish, anything else to leave it") + "\n")
	}
	return out.String()
}

// publicationGuidance is the sentence that says what this screen is waiting for.
//
// It is written out at every step rather than left to the key hints, because the
// sequence is the part of this screen nobody has memorised: a footer that offers
// "e" says which key, and this says why it is there and what follows it.
func (m Model) publicationGuidance() string {
	switch {
	case m.publication.working, m.publication.confirming, m.publication.done:
		return ""
	case len(m.publication.status.Drafts) == 0:
		return ""
	case len(api.OfferedDrafts(m.publication.status.Drafts)) == 0:
		// Every repository is above, and none of them is one this screen can
		// send: they have published, or their draft describes a commit that is
		// no longer current. Saying so is the difference between a screen that
		// offers a key that does nothing and one that says why.
		return "none of these is left to publish; the lines above say why"
	}

	approvals := m.publicationApprovals()
	switch {
	case len(approvals) == 0:
		return "every repository was removed from the draft, so there is nothing left to publish; " +
			"e opens it again"
	case !m.publicationRead():
		return "these are the words that would be sent, and they are the agent's: read them to the end " +
			"before publishing. ↑↓ and pgup/pgdn scroll, e opens them in your editor"
	case m.publication.edited:
		return "these are the words you left in the editor; enter opens " +
			publicationCount(len(approvals)) + " with them"
	}
	return "enter opens " + publicationCount(len(approvals)) +
		" with the words above; e edits them in your editor first"
}

// publicationHints are the keys the footer offers.
func (m Model) publicationHints() string {
	switch {
	case m.publication.publishing:
		// Nothing is offered while branches are being pushed and requests
		// opened. The screen has shut its keyboard, and a key map advertising a
		// way out beside a publication that will not be abandoned is one that
		// has to be tried to be disbelieved.
		return mutedStyle.Render("publishing…")
	case m.publication.confirming:
		return keyHints(keyHint("y", "publish"), keyHint("esc", "leave it"))
	case m.publication.done:
		// Looking again is offered here rather than only before a publication,
		// because this is where a partial one is read: nothing is rolled back,
		// and a fresh plan is what names the repositories still to publish.
		return keyHints(keyHint("↑↓ pgup/pgdn", "read"),
			keyHint("r", "look again"), keyHint("esc", "close"))
	}

	var hints []string
	if m.publicationScrollable() {
		hints = append(hints, keyHint("↑↓ pgup/pgdn", "read"))
	}
	if len(api.OfferedDrafts(m.publication.status.Drafts)) > 0 {
		hints = append(hints, keyHint("e", "edit the draft"))
	}
	if m.publicationRead() && len(m.publicationApprovals()) > 0 {
		hints = append(hints, keyHint("enter", "publish"))
	}
	return keyHints(append(hints, keyHint("r", "look again"), keyHint("esc", "close"))...)
}

// publicationScrollable reports that the document does not fit its window.
func (m Model) publicationScrollable() bool {
	width, height := m.publicationDocumentSize()
	lines, _ := m.publicationLines(width)
	return len(lines) > height
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

// wrapLines folds text to a measure and indents it, one rendered line at a time.
//
// Each of the text's own lines is folded on its own, so that what the agent
// wrote as a list stays a list: reflowing the whole block into one paragraph
// would show the user something other than what is being sent. Nothing here is
// truncated, for the same reason — a description read with its ends cut off is
// one that was not read.
func wrapLines(text string, width, indent int) []string {
	measure := max(publicationNarrowest, width-indent)
	margin := strings.Repeat(" ", indent)

	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		for _, folded := range strings.Split(lipgloss.NewStyle().Width(measure).Render(line), "\n") {
			out = append(out, margin+strings.TrimRight(folded, " "))
		}
	}
	return out
}

// publicationNarrowest is the measure prose is folded to whatever the terminal
// says, below which words break more than they wrap.
const publicationNarrowest = 20
