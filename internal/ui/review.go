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
	// scroll is the first line of the panel the region shows.
	//
	// The panel is what detail and review became, and it is taller than either
	// was: a task with two repositories, a check that failed, and a brief does
	// not fit a region at any terminal size worth supporting. Clipping it
	// silently would hide the brief, which FR-UI-003 requires.
	scroll int
	// pending is the action in flight, so the screen says what it is waiting for.
	pending api.ReviewAction
	// observing reports that the comparison the panel opened with is in flight.
	//
	// It is separate from pending, which is the action a key press asked for.
	// The opening read is nobody's key press, and it is what the loading
	// indicator is drawn for here: a comparison walks every one of the task's
	// worktrees and takes seconds, and until this the line saying so was as still
	// as the one preparation used to draw.
	observing bool
	// err is a failed action, shown rather than thrown.
	err error
}

// reviewMsg carries the result of one review action.
//
// task is what the request named, which is what the response is matched
// against. A comparison walks every one of a task's worktrees and can take
// seconds, so one issued before the user moved on arrives after: without this
// the panel drew one task's agent report, check results and expanded diff and
// editor commands under another task's name, and `d` and `e` opened the wrong
// worktree.
//
// It is what the request named rather than what the daemon resolved it to,
// because `feat review <task>` may name a task by its short key and a response
// has to be matchable against the request that produced it.
type reviewMsg struct {
	task   string
	action api.ReviewAction
	status api.ReviewStatus
	err    error
}

// openTask shows the task panel for the task an action applies to.
//
// A draft opens it too, and gets the half of the panel it has. Review used to
// refuse one outright, which was right about a draft having nothing to compare
// and wrong about there being nothing to show: a draft has a brief, a project,
// and the repositories it will bind, and refusing left the tab where it was
// under the name of a task the user had moved away from.
func (m Model) openTask() (tea.Model, tea.Cmd) {
	task, ok := m.subject()
	if !ok {
		// Nothing is selected, so there is nothing to draw. The panel still
		// takes the region and says so, rather than leaving the previous tab up.
		m.screen = screenTask
		return m, nil
	}

	m.screen = screenTask
	m.selected = task.ID
	m.review = reviewModel{task: task.ID}
	if isDraft(task) {
		return m, nil
	}
	// Opening the panel compares every repository against its own recorded
	// base. Nothing else happens: a review action never starts, stops, or
	// removes anything.
	m.review.observing = true
	return m, m.reviewAction(api.ReviewObserve)
}

// reviewAction performs one action against the daemon.
func (m Model) reviewAction(action api.ReviewAction) tea.Cmd {
	backend, id := m.backend, m.review.task
	return func() tea.Msg {
		status, err := backend.Review(context.Background(), id, action)
		return reviewMsg{task: id, action: action, status: status, err: err}
	}
}

