package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

// Request is the Git side of one task: which repositories it takes part in,
// where each of them starts, and what it will be given.
//
// Every name and path in it is final. Templates are expanded by the caller,
// because the placeholder vocabulary belongs to configuration and this package
// must not learn the shape of a YAML file to create a worktree.
type Request struct {
	// Project owns the task.
	Project domain.ProjectID
	// Task is the task being prepared.
	Task domain.TaskID
	// Root is the fixed directory Feat owns, which is the part of the
	// configured worktree root that contains no placeholder. Every worktree of
	// every task must descend from it, so that the directory the user allowed
	// and the directory Feat creates in are the same one.
	Root string
	// Fetch reports whether configured remotes are fetched before bases are
	// resolved.
	Fetch bool
	// Repositories are the selected repositories, in the order they should be
	// prepared.
	Repositories []RepositoryRequest
}

// RepositoryRequest is one repository's part in a task.
type RepositoryRequest struct {
	// ID identifies the repository within its project.
	ID domain.RepositoryID
	// HostPath is the user's ordinary checkout. Feat reads it and fetches into
	// it; it never changes what is checked out there.
	HostPath string
	// Remote is the remote a remote base policy reads.
	Remote string
	// DefaultBranch is the branch a remote or local base policy reads.
	DefaultBranch string
	// Access is the task's access to the repository.
	Access domain.TaskAccess
	// Policy decides which commit the task starts from.
	Policy BasePolicy
	// Ref is the revision an explicit base policy reads.
	Ref string
	// Branch is the generated task branch. Read-write repositories have one and
	// read-only repositories must not (invariant 7).
	Branch string
	// WorktreePath is where the task worktree is created.
	WorktreePath string
	// ContainerPath is where the worktree is mounted in a devcontainer, carried
	// through so that the recorded binding is complete.
	ContainerPath string
}

// Plan is what Feat would create for a task, and what it found while working
// that out. Producing one changes nothing except the remote-tracking refs a
// fetch updates, so a draft can show a plan the user has not confirmed.
type Plan struct {
	// Project owns the task.
	Project domain.ProjectID
	// Task is the task being prepared.
	Task domain.TaskID
	// Repositories are the resolved per-repository plans, one for each request
	// that produced no problem.
	Repositories []RepositoryPlan
	// Notes record what happened that the user should know about but that does
	// not stop the task, such as a fetch that failed while an older
	// remote-tracking ref was still available.
	Notes []Note
	// Problems are the reasons this plan cannot be applied.
	Problems []Problem
}

// RepositoryPlan is one repository's resolved plan.
type RepositoryPlan struct {
	// ID identifies the repository within its project.
	ID domain.RepositoryID
	// Access is the task's access to the repository.
	Access domain.TaskAccess
	// HostPath is the user's ordinary checkout.
	HostPath string
	// BaseRef is the ref the base policy named.
	BaseRef string
	// BaseCommit is the immutable commit it resolved to.
	BaseCommit string
	// Branch is the task branch, empty for a read-only repository.
	Branch string
	// WorktreePath is where the task worktree will be created.
	WorktreePath string
	// ContainerPath is where the worktree is mounted in a devcontainer.
	ContainerPath string
}

// Note is something worth reporting that does not stop a task.
type Note struct {
	// Repository is the repository the note is about, empty for a note about
	// the task as a whole.
	Repository domain.RepositoryID
	// Summary is the note, written for the user rather than for a log.
	Summary string
}

// Problem is a reason a plan cannot be applied.
type Problem struct {
	// Repository is the repository the problem is about.
	Repository domain.RepositoryID
	// Reason says what is wrong, in terms the user can act on.
	Reason string
}

// Applicable reports whether the plan can be applied.
func (p *Plan) Applicable() bool { return len(p.Problems) == 0 && len(p.Repositories) > 0 }

// Err returns the plan's problems as one error, or nil when it has none.
func (p *Plan) Err() error {
	if len(p.Problems) == 0 {
		return nil
	}
	return &PlanError{Task: p.Task, Problems: p.Problems}
}

// PlanError reports every reason a task's Git plan cannot be applied.
//
// Every problem is reported rather than the first, for the reason configuration
// validation reports every problem: the user is going to fix them by hand, and
// finding three of them one launch at a time is three times the work.
type PlanError struct {
	// Task is the task the plan belongs to.
	Task domain.TaskID
	// Problems are the reasons, in repository order.
	Problems []Problem
}

func (e *PlanError) Error() string {
	lines := make([]string, 0, len(e.Problems)+1)
	lines = append(lines, fmt.Sprintf("task %s cannot be prepared:", e.Task))
	for _, problem := range e.Problems {
		if problem.Repository == "" {
			lines = append(lines, "  "+problem.Reason)
			continue
		}
		lines = append(lines, "  "+problem.Repository.String()+": "+problem.Reason)
	}
	return strings.Join(lines, "\n")
}

