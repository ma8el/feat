package ui

import (
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// railWidth is how wide the task rail is, in cells.
//
// It holds an eight-character key, a marker, an attention glyph, and enough of a
// title to tell two tasks apart, which is what the rail is for. Everything
// wider than that is in the main region beside it (ADR-041).
const railWidth = 32

// badgeIdle marks a task that needs nothing from the user.
//
// It is drawn rather than left blank so that the attention column has the same
// shape on every line: a glyph that appears only when something is wrong makes
// the rows above and below it move.
const badgeIdle = "○"

// railView renders the task selector, grouped by the project that owns each task.
//
// It carries the five fields FR-UI-002 requires of a list entry — identity,
// attention, agent state, elapsed time, and changed-file count — over two lines
// per task. One line cannot hold them at this width, and the fields that used to
// share the line are required of the selected task by FR-UI-003 and FR-UI-005
// instead.
//
// Attention is a glyph and agent state is a word, deliberately. Feat keeps
// process, attention, workflow, and runtime states apart in the domain, and a
// single composite badge would put them back together in the one place a user
// actually reads. Colour is not asked to carry the difference either, because a
// terminal without it would lose the distinction without showing that it had.
func (m Model) railView(height int) string {
	var out strings.Builder
	out.WriteString(headingStyle.Render("tasks"))
	if summary := attentionSummary(m.tasks); summary != "" {
		out.WriteString("\n" + attentionStyle.Render(summary))
	}
	out.WriteString("\n")

	switch {
	case !m.loaded && m.err == nil:
		out.WriteString("\n" + mutedStyle.Render("reading task state…"))

	case len(m.tasks) == 0:
		out.WriteString("\n" + mutedStyle.Render("no tasks yet"))
		out.WriteString("\n" + mutedStyle.Render("press n to prepare one"))

	default:
		for _, group := range groupByProject(m.tasks) {
			out.WriteString("\n" + mutedStyle.Render("▾ "+truncate(group.project, railWidth-2)) + "\n")
			for _, index := range group.indexes {
				out.WriteString(m.railEntry(m.tasks[index], index == m.cursor))
			}
		}
	}

	if m.archived > 0 {
		out.WriteString("\n" + mutedStyle.Render(truncate(archivedNote(m.archived), railWidth)))
	}
	return out.String()
}

// railEntry renders one task as two lines.
func (m Model) railEntry(task api.Task, selected bool) string {
	marker := "  "
	if selected {
		marker = selectedStyle.Render("▸ ")
	}

	title := task.Title
	if title == "" {
		title = mutedStyle.Render("(untitled)")
	}
	// The marker, the glyph and its space, the key, and two spaces before the
	// title are all fixed, so the title takes what is left of the rail.
	head := marker + attentionBadge(task) + " " + taskKey(task) + "  " +
		truncate(title, railWidth-2-1-1-8-2)

	// Agent state, elapsed time, and the changed-file count are the rest of what
	// FR-UI-002 asks of an entry, and they are what a user compares between two
	// tasks: how far along, how long, how much.
	detail := agentState(task) + " · " + elapsed(task, m.now()) + " · " + changedFileNote(task)

	return head + "\n" + "    " + mutedStyle.Render(truncate(detail, railWidth-4)) + "\n"
}

// attentionBadge is the glyph for a task's attention state.
func attentionBadge(task api.Task) string {
	switch task.Attention {
	case "needs_input":
		return attentionStyle.Render(badgeNeedsInput)
	case "possibly_waiting":
		return attentionStyle.Render(badgeMaybe)
	default:
		return mutedStyle.Render(badgeIdle)
	}
}

// changedFileNote renders the changed-file count for a rail entry.
//
// A task none of whose repositories has been observed says so rather than
// reporting zero files, which is the rule the task list already followed: those
// are different answers and only one of them was measured.
func changedFileNote(task api.Task) string {
	count := changedFiles(task)
	if count == absent {
		return "— files"
	}
	if count == "1" {
		return "1 file"
	}
	return count + " files"
}

// projectGroup is one project's tasks, holding indexes into the task list so
// that the cursor keeps meaning the same task whichever way the rail groups it.
type projectGroup struct {
	project string
	indexes []int
}

// groupByProject collects tasks under their project.
//
// Projects appear in the order their first task does, so grouping never
// reorders the list under a cursor that is holding still: the task list is
// already sorted so that a refresh cannot move a row, and this preserves that
// (FR-UI-001).
func groupByProject(tasks []api.Task) []projectGroup {
	groups := make([]projectGroup, 0, 4)
	at := make(map[string]int, 4)

	for i, task := range tasks {
		index, ok := at[task.ProjectID]
		if !ok {
			at[task.ProjectID] = len(groups)
			groups = append(groups, projectGroup{project: task.ProjectID})
			index = len(groups) - 1
		}
		groups[index].indexes = append(groups[index].indexes, i)
	}
	return groups
}

// railFooter is the selected task's worktree path, for the frame's footer.
//
// The first writable repository's worktree is the directory a user changes into,
// which is the coordination Feat exists to remove: it is the value that would
// otherwise be looked up and pasted. A read-only binding has no worktree of its
// own to offer.
func worktreeNote(task api.Task) string {
	for _, binding := range task.Repositories {
		if binding.WorktreePath != "" && binding.Access != "read_only" {
			if len(task.Repositories) > 1 {
				return binding.WorktreePath + " " +
					mutedStyle.Render("(+"+strconv.Itoa(len(task.Repositories)-1)+" more)")
			}
			return binding.WorktreePath
		}
	}
	return mutedStyle.Render("no worktree yet")
}
