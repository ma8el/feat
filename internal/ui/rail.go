package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

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

// railHeader is the rail's card header: what the region is, and how much of it
// wants the user.
//
// The heading was the first line of the rail's own content until ADR-051, which
// is what made it read as the first entry of the list under it. It is now the
// card's header, ruled off from the tasks, and the attention summary sits beside
// it rather than under it: a count of what is waiting belongs with the heading of
// the thing it is counting.
func (m Model) railHeader(width int) string {
	aside := ""
	if m.loaded {
		// A count before the first task list has arrived would be a measurement
		// nothing made, which is the rule the rest of the dashboard follows: zero
		// tasks and no answer yet are different things.
		aside = mutedStyle.Render(count(len(m.tasks), "task", "tasks"))
	}
	if summary := attentionSummary(m.tasks); summary != "" {
		aside = attentionStyle.Render(summary)
	}
	return cardHeader(titleStyle.Render("tasks"), aside, width)
}

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

	switch {
	case !m.loaded && m.err == nil:
		out.WriteString(mutedStyle.Render("reading task state…"))

	case len(m.tasks) == 0:
		out.WriteString(mutedStyle.Render("no tasks yet"))
		out.WriteString("\n" + mutedStyle.Render("press n to prepare one"))

	default:
		for i, group := range groupByProject(m.tasks) {
			if i > 0 {
				out.WriteString("\n")
			}
			out.WriteString(m.projectHeader(group) + "\n")
			if m.folded[group.project] {
				continue
			}
			for _, index := range group.indexes {
				task := m.tasks[index]
				out.WriteString(m.railEntry(task, task.ID == m.selected))
			}
		}
	}

	if m.archived > 0 {
		out.WriteString("\n" + mutedStyle.Render(truncate(archivedNote(m.archived), railWidth)))
	}
	return pinFoot(out.String(), m.railFoot(), height)
}

// The markers on a project header, which say what pressing space there would do.
//
// They were drawn before ADR-051 too, on a rail where nothing could be folded:
// the header claimed a control that did not exist, which is a worse defect than
// having no control at all, because a user who tries it learns to distrust the
// rest of the screen.
const (
	glyphOpen   = "▾"
	glyphFolded = "▸"
)

// projectHeader renders one project's line in the rail.
//
// A folded project still reports what is inside it: how many tasks, and whether
// any of them wants the user. Folding is for reading less about projects a user
// is not working in, and a fold that could hide the one task that stopped would
// make the rail unsafe to fold at all (FR-UI-001).
func (m Model) projectHeader(group projectGroup) string {
	glyph, style := glyphOpen, mutedStyle
	if m.folded[group.project] {
		glyph = glyphFolded
	}
	if m.holdsCursor(group) {
		style = selectedStyle
	}

	aside := mutedStyle.Render(strconv.Itoa(len(group.indexes)))
	if m.folded[group.project] {
		// The glyph is drawn only where the tasks themselves are not. An open
		// project whose entries each carry their own does not need a second one on
		// the header; a folded one has nothing else to say it with.
		if waiting := m.groupAttention(group); waiting != "" {
			aside = attentionStyle.Render(waiting) + " " + aside
		}
		// A fold is a cursor stop of its own (ADR-052), so the header is where the
		// selected task is reported for as long as the cursor is parked on one.
		// The task is named rather than left to the header's colour, because a
		// terminal without colour would lose the distinction without showing that
		// it had.
		if task, ok := m.subject(); ok && m.holdsCursor(group) {
			aside = selectedStyle.Render(task.Key) + " " + aside
		}
	}

	name := truncate(group.project, railWidth-2-ansi.StringWidth(aside)-1)
	return cardHeader(style.Render(glyph+" "+name), aside, railWidth)
}

// holdsCursor reports whether the selected task is one of this project's, which
// a folded header is the only remaining sign of in the rail. The main region's
// own header names the task in words whatever the rail is doing.
//
// It asks the selected task which project it is in rather than comparing rows,
// because a group's rows are true of the list it was built from and the
// selection is true of the task.
func (m Model) holdsCursor(group projectGroup) bool {
	task, ok := m.subject()
	return ok && task.ProjectID == group.project
}

// groupAttention is the strongest attention glyph among a project's tasks, for
// the header of a folded one.
func (m Model) groupAttention(group projectGroup) string {
	glyph := ""
	for _, index := range group.indexes {
		switch m.tasks[index].Attention {
		case "needs_input":
			return badgeNeedsInput
		case "possibly_waiting":
			glyph = badgeMaybe
		}
	}
	return glyph
}

// railFoot is what the rail keeps at its bottom, whatever the task list does.
//
// The machine's resources, and then what the last reconciliation pass wants
// looked at as a count and a key. Both are about the machine rather than about
// the selected task, and neither is something a user goes looking for: they are
// what the eye should find in the same place every time it drops to the corner.
// The findings themselves are three lines each and belong in the overlay the key
// opens; what the rail owes a user is that there is something to open (ADR-043).
func (m Model) railFoot() string {
	foot := railRule() + "\n" + m.machineBlock()
	if note := truncate(m.recoveryRailNote(), railWidth); note != "" {
		foot += "\n" + railRule() + "\n" + note
	}
	return foot
}

