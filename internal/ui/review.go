package ui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// reviewModel is the state of the review screen.
//
// It holds what the last action returned rather than deriving it from the task
// list, because the per-repository comparison, the check results, and the
// expanded commands are what that action saw. The cursor is a repository, since
// every command on this screen is about one.
type reviewModel struct {
	// task is the task under review.
	task string
	// status is what the last action reported, and whether one has run at all.
	status api.ReviewStatus
	loaded bool
	// cursor is the selected repository.
	cursor int
	// pending is the action in flight, so the screen says what it is waiting for.
	pending api.ReviewAction
	// err is a failed action, shown rather than thrown.
	err error
}

// reviewMsg carries the result of one review action.
type reviewMsg struct {
	action api.ReviewAction
	status api.ReviewStatus
	err    error
}

// openReview shows the review screen for the task an action applies to.
func (m Model) openReview() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		return m, nil
	}
	if isDraft(task) {
		m.status = "task " + task.Key + " is a draft; there is nothing to review yet"
		return m, nil
	}

	m.screen = screenReview
	m.selected = task.ID
	m.review = reviewModel{task: task.ID}
	// Opening the screen compares every repository against its own recorded
	// base. Nothing else happens: a review action never starts, stops, or
	// removes anything.
	return m, m.reviewAction(api.ReviewObserve)
}

// reviewAction performs one action against the daemon.
func (m Model) reviewAction(action api.ReviewAction) tea.Cmd {
	backend, id := m.backend, m.review.task
	return func() tea.Msg {
		status, err := backend.Review(context.Background(), id, action)
		return reviewMsg{action: action, status: status, err: err}
	}
}

// reviewKey routes a key press on the review screen.
func (m Model) reviewKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.screen = screenDetail
		return m, nil

	case "ctrl+c", "q":
		m.quitting = true
		m.stopStream()
		return m, tea.Quit

	case "up", "k":
		if m.review.cursor > 0 {
			m.review.cursor--
		}
		return m, nil

	case "down", "j":
		if m.review.cursor < len(m.review.status.Repositories)-1 {
			m.review.cursor++
		}
		return m, nil

	case "d":
		return m.runReviewCommand(api.ReviewCommandKindDiff)

	case "e":
		return m.runReviewCommand(api.ReviewCommandKindEditor)

	case "s":
		return m.runReviewCommand(api.ReviewCommandKindStatus)

	case "A":
		return m.startReview(api.ReviewApprove)

	case "C":
		return m.startReview(api.ReviewRequestChanges)

	case "P":
		return m.startReview(api.ReviewLeavePending)

	case "V":
		return m.startReview(api.ReviewVerify)

	case "a":
		return m.attach()

	case "r":
		return m.startReview(api.ReviewObserve)
	}
	return m, nil
}

// startReview records what the screen is waiting for and asks for it.
func (m Model) startReview(action api.ReviewAction) (tea.Model, tea.Cmd) {
	m.review.pending = action
	m.review.err = nil
	m.status = ""
	return m, m.reviewAction(action)
}

// runReviewCommand yields this terminal to one of the project's own tools.
//
// Feat renders no diff of its own (ADR-006): it opens what the user configured,
// in the worktree of the repository under the cursor, and takes the terminal
// back when they leave it.
func (m Model) runReviewCommand(kind string) (tea.Model, tea.Cmd) {
	repository, ok := m.reviewRepository()
	if !ok {
		return m, nil
	}

	command, found := findCommand(m.review.status.Commands, kind, repository.RepositoryID)
	if !found {
		if kind != api.ReviewCommandKindEditor {
			m.status = "this project configures no " + kind + " command"
			return m, nil
		}
		// The editor is the one command that may be unconfigured: it defaults to
		// $EDITOR, which this process can see and the daemon cannot (FR-REV-003).
		backend, path := m.backend, repository.WorktreePath
		return m, func() tea.Msg {
			editor, err := backend.EditorCommand(path)
			if err != nil {
				return execMsg{err: err}
			}
			return tea.Exec(editor, func(err error) tea.Msg { return execMsg{err: err} })()
		}
	}

	backend := m.backend
	return m, func() tea.Msg {
		process, err := backend.ReviewCommand(command)
		if err != nil {
			return execMsg{err: err}
		}
		return tea.Exec(process, func(err error) tea.Msg { return execMsg{err: err} })()
	}
}

