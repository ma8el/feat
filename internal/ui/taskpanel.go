package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// panelPage is how far the page keys move the task panel.
const panelPage = 10

// stackedFooterHeight is what m.footer occupies in the narrow fallback: the rule
// that separates it from the content above, a blank line, the status line, a
// blank line, the hints, and the daemon.
const stackedFooterHeight = 6

// taskView renders the task panel as a whole terminal, which is what the narrow
// fallback draws when there is no room for the three regions.
func (m Model) taskView() string {
	width, height := m.frameSize()
	body := m.taskBody(width, height-stackedFooterHeight)

	if _, ok := m.task(m.selected); !ok {
		return body + m.footer(keyHints(keyHint("esc", "back"), keyHint("q", "quit")))
	}
	return body + m.footer(taskPanelHints())
}

// taskBody renders the task panel into a region, scrolled to where the user is.
//
// The panel is taller than the region on any terminal worth supporting once a
// task has two repositories and a brief, so what does not fit is scrolled to
// rather than lost. The last line says what is above and below: a panel clipped
// in silence reads as a panel that is short, and FR-UI-003 requires the brief to
// be reachable.
//
// It is wrapped to the region before it is measured, and it is the one body that
// is. Everything else the dashboard draws is a line whose width it controls, and
// a rendered pane must never be re-flowed; this is prose — a brief, a note, a
// sentence explaining what a field could not be filled with — and prose cut at
// the region's edge loses the half of the sentence that says what to do about it.
// Wrapping before the split is also what keeps the scroll honest: the lines
// counted are the lines drawn.
func (m Model) taskBody(width, height int) string {
	panel := m.wrappedPanel(width)
	if height <= 0 {
		return panel
	}

	lines := strings.Split(panel, "\n")
	if len(lines) <= height {
		return panel
	}

	// One line of the region belongs to the note, so the window is that much
	// shorter than the space.
	visible := height - 1
	offset := clampScroll(m.review.scroll, len(lines), height)
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
//
// The bound is applied here rather than while rendering, because rendering
// cannot write back: without it, holding pgdn past the end would build up an
// offset that took as many presses to undo.
func (m Model) panelScroll(delta int) int {
	// The region's own size, which already excludes the card's header: the panel
	// is drawn into what is left under the rule. It is measured wrapped, because
	// wrapped is how it is drawn, and a bound counted on the unwrapped panel
	// stops the scroll short of its own last lines.
	width, height := m.mainRegionSize()
	total := len(strings.Split(m.wrappedPanel(width), "\n"))
	return clampScroll(m.review.scroll+delta, total, height)
}

// wrappedPanel is the task panel re-flowed to the width it will be drawn at.
//
// Made measurable before it is measured. Most of the panel is Feat's own text,
// but the parts a user reads it for are not: a brief they wrote, a check's
// captured output, an error another program produced. Those carry tabs and
// carriage returns, which are worth nothing to the wrap and everything to the
// terminal (ADR-054).
func (m Model) wrappedPanel(width int) string {
	panel := plainText(m.taskPanel())
	if width <= 0 {
		return panel
	}
	return ansi.Wrap(panel, width, "")
}

// taskPanel renders one task: what it is, what it has changed, and what is left
// to decide about that.
//
// Detail and review were two tabs until ADR-042. They were conceptually
// different and shared their subject, their header, their workflow, their
// repository list, and their check summary, and neither filled the main region
// on its own. This carries what FR-UI-003 requires of task detail and what
// FR-REV-001 requires of review, once each.
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
	out.WriteString(headingStyle.Render(task.Key+"  "+task.Title) + "\n")
	out.WriteString(mutedStyle.Render(task.ProjectID+" · "+task.ID) + "\n\n")

	// What the last reconciliation pass found about this task, before the fields
	// it contradicts.
	out.WriteString(m.recoveryBlock(m.recoveryFindings(task)))

	out.WriteString(field("workflow", task.Workflow))
	out.WriteString(failureBlock(task))
	out.WriteString(field("attention", attentionState(task)))
	out.WriteString(field("agent", agentDetail(task)))
	// The runtime field carries the offer to stop services after an approval,
	// which is why the review section below does not repeat it.
	out.WriteString(field("runtime", runtimeDetail(task)))
	out.WriteString(field("resources", m.resourceDetail(task)))
	out.WriteString(field("elapsed", elapsed(task, m.now())))
	out.WriteString(field("source", sourceDetail(task.Source)))

	out.WriteString("\n" + headingStyle.Render("review") + "\n")
	out.WriteString(field("decision", reviewDecision(task)))
	out.WriteString(field("checks", m.checksField(task)))
	if summary := m.review.status.Review.Summary; summary != "" {
		out.WriteString(field("the agent says", summary))
	}
	switch {
	case isDraft(task):
		out.WriteString(mutedStyle.Render(
			"  a draft has nothing to compare yet; it owns no worktree until it is launched") + "\n")
	case !m.review.loaded:
		out.WriteString(mutedStyle.Render(
			"  comparing every repository against its recorded base…") + "\n")
	}
	for _, note := range m.review.status.Notes {
		out.WriteString("  " + attentionStyle.Render("note") + " " + note + "\n")
	}
	if m.review.err != nil {
		out.WriteString("  " + failureStyle.Render(m.review.err.Error()) + "\n")
	}
	if m.review.pending != "" {
		out.WriteString("  " + mutedStyle.Render("waiting for "+string(m.review.pending)+"…") + "\n")
	}

	out.WriteString("\n" + headingStyle.Render("repositories"))
	if m.review.loaded {
		out.WriteString(mutedStyle.Render("  each compared against its own recorded base commit"))
	}
	out.WriteString("\n" + m.taskRepositories(task))

	if checks := m.review.status.Review.Checks; len(checks) > 0 {
		out.WriteString("\n" + headingStyle.Render("checks") + "\n")
		out.WriteString(reviewChecks(checks))
	}

	if task.Session != nil {
		out.WriteString("\n" + headingStyle.Render("terminal") + "\n")
		// Named rather than run together. These are three different kinds of
		// identifier and a reader who needs one — to run a tmux command against
		// the task themselves — cannot tell them apart from their order.
		out.WriteString(field("tmux", mutedStyle.Render("session ")+task.Session.Tmux.Session+
			mutedStyle.Render("  window ")+task.Session.Tmux.Window+
			mutedStyle.Render("  pane ")+task.Session.Tmux.Pane))
		out.WriteString(field("socket", task.Session.Tmux.Socket))
		if note := terminalNote(task); note != "" {
			out.WriteString(mutedStyle.Render("  "+note) + "\n")
		}
	}

	if task.Session != nil && task.Session.Execution != nil {
		out.WriteString("\n" + headingStyle.Render("environment") + "\n")
		out.WriteString(executionDetail(*task.Session.Execution))
	}

	out.WriteString("\n" + headingStyle.Render("brief") + "\n")
	out.WriteString(indent(task.Brief, "  ") + "\n")

	return out.String()
}

