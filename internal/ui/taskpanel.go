package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// panelPage is how far the page keys move the task panel.
const panelPage = 10

// stackedFooterHeight is what m.footer occupies in the narrow fallback: the
// rule that separates it from the content above, a blank line, the status line,
// a blank line, the hints, and the daemon.
const stackedFooterHeight = 6

// taskView renders the task panel as a whole terminal, which is what the narrow
// fallback draws when there is no room for the three regions.
func (m Model) taskView() string {
	width, height := m.frameSize()
	body := m.taskBody(width, height-stackedFooterHeight)

	if _, ok := m.task(m.selected); !ok {
		return body + m.footer(keyHints(keyHint("A", "task list"), keyHint("q", "quit")))
	}
	return body + m.footer(taskPanelHints())
}

// taskBody renders the task panel into a region, scrolled to where the user is.
// A one-repository task fits the region now the brief has a tab of its own
// (ADR-086), and a task with several repositories or a long check detail still
// outgrows it. What does not fit is scrolled to, and the last line says what is
// above and below, because a panel clipped in silence reads as a panel that is
// short.
//
// It is wrapped to the region before it is measured. This is prose — a note, a
// captured command's output, a sentence about a field Feat could not fill — and
// prose cut at the region's edge loses the half that says what to do about it.
// Wrapping first also keeps the scroll honest: the lines counted are the lines
// drawn.
func (m Model) taskBody(width, height int) string {
	return scrollWindow(m.wrappedPanel(width), m.review.scroll, width, height)
}

// scrollWindow is the part of a rendered body that fits the region, under a
// line saying how much of it is above and below. The two bodies that scroll
// share it and keep their own offsets, because a brief and a task panel sharing
// one would each move the other's position.
func scrollWindow(body string, offset, width, height int) string {
	if height <= 0 {
		return body
	}

	lines := strings.Split(body, "\n")
	if len(lines) <= height {
		return body
	}

	// One line of the region belongs to the note, so the window is that much
	// shorter than the space.
	visible := height - 1
	offset = clampScroll(offset, len(lines), height)
	window := lines[offset : offset+visible]

	var parts []string
	if offset > 0 {
		parts = append(parts, count(offset, "line above", "lines above"))
	}
	if below := len(lines) - offset - visible; below > 0 {
		parts = append(parts, count(below, "line below", "lines below"))
	}
	note := strings.Join(parts, ", ") + "  ·  pgup/pgdn to scroll"

	return strings.Join(window, "\n") + "\n" + mutedStyle.Render(truncate(note, width))
}

