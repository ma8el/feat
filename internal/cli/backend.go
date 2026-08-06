package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/ui"
)

// fallbackEditor is used when the environment names none.
//
// FR-REV-003 makes $EDITOR the default; a machine that sets neither $VISUAL nor
// $EDITOR still needs something that exists, and vi is specified by POSIX.
const fallbackEditor = "vi"

// backend adapts the daemon client to what the dashboard needs.
//
// It exists in internal/cli rather than in internal/ui because of the three
// commands it builds: native tmux attach, a task shell, and the user's editor.
// Constructing them here keeps os/exec out of the TUI, which is the boundary
// ADR-031 records, and puts the terminal-yielding commands beside the
// `feat attach` implementation they share (ADR-030).
type backend struct {
	client *client.Client
	env    paths.Environment
}

var _ ui.Backend = (*backend)(nil)

func (b *backend) Projects(ctx context.Context) ([]api.Project, error) {
	return b.client.Projects(ctx)
}

func (b *backend) Tasks(ctx context.Context) ([]api.Task, error) {
	return b.client.Tasks(ctx)
}

func (b *backend) Events(ctx context.Context, handle func(api.Event) error) error {
	return b.client.Events(ctx, handle)
}

func (b *backend) CreateDraft(ctx context.Context, request api.CreateDraft) (api.Task, error) {
	return b.client.CreateDraft(ctx, request)
}

func (b *backend) UpdateDraft(ctx context.Context, id string, request api.UpdateDraft) (api.Task, error) {
	return b.client.UpdateDraft(ctx, id, request)
}

func (b *backend) PlanDraft(ctx context.Context, id string) (api.DraftPlan, error) {
	return b.client.PlanDraft(ctx, id)
}

func (b *backend) LaunchDraft(ctx context.Context, id, fingerprint string) (api.Task, error) {
	return b.client.LaunchDraft(ctx, id, fingerprint)
}

func (b *backend) CancelDraft(ctx context.Context, id string) (api.Task, error) {
	return b.client.CancelDraft(ctx, id)
}

// AttachCommand resolves the task's live terminal and returns the native tmux
// client for it.
func (b *backend) AttachCommand(ctx context.Context, id string) (tea.ExecCommand, error) {
	info, err := b.client.AttachInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	return b.tmux(ctx, info)
}

// ShellCommand opens or finds the task's shell pane and returns the native tmux
// client for it.
func (b *backend) ShellCommand(ctx context.Context, id string) (tea.ExecCommand, error) {
	info, err := b.client.Shell(ctx, id)
	if err != nil {
		return nil, err
	}
	return b.tmux(ctx, info)
}

func (b *backend) tmux(ctx context.Context, info api.AttachInfo) (tea.ExecCommand, error) {
	command, err := attachCommand(ctx, info)
	if err != nil {
		return nil, err
	}
	return execCommand{command}, nil
}

// EditorCommand opens a file in the user's editor.
//
// $VISUAL is preferred over $EDITOR where both are set, which is the
// convention: $VISUAL names the full-screen editor and $EDITOR may be a line
// editor. The value may carry arguments, as `code -w` does, so it is split
// rather than treated as one program name.
func (b *backend) EditorCommand(path string) (tea.ExecCommand, error) {
	fields := strings.Fields(b.editor())
	if len(fields) == 0 {
		return nil, errors.New("no editor is configured; set $EDITOR")
	}

	// #nosec G204 -- the program comes from the user's own environment and the
	// file is one Feat just created; every part is a separate argument and
	// nothing reaches a shell.
	command := exec.Command(fields[0], append(fields[1:], path)...)
	return execCommand{command}, nil
}

// editor returns the editor command from the environment.
func (b *backend) editor() string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(b.lookup(name)); value != "" {
			return value
		}
	}
	return fallbackEditor
}

func (b *backend) lookup(name string) string {
	if b.env.Getenv != nil {
		return b.env.Getenv(name)
	}
	return os.Getenv(name)
}

// execCommand adapts an exec.Cmd to what Bubble Tea runs while it has released
// the terminal.
type execCommand struct{ command *exec.Cmd }

func (c execCommand) Run() error {
	if err := c.command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("%s exited with status %d", c.command.Path, exit.ExitCode())
		}
		return fmt.Errorf("running %s: %w", c.command.Path, err)
	}
	return nil
}

func (c execCommand) SetStdin(reader io.Reader) {
	if c.command.Stdin == nil {
		c.command.Stdin = reader
	}
}

func (c execCommand) SetStdout(writer io.Writer) {
	if c.command.Stdout == nil {
		c.command.Stdout = writer
	}
}

func (c execCommand) SetStderr(writer io.Writer) {
	if c.command.Stderr == nil {
		c.command.Stderr = writer
	}
}
