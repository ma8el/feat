package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/project"
	"github.com/ma8el/feat/internal/ui"
	"github.com/ma8el/feat/internal/wizard"
)

// fallbackEditor is used when the environment names none.
//
// FR-REV-003 makes $EDITOR the default; a machine that sets neither $VISUAL nor
// $EDITOR still needs something that exists, and vi is specified by POSIX.
const fallbackEditor = "vi"

// backend adapts the daemon client to what the dashboard needs.
//
// It exists in internal/cli rather than in internal/ui because of the commands
// it builds: native tmux attach, a task shell, the user's editor, and the
// project wizard. Constructing them here keeps os/exec out of the TUI, which is
// the boundary ADR-031 records, and puts the terminal-yielding commands beside
// the `feat attach` implementation they share (ADR-030).
//
// It holds the process environment rather than the resolved paths, because the
// wizard needs what a project command needs — the configuration directory, what
// paths resolve against, and the runner it inspects checkouts with — and those
// are the same values `feat project init` asks the environment for.
type backend struct {
	client *client.Client
	env    *environment
}

var _ ui.Backend = (*backend)(nil)

func (b *backend) Projects(ctx context.Context) ([]api.Project, error) {
	return b.client.Projects(ctx)
}

func (b *backend) Tasks(ctx context.Context) ([]api.Task, error) {
	return b.client.Tasks(ctx)
}

// Tickets runs the project's configured tracker command through the daemon.
//
// The daemon runs it rather than this process, because that is where every
// credentialed provider call is made and where the answer becomes a task
// (ADR-070). `feat doctor` is the exception and says why: it validates a
// project's configuration before a daemon exists.
func (b *backend) Tickets(ctx context.Context, project string) (api.TicketList, error) {
	return b.client.Tickets(ctx, project)
}

func (b *backend) Events(ctx context.Context, handle func(api.Event) error) error {
	return b.client.Events(ctx, handle)
}

func (b *backend) Resources(ctx context.Context) (api.ResourceReport, error) {
	return b.client.Resources(ctx)
}

