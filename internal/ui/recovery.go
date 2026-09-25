package ui

import (
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// Reconciliation findings live in two places, and neither is the footer.
// Nothing else shows them — `feat daemon status` does not print findings and
// doctor's are a different kind — so the dashboard is where a half-created task
// becomes recoverable. A finding that names a task belongs on that task's
// panel, beside the workflow it contradicts. Everything the pass found is also
// in one overlay, because a finding is three lines — what, where, and what to
// do — and a machine with several has more than a line of footer can hold. The
// rail carries the count and the key, which is the job the attention summary
// above it does.
//
// Nothing here is an action Feat took. Each entry is a resource and what a user
// can do about it, which is the whole of what reconciliation offers
// (FR-STATE-003, FR-STATE-004).

// badgeWarning marks the recovery count in the rail. It is a triangle rather
// than the warning sign, whose width a terminal may double depending on how it
// resolves the emoji presentation; the rail is a fixed thirty-two cells.
const badgeWarning = "▲"

// recoveryFindings is what the last pass found about one task.
func (m Model) recoveryFindings(task api.Task) []api.ReconciliationFinding {
	if !m.reconciliation.Ran || !m.reconciliation.NeedsAttention {
		return nil
	}

	var found []api.ReconciliationFinding
	for _, finding := range m.reconciliation.Findings {
		if finding.Status == "present" {
			continue
		}
		if finding.TaskID == task.ID {
			found = append(found, finding)
		}
	}
	return found
}

// recoveryCount is how many things the last pass wants a person to look at. A
// pass in which everything matched its record counts nothing, because a marker
// that is always there is one nobody reads on the day it matters.
func (m Model) recoveryCount() int {
	if !m.reconciliation.Ran || !m.reconciliation.NeedsAttention {
		return 0
	}

	count := len(m.reconciliation.Problems)
	for _, finding := range m.reconciliation.Findings {
		if finding.Status != "present" {
			count++
		}
	}
	if !m.reconciliation.PreviousRunEndedCleanly {
		count++
	}
	return count
}

// recoveryRailNote is the marker at the foot of the rail.
func (m Model) recoveryRailNote() string {
	count := m.recoveryCount()
	if count == 0 {
		return ""
	}

	word := " warnings"
	if count == 1 {
		word = " warning"
	}
	return attentionStyle.Render(badgeWarning+" "+strconv.Itoa(count)+word) +
		mutedStyle.Render("  ! to see")
}

// recoveryBlock renders a task's findings for the top of its panel. It is at
// the top because a workflow of working and a worktree that is not on disk
// contradict each other, and the fields below would read as though they agreed.
func (m Model) recoveryBlock(findings []api.ReconciliationFinding) string {
	if len(findings) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString(attentionStyle.Render("recovery") + m.checkedAt() + "\n")
	for _, finding := range findings {
		out.WriteString(recoveryEntry(finding, false))
	}
	out.WriteString("\n")
	return out.String()
}

// recoveryList is the overlay: everything the last pass wants looked at.
// Task-scoped findings appear here as well as on their own panels, because a
// panel answers what is wrong with this task and this answers what is wrong at
// all — the question after a machine has been asleep or a daemon restarted.
func (m Model) recoveryList() string {
	if m.recoveryCount() == 0 {
		if m.reconciling {
			return mutedStyle.Render("looking again…")
		}
		return mutedStyle.Render("the last pass found everything where its record said it would be") +
			"\n\n" + mutedStyle.Render(m.checkedText())
	}

	var out strings.Builder
	out.WriteString(mutedStyle.Render(m.checkedText()) + "\n")
	if m.reconciling {
		out.WriteString(mutedStyle.Render("looking again…") + "\n")
	}

	if !m.reconciliation.PreviousRunEndedCleanly {
		out.WriteString("\n" + failureStyle.Render("  unclean shutdown") + "\n")
		out.WriteString(mutedStyle.Render("      the previous daemon did not stop, it died") + "\n")
	}
	for _, finding := range m.reconciliation.Findings {
		if finding.Status == "present" {
			continue
		}
		out.WriteString("\n" + recoveryEntry(finding, true))
	}
	// A pass that could not ask a question says so rather than reporting the
	// answer as "nothing", which is why these are not findings.
	for _, problem := range m.reconciliation.Problems {
		head := "  unchecked"
		if problem.Class != "" {
			head += "  " + problem.Class
		}
		out.WriteString("\n" + failureStyle.Render(head) + "\n")
		out.WriteString(mutedStyle.Render("      "+problem.Reason) + "\n")
	}
	return out.String()
}

// recoveryEntry renders one finding: what it is, what was found, what to do.
func recoveryEntry(finding api.ReconciliationFinding, named bool) string {
	head := "  " + finding.Status + "  " + finding.Class
	if named && finding.TaskKey != "" {
		head += "  task " + finding.TaskKey
	}
	if named && finding.Identity != "" {
		head += "  " + finding.Identity
	}

	var out strings.Builder
	out.WriteString(failureStyle.Render(head) + "\n")
	out.WriteString(mutedStyle.Render("      "+finding.Detail) + "\n")
	if finding.Action != "" {
		out.WriteString(mutedStyle.Render("      → "+finding.Action) + "\n")
	}
	return out.String()
}

// checkedAt says when the pass ran and how to run another. The daemon runs one
// at startup and nothing repeats it on a timer, so without the time a finding
// reads as current however old it is.
func (m Model) checkedAt() string { return mutedStyle.Render("  " + m.checkedText()) }

// checkedText is the same without the spacing a heading needs beside it.
func (m Model) checkedText() string {
	if m.reconciliation.FinishedAt.IsZero() {
		return "r to refresh"
	}
	return "checked " + m.reconciliation.FinishedAt.Local().Format("15:04:05") +
		"  ·  r to refresh"
}
