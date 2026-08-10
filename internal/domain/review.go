package domain

import "time"

// Review is the evidence about one task's work: what changed, what the agent
// claimed about it, and what the checks found.
//
// It is a task-level aggregate of repository-level comparisons: every changed
// repository is compared against its own recorded base commit (FR-REV-001),
// which is why the base is recorded here as well as on the binding. A review
// that outlives a rebase, a branch move, or a configuration edit still knows
// what it was reviewing.
//
// What it deliberately does not hold is the user's decision. That is the task's
// workflow state, which is the only record of it: an aggregate carrying its own
// copy was a second answer to one question, and the two could disagree
// (ADR-047).
type Review struct {
	// TaskID is the task under review.
	TaskID TaskID
	// Repositories are the per-repository change summaries.
	Repositories []RepositoryChange
	// CompletionSummary is what the agent reported when it requested review. It
	// is the agent's claim, not a verified result.
	CompletionSummary string
	// Checks are the agent-reported or provider-gated check results.
	Checks []Check
	// RequestedAt is when the agent requested review, or the zero time if it
	// has not.
	RequestedAt time.Time
	// UpdatedAt is when the snapshot last changed.
	UpdatedAt time.Time
}

// RepositoryChange summarises one repository's changes against its recorded
// base commit.
type RepositoryChange struct {
	// RepositoryID identifies the repository.
	RepositoryID RepositoryID
	// BaseCommit is the immutable base recorded when the task was created.
	BaseCommit string
	// HeadCommit is the task branch's current commit, or empty when the agent
	// has not committed. Uncommitted work is supported (FR-GIT-007).
	HeadCommit string
	// ChangedFiles counts files that differ from the base commit.
	ChangedFiles int
	// Insertions counts added lines.
	Insertions int
	// Deletions counts removed lines.
	Deletions int
	// Dirty reports uncommitted changes in the worktree.
	Dirty bool
	// SummarizedAt is when the comparison was computed.
	SummarizedAt time.Time
}

// CheckStatus is the outcome of one configured check.
type CheckStatus string

// Check statuses.
const (
	// CheckUnknown is a check that has not reported.
	CheckUnknown CheckStatus = "unknown"
	// CheckPassed is a check that succeeded.
	CheckPassed CheckStatus = "passed"
	// CheckFailed is a check that failed.
	CheckFailed CheckStatus = "failed"
	// CheckSkipped is a check that did not run.
	CheckSkipped CheckStatus = "skipped"
)

// Valid reports whether the status is documented.
func (s CheckStatus) Valid() bool {
	switch s {
	case CheckUnknown, CheckPassed, CheckFailed, CheckSkipped:
		return true
	default:
		return false
	}
}

// CheckReporter records who ran a check, because the two carry different
// weight: a provider-gated result was enforced, an agent-reported one was
// claimed.
type CheckReporter string

// Check reporters.
const (
	// ReporterAgent is a result the agent reported.
	ReporterAgent CheckReporter = "agent"
	// ReporterProvider is a result a provider-native completion gate enforced.
	ReporterProvider CheckReporter = "provider"
)

// Valid reports whether the reporter is documented.
func (r CheckReporter) Valid() bool {
	return r == ReporterAgent || r == ReporterProvider
}

// Check is one configured check's result.
type Check struct {
	// ID is the configured check identifier.
	ID string
	// RepositoryID is the repository the check belongs to, or empty for a
	// task-level check.
	RepositoryID RepositoryID
	// Status is the outcome.
	Status CheckStatus
	// Reporter records who ran the check.
	Reporter CheckReporter
	// Detail is what the check said about itself: an agent's own words for a
	// reported result, and a bounded excerpt of the command's output for a
	// gated one.
	//
	// It is shown on the review screen and stored in the task's review
	// document, and it is deliberately never put into an event payload. A
	// failing check prints whatever the project's own program prints, which is
	// the one thing a person reviewing a failure needs and is not something
	// Feat can promise anything about (ADR-036). Whoever fills it bounds it;
	// MaxCheckDetail is the limit a stored review will accept.
	Detail string
	// RanAt is when the check ran, or the zero time if it has not.
	RanAt time.Time
}

