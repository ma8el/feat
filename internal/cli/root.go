package cli

import (
	"fmt"
	"io"
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
	// Input is where a command that asks questions reads its answers. A nil
	// value reads the process's standard input, which is what the real binary
	// wants; a test supplies a script so that a conversation can be driven
	// without a terminal.
	Input io.Reader
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

	if opts.Input != nil {
		// Cobra hands this to every command through InOrStdin, so the whole
		// tree reads the same script a test supplied.
		root.SetIn(opts.Input)
	}

	// Shell completion is delivered by slice 14. Hiding the generated command
	// keeps `feat --help` equal to the documented v0 command surface.
	root.CompletionOptions.DisableDefaultCmd = true

	// Applies to the whole tree: cobra walks to the parent when a command has
	// no flag error function of its own.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &usageError{cmd: cmd, err: err}
	})

	// Attaching and reviewing are typed all day, so they keep the short names
	// they had before ADR-040 moved them under `feat task`.
	attach, review := newAttachCommand(env), newReviewCommand(env)

	root.AddCommand(
		newImplementCommand(env),
		newProjectCommand(env),
		newTaskCommand(env, attach, review),
		aliasOf(attach, "feat task attach"),
		aliasOf(review, "feat task review"),
		newRuntimeCommand(env),
		newDoctorCommand(env),
		newDaemonCommand(env),
		newVersionCommand(),
	)

	return root
}

// aliasOf gives a command a second name at the top level.
//
// The alias holds the same RunE value as the command it stands for, so there is
// one implementation under two names rather than two that drift. Cobra sets a
// parent in AddCommand, so the same command value cannot hold both positions and
// a second one has to exist; what that second one must not have is a body of its
// own.
//
// It is hidden for the reason `feat daemon run` is (ADR-027): `feat --help`
// stays equal to the documented command surface, while the golden file, which
// walks hidden commands, still pins it.
func aliasOf(canonical *cobra.Command, path string) *cobra.Command {
	return &cobra.Command{
		Use:    canonical.Use,
		Short:  canonical.Short,
		Long:   "This is " + path + " under a shorter name.\n\n" + canonical.Long,
		Args:   canonical.Args,
		Hidden: true,
		RunE:   canonical.RunE,
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
