package domain

import "time"

// Review is the review state of one task.
//
// It is a task-level aggregate of repository-level comparisons: every changed
// repository is compared against its own recorded base commit (FR-REV-001),
// which is why the base is recorded here as well as on the binding. A review
// that outlives a rebase, a branch move, or a configuration edit still knows
// what it was reviewing.
type Review struct {
	// TaskID is the task under review.
	TaskID TaskID
	// Status is the user's decision so far.
	Status ReviewStatus
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
	// DecidedAt is when the user approved or requested changes, or the zero
	// time while the review is pending.
	DecidedAt time.Time
	// UpdatedAt is when the snapshot last changed.
	UpdatedAt time.Time
}

// ReviewStatus is the user's review decision.
type ReviewStatus string

// Review statuses. They mirror the decisions in FR-REV-004: leave pending,
// approve, or send back for revision.
const (
	// ReviewPending is a review the user has not decided.
	ReviewPending ReviewStatus = "pending"
	// ReviewApproved is an approved task. Approval never stops or destroys a
	// runtime by itself.
	ReviewApproved ReviewStatus = "approved"
	// ReviewChangesRequested is a task the user sent back for revision.
	ReviewChangesRequested ReviewStatus = "changes_requested"
)

// Valid reports whether the status is documented.
func (s ReviewStatus) Valid() bool {
	switch s {
	case ReviewPending, ReviewApproved, ReviewChangesRequested:
		return true
	default:
		return false
	}
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
	// Detail is a short human-readable summary. It must not carry secrets,
	// because review state reaches the dashboard and the event stream.
	Detail string
	// RanAt is when the check ran, or the zero time if it has not.
	RanAt time.Time
}

// NewReview creates a pending review for a task.
func NewReview(task TaskID, now time.Time) (*Review, error) {
	review := &Review{
		TaskID:    task,
		Status:    ReviewPending,
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

// Decide records the user's review decision.
func (r *Review) Decide(status ReviewStatus, now time.Time) error {
	if !status.Valid() {
		return &ValidationError{
			Entity: "review",
			ID:     r.TaskID.String(),
			Field:  "status",
			Reason: "must be a documented review status, but is " + quote(string(status)),
		}
	}
	r.Status = status
	if status == ReviewPending {
		r.DecidedAt = time.Time{}
	} else {
		r.DecidedAt = normalizeTime(now)
	}
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
	if !r.Status.Valid() {
		return &ValidationError{
			Entity: "review",
			ID:     r.TaskID.String(),
			Field:  "status",
			Reason: "must be a documented review status, but is " + quote(string(r.Status)),
		}
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
	return nil
}
