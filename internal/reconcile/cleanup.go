package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

// Class is one kind of resource a task owns, and one independent choice in a
// cleanup (FR-CLEAN-002).
type Class string

// The classes. The four docs/04-functional-specification.md names, with the
// agent's containers and the application's kept apart because they are separate
// concepts everywhere else in this product, plus the two a task owns that the
// specification's list does not reach: a tmux window that would otherwise
// outlive every task for ever, and the control workspace ADR-032 said cleanup
// owns. FR-CLEAN-001 requires the inventory to be exact, and an inventory that
// cannot name two of a task's resources is not (ADR-037).
const (
	// ClassTerminal is the task's tmux window and its panes.
	ClassTerminal Class = "terminal"
	// ClassAgentContainers is the containers and networks of the task's
	// devcontainer Compose project. Volumes are never included.
	ClassAgentContainers Class = "agent_containers"
	// ClassRuntimeContainers is the containers and networks of the task's
	// application Compose project. Volumes are never included.
	ClassRuntimeContainers Class = "runtime_containers"
	// ClassVolumes is the named volumes of both Compose projects. It is the one
	// class that is never implied by another (FR-CLEAN-004).
	ClassVolumes Class = "volumes"
	// ClassWorktrees is the task's Git worktrees.
	ClassWorktrees Class = "worktrees"
	// ClassBranches is the task's Git branches.
	ClassBranches Class = "branches"
	// ClassControl is the task's control workspace, which holds the brief and
	// the agent's outbox.
	ClassControl Class = "control"
)

// order is the sequence a cleanup removes classes in, and the sequence a user
// is asked about them in.
//
// It is a requirement rather than a detail. What holds a file is stopped before
// the file is removed: a container with a worktree mounted, or a tmux pane whose
// process has the directory open, would otherwise be removed after the thing it
// is using. Risk increases down the list, so a user answering in order answers
// the cheap questions first.
var order = []Class{
	ClassTerminal,
	ClassAgentContainers,
	ClassRuntimeContainers,
	ClassVolumes,
	ClassWorktrees,
	ClassBranches,
	ClassControl,
}

// Classes returns every class in removal order.
func Classes() []Class { return append([]Class(nil), order...) }

// Order returns the class's position in the removal sequence.
func (c Class) Order() int {
	for i, class := range order {
		if class == c {
			return i
		}
	}
	return len(order)
}

// Valid reports whether the class is one this build defines.
func (c Class) Valid() bool { return c.Order() < len(order) }

// Title renders the class for a person.
func (c Class) Title() string {
	switch c {
	case ClassTerminal:
		return "terminal"
	case ClassAgentContainers:
		return "agent containers and networks"
	case ClassRuntimeContainers:
		return "application containers and networks"
	case ClassVolumes:
		return "volumes"
	case ClassWorktrees:
		return "worktrees"
	case ClassBranches:
		return "branches"
	case ClassControl:
		return "control workspace"
	default:
		return string(c)
	}
}

// pathClasses are the classes whose identity is a filesystem path, and which
// therefore have to survive the broad-path rule before anything removes them.
//
// The rule here is coarse on purpose: this package knows nothing about a
// project's worktree root, so it can only refuse the shapes that are wrong
// whatever the configuration says. The precise check — that a path is inside
// the directory Feat owns and outside every checkout — belongs to the adapter
// that does the deleting, and internal/git applies it again immediately before
// removing anything.
var pathClasses = map[Class]bool{ClassWorktrees: true, ClassControl: true}

// WarningVolume is the standing warning of the volume class.
//
// It is a constant of the policy rather than a string the daemon composes,
// because the rule below requires it: a volume is removable only when this exact
// warning was confirmed, so "volumes are retained by default" is a property of
// the policy rather than of the daemon having remembered to attach a warning
// (FR-CLEAN-004, ADR-037).
const WarningVolume = "removing a volume discards whatever it holds, and Feat keeps no copy"

// standing are the warnings a class always carries, whatever its targets say.
var standing = map[Class]string{ClassVolumes: WarningVolume}

// Target is one resource a cleanup could remove.
type Target struct {
	// Class is which choice removes it.
	Class Class
	// Identity is the resource: a path, a branch name, a Compose project name, a
	// volume name, or a tmux window identifier. It is what the token covers and
	// what an adapter is given.
	Identity string
	// Repository is the repository a worktree or branch belongs to, empty
	// otherwise. It is what lets a report group by repository.
	Repository domain.RepositoryID
	// Detail describes the target for a person.
	Detail string
	// Present reports whether it is there now. A target that is already gone is
	// kept in the plan so the inventory stays exact and the record can still be
	// tidied.
	Present bool
	// Warnings are the reasons removing this target needs explicit confirmation:
	// uncommitted work, unpushed commits, an unmerged branch (FR-CLEAN-003).
	// They are deliberately not part of the token.
	Warnings []string
}

