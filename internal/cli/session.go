package cli

import (
	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
)

const resumeLong = `Continue a task's recorded agent session.

The session is continued rather than restarted: Feat hands the agent the session
identifier it reported when it started, so the conversation carries on with its
history rather than opening an empty one that looks the same. For a task that
runs its agent in a container, this brings that container back up first.

It is refused while there is still a live agent to attach to, because a second
one in the same task would be two agents editing one worktree. Nothing reaches
this on its own: reconciliation reports that a session can be resumed and never
resumes one.`

const stopLong = `Stop the container a task's agent runs in.

Everything else the task owns is kept: its worktrees, its branches, its control
workspace, its volumes, and its tmux window, whose pane holds the output of the
session that ran. The task's application services are not touched — those have
their own verbs under ` + "`feat runtime`" + `.

It is the reversible half of a pair. Bring the task back with ` + "`feat task resume`" + `,
which starts the same containers again and continues the same agent session.
There is no command that starts the container without the session, because a
container Feat cannot account for is worse than one that has to be resumed.

A task whose agent runs on this machine rather than in a container is refused:
Feat owns no process there to stop.`

// newResumeCommand continues a task's recorded agent session.
//
// It exists as a command because reconciliation tells users to resume a task and
// the dashboard was the only place they could: a recovery a user reads about in
// one surface and cannot perform there is not a recovery the product offers
// (ADR-057).
func newResumeCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "resume <task>",
		Short: "Continue a task's recorded agent session",
		Long:  withTaskArgument(resumeLong),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRuntimeClient(env, cmd, func(caller *client.Client) error {
				task, err := caller.Resume(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				printf(cmd.OutOrStdout(), "resumed %s: %s\n", task.Key, describeSession(task))
				return nil
			})
		},
	}
}

// newStopCommand stops the environment a task's agent runs in.
func newStopCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <task>",
		Short: "Stop the container a task's agent runs in",
		Long:  withTaskArgument(stopLong),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRuntimeClient(env, cmd, func(caller *client.Client) error {
				task, err := caller.Stop(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				printf(out, "stopped the agent of %s: %s\n", task.Key, describeSession(task))
				printf(out, "its worktrees, branches, control workspace, volumes, and terminal were kept\n")
				printf(out, "bring it back with `feat task resume %s`\n", task.Key)
				return nil
			})
		},
	}
}

// describeSession says what a task's agent session and its environment are now.
//
// Both, because they are two facts and a user who just changed one wants to read
// the other beside it: an agent process and the container it lives in are what
// this pair of commands moves together.
func describeSession(task api.Task) string {
	if task.Session == nil {
		return "no agent session"
	}
	described := "agent " + task.Session.Process
	if task.Session.Execution == nil {
		return described + ", running on this machine"
	}
	return described + ", container " + describeExecution(*task.Session.Execution)
}

// describeExecution renders what was last observed of an agent environment.
func describeExecution(environment api.Execution) string {
	if environment.Status != "" {
		return environment.Status
	}
	if environment.Running {
		return "running"
	}
	return "not running"
}
