package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// View renders task preparation.
//
// The indicator is passed in rather than kept here, because there is one of them
// for the whole dashboard and it is animated by the dashboard (see activity).
// What this screen owns is what it is waiting for and what it calls that; the
// frame in front of it belongs to the model that ticks.
func (p prepareModel) View(indicator activity) string {
	var out strings.Builder
	out.WriteString(headingStyle.Render("prepare a task") + mutedStyle.Render("  "+p.trail()) + "\n\n")

	switch p.step {
	case stepProject:
		out.WriteString(p.projectView())
	case stepSource:
		out.WriteString(p.sourceView())
	case stepBrief:
		out.WriteString(p.briefView())
	case stepRepositories:
		out.WriteString(p.repositoryView())
	case stepReview:
		out.WriteString(p.reviewView())
	case stepTickets:
		out.WriteString(p.ticketView())
	case stepImport:
		out.WriteString(p.importView())
	}

	out.WriteString("\n")
	if p.err != nil {
		out.WriteString("\n" + failureStyle.Render(p.err.Error()) + "\n")
	} else if p.busy && p.status != "" {
		// Marked while it is waiting, and it is only drawn while it is waiting.
		// Resolving a draft reaches every repository in the project and confirming
		// one creates the worktrees, the branches, and the terminal; both take long
		// enough that a line which never moved was read as a screen that had stopped.
		out.WriteString("\n" + mutedStyle.Render(indicator.mark(p.status)) + "\n")
	} else {
		out.WriteString("\n")
	}

	out.WriteString("\n" + p.hints())
	return out.String()
}

// trailStep is the step of the trail a screen belongs to.
//
// The ticket list and the file screen are each one answer to the source question
// being worked out rather than a stage of their own, and each returns to that
// step, so that is where the trail says the user is.
func trailStep(current step) step {
	if current == stepTickets || current == stepImport {
		return stepSource
	}
	return current
}

