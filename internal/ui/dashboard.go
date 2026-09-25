package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// listView renders the task list, which is the narrow fallback's only way to
// see and choose a task (FR-UI-001). It draws the rail's own entries rather
// than a wide table: eleven columns and 158 cells fit no terminal small enough
// to reach this view, which is the defect ADR-041 was built to fix.
func (m Model) listView() string {
	width, _ := m.frameSize()

	var out strings.Builder
	// The fallback has no cards to put a header in, so the rail's header and the
	// rule under it are drawn here. A heading run together with its first entry
	// reads the same way in one column as it did in two (ADR-051).
	out.WriteString(m.railHeader(width) + "\n")
	out.WriteString(ruleStyle.Render(strings.Repeat(cardHorizontal, width)) + "\n")
	out.WriteString(m.railView(0))
	if m.loaded && len(m.tasks) == 0 {
		// The rail says which key prepares a task; this has room to say that
		// there is a command for it too, which is how a first run starts.
		out.WriteString("\n" + mutedStyle.Render("or run `feat implement`"))
	}
	// The machine's own figures are at the foot of the rail this view draws. What
	// is left is the note explaining an absent figure, which is a sentence and
	// gets the fallback's full width rather than the rail's.
	if note := m.machineNote(); note != "" {
		out.WriteString("\n" + clampBlock(note, width))
	}

	return out.String() + m.footer(keyHints(
		keyHint("↑↓", "select"),
		keyHint("T", "task"),
		keyHint("n", "new task"),
		keyHint("a", "attach"),
		keyHint("s", "shell"),
		keyHint("R", "runtime"),
		keyHint("C", "cleanup"),
		keyHint("z", "resume"),
		keyHint("t", "stop"),
		keyHint("r", "refresh"),
		keyHint("q", "quit"),
	))
}

// taskKey renders a task's short identifier, marking one that is still a draft.
// A draft and a launched task look alike in a list: one has worktrees, a
// branch, and a terminal, and the other has none of them.
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

// agentDetail is what runs this task, where, and in what. It absorbed the
// environment section, whose five fields and two lines of explanation were
// identical on every task (ADR-086). Three things survive: what runs and where,
// the compose project — a name a user types into a tool they already have on
// the trusted host — and the container's state, appended only when it is not
// simply running, because reconciliation can observe an agent container that is
// not and the process word cannot express it.
//
// The compose project goes on a continuation line rather than into the value.
// It is about fifty cells against a value column of thirty-nine at the minimum
// width and sixty-three at 120, so inside the value it would break in a
// different place at every terminal width.
func agentDetail(task api.Task) string {
	if task.Session == nil {
		return absent + "  " + mutedStyle.Render("(no terminal yet)")
	}

	detail := task.Session.Process + " · " + task.Session.Provider + " " +
		agentLocation(task.Session.ExecutionMode)
	if environment := task.Session.Execution; environment != nil {
		if environment.Container != "" && !environment.Running {
			detail += " · " + attentionStyle.Render("container not running")
		}
		if environment.Identity != "" {
			detail = continued(detail, environment.Identity)
		}
	}
	if note := terminalNote(task); note != "" {
		detail = continued(detail, mutedStyle.Render(note))
	}
	return detail
}

// agentLocation names where a session runs, in the preposition its mode takes.
// A devcontainer is something the agent runs inside; the host is not, and "in
// host" read as the name of a container nobody had configured.
func agentLocation(mode string) string {
	if mode == "host" {
		return "on the host"
	}
	return "in " + mode
}

// terminalNote explains a task terminal that is not what the project asked for.
// A task still preparing after its terminal exists holds a shell rather than an
// agent, which happens when the project configures a devcontainer this build
// cannot start. ADR-031 is why that is said in words: a boundary that is not
// there is never implied by silence. The wording for that state is
// startingNote's, because the terminal tab says the same thing about it.
func terminalNote(task api.Task) string {
	if task.Session == nil {
		return ""
	}
	if note := startingNote(task); note != "" {
		return note
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

// runtimeDetail is what this task's application services are doing. A task with
// none reads as the bare word rather than the em dash, which means "nothing
// measured" everywhere else on this screen, where no services running is
// measured. The sentence explaining that v0 starts services only when asked
// read as an apology on nearly every task (ADR-086).
func runtimeDetail(task api.Task) string {
	if task.Runtime == nil {
		return "absent"
	}
	detail := task.Runtime.State + ", health " + task.Runtime.Health
	if len(task.Runtime.Services) > 0 {
		detail += "  " + strings.Join(task.Runtime.Services, ", ")
	}
	return detail
}

// sourceDetail says where a task's brief came from. It lives beside the brief
// rather than on the task panel, being a fact about that document rather than
// about the task's state (ADR-086).
func sourceDetail(source api.Source) string {
	if source.Reference != "" {
		return source.Kind + " · " + source.Reference
	}
	return source.Kind
}

// field renders one label and value of the task panel. A label as wide as the
// column keeps a single space instead of the padding, because a fixed width
// wraps rather than overflows and left "project" against the panel's edge as a
// heading.
func field(label, value string) string {
	if ansi.StringWidth(label) >= fieldWidth {
		return "  " + fieldStyle.UnsetWidth().Render(label) + " " + value + "\n"
	}
	return "  " + fieldStyle.Render(label) + value + "\n"
}

// fieldValueColumn is the cell a field's value starts in: the panel's own
// margin and the label column beside it.
const fieldValueColumn = 2 + fieldWidth

// continued puts a second line under a field's value, in the value's column. A
// value that will not sit beside its label is broken here rather than left to
// the wrap, which breaks wherever the width runs out and gives the same field a
// different shape in every terminal.
func continued(value, line string) string {
	return value + "\n" + strings.Repeat(" ", fieldValueColumn) + line
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