// taskPanelKey routes a key press on the task panel.
//
// The plain arrows move the repository under the cursor, because every external
// command on this panel is about one repository. Scrolling the panel itself is
// on the page keys, which nothing else uses: a panel that took the arrows for
// scrolling would leave the diff and editor commands with no way to say which
// repository they meant.
func (m Model) taskPanelKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		// Through selectTab rather than by setting the screen, for the reason
		// runtime's own esc goes through openTask: the tab this lands on holds
		// what it was last told about one task, and the selection may have moved
		// while the panel had the keyboard. Opening it is what discards the pane
		// it was holding and asks tmux for the selected task's.
		return m.selectTab(tabTerminal)

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

	case "pgup":
		m.review.scroll = m.panelScroll(-panelPage)
		return m, nil

	case "pgdown":
		m.review.scroll = m.panelScroll(panelPage)
		return m, nil

	case "d":
		return m.runReviewCommand(api.ReviewCommandKindDiff)

	case "e":
		return m.runReviewCommand(api.ReviewCommandKindEditor)

	case "A":
		return m.startReview(api.ReviewApprove)

	case "C":
		return m.startReview(api.ReviewRequestChanges)

	case "V":
		return m.startReview(api.ReviewVerify)

	case "a":
		return m.attach()

	case "s":
		// The shell, as everywhere else. The status command used to be here and
		// is not reachable from the panel any more (ADR-045): it printed a line
		// or two and exited, which the altscreen swallowed before anyone could
		// read it, and the panel already carries what it would have said.
		return m.shell()

	case "r":
		return m.startReview(api.ReviewObserve)
	}
	// Everything the panel does not claim is the dashboard's. `?` and `!` open
	// their overlays from here, `n` prepares a task, `z` resumes one — none of
	// which this panel has any reason to refuse, and all of which it used to
	// swallow by returning here.
	return m.dashboardKey(key)
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
//
// A response for a task the panel is no longer about is dropped rather than
// drawn, as applyFrame drops a frame whose pane belongs to another task. Nothing
// is cleared for it either: the pending marker belongs to whatever this panel is
// waiting for now.
func (m Model) applyReview(message reviewMsg) (tea.Model, tea.Cmd) {
	if message.task != m.review.task {
		return m, nil
	}

	m.review.pending, m.review.observing = "", false
	if message.err != nil {
		m.review.err = message.err
		return m, nil
	}

	m.review.err = nil
	m.review.status = message.status
	m.review.loaded = true
	// The response names the task the daemon resolved the request to, which is
	// how the short key `feat review <task>` accepts becomes the identifier the
	// rest of the dashboard matches against. Without it the panel drew "this task
	// is no longer listed" — nothing in m.tasks equals an eight-character key —
	// while `A`, `C` and `V` went on working, because they act on the review's own
	// task: the one screen saying the task did not exist was the screen from
	// which approving it succeeded.
	if id := message.status.Task.ID; id != "" && id != m.review.task {
		if m.selected == m.review.task {
			m.selected = id
		}
		m.review.task = id
	}
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

// reviewDecision renders the user's decision, which is the task's workflow
// state and is read from there.
//
// The keys are offered only where the transition exists. The decision used to be
// read from the review aggregate's own copy of it, which knew nothing about the
// task: a working task was offered "A to approve" and the daemon refused it,
// because approving applies to a task whose agent has asked for review
// (domain.workflowTransitions, ADR-047).
//
// Requesting changes is the one decision that is not finished when it is
// recorded. Feat tells the agent nothing about it: the revision reaches the
// session when the user types it, and submitting that prompt is what returns the
// task to working (FR-AGENT-009). So the line says what is left to do, rather
// than marking a task with a decision that has not yet travelled anywhere.
func reviewDecision(task api.Task) string {
	switch task.Workflow {
	case "approved":
		// Approval's own next step — the offer to stop services a task was
		// approved with still running — is on the runtime line, where it appears
		// only when there are services to stop.
		return "approved"
	case "changes_requested":
		return "changes requested  " + mutedStyle.Render("(a to attach and say what to change)")
	case "verifying":
		return "pending  " + mutedStyle.Render("(the project's checks are running)")
	case "review_requested", "ready_for_review", "verification_failed":
		return "pending  " + mutedStyle.Render("(A to approve, C to send back)")
	default:
		return absent + "  " + mutedStyle.Render("(the agent has not asked for review)")
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
//
// A check that never reported is named as that rather than as "unknown", in the
// same words the summary line above it uses. The screen is where a user sent
// here by a blocked gate reads which check it was and why it did not run, so the
// two lines have to be about the same thing (ADR-051).
func reviewChecks(checks []api.ReviewCheck) string {
	var out strings.Builder
	for _, check := range checks {
		name := check.ID
		if check.RepositoryID != "" {
			name += mutedStyle.Render(" (" + check.RepositoryID + ")")
		}

		status := check.Status
		switch check.Status {
		case "failed":
			status = failureStyle.Render(status)
		case "passed", "skipped":
		default:
			// Amber rather than red: it needs the user, and it is not a verdict
			// against the work.
			status = attentionStyle.Render("did not report")
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