// trail shows where the user is, and that nothing exists until the last step.
func (p prepareModel) trail() string {
	steps := []string{"project", "source", "brief", "repositories", "review"}
	current := trailStep(p.step)
	rendered := make([]string, 0, len(steps))
	for i, name := range steps {
		if step(i) == current {
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

// sourceView asks where the brief comes from.
//
// The answers are drawn as a list with a cursor, like every other closed
// question Feat asks, and each says what choosing it does rather than only what
// it is called: the two that leave this screen run somebody's tracker or read a
// file, and a user is entitled to know that before pressing Enter.
func (p prepareModel) sourceView() string {
	var out strings.Builder
	out.WriteString(mutedStyle.Render("where does this task's brief come from?") + "\n\n")

	for i, option := range sourceOptions {
		marker, label := "  ", option.label
		if i == p.cursor {
			marker = selectedStyle.Render("▸ ")
			label = selectedStyle.Render(option.label)
		}
		out.WriteString(marker + pad(label, 23) + mutedStyle.Render(option.note) + "\n")
	}
	return out.String()
}

// importView is the file whose text becomes the brief.
//
// It says that the file is read here, because that is the part a user cannot see
// and the part that makes the rest of preparation apply to it: what is imported
// is text in the same editable field, confirmed like any other brief (ADR-028,
// ADR-070).
func (p prepareModel) importView() string {
	var out strings.Builder
	out.WriteString(mutedStyle.Render("which file holds the brief?") + "\n\n")

	out.WriteString("  " + mutedStyle.Render("path") + "  " + p.pathField() + "\n")
	if len(p.candidates) > 0 {
		// The key is named under the field because nothing in an empty field
		// shows what tab would put there (ADR-077).
		out.WriteString("        " + mutedStyle.Render("press tab to use one of them") + "\n")
	}
	out.WriteString("\n" + mutedStyle.Render(
		"  the file is read here; its text becomes the brief you read and confirm") + "\n")
	return out.String()
}

// pathField draws the field in the width it was given.
//
// The widget pads its line out to that width from the typed value alone and then
// writes the completion after the padding, so a suggestion makes the line as wide
// as the field plus the whole of what it is suggesting — which on this screen is
// an absolute path, and would wrap. One more cell than the width, because the
// cursor sits after the value; the wizard's field is drawn the same way and for
// the same reason.
func (p prepareModel) pathField() string {
	if p.path.Width <= 0 {
		return p.path.View()
	}
	return ansi.Truncate(p.path.View(), p.path.Width+1, "")
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

// ticketView is the project's tickets offered as a selection.
//
// It shows what the tracker printed and nothing Feat inferred: the state is the
// tracker's own word, and the reference is the tracker's own identifier
// (ADR-071). Selecting one composes a brief from it, which is the document the
// user then reads, edits, and confirms.
func (p prepareModel) ticketView() string {
	var out strings.Builder
	out.WriteString(mutedStyle.Render("which ticket is this task for?") + "\n\n")

	rows := make([][]string, 0, len(p.tickets))
	for i, ticket := range p.tickets {
		marker, reference := "  ", ticket.Reference
		if i == p.cursor {
			marker = selectedStyle.Render("▸ ")
			reference = selectedStyle.Render(ticket.Reference)
		}
		rows = append(rows, []string{
			marker + reference,
			ticket.Title,
			mutedStyle.Render(ticket.State),
			mutedStyle.Render(ticket.Source),
		})
	}
	out.WriteString(renderTable([]column{
		{title: "  TICKET", width: 16},
		{title: "TITLE", width: 52},
		{title: "STATE", width: 18},
		{title: "TRACKER"},
	}, rows))

	out.WriteString("\n\n" + mutedStyle.Render(
		"  selecting one writes a brief from it, which you read and edit before anything is created") + "\n")
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
	out.WriteString("  " + fieldStyle.Render("start") + startProfile(p.planFirst) + "\n")
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

// startProfile describes how the session begins, and names the key that changes
// it.
//
// Both states are spelled out rather than one of them being an unlabelled
// default, because this line is a promise about what the agent will do to the
// user's repositories in the next few seconds. A user who reads "straight into
// the work" and meant to plan can see that they did not press the key.
func startProfile(planFirst bool) string {
	note := mutedStyle.Render("(p — " + startAction(planFirst) + ")")
	if planFirst {
		return "plan first, and wait for your approval  " + note
	}
	return "straight into the work  " + note
}

// startAction is what pressing p would do, which is the name of the other
// state. The line above and the footer hint read it from here so that the two
// cannot come to disagree about what the key does.
func startAction(planFirst bool) string {
	if planFirst {
		return "start straight in"
	}
	return "plan first"
}

func (p prepareModel) hints() string {
	// While a request is in flight only the cancel is answered — see key — so it
	// is the only one offered. A key map naming four keys that do nothing is one
	// a user has to try to disbelieve, which is exactly what they do when a screen
	// has been still for three seconds; the cleanup screen's key map holds to the
	// same rule while its own removal runs.
	if p.busy {
		return keyHints(keyHint("ctrl+c", "cancel"))
	}
	switch p.step {
	case stepProject:
		return keyHints(keyHint("↑↓", "select"), keyHint("enter", "continue"), keyHint("ctrl+c", "cancel"))
	case stepSource:
		return keyHints(
			keyHint("↑↓", "select"),
			keyHint("enter", "continue"),
			keyHint("esc", "back"),
			keyHint("ctrl+c", "cancel"),
		)
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
		// The p hint is named for what pressing it would do rather than for the
		// state it leaves, which is how every other hint on this screen reads.
		return keyHints(
			keyHint("enter", "confirm and launch"),
			keyHint("p", startAction(p.planFirst)),
			keyHint("esc", "back"),
			keyHint("x", "discard draft"),
		)
	case stepTickets:
		return keyHints(
			keyHint("↑↓", "select"),
			keyHint("enter", "write a brief from it"),
			keyHint("esc", "back"),
			keyHint("ctrl+c", "cancel"),
		)
	case stepImport:
		return keyHints(
			keyHint("tab", "complete"),
			keyHint("enter", "import it"),
			keyHint("esc", "back"),
			keyHint("ctrl+c", "cancel"),
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
