package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// claimedCaveat explains what the verification column means when the agent is
// the only reporter.
//
// Feat runs the checks a project configures when the agent asks for review, and
// marks those results as its own. A project that configures none leaves the
// agent's claim as the whole of what is known, and the column says so rather
// than reading like a verdict Feat reached (ADR-032, corrected by ADR-033;
// FR-UI-002, FR-UI-003).
const claimedCaveat = "Feat verifies the checks a project configures"

// Attention badges.
//
// The badge is what makes attention readable across a screen of tasks: a user
// scanning the dashboard is looking for the one task that stopped, and a column
// of words all beginning with the same letters is not something an eye finds
// (FR-UI-004).
const (
	// badgeNeedsInput marks a task the provider reported as blocked on the user.
	badgeNeedsInput = "●"
	// badgeMaybe marks the conservative state: Feat cannot tell a finished turn
	// from a question, so it says it may be waiting rather than that it is.
	badgeMaybe = "◐"
)

// attentionSummary says how many tasks are waiting for the user.
//
// It is the badge for the whole screen. A user who has just come back to a
// terminal wants to know whether anything needs them before they read anything
// else, and counting rows is what this exists to save them.
func attentionSummary(tasks []api.Task) string {
	waiting := 0
	for _, task := range tasks {
		if task.Attention == "needs_input" || task.Attention == "possibly_waiting" {
			waiting++
		}
	}
	if waiting == 0 {
		return ""
	}
	if waiting == 1 {
		return badgeNeedsInput + " 1 task may need you"
	}
	return badgeNeedsInput + " " + strconv.Itoa(waiting) + " tasks may need you"
}

// verificationDetail renders the same results with room to explain them.
func verificationDetail(task api.Task) string {
	if task.Verification == nil {
		return absent + "  " + mutedStyle.Render("(the agent has reported no checks; "+
			"the project's configured checks run when it asks for review)")
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
		detail += "  " + mutedStyle.Render("(reported by the agent, not verified; "+claimedCaveat+")")
	}
	if reported.Summary != "" {
		detail += "\n  " + mutedStyle.Render(reported.Summary)
	}
	return detail
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
