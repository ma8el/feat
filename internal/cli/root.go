package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/ui"
	"github.com/ma8el/feat/internal/version"
)

// Options configures the command tree.
type Options struct {
	// Interactive reports whether a full-screen TUI can be started. When
	// false, the root command renders a plain-text health summary instead.
	Interactive bool
	// Layout locates the daemon's socket, state, and runtime directories. A nil
	// value resolves them from the process environment, which is what the real
	// binary wants; a test supplies its own so that it never reaches the
	// developer's own daemon.
	Layout *paths.Layout
}

// environment is what commands need from the process: where Feat's directories
// are, whether a full-screen TUI is possible, and which build this is.
type environment struct {
	build       version.Info
	interactive bool
	layout      *paths.Layout
}

// resolve returns the path layout, resolving it from the environment unless one
// was supplied.
func (e *environment) resolve() (paths.Layout, error) {
	if e.layout != nil {
		return *e.layout, nil
	}
	current, err := paths.Current()
	if err != nil {
		return paths.Layout{}, err
	}
	return paths.Resolve(current)
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
	env := &environment{
		build:       version.Get(),
		interactive: opts.Interactive,
		layout:      opts.Layout,
	}

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
			// Opening the dashboard starts the daemon when none is running
			// (ADR-008); see describeDaemon for what a non-interactive run does
			// instead.
			daemonLine, socket := env.describeDaemon(cmd)
			return ui.RunHealth(cmd.Context(), ui.Health{
				Version:   env.build.Version,
				Commit:    env.build.Commit,
				GoVersion: env.build.GoVersion,
				Platform:  env.build.Platform(),
				Daemon:    daemonLine,
				Socket:    socket,
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
		newDaemonCommand(env),
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
