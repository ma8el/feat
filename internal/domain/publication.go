package domain

import "time"

// ForgeKind is the forge one repository publishes to.
//
// It is declared in project configuration rather than derived from the remote,
// because inference works only where the remote's host is one Feat recognises:
// a self-hosted instance is not guessable (ADR-071).
type ForgeKind string

// Forges Feat publishes to.
const (
	// ForgeGitHub is GitHub, whose merge requests are called pull requests.
	ForgeGitHub ForgeKind = "github"
	// ForgeGitLab is GitLab, self-hosted or not.
	ForgeGitLab ForgeKind = "gitlab"
)

// Valid reports whether the kind is a forge Feat publishes to.
func (k ForgeKind) Valid() bool { return k == ForgeGitHub || k == ForgeGitLab }

// Publication is what publishing one task would do, and what came of it.
//
// It is per repository rather than per task because a publication is one merge
// request per changed repository and a repository's forge is its own, so one
// action can legitimately reach two forges (ADR-073). The plan is recorded
// before anything is attempted and every result is recorded before the next
// repository begins, which is the same order task preparation uses and for a
// stronger reason: a worktree Feat forgot is on the user's disk, and a merge
// request Feat forgot is on somebody else's server (ADR-029, ADR-073
// evidence 1).
//
// A partial publication is therefore a recorded state rather than one to be
// undone. Nothing here rolls back: deleting a merge request that was just
// opened is destructive, can fail on its own account, and cannot recall a
// notification that has already gone out.
type Publication struct {
	// Repositories are the planned repositories in the order they are applied,
	// each carrying its own result once it has one.
	Repositories []RepositoryPublication
	// PlannedAt is when the current plan was recorded.
	PlannedAt time.Time
	// UpdatedAt is when the record last changed.
	UpdatedAt time.Time
}

// PublicationState is what has become of one repository's publication.
type PublicationState string

// Publication states.
const (
	// PublicationPlanned is a repository the publication has not attempted
	// yet. It is what an interruption leaves behind, and it is why the plan is
	// recorded first: what was not attempted is named rather than deduced.
	PublicationPlanned PublicationState = "planned"
	// PublicationPublished is a repository whose merge request Feat opened.
	PublicationPublished PublicationState = "published"
	// PublicationFailed is a repository whose publication failed. It does not
	// stop the others: where the cause is common the user reads it several
	// times, and where it is local to one repository the rest still land
	// (ADR-073 evidence 3).
	PublicationFailed PublicationState = "failed"
)

// Valid reports whether the state is a documented publication state.
func (s PublicationState) Valid() bool {
	switch s {
	case PublicationPlanned, PublicationPublished, PublicationFailed:
		return true
	default:
		return false
	}
}

// RepositoryPublication is one repository's part of a publication: what was
// planned for it, and what came of it.
type RepositoryPublication struct {
	// RepositoryID identifies the repository within the project.
	RepositoryID RepositoryID
	// Forge is the forge the repository publishes to, as its configuration
	// declared it when the plan was made.
	Forge ForgeKind
	// Remote is the Git remote the task branch is pushed to.
	Remote string
	// BaseBranch is the branch the merge request asks to merge into.
	BaseBranch string
	// Commit is the commit the agent's draft describes.
	//
	// It is recorded with the plan so that a draft written before further work
	// is refused rather than published, as a confirmation fingerprint refuses a
	// draft that changed after it was displayed (ADR-070, ADR-031).
	Commit string
	// State is what has become of this repository.
	State PublicationState
	// Request is the merge request Feat opened, and is present exactly while
	// the state is published.
	Request *MergeRequest
	// Failure is why this repository did not publish, in the words of whatever
	// failed, and is present exactly while the state is failed.
	Failure string
	// AttemptedAt is when Feat last attempted this repository, and is the zero
	// time while it is planned.
	AttemptedAt time.Time
}

// MergeRequest is the request Feat opened on a forge. GitHub calls it a pull
// request; there is one record either way, because a task publishes the same
// way to both.
type MergeRequest struct {
	// Reference is the forge's own identifier for the request.
	Reference string
	// URL is where the request can be read.
	URL string
}

