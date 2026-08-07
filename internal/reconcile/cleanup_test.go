package reconcile

import (
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

// plan builds an inventory for the tests below.
func plan(targets ...Target) *Plan {
	p := &Plan{Project: "example", Task: "7f3a1c2e9b5d4a1f", Targets: targets}
	p.Sort()
	return p
}

func worktree(path string, warnings ...string) Target {
	return Target{Class: ClassWorktrees, Identity: path, Present: true, Warnings: warnings}
}

func volume(name string) Target {
	return Target{Class: ClassVolumes, Identity: name, Present: true}
}

// choose builds a selection that confirms every warning of the given classes.
func choose(p *Plan, classes ...Class) Selection {
	selection := Selection{Token: p.Token()}
	for _, class := range classes {
		selection.Classes = append(selection.Classes, Choice{
			Class: class, ConfirmedWarnings: p.Warnings(class),
		})
	}
	return selection
}

// TestVolumesRemainUnlessExplicitlyChosen is slice 12's sixth acceptance
// criterion.
//
// It is checked as a property of the selection rather than of an outcome,
// because a volume that survived because a fake never removed it proves nothing.
// What matters is that choosing every other class leaves the volume class
// unselected, so nothing downstream is ever asked to remove one.
func TestVolumesRemainUnlessExplicitlyChosen(t *testing.T) {
	p := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/api"),
		Target{Class: ClassBranches, Identity: "feat/7f3a1c2e", Present: true},
		Target{Class: ClassAgentContainers, Identity: "feat-example-7f3a1c2e", Present: true},
		volume("feat-example-7f3a1c2e_claude"),
	)

	everythingElse := choose(p, ClassAgentContainers, ClassWorktrees, ClassBranches)
	if err := p.Check(everythingElse); err != nil {
		t.Fatalf("selecting every class but volumes was refused: %v", err)
	}
	if everythingElse.Chose(ClassVolumes) {
		t.Fatal("a selection that named no volumes chose the volume class")
	}

	// The volume is still there to be chosen, and choosing it needs the warning
	// that says what it costs.
	if len(p.For(ClassVolumes)) != 1 {
		t.Fatalf("the plan lost the volume: %+v", p.Targets)
	}
	unconfirmed := Selection{Token: p.Token(), Classes: []Choice{{Class: ClassVolumes}}}
	if err := p.Check(unconfirmed); err == nil {
		t.Fatal("a volume was removable without confirming what removing it costs")
	}
	if err := p.Check(choose(p, ClassVolumes)); err != nil {
		t.Fatalf("a confirmed volume removal was refused: %v", err)
	}
}

// TestDirtyAndUnmergedResourcesRequireExplicitConfirmation is slice 12's eighth
// acceptance criterion.
//
// The confirmation is the exact warning the user was shown, so a selection that
// confirmed something else does not cover it. That is what makes the rule about
// what a person read rather than about a flag somebody set.
func TestDirtyAndUnmergedResourcesRequireExplicitConfirmation(t *testing.T) {
	dirty := "the worktree has uncommitted or untracked changes"
	unmerged := "the branch is not merged into refs/remotes/origin/main"

	p := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/api", dirty),
		Target{Class: ClassBranches, Identity: "feat/7f3a1c2e", Present: true, Warnings: []string{unmerged}},
	)

	for _, test := range []struct {
		name      string
		selection Selection
		want      string
	}{
		{
			name:      "no confirmation at all",
			selection: Selection{Token: p.Token(), Classes: []Choice{{Class: ClassWorktrees}}},
			want:      dirty,
		},
		{
			name: "a confirmation of the wrong warning",
			selection: Selection{Token: p.Token(), Classes: []Choice{{
				Class: ClassWorktrees, ConfirmedWarnings: []string{unmerged},
			}}},
			want: dirty,
		},
		{
			name: "one class confirmed and the other not",
			selection: Selection{Token: p.Token(), Classes: []Choice{
				{Class: ClassWorktrees, ConfirmedWarnings: []string{dirty}},
				{Class: ClassBranches},
			}},
			want: unmerged,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := p.Check(test.selection)
			if err == nil {
				t.Fatal("removing work that would be lost was allowed without confirming it")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %v, want it to name %q", err, test.want)
			}
			if !strings.Contains(err.Error(), "nothing was removed") {
				t.Errorf("error = %v, want it to say nothing was removed", err)
			}
		})
	}

	if err := p.Check(choose(p, ClassWorktrees, ClassBranches)); err != nil {
		t.Fatalf("a fully confirmed selection was refused: %v", err)
	}
}