// Risky reports whether removing the target would need confirmation.
func (t Target) Risky() bool { return len(t.Warnings) > 0 }

// Plan is the exact set of resources one task owns.
type Plan struct {
	// Project and Task own everything in it.
	Project domain.ProjectID
	Task    domain.TaskID
	// Targets are the resources, in removal order.
	Targets []Target
	// Problems are recorded resources the plan refuses to name as targets. A
	// path that is not one Feat may remove appears here rather than as a target:
	// a record can be edited or restored from a backup, and the moment a path
	// from one of those decides what is deleted the record has become an
	// instruction (ADR-029).
	Problems []string
}

// planSchema versions what a token covers.
//
// A build that changes which resources a class names must invalidate the tokens
// of the build before it, or a plan displayed by one and executed by the other
// would remove a different set than the one the user read.
const planSchema = "1"

// Sort orders the targets in removal order.
func (p *Plan) Sort() {
	sort.SliceStable(p.Targets, func(i, j int) bool {
		a, b := p.Targets[i], p.Targets[j]
		if a.Class != b.Class {
			return a.Class.Order() < b.Class.Order()
		}
		return a.Identity < b.Identity
	})
}

// Token is the stable identifier of what this plan would remove.
//
// It covers the task and the identity of every target, and deliberately not the
// warnings. A token over observations would change whenever the agent wrote a
// file, so a user would be told their plan was stale when what had really
// happened is that their worktree became dirty — and the second is the thing
// they need to hear. Execute re-resolves the plan and compares tokens, so a plan
// that has since gained or lost a resource is refused; the warnings are checked
// separately, against what is true at the moment of removal (ADR-037).
func (p Plan) Token() string {
	digest := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			// The length prefix keeps two different target lists from hashing
			// alike because their concatenations happen to match. A hash never
			// fails a write, which is why the error is discarded here and
			// nowhere else.
			_, _ = digest.Write(fmt.Appendf(nil, "%d:%s\x00", len(part), part))
		}
	}
	write(planSchema, p.Project.String(), p.Task.String())

	identities := make([]string, 0, len(p.Targets))
	for _, target := range p.Targets {
		// The repository is part of what a target is, not decoration. Two
		// repositories of one task are given the same branch name by the same
		// template, so a token over the name alone cannot tell a plan naming
		// api's branch from one naming store's — and the removal is pointed at a
		// repository by this field. Found by reading a real task's inventory,
		// where both branches printed identically.
		identities = append(identities,
			string(target.Class)+"\x00"+target.Repository.String()+"\x00"+target.Identity)
	}
	sort.Strings(identities)
	write(identities...)
	return hex.EncodeToString(digest.Sum(nil))
}

// Classes returns the classes this plan has targets for, in removal order.
func (p Plan) Classes() []Class {
	seen := make(map[Class]bool, len(p.Targets))
	var found []Class
	for _, class := range order {
		for _, target := range p.Targets {
			if target.Class == class && !seen[class] {
				seen[class] = true
				found = append(found, class)
			}
		}
	}
	return found
}

// For returns the targets of one class, in plan order.
func (p Plan) For(class Class) []Target {
	var found []Target
	for _, target := range p.Targets {
		if target.Class == class {
			found = append(found, target)
		}
	}
	return found
}

// Warnings returns every distinct warning of one class, in plan order,
// beginning with the class's standing warning where it has one.
func (p Plan) Warnings(class Class) []string {
	seen := make(map[string]bool)
	var found []string
	if warning, always := standing[class]; always && len(p.For(class)) > 0 {
		seen[warning] = true
		found = append(found, warning)
	}
	for _, target := range p.For(class) {
		for _, warning := range target.Warnings {
			if !seen[warning] {
				seen[warning] = true
				found = append(found, warning)
			}
		}
	}
	return found
}

// Remaining returns the classes with targets that are still present and are not
// in the selection. It is what makes archiving refusable.
func (p Plan) Remaining(selection Selection) []Class {
	chosen := make(map[Class]bool, len(selection.Classes))
	for _, choice := range selection.Classes {
		chosen[choice.Class] = true
	}
	var left []Class
	for _, class := range p.Classes() {
		if chosen[class] {
			continue
		}
		for _, target := range p.For(class) {
			if target.Present {
				left = append(left, class)
				break
			}
		}
	}
	return left
}

