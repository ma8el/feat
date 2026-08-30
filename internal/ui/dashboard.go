package ui

import (
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
	// The fallback has no cards to put a header in, so the rail's header and the
	// rule under it are drawn here: below the layout's minimum this list is the
	// whole screen, and a heading run together with its first entry reads the same
	// way in one column as it did in two (ADR-051).
	out.WriteString(m.railHeader(width) + "\n")
	out.WriteString(ruleStyle.Render(strings.Repeat(cardHorizontal, width)) + "\n")
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
		keyHint("t", "stop"),
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

// agentDetail is what runs this task, where, and in what.
//
// It absorbed the environment section, which was five fields and two lines of
// explanation that were identical on every task — documentation living in a
// status panel (ADR-086). Three things survive that. What runs and where, which
// names the provider although v0 has one, because it is a line that would
// otherwise be edited twice. The compose project, which is the one identifier
// here with a use the tmux ids lacked: a name a user types into a tool they
// already have on the trusted host. And the container's state, appended only
// when it is not simply running — reconciliation observes an agent container
// that is not running, and the process word cannot express that. A container
// that is running says nothing extra, which is the panel's rule throughout: a
// check with nothing to report reports nothing.
//
// The compose project goes on a continuation line rather than into the value.
// It is about fifty cells against a value column of thirty-nine at the minimum
// width and sixty-three at 120, so inside the value it would break in a
// different place at every terminal width; on a line of its own the field is the
// same shape everywhere.
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
//
// A devcontainer is something the agent runs inside; the host is not, and "in
// host" read as the name of a container nobody had configured.
func agentLocation(mode string) string {
	if mode == "host" {
		return "on the host"
	}
	return "in " + mode
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

// runtimeDetail is what this task's application services are doing.
//
// A task with none reads as the bare word rather than as the em dash: the dash
// means "nothing measured" everywhere else on this screen, and no services
// running is a different fact and a measured one. The sentence explaining that
// v0 starts services only when asked was an apology on every task that had never
// been asked, which is nearly all of them (ADR-086).
func runtimeDetail(task api.Task) string {
	if task.Runtime == nil {
		return "absent"
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

// sourceDetail says where a task's brief came from.
//
// It lives beside the brief rather than on the task panel, being a fact about
// that document rather than about the task's state (ADR-086).
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

// fieldValueColumn is the cell a field's value starts in: the panel's own margin
// and the label column beside it.
const fieldValueColumn = 2 + fieldWidth

// continued puts a second line under a field's value, in the value's column.
//
// A value that will not sit beside its label is broken here rather than left to
// the wrap. The wrap breaks wherever the width runs out, so the same field would
// have a different shape in every terminal; a break made deliberately is in the
// same place in all of them.
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