// reviewRepository is the repository under the cursor.
func (m Model) reviewRepository() (api.ReviewRepository, bool) {
	rows := m.review.status.Repositories
	if m.review.cursor < 0 || m.review.cursor >= len(rows) {
		return api.ReviewRepository{}, false
	}
	return rows[m.review.cursor], true
}

// findCommand returns the expanded command of one kind for one repository.
func findCommand(commands []api.ReviewCommand, kind, repository string) (api.ReviewCommand, bool) {
	for _, command := range commands {
		if command.Kind == kind && command.RepositoryID == repository {
			return command, true
		}
	}
	return api.ReviewCommand{}, false
}

// applyReview records what an action reported.
func (m Model) applyReview(message reviewMsg) (tea.Model, tea.Cmd) {
	m.review.pending = ""
	if message.err != nil {
		m.review.err = message.err
		return m, nil
	}

	m.review.err = nil
	m.review.status = message.status
	m.review.loaded = true
	if m.review.cursor >= len(message.status.Repositories) {
		m.review.cursor = 0
	}
	if message.action != api.ReviewObserve {
		m.status = "review " + string(message.action) + " on task " + message.status.Task.Key
	}
	// The task list carries the workflow state and the check summary too, so a
	// completed action refreshes it rather than leaving the dashboard behind.
	return m, m.load()
}

// reviewView renders the review screen (FR-REV-001 to FR-REV-004).
func (m Model) reviewView() string {
	task, ok := m.task(m.selected)
	if !ok {
		return headingStyle.Render("review") + "\n\n" +
			mutedStyle.Render("this task is no longer listed") +
			m.footer(keyHints(keyHint("esc", "back"), keyHint("q", "quit")))
	}

	var out strings.Builder
	out.WriteString(headingStyle.Render(task.Key+"  review") + "\n")
	out.WriteString(mutedStyle.Render(task.ProjectID+" · "+task.Title) + "\n\n")

	out.WriteString(field("workflow", task.Workflow))
	out.WriteString(field("decision", reviewDecision(m.review.status.Review)))
	out.WriteString(field("checks", reviewChecksSummary(m.review.status.Review)))
	if summary := m.review.status.Review.Summary; summary != "" {
		out.WriteString(field("the agent says", summary))
	}

	if !m.review.loaded {
		out.WriteString("\n" + mutedStyle.Render("comparing every repository against its recorded base…") + "\n")
		return out.String() + m.footer(reviewHints())
	}

	out.WriteString("\n" + headingStyle.Render("repositories") +
		mutedStyle.Render("  each compared against its own recorded base commit") + "\n")
	out.WriteString(m.reviewRepositories())

	if checks := m.review.status.Review.Checks; len(checks) > 0 {
		out.WriteString("\n" + headingStyle.Render("checks") + "\n")
		out.WriteString(reviewChecks(checks))
	}

	if offer := approvalOffer(task); offer != "" {
		out.WriteString("\n" + attentionStyle.Render(offer) + "\n")
	}
	for _, note := range m.review.status.Notes {
		out.WriteString("\n" + attentionStyle.Render("note") + " " + note + "\n")
	}
	if m.review.err != nil {
		out.WriteString("\n" + failureStyle.Render(m.review.err.Error()) + "\n")
	}
	if m.review.pending != "" {
		out.WriteString("\n" + mutedStyle.Render("waiting for "+string(m.review.pending)+"…") + "\n")
	}

	return out.String() + m.footer(reviewHints())
}

func reviewHints() string {
	return keyHints(
		keyHint("↑↓", "repository"),
		keyHint("d", "diff"),
		keyHint("e", "editor"),
		keyHint("s", "status"),
		keyHint("A", "approve"),
		keyHint("C", "request changes"),
		keyHint("P", "pending"),
		keyHint("V", "run checks"),
		keyHint("a", "attach"),
		keyHint("r", "refresh"),
		keyHint("esc", "back"),
	)
}