// TestAWarningThatAppearedSinceThePlanIsRefused is the reason the token covers
// identities and not observations.
//
// A worktree that became dirty between the screen and the key press is exactly
// what the confirmation exists to protect, and the token deliberately does not
// change for it — so the plan is still the same plan, and the confirmation is
// the thing that no longer covers what is true (ADR-037).
func TestAWarningThatAppearedSinceThePlanIsRefused(t *testing.T) {
	clean := plan(worktree("/state/feat/worktrees/example/7f3a1c2e/api"))
	selection := choose(clean, ClassWorktrees)
	if len(selection.Classes[0].ConfirmedWarnings) != 0 {
		t.Fatal("a clean worktree produced a warning to confirm")
	}

	dirty := plan(worktree("/state/feat/worktrees/example/7f3a1c2e/api",
		"the worktree has uncommitted or untracked changes"))

	if clean.Token() != dirty.Token() {
		t.Fatal("the token changed because a worktree became dirty: " +
			"a plan must not expire because the agent wrote a file")
	}
	err := dirty.Check(selection)
	if err == nil {
		t.Fatal("a confirmation given before the worktree was dirty still removed it")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error = %v, want it to name what appeared since the plan", err)
	}
}

// TestAPlanThatGainedOrLostAResourceIsRefused is the other half of the token's
// job: what is removed has to be what the user read.
func TestAPlanThatGainedOrLostAResourceIsRefused(t *testing.T) {
	before := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/api"),
		worktree("/state/feat/worktrees/example/7f3a1c2e/web"),
	)
	selection := choose(before, ClassWorktrees)

	after := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/api"),
		worktree("/state/feat/worktrees/example/7f3a1c2e/web"),
		worktree("/state/feat/worktrees/example/7f3a1c2e/db"),
	)
	err := after.Check(selection)
	if err == nil {
		t.Fatal("a selection removed a resource that was not in the plan the user was shown")
	}
	if !strings.Contains(err.Error(), "changed since the plan was displayed") {
		t.Errorf("error = %v, want it to say the plan is out of date", err)
	}

	// The same targets in a different order are the same plan: a token that
	// depended on ordering would expire for no reason a user could see.
	reordered := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/web"),
		worktree("/state/feat/worktrees/example/7f3a1c2e/api"),
	)
	if err := reordered.Check(selection); err != nil {
		t.Errorf("reordering the same targets invalidated the plan: %v", err)
	}
}

// TestBroadPathOrNonTaskResourceDeletionIsRejected is slice 12's seventh
// acceptance criterion.
//
// The table is every shape a record could take after being edited, restored
// from a backup, or written by an older version. Each one is a directory a
// removal would delete, and a record that decides what gets deleted has stopped
// being a record (ADR-029).
func TestBroadPathOrNonTaskResourceDeletionIsRejected(t *testing.T) {
	// These are the shapes that are wrong whatever a project configures. A path
	// that merely sits outside the task's own worktree root — another task's
	// worktree, say — is refused by internal/git, which knows the root and
	// checks it again immediately before deleting anything.
	for _, path := range []string{
		"/", "/home", "/Users", "/tmp", "/var", "/etc",
		"relative/worktree", "", "   ",
	} {
		t.Run(path, func(t *testing.T) {
			p := plan(worktree(path))
			err := p.Check(choose(p, ClassWorktrees))
			if err == nil {
				t.Fatalf("a cleanup was allowed to remove %q", path)
			}
		})
	}

	// The control workspace is a path class too, and is checked by the same rule
	// rather than by a second one.
	p := plan(Target{Class: ClassControl, Identity: "/tmp", Present: true})
	if err := p.Check(choose(p, ClassControl)); err == nil {
		t.Fatal("a cleanup was allowed to remove a shared directory as a control workspace")
	}

	// A real task path is accepted, so the rule refuses shapes rather than
	// everything.
	good := plan(worktree("/state/feat/worktrees/example/7f3a1c2e/api"))
	if err := good.Check(choose(good, ClassWorktrees)); err != nil {
		t.Fatalf("a task's own worktree was refused: %v", err)
	}
}

// TestArchivingIsRefusedWhileTheTaskStillOwnsSomething keeps an archived task
// from becoming the orphan this slice exists to report.
func TestArchivingIsRefusedWhileTheTaskStillOwnsSomething(t *testing.T) {
	p := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/api"),
		Target{Class: ClassRuntimeContainers, Identity: "example-7f3a1c2e", Present: true},
	)

	partial := choose(p, ClassWorktrees)
	partial.Archive = true
	err := p.Check(partial)
	if err == nil {
		t.Fatal("a task was archived while its application containers were still running")
	}
	if !strings.Contains(err.Error(), "application containers") {
		t.Errorf("error = %v, want it to name what would be stranded", err)
	}

	everything := choose(p, ClassWorktrees, ClassRuntimeContainers)
	everything.Archive = true
	if err := p.Check(everything); err != nil {
		t.Fatalf("archiving after removing everything was refused: %v", err)
	}

	// A resource that is already gone strands nothing, so it does not block an
	// archive: the user asked for it to be absent and it is.
	absent := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/api"),
		Target{Class: ClassRuntimeContainers, Identity: "example-7f3a1c2e", Present: false},
	)
	onlyWorktrees := choose(absent, ClassWorktrees)
	onlyWorktrees.Archive = true
	if err := absent.Check(onlyWorktrees); err != nil {
		t.Errorf("a resource that was already gone blocked an archive: %v", err)
	}
}