// MaxCheckDetail bounds the excerpt a check result carries.
//
// It is large enough for the tail of a failing test run, which is where the
// reason usually is, and small enough that a review document stays a document.
const MaxCheckDetail = 4 << 10

// NewReview creates an empty review for a task.
func NewReview(task TaskID, now time.Time) (*Review, error) {
	review := &Review{
		TaskID:    task,
		UpdatedAt: normalizeTime(now),
	}
	if err := review.Validate(); err != nil {
		return nil, err
	}
	return review, nil
}

// RecordRequest records that the agent explicitly requested review.
//
// Requesting review is a semantic event the agent has to emit; an idle or
// end-of-turn signal never reaches this method (FR-AGENT-008).
func (r *Review) RecordRequest(summary string, checks []Check, now time.Time) error {
	for _, check := range checks {
		if err := check.Validate(r.TaskID); err != nil {
			return err
		}
	}
	r.CompletionSummary = summary
	r.Checks = checks
	r.RequestedAt = normalizeTime(now)
	r.UpdatedAt = normalizeTime(now)
	return nil
}

// RecordChecks records the results a completion gate produced.
//
// A check is identified by its repository and its identifier together, and a
// result overwrites the one recorded for the same identity. What it deliberately
// does not do is clear the rest: an agent's claim about a check the gate did not
// run stays, marked as the claim it is, because removing it would be Feat
// deciding that a report it did not verify never happened.
//
// The results a gate produces are attributed to the provider, and the caller may
// not say otherwise: the difference between an enforced result and an asserted
// one is the whole reason the field exists (FR-AGENT-006, ADR-036).
func (r *Review) RecordChecks(checks []Check, now time.Time) error {
	for _, check := range checks {
		if check.Reporter != ReporterProvider {
			return &ValidationError{
				Entity: "review",
				ID:     r.TaskID.String(),
				Field:  "checks." + check.ID + ".reporter",
				Reason: "must be " + quote(string(ReporterProvider)) +
					" for a result the gate ran, but is " + quote(string(check.Reporter)),
			}
		}
		if err := check.Validate(r.TaskID); err != nil {
			return err
		}
	}

	for _, check := range checks {
		check.RanAt = normalizeTime(check.RanAt)
		replaced := false
		for i := range r.Checks {
			if supersedes(check, r.Checks[i]) {
				r.Checks[i] = check
				replaced = true
				break
			}
		}
		if !replaced {
			r.Checks = append(r.Checks, check)
		}
	}
	r.UpdatedAt = normalizeTime(now)
	return nil
}

// supersedes reports whether a gated result replaces a recorded one.
//
// A check is identified by its repository and its identifier together, so two
// repositories that both configure a check called "test" keep their own results.
// The exception is a result recorded against no repository at all, which is what
// an agent's claim looks like when it names a check by identifier alone: Feat
// has now run the configured check of that identifier, and keeping the claim
// beside the evidence would count one check twice and show a user a check that
// both passed and failed.
func supersedes(gated, recorded Check) bool {
	if gated.ID != recorded.ID {
		return false
	}
	return recorded.RepositoryID == gated.RepositoryID || recorded.RepositoryID == ""
}

// Gated reports whether a completion gate produced any of the recorded results.
//
// It is what tells a claim from an enforced result, and the answer is per review
// rather than per check because that is the question the dashboard asks.
func (r *Review) Gated() bool {
	for _, check := range r.Checks {
		if check.Reporter == ReporterProvider {
			return true
		}
	}
	return false
}

// FailedChecks returns the recorded results that failed, in the order they were
// recorded.
func (r *Review) FailedChecks() []Check {
	var failed []Check
	for _, check := range r.Checks {
		if check.Status == CheckFailed {
			failed = append(failed, check)
		}
	}
	return failed
}

