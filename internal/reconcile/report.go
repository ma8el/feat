package reconcile

import (
	"sort"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Status is what one pass found about one resource. The four values other than
// Present are the ones docs/02-user-workflows.md §10 asks to be reported, and
// none of them repairs anything: a pass reports and offers the user an action.
type Status string

// Statuses.
const (
	// StatusPresent is a recorded resource that is there and matches its record.
	StatusPresent Status = "present"
	// StatusMissing is a recorded resource that is not there. A missing container
	// is reported and never restarted (FR-STATE-004).
	StatusMissing Status = "missing"
	// StatusOrphaned is a resource carrying Feat's ownership marks that no record
	// claims. It is reported rather than adopted or removed.
	StatusOrphaned Status = "orphaned"
	// StatusInconsistent is a resource that exists but is not what the record
	// says it is.
	StatusInconsistent Status = "inconsistent"
	// StatusDamaged is a resource that exists and cannot be read. It is
	// quarantined so that healthy resources stay usable (ADR-037).
	StatusDamaged Status = "damaged"
)

// Valid reports whether the status is one this build defines.
func (s Status) Valid() bool {
	switch s {
	case StatusPresent, StatusMissing, StatusOrphaned, StatusInconsistent, StatusDamaged:
		return true
	default:
		return false
	}
}

// Severity orders a status for display, most serious first. Present is last so
// that a report leads with what needs the user.
func (s Status) Severity() int {
	switch s {
	case StatusDamaged:
		return 0
	case StatusInconsistent:
		return 1
	case StatusOrphaned:
		return 2
	case StatusMissing:
		return 3
	default:
		return 4
	}
}

// Finding is one resource a reconciliation pass looked at.
type Finding struct {
	// Class is the kind of resource.
	Class Class
	// Status is what was found.
	Status Status
	// Project owns the resource. It is empty only when the resource carries no
	// readable project mark.
	Project domain.ProjectID
	// Task owns the resource, empty for an orphan that names none.
	Task domain.TaskID
	// Identity is the resource itself: a path, a branch, a Compose project, a
	// volume, or a tmux window identifier.
	Identity string
	// Detail says what was found, in terms a user can act on.
	Detail string
	// Action names what the user can do about it, empty when nothing is offered.
	// It is a sentence rather than a command, because the user decides what to
	// run.
	Action string
}

// Problem is an enumeration that failed outright. A damaged finding is a resource
// the enumeration returned and this build could not use, whereas a problem leaves
// the resources behind it unknown rather than broken.
type Problem struct {
	// Class is what could not be enumerated.
	Class Class
	// Project and Task narrow the failure when it was scoped to one.
	Project domain.ProjectID
	Task    domain.TaskID
	// Reason is why.
	Reason string
}

// Report is one whole reconciliation pass.
type Report struct {
	// StartedAt and FinishedAt bound the pass.
	StartedAt  time.Time
	FinishedAt time.Time
	// Findings are what was looked at, most serious first.
	Findings []Finding
	// Problems are the enumerations that failed. A pass whose enumeration failed
	// says so rather than reporting that it found nothing.
	Problems []Problem
	// PreviousRunEndedCleanly reports whether the last daemon to own this state
	// directory stopped rather than died. It comes from the durable daemon record,
	// and it tells the user that interrupted checks explain the findings
	// (ADR-037).
	PreviousRunEndedCleanly bool
	// PreviousRunStoppedAt is when it last stopped, zero when unknown. It bounds
	// how long Feat was not observing.
	PreviousRunStoppedAt time.Time
}

// Add records one finding.
func (r *Report) Add(finding Finding) { r.Findings = append(r.Findings, finding) }

// Fail records an enumeration that could not be performed.
func (r *Report) Fail(problem Problem) { r.Problems = append(r.Problems, problem) }

// Sort orders findings by severity, then class order, then identity, so that
// two passes over the same world produce the same report.
func (r *Report) Sort() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.Status.Severity() != b.Status.Severity() {
			return a.Status.Severity() < b.Status.Severity()
		}
		if a.Class != b.Class {
			return a.Class.Order() < b.Class.Order()
		}
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		return a.Identity < b.Identity
	})
}

// NeedsAttention reports whether anything was not simply present. It decides
// whether the user sees a recovery band, which stays hidden when every resource
// matched its record.
func (r *Report) NeedsAttention() bool {
	if len(r.Problems) > 0 {
		return true
	}
	for _, finding := range r.Findings {
		if finding.Status != StatusPresent {
			return true
		}
	}
	return false
}

// Attention returns the findings that are not simply present.
func (r *Report) Attention() []Finding {
	var found []Finding
	for _, finding := range r.Findings {
		if finding.Status != StatusPresent {
			found = append(found, finding)
		}
	}
	return found
}