// failureBlock is why a failed task failed, under the state it explains.
//
// It sits there rather than in a section of its own because it is not a separate
// fact: `failed` and its reason are one thing said twice, and a user reading the
// state has to travel no further to learn what it means. Before this the reason
// was in the task's event log on disk and in an error banner that had already
// gone, so the panel could say a launch failed and never why.
//
// The reason is printed as it was reported and wrapped by the panel rather than
// truncated. It names a Compose service, a mount, or a path, and a cut sentence
// loses exactly the end that identifies which one.
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
// has reported.
//
// The task snapshot carries the agent's own count and the review status carries
// the results with the reporter of each, which is the richer answer and the one
// that can say Feat ran them. Showing both was what made two thin tabs look like
// two different facts.
func (m Model) checksField(task api.Task) string {
	if m.review.loaded && len(m.review.status.Review.Checks) > 0 {
		return reviewChecksSummary(m.review.status.Review)
	}
	return verificationDetail(task)
}

// taskRepositories renders one block per repository the task binds.
//
// It walks the task's own bindings rather than the comparison's rows, so that a
// draft — which has bindings and no worktrees — is drawn with what it has. Where
// a comparison exists its numbers are used, because those are the ones actually
// measured against the recorded base (FR-REV-001).
//
// Four lines each rather than a row of columns: the base commit, the branch, and
// the worktree path are what a user reads this panel to find, and a truncated one
// has to be looked up somewhere else.
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

// taskPanelHints are the panel's own keys.
//
// The external commands are diff and editor, each about the repository under the
// cursor (FR-REV-002). The status command is not among them: `s` opens the
// task's shell here as it does everywhere else (ADR-045), and the shell is where
// a status is read anyway.
func taskPanelHints() string {
	return keyHints(
		keyHint("j k", "repository"),
		keyHint("d", "diff"),
		keyHint("e", "editor"),
		keyHint("A", "approve"),
		keyHint("C", "request changes"),
		keyHint("V", "run checks"),
		keyHint("pgup/pgdn", "scroll"),
	)
}