// TestClassesAreSeparateChoicesInRemovalOrder pins FR-CLEAN-002 and the order
// removal depends on.
//
// The order is a requirement rather than a detail: what holds a file is stopped
// before the file is removed, so a container with a worktree mounted cannot be
// removed after the worktree it is using.
func TestClassesAreSeparateChoicesInRemovalOrder(t *testing.T) {
	want := []Class{
		ClassTerminal,
		ClassAgentContainers,
		ClassRuntimeContainers,
		ClassVolumes,
		ClassWorktrees,
		ClassBranches,
		ClassControl,
	}
	got := Classes()
	if len(got) != len(want) {
		t.Fatalf("Classes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Classes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Everything that holds a file comes before the files.
	for _, holder := range []Class{ClassTerminal, ClassAgentContainers, ClassRuntimeContainers} {
		if holder.Order() > ClassWorktrees.Order() {
			t.Errorf("%s is removed after the worktrees it may be using", holder)
		}
	}
	if ClassWorktrees.Order() > ClassBranches.Order() {
		t.Error("branches are deleted before the worktrees checked out on them")
	}

	// Selecting one class never implies another, which is the whole of
	// "separate choices".
	p := plan(
		worktree("/state/feat/worktrees/example/7f3a1c2e/api"),
		volume("feat-example-7f3a1c2e_claude"),
	)
	selection := choose(p, ClassWorktrees)
	if selection.Chose(ClassVolumes) {
		t.Error("choosing the worktrees also chose the volumes")
	}
}

// TestASelectionMustCarryTheTokenOfAPlan refuses a request that names no plan.
func TestASelectionMustCarryTheTokenOfAPlan(t *testing.T) {
	p := plan(worktree("/state/feat/worktrees/example/7f3a1c2e/api"))

	for _, test := range []struct {
		name      string
		selection Selection
	}{
		{"no token", Selection{Classes: []Choice{{Class: ClassWorktrees}}}},
		{"a token from nowhere", Selection{Token: "deadbeef", Classes: []Choice{{Class: ClassWorktrees}}}},
		{"no classes", Selection{Token: p.Token()}},
		{"an unknown class", Selection{Token: p.Token(), Classes: []Choice{{Class: "everything"}}}},
		{"a class the plan has no targets for", Selection{Token: p.Token(), Classes: []Choice{{Class: ClassVolumes}}}},
		{
			"the same class twice",
			Selection{Token: p.Token(), Classes: []Choice{{Class: ClassWorktrees}, {Class: ClassWorktrees}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := p.Check(test.selection); err == nil {
				t.Fatal("a selection was accepted that should not have been")
			}
		})
	}
}

// TestTheTokenSeparatesRepositoriesWithTheSameBranchName is the gap a real
// task's inventory showed.
//
// One branch template gives every repository of a task the same branch name, so
// a token over the name alone cannot tell a plan naming one repository's branch
// from one naming another's — while the removal is pointed at a repository by
// exactly that field.
func TestTheTokenSeparatesRepositoriesWithTheSameBranchName(t *testing.T) {
	branch := func(repository domain.RepositoryID) Target {
		return Target{Class: ClassBranches, Identity: "feat/7f3a1c2e-add-a-rate-limit",
			Repository: repository, Present: true}
	}

	api := plan(branch("api"))
	store := plan(branch("store"))
	if api.Token() == store.Token() {
		t.Error("the same branch name in two repositories produces one token")
	}

	both := plan(branch("api"), branch("store"))
	if both.Token() == api.Token() || both.Token() == store.Token() {
		t.Error("a plan naming two repositories' branches matches one naming a single repository's")
	}

	// A selection built for both is still accepted against both.
	if err := both.Check(choose(both, ClassBranches)); err != nil {
		t.Errorf("a plan with two identically named branches was refused: %v", err)
	}
}

// TestTheTokenSeparatesDifferentTasksAndClasses checks that the digest cannot
// be made to collide by rearranging what it covers.
func TestTheTokenSeparatesDifferentTasksAndClasses(t *testing.T) {
	one := plan(worktree("/a/b/c"), worktree("/d/e/f"))
	two := &Plan{Project: "example", Task: "0000000000000000", Targets: one.Targets}
	if one.Token() == two.Token() {
		t.Error("two tasks with the same resources share a token")
	}

	// The class is part of what a target is, so the same identity under two
	// classes is two different plans.
	three := plan(Target{Class: ClassWorktrees, Identity: "x"})
	four := plan(Target{Class: ClassBranches, Identity: "x"})
	if three.Token() == four.Token() {
		t.Error("a worktree and a branch with the same name share a token")
	}

	// Concatenation must not be ambiguous: "ab"+"c" and "a"+"bc" are different
	// plans.
	five := plan(Target{Class: ClassWorktrees, Identity: "/ab"}, Target{Class: ClassWorktrees, Identity: "/c"})
	six := plan(Target{Class: ClassWorktrees, Identity: "/a"}, Target{Class: ClassWorktrees, Identity: "/bc"})
	if five.Token() == six.Token() {
		t.Error("two different target lists hash alike")
	}
}