// StartDaemon starts a daemon for a dashboard whose own has stopped answering.
//
// It is the same act that opening the dashboard performs (ADR-008), reached
// again from inside it. It lives here rather than in internal/ui for the reason
// the tmux attach and the editor do: this is where a process is started, and the
// TUI reaches the daemon over the socket rather than in process.
//
// A daemon that is already answering is left alone and reported as success. Two
// clients can be asking at once — the periodic read that discovered the absence
// and the key the user pressed — and Spawn's own answer to that race is that the
// loser's child exits reporting the winner; this is the cheaper half of it.
func (b *backend) StartDaemon(ctx context.Context) error {
	layout, err := b.env.resolve()
	if err != nil {
		return err
	}
	if status := daemon.Inspect(layout); status.Running() {
		return nil
	}
	_, err = daemon.Spawn(ctx, layout, daemon.SpawnOptions{
		Args: foregroundCommand(slog.LevelInfo.String()),
	})
	return err
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

// Runtime performs one manual application-runtime action.
func (b *backend) Runtime(ctx context.Context, id string, action api.RuntimeAction) (api.RuntimeStatus, error) {
	return b.client.Runtime(ctx, id, action)
}

// LogsCommand resolves the task's Compose logs command and returns it for the
// dashboard to run while it has released the terminal.
//
// The command is checked here, where every other command the TUI runs is built,
// so the TUI itself names no os/exec type and the rule keeping process execution
// in adapters stays mechanical (ADR-031).
func (b *backend) LogsCommand(ctx context.Context, id string) (tea.ExecCommand, error) {
	command, err := b.client.RuntimeLogs(ctx, id)
	if err != nil {
		return nil, err
	}
	process, err := logsCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	return execCommand{process}, nil
}

// Review performs one review action.
func (b *backend) Review(ctx context.Context, id string, action api.ReviewAction) (api.ReviewStatus, error) {
	return b.client.Review(ctx, id, action)
}

// CleanupPlan resolves what a task owns, removing nothing.
func (b *backend) CleanupPlan(ctx context.Context, id string) (api.CleanupPlan, error) {
	return b.client.CleanupPlan(ctx, id)
}

// Cleanup removes the classes a selection names.
func (b *backend) Cleanup(
	ctx context.Context, id string, selection api.CleanupSelection,
) (api.CleanupStatus, error) {
	return b.client.Cleanup(ctx, id, selection)
}

// Resume continues a task's recorded agent session.
func (b *backend) Resume(ctx context.Context, id string) (api.Task, error) {
	return b.client.Resume(ctx, id)
}

// Stop stops the environment a task's agent session runs in.
func (b *backend) Stop(ctx context.Context, id string) (api.Task, error) {
	return b.client.Stop(ctx, id)
}

// TerminalFrame asks the daemon for one rendered view of a task's pane.
func (b *backend) TerminalFrame(ctx context.Context, id string, view api.TerminalView) (api.TerminalFrame, error) {
	return b.client.TerminalFrame(ctx, id, view)
}

// SendTerminalInput delivers what the user typed to a task's pane.
func (b *backend) SendTerminalInput(ctx context.Context, id string, input api.TerminalInput) error {
	return b.client.SendTerminalInput(ctx, id, input)
}

// Reconciliation returns the daemon's most recent recovery pass.
func (b *backend) Reconciliation(ctx context.Context) (api.Reconciliation, error) {
	return b.client.Reconciliation(ctx)
}

// Reconcile asks the daemon to compare persisted state with the machine again.
func (b *backend) Reconcile(ctx context.Context) (api.Reconciliation, error) {
	return b.client.Reconcile(ctx)
}

// ReviewCommand builds the process for one of the project's own review tools.
//
// It is checked here, where every other command the TUI runs is built, so the
// TUI itself names no os/exec type (ADR-031) and there is one place where a
// command the daemon returned becomes a process.
func (b *backend) ReviewCommand(command api.ReviewCommand) (tea.ExecCommand, error) {
	process, err := reviewCommand(command)
	if err != nil {
		return nil, err
	}
	return execCommand{process}, nil
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

// PlanPublication composes what publishing a task would do, sending nothing.
func (b *backend) PlanPublication(ctx context.Context, id string) (api.PublicationStatus, error) {
	return b.client.PlanPublication(ctx, id)
}

// ApplyPublication opens one merge request per approved repository.
func (b *backend) ApplyPublication(
	ctx context.Context, id string, request api.PublishRequest,
) (api.PublicationStatus, error) {
	return b.client.ApplyPublication(ctx, id, request)
}

// EditPublication writes the draft of a plan and returns it on its way to the
// editor.
//
// It is the document `feat task publish` writes, through the same constructor:
// the file, the parser, and the read-back are shared, and what differs is that
// the dashboard has to release the terminal before the editor runs and is told
// afterwards. Closing it is the caller's, which for the dashboard is the
// callback Bubble Tea runs when the editor exits.
func (b *backend) EditPublication(plan api.PublicationStatus) (ui.PublicationEditor, error) {
	draft, err := newPublicationDraft(plan, b.env)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

var _ ui.PublicationEditor = (*publicationDraft)(nil)

// Command opens the draft in the user's editor.
//
// It is the one part of a publication draft that is Bubble Tea's, so it lives
// here beside the other commands the dashboard runs with the terminal released,
// rather than with the document the rest of the type is about (ADR-031).
func (d *publicationDraft) Command() tea.ExecCommand { return execCommand{d.command} }

// NewWizard builds the project wizard the dashboard asks its questions from.
//
// It is built here because of what it needs: the configuration directory, what
// paths resolve against, and a host that runs Git and reads Compose files. The
// dashboard has none of those and is not meant to — it drives the questions and
// draws them, and everything underneath them is this side of the interface
// (ADR-063).
func (b *backend) NewWizard() (*wizard.Wizard, error) {
	return b.env.wizard("")
}

// WriteProject writes the configuration the wizard composed.
//
// The dashboard asks for it rather than doing it, so that the exclusive create
// that protects an existing configuration is the same one `feat project init`
// writes through, in the same package, with no second implementation to keep
// honest.
func (b *backend) WriteProject(flow *wizard.Wizard) (string, error) {
	return flow.Write()
}

// Diagnose checks one project against this machine, and the machine itself.
//
// The checks run here, in the process the user is in front of, for the reason
// `feat doctor` does: diagnosis works before a daemon or a registration exists
// (ADR-028), so making it a daemon request would be a second implementation of
// the same checks — and the second one would answer about a different process's
// environment. What crosses to the dashboard is data, so the screen that draws
// it reaches no adapter (ADR-031, ADR-064).
func (b *backend) Diagnose(ctx context.Context, id string) (api.Diagnosis, error) {
	layout, options, err := b.env.project()
	if err != nil {
		return api.Diagnosis{}, err
	}

	report, err := project.Diagnose(ctx, project.Options{
		ConfigDir:  layout.ProjectConfigDir(),
		Resolve:    options,
		Runner:     b.env.runner,
		Projects:   projectList(id),
		Registered: b.registered(ctx),
	})
	if err != nil {
		return api.Diagnosis{}, err
	}
	return diagnosis(report), nil
}

// projectList limits a run to one project, or to every configured one.
func projectList(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

// registered answers whether a project is registered, from the daemon this
// dashboard is already talking to.
//
// A failed read reports every project as unregistered rather than failing the
// diagnosis: the daemon is one of the things being diagnosed.
func (b *backend) registered(ctx context.Context) func(string) bool {
	projects, err := b.client.Projects(ctx)
	if err != nil {
		return func(string) bool { return false }
	}
	known := make(map[string]bool, len(projects))
	for _, project := range projects {
		known[project.ID] = true
	}
	return func(id string) bool { return known[id] }
}

// diagnosis converts a report into what a screen draws.
func diagnosis(report project.Report) api.Diagnosis {
	converted := api.Diagnosis{
		Host:        findings(report.Host),
		Environment: "this terminal",
	}
	for _, checked := range report.Projects {
		converted.Projects = append(converted.Projects, api.ProjectDiagnosis{
			ID:       checked.ID,
			File:     checked.File,
			Findings: findings(checked.Findings),
		})
	}
	return converted
}

func findings(found []project.Finding) []api.Finding {
	converted := make([]api.Finding, 0, len(found))
	for _, finding := range found {
		converted = append(converted, api.Finding{
			Check:    finding.Check,
			Severity: string(finding.Severity),
			Summary:  finding.Summary,
			Action:   finding.Action,
		})
	}
	return converted
}

// RegisterProject registers a written project with the running daemon.
//
// It is the request `feat project add` makes, and it is made only when the user
// asks for it: a file on disk and a project the daemon knows about stay
// different things (ADR-028).
func (b *backend) RegisterProject(ctx context.Context, id string) (api.Registration, error) {
	return b.client.RegisterProject(ctx, id)
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
	// An environment that cannot be resolved is not a reason to have no editor:
	// the process's own is what a resolved one would have read anyway.
	current, err := b.env.current()
	if err != nil || current.Getenv == nil {
		return os.Getenv(name)
	}
	return current.Getenv(name)
}

// execCommand adapts an exec.Cmd to what Bubble Tea runs while it has released
// the terminal.
type execCommand struct{ command *exec.Cmd }

// interruptedExit is the status a program the user interrupted at the terminal
// exits with: the shell's convention of 128 plus SIGINT.
//
// Ctrl-C is how a user leaves `docker compose logs --follow`, and Compose exits
// 130 when they do. Reporting that as an error would put a failure banner on the
// dashboard for a key the user meant to press — the state that cries wolf on the
// ordinary path, which ADR-034 evidence 9 refused for a stopped container and
// which is the same mistake here (ADR-049).
const interruptedExit = 130

func (c execCommand) Run() error {
	if err := c.command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == interruptedExit || interruptedBySignal(exit) {
				return nil
			}
			return fmt.Errorf("%s exited with status %d", c.command.Path, exit.ExitCode())
		}
		return fmt.Errorf("running %s: %w", c.command.Path, err)
	}
	return nil
}

// interruptedBySignal reports whether the interrupt killed the program outright
// rather than being handled by it, which is what happens to a program that
// installs no handler of its own.
func interruptedBySignal(exit *exec.ExitError) bool {
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGINT
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
