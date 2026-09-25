package ui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// reviewModel is the state of the review screen. It holds what the last action
// returned rather than deriving it from the task list, because the
// per-repository comparison, the check results, and the expanded commands are
// what that action saw. The cursor is a repository, since every command on this
// screen is about one.
type reviewModel struct {
	// task is the task under review.
	task string
	// status is what the last action reported, and whether one has run at all.
	status api.ReviewStatus
	loaded bool
	// cursor is the selected repository.
	cursor int
	// scroll is the first line of the panel the region shows. A task with two
	// repositories, a check that failed, and a brief does not fit a region at any
	// terminal size worth supporting, and clipping it silently would hide the
	// brief FR-UI-003 requires.
	scroll int
	// pending is the action in flight, so the screen says what it is waiting for.
	pending api.ReviewAction
	// observing reports that a comparison is in flight, whoever asked for it. It
	// is separate from pending, which is every other action, because the two can
	// be outstanding at once: a gate landing while the user waits for a check run
	// is exactly that. The division is by action rather than by who asked, which
	// is how applyReview clears it.
	//
	// It is what the loading indicator is drawn for: a comparison walks every one
	// of the task's worktrees and takes seconds.
	observing bool
	// err is a failed action, shown rather than thrown.
	err error
}

// reviewMsg carries the result of one review action. task is what the request
// named, which is what the response is matched against: a comparison walks
// every one of a task's worktrees and takes seconds, so one issued before the
// user moved on arrives after, and without the match `d` and `e` open the wrong
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

// openTask shows the task panel for the task an action applies to. A draft
// opens it too and gets the half of the panel it has: it has nothing to
// compare, but it has a brief, a project, and the repositories it will bind,
// and refusing would leave the tab under the name of a task the user had moved
// away from.
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

// taskPanelKey routes a key press on the task panel. The plain arrows move the
// repository under the cursor, because every external command here is about one
// repository. Scrolling is on the page keys: arrows spent on it would leave the
// diff and editor commands with no way to say which repository they meant.
func (m Model) taskPanelKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
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

	case "V":
		return m.startReview(api.ReviewVerify)

	case "P":
		// Publication has a screen of its own, because it is a sequence — read the
		// draft, edit it, approve it — and what it does reaches somebody else's
		// server. Opening it composes what publishing would do and sends nothing
		// (ADR-070, ADR-073).
		return m.openPublication()

	case "a":
		return m.attach()

	case "s":
		// The shell, as everywhere else. The status command is not reachable from
		// the panel (ADR-045): it printed a line or two and exited, which the
		// altscreen swallowed, and the panel already carries what it would say.
		return m.shell()

	case "r":
		return m.startReview(api.ReviewObserve)
	}
	// Everything the panel does not claim is the dashboard's: `?` and `!` open
	// their overlays from here, `n` prepares a task, and `z` resumes one.
	return m.dashboardKey(key)
}

// startReview records what the screen is waiting for and asks for it. Which
// marker records the wait is the split applyReview clears by, and it is on the
// action rather than on who asked: recording `r`'s comparison as pending leaves
// the indicator running until the panel is closed, because applyReview clears
// observing for it.
func (m Model) startReview(action api.ReviewAction) (tea.Model, tea.Cmd) {
	if action == api.ReviewObserve {
		m.review.observing = true
	} else {
		m.review.pending = action
	}
	m.review.err = nil
	m.status = ""
	return m, m.reviewAction(action)
}

// runReviewCommand yields this terminal to one of the project's own tools. Feat
// renders no diff of its own (ADR-006): it opens what the user configured, in
// the worktree of the repository under the cursor, and takes the terminal back
// after.
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

// reviewOutdatedBy reports whether an event means this panel is drawing a
// review that has moved on. Re-reading the task list is not the whole answer:
// the check results come from an observation, made when the panel opens and
// when a key asks for one. A completion gate finishes minutes after the key
// that started it (daemon startGate), so without this the panel that asked for
// the run never sees it.
//
// It is deliberately narrow. An observation walks every repository with Git,
// and an agent's hooks produce events several times a turn, so only a change to
// what this panel draws about the task it is drawing earns one. Observing
// records nothing itself, so no event leads to a second observation.
func (m Model) reviewOutdatedBy(event api.Event) bool {
	if m.screen != screenTask || m.review.task == "" || m.review.observing {
		return false
	}
	changed := event.TaskEvent
	if changed == nil || changed.TaskID != m.review.task {
		return false
	}
	// Spelled as the wire spells them, which is how this package reads every
	// other state the API carries as a string. A workflow change is what a gate
	// starting and landing looks like; a review change is what it recorded.
	switch changed.Type {
	case "task_workflow_changed", "review_state_changed":
		return true
	default:
		return false
	}
}

