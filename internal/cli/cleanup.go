package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
)

const cleanupLong = `Produce an exact inventory of the resources a task owns, then ask once whether to
remove all of it and archive the task.

Dirty worktrees, unpushed or unmerged branches, and volumes are listed under the
question. Nothing is removed unless you answer yes.

Outside a terminal the inventory is printed and nothing is removed, so this is
safe in a pipe or a script that only wants to see what a task owns.`

func newCleanupCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup <task>",
		Short: "Plan and execute removal of a task's resources",
		Long:  withTaskArgument(cleanupLong),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRuntimeClient(env, cmd, func(caller *client.Client) error {
				plan, err := caller.CleanupPlan(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				printCleanupPlan(out, plan)

				if !interactive() {
					printf(out, "\nNothing was removed: cleanup asks for confirmation and needs a terminal.\n")
					return nil
				}
				if !plan.Archivable {
					printf(out, "\nNothing was removed: task %s cannot be archived until the problems above are fixed.\n",
						plan.TaskKey)
					return nil
				}

				selection, ok, err := askCleanup(cmd.InOrStdin(), out, plan)
				if err != nil {
					return err
				}
				if !ok {
					printf(out, "\nNothing was removed.\n")
					return nil
				}

				status, err := caller.Cleanup(cmd.Context(), args[0], selection)
				if err != nil {
					// Removal stops where it fails and what went stays gone, so the
					// way out is to fix the cause and ask again. Without this the
					// user has a resource Feat named and no reason to believe a
					// second run would behave differently.
					return fmt.Errorf("%w\nremoval stopped here; fix the cause and run `feat task cleanup %s` again",
						err, args[0])
				}
				printCleanupResult(out, status)
				return nil
			})
		},
	}
}

// printCleanupPlan renders the inventory.
//
// Every target is listed rather than counted. A user deciding whether to remove
// three worktrees needs to see which three, and FR-CLEAN-001 asks for an exact
// inventory rather than a short one.
func printCleanupPlan(out io.Writer, plan api.CleanupPlan) {
	printf(out, "Task %s in project %s is %s.\n", plan.TaskKey, plan.ProjectID, plan.Workflow)

	if len(plan.Classes) == 0 {
		printf(out, "\nFeat resolved no resources for this task.\n")
	}
	for _, class := range plan.Classes {
		printf(out, "\n%s\n", class.Title)
		for _, target := range class.Targets {
			state := "present"
			if !target.Present {
				state = "already gone"
			}
			printf(out, "  %s (%s)\n", target.Identity, state)
			printf(out, "      %s\n", target.Detail)
			for _, warning := range target.Warnings {
				printf(out, "      ! %s\n", warning)
			}
		}
	}

	for _, problem := range plan.Problems {
		printf(out, "\n! %s\n", problem)
	}
}

// askCleanup asks once whether to remove everything and archive the task,
// listing every risk under the question (ADR-110). The request echoes the
// warnings shown, so the daemon refuses one that no longer covers what is true
// (ADR-037).
func askCleanup(in io.Reader, out io.Writer, plan api.CleanupPlan) (api.CleanupSelection, bool, error) {
	question := fmt.Sprintf("\nClean up and archive task %s?", plan.TaskKey)
	if risks := plan.Risks(); len(risks) > 0 {
		question = "\nThis would lose work:\n"
		for _, risk := range risks {
			question += "  ! " + risk + "\n"
		}
		question += fmt.Sprintf("Clean up and archive task %s anyway?", plan.TaskKey)
	}
	confirmed, err := ask(bufio.NewReader(in), out, question)
	if err != nil {
		return api.CleanupSelection{}, false, err
	}
	return plan.Everything(), confirmed, nil
}

// ask puts one question, reading from a reader shared across the whole
// conversation.
//
// A new buffered reader per question discards what the previous one buffered,
// which over a sequence of prompts loses the answers a user typed ahead.
func ask(reader *bufio.Reader, out io.Writer, question string) (bool, error) {
	printf(out, "%s [y/N]: ", question)

	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// printCleanupResult reports which of the selected resources were removed.
func printCleanupResult(out io.Writer, status api.CleanupStatus) {
	printf(out, "\n")
	for _, removal := range status.Removed {
		if removal.Removed {
			printf(out, "removed %s\n", removal.Identity)
			continue
		}
		printf(out, "%s was already gone\n", removal.Identity)
	}
	if status.Archived {
		printf(out, "task %s is archived; its record and history are kept\n", status.Task.Key)
	}
}
