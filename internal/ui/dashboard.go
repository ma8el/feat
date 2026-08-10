package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// listView renders the task list, which is the narrow fallback's only way to
// see and choose a task (FR-UI-001).
//
// It draws the rail's own entries rather than the wide table the overview page
// used. That table was eleven columns and 158 cells, which is the defect ADR-041
// was built to fix and which the fallback still had: it fitted no terminal small
// enough to reach this view.
func (m Model) listView() string {
	width, _ := m.frameSize()

	var out strings.Builder
	out.WriteString(m.railView(0))
	if m.loaded && len(m.tasks) == 0 {
		// The rail says which key prepares a task; this has room to say that
		// there is a command for it too, which is how a first run starts.
		out.WriteString("\n" + mutedStyle.Render("or run `feat implement`"))
	}
	// The machine's own figures are at the foot of the rail this view draws.
	// What is left is the note explaining a figure that is absent, which is a
	// sentence and gets the fallback's full width rather than the rail's.
	if note := m.machineNote(); note != "" {
		out.WriteString("\n" + clampBlock(note, width))
	}

	return out.String() + m.footer(keyHints(
		keyHint("↑↓", "select"),
		keyHint("enter", "task"),
		keyHint("n", "new task"),
		keyHint("a", "attach"),
		keyHint("s", "shell"),
		keyHint("R", "runtime"),
		keyHint("C", "cleanup"),
		keyHint("z", "resume"),
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