// SummarizeRepository records one repository's comparison against its base.
func (r *Review) SummarizeRepository(change RepositoryChange, now time.Time) error {
	if err := change.Validate(r.TaskID); err != nil {
		return err
	}
	change.SummarizedAt = normalizeTime(change.SummarizedAt)
	for i := range r.Repositories {
		if r.Repositories[i].RepositoryID == change.RepositoryID {
			r.Repositories[i] = change
			r.UpdatedAt = normalizeTime(now)
			return nil
		}
	}
	r.Repositories = append(r.Repositories, change)
	r.UpdatedAt = normalizeTime(now)
	return nil
}

// Repository returns the summary recorded for one repository.
func (r *Review) Repository(id RepositoryID) (RepositoryChange, bool) {
	for _, change := range r.Repositories {
		if change.RepositoryID == id {
			return change, true
		}
	}
	return RepositoryChange{}, false
}

// Validate reports whether the review is internally consistent.
func (r *Review) Validate() error {
	if err := r.TaskID.Validate(); err != nil {
		return err
	}
	if r.UpdatedAt.IsZero() {
		return &ValidationError{Entity: "review", ID: r.TaskID.String(), Field: "updated_at", Reason: "must be set"}
	}
	seen := make(map[RepositoryID]bool, len(r.Repositories))
	for _, change := range r.Repositories {
		if err := change.Validate(r.TaskID); err != nil {
			return err
		}
		if seen[change.RepositoryID] {
			return &ValidationError{
				Entity: "review",
				ID:     r.TaskID.String(),
				Field:  "repositories",
				Reason: "must not summarise repository " + change.RepositoryID.String() + " twice",
			}
		}
		seen[change.RepositoryID] = true
	}
	for _, check := range r.Checks {
		if err := check.Validate(r.TaskID); err != nil {
			return err
		}
	}
	return nil
}

// Validate reports whether the change summary is internally consistent.
func (c RepositoryChange) Validate(task TaskID) error {
	if err := c.RepositoryID.Validate(); err != nil {
		return err
	}
	field := "repositories." + c.RepositoryID.String()
	if !commitPattern.MatchString(c.BaseCommit) {
		return &ValidationError{
			Entity: "review",
			ID:     task.String(),
			Field:  field + ".base_commit",
			Reason: "must be the full commit recorded for the task, but is " + quote(c.BaseCommit),
		}
	}
	if c.HeadCommit != "" && !commitPattern.MatchString(c.HeadCommit) {
		return &ValidationError{
			Entity: "review",
			ID:     task.String(),
			Field:  field + ".head_commit",
			Reason: "must be a full 40-character commit, but is " + quote(c.HeadCommit),
		}
	}
	return nil
}

// Validate reports whether the check result is internally consistent.
func (c Check) Validate(task TaskID) error {
	if c.ID == "" {
		return &ValidationError{Entity: "review", ID: task.String(), Field: "checks", Reason: "must give every check an identifier"}
	}
	if c.RepositoryID != "" {
		if err := c.RepositoryID.Validate(); err != nil {
			return err
		}
	}
	if !c.Status.Valid() {
		return &ValidationError{
			Entity: "review",
			ID:     task.String(),
			Field:  "checks." + c.ID + ".status",
			Reason: "must be a documented check status, but is " + quote(string(c.Status)),
		}
	}
	if !c.Reporter.Valid() {
		return &ValidationError{
			Entity: "review",
			ID:     task.String(),
			Field:  "checks." + c.ID + ".reporter",
			Reason: "must record whether the agent or the provider reported the result, but is " + quote(string(c.Reporter)),
		}
	}
	if len(c.Detail) > MaxCheckDetail {
		return &ValidationError{
			Entity: "review",
			ID:     task.String(),
			Field:  "checks." + c.ID + ".detail",
			Reason: "is " + formatInt(len(c.Detail)) + " bytes, and a check result carries an excerpt of at most " +
				formatInt(MaxCheckDetail),
		}
	}
	return nil
}
