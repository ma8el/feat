package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/ui"
	"github.com/ma8el/feat/internal/version"
)

// Options configures the command tree.
type Options struct {
	// Interactive reports whether a full-screen TUI can be started. When
	// false, the root command renders a plain-text health summary instead.
	Interactive bool
}

const rootLong = `Feat is a terminal-native control plane for running feature work through
several coding-agent sessions in parallel.

One task owns one agent session, one set of Git worktrees, and one feature
environment. Feat orchestrates the native tools it composes; it does not
replace the coding agent, the terminal multiplexer, Git, or the container
runtime.

Running feat without a subcommand opens the dashboard.

This build is a pre-alpha skeleton. Subcommands report the implementation
slice that delivers them.`

// NewRootCommand builds the full feat command tree.
func NewRootCommand(opts Options) *cobra.Command {
	root := &cobra.Command{
		Use:   "feat",
		Short: "Terminal-native control plane for parallel coding-agent tasks",
		Long:  rootLong,
		Args:  checkArgs(cobra.NoArgs),

		// Errors are rendered by Execute so that exit codes stay under our
		// control and usage text is printed only for usage errors.
		SilenceUsage:  true,
		SilenceErrors: true,

		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			return ui.RunHealth(cmd.Context(), ui.Health{
				Version:   info.Version,
				Commit:    info.Commit,
				GoVersion: info.GoVersion,
				Platform:  info.Platform(),
				Daemon:    "not implemented (slice 2)",
			}, cmd.OutOrStdout(), opts.Interactive)
		},
	}

	// Shell completion is delivered by slice 14. Hiding the generated command
	// keeps `feat --help` equal to the documented v0 command surface.
	root.CompletionOptions.DisableDefaultCmd = true

	// Applies to the whole tree: cobra walks to the parent when a command has
	// no flag error function of its own.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &usageError{cmd: cmd, err: err}
	})

	root.AddCommand(
		newImplementCommand(),
		newProjectCommand(),
		newTaskCommand(),
		newAttachCommand(),
		newReviewCommand(),
		newRuntimeCommand(),
		newCleanupCommand(),
		newDoctorCommand(),
		newDaemonCommand(),
		newVersionCommand(),
	)

	return root
}

func newImplementCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "implement",
		Short: "Prepare and launch a task",
		Long: `Open task preparation. Feat does not start an agent until the final task
brief and repository selection are confirmed.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: notImplemented(6, "task preparation and initial TUI"),
	}
	cmd.Flags().String("file", "", "read the task brief from a Markdown file")
	return cmd
}

func newProjectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage registered projects",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "add",
			Short: "Register a project from its YAML configuration",
			Args:  checkArgs(cobra.NoArgs),
			RunE:  notImplemented(3, "YAML project configuration and doctor"),
		},
		&cobra.Command{
			Use:   "list",
			Short: "List registered projects",
			Args:  checkArgs(cobra.NoArgs),
			RunE:  notImplemented(3, "YAML project configuration and doctor"),
		},
		&cobra.Command{
			Use:   "show <project>",
			Short: "Show a project's resolved configuration",
			Args:  checkArgs(cobra.ExactArgs(1)),
			RunE:  notImplemented(3, "YAML project configuration and doctor"),
		},
	)
	return cmd
}

func newTaskCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Inspect tasks",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List tasks across all projects",
		Args:  checkArgs(cobra.NoArgs),
		RunE:  notImplemented(6, "task preparation and initial TUI"),
	})
	return cmd
}

func newAttachCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <task>",
		Short: "Attach to a task's agent terminal",
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE:  notImplemented(5, "tmux execution backend"),
	}
}

func newReviewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "review <task>",
		Short: "Review a task's changes against its recorded base commits",
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE:  notImplemented(11, "review and external commands"),
	}
}

func newRuntimeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Control a task's application runtime",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start <task>",
			Short: "Start the task's application services",
			Args:  checkArgs(cobra.ExactArgs(1)),
			RunE:  notImplemented(9, "manual application runtime"),
		},
		&cobra.Command{
			Use:   "stop <task>",
			Short: "Stop the task's application services",
			Args:  checkArgs(cobra.ExactArgs(1)),
			RunE:  notImplemented(9, "manual application runtime"),
		},
	)
	return cmd
}

func newCleanupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup <task>",
		Short: "Plan and execute removal of a task's resources",
		Long: `Produce an exact inventory of the resources a task owns and remove only the
resources explicitly selected. Volumes are retained unless chosen, and dirty or
unmerged work requires explicit confirmation.`,
		Args: checkArgs(cobra.ExactArgs(1)),
		RunE: notImplemented(12, "reconciliation and cleanup"),
	}
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose host prerequisites and project configuration",
		Args:  checkArgs(cobra.NoArgs),
		RunE:  notImplemented(3, "YAML project configuration and doctor"),
	}
}

func newDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Control the local daemon",
		Long: `The daemon owns orchestration, reconciliation, and event publication, and is
the only writer of persistent state. Opening the dashboard starts it
automatically; these commands control it explicitly.`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start the local daemon",
			Args:  checkArgs(cobra.NoArgs),
			RunE:  notImplemented(2, "daemon and local API"),
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the local daemon",
			Args:  checkArgs(cobra.NoArgs),
			RunE:  notImplemented(2, "daemon and local API"),
		},
		&cobra.Command{
			Use:   "status",
			Short: "Report local daemon status",
			Args:  checkArgs(cobra.NoArgs),
			RunE:  notImplemented(2, "daemon and local API"),
		},
	)
	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Args:  checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.Get())
			return err
		},
	}
}
