package git_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/git"
)

// TestTheWorktreeEnvironmentTurnsOffAutostash pins what the entries say.
//
// The names are Git's own and the form is load-bearing: GIT_CONFIG_COUNT has to
// agree with the number of key/value pairs, or Git rejects every command run
// with it rather than ignoring the extra one.
func TestTheWorktreeEnvironmentTurnsOffAutostash(t *testing.T) {
	entries := git.WorktreeEnvironment()

	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			t.Fatalf("the entry %q is not KEY=VALUE", entry)
		}
		values[name] = value
	}

	if values["GIT_CONFIG_COUNT"] != "2" {
		t.Errorf("GIT_CONFIG_COUNT is %q, want 2 for two settings: %v", values["GIT_CONFIG_COUNT"], entries)
	}
	for _, want := range []struct{ key, value string }{
		{"rebase.autoStash", "false"},
		{"merge.autoStash", "false"},
	} {
		if !configures(values, want.key, want.value) {
			t.Errorf("the environment does not set %s to %s: %v", want.key, want.value, entries)
		}
	}
}

// configures reports whether the rendered environment sets one Git setting.
func configures(values map[string]string, key, value string) bool {
	for name, candidate := range values {
		index, found := strings.CutPrefix(name, "GIT_CONFIG_KEY_")
		if !found || candidate != key {
			continue
		}
		if values["GIT_CONFIG_VALUE_"+index] == value {
			return true
		}
	}
	return false
}

// TestGitReadsTheWorktreeEnvironment runs the real Git against it.
//
// The point of the environment is that a setting in the user's own repository
// configuration does not reach the agent's commands, and only Git can say
// whether its own form was written correctly. A test that only compared strings
// would pass just as happily with a misspelled variable name.
func TestGitReadsTheWorktreeEnvironment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "rebase.autoStash", "true"},
		{"config", "merge.autoStash", "true"},
	} {
		command := exec.Command("git", args...)
		command.Dir = repository
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}

	for _, setting := range []string{"rebase.autoStash", "merge.autoStash"} {
		command := exec.Command("git", "config", "--get", setting)
		command.Dir = repository
		command.Env = append(command.Environ(), git.WorktreeEnvironment()...)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("reading %s under the worktree environment: %v", setting, err)
		}
		if got := strings.TrimSpace(string(output)); got != "false" {
			t.Errorf("%s is %q under the worktree environment, want false", setting, got)
		}
	}
}
