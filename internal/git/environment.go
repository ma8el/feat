package git

import "strconv"

// worktreeSettings are the Git settings every process working in a Feat task
// worktree runs with, in the order they are rendered.
//
// A linked worktree has its own working tree, index, and HEAD, and shares
// everything else with the checkout it was created from — including the one ref
// Git uses for the stash. `refs/stash` is not in Git's per-worktree namespace
// (`HEAD`, `refs/bisect/*`, `refs/worktree/*`, `refs/rewritten/*`), so every
// task worktree of a repository, and the user's own checkout, push onto and pop
// from a single stack. `git stash pop` takes the newest entry in the
// repository, whoever made it, and reports nothing unusual when that entry
// belongs to somebody else.
//
// The agent is told this (the Claude adapter's system prompt) and can act on
// it. Autostash is the part it cannot: `rebase.autoStash` and `merge.autoStash`
// are read from the repository's shared configuration, which is the user's, so
// a user who set either one has an agent stashing onto that shared stack from
// commands it never spelled `stash` — and on a conflict, the autostash entry
// stays on the stack for anyone to pop.
//
// Turning them off for the agent's processes turns a silent stash into
// `error: cannot rebase: You have unstaged changes`, which names what to do
// next and loses nothing. It is deliberately narrow: nothing here forbids
// stashing, because a session that decides to stash has decided something, and
// this is for the settings that decide on its behalf.
//
// See ADR-056 in docs/10-decisions-and-open-questions.md.
var worktreeSettings = [][2]string{
	{"rebase.autoStash", "false"},
	{"merge.autoStash", "false"},
}

// WorktreeEnvironment is the environment those settings need, as KEY=VALUE
// entries.
//
// It uses Git's own `GIT_CONFIG_COUNT` form rather than writing to a
// configuration file, because the file that would have to hold them is the
// user's: a linked worktree reads the main checkout's `.git/config`, and Feat
// changing a value there to protect a task would outlive the task and change
// how the user's own commands behave. The environment is inherited by every Git
// the session starts and by nothing else on the machine.
func WorktreeEnvironment() []string { return configEnvironment(worktreeSettings) }

// noHooksPath is what core.hooksPath is set to for a host-side push.
//
// Git resolves a hook by joining this path with the hook's name, so a path that
// cannot be a directory has no hooks in it and every lookup misses. It is a
// value rather than a temporary empty directory because there is nothing to
// create, nothing to clean up, and nothing a later process could write into
// between the two.
const noHooksPath = "/dev/null"

// pushSettings are the Git settings a host-side push runs with.
//
// A task's repositories are linked worktrees of the user's own checkout, and
// `.git/hooks` and `.git/config` are common-directory files the agent can write
// (ADR-050). Approving a publication should not be how a user runs whatever the
// agent left there, so the push disables the three things that would execute it:
// hooks, the pager, and an external diff driver.
//
// They are settings rather than flags because that is what covers all three at
// once — `--no-verify` reaches the hooks and nothing else — and because they are
// set the same way the worktree settings above are: in the environment of the
// one process, never in the user's configuration file.
//
// It is deliberately narrower than "everything ADR-050 lists". `core.sshCommand`
// and `credential.helper` are how a user's push authenticates on their own
// machine, so overriding them would break the operation rather than protect it;
// what those cost is recorded as an accepted exposure and is not this list's to
// re-decide.
var pushSettings = [][2]string{
	{"core.hooksPath", noHooksPath},
	{"core.pager", "cat"},
	{"diff.external", ""},
}

// PushEnvironment is the environment a host-side push runs with, as KEY=VALUE
// entries.
//
// The settings it carries are pushSettings above; what it does not carry is the
// worktree settings, because those are for the agent's own session and a push is
// not one.
func PushEnvironment() []string { return configEnvironment(pushSettings) }

// configEnvironment renders settings in Git's own GIT_CONFIG_COUNT form.
func configEnvironment(settings [][2]string) []string {
	entries := make([]string, 0, 1+2*len(settings))
	entries = append(entries, "GIT_CONFIG_COUNT="+strconv.Itoa(len(settings)))
	for i, setting := range settings {
		index := strconv.Itoa(i)
		entries = append(entries,
			"GIT_CONFIG_KEY_"+index+"="+setting[0],
			"GIT_CONFIG_VALUE_"+index+"="+setting[1],
		)
	}
	return entries
}
