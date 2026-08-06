package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// Slices that deliver the task fields v0 requires but this build cannot fill.
//
// They are named in the task detail rather than left blank, so that a user who
// asks why a column is empty is told, and a reviewer can see which requirement
// is still outstanding (FR-UI-002, FR-UI-003).
const (
	// gateSlice explains what a verification column can and cannot mean today.
	// A reported result is the agent's claim; a gate that runs the project's
	// configured checks needs the environment slice 8 starts (ADR-032).
	gateSlice     = "checks are the agent's own report; a gate that runs them arrives with slice 8"
	resourceSlice = "resource usage arrives with slice 10"
	// containerSlice explains a task terminal that is not an agent session.
	containerSlice = "this project runs its agent in a devcontainer, which arrives with slice 8; " +
		"the terminal holds a shell"
)

// taskColumns is the task list, in the order FR-UI-002 lists the fields.
//
// PR state is deliberately absent: v0 does not require it in a task row.
var taskColumns = []column{
	{title: "TASK", width: 8},
	{title: "TITLE", width: 28},
	{title: "REPOSITORIES", width: 18},
	{title: "WORKFLOW", width: 17},
	{title: "AGENT", width: 8},
	{title: "ATTENTION", width: 16},
	{title: "RUNTIME", width: 8},
	{title: "CHECKS", width: 7},
	{title: "FILES", width: 5},
	{title: "RESOURCES", width: 9},
	{title: "ELAPSED", width: 7},
}

// taskRow renders one task as the columns above.
func taskRow(task api.Task, now time.Time) []string {
	return []string{
		task.Key,
		task.Title,
		repositoryList(task),
		task.Workflow,
		agentState(task),
		attentionState(task),
		runtimeState(task),
		verificationState(task),
		changedFiles(task),
		// Resource usage is the one required field no slice has delivered yet.
		absent,
		elapsed(task, now),
	}
}

// verificationState renders a task's check results for the task list.
//
// A task whose agent has reported nothing shows nothing: an unmeasured value is
// never rendered as a measured one, which is the rule ADR-028 established for
// diagnostics and ADR-031 carried into the dashboard. What is rendered is a
// count with a marker saying who produced it, because the column is narrow and
// the distinction between a claimed result and an enforced one has to survive
// the abbreviation.
func verificationState(task api.Task) string {
	if task.Verification == nil || task.Verification.Total() == 0 {
		return absent
	}
	reported := *task.Verification

	state := strconv.Itoa(reported.Passed) + "/" + strconv.Itoa(reported.Total())
	if reported.Failed > 0 {
		state = "✗ " + state
	}
	if reported.Source == "agent" {
		// The tilde is the whole point of the column being honest: these results
		// were asserted by the agent, not enforced by anything.
		state = "~" + state
	}
	return state
}

// verificationDetail renders the same results with room to explain them.
func verificationDetail(task api.Task) string {
	if task.Verification == nil {
		return absent + "  " + mutedStyle.Render("(the agent has reported no checks; "+
			"a gate that runs the project's configured checks arrives with slice 8)")
	}
	reported := *task.Verification

	var parts []string
	if reported.Total() > 0 {
		parts = append(parts, strconv.Itoa(reported.Passed)+" passed")
		if reported.Failed > 0 {
			parts = append(parts, strconv.Itoa(reported.Failed)+" failed")
		}
		if reported.Other > 0 {
			parts = append(parts, strconv.Itoa(reported.Other)+" other")
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no checks")
	}

	detail := strings.Join(parts, ", ")
	if reported.Source == "agent" {
		detail += "  " + mutedStyle.Render("(reported by the agent, not verified; "+gateSlice+")")
	}
	if reported.Summary != "" {
		detail += "\n  " + mutedStyle.Render(reported.Summary)
	}
	return detail
}

// repositoryList names the repositories a task binds, marking the ones it may
// not write to.
func repositoryList(task api.Task) string {
	if len(task.Repositories) == 0 {
		return absent
	}
	names := make([]string, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		name := binding.RepositoryID
		if binding.Access == "read_only" {
			name += " (ro)"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// agentState is the observed process state of the task's session.
//
// A task with no session has nothing to report, which is not the same as a
// stopped one: nothing has been started.
func agentState(task api.Task) string {
	if task.Session == nil {
		return absent
	}
	return task.Session.Process
}

// attentionState renders whether the user may need to intervene.
func attentionState(task api.Task) string {
	switch task.Attention {
	case "none":
		return "—"
	case "possibly_waiting":
		return "possibly waiting"
	case "needs_input":
		return "needs input"
	default:
		return task.Attention
	}
}

// runtimeState renders the application runtime's lifecycle state.
//
// A task with no runtime is absent rather than stopped: v0 starts application
// services only by explicit user action (FR-RUN-005).
func runtimeState(task api.Task) string {
	if task.Runtime == nil {
		return "absent"
	}
	return task.Runtime.State
}

// changedFiles totals what Feat last observed across the task's repositories.
//
// A task none of whose repositories has been observed reports nothing rather
// than zero: those are different answers, and only one of them was measured.
func changedFiles(task api.Task) string {
	total, observed := 0, false
	for _, binding := range task.Repositories {
		if binding.Observation == nil {
			continue
		}
		observed = true
		total += binding.Observation.ChangedFiles
	}
	if !observed {
		return absent
	}
	return strconv.Itoa(total)
}

// elapsed is how long the task has existed, rendered coarsely.
func elapsed(task api.Task, now time.Time) string {
	return duration(now.Sub(task.CreatedAt))
}

// duration renders a span in the largest unit that keeps it readable.
func duration(span time.Duration) string {
	if span < 0 {
		span = 0
	}
	switch {
	case span < time.Minute:
		return fmt.Sprintf("%ds", int(span.Seconds()))
	case span < time.Hour:
		return fmt.Sprintf("%dm", int(span.Minutes()))
	case span < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(span.Hours()), int(span.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(span.Hours())/24, int(span.Hours())%24)
	}
}

// isDraft reports whether a task has not been confirmed yet.
func isDraft(task api.Task) bool { return task.Workflow == "draft" }

// isArchived reports whether a task has been archived, which a cancelled draft
// is.
func isArchived(task api.Task) bool { return task.Workflow == "archived" }

// activeTasks are the tasks the dashboard lists.
//
// Archived tasks are left out: they are the record of work that is over, and
// showing every cancelled draft forever would bury the tasks a user is actually
// running. The count is still reported, so nothing disappears silently.
func activeTasks(tasks []api.Task) (active []api.Task, archived int) {
	for _, task := range tasks {
		if isArchived(task) {
			archived++
			continue
		}
		active = append(active, task)
	}
	return active, archived
}
