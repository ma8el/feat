package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushable arranges a repository with a task worktree in it.
func pushable(t *testing.T) (*fakeGit, string) {
	t.Helper()

	fake := newFakeGit()
	worktree := t.TempDir()
	fake.add(worktree, &fakeRepository{hooksDir: filepath.Join(worktree, ".git", "hooks")})
	if err := os.MkdirAll(filepath.Join(worktree, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("creating the hook directory: %v", err)
	}
	return fake, worktree
}

// TestAPushCarriesTheCommitItPlanned pins the argument vector.
//
// The refspec names the object rather than a local ref, because a publication
// records the commit the agent's draft describes and the push has to put that
// commit on the remote — not whatever the worktree acquired between the plan and
// the push. Nothing forces: a remote that refuses a non-fast-forward is a
// failure to record, not something to overwrite.
func TestAPushCarriesTheCommitItPlanned(t *testing.T) {
	fake, worktree := pushable(t)
	head := commit("feed")

	if _, err := New(fake).Push(context.Background(), PushRequest{
		Worktree: worktree,
		Remote:   "origin",
		Branch:   "feat/7f3a1c2e-rate-limit",
		Commit:   head,
	}); err != nil {
		t.Fatalf("pushing: %v", err)
	}

	want := "push origin " + head + ":refs/heads/feat/7f3a1c2e-rate-limit"
	if !containsVector(fake.vectors(), want) {
		t.Errorf("the push ran %v, want %q", fake.vectors(), want)
	}
	for _, vector := range fake.vectors() {
		if strings.Contains(vector, "--force") {
			t.Errorf("the push forces: %q", vector)
		}
	}
}

// TestAPushRunsWithHooksAndTheExternalToolsDisabled is the ADR-070 requirement
// that approving a publication is not how a user runs what the agent wrote.
//
// A task's repositories are linked worktrees whose .git/hooks and .git/config
// the agent can write (ADR-050). The settings are passed in this one process's
// environment, in Git's own GIT_CONFIG_COUNT form, and never written to the
// user's configuration file — which a linked worktree shares with the user's own
// checkout, so a value set to protect one task would outlive it.
func TestAPushRunsWithHooksAndTheExternalToolsDisabled(t *testing.T) {
	fake, worktree := pushable(t)

	if _, err := New(fake).Push(context.Background(), PushRequest{
		Worktree: worktree, Remote: "origin", Branch: "task", Commit: commit("feed"),
	}); err != nil {
		t.Fatalf("pushing: %v", err)
	}

	index := -1
	for i, vector := range fake.vectors() {
		if strings.HasPrefix(vector, "push ") {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("nothing was pushed")
	}

	environment := strings.Join(fake.environmentOf(index), " ")
	for _, setting := range []string{"core.hooksPath", "core.pager", "diff.external"} {
		if !strings.Contains(environment, setting) {
			t.Errorf("the push environment does not disable %s: %q", setting, environment)
		}
	}
	if !strings.Contains(environment, "GIT_CONFIG_COUNT=3") {
		t.Errorf("the push environment is not Git's own settings form: %q", environment)
	}
	if strings.Contains(environment, "credential") || strings.Contains(environment, "sshCommand") {
		t.Errorf("the push overrides how the user authenticates: %q", environment)
	}
}

// TestAPushSaysWhichHookItDidNotRun is what keeps disabling hooks from being
// silent.
//
// A pre-push hook is not always its author's own convenience: it may be what
// scans for secrets before anything leaves the machine. Where one exists, Feat's
// publication is the one route out that skips it, and a user who never chose
// that has no way to learn it except by being told (ADR-070).
func TestAPushSaysWhichHookItDidNotRun(t *testing.T) {
	fake, worktree := pushable(t)
	hooks := filepath.Join(worktree, ".git", "hooks")

	// The sample Git installs, which never runs and is not a hook.
	plant(t, filepath.Join(hooks, "pre-push.sample"), "#!/bin/sh\nexit 0\n")

	report, err := New(fake).Push(context.Background(), PushRequest{
		Worktree: worktree, Remote: "origin", Branch: "task", Commit: commit("feed"),
	})
	if err != nil {
		t.Fatalf("pushing: %v", err)
	}
	if len(report.Skipped) != 0 {
		t.Errorf("a repository with only Git's own sample reported %v, and a check with nothing to "+
			"report reports nothing", report.Skipped)
	}

	// The hook itself.
	plant(t, filepath.Join(hooks, "pre-push"), "#!/bin/sh\nexit 0\n")
	report, err = New(fake).Push(context.Background(), PushRequest{
		Worktree: worktree, Remote: "origin", Branch: "task", Commit: commit("feed"),
	})
	if err != nil {
		t.Fatalf("pushing: %v", err)
	}
	if len(report.Skipped) != 1 || !strings.Contains(report.Skipped[0], "pre-push") {
		t.Errorf("the report is %v, want the pre-push hook it did not run", report.Skipped)
	}
	if !strings.Contains(report.Skipped[0], "does not run it") {
		t.Errorf("the report does not say what was skipped: %q", report.Skipped[0])
	}
}

// TestAConfiguredHooksPathIsReportedToo checks the other half of the report.
//
// A repository can point Git at a hook directory of its own, and the hooks there
// are as load-bearing as the ones in .git/hooks. Both are reported, because a
// user who set core.hooksPath deliberately is the user most likely to depend on
// what is in it.
func TestAConfiguredHooksPathIsReportedToo(t *testing.T) {
	fake, worktree := pushable(t)
	elsewhere := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("creating the hook directory: %v", err)
	}
	plant(t, filepath.Join(elsewhere, "pre-push"), "#!/bin/sh\nexit 0\n")

	repository := fake.repositories[worktree]
	repository.hooksPath = elsewhere
	repository.hooksDir = elsewhere

	skipped, err := New(fake).SuppressedHooks(context.Background(), worktree)
	if err != nil {
		t.Fatalf("reporting the hooks a push would skip: %v", err)
	}
	if len(skipped) != 2 {
		t.Fatalf("the report is %v, want the configured path and the hook in it", skipped)
	}
	if !strings.Contains(skipped[0], "core.hooksPath") {
		t.Errorf("the report does not name the configured path: %q", skipped[0])
	}
	if !strings.Contains(skipped[1], "pre-push") {
		t.Errorf("the report does not name the hook: %q", skipped[1])
	}
}

// TestAFailedPushStillSaysWhatItSkipped checks that the two facts are
// independent.
//
// What a push does not run is decided before it runs, and a user whose pre-push
// hook scans for secrets needs to know it was skipped whether or not the remote
// accepted the branch.
func TestAFailedPushStillSaysWhatItSkipped(t *testing.T) {
	fake, worktree := pushable(t)
	plant(t, filepath.Join(worktree, ".git", "hooks", "pre-push"), "#!/bin/sh\nexit 0\n")
	fake.repositories[worktree].fail["push"] = &ExitError{
		Args: []string{"push"}, Dir: worktree, Code: 1,
		Stderr: "remote: You are not allowed to push code to this project.",
	}

	report, err := New(fake).Push(context.Background(), PushRequest{
		Worktree: worktree, Remote: "origin", Branch: "task", Commit: commit("feed"),
	})
	if err == nil {
		t.Fatal("a refused push was reported as successful")
	}
	if len(report.Skipped) != 1 {
		t.Errorf("a failed push reported %v, and what it skipped is true either way", report.Skipped)
	}
}

// TestAPushRefusesAnArgumentGitWouldReadAsAnOption keeps a configured value from
// becoming a flag.
func TestAPushRefusesAnArgumentGitWouldReadAsAnOption(t *testing.T) {
	fake, worktree := pushable(t)

	cases := map[string]PushRequest{
		"a remote that is an option": {
			Worktree: worktree, Remote: "--upload-pack=touch /tmp/pwned", Branch: "task", Commit: commit("feed"),
		},
		"a branch that is an option": {
			Worktree: worktree, Remote: "origin", Branch: "--force", Commit: commit("feed"),
		},
		"an abbreviated commit": {
			Worktree: worktree, Remote: "origin", Branch: "task", Commit: "1a2b3c4",
		},
		"no worktree": {Remote: "origin", Branch: "task", Commit: commit("feed")},
	}

	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(fake).Push(context.Background(), request); err == nil {
				t.Fatal("the push was accepted")
			}
			for _, vector := range fake.vectors() {
				if strings.HasPrefix(vector, "push ") {
					t.Errorf("a refused push still ran %q", vector)
				}
			}
		})
	}
}

// plant puts a file at an exact path.
func plant(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// containsVector reports whether one of the recorded vectors is exactly this.
func containsVector(vectors []string, want string) bool {
	for _, vector := range vectors {
		if vector == want {
			return true
		}
	}
	return false
}
