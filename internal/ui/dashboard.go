package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// dashboardView renders the global task list (FR-UI-001, FR-UI-002).
func (m Model) dashboardView() string {
	var out strings.Builder
	out.WriteString(headingStyle.Render("feat") + mutedStyle.Render("  tasks across every registered project"))
	out.WriteString("\n\n")

	switch {
	case !m.loaded && m.err == nil:
		out.WriteString(mutedStyle.Render("reading task state…"))

	case len(m.tasks) == 0:
		out.WriteString(mutedStyle.Render("no tasks yet"))
		out.WriteString("\n" + mutedStyle.Render("press n to prepare one, or run `feat implement`"))
		if m.archived > 0 {
			out.WriteString("\n\n" + mutedStyle.Render(archivedNote(m.archived)))
		}

	default:
		rows := make([][]string, 0, len(m.tasks))
		for i, task := range m.tasks {
			row := taskRow(task, m.now())
			marker := "  "
			if i == m.cursor {
				marker = selectedStyle.Render("▸ ")
			}
			row[0] = marker + taskKey(task)
			rows = append(rows, row)
		}

		// The marker occupies two cells in front of the key, so the first
		// column is that much wider than the key it holds.
		columns := append([]column(nil), taskColumns...)
		columns[0].width += 2
		out.WriteString(renderTable(columns, rows))

		if m.archived > 0 {
			out.WriteString("\n\n" + mutedStyle.Render(archivedNote(m.archived)))
		}
	}

	return out.String() + m.footer(keyHints(
		keyHint("↑↓", "select"),
		keyHint("enter", "detail"),
		keyHint("n", "new task"),
		keyHint("a", "attach"),
		keyHint("s", "shell"),
		keyHint("x", "cancel draft"),
		keyHint("r", "refresh"),
		keyHint("q", "quit"),
	))
}

// taskKey renders a task's short identifier, marking one that is still a draft.
//
// A draft and a launched task look alike in a list and are not alike at all:
// one has worktrees, a branch, and a terminal, and the other has none of them.
func taskKey(task api.Task) string {
	if isDraft(task) {
		return attentionStyle.Render(task.Key)
	}
	if task.Workflow == "failed" {
		return failureStyle.Render(task.Key)
	}
	return task.Key
}

func archivedNote(count int) string {
	return strconv.Itoa(count) + " archived " + pluralTasks(count) + " not shown"
}

func pluralTasks(count int) string {
	if count == 1 {
		return "task"
	}
	return "tasks"
}

// detailView renders one task (FR-UI-003).
func (m Model) detailView() string {
	task, ok := m.task(m.selected)
	if !ok {
		return headingStyle.Render("task") + "\n\n" +
			mutedStyle.Render("this task is no longer listed") +
			m.footer(keyHints(keyHint("esc", "back"), keyHint("q", "quit")))
	}

	var out strings.Builder
	out.WriteString(headingStyle.Render(task.Key+"  "+task.Title) + "\n")
	out.WriteString(mutedStyle.Render(task.ProjectID+" · "+task.ID) + "\n\n")

	out.WriteString(field("workflow", task.Workflow))
	out.WriteString(field("attention", attentionState(task)))
	out.WriteString(field("agent", agentDetail(task)))
	out.WriteString(field("runtime", runtimeDetail(task)))
	out.WriteString(field("verification", verificationDetail(task)))
	out.WriteString(field("resources", absent+"  "+mutedStyle.Render("("+resourceSlice+")")))
	out.WriteString(field("elapsed", elapsed(task, m.now())))
	out.WriteString(field("source", sourceDetail(task.Source)))

	out.WriteString("\n" + headingStyle.Render("repositories") + "\n")
	out.WriteString(repositoryTable(task) + "\n")

	if task.Session != nil {
		out.WriteString("\n" + headingStyle.Render("terminal") + "\n")
		out.WriteString(field("tmux", task.Session.Tmux.Session+" "+
			task.Session.Tmux.Window+" "+task.Session.Tmux.Pane))
		out.WriteString(field("socket", task.Session.Tmux.Socket))
		if note := terminalNote(task); note != "" {
			out.WriteString(mutedStyle.Render("  "+note) + "\n")
		}
	}

	out.WriteString("\n" + headingStyle.Render("brief") + "\n")
	out.WriteString(indent(task.Brief, "  ") + "\n")

	return out.String() + m.footer(keyHints(
		keyHint("esc", "back"),
		keyHint("a", "attach"),
		keyHint("s", "shell"),
		keyHint("x", "cancel draft"),
		keyHint("q", "quit"),
	))
}