// applyReview records what an action reported. A response for a task the panel
// is no longer about is dropped rather than drawn, as applyFrame drops a frame
// whose pane belongs to another task. Nothing is cleared for it either: the
// pending marker belongs to whatever this panel is waiting for now.
func (m Model) applyReview(message reviewMsg) (tea.Model, tea.Cmd) {
	if message.task != m.review.task {
		return m, nil
	}

	// Only the request this answers. An observation and a check run can be
	// outstanding at once — a gate landing while the user waits for one is
	// exactly that — and clearing both would stop the indicator while the other
	// was still in flight.
	if message.action == api.ReviewObserve {
		m.review.observing = false
	} else {
		m.review.pending = ""
	}
	if message.err != nil {
		m.review.err = message.err
		return m, nil
	}

	m.review.err = nil
	m.review.status = message.status
	m.review.loaded = true
	// The response names the task the daemon resolved the request to, which is
	// how the short key `feat review <task>` accepts becomes the identifier the
	// rest of the dashboard matches against. Without it the panel draws "this
	// task is no longer listed", because nothing in m.tasks equals an
	// eight-character key, while `A`, `C` and `V` go on working against the
	// review's own task.
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

// reviewChangeSummary renders what one repository holds against its base. The
// line counts cover tracked changes only: an untracked file is counted as
// changed and its lines are not, because counting them would mean writing to
// the index (ADR-036).
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

// reviewExits names what a task in a review state can do next. It replaces the
// decision field, whose two keys went unpressed — approve once in fifty-one
// tasks and requesting changes never — while the transitions carrying the real
// loop, back to working by attaching and out through cleanup, had no key here
// (ADR-086).
//
// A task whose agent has not asked for review has no exits to name and gets no
// line, which is the panel's rule throughout: a check with nothing to report
// reports nothing. Nor does a task whose checks are running, because what
// happens next is the gate's rather than the user's.
func reviewExits(task api.Task) string {
	switch task.Workflow {
	case "review_requested", "ready_for_review", "verification_failed":
		return "P to publish · a to attach and revise"
	default:
		return ""
	}
}

// reviewChecksSummary says what the checks amount to and who ran them. A gated
// result was enforced by Feat running the command itself; an agent-reported one
// is a claim, and showing them alike would tell the user something Feat does
// not know (FR-AGENT-006).
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

// reviewChecks renders one line per check result. A check that never reported
// is named as that rather than as "unknown", in the words the summary line
// above it uses: this is where a user sent by a blocked gate reads which check
// it was and why it did not run (ADR-051).
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
		if detail := checkDetail(check); detail != "" {
			out.WriteString(indent(detail, "      ") + "\n")
		}
	}
	return out.String()
}

// checkDetail is what one check's line has under it, or nothing. A check Feat
// ran and that passed says nothing more: its detail is the whole output of the
// user's own build command, forty lines of `ok  <package>  (cached)` under a
// line that has already said "passed". The excerpt is still stored, because
// ADR-036 records it, and hiding it here is a rule about this panel rather than
// about the record.
//
// Every other detail earns its room. A failure's output is why it failed, a
// skip carries the reason it was skipped, and a check that did not report
// carries Feat's own account — a bound that elapsed, a runner this build does
// not have — which is the only place the difference between "did not run" and
// "ran and found nothing" is written down (ADR-028).
//
// The agent's own details are short by construction: a sentence it wrote about
// its work rather than a command's output, so the panel keeps them whatever the
// status.
func checkDetail(check api.ReviewCheck) string {
	if check.Status == "passed" && check.Reporter == "provider" {
		return ""
	}
	return strings.TrimSpace(check.Detail)
}

// shortCommit renders a commit at the length a person reads.
func shortCommit(commit string) string {
	if commit == "" {
		return absent
	}
	return commit[:min(12, len(commit))]
}
