package cli

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/ui"
)

const reviewLong = `Review one task's changes, grouped by repository.

Every repository is compared against the base commit recorded when the task was
created, which never moves: what you see is what this task changed, however far
the branch it started from has travelled since.

Feat renders no diff of its own. It opens the diff, editor, and status commands
the project configures, in the worktree of the repository you select, and takes
the terminal back when you leave them.

In a terminal this opens the review screen. Anywhere else it prints the same
comparison and exits.`

func newReviewCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "review <task>",
		Short: "Review a task's changes against its recorded base commits",
		Long:  withTaskArgument(reviewLong),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			// Review reads a task's worktrees and records what it saw, so it is
			// an explicit action against a running daemon rather than a reason
			// to start one (ADR-028's rule for `feat project add`).
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}

			caller := client.New(layout.Socket)
			defer caller.Close()
			current, err := env.current()
			if err != nil {
				return err
			}

			if !env.interactive {
				// A run with no terminal reports what it observed rather than
				// opening a screen nobody can read, which is what `feat` itself
				// does (ADR-027).
				status, err := caller.Review(cmd.Context(), args[0], api.ReviewObserve)
				if err != nil {
					return err
				}
				printReview(cmd.OutOrStdout(), status)
				return nil
			}

			return ui.Run(cmd.Context(), ui.Options{
				Backend: &backend{client: caller, env: current},
				Daemon:  describeBackend(cmd, caller, layout),
				Review:  args[0],
			})
		},
	}
}

// reviewCommand checks an expanded review command and builds the process.
//
// The daemon expanded it from the project's own configuration and refused
// anything that could leave the task's worktrees. It is checked again here for
// the reason `feat runtime logs` checks the Compose command it is handed: the
// daemon is the same user, and a client that ran whatever it received would be
// one nobody could reason about.
//
// What cannot be checked here is which program it is — a review command is
// whatever the user configured, `git` or `nvim` or something of their own — so
// what is checked is everything else: an argument that would not survive a
// vector, and a working directory that is not an absolute path.
func reviewCommand(command api.ReviewCommand) (*exec.Cmd, error) {
	if strings.TrimSpace(command.Program) == "" {
		return nil, fmt.Errorf("the daemon returned no program for the %s command", command.Kind)
	}
	if strings.HasPrefix(command.Program, "-") {
		return nil, fmt.Errorf("the daemon returned the program %q for the %s command, which would be read "+
			"as an option", command.Program, command.Kind)
	}
	for _, argument := range command.Arguments {
		if strings.ContainsAny(argument, "\x00\n") {
			return nil, fmt.Errorf("the daemon returned an argument containing a NUL or a newline for the %s command",
				command.Kind)
		}
	}
	if !filepath.IsAbs(command.Directory) {
		return nil, fmt.Errorf("the daemon returned the non-absolute directory %q for the %s command",
			command.Directory, command.Kind)
	}

	// #nosec G204 -- the program and its arguments are the project's own
	// configuration, expanded and checked by the daemon and checked again above;
	// every element is one argument and nothing reaches a shell.
	process := exec.Command(command.Program, command.Arguments...)
	process.Dir = command.Directory
	return process, nil
}

// printReview renders a review for a terminal that cannot show the screen.
//
// The repositories come first because they are what review is: each against its
// own recorded base commit, with the commands that open it.
func printReview(out io.Writer, status api.ReviewStatus) {
	printf(out, "%s  %s\n", status.Task.Key, status.Task.Title)
	// The workflow is the decision: approved and changes_requested are workflow
	// states, and there is no second record of them to print (ADR-047).
	printf(out, "workflow  %s\n", status.Task.Workflow)
	if status.Review.Summary != "" {
		printf(out, "the agent says  %s\n", status.Review.Summary)
	}

	if len(status.Repositories) > 0 {
		rows := &table{}
		rows.add("REPOSITORY", "ACCESS", "BASE", "HEAD", "FILES", "+/-", "STATE")
		for _, repository := range status.Repositories {
			rows.add(
				repository.RepositoryID,
				repository.Access,
				short(repository.BaseCommit),
				valueOr(short(repository.HeadCommit), absent),
				strconv.Itoa(repository.ChangedFiles),
				"+"+strconv.Itoa(repository.Insertions)+" -"+strconv.Itoa(repository.Deletions),
				repositoryState(repository),
			)
		}
		printf(out, "\n")
		rows.render(out, "")
	}

	if len(status.Review.Checks) > 0 {
		rows := &table{}
		rows.add("CHECK", "REPOSITORY", "STATUS", "RAN BY")
		for _, check := range status.Review.Checks {
			rows.add(check.ID, valueOr(check.RepositoryID, absent), check.Status, reporter(check.Reporter))
		}
		printf(out, "\n")
		rows.render(out, "")
	}

	for _, command := range status.Commands {
		printf(out, "\n%-7s %s  %s\n", command.Kind, command.RepositoryID,
			strings.Join(append([]string{command.Program}, command.Arguments...), " "))
		printf(out, "        in %s\n", command.Directory)
	}
	for _, note := range status.Notes {
		printf(out, "\nnote: %s\n", note)
	}
}

// repositoryState says what a repository holds beyond its counts.
func repositoryState(repository api.ReviewRepository) string {
	var parts []string
	if repository.Dirty {
		parts = append(parts, "uncommitted")
	}
	if repository.Ahead > 0 {
		parts = append(parts, strconv.Itoa(repository.Ahead)+" ahead")
	}
	if repository.Behind > 0 {
		parts = append(parts, strconv.Itoa(repository.Behind)+" behind")
	}
	if repository.Merged {
		parts = append(parts, "merged")
	}
	if len(parts) == 0 {
		return absent
	}
	return strings.Join(parts, ", ")
}

// reporter says who produced a check result, in words rather than in a term
// only Feat uses. The distinction is the point: one was enforced and the other
// was claimed.
func reporter(value string) string {
	if value == "provider" {
		return "feat"
	}
	return "the agent"
}

// short renders a commit at the length a person reads.
func short(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}
