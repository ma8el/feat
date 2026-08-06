package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
)

var (
	attachSessionPattern = regexp.MustCompile(`^\$[0-9]+$`)
	attachWindowPattern  = regexp.MustCompile(`^@[0-9]+$`)
	attachPanePattern    = regexp.MustCompile(`^%[0-9]+$`)
)

// TerminalAttacher attaches this process's streams to a native task terminal.
// It lives in the CLI rather than internal/tmux because attachment must inherit
// the client's terminal and may last until the user detaches (ADR-030).
type TerminalAttacher interface {
	Attach(ctx context.Context, info api.AttachInfo, stdin io.Reader, stdout, stderr io.Writer) error
}

type hostAttacher struct{}

// attachCommand builds the native tmux client for one resolved target.
//
// It is shared by `feat attach` and by the dashboard's attach and shell
// actions, so that there is one place where a target becomes a command and one
// place where that target is validated.
func attachCommand(ctx context.Context, info api.AttachInfo) (*exec.Cmd, error) {
	if err := validateAttachInfo(info); err != nil {
		return nil, err
	}

	// tmux accepts a full stable target for attach. No name or numeric index is
	// involved, and selecting the pane as part of the target makes `feat attach`
	// return to the agent even after an on-demand shell was active.
	target := info.Session + ":" + info.Window + "." + info.Pane
	// #nosec G204 -- tmux is a fixed executable; every dynamic target component
	// is validated above and passed as its own argument, never through a shell.
	command := exec.CommandContext(ctx, "tmux", "-S", info.Socket, "attach-session", "-t", target)
	command.Env = outsideTmux(command.Environ())
	return command, nil
}

func (hostAttacher) Attach(ctx context.Context, info api.AttachInfo, stdin io.Reader, stdout, stderr io.Writer) error {
	command, err := attachCommand(ctx, info)
	if err != nil {
		return err
	}
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr

	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("native tmux attach exited with status %d", exit.ExitCode())
		}
		return fmt.Errorf("starting native tmux attach: %w", err)
	}
	return nil
}

func newAttachCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <task>",
		Short: "Attach to a task's agent terminal",
		Long: `Resolve the task's tagged terminal through the daemon and temporarily yield
this terminal to native tmux. Detach with the user's normal tmux binding to
return to Feat. Window names and indexes are not used as identity.`,
		Args: checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}

			caller := client.New(layout.Socket)
			defer caller.Close()
			info, err := caller.AttachInfo(cmd.Context(), args[0])
			if err != nil {
				if errors.Is(err, client.ErrDaemonNotRunning) {
					return &NotRunningError{Socket: layout.Socket}
				}
				return err
			}

			attacher := env.attacher
			if attacher == nil {
				attacher = hostAttacher{}
			}
			return attacher.Attach(cmd.Context(), info, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func validateAttachInfo(info api.AttachInfo) error {
	if !filepath.IsAbs(info.Socket) {
		return fmt.Errorf("the daemon returned non-absolute tmux socket %q", info.Socket)
	}
	for _, field := range []struct {
		name    string
		value   string
		pattern *regexp.Regexp
	}{
		{"session", info.Session, attachSessionPattern},
		{"window", info.Window, attachWindowPattern},
		{"pane", info.Pane, attachPanePattern},
	} {
		if !field.pattern.MatchString(field.value) {
			return fmt.Errorf("the daemon returned invalid tmux %s id %q", field.name, field.value)
		}
	}
	return nil
}

// outsideTmux removes the variables that make tmux reject an attach as a
// nested session. Feat's dedicated server is intentionally attachable from a
// dashboard running inside the user's ordinary tmux server.
func outsideTmux(environment []string) []string {
	out := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name == "TMUX" || name == "TMUX_PANE" {
			continue
		}
		out = append(out, entry)
	}
	return out
}
