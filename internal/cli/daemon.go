package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
)

// foregroundCommand is the argument vector that makes this binary run a daemon
// in the foreground. `feat daemon start` spawns it, and a later launchd or
// systemd unit invokes it directly (ADR-027).
func foregroundCommand(level string) []string {
	return []string{"daemon", "run", "--log-level", level}
}

// printf writes one line of command output.
//
// A failed write to standard output is not something a command can act on, and
// reporting it would need another write to the stream that just failed, so the
// error is dropped deliberately here rather than at each call.
func printf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

func newDaemonCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Control the local daemon",
		Long: `The daemon owns orchestration, reconciliation, and event publication, and is
the only reader and writer of persistent state. Opening the dashboard starts it
automatically; these commands control it explicitly.

It listens on a Unix-domain socket owned by the current user. There is no
network listener.`,
	}
	cmd.AddCommand(
		newDaemonStartCommand(env),
		newDaemonStopCommand(env),
		newDaemonRestartCommand(env),
		newDaemonStatusCommand(env),
		newDaemonRunCommand(env),
	)
	return cmd
}

func newDaemonStartCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local daemon",
		Long: `Start the daemon in the background and wait until it answers.

Starting a daemon while one is already running is not an error: the running one
is reported instead. A socket left behind by a daemon that is gone is reclaimed
after checking that nothing is serving on it.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			level, err := logLevel(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if status := daemon.Inspect(layout); status.Running() {
				if status.HasEndpoint {
					printf(out, "a feat daemon is already running: pid %d on %s\n",
						status.Endpoint.PID, status.Endpoint.Socket)
				} else {
					printf(out, "a feat daemon is already answering on %s\n", layout.Socket)
				}
				return nil
			}

			endpoint, err := daemon.Spawn(cmd.Context(), layout, daemon.SpawnOptions{
				Args: foregroundCommand(level.String()),
			})
			if err != nil {
				return err
			}

			printf(out, "daemon started: pid %d on %s\n", endpoint.PID, endpoint.Socket)
			printf(out, "log: %s\n", layout.LogFile())
			return nil
		},
	}
	addLogLevelFlag(cmd)
	return cmd
}

func newDaemonStopCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the local daemon",
		Long: `Ask the daemon to shut down and wait until it stops answering.

Shutdown is graceful: in-flight requests finish and event streams are closed.
Tasks, worktrees, containers, and tmux sessions are left exactly as they are;
stopping the daemon is not a cleanup.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}

			endpoint, err := daemon.Stop(cmd.Context(), layout, 0)
			if err != nil {
				if errors.Is(err, daemon.ErrNotRunning) {
					return &NotRunningError{Detail: err.Error(), Socket: layout.Socket}
				}
				return err
			}

			printf(cmd.OutOrStdout(), "daemon stopped: pid %d\n", endpoint.PID)
			return nil
		},
	}
}

func newDaemonRestartCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Stop the local daemon and start it again",
		Long: `Stop the running daemon, wait until it is gone, and start a new one.

It is the two commands in one, and it is safe as one because stopping already
waits: ` + "`feat daemon stop`" + ` returns when the socket has stopped answering and the
process has exited, so the new daemon never races the old one for the runtime
directory.

Restarting when nothing is running starts a daemon, rather than failing. That
makes it the command to reach for when you do not know or do not care.

It is how a changed settings file takes effect, because settings are read once at
startup — but it is the daemon's own verb rather than the settings', and a new
build or a daemon you want reconciled from scratch are the same command.

What a restart costs: tasks, worktrees, branches, containers, volumes, and tmux
sessions are untouched, and the daemon reconciles what it finds when it comes
back. A task whose configured checks were running loses that run — it returns to
its review request, and reconciliation reports it with the action to run them
again — and a task that was waiting out its notification grace period is not
notified about that silence.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			level, err := logLevel(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// A daemon that is not running is not an obstacle to starting one.
			// Anything else is: a record this could not read is a question about
			// ownership, and spawning a second daemon is not the answer to it.
			switch stopped, err := daemon.Stop(cmd.Context(), layout, 0); {
			case err == nil:
				printf(out, "daemon stopped: pid %d\n", stopped.PID)
			case errors.Is(err, daemon.ErrNotRunning):
				printf(out, "no daemon was running\n")
			default:
				return err
			}

			endpoint, err := daemon.Spawn(cmd.Context(), layout, daemon.SpawnOptions{
				Args: foregroundCommand(level.String()),
			})
			if err != nil {
				// The old one is already gone, so this left the machine without a
				// daemon. Said plainly: the user asked for a restart and has to
				// know they now have none.
				return fmt.Errorf("the daemon was stopped and the new one did not start, "+
					"so none is running now: %w", err)
			}

			printf(out, "daemon started: pid %d on %s\n", endpoint.PID, endpoint.Socket)
			printf(out, "log: %s\n", layout.LogFile())
			return nil
		},
	}
	addLogLevelFlag(cmd)
	return cmd
}

func newDaemonStatusCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report local daemon status",
		Long: `Report whether a daemon is running, which build it is, and what it owns.

The exit code is 0 when a daemon is running and 4 when none is, so a script can
tell the two apart without reading the output.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			status := daemon.Inspect(layout)
			if !status.Running() {
				printf(out, "%s\n", status.Diagnose())
				printf(out, "socket: %s\n", layout.Socket)
				printf(out, "log:    %s\n", layout.LogFile())
				return &NotRunningError{Socket: layout.Socket}
			}

			health, err := client.New(layout.Socket).Health(cmd.Context())
			if err != nil {
				// Something is accepting connections but not serving the API.
				// That is worth reporting exactly, not smoothing over.
				return fmt.Errorf("a process is listening on %s but did not answer: %w", layout.Socket, err)
			}

			fields := [][2]string{
				{"status", health.Status},
				{"api", health.APIVersion},
				{"version", health.Daemon.Version},
				{"commit", health.Daemon.Commit},
				{"platform", health.Daemon.Platform},
				{"pid", fmt.Sprint(health.Daemon.PID)},
				{"uptime", uptime(health.Daemon.StartedAt)},
				{"socket", health.Daemon.Socket},
				{"state", health.State.Directory},
				{"projects", fmt.Sprint(health.State.Projects)},
				{"log", layout.LogFile()},
			}
			for _, field := range fields {
				printf(out, "%-9s %s\n", field[0], field[1])
			}
			if health.Detail != "" {
				printf(out, "detail:   %s\n", health.Detail)
			}
			if note := buildSkew(env, health.Daemon.Version); note != "" {
				printf(out, "\n%s\n", note)
			}
			return nil
		},
	}
}

func newDaemonRunCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground",
		Long: `Run the daemon in this process until it is interrupted.

This is the process that ` + "`feat daemon start`" + ` spawns, and the command a
service manager should invoke.`,
		// Hidden from help because it is not the way a person starts a daemon,
		// but still pinned by the command-surface golden file: hidden is not the
		// same as unpublished (ADR-027).
		Hidden: true,
		Args:   checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			level, err := logLevel(cmd)
			if err != nil {
				return err
			}

			// A person running the daemon in a terminal expects to see it
			// working. A daemon started in the background has its standard
			// error redirected into the same log file, so writing there too
			// would record everything twice.
			log, err := daemon.OpenLog(layout, level, terminalStderr())
			if err != nil {
				return err
			}
			defer func() { _ = log.Close() }()

			return daemon.Run(cmd.Context(), daemon.Options{
				Layout: layout,
				Build: daemon.Build{
					Version:   env.build.Version,
					Commit:    env.build.Commit,
					GoVersion: env.build.GoVersion,
					Platform:  env.build.Platform(),
				},
				Logger: log.Logger,
			})
		},
	}
	addLogLevelFlag(cmd)
	return cmd
}

