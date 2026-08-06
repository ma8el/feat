package ui

import (
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// View renders task preparation.
func (p prepareModel) View() string {
	var out strings.Builder
	out.WriteString(headingStyle.Render("prepare a task") + mutedStyle.Render("  "+p.trail()) + "\n\n")

	switch p.step {
	case stepProject:
		out.WriteString(p.projectView())
	case stepBrief:
		out.WriteString(p.briefView())
	case stepRepositories:
		out.WriteString(p.repositoryView())
	case stepReview:
		out.WriteString(p.reviewView())
	}

	out.WriteString("\n")
	if p.err != nil {
		out.WriteString("\n" + failureStyle.Render(p.err.Error()) + "\n")
	} else if p.busy && p.status != "" {
		out.WriteString("\n" + mutedStyle.Render(p.status) + "\n")
	} else {
		out.WriteString("\n")
	}

	out.WriteString("\n" + p.hints())
	return out.String()
}

// trail shows where the user is, and that nothing exists until the last step.
func (p prepareModel) trail() string {
	steps := []string{"project", "brief", "repositories", "review"}
	rendered := make([]string, 0, len(steps))
	for i, name := range steps {
		if step(i) == p.step {
			rendered = append(rendered, selectedStyle.Render(name))
			continue
		}
		rendered = append(rendered, mutedStyle.Render(name))
	}
	return strings.Join(rendered, mutedStyle.Render(" › "))
}

func (p prepareModel) projectView() string {
	var out strings.Builder
	out.WriteString(mutedStyle.Render("which project is this task in?") + "\n\n")

	for i, project := range p.projects {
		marker := "  "
		name := project.ID + "  " + mutedStyle.Render(project.Name)
		if i == p.cursor {
			marker = selectedStyle.Render("▸ ")
			name = selectedStyle.Render(project.ID) + "  " + mutedStyle.Render(project.Name)
		}
		out.WriteString(marker + name + "\n")
	}
	return out.String()
}

func (p prepareModel) briefView() string {
	var out strings.Builder

	titleLabel, briefLabel := mutedStyle.Render("title"), mutedStyle.Render("brief")
	if p.focus == 0 {
		titleLabel = selectedStyle.Render("title")
	} else {
		briefLabel = selectedStyle.Render("brief")
	}

	out.WriteString("  " + titleLabel + "\n")
	out.WriteString("  " + p.title.View() + "\n\n")
	out.WriteString("  " + briefLabel)
	if name := briefName(p.source.Reference); name != "" {
		out.WriteString(mutedStyle.Render("  imported from " + name))
	}
	out.WriteString("\n")
	out.WriteString(indentPlain(p.brief.View(), "  ") + "\n\n")
	out.WriteString(mutedStyle.Render("  the agent receives this brief exactly as written") + "\n")
	return out.String()
}

func (p prepareModel) repositoryView() string {
	var out strings.Builder
	out.WriteString(mutedStyle.Render("which repositories does the task touch?") + "\n\n")

	rows := make([][]string, 0, len(p.selection))
	for i, row := range p.selection {
		marker := "  "
		name := row.repository
		if i == p.cursor {
			marker = selectedStyle.Render("▸ ")
			name = selectedStyle.Render(row.repository)
		}

		note := ""
		if len(row.permitted) < len(accessModes) {
			note = "configured " + row.declared + ", so it cannot be promoted"
		}
		rows = append(rows, []string{marker + name, accessLabel(row.access), mutedStyle.Render(note)})
	}
	out.WriteString(renderTable([]column{
		{title: "  REPOSITORY", width: 20},
		{title: "ACCESS", width: 14},
		{title: "NOTE"},
	}, rows))

	out.WriteString("\n\n" + mutedStyle.Render(
		"  read-write repositories get a task branch and a writable worktree; read-only ones get a worktree only") + "\n")
	return out.String()
}

func accessLabel(access string) string {
	switch access {
	case "read_write":
		return "read-write"
	case "read_only":
		return "read-only"
	default:
		return mutedStyle.Render("omitted")
	}
}

// reviewView is the last screen before anything is created.
//
// It shows what the confirmation will produce: the resolved immutable bases,
// the branches and paths, and the profiles the task will run under. FR-TASK-003
// requires every one of them to be visible before, not after.
func (p prepareModel) reviewView() string {
	if p.plan == nil {
		return mutedStyle.Render("nothing has been resolved yet")
	}
	task := p.plan.Task

	var out strings.Builder
	out.WriteString("  " + fieldStyle.Render("title") + task.Title + "\n")
	out.WriteString("  " + fieldStyle.Render("project") + task.ProjectID + "\n")
	out.WriteString("  " + fieldStyle.Render("task") + task.Key + "\n\n")

	rows := make([][]string, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		branch := binding.Branch
		if branch == "" {
			branch = mutedStyle.Render("none (read-only)")
		}
		base := binding.BaseCommit
		if len(base) > 12 {
			base = base[:12]
		}
		rows = append(rows, []string{
			"  " + binding.RepositoryID,
			accessLabel(binding.Access),
			base + "  " + mutedStyle.Render(binding.BaseRef),
			branch,
			binding.WorktreePath,
		})
	}
	out.WriteString(renderTable([]column{
		{title: "  REPOSITORY", width: 16},
		{title: "ACCESS", width: 12},
		{title: "BASE", width: 26},
		{title: "BRANCH", width: 34},
		{title: "WORKTREE"},
	}, rows))
	out.WriteString("\n\n")

	out.WriteString("  " + fieldStyle.Render("agent") + agentProfile(task) + "\n")
	out.WriteString("  " + fieldStyle.Render("runtime") +
		"absent  " + mutedStyle.Render("(start application services yourself, once the task is running)") + "\n")

	for _, note := range p.plan.Notes {
		out.WriteString("\n  " + attentionStyle.Render("note") + " " + note + "\n")
	}

	out.WriteString("\n" + mutedStyle.Render(
		"  nothing above exists yet. confirming creates the branches, the worktrees, and the task terminal.") + "\n")
	return out.String()
}

// agentProfile describes what the task's terminal will run.
//
// It says where the agent will run before anything is created, because that is
// the difference the security model is about and the user is the one confirming
// it (ADR-031, ADR-033).
func agentProfile(task api.Task) string {
	devcontainer := false
	for _, binding := range task.Repositories {
		if binding.ContainerPath != "" {
			devcontainer = true
			break
		}
	}
	if devcontainer {
		return "a Claude session in this project's devcontainer  " +
			mutedStyle.Render("(your task worktrees are mounted at their container paths, "+
				"and the agent gets no Docker access)")
	}
	return "a Claude session in the primary worktree  " +
		mutedStyle.Render("(host execution, with no container boundary around the agent)")
}

func (p prepareModel) hints() string {
	switch p.step {
	case stepProject:
		return keyHints(keyHint("↑↓", "select"), keyHint("enter", "continue"), keyHint("ctrl+c", "cancel"))
	case stepBrief:
		return keyHints(
			keyHint("tab", "title/brief"),
			keyHint("ctrl+e", "edit in $EDITOR"),
			keyHint("ctrl+s", "continue"),
			keyHint("esc", "back"),
			keyHint("ctrl+c", "cancel"),
		)
	case stepRepositories:
		return keyHints(
			keyHint("↑↓", "select"),
			keyHint("space", "access"),
			keyHint("enter", "resolve"),
			keyHint("esc", "back"),
			keyHint("ctrl+c", "cancel"),
		)
	case stepReview:
		return keyHints(
			keyHint("enter", "confirm and launch"),
			keyHint("esc", "back"),
			keyHint("x", "discard draft"),
		)
	}
	return ""
}

// indentPlain prefixes every line of a block without styling an empty one.
func indentPlain(block, prefix string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