// clampScroll keeps an offset inside a panel of this many lines.
func clampScroll(offset, total, height int) int {
	most := total - (height - 1)
	if most < 0 {
		most = 0
	}
	if offset > most {
		offset = most
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// panelScroll is where the page keys leave the panel, bounded by its length.
// The bound is applied here rather than while rendering, because rendering
// cannot write back and holding pgdn past the end would build an offset that
// took as many presses to undo.
func (m Model) panelScroll(delta int) int {
	// The region's own size, which already excludes the card's header. It is
	// measured wrapped, because wrapped is how it is drawn, and a bound counted
	// on the unwrapped panel stops the scroll short of its own last lines.
	width, height := m.mainRegionSize()
	total := len(strings.Split(m.wrappedPanel(width), "\n"))
	return clampScroll(m.review.scroll+delta, total, height)
}

// wrappedPanel is the task panel re-flowed to the width it will be drawn at.
// The parts a user reads it for are not Feat's own text — a check's captured
// output, an error another program produced — and those carry tabs and carriage
// returns worth nothing to the wrap and everything to the terminal (ADR-054).
func (m Model) wrappedPanel(width int) string {
	panel := plainText(m.taskPanel())
	if width <= 0 {
		return panel
	}
	return ansi.Wrap(panel, width, "")
}

// taskPanel renders one task: what it is, what it has changed, and what is left
// to decide about that. ADR-042 made detail and review one tab, because they
// shared their subject, header, workflow, repository list, and check summary
// and neither filled the region. This carries what FR-UI-003 asks of detail and
// what FR-REV-001 asks of review, once each.
func (m Model) taskPanel() string {
	task, ok := m.task(m.selected)
	if !ok {
		if m.selected == "" {
			return headingStyle.Render("task") + "\n\n" + mutedStyle.Render("no task selected")
		}
		return headingStyle.Render("task") + "\n\n" +
			mutedStyle.Render("this task is no longer listed")
	}

	var out strings.Builder
	out.WriteString(headingStyle.Render(task.Key+"  "+task.Title) + "\n\n")

	// What the last reconciliation pass found about this task, before the fields
	// it contradicts.
	out.WriteString(m.recoveryBlock(m.recoveryFindings(task)))

	// Six lines and what only they can say. Attention, agent state as a bare
	// word, elapsed time, and the task's identifiers are four cells to the left
	// in the rail, and a panel repeating them ran to thirty-five lines (ADR-086).
	workflow := task.Workflow
	if exits := reviewExits(task); exits != "" {
		workflow = continued(workflow, mutedStyle.Render(exits))
	}
	out.WriteString(field("workflow", workflow))
	out.WriteString(failureBlock(task))
	out.WriteString(field("agent", agentDetail(task)))
	// The runtime field carries the offer to stop services after an approval,
	// which is why the review lines below do not repeat it.
	out.WriteString(field("runtime", runtimeDetail(task)))
	out.WriteString(field("checks", m.checksField(task)))
	out.WriteString(field("resources", m.resourceDetail(task)))

	// What the review found, under the fields and without a heading. The decision
	// field went with the two keys it named (ADR-086), so what is left to say
	// about a task in a review state is on the workflow field, as the exits it
	// has.
	out.WriteString("\n")
	if summary := m.review.status.Review.Summary; summary != "" {
		out.WriteString(field("the agent says", summary))
	}
	switch {
	case isDraft(task):
		out.WriteString(mutedStyle.Render(
			"  a draft has nothing to compare yet; it owns no worktree until it is launched") + "\n")
	case m.review.observing:
		// The wait every task panel opens with, and the one `r` asks for again. It
		// walks each of the task's worktrees, so on a task with three it is seconds
		// of an otherwise complete and still panel; the mark says they are being
		// spent (see activity).
		//
		// On the comparison in flight rather than on never having had one, because
		// a refresh on a loaded panel otherwise draws nothing and a failed
		// comparison leaves this line under its own error, saying it is still being
		// made.
		out.WriteString(mutedStyle.Render(
			"  "+m.activity.mark("comparing every repository against its recorded base…")) + "\n")
	}
	for _, note := range m.review.status.Notes {
		out.WriteString("  " + attentionStyle.Render("note") + " " + note + "\n")
	}
	if m.review.err != nil {
		out.WriteString("  " + daemonNote(m.review.err, task) + "\n")
	}
	if m.review.pending != "" {
		out.WriteString("  " + mutedStyle.Render(
			m.activity.mark("waiting for "+string(m.review.pending)+"…")) + "\n")
	}

	out.WriteString("\n" + headingStyle.Render("repositories"))
	if m.review.loaded {
		out.WriteString(mutedStyle.Render("  each compared against its own recorded base commit"))
	}
	out.WriteString("\n" + m.taskRepositories(task))

	if checks := m.review.status.Review.Checks; len(checks) > 0 {
		out.WriteString("\n" + headingStyle.Render("checks"))
		// Said once, over the results rather than on each of them. While a gate
		// runs these are the run before it: kept, because the last thing known is
		// worth reading, and dated, because it is not what is happening.
		if verifying(task) {
			out.WriteString(mutedStyle.Render("  from the run before this one"))
		}
		out.WriteString("\n" + reviewChecks(checks))
	}

	out.WriteString(publicationBlock(task))

	// The tmux target is not here. The socket is one value across every session
	// on this machine, being the runtime path by construction, and the other
	// three are object ids chosen for stable identity rather than for a reader.
	// Running a tmux command by hand is what `a` and `feat attach` serve, and
	// when the daemon is down this panel is not on screen while `task.json` still
	// holds them (ADR-086).
	//
	// Nor is the brief, which is a document and unbounded: it has a tab of its
	// own.

	return out.String()
}

// publicationBlock is what this task has published, or tried to. It is here
// rather than only on the publication screen because the record outlives the
// screen: nothing is rolled back, and a user who closed the screen should not
// have to compose a fresh plan — a lock, a walk of every repository, a read of
// the agent's outbox — to be told a fact that was written down (ADR-073).
//
// A task that has never published has no section at all, which is the panel's
// rule throughout: a check with nothing to report reports nothing.
func publicationBlock(task api.Task) string {
	if task.Publication == nil || len(task.Publication.Repositories) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("\n" + headingStyle.Render("publication") + "\n")
	for _, entry := range task.Publication.Repositories {
		state := publicationEntryState(entry)
		if entry.State == "failed" {
			state = failureStyle.Render(state)
		}
		out.WriteString(field(entry.RepositoryID, state))
	}
	return out.String()
}

// failureBlock is why a failed task failed, under the state it explains. It
// sits there rather than in a section of its own because `failed` and its
// reason are one fact, and the reason is otherwise only in the task's event log
// on disk.
//
// The reason is printed as it was reported and wrapped rather than truncated.
// It names a Compose service, a mount, or a path, and a cut sentence loses the
// end that identifies which one.
func failureBlock(task api.Task) string {
	if task.Failure == nil {
		return ""
	}
	var out strings.Builder
	out.WriteString("  " + failureStyle.Render(task.Failure.Reason) + "\n")
	if !task.Failure.At.IsZero() {
		out.WriteString(mutedStyle.Render("  failed at "+task.Failure.At.Local().Format("15:04:05")) + "\n")
	}
	return out.String()
}

// checksField is what is known about this task's checks, from whichever source
// has reported. The task snapshot carries the agent's own count; the review
// status carries the results with the reporter of each, which is the richer
// answer and the one that can say Feat ran them.
//
// A task whose checks are running has neither answer yet. A gate records
// nothing until it finishes, so what is stored while it runs is the run before
// it, and reporting that would tell a user who has just started a run that it
// had failed.
func (m Model) checksField(task api.Task) string {
	if verifying(task) {
		return "running  " + mutedStyle.Render("(Feat is running the project's configured checks)")
	}
	if m.review.loaded && len(m.review.status.Review.Checks) > 0 {
		return reviewChecksSummary(m.review.status.Review)
	}
	return verificationDetail(task)
}

// verifying reports whether this task's configured checks are running now.
func verifying(task api.Task) bool { return task.Workflow == "verifying" }

// taskRepositories renders one block per repository the task binds. It walks
// the task's own bindings rather than the comparison's rows, so a draft — which
// has bindings and no worktrees — is drawn with what it has. Where a comparison
// exists its numbers are used, because those were measured against the recorded
// base (FR-REV-001).
//
// Four lines each rather than a row of columns: the base commit, the branch,
// and the worktree path are what a user reads this panel to find, and a
// truncated one has to be looked up elsewhere.
func (m Model) taskRepositories(task api.Task) string {
	if len(task.Repositories) == 0 {
		return mutedStyle.Render("  none selected") + "\n"
	}
	selected, hasCursor := m.reviewRepository()

	var out strings.Builder
	for _, binding := range task.Repositories {
		row, compared := findReviewRow(m.review.status.Repositories, binding.RepositoryID)

		marker := "  "
		if hasCursor && selected.RepositoryID == binding.RepositoryID {
			marker = selectedStyle.Render("▸ ")
		}

		changed := bindingChangeSummary(binding)
		if compared {
			changed = reviewChangeSummary(row)
		}
		out.WriteString(marker + headingStyle.Render(binding.RepositoryID) +
			mutedStyle.Render("  "+accessLabel(binding.Access)) + "  " + changed + "\n")

		base, ref := binding.BaseCommit, binding.BaseRef
		if compared {
			base, ref = row.BaseCommit, row.BaseRef
		}
		line := shortCommit(base)
		if ref != "" {
			line += mutedStyle.Render("  (" + ref + ")")
		}
		out.WriteString("    " + mutedStyle.Render("base     ") + line + "\n")

		if compared {
			head := mutedStyle.Render("nothing committed yet")
			if row.HeadCommit != "" {
				head = shortCommit(row.HeadCommit)
				if row.Ahead > 0 {
					head += mutedStyle.Render("  " + strconv.Itoa(row.Ahead) +
						" commit(s) ahead of the base")
				}
			}
			out.WriteString("    " + mutedStyle.Render("head     ") + head + "\n")
		}

		branch := binding.Branch
		if branch == "" {
			branch = mutedStyle.Render("no branch (read-only)")
		}
		out.WriteString("    " + mutedStyle.Render("branch   ") + branch + "\n")

		worktree := binding.WorktreePath
		if worktree == "" {
			worktree = mutedStyle.Render("not created yet")
		}
		out.WriteString("    " + mutedStyle.Render("worktree ") + worktree + "\n")
	}
	return out.String()
}

// findReviewRow is the comparison of one repository, when one has been made.
func findReviewRow(rows []api.ReviewRepository, repository string) (api.ReviewRepository, bool) {
	for _, row := range rows {
		if row.RepositoryID == repository {
			return row, true
		}
	}
	return api.ReviewRepository{}, false
}

// bindingChangeSummary is what the task snapshot last observed of a repository,
// for a task no comparison has been run against.
func bindingChangeSummary(binding api.TaskRepository) string {
	if binding.Observation == nil {
		return mutedStyle.Render("not compared")
	}
	summary := strconv.Itoa(binding.Observation.ChangedFiles) + " file(s)"
	if binding.Observation.Dirty {
		summary += mutedStyle.Render("  uncommitted")
	}
	return summary
}

// taskPanelHints are the panel's own keys. The external commands are diff and
// editor, each about the repository under the cursor (FR-REV-002). The status
// command is not among them, because `s` opens the task's shell here as it does
// everywhere else (ADR-045).
func taskPanelHints() string {
	return keyHints(
		keyHint("j k", "repository"),
		keyHint("d", "diff"),
		keyHint("e", "editor"),
		keyHint("V", "run checks"),
		keyHint("P", "publish"),
		keyHint("pgup/pgdn", "scroll"),
	)
}
