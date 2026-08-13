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
func WorktreeEnvironment() []string {
	entries := make([]string, 0, 1+2*len(worktreeSettings))
	entries = append(entries, "GIT_CONFIG_COUNT="+strconv.Itoa(len(worktreeSettings)))
	for i, setting := range worktreeSettings {
		index := strconv.Itoa(i)
		entries = append(entries,
			"GIT_CONFIG_KEY_"+index+"="+setting[0],
			"GIT_CONFIG_VALUE_"+index+"="+setting[1],
		)
	}
	return entries
}
