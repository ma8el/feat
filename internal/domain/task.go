package domain

import (
	"regexp"
	"time"
)

// commitPattern matches a full Git object name. Feat records resolved commits
// rather than refs, so an abbreviated name is not accepted.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Task is the aggregate root for one unit of agent work.
//
// A task's shape is mutable while it is a draft and frozen afterwards. That one
// rule carries several requirements at once: nothing is created before the user
// confirms a draft (FR-TASK-003), the brief the agent receives is the brief the
// user accepted, and a resolved base commit never changes for the lifetime of a
// task (invariant 8). Everything a task observes later - states, Git
// observations, the session, the runtime - keeps changing.
type Task struct {
	// ID is the stable identity of the task.
	ID TaskID
	// ProjectID is the project the task belongs to. A task belongs to exactly
	// one project (invariant 1).
	ProjectID ProjectID
	// Title is the short human-facing name.
	Title string
	// Brief is the accepted task brief in Markdown. It is frozen when the task
	// leaves draft.
	Brief string
	// Source records where the brief came from.
	Source TaskSource
	// Workflow is the product-level state.
	Workflow WorkflowState
	// Attention records whether the user may need to intervene.
	//
	// docs/03-domain-model.md lists an attention state on both the task and the
	// agent session. Feat keeps one authoritative copy here: a second copy on
	// the session would be a second source of truth for the same question, and
	// the dashboard, notifications, and review all ask it of the task.
	Attention AttentionState
	// Repositories are the repositories bound to the task. A task may bind
	// several (invariant 5).
	Repositories []TaskRepository
	// Session is the task's single agent session (invariant 2). It is nil until
	// the task is launched.
	Session *AgentSession
	// Runtime is the task's application runtime (invariant 3). It is nil until
	// the user creates one, and it is never shared with another task.
	Runtime *RuntimeEnvironment
	// CreatedAt is immutable.
	CreatedAt time.Time
	// UpdatedAt is when the snapshot last changed.
	UpdatedAt time.Time
}

// SourceKind records where a task's brief came from.
type SourceKind string

// Task sources supported in v0. Ticket sources arrive with the provider
// adapters on the roadmap.
const (
	// SourcePrompt is a brief typed during task preparation.
	SourcePrompt SourceKind = "prompt"
	// SourceMarkdown is a brief imported from a Markdown file.
	SourceMarkdown SourceKind = "markdown"
)

// TaskSource records the origin of a task brief.
type TaskSource struct {
	// Kind is the origin.
	Kind SourceKind
	// Reference locates the origin: the Markdown file path for an imported
	// brief, empty for a typed prompt.
	Reference string
}

// Valid reports whether the kind is a documented source.
func (k SourceKind) Valid() bool {
	return k == SourcePrompt || k == SourceMarkdown
}

// TaskRepository binds a task to one repository of its project.
type TaskRepository struct {
	// RepositoryID identifies the repository within the project.
	RepositoryID RepositoryID
	// Access is the task's access to the repository.
	Access TaskAccess
	// BaseRef is the ref the configured base policy resolved, kept for
	// explanation. The recorded commit, not this ref, is what review compares
	// against.
	BaseRef string
	// BaseCommit is the resolved base commit. It is immutable once the task
	// leaves draft (invariant 8).
	BaseCommit string
	// Branch is the generated task branch. Every read-write binding has one
	// (invariant 7); read-only bindings have none.
	Branch string
	// WorktreePath is the absolute host path of the task worktree. Every
	// selected repository receives one (invariant 6).
	WorktreePath string
	// ContainerPath is where the worktree is mounted in a devcontainer. It is
	// empty for host-native execution.
	ContainerPath string
	// Observation is the last observed Git state, or nil if never observed.
	Observation *GitObservation
}

// GitObservation is what Feat last saw in a task worktree. It is an
// observation, so it carries the time it was taken.
type GitObservation struct {
	// Dirty reports uncommitted changes in the task worktree.
	Dirty bool
	// Ahead counts commits the task branch has beyond its base.
	Ahead int
	// Behind counts commits the base has beyond the task branch.
	Behind int
	// Merged reports whether the task branch is contained in its base branch.
	Merged bool
	// ChangedFiles counts files that differ from the recorded base commit.
	ChangedFiles int
	// ObservedAt is when the observation was taken.
	ObservedAt time.Time
}

// NewTask creates a task in the draft state. Nothing exists on the host for it
// yet, and nothing will until the draft is confirmed.
func NewTask(id TaskID, project ProjectID, title string, source TaskSource, now time.Time) (*Task, error) {
	task := &Task{
		ID:        id,
		ProjectID: project,
		Title:     title,
		Source:    source,
		Workflow:  WorkflowDraft,
		Attention: AttentionNone,
		CreatedAt: normalizeTime(now),
		UpdatedAt: normalizeTime(now),
	}
	if err := task.Validate(); err != nil {
		return nil, err
	}
	return task, nil
}