// Plan resolves bases, checks for collisions, and returns what Feat would
// create.
//
// It creates nothing. The only change it makes to the machine is the
// remote-tracking refs a fetch updates, which is a change to the repository's
// refs and never to the user's working tree, index, or checked-out branch
// (FR-GIT-001, FR-GIT-003).
func (g *Git) Plan(ctx context.Context, req Request) (*Plan, error) {
	plan := &Plan{Project: req.Project, Task: req.Task}

	if err := req.validate(); err != nil {
		return nil, err
	}

	checkouts := make([]string, 0, len(req.Repositories))
	for _, repository := range req.Repositories {
		checkouts = append(checkouts, repository.HostPath)
	}

	for _, repository := range req.Repositories {
		resolved, problems, notes := g.planRepository(ctx, req, repository, checkouts)
		plan.Notes = append(plan.Notes, notes...)
		if len(problems) > 0 {
			plan.Problems = append(plan.Problems, problems...)
			continue
		}
		plan.Repositories = append(plan.Repositories, resolved)
	}

	return plan, plan.Err()
}

// planRepository resolves one repository, returning what it found rather than
// stopping at the first problem.
func (g *Git) planRepository(
	ctx context.Context, req Request, repository RepositoryRequest, checkouts []string,
) (RepositoryPlan, []Problem, []Note) {
	var (
		problems []Problem
		notes    []Note
	)
	problem := func(format string, args ...any) {
		problems = append(problems, Problem{Repository: repository.ID, Reason: fmt.Sprintf(format, args...)})
	}
	note := func(format string, args ...any) {
		notes = append(notes, Note{Repository: repository.ID, Summary: fmt.Sprintf(format, args...)})
	}

	if err := repository.validate(); err != nil {
		problem("%s", err)
		return RepositoryPlan{}, problems, notes
	}
	if err := CheckWorktreePath(req.Root, repository.WorktreePath, checkouts); err != nil {
		problem("%s", err)
		return RepositoryPlan{}, problems, notes
	}
	if err := g.IsRepository(ctx, repository.HostPath); err != nil {
		problem("%s", err)
		return RepositoryPlan{}, problems, notes
	}

	// A fetch that fails is reported and does not stop the task: FR-GIT-001
	// asks for a fetch "when network access is available", and a base that
	// resolves from the last fetched state is a task the user can still do. What
	// they must not have is the impression that the base is current.
	if req.Fetch && repository.Policy == PolicyRemote {
		if err := g.Fetch(ctx, repository.HostPath, repository.Remote); err != nil {
			note("fetching %s failed, so the base is resolved from the last fetched state: %s",
				repository.Remote, err)
		}
	}

	base, err := g.ResolveBase(ctx, BaseRequest{
		Dir:           repository.HostPath,
		Policy:        repository.Policy,
		Remote:        repository.Remote,
		DefaultBranch: repository.DefaultBranch,
		Ref:           repository.Ref,
	})
	if err != nil {
		problem("%s", err)
		return RepositoryPlan{}, problems, notes
	}

	problems = append(problems, g.collisions(ctx, repository)...)

	return RepositoryPlan{
		ID:            repository.ID,
		Access:        repository.Access,
		HostPath:      repository.HostPath,
		BaseRef:       base.Ref,
		BaseCommit:    base.Commit,
		Branch:        repository.Branch,
		WorktreePath:  repository.WorktreePath,
		ContainerPath: repository.ContainerPath,
	}, problems, notes
}

