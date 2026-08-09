package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// dashboardView renders the global task list (FR-UI-001, FR-UI-002).
func (m Model) dashboardView() string {
	var out strings.Builder
	out.WriteString(headingStyle.Render("feat") + mutedStyle.Render("  tasks across every registered project"))
	if summary := attentionSummary(m.tasks); summary != "" {
		out.WriteString("   " + attentionStyle.Render(summary))
	}
	out.WriteString("\n")
	out.WriteString(m.machineCard())
	out.WriteString("\n")
	out.WriteString(m.recoveryBand())
	out.WriteString("\n")

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
			row := m.taskRow(task, m.now())
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
		keyHint("R", "runtime"),
		keyHint("v", "review"),
		keyHint("C", "cleanup"),
		keyHint("z", "resume"),
		keyHint("x", "cancel draft"),
		keyHint("r", "refresh"),
		keyHint("q", "quit"),
	))
}

// recoveryBand renders what the daemon's last reconciliation pass found.
//
// It appears only when something was not simply present, because a pass in which
// everything matched its record is not news — and a band that was always there
// would be one nobody read on the day it mattered.
//
// Nothing here is an action Feat took. Each line is a resource and what a user
// can do about it, which is the whole of what reconciliation offers
// (FR-STATE-003, FR-STATE-004).
func (m Model) recoveryBand() string {
	if !m.reconciliation.Ran || !m.reconciliation.NeedsAttention {
		return ""
	}

	var out strings.Builder
	// When the pass ran, because everything it names can be acted on from this
	// dashboard: a band with no time on it reads as current however old it is.
	when := ""
	if !m.reconciliation.FinishedAt.IsZero() {
		when = mutedStyle.Render("  checked " + m.reconciliation.FinishedAt.Local().Format("15:04:05") +
			"  ·  r to look again")
	}
	out.WriteString("\n" + attentionStyle.Render("recovery") + when + "\n")
	if !m.reconciliation.PreviousRunEndedCleanly {
		out.WriteString(mutedStyle.Render("  the previous daemon did not shut down cleanly") + "\n")
	}

	shown := 0
	for _, finding := range m.reconciliation.Findings {
		if finding.Status == "present" {
			continue
		}
		if shown == recoveryLines {
			out.WriteString(mutedStyle.Render("  … and more; see `feat daemon status`") + "\n")
			break
		}
		shown++

		line := "  " + finding.Status + "  " + finding.Class
		if finding.TaskKey != "" {
			line += "  task " + finding.TaskKey
		}
		out.WriteString(failureStyle.Render(line) + "\n")
		out.WriteString(mutedStyle.Render("      "+finding.Detail) + "\n")
		if finding.Action != "" {
			out.WriteString(mutedStyle.Render("      → "+finding.Action) + "\n")
		}
	}
	for _, problem := range m.reconciliation.Problems {
		out.WriteString(failureStyle.Render("  unchecked  "+problem.Reason) + "\n")
	}
	return out.String()
}

// recoveryLines bounds the band, so that a machine with many stale resources
// still leaves room for the task list the dashboard is mainly about.
const recoveryLines = 4

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

// executionDetail renders the isolated environment the agent runs in.
//
// Three things are said rather than implied. The identity, because it is what a
// user needs to inspect or clean up the container themselves. The user, because
// a non-root agent is the boundary the security model describes and a claim
// nobody can see is a claim nobody can check. And what the generated override
// changed about the project's own service, because Feat editing somebody else's
// Compose file quietly would be worse than not editing it at all (ADR-033).
func executionDetail(environment api.Execution) string {
	var out strings.Builder

	out.WriteString(field("compose project", environment.Identity))
	out.WriteString(field("service", environment.Service+"  "+
		mutedStyle.Render("(running as "+environment.User+", with no Docker access)")))

	state := "not observed"
	switch {
	case environment.Running && environment.Status != "":
		state = environment.Status
	case environment.Running:
		state = "running"
	case environment.Container != "":
		state = "not running"
	}
	if environment.Container != "" {
		state += "  " + mutedStyle.Render("("+environment.Container+")")
	}
	out.WriteString(field("container", state))

	if environment.Health != "" && environment.Health != "unknown" {
		out.WriteString(field("health", environment.Health))
	}
	out.WriteString(mutedStyle.Render(
		"  Feat's generated override mounts this task's worktrees at their container paths and resets\n" +
			"  container_name and published ports for this service, so tasks can run side by side\n"))
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
		return "the terminal is running; the agent has not reported starting yet"
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
	// An approved task whose services are still running is offered the stop and
	// never given it, here as well as on the runtime screen: this is the line a
	// user reads after approving, and a runtime Feat had stopped on their behalf
	// would be one they did not decide to end (docs/02-user-workflows.md §7).
	if offer := approvalOffer(task); offer != "" {
		detail += "\n  " + attentionStyle.Render(offer)
	}
	return detail
}

func sourceDetail(source api.Source) string {
	if source.Reference != "" {
		return source.Kind + " · " + source.Reference
	}
	return source.Kind
}

// field renders one label and value of the task panel.
//
// A label as wide as the column keeps a single space instead of the padding. A
// fixed width wraps rather than overflows, which put "compose project" on two
// lines and left "project" against the panel's left edge looking like a heading.
func field(label, value string) string {
	if ansi.StringWidth(label) >= fieldWidth {
		return "  " + fieldStyle.UnsetWidth().Render(label) + " " + value + "\n"
	}
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