// addLogLevelFlag adds the shared log-level flag. `daemon start` carries it too,
// because it passes the value to the daemon it spawns.
func addLogLevelFlag(cmd *cobra.Command) {
	cmd.Flags().String("log-level", "info", "daemon log level: debug, info, warn, or error")
}

// logLevel reads the log-level flag.
func logLevel(cmd *cobra.Command) (slog.Level, error) {
	value, err := cmd.Flags().GetString("log-level")
	if err != nil {
		return 0, err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, &usageError{
			cmd: cmd,
			err: fmt.Errorf("--log-level must be debug, info, warn, or error, not %q", value),
		}
	}
	return level, nil
}

// uptime renders how long a daemon has been running, at second resolution.
func uptime(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "unknown"
	}
	elapsed := time.Since(startedAt).Round(time.Second)
	if elapsed < 0 {
		// The daemon's clock and this one disagree; reporting a negative uptime
		// would be worse than admitting it.
		return "unknown"
	}
	return elapsed.String()
}

// buildSkew reports a running daemon built from a different version than the
// client asking, which during development is the difference between "my change
// does nothing" and "my change is not running yet".
func buildSkew(env *environment, daemonVersion string) string {
	if daemonVersion == "" || daemonVersion == env.build.Version {
		return ""
	}
	return fmt.Sprintf(
		"this client is %s and the running daemon is %s;\nrun `feat daemon stop` and `feat daemon start` to run the new build",
		env.build.Version, daemonVersion)
}

// daemonSummary is what the root command learned about the daemon.
//
// The failure is carried separately from the line because the two are read by
// different callers and cut to different lengths. The line is one row of a
// health summary; startErr is why a daemon could not be started, and a spawn
// that failed says so in a quoted daemon log, which is several lines and all of
// them worth showing.
type daemonSummary struct {
	// line is the one-line rendering for the health summary.
	line string
	// socket is where a daemon would be listening.
	socket string
	// startErr is why starting one failed, when one was tried and could not be.
	startErr error
}

// describeDaemon renders the daemon's state for the dashboard, starting one when
// the dashboard is interactive.
//
// Opening the dashboard starts the daemon it needs (ADR-008). A non-interactive
// `feat`, in a pipe or in CI, reports what it observes instead: printing a
// summary should not start a background process as a side effect.
func (e *environment) describeDaemon(cmd *cobra.Command) daemonSummary {
	layout, err := e.resolve()
	if err != nil {
		return daemonSummary{line: "unavailable: " + err.Error()}
	}
	summary := daemonSummary{socket: layout.Socket}

	status := daemon.Inspect(layout)
	if !status.Running() {
		if !e.interactive {
			summary.line = status.Diagnose()
			return summary
		}
		if _, err := daemon.Spawn(cmd.Context(), layout, daemon.SpawnOptions{
			Args: foregroundCommand(slog.LevelInfo.String()),
		}); err != nil {
			summary.line = "could not be started: " + firstLine(err.Error())
			summary.startErr = err
			return summary
		}
	}

	health, err := client.New(layout.Socket).Health(cmd.Context())
	if err != nil {
		summary.line = "not answering: " + firstLine(err.Error())
		return summary
	}

	summary.line = fmt.Sprintf("%s (pid %d, up %s)",
		health.Status, health.Daemon.PID, uptime(health.Daemon.StartedAt))
	if health.Daemon.Version != e.build.Version {
		summary.line += ", build " + health.Daemon.Version
	}
	summary.socket = health.Daemon.Socket
	return summary
}

// firstLine keeps a multi-line failure, such as a quoted daemon log, from
// breaking a single-line summary. The full error is available from
// `feat daemon status`.
func firstLine(message string) string {
	if index := strings.IndexByte(message, '\n'); index >= 0 {
		return message[:index] + " (see feat daemon status)"
	}
	return message
}