// reviewRepositories renders one block per repository, with the cursor.
//
// Two lines each rather than a row of columns, for the reason the task detail
// gives: the base commit, the branch, and the worktree path are what a user
// reads this screen to find, and a truncated one has to be looked up somewhere
// else.
func (m Model) reviewRepositories() string {
	rows := m.review.status.Repositories
	if len(rows) == 0 {
		return mutedStyle.Render("  none selected") + "\n"
	}

	var out strings.Builder
	for i, row := range rows {
		marker := "  "
		if i == m.review.cursor {
			marker = selectedStyle.Render("▸ ")
		}

		out.WriteString(marker + headingStyle.Render(row.RepositoryID) +
			mutedStyle.Render("  "+accessLabel(row.Access)) + "  " + reviewChangeSummary(row) + "\n")
		out.WriteString("    " + mutedStyle.Render("base    ") + shortCommit(row.BaseCommit))
		if row.BaseRef != "" {
			out.WriteString(mutedStyle.Render("  (" + row.BaseRef + ")"))
		}
		out.WriteString("\n")

		head := mutedStyle.Render("nothing committed yet")
		if row.HeadCommit != "" {
			head = shortCommit(row.HeadCommit)
			if row.Ahead > 0 {
				head += mutedStyle.Render("  " + strconv.Itoa(row.Ahead) + " commit(s) ahead of the base")
			}
		}
		out.WriteString("    " + mutedStyle.Render("head    ") + head + "\n")
		out.WriteString("    " + mutedStyle.Render("worktree ") + row.WorktreePath + "\n")
	}
	return out.String()
}

// reviewChangeSummary renders what one repository holds against its base.
//
// The line counts cover tracked changes only, which the note beside the table
// says: an untracked file is counted as changed and its lines are not, because
// counting them would mean writing to the index (ADR-036).
func reviewChangeSummary(row api.ReviewRepository) string {
	if row.SummarizedAt == nil {
		return mutedStyle.Render("not compared")
	}
	summary := strconv.Itoa(row.ChangedFiles) + " file(s)"
	if row.Insertions > 0 || row.Deletions > 0 {
		summary += "  +" + strconv.Itoa(row.Insertions) + " -" + strconv.Itoa(row.Deletions)
	}
	if row.Dirty {
		summary += mutedStyle.Render("  uncommitted")
	}
	if row.Merged {
		summary += mutedStyle.Render("  merged into its base branch")
	}
	return summary
}

// reviewDecision renders the user's decision so far.
func reviewDecision(review api.Review) string {
	switch review.Status {
	case "approved":
		return "approved"
	case "changes_requested":
		return "changes requested"
	default:
		return "pending  " + mutedStyle.Render("(A to approve, C to send back, P to leave pending)")
	}
}

// reviewChecksSummary says what the checks amount to and who ran them.
//
// The distinction is the point of the line. A gated result was enforced by Feat
// running the command itself; an agent-reported one is a claim, and a screen
// that showed them alike would tell the user something Feat does not know
// (FR-AGENT-006).
func reviewChecksSummary(review api.Review) string {
	if len(review.Checks) == 0 {
		return absent + "  " + mutedStyle.Render("(no checks have reported)")
	}

	var passed, failed, other, skipped int
	for _, check := range review.Checks {
		switch check.Status {
		case "passed":
			passed++
		case "failed":
			failed++
		case "skipped":
			skipped++
		default:
			other++
		}
	}

	parts := []string{strconv.Itoa(passed) + " passed"}
	if failed > 0 {
		parts = append(parts, strconv.Itoa(failed)+" failed")
	}
	if other > 0 {
		parts = append(parts, strconv.Itoa(other)+" did not report")
	}
	if skipped > 0 {
		parts = append(parts, strconv.Itoa(skipped)+" skipped")
	}

	summary := strings.Join(parts, ", ")
	if review.Gated {
		return summary + "  " + mutedStyle.Render("(run by Feat)")
	}
	return summary + "  " + mutedStyle.Render("(reported by the agent, not verified)")
}

// reviewChecks renders one line per check result.
func reviewChecks(checks []api.ReviewCheck) string {
	var out strings.Builder
	for _, check := range checks {
		name := check.ID
		if check.RepositoryID != "" {
			name += mutedStyle.Render(" (" + check.RepositoryID + ")")
		}

		status := check.Status
		if check.Status == "failed" {
			status = failureStyle.Render(status)
		}
		reporter := "the agent reported this"
		if check.Reporter == "provider" {
			reporter = "Feat ran this"
		}

		out.WriteString("  " + name + "  " + status + mutedStyle.Render("  "+reporter) + "\n")
		if detail := strings.TrimSpace(check.Detail); detail != "" {
			out.WriteString(indent(detail, "      ") + "\n")
		}
	}
	return out.String()
}

// shortCommit renders a commit at the length a person reads.
func shortCommit(commit string) string {
	if commit == "" {
		return absent
	}
	return commit[:min(12, len(commit))]
}
