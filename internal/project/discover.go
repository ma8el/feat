package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Checkout is what an ordinary Git checkout can say about itself.
//
// It is what `feat project init` asks a directory before it proposes a
// repository: the identity of a repository is the user's to choose, but the
// branch it develops on and the remote it fetches are facts the checkout
// already holds, and asking a user to retype a fact their tools know is how a
// configuration file acquires a value that was never true.
//
// A field this package could not establish is empty rather than guessed. An
// empty remote is a repository with none, and an empty branch is a checkout
// whose HEAD is detached or whose remote publishes no default; the caller
// decides what to propose instead, and says that it is proposing rather than
// reporting.
type Checkout struct {
	// Root is the absolute path of the working tree's root, which is not
	// necessarily the directory that was asked.
	Root string
	// Remote is the remote a base policy would fetch: "origin" when there is
	// one, otherwise the only other remote, otherwise empty.
	Remote string
	// DefaultBranch is the branch the remote publishes as its head, or the
	// branch currently checked out when it publishes none.
	DefaultBranch string
}

// Inspect asks the repository containing a directory about itself.
//
// A directory that is not in a repository is an error, because the caller asked
// about a repository and there is none: everything else this returns is
// optional, and this is the one answer that decides whether there is anything
// to configure at all.
func Inspect(ctx context.Context, runner Runner, dir string) (Checkout, error) {
	if runner == nil {
		runner = HostRunner{}
	}

	root, err := runner.Run(ctx, dir, gitExecutable, "rev-parse", "--show-toplevel")
	if err != nil {
		return Checkout{}, fmt.Errorf("%s is not inside a Git repository: %w", dir, err)
	}
	checkout := Checkout{Root: strings.TrimSpace(root)}
	if checkout.Root == "" {
		// A repository with no working tree — a bare one, or one asked through a
		// Git directory — has no ordinary checkout to configure, and reporting
		// the empty answer as success would put an empty host_path in a file.
		return Checkout{}, fmt.Errorf("%s has no working tree, so there is no ordinary checkout to configure", dir)
	}

	checkout.Remote = remoteOf(ctx, runner, checkout.Root)
	checkout.DefaultBranch = branchOf(ctx, runner, checkout.Root, checkout.Remote)
	return checkout, nil
}

// remoteOf picks the remote a base policy would fetch.
//
// "origin" wins wherever it exists, because that is the name every clone
// creates and the name Feat defaults to. A repository with exactly one other
// remote answers with that one; a repository with several does not, because
// choosing between them is the user's decision and a wrong guess would be
// written into a file as though it had been established.
func remoteOf(ctx context.Context, runner Runner, root string) string {
	output, err := runner.Run(ctx, root, gitExecutable, "remote")
	if err != nil {
		return ""
	}
	remotes := lines(output)
	switch {
	case len(remotes) == 0:
		return ""
	case len(remotes) == 1:
		return remotes[0]
	}
	for _, remote := range remotes {
		if remote == defaultRemote {
			return remote
		}
	}
	return ""
}

// defaultRemote is the remote name every clone creates, and the one Feat
// defaults to when configuration names none.
const defaultRemote = "origin"

// branchOf resolves the branch a base policy would use.
//
// The remote's own head is asked first: it is what the hosting side calls the
// default branch, and it is right even in a checkout that is sitting on a
// feature branch — which is the checkout a user is most likely to run the
// wizard from. It is a local ref, so nothing here reaches the network.
func branchOf(ctx context.Context, runner Runner, root, remote string) string {
	if remote != "" {
		head, err := runner.Run(ctx, root, gitExecutable,
			"symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
		if err == nil {
			// The ref reads "origin/main", and a branch name may itself contain
			// a slash, so only the remote's own prefix is removed.
			if branch := strings.TrimPrefix(strings.TrimSpace(head), remote+"/"); branch != "" {
				return branch
			}
		}
	}

	current, err := runner.Run(ctx, root, gitExecutable, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		// A detached HEAD answers nothing, which is not a failure: a worktree
		// checked out at a commit is a normal state for a repository to be in.
		return ""
	}
	return strings.TrimSpace(current)
}

// composeFileNames are the file names Docker Compose itself looks for, in its
// own order of preference.
var composeFileNames = []string{
	"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
}

// composeSubdirectories are the directories a project keeps its Compose files
// in, beside the root of a checkout.
var composeSubdirectories = []string{".", ".devcontainer", "docker"}

// ComposeFiles returns the Compose files present under a directory.
//
// It looks only where Compose and the devcontainer convention already look, one
// level deep, and it returns what exists rather than what might: the caller
// offers these as candidates and the user confirms or replaces them, so a file
// this misses costs a user one line of typing and a file this invents would be
// a path in a configuration that does not resolve.
func ComposeFiles(dir string) []string {
	var found []string
	for _, subdirectory := range composeSubdirectories {
		for _, name := range composeFileNames {
			candidate := filepath.Join(dir, subdirectory, name)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			found = append(found, filepath.Clean(candidate))
		}
	}
	return found
}

// maxComposeFileBytes bounds a Compose file read for its service names. A
// Compose file is a few kilobytes; a file this size is something else, and
// reading it into memory to look for a mapping key is not worth doing.
const maxComposeFileBytes = 1 << 20

// ComposeServices returns the service names the given Compose files declare.
//
// Only the keys of the top-level `services` mapping are read. Nothing else in
// the file is looked at, and no value is ever carried out of it: a Compose file
// names environment files, image registries, and sometimes a password that
// should not have been written there, and none of that has any business
// reaching a suggestion (docs/05-security-model.md).
//
// It is deliberately best effort and returns no error. A file that does not
// parse, uses a feature this does not understand, or is not there yet is a file
// this has nothing to suggest from, and the caller asks the question without a
// suggestion. Whether the file and service really exist is `feat doctor`'s
// answer, which it gets from Compose itself rather than from a partial reading.
func ComposeServices(files ...string) []string {
	seen := make(map[string]bool)
	var names []string

	for _, file := range files {
		for _, name := range serviceNames(file) {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// serviceNames reads one Compose file's service names.
func serviceNames(file string) []string {
	info, err := os.Stat(file)
	if err != nil || info.IsDir() || info.Size() > maxComposeFileBytes {
		return nil
	}
	data, err := os.ReadFile(file) // #nosec G304 -- the user named this file, and only mapping keys are read
	if err != nil {
		return nil
	}

	var document struct {
		Services map[string]yaml.RawMessage `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil
	}

	names := make([]string, 0, len(document.Services))
	for name := range document.Services {
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// lines splits command output into non-empty trimmed lines.
func lines(output string) []string {
	var found []string
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			found = append(found, trimmed)
		}
	}
	return found
}