// Choice is one class a user selected, with what they were shown about it.
type Choice struct {
	// Class is the class being removed.
	Class Class
	// ConfirmedWarnings are the exact warnings the user was shown and accepted.
	// Execute refuses when a warning observed at removal time is not among them,
	// which is what makes "explicit confirmation" a statement about what the
	// person actually read rather than about a flag somebody set.
	ConfirmedWarnings []string
}

// Selection is what a user asked a cleanup to remove.
type Selection struct {
	// Token is the token of the plan the user was shown.
	Token string
	// Classes are the classes chosen, each with its confirmations.
	Classes []Choice
	// Archive asks for the task's metadata to be archived once the selected
	// resources are gone.
	Archive bool
}

// Chose reports whether the selection includes a class.
func (s Selection) Chose(class Class) bool {
	for _, choice := range s.Classes {
		if choice.Class == class {
			return true
		}
	}
	return false
}

// Check reports whether a selection may be executed against a freshly resolved
// plan.
//
// Every rule here is one of FR-CLEAN-001 to FR-CLEAN-004's, and each is checked
// against the plan as it is now rather than as it was displayed.
func (p Plan) Check(selection Selection) error {
	if selection.Token == "" {
		return fmt.Errorf("a cleanup must carry the token of the plan it was shown, and this request carries none")
	}
	if token := p.Token(); selection.Token != token {
		return fmt.Errorf(
			"the resources of task %s have changed since the plan was displayed, so the cleanup was not performed; "+
				"ask for the plan again and check what it now names", p.Task)
	}
	if len(selection.Classes) == 0 && !selection.Archive {
		return fmt.Errorf("a cleanup must name at least one class of resource to remove")
	}

	seen := make(map[Class]bool, len(selection.Classes))
	for _, choice := range selection.Classes {
		if !choice.Class.Valid() {
			return fmt.Errorf("%q is not a class of resource Feat removes", choice.Class)
		}
		if seen[choice.Class] {
			return fmt.Errorf("the cleanup names %s twice", choice.Class.Title())
		}
		seen[choice.Class] = true

		targets := p.For(choice.Class)
		if len(targets) == 0 {
			return fmt.Errorf("task %s has no %s to remove", p.Task, choice.Class.Title())
		}
		if err := checkWarnings(choice, targets, p.Task); err != nil {
			return err
		}
		for _, target := range targets {
			if err := checkIdentity(target); err != nil {
				return err
			}
		}
	}

	if selection.Archive {
		if left := p.Remaining(selection); len(left) > 0 {
			names := make([]string, 0, len(left))
			for _, class := range left {
				names = append(names, class.Title())
			}
			return fmt.Errorf(
				"task %s cannot be archived while it still owns %s: an archived task is one Feat stops tracking, "+
					"and those resources would be left with nothing recording that they belong to anybody",
				p.Task, strings.Join(names, ", "))
		}
	}
	return nil
}

// checkWarnings requires every currently observed warning to have been
// confirmed, and every standing warning of the class with it.
func checkWarnings(choice Choice, targets []Target, task domain.TaskID) error {
	confirmed := make(map[string]bool, len(choice.ConfirmedWarnings))
	for _, warning := range choice.ConfirmedWarnings {
		confirmed[warning] = true
	}

	if warning, always := standing[choice.Class]; always && !confirmed[warning] {
		return fmt.Errorf(
			"the %s of task %s: %s. Removing them needs that confirmed explicitly, and the cleanup did not "+
				"confirm it, so nothing was removed", choice.Class.Title(), task, warning)
	}

	for _, target := range targets {
		for _, warning := range target.Warnings {
			if confirmed[warning] {
				continue
			}
			return fmt.Errorf(
				"%s of task %s: %s. Removing it needs that confirmed explicitly, and the cleanup did not confirm it, "+
					"so nothing was removed", target.Identity, choice.Class.Title(), warning)
		}
	}
	return nil
}

// checkIdentity refuses a target Feat must not act on.
//
// It is the last gate before an adapter is asked to remove something, and it is
// deliberately not the only one: internal/git checks a worktree path against the
// task's own root again immediately before removing it. One is about consent and
// ownership, the other about paths, and neither is a copy of the other
// (docs/05-security-model.md, cleanup safety, rule 2).
func checkIdentity(target Target) error {
	if strings.TrimSpace(target.Identity) == "" {
		return fmt.Errorf("a %s target has no identity, so Feat cannot tell what it would remove", target.Class.Title())
	}
	if !pathClasses[target.Class] {
		return nil
	}
	if paths.Broad(target.Identity) {
		return fmt.Errorf(
			"the recorded %s path %q is a shared system directory or is not absolute, which Feat must never remove",
			target.Class.Title(), target.Identity)
	}
	return nil
}
