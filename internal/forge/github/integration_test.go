package github_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/forge/github"
	"github.com/ma8el/feat/internal/integrationtest"
)

// requireGh ends the test unless the run is opted in and gh is installed.
func requireGh(t *testing.T) {
	t.Helper()

	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that use a real gh", integrationtest.Env)
	}
	if _, err := exec.LookPath(github.Executable); err != nil {
		integrationtest.Unavailable(t, integrationtest.Gh, "gh is not installed")
	}
}

// TestRealGhAcceptsTheFlagsThisAdapterPasses is the verification
// docs/06-technical-architecture.md requires of a provider CLI.
//
// A fake runner pins the argument vector, which is enough to know what Feat
// sends and not enough to know that gh still accepts it. The failure this exists
// to catch is the quiet kind: a renamed flag turns every publication into a
// refusal the user reads as their own project being wrong.
func TestRealGhAcceptsTheFlagsThisAdapterPasses(t *testing.T) {
	requireGh(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if version, err := exec.CommandContext(ctx, github.Executable, "--version").Output(); err == nil {
		t.Logf("checking the flags against %s; this adapter was written against %s",
			strings.TrimSpace(strings.SplitN(string(version), "\n", 2)[0]), github.Verified)
	}

	command := exec.CommandContext(ctx, github.Executable, "pr", "create", "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("`gh pr create --help`: %v\n%s", err, output)
	}
	help := string(output)

	for _, flag := range github.Flags {
		if !strings.Contains(help, flag) {
			t.Errorf("the installed gh does not document %s.\n"+
				"\tThis build passes it for every pull request it opens, so publication would fail "+
				"for a reason the user cannot act on. It was written against gh %s; compare the "+
				"adapter with `gh pr create --help`.",
				flag, github.Verified)
		}
	}
}

// TestRealGhRefusesRatherThanPromptsWithoutATitleAndBody is what this adapter
// relies on in place of a confirmation flag.
//
// glab has `--yes`; gh has nothing equivalent, and what keeps it non-interactive
// is that a title and a body are supplied. This checks the other side of that:
// that gh, given neither and no terminal, says so and exits rather than waiting
// for one. A daemon has no terminal to answer on, so the difference between
// those two behaviours is the difference between a failed publication and a
// hung one.
//
// It needs no account. gh refuses for want of the flags before it needs a
// credential.
func TestRealGhRefusesRatherThanPromptsWithoutATitleAndBody(t *testing.T) {
	requireGh(t)

	// Short, because what would fail this test is gh waiting. A prompt with no
	// terminal would hold until the context ends rather than answering.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	repository := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main", "."},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Feat Test"},
		{"commit", "--quiet", "--allow-empty", "-m", "initial commit"},
		{"remote", "add", "origin", "https://github.com/example/nothing-here.git"},
	} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = repository
		command.Env = append(command.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("`git %s`: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	command := exec.CommandContext(ctx, github.Executable, "pr", "create", "--head", "feat/x", "--base", "main")
	command.Dir = repository
	// A token that is not one: it gets gh past "log in first" and no further, so
	// what is measured is the flag handling rather than the authentication.
	command.Env = append(command.Environ(), "GH_TOKEN=not-a-token", "GH_PROMPT_DISABLED=")
	output, err := command.CombinedOutput()

	if ctx.Err() != nil {
		t.Fatalf("gh waited for a terminal instead of refusing, which would hang a publication:\n%s", output)
	}
	if err == nil {
		t.Fatalf("gh accepted a pull request with no title and no body:\n%s", output)
	}
	if !strings.Contains(string(output), "--title") || !strings.Contains(string(output), "--body") {
		t.Errorf("gh refused for a reason other than the missing title and body, so what this "+
			"adapter relies on may have changed:\n%s", output)
	}
}
