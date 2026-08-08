package cli

import (
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
)

// absent is what a field this build cannot fill renders as.
//
// It is never a zero or a blank. A list that prints "0 files changed" where it
// has not looked is making a claim it did not measure, which is the rule
// `feat doctor` follows for a check it could not run (ADR-028, ADR-031).
const absent = "-"

const taskListLong = `List tasks across every registered project.

Drafts appear alongside launched tasks and are marked as drafts: a draft has no
worktree, no branch, and no terminal until it is confirmed. Archived tasks,
which a cancelled draft becomes, are counted rather than listed.

Fields a later implementation slice delivers are shown as "-" rather than as a
value that was never measured.

The TASK column is the short key derived from a task's identifier, and it is
what every command that takes a task accepts.`

// taskArgument says what <task> is, wherever a command takes one.
//
// It is one sentence repeated rather than a rule stated once somewhere else,
// because the command a user is reading is where they need it: the defect this
// answers was a user reading `feat attach <task>` with nowhere to get the
// argument from.
const taskArgument = `<task> is a task's short key as ` + "`feat task list`" + ` prints it, its whole
identifier as the dashboard's task detail shows it, or any prefix of that
identifier. A prefix that matches two tasks is reported rather than resolved to
either.`

// withTaskArgument appends that sentence to a command's help.
func withTaskArgument(long string) string {
	if long == "" {
		return taskArgument
	}
	return long + "\n\n" + taskArgument
}

func newTaskCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Inspect tasks",
	}
	cmd.AddCommand(newTaskListCommand(env))
	return cmd
}

func newTaskListCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tasks across all projects",
		Long:  taskListLong,
		Args:  checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}

			caller := client.New(layout.Socket)
			defer caller.Close()

			tasks, err := caller.Tasks(cmd.Context())
			if err != nil {
				return err
			}
			printTasks(cmd.OutOrStdout(), tasks, env.clock()())
			return nil
		},
	}
}

// printTasks renders the task list.
//
// The columns are the v0 task row (FR-UI-002) minus the ones no slice has
// delivered: verification state arrives with the Claude adapter and resource
// usage with the resource monitor, and both are shown as absent in the
// dashboard rather than dropped. PR state is not required in v0.
func printTasks(out io.Writer, tasks []api.Task, now time.Time) {
	active, archived := 0, 0
	for _, task := range tasks {
		if task.Workflow == "archived" {
			archived++
			continue
		}
		active++
	}

	if active == 0 {
		printf(out, "no tasks\n")
		printf(out, "prepare one with `feat implement`\n")
		if archived > 0 {
			printf(out, "%d archived %s not shown\n", archived, plural(archived, "task", "tasks"))
		}
		return
	}

	ordered := append([]api.Task(nil), tasks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})

	rows := &table{}
	rows.add("TASK", "PROJECT", "TITLE", "WORKFLOW", "AGENT", "ATTENTION", "RUNTIME", "FILES", "ELAPSED")
	for _, task := range ordered {
		if task.Workflow == "archived" {
			continue
		}
		rows.add(
			task.Key,
			task.ProjectID,
			task.Title,
			task.Workflow,
			process(task),
			task.Attention,
			runtimeState(task),
			changedFiles(task),
			since(task.CreatedAt, now),
		)
	}
	rows.render(out, "")

	if archived > 0 {
		printf(out, "\n%d archived %s not shown\n", archived, plural(archived, "task", "tasks"))
	}
}

// process is the observed state of the task's agent session.
//
// A task with no session reports nothing rather than "stopped": nothing has
// been started, which is a different answer.
func process(task api.Task) string {
	if task.Session == nil {
		return absent
	}
	return task.Session.Process
}

// runtimeState is the task's application runtime state. A task with no runtime
// is absent, because v0 starts application services only when asked.
func runtimeState(task api.Task) string {
	if task.Runtime == nil {
		return "absent"
	}
	return task.Runtime.State
}

// changedFiles totals what Feat last observed, or reports that it has not
// looked.
func changedFiles(task api.Task) string {
	total, observed := 0, false
	for _, binding := range task.Repositories {
		if binding.Observation == nil {
			continue
		}
		observed = true
		total += binding.Observation.ChangedFiles
	}
	if !observed {
		return absent
	}
	return strconv.Itoa(total)
}

// since renders how long ago a moment was, in the largest readable unit.
func since(moment, now time.Time) string {
	span := now.Sub(moment)
	if span < 0 {
		span = 0
	}
	switch {
	case span < time.Minute:
		return strconv.Itoa(int(span.Seconds())) + "s"
	case span < time.Hour:
		return strconv.Itoa(int(span.Minutes())) + "m"
	case span < 24*time.Hour:
		return strconv.Itoa(int(span.Hours())) + "h"
	default:
		return strconv.Itoa(int(span.Hours())/24) + "d"
	}
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}