// PlanPublication records what publishing this task would do, before anything
// is attempted.
//
// Entries carry no result: a result is recorded by RecordPublished or
// RecordPublicationFailure, one repository at a time, so that the record is
// never ahead of the forge.
//
// Re-planning keeps every merge request already opened. A repository that
// published is kept as it was recorded — skipped as already published rather
// than as stale, which is what lets a refusal keep its one meaning (ADR-073) —
// and a repository the new plan leaves out is kept as well, because a record
// that can forget a merge request is the one hazard this ordering exists to
// remove.
func (t *Task) PlanPublication(entries []RepositoryPublication, now time.Time) error {
	planned := make([]RepositoryPublication, 0, len(entries))
	seen := make(map[RepositoryID]bool, len(entries))

	for _, entry := range entries {
		if err := entry.validatePlan(t.ID); err != nil {
			return err
		}
		binding, bound := t.Repository(entry.RepositoryID)
		if !bound {
			return t.unknownRepository(entry.RepositoryID)
		}
		if binding.Access != TaskAccessReadWrite {
			return &InvariantError{
				Entity:    "task",
				ID:        t.ID.String(),
				Invariant: 7,
				Rule:      "only read-write task repositories have a task branch",
				Reason: "repository " + entry.RepositoryID.String() +
					" is read-only, so it has no branch to open a merge request from",
			}
		}
		if seen[entry.RepositoryID] {
			return &ValidationError{
				Entity: "task",
				ID:     t.ID.String(),
				Field:  "publication.repositories",
				Reason: "must not plan repository " + entry.RepositoryID.String() + " twice",
			}
		}
		seen[entry.RepositoryID] = true

		if entry.State != "" || entry.Request != nil || entry.Failure != "" || !entry.AttemptedAt.IsZero() {
			return &ValidationError{
				Entity: "task",
				ID:     t.ID.String(),
				Field:  "publication.repositories." + entry.RepositoryID.String(),
				Reason: "must carry no result: a plan says what a publication will attempt, and a result " +
					"is recorded by the attempt that produced it",
			}
		}
		entry.State = PublicationPlanned
		if recorded, ok := t.publishedRepository(entry.RepositoryID); ok {
			entry = recorded
		}
		planned = append(planned, entry)
	}

	// Whatever the new plan does not name and Feat has already published. It is
	// kept after the plan rather than in place of it, so the order the
	// repositories are applied in is the order this plan asked for.
	if t.Publication != nil {
		for _, recorded := range t.Publication.Repositories {
			if recorded.State == PublicationPublished && !seen[recorded.RepositoryID] {
				planned = append(planned, recorded)
			}
		}
	}

	t.Publication = &Publication{
		Repositories: planned,
		PlannedAt:    normalizeTime(now),
		UpdatedAt:    normalizeTime(now),
	}
	t.touch(now)
	return nil
}

// RecordPublished records the merge request one repository's publication
// opened.
//
// It is called before the next repository is attempted, so that an interruption
// leaves a record naming what exists rather than one that has to be discovered
// on the forge afterwards (ADR-073).
func (t *Task) RecordPublished(repository RepositoryID, request MergeRequest, now time.Time) error {
	entry, err := t.planned(repository)
	if err != nil {
		return err
	}
	if request.URL == "" {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "publication.repositories." + repository.String() + ".request.url",
			Reason: "must say where the merge request can be read",
		}
	}
	if request.Reference == "" {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "publication.repositories." + repository.String() + ".request.reference",
			Reason: "must carry the forge's own identifier for the merge request",
		}
	}
	opened := request
	entry.State = PublicationPublished
	entry.Request = &opened
	entry.Failure = ""
	entry.AttemptedAt = normalizeTime(now)
	t.Publication.UpdatedAt = normalizeTime(now)
	t.touch(now)
	return nil
}

// RecordPublicationFailure records why one repository did not publish.
//
// The reason is kept in the words of whatever failed, because it is the same
// sentence the user would have seen at the moment and a second account of one
// event helps nobody.
func (t *Task) RecordPublicationFailure(repository RepositoryID, reason string, now time.Time) error {
	entry, err := t.planned(repository)
	if err != nil {
		return err
	}
	if reason == "" {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "publication.repositories." + repository.String() + ".failure",
			Reason: "must say why the repository did not publish",
		}
	}
	entry.State = PublicationFailed
	entry.Request = nil
	entry.Failure = reason
	entry.AttemptedAt = normalizeTime(now)
	t.Publication.UpdatedAt = normalizeTime(now)
	t.touch(now)
	return nil
}

// planned returns the entry a result may be recorded against.
//
// A repository that already published is refused rather than overwritten: a
// re-publication skips it, and recording a second merge request over the first
// would leave the first one open with nothing naming it.
func (t *Task) planned(repository RepositoryID) (*RepositoryPublication, error) {
	if t.Publication == nil {
		return nil, &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "publication",
			Reason: "must be planned before a result is recorded against it",
		}
	}
	for i := range t.Publication.Repositories {
		entry := &t.Publication.Repositories[i]
		if entry.RepositoryID != repository {
			continue
		}
		if entry.State == PublicationPublished {
			opened := ""
			if entry.Request != nil {
				opened = " as " + quote(entry.Request.URL)
			}
			return nil, &InvariantError{
				Entity: "task",
				ID:     t.ID.String(),
				Rule:   "a repository publishes at most one merge request per publication",
				Reason: "repository " + repository.String() + " already published" + opened,
			}
		}
		return entry, nil
	}
	return nil, &ValidationError{
		Entity: "task",
		ID:     t.ID.String(),
		Field:  "publication.repositories",
		Reason: "does not plan repository " + repository.String(),
	}
}

// publishedRepository returns the recorded entry of a repository that has
// already published.
func (t *Task) publishedRepository(repository RepositoryID) (RepositoryPublication, bool) {
	if t.Publication == nil {
		return RepositoryPublication{}, false
	}
	for _, entry := range t.Publication.Repositories {
		if entry.RepositoryID == repository && entry.State == PublicationPublished {
			return entry, true
		}
	}
	return RepositoryPublication{}, false
}