// railRule separates the rail's three parts.
//
// A line rather than a blank one, because the parts are three different things
// about three different subjects — the tasks, the machine, and what
// reconciliation found — and blank space between them read as one list that had
// stopped. It is drawn in the colour the card around it is, so that the rail is
// ruled by the frame rather than decorated.
func railRule() string {
	return ruleStyle.Render(strings.Repeat(cardHorizontal, railWidth))
}

// pinFoot puts a block on the rail's last lines.
//
// At the foot rather than after the tasks, because the tasks move: adding one
// would otherwise push the block down the screen, and something read by
// position should be in the same position each time. The narrow fallback passes
// no height and gets it below the list, which is the bottom there too.
//
// A task list too long for the rail is cut here, above the foot, and says that
// it was. The region used to be clipped by the layout, which cut from the bottom
// and so took the machine's figures away instead — the one part of the rail that
// is read by position. What a user does about it is fold a project, which is why
// the note says so.
func pinFoot(body, foot string, height int) string {
	if foot == "" {
		return body
	}
	if height <= 0 {
		return body + "\n\n" + foot
	}

	lines := strings.Split(body, "\n")
	room := height - len(strings.Split(foot, "\n"))
	switch {
	case room < 1:
		// No room for the tasks at all, which is a terminal too short for the
		// layout rather than a list too long. The foot is what survives.
		return foot
	case len(lines) > room:
		note := "… " + count(len(lines)-room+1, "more line", "more lines") + ", space folds"
		lines = append(lines[:room-1], mutedStyle.Render(truncate(note, railWidth)))
	default:
		lines = append(lines, make([]string, room-len(lines))...)
	}
	return strings.Join(lines, "\n") + "\n" + foot
}

// railEntry renders one task as two lines.
//
// The entry of a task whose terminal has the keyboard is drawn on a background
// of its own. That is the only thing the terminal's heading said which nothing
// else did — the key was already here, and both of its hints were already in the
// footer — and it belongs beside the task it applies to rather than above the
// pane, where it had to be read and then went away.
func (m Model) railEntry(task api.Task, selected bool) string {
	title := plainLine(task.Title)
	if title == "" {
		title = "(untitled)"
	}
	// The marker, the glyph and its space, the key, and two spaces before the
	// title are all fixed, so the title takes what is left of the rail.
	title = truncate(title, railWidth-2-1-1-8-2)

	// Agent state, elapsed time, and the changed-file count are the rest of what
	// FR-UI-002 asks of an entry, and they are what a user compares between two
	// tasks: how far along, how long, how much.
	//
	// A task with no session reports its workflow instead of an absent agent
	// state. Those read alike and are not alike: one has not been launched and
	// the other has and stopped. The rail is the only list there is, so the
	// distinction has to survive here or nowhere.
	state := agentState(task)
	if task.Session == nil {
		state = task.Workflow
	}
	detail := truncate(state+" · "+elapsed(task, m.now())+" · "+changedFileNote(task),
		railWidth-4)

	if m.holdsKeyboard(task) {
		// Rendered without its own styling, because a background is applied to
		// the whole line and an inner style that ends resets it partway across.
		return focusedEntryStyle.Width(railWidth).Render(
			"▸ "+attentionRune(task)+" "+task.Key+"  "+title) + "\n" +
			focusedEntryStyle.Width(railWidth).Render("    "+detail) + "\n"
	}

	marker := "  "
	if selected {
		marker = selectedStyle.Render("▸ ")
	}
	if task.Title == "" {
		title = mutedStyle.Render(title)
	}
	head := marker + attentionBadge(task) + " " + taskKey(task) + "  " + title

	return head + "\n" + "    " + mutedStyle.Render(detail) + "\n"
}

// holdsKeyboard reports whether this task's terminal is the one taking keys.
func (m Model) holdsKeyboard(task api.Task) bool {
	if !m.terminal.focused || m.screen != screenTerminal {
		return false
	}
	selected, ok := m.subject()
	return ok && selected.ID == task.ID
}

// attentionRune is the attention glyph without styling, for a line that carries
// a background of its own.
func attentionRune(task api.Task) string {
	switch task.Attention {
	case "needs_input":
		return badgeNeedsInput
	case "possibly_waiting":
		return badgeMaybe
	default:
		return badgeIdle
	}
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

// groupByProject collects tasks under their project, in a fixed order.
//
// Projects are ordered by name, which is what the rail draws them as, and tasks
// keep the order the list sorted them into inside each one. The order a project's
// first task appeared in was the order before, and it moved a project the moment
// a task was launched in it: the newest task sorts first, so starting one in the
// project at the bottom of the rail lifted that project to the top and pushed
// every other one down. That is ADR-041's evidence 4 — the row a user's eye
// learned moving on the day it mattered — reappearing a level up, on the day the
// user was busy enough to have started something.
//
// So a refresh cannot move a project, and no task can. Only registering a project
// or finishing the last task in one changes what the rail holds, which is the
// list changing rather than reordering itself under a cursor holding still
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
	sort.Slice(groups, func(i, j int) bool { return groups[i].project < groups[j].project })
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
