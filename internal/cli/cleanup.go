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

const cleanupLong = `Produce an exact inventory of the resources a task owns and remove only what you
select.

Each class of resource is a separate choice, asked in the order Feat would remove
them: what holds a file is stopped before the file is removed. Volumes are
retained unless you choose them. Dirty worktrees, unpushed commits, and unmerged
branches are shown and need a second, explicit confirmation.

There is no flag that answers every question. Removing a task's work is not a
thing to do by accident, and a single --yes is exactly how it would happen.

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
					printf(out, "\nNothing was removed: cleanup asks per class of resource and needs a terminal.\n")
					return nil
				}
				if len(plan.Classes) == 0 {
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
					return err
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
// three worktrees needs to see which three, and FR-CLEAN-001 is about the
// inventory being exact rather than about it being short.
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

// askCleanup asks once per class, in removal order, and again for every
// warning.
//
// One question per class is FR-CLEAN-002's "separate choices" taken literally,
// and the second question is FR-CLEAN-003's explicit confirmation. The warnings
// a user accepts are echoed back in the request, so the daemon refuses a
// confirmation that no longer covers what is true (ADR-037).
func askCleanup(in io.Reader, out io.Writer, plan api.CleanupPlan) (api.CleanupSelection, bool, error) {
	reader := bufio.NewReader(in)
	selection := api.CleanupSelection{Token: plan.Token}

	for _, class := range plan.Classes {
		chosen, err := ask(reader, out, fmt.Sprintf("\nRemove the %s of task %s?", class.Title, plan.TaskKey))
		if err != nil {
			return api.CleanupSelection{}, false, err
		}
		if !chosen {
			continue
		}

		choice := api.CleanupChoice{Class: class.Class}
		for _, warning := range class.Warnings {
			accepted, err := ask(reader, out, "  "+warning+".\n  Remove it anyway?")
			if err != nil {
				return api.CleanupSelection{}, false, err
			}
			if !accepted {
				printf(out, "  Leaving the %s alone.\n", class.Title)
				choice.Class = ""
				break
			}
			choice.ConfirmedWarnings = append(choice.ConfirmedWarnings, warning)
		}
		if choice.Class != "" {
			selection.Classes = append(selection.Classes, choice)
		}
	}

	if len(selection.Classes) == 0 {
		return api.CleanupSelection{}, false, nil
	}

	// Archiving is offered only when everything the plan names was chosen,
	// because the daemon refuses it otherwise: an archived task that still owns a
	// running container is exactly the orphan reconciliation exists to report.
	if len(selection.Classes) == len(plan.Classes) && plan.Archivable {
		archive, err := ask(reader, out, "\nArchive the task's metadata? Its record and history are kept.")
		if err != nil {
			return api.CleanupSelection{}, false, err
		}
		selection.Archive = archive
	}

	printf(out, "\nAbout to remove:\n")
	for _, choice := range selection.Classes {
		printf(out, "  - %s\n", title(plan, choice.Class))
	}
	if selection.Archive {
		printf(out, "  - and archive the task's metadata\n")
	}
	confirmed, err := ask(reader, out, "Go ahead?")
	if err != nil {
		return api.CleanupSelection{}, false, err
	}
	return selection, confirmed, nil
}

// title renders a class the way the plan named it.
func title(plan api.CleanupPlan, class string) string {
	for _, entry := range plan.Classes {
		if entry.Class == class {
			return entry.Title
		}
	}
	return class
}

// ask puts one question, reading from a reader shared across the whole
// conversation.
//
// The reader is shared because a new buffered reader per question discards
// whatever the previous one buffered, which for a sequence of prompts means
// losing the answers a user typed ahead.
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

// printCleanupResult reports what went.
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
