package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
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
	// Environment is what a leading "~" in project configuration expands
	// against, and where a default such as $EDITOR is read from. A nil value
	// uses the process environment.
	Environment *paths.Environment
	// Runner runs the commands `feat doctor` uses to inspect the host. A nil
	// value runs them on the real host; a test supplies its own so that its
	// result does not depend on which tools happen to be installed.
	Runner project.Runner
	// Attacher yields this process's terminal to native tmux. A nil value runs
	// the installed tmux client; tests inject one so they never take over the
	// test process's terminal.
	Attacher TerminalAttacher
	// Now supplies the current time, which elapsed columns are measured
	// against. A nil value reads the wall clock; a test supplies its own so
	// that its output does not change between runs.
	Now func() time.Time
}

// environment is what commands need from the process: where Feat's directories
// are, whose home directory a path expands against, whether a full-screen TUI
// is possible, and which build this is.
type environment struct {
	build       version.Info
	interactive bool
	layout      *paths.Layout
	process     *paths.Environment
	runner      project.Runner
	attacher    TerminalAttacher
	now         func() time.Time
}

// clock returns the time source elapsed columns are measured against.
func (e *environment) clock() func() time.Time {
	if e.now != nil {
		return e.now
	}
	return time.Now
}

// resolve returns the path layout, resolving it from the environment unless one
// was supplied.
func (e *environment) resolve() (paths.Layout, error) {
	if e.layout != nil {
		return *e.layout, nil
	}
	current, err := e.current()
	if err != nil {
		return paths.Layout{}, err
	}
	return paths.Resolve(current)
}

// current returns the process environment paths are resolved against.
func (e *environment) current() (paths.Environment, error) {
	if e.process != nil {
		return *e.process, nil
	}
	return paths.Current()
}

// project returns everything a project command needs: where the configuration
// lives, and what its paths and defaults resolve against.
func (e *environment) project() (paths.Layout, config.Options, error) {
	layout, err := e.resolve()
	if err != nil {
		return paths.Layout{}, config.Options{}, err
	}
	current, err := e.current()
	if err != nil {
		return paths.Layout{}, config.Options{}, err
	}
	return layout, config.Options{Env: current, StateDir: layout.State}, nil
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
		process:     opts.Environment,
		runner:      opts.Runner,
		attacher:    opts.Attacher,
		now:         opts.Now,
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
			if !opts.Interactive {
				// A run with no terminal reports what it observed rather than
				// opening a screen nobody can read (ADR-027).
				return ui.RunHealth(cmd.Context(), ui.Health{
					Version:   env.build.Version,
					Commit:    env.build.Commit,
					GoVersion: env.build.GoVersion,
					Platform:  env.build.Platform(),
					Daemon:    daemonLine,
					Socket:    socket,
				}, cmd.OutOrStdout(), false)
			}

			layout, err := env.resolve()
			if err != nil {
				return err
			}
			if status := daemon.Inspect(layout); !status.Running() {
				// describeDaemon starts one; if there still is none, the
				// dashboard has nothing to show and says so.
				return &NotRunningError{Socket: layout.Socket}
			}

			caller := client.New(layout.Socket)
			defer caller.Close()
			current, err := env.current()
			if err != nil {
				return err
			}

			return ui.Run(cmd.Context(), ui.Options{
				Backend: &backend{client: caller, env: current},
				Daemon:  ui.Daemon{Version: env.build.Version, Socket: layout.Socket},
			})
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
		newImplementCommand(env),
		newProjectCommand(env),
		newTaskCommand(env),
		newAttachCommand(env),
		newReviewCommand(),
		newRuntimeCommand(env),
		newCleanupCommand(),
		newDoctorCommand(env),
		newDaemonCommand(env),
		newVersionCommand(),
	)

	return root
}

func newReviewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "review <task>",
		Short: "Review a task's changes against its recorded base commits",
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE:  notImplemented(11, "review and external commands"),
	}
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