// Key returns the human-facing short identifier of the task.
func (t *Task) Key() TaskKey { return t.ID.Key() }

// SetTitle replaces the title. Titles are editable only while the task is a
// draft.
func (t *Task) SetTitle(title string, now time.Time) error {
	if err := t.requireDraft("title"); err != nil {
		return err
	}
	if title == "" {
		return &ValidationError{Entity: "task", ID: t.ID.String(), Field: "title", Reason: "must not be empty"}
	}
	t.Title = title
	t.touch(now)
	return nil
}

// SetBrief replaces the task brief. Briefs are editable only while the task is
// a draft, so the agent always receives the brief the user accepted.
func (t *Task) SetBrief(brief string, now time.Time) error {
	if err := t.requireDraft("brief"); err != nil {
		return err
	}
	t.Brief = brief
	t.touch(now)
	return nil
}

// Bind adds a repository to the task. Repository selection is part of a task's
// shape, so it is editable only while the task is a draft.
func (t *Task) Bind(binding TaskRepository, now time.Time) error {
	if err := t.requireDraft("repositories"); err != nil {
		return err
	}
	if err := binding.Validate(t.ID); err != nil {
		return err
	}
	if _, exists := t.Repository(binding.RepositoryID); exists {
		return &InvariantError{
			Entity: "task",
			ID:     t.ID.String(),
			Rule:   "a task binds each repository at most once",
			Reason: "repository " + binding.RepositoryID.String() + " is already bound",
		}
	}
	t.Repositories = append(t.Repositories, binding)
	t.touch(now)
	return nil
}

// Unbind removes a repository from the task while it is a draft.
func (t *Task) Unbind(id RepositoryID, now time.Time) error {
	if err := t.requireDraft("repositories"); err != nil {
		return err
	}
	for i, binding := range t.Repositories {
		if binding.RepositoryID == id {
			t.Repositories = append(t.Repositories[:i:i], t.Repositories[i+1:]...)
			t.touch(now)
			return nil
		}
	}
	return t.unknownRepository(id)
}

// Repository returns a pointer to the binding for the given repository, so a
// caller can read it. Changing it goes through the methods on Task, which is
// where the invariants live.
func (t *Task) Repository(id RepositoryID) (*TaskRepository, bool) {
	for i := range t.Repositories {
		if t.Repositories[i].RepositoryID == id {
			return &t.Repositories[i], true
		}
	}
	return nil, false
}

// ResolveBase records the base ref and the commit it resolved to.
//
// Re-resolving is allowed while the task is a draft, because a draft may be
// refreshed before the user confirms it. Once the task leaves draft the
// recorded commit is immutable (invariant 8): review, cleanup, and every diff
// compare against it for the lifetime of the task.
func (t *Task) ResolveBase(id RepositoryID, ref, commit string, now time.Time) error {
	binding, ok := t.Repository(id)
	if !ok {
		return t.unknownRepository(id)
	}
	if t.Workflow != WorkflowDraft {
		if binding.BaseCommit == commit && binding.BaseRef == ref {
			return nil
		}
		return &InvariantError{
			Entity:    "task",
			ID:        t.ID.String(),
			Invariant: 8,
			Rule:      "the resolved base commit never changes after task creation",
			Reason: "repository " + id.String() + " is recorded at " + binding.BaseCommit +
				" and the task is no longer a draft",
		}
	}
	if !commitPattern.MatchString(commit) {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "repositories." + id.String() + ".base_commit",
			Reason: "must be a full 40-character commit, but is " + quote(commit),
		}
	}
	if ref == "" {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "repositories." + id.String() + ".base_ref",
			Reason: "must not be empty",
		}
	}
	binding.BaseRef = ref
	binding.BaseCommit = commit
	t.touch(now)
	return nil
}

// ObserveRepository records what Feat last saw in a task worktree.
func (t *Task) ObserveRepository(id RepositoryID, observation GitObservation, now time.Time) error {
	binding, ok := t.Repository(id)
	if !ok {
		return t.unknownRepository(id)
	}
	if observation.ObservedAt.IsZero() {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "repositories." + id.String() + ".observation.observed_at",
			Reason: "must be set, because an observation without a time cannot be aged out",
		}
	}
	observation.ObservedAt = normalizeTime(observation.ObservedAt)
	binding.Observation = &observation
	t.touch(now)
	return nil
}