// collisions reports resources a task would need that something already holds.
//
// They are found before anything is created so that a draft can show them, and
// they are never resolved by choosing another name: a branch Feat renamed on the
// user's behalf is a branch they did not agree to and will look for under the
// name they saw.
func (g *Git) collisions(ctx context.Context, repository RepositoryRequest) []Problem {
	var problems []Problem
	problem := func(format string, args ...any) {
		problems = append(problems, Problem{Repository: repository.ID, Reason: fmt.Sprintf(format, args...)})
	}

	if _, err := os.Lstat(repository.WorktreePath); err == nil {
		problem("%s already exists: remove it, or clean up the task that owns it", repository.WorktreePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		problem("%s cannot be examined: %s", repository.WorktreePath, err)
	}

	worktrees, err := g.Worktrees(ctx, repository.HostPath)
	if err != nil {
		problem("the worktrees of %s cannot be listed: %s", repository.HostPath, err)
		return problems
	}
	proposed := resolvePath(repository.WorktreePath)
	for _, worktree := range worktrees {
		// Git reports the resolved path, so both sides are resolved before they
		// are compared. Overlap in either direction counts: a worktree inside the
		// proposed path would be removed with the task, and the proposed path
		// inside an existing worktree would nest one working tree in another.
		existing := resolvePath(worktree.Path)
		if paths.Under(existing, proposed) || paths.Under(proposed, existing) {
			problem("%s already has a worktree at %s", repository.HostPath, worktree.Path)
		}
	}

	if repository.Branch != "" {
		exists, err := g.Exists(ctx, repository.HostPath, "refs/heads/"+repository.Branch)
		switch {
		case err != nil:
			problem("checking whether branch %s exists in %s failed: %s",
				repository.Branch, repository.HostPath, err)
		case exists:
			problem("branch %s already exists in %s: delete it, or clean up the task that owns it",
				repository.Branch, repository.HostPath)
		}
	}
	return problems
}

// validate checks the shape of a request before anything is run.
func (r Request) validate() error {
	if err := r.Project.Validate(); err != nil {
		return err
	}
	if err := r.Task.Validate(); err != nil {
		return err
	}
	if len(r.Repositories) == 0 {
		return fmt.Errorf("task %s selects no repository", r.Task)
	}
	if !filepath.IsAbs(r.Root) || paths.Broad(r.Root) {
		return fmt.Errorf(
			"task worktrees would be created under %q, which is not a directory Feat may own: "+
				"it must be an absolute path at least two levels below the filesystem root and outside the shared system directories",
			r.Root)
	}

	seenID := make(map[domain.RepositoryID]bool, len(r.Repositories))
	seenPath := make(map[string]domain.RepositoryID, len(r.Repositories))
	for _, repository := range r.Repositories {
		if seenID[repository.ID] {
			return fmt.Errorf("task %s selects repository %s twice", r.Task, repository.ID)
		}
		seenID[repository.ID] = true

		clean := filepath.Clean(repository.WorktreePath)
		if other, taken := seenPath[clean]; taken {
			return fmt.Errorf("repositories %s and %s would share the worktree %s",
				other, repository.ID, clean)
		}
		seenPath[clean] = repository.ID
	}
	return nil
}

// validate checks one repository request.
func (r RepositoryRequest) validate() error {
	if err := r.ID.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(r.HostPath) {
		return fmt.Errorf("the checkout path %q is not absolute", r.HostPath)
	}
	if !r.Access.Valid() {
		return fmt.Errorf("%q is not a task access mode", string(r.Access))
	}
	if !r.Policy.Valid() {
		return fmt.Errorf("%q is not a base policy", string(r.Policy))
	}
	switch {
	case r.Access == domain.TaskAccessReadWrite && r.Branch == "":
		// Invariant 7. A read-write repository with no branch would put the
		// agent's commits on whatever the worktree happened to check out.
		return errors.New("a read-write repository needs a task branch")
	case r.Access == domain.TaskAccessReadOnly && r.Branch != "":
		return fmt.Errorf("a read-only repository has no task branch, but %q was proposed", r.Branch)
	}
	if r.Branch != "" {
		if err := checkArgument("branch", r.Branch); err != nil {
			return err
		}
	}
	return nil
}

// CheckWorktreePath reports whether a task worktree may be created at a path.
//
// The rules exist because Feat creates this directory and, later, removes it:
//
//   - it must be absolute and written cleanly, so that what is checked is what
//     is used;
//   - it must be strictly inside the root Feat owns, so that removing a task's
//     worktree can never remove the root or anything beside it;
//   - it must not be a shared system directory, checked after symbolic links
//     are resolved, so that a link cannot move a task's directory somewhere Feat
//     would never have accepted;
//   - it must not overlap any repository checkout, in either direction: a
//     worktree inside a checkout would be deleted with the task, and a checkout
//     inside a worktree would be deleted with it.
func CheckWorktreePath(root, path string, checkouts []string) error {
	if path == "" {
		return errors.New("no worktree path was proposed")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("the worktree path %q is not absolute", path)
	}
	if path != filepath.Clean(path) {
		return fmt.Errorf("the worktree path %q is not a clean path: it should be written %q",
			path, filepath.Clean(path))
	}
	if paths.Broad(path) {
		return fmt.Errorf("the worktree path %q is a shared system directory, which Feat must not create or remove", path)
	}

	realRoot, realPath := resolvePath(root), resolvePath(path)
	if paths.Broad(realRoot) {
		return fmt.Errorf("the worktree root %q resolves to %q, which is a shared system directory", root, realRoot)
	}
	if realRoot == realPath || !paths.Under(realRoot, realPath) {
		return fmt.Errorf("the worktree path %q is not inside the worktree root %q, which is the only directory Feat may create task worktrees in",
			path, root)
	}

	for _, checkout := range checkouts {
		if checkout == "" {
			continue
		}
		realCheckout := resolvePath(checkout)
		if paths.Under(realCheckout, realPath) || paths.Under(realPath, realCheckout) {
			return fmt.Errorf("the worktree path %q overlaps the checkout %q: task worktrees must live outside the repositories they come from",
				path, checkout)
		}
	}
	return nil
}

// resolvePath resolves symbolic links as far as the path exists, and keeps the
// rest as written.
//
// A task worktree does not exist yet when it is checked, so the path cannot be
// resolved as a whole. What can be resolved is the part that already exists,
// which is where a link would have to be for the result to land somewhere else.
func resolvePath(path string) string {
	cleaned := filepath.Clean(path)
	remainder := ""
	for current := cleaned; ; {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, remainder)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return cleaned
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}