// Repository returns one repository's part of the publication.
func (p *Publication) Repository(id RepositoryID) (RepositoryPublication, bool) {
	for _, entry := range p.Repositories {
		if entry.RepositoryID == id {
			return entry, true
		}
	}
	return RepositoryPublication{}, false
}

// Pending returns the repositories the publication has not attempted, in the
// order it would attempt them.
//
// It is what an interrupted publication is asked for: the plan was recorded
// before anything was applied, so what remains is named rather than worked out
// from what the forges happen to hold.
func (p *Publication) Pending() []RepositoryPublication {
	var pending []RepositoryPublication
	for _, entry := range p.Repositories {
		if entry.State == PublicationPlanned {
			pending = append(pending, entry)
		}
	}
	return pending
}

// Published returns the repositories whose merge requests Feat opened, in plan
// order.
func (p *Publication) Published() []RepositoryPublication {
	var published []RepositoryPublication
	for _, entry := range p.Repositories {
		if entry.State == PublicationPublished {
			published = append(published, entry)
		}
	}
	return published
}

// Validate reports whether the publication record is internally consistent.
func (p *Publication) Validate(task TaskID) error {
	if p.PlannedAt.IsZero() {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  "publication.planned_at",
			Reason: "must be set: a publication holds its plan before it holds any result",
		}
	}
	if p.UpdatedAt.Before(p.PlannedAt) {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  "publication.updated_at",
			Reason: "must not precede planned_at",
		}
	}
	seen := make(map[RepositoryID]bool, len(p.Repositories))
	for _, entry := range p.Repositories {
		if err := entry.Validate(task); err != nil {
			return err
		}
		if seen[entry.RepositoryID] {
			return &ValidationError{
				Entity: "task",
				ID:     task.String(),
				Field:  "publication.repositories",
				Reason: "must not plan repository " + entry.RepositoryID.String() + " twice",
			}
		}
		seen[entry.RepositoryID] = true
	}
	return nil
}

// Validate reports whether one repository's part of a publication is internally
// consistent.
//
// The result fields are checked against the state rather than on their own: a
// published repository with no merge request would be a record that cannot name
// what it created, which is the whole hazard here.
func (r RepositoryPublication) Validate(task TaskID) error {
	if err := r.validatePlan(task); err != nil {
		return err
	}
	field := "publication.repositories." + r.RepositoryID.String()
	if !r.State.Valid() {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  field + ".state",
			Reason: "must be a documented publication state, but is " + quote(string(r.State)),
		}
	}
	switch r.State {
	case PublicationPlanned:
		if r.Request != nil {
			return publicationProblem(task, field+".request",
				"must be absent while the repository has not been attempted")
		}
		if r.Failure != "" {
			return publicationProblem(task, field+".failure",
				"must be empty while the repository has not been attempted")
		}
		if !r.AttemptedAt.IsZero() {
			return publicationProblem(task, field+".attempted_at",
				"must be unset while the repository has not been attempted")
		}
	case PublicationPublished:
		// Both halves, and for the same reason RecordPublished asks for both:
		// the URL is where the user reads the request, and the reference is
		// what anything else has to name it by.
		if r.Request == nil || r.Request.URL == "" || r.Request.Reference == "" {
			return publicationProblem(task, field+".request",
				"must name the merge request the publication opened, by reference and URL")
		}
		if r.Failure != "" {
			return publicationProblem(task, field+".failure",
				"must be empty for a repository that published")
		}
	case PublicationFailed:
		if r.Failure == "" {
			return publicationProblem(task, field+".failure",
				"must say why the repository did not publish")
		}
		if r.Request != nil {
			return publicationProblem(task, field+".request",
				"must be absent for a repository that did not publish")
		}
	}
	if r.State != PublicationPlanned && r.AttemptedAt.IsZero() {
		return publicationProblem(task, field+".attempted_at",
			"must record when the repository was attempted")
	}
	return nil
}

// validatePlan checks the part of an entry a plan decides, which is everything
// the publication needs before it attempts anything.
func (r RepositoryPublication) validatePlan(task TaskID) error {
	if err := r.RepositoryID.Validate(); err != nil {
		return err
	}
	field := "publication.repositories." + r.RepositoryID.String()
	if !r.Forge.Valid() {
		return publicationProblem(task, field+".forge",
			"must be a forge Feat publishes to, but is "+quote(string(r.Forge)))
	}
	if r.Remote == "" {
		return publicationProblem(task, field+".remote", "must name the remote the branch is pushed to")
	}
	if r.BaseBranch == "" {
		return publicationProblem(task, field+".base_branch",
			"must name the branch the merge request asks to merge into")
	}
	if !commitPattern.MatchString(r.Commit) {
		return publicationProblem(task, field+".commit",
			"must be the full commit the draft describes, but is "+quote(r.Commit))
	}
	return nil
}

// publicationProblem reports one inconsistency in a publication record.
func publicationProblem(task TaskID, field, reason string) error {
	return &ValidationError{Entity: "task", ID: task.String(), Field: field, Reason: reason}
}