// TransitionTo moves the task to the next workflow state.
//
// It rejects a state the transition table does not allow, and a state the task
// is not ready for. Both produce a *TransitionError naming what is missing.
func (t *Task) TransitionTo(next WorkflowState, now time.Time) error {
	if !next.Valid() {
		return &TransitionError{
			Entity:    "task",
			ID:        t.ID.String(),
			Dimension: "workflow",
			From:      string(t.Workflow),
			To:        string(next),
			Reason:    "no such workflow state",
			Allowed:   workflowStrings(t.Workflow.Reachable()),
		}
	}
	if !t.Workflow.CanTransitionTo(next) {
		return &TransitionError{
			Entity:    "task",
			ID:        t.ID.String(),
			Dimension: "workflow",
			From:      string(t.Workflow),
			To:        string(next),
			Allowed:   workflowStrings(t.Workflow.Reachable()),
		}
	}
	if reason := t.notReadyFor(next); reason != "" {
		return &TransitionError{
			Entity:    "task",
			ID:        t.ID.String(),
			Dimension: "workflow",
			From:      string(t.Workflow),
			To:        string(next),
			Reason:    reason,
		}
	}
	t.Workflow = next
	t.touch(now)
	return nil
}

// SetAttention records whether the user may need to intervene.
//
// Attention is observed rather than decided, so any documented value may follow
// any other. What must never happen is a workflow transition derived from it,
// which the workflow transition table prevents.
func (t *Task) SetAttention(attention AttentionState, now time.Time) error {
	if !attention.Valid() {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "attention",
			Reason: "must be a documented attention state, but is " + quote(string(attention)),
		}
	}
	t.Attention = attention
	t.touch(now)
	return nil
}

// AttachSession gives the task its agent session.
func (t *Task) AttachSession(session *AgentSession, now time.Time) error {
	if session == nil {
		return &ValidationError{Entity: "task", ID: t.ID.String(), Field: "session", Reason: "must not be nil"}
	}
	if t.Session != nil {
		return &InvariantError{
			Entity:    "task",
			ID:        t.ID.String(),
			Invariant: 2,
			Rule:      "one task owns exactly one primary agent session",
			Reason:    "the task already owns a " + session.Provider + " session",
		}
	}
	if err := session.Validate(t.ID); err != nil {
		return err
	}
	t.Session = session
	t.touch(now)
	return nil
}

// AttachRuntime gives the task its application runtime.
func (t *Task) AttachRuntime(runtime *RuntimeEnvironment, now time.Time) error {
	if runtime == nil {
		return &ValidationError{Entity: "task", ID: t.ID.String(), Field: "runtime", Reason: "must not be nil"}
	}
	if t.Runtime != nil {
		return &InvariantError{
			Entity:    "task",
			ID:        t.ID.String(),
			Invariant: 3,
			Rule:      "one task owns at most one application runtime environment",
			Reason:    "the task already owns runtime " + t.Runtime.Identity,
		}
	}
	if err := runtime.Validate(t.ID); err != nil {
		return err
	}
	t.Runtime = runtime
	t.touch(now)
	return nil
}

// Validate reports whether the task is internally consistent, including the
// readiness its current workflow state implies. Storage validates every task it
// writes and every task it reads, so an aggregate that broke an invariant
// cannot become the recorded state of the world.
func (t *Task) Validate() error {
	if err := t.ID.Validate(); err != nil {
		return err
	}
	if err := t.ProjectID.Validate(); err != nil {
		return err
	}
	if t.Title == "" {
		return &ValidationError{Entity: "task", ID: t.ID.String(), Field: "title", Reason: "must not be empty"}
	}
	if err := t.Source.Validate(t.ID); err != nil {
		return err
	}
	if !t.Workflow.Valid() {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "workflow",
			Reason: "must be a documented workflow state, but is " + quote(string(t.Workflow)),
		}
	}
	if !t.Attention.Valid() {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "attention",
			Reason: "must be a documented attention state, but is " + quote(string(t.Attention)),
		}
	}
	if t.CreatedAt.IsZero() {
		return &ValidationError{Entity: "task", ID: t.ID.String(), Field: "created_at", Reason: "must be set"}
	}
	if t.UpdatedAt.Before(t.CreatedAt) {
		return &ValidationError{Entity: "task", ID: t.ID.String(), Field: "updated_at", Reason: "must not precede created_at"}
	}

	seen := make(map[RepositoryID]bool, len(t.Repositories))
	for _, binding := range t.Repositories {
		if err := binding.Validate(t.ID); err != nil {
			return err
		}
		if seen[binding.RepositoryID] {
			return &InvariantError{
				Entity: "task",
				ID:     t.ID.String(),
				Rule:   "a task binds each repository at most once",
				Reason: "repository " + binding.RepositoryID.String() + " is bound twice",
			}
		}
		seen[binding.RepositoryID] = true
	}
	if t.Session != nil {
		if err := t.Session.Validate(t.ID); err != nil {
			return err
		}
	}
	if t.Runtime != nil {
		if err := t.Runtime.Validate(t.ID); err != nil {
			return err
		}
	}

	if reason := t.notReadyFor(t.Workflow); reason != "" {
		return &ValidationError{
			Entity: "task",
			ID:     t.ID.String(),
			Field:  "workflow",
			Reason: "is " + string(t.Workflow) + ", but " + reason,
		}
	}
	return nil
}

