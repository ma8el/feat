package git

import "strconv"

// worktreeSettings are the Git settings every process working in a Feat task
// worktree runs with, in the order they are rendered.
//
// Every worktree of a repository shares one `refs/stash` with the user's own
// checkout, and these two settings put work on it from commands the agent never
// spelled `stash`. Turned off, a rebase with a dirty tree stops and says so
// instead. Nothing here forbids stashing, which is a session's own decision to
// make (ADR-056).
var worktreeSettings = [][2]string{
	{"rebase.autoStash", "false"},
	{"merge.autoStash", "false"},
}

// WorktreeEnvironment is the environment those settings need, as KEY=VALUE
// entries. It uses Git's own `GIT_CONFIG_COUNT` form because the file that
// would otherwise hold them is the user's `.git/config`, which the linked
// worktree reads, so a value set there would outlive the task (ADR-056).
func WorktreeEnvironment() []string { return configEnvironment(worktreeSettings) }

// noHooksPath is what core.hooksPath is set to for a host-side push. Git joins
// it with the hook's name, so a path that cannot be a directory misses every
// lookup, and there is no temporary directory to create, clean up, or guard.
const noHooksPath = "/dev/null"

// pushSettings are the Git settings a host-side push runs with.
//
// `.git/hooks` and `.git/config` are common-directory files the agent can write
// (ADR-050), and approving a publication should not be how a user runs what the
// agent left there. The push therefore disables hooks, the pager, and an
// external diff driver (ADR-070). They are settings rather than flags because
// `--no-verify` reaches the hooks and nothing else.
//
// `core.sshCommand` and `credential.helper` stay as the user set them, because
// they are how the push authenticates; overriding them would break the
// operation rather than protect it. ADR-050 records what that leaves exposed.
var pushSettings = [][2]string{
	{"core.hooksPath", noHooksPath},
	{"core.pager", "cat"},
	{"diff.external", ""},
}

// PushEnvironment is the environment a host-side push runs with, as KEY=VALUE
// entries. It carries pushSettings and not the worktree settings, because those
// are for the agent's own session and a push is not one.
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