// repositoryTable renders the repository and base mapping FR-UI-003 requires.
//
// Each repository takes two lines rather than one column-aligned row. Branch
// names and worktree paths are both long, and this is the screen a user reads
// to find out exactly which branch and which directory a task owns — a
// truncated one would have to be looked up somewhere else, which is the
// coordination Feat exists to remove.
func repositoryTable(task api.Task) string {
	if len(task.Repositories) == 0 {
		return mutedStyle.Render("  none selected")
	}

	var out strings.Builder
	for i, binding := range task.Repositories {
		if i > 0 {
			out.WriteString("\n")
		}

		base := absent
		if binding.BaseCommit != "" {
			base = binding.BaseCommit[:min(12, len(binding.BaseCommit))]
			if binding.BaseRef != "" {
				base += " " + mutedStyle.Render("("+binding.BaseRef+")")
			}
		}
		changed := absent
		if binding.Observation != nil {
			changed = strconv.Itoa(binding.Observation.ChangedFiles) + " changed"
			if binding.Observation.Dirty {
				changed += ", uncommitted"
			}
		}

		out.WriteString("  " + headingStyle.Render(binding.RepositoryID) +
			mutedStyle.Render("  "+accessLabel(binding.Access)) +
			"  " + base + mutedStyle.Render("  "+changed) + "\n")

		branch := binding.Branch
		if branch == "" {
			branch = mutedStyle.Render("no branch (read-only)")
		}
		worktree := binding.WorktreePath
		if worktree == "" {
			worktree = mutedStyle.Render("not created yet")
		}
		out.WriteString("    " + mutedStyle.Render("branch  ") + branch + "\n")
		out.WriteString("    " + mutedStyle.Render("worktree") + " " + worktree + "\n")
	}
	return out.String()
}

func agentDetail(task api.Task) string {
	if task.Session == nil {
		return absent + "  " + mutedStyle.Render("(no terminal yet)")
	}
	return fmt.Sprintf("%s, %s in %s", task.Session.Process, task.Session.Provider, task.Session.ExecutionMode)
}

// terminalNote explains a task terminal that is not what the project asked for.
//
// A task still preparing after its terminal exists is one whose pane holds a
// shell rather than an agent, which happens when the project configures a
// devcontainer this build cannot start. Saying so in words is the rule ADR-031
// set: a value that was never measured is never displayed as one, and a
// boundary that is not there is never implied by silence.
func terminalNote(task api.Task) string {
	if task.Session == nil {
		return ""
	}
	if task.Workflow == "preparing" {
		return containerSlice
	}
	if task.Session.ExecutionMode == "host" {
		for _, binding := range task.Repositories {
			if binding.ContainerPath != "" {
				return "this project configures a devcontainer, and this session is running " +
					"directly on this host instead"
			}
		}
	}
	return ""
}

func runtimeDetail(task api.Task) string {
	if task.Runtime == nil {
		return "absent  " + mutedStyle.Render("(v0 starts application services only when you ask)")
	}
	detail := task.Runtime.State + ", health " + task.Runtime.Health
	if len(task.Runtime.Services) > 0 {
		detail += "  " + strings.Join(task.Runtime.Services, ", ")
	}
	return detail
}

func sourceDetail(source api.Source) string {
	if source.Reference != "" {
		return source.Kind + " · " + source.Reference
	}
	return source.Kind
}

// field renders one label and value of the detail screen.
func field(label, value string) string {
	return "  " + fieldStyle.Render(label) + value + "\n"
}

// indent prefixes every line of a block.
func indent(block, prefix string) string {
	if block == "" {
		return mutedStyle.Render(prefix + "(empty)")
	}
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