// notReadyFor reports why the task cannot hold the given workflow state, or an
// empty string when it can.
//
// Leaving draft requires everything the user confirmed: a brief, a repository
// selection, and a resolved immutable base for every selected repository, with
// a branch and worktree wherever the agent may write (invariants 6 and 7).
// Running states additionally require the agent session the task owns
// (invariant 2).
//
// A draft has none of that yet, and an archived task no longer needs it: a draft
// the user cancels before confirming it is archived without ever having a brief,
// a base, or a session, and refusing to record that would leave the cancelled
// draft with nowhere to go.
func (t *Task) notReadyFor(state WorkflowState) string {
	if state == WorkflowDraft || state == WorkflowArchived {
		return ""
	}
	if t.Brief == "" {
		return "the task has no brief"
	}
	if len(t.Repositories) == 0 {
		return "the task selects no repository"
	}
	for _, binding := range t.Repositories {
		name := binding.RepositoryID.String()
		if binding.BaseCommit == "" {
			return "repository " + name + " has no resolved base commit"
		}
		if binding.WorktreePath == "" {
			return "repository " + name + " has no worktree path"
		}
		if binding.Access == TaskAccessReadWrite && binding.Branch == "" {
			return "read-write repository " + name + " has no task branch"
		}
	}
	if state.requiresSession() && t.Session == nil {
		return "the task owns no agent session"
	}
	return ""
}

// requireDraft rejects a change to the task's shape after the user confirmed
// it.
func (t *Task) requireDraft(field string) error {
	if t.Workflow == WorkflowDraft {
		return nil
	}
	return &InvariantError{
		Entity: "task",
		ID:     t.ID.String(),
		Rule:   "a task's title, brief, and repository selection are frozen when it leaves draft",
		Reason: "the task is " + string(t.Workflow) + " and its " + field + " can no longer change",
	}
}

func (t *Task) unknownRepository(id RepositoryID) error {
	return &ValidationError{
		Entity: "task",
		ID:     t.ID.String(),
		Field:  "repositories",
		Reason: "does not bind repository " + id.String(),
	}
}

func (t *Task) touch(now time.Time) { t.UpdatedAt = normalizeTime(now) }

// Validate reports whether the source is internally consistent.
func (s TaskSource) Validate(task TaskID) error {
	if !s.Kind.Valid() {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  "source.kind",
			Reason: "must be a documented source, but is " + quote(string(s.Kind)),
		}
	}
	if s.Kind == SourceMarkdown && s.Reference == "" {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  "source.reference",
			Reason: "must name the imported file",
		}
	}
	if s.Kind == SourcePrompt && s.Reference != "" {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  "source.reference",
			Reason: "must be empty for a typed prompt",
		}
	}
	return nil
}

// Validate reports whether the binding is internally consistent.
func (b TaskRepository) Validate(task TaskID) error {
	if err := b.RepositoryID.Validate(); err != nil {
		return err
	}
	field := "repositories." + b.RepositoryID.String()
	if !b.Access.Valid() {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  field + ".access",
			Reason: "must be read_write or read_only, but is " + quote(string(b.Access)),
		}
	}
	if b.BaseCommit != "" && !commitPattern.MatchString(b.BaseCommit) {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  field + ".base_commit",
			Reason: "must be a full 40-character commit, but is " + quote(b.BaseCommit),
		}
	}
	if b.Access == TaskAccessReadOnly && b.Branch != "" {
		return &InvariantError{
			Entity:    "task",
			ID:        task.String(),
			Invariant: 7,
			Rule:      "only read-write task repositories have a task branch",
			Reason:    "read-only repository " + b.RepositoryID.String() + " was given branch " + quote(b.Branch),
		}
	}
	if b.WorktreePath != "" && !isAbsPath(b.WorktreePath) {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  field + ".worktree_path",
			Reason: "must be absolute, but is " + quote(b.WorktreePath),
		}
	}
	if b.ContainerPath != "" && !isAbsSlashPath(b.ContainerPath) {
		return &ValidationError{
			Entity: "task",
			ID:     task.String(),
			Field:  field + ".container_path",
			Reason: "must be an absolute path inside the execution environment, but is " + quote(b.ContainerPath),
		}
	}
	return nil
}

func workflowStrings(states []WorkflowState) []string {
	names := make([]string, len(states))
	for i, state := range states {
		names[i] = string(state)
	}
	return names
}
