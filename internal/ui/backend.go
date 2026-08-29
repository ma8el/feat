package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/wizard"
)

// Backend is everything the dashboard needs from the daemon.
//
// It is an interface declared here rather than a client passed in, for two
// reasons. The models can then be driven by a fake, so a screen's behaviour is
// testable without a socket, a daemon, or tmux. And the three things the TUI has
// to launch — native tmux attach, a task shell, and the user's editor — arrive
// as tea.ExecCommand values built elsewhere, so this package never names an
// os/exec type and the rule that keeps process execution in adapters stays
// mechanical (ADR-031).
type Backend interface {
	// Projects returns every registered project.
	Projects(ctx context.Context) ([]api.Project, error)
	// Tasks returns every task of every project, drafts included.
	Tasks(ctx context.Context) ([]api.Task, error)
	// Tickets runs a project's configured tracker command and returns the
	// tickets it printed.
	//
	// It is asked for on a key rather than when a screen opens: the command
	// reaches somebody's tracker, and a network call should follow a key the
	// user pressed (ADR-031, ADR-071).
	Tickets(ctx context.Context, project string) (api.TicketList, error)
	// Events delivers daemon state changes until the context ends. Handle
	// returning an error ends the subscription.
	Events(ctx context.Context, handle func(api.Event) error) error
	// Resources returns the daemon's most recent resource sample. It is read
	// separately from tasks because it is an observation nobody stores, with its
	// own time and its own failure mode (FR-UI-005).
	Resources(ctx context.Context) (api.ResourceReport, error)
	// StartDaemon starts a daemon and waits until it answers, so that a dashboard
	// whose daemon stopped can be repaired without being quit. It is the one
	// thing here that is not a request over the socket, and it is asked for
	// rather than done because starting a process belongs to an adapter
	// (ADR-031): internal/ui does not import internal/daemon.
	//
	// Nothing calls it on its own. Opening the dashboard starts a daemon
	// (ADR-008) and this is the same act offered again, after a user has been
	// asked and has said yes.
	StartDaemon(ctx context.Context) error

	// CreateDraft records a new task draft and creates nothing else.
	CreateDraft(ctx context.Context, request api.CreateDraft) (api.Task, error)
	// UpdateDraft replaces a draft's title, brief, and repository selection.
	UpdateDraft(ctx context.Context, id string, request api.UpdateDraft) (api.Task, error)
	// PlanDraft resolves the draft's bases and proposes its branches and
	// worktree paths, creating nothing.
	PlanDraft(ctx context.Context, id string) (api.DraftPlan, error)
	// LaunchDraft confirms a draft, carrying the fingerprint of the plan the
	// user was shown and the decisions they made on the review step.
	LaunchDraft(ctx context.Context, id string, confirmation api.Confirmation) (api.Task, error)
	// CancelDraft abandons a draft.
	CancelDraft(ctx context.Context, id string) (api.Task, error)

	// Runtime performs one manual application-runtime action and reports what it
	// observed. The dashboard never calls it on its own: v0 starts and stops
	// application services only when the user asks (FR-RUN-005).
	Runtime(ctx context.Context, id string, action api.RuntimeAction) (api.RuntimeStatus, error)
	// Review performs one review action and returns what the task's review
	// shows. Approving never stops or destroys anything; the offer to stop a
	// runtime is made in words (FR-REV-004).
	Review(ctx context.Context, id string, action api.ReviewAction) (api.ReviewStatus, error)
	// PlanPublication composes what publishing a task would do and sends
	// nothing. It is safe to reach with one key press for the reason CleanupPlan
	// is: what publishing would do is a question, and only what the user
	// approves is ever acted on.
	PlanPublication(ctx context.Context, id string) (api.PublicationStatus, error)
	// ApplyPublication opens one merge request per approved repository, carrying
	// the words the user read.
	ApplyPublication(ctx context.Context, id string, request api.PublishRequest) (api.PublicationStatus, error)
	// EditPublication writes the draft of a plan to a file of its own and
	// returns it on its way to the user's editor.
	//
	// The document is written here rather than in the dashboard for the reason
	// every terminal-yielding command is built here: the process belongs to an
	// adapter, and so does the file it opens (ADR-031).
	EditPublication(plan api.PublicationStatus) (PublicationEditor, error)
	// CleanupPlan resolves what a task owns and removes nothing, which is what
	// makes it safe to reach with one key press.
	CleanupPlan(ctx context.Context, id string) (api.CleanupPlan, error)
	// Cleanup removes exactly the classes a selection names, carrying the plan
	// token and the warnings the user accepted (FR-CLEAN-002, FR-CLEAN-003).
	Cleanup(ctx context.Context, id string, selection api.CleanupSelection) (api.CleanupStatus, error)
	// Resume continues a task's recorded agent session. Nothing but a user
	// reaches it: recovery is offered and never automatic (FR-STATE-004).
	Resume(ctx context.Context, id string) (api.Task, error)
	// Stop stops the environment a task's agent session runs in, keeping the
	// containers so that a resume starts the same ones again. It is the only
	// way to stop a task's agent short of removing it (ADR-057).
	Stop(ctx context.Context, id string) (api.Task, error)
	// Reconciliation returns the daemon's most recent recovery pass, so the
	// dashboard can show what needs attention. Reading it runs nothing.
	Reconciliation(ctx context.Context) (api.Reconciliation, error)
	// Reconcile asks the daemon to look again. It is separate from reading
	// because a pass asks the container runtime about every task: the periodic
	// refresh reads, and a new pass follows something that changed.
	Reconcile(ctx context.Context) (api.Reconciliation, error)

	// TerminalFrame asks for one rendered view of a task's pane, sized to the
	// region it will be drawn into. What comes back is what tmux drew: the
	// dashboard places it and reads nothing out of it but cell width (ADR-042).
	TerminalFrame(ctx context.Context, id string, view api.TerminalView) (api.TerminalFrame, error)
	// SendTerminalInput delivers keys or typed text to a task's pane, which is
	// what a focused pane does with the keyboard.
	SendTerminalInput(ctx context.Context, id string, input api.TerminalInput) error

	// AttachCommand yields this terminal to the task's agent pane until the
	// user detaches.
	AttachCommand(ctx context.Context, id string) (tea.ExecCommand, error)
	// ShellCommand opens the task's shell pane in this terminal.
	ShellCommand(ctx context.Context, id string) (tea.ExecCommand, error)
	// LogsCommand yields this terminal to the task's normal Compose logs.
	LogsCommand(ctx context.Context, id string) (tea.ExecCommand, error)
	// ReviewCommand yields this terminal to one of the project's own review
	// tools. The command was expanded and checked by the daemon and is checked
	// again before it runs.
	ReviewCommand(command api.ReviewCommand) (tea.ExecCommand, error)
	// EditorCommand opens a file or directory in the user's editor. It is the
	// fallback FR-REV-003 asks for when a project configures no editor command:
	// $EDITOR belongs to this process's environment, not the daemon's.
	EditorCommand(path string) (tea.ExecCommand, error)
	// NewWizard builds the questions that compose a project's configuration.
	// They are `feat project init`'s own questions, in their own package, so
	// that the dialog that draws them is a second asker rather than a second
	// wizard (ADR-063). Nothing is created by building one.
	NewWizard() (*wizard.Wizard, error)
	// WriteProject writes the configuration the wizard composed and returns the
	// file it wrote. It is asked for rather than done here because the dashboard
	// reaches no adapter of its own, and because the exclusive create that
	// refuses to replace an existing configuration must have one implementation.
	WriteProject(flow *wizard.Wizard) (string, error)
	// RegisterProject registers a written project with the daemon, which is what
	// `feat project add` does and is separate from writing the file.
	RegisterProject(ctx context.Context, id string) (api.Registration, error)
	// Diagnose checks one project against this machine and reports what it
	// found, changing nothing. An empty identifier checks every configured
	// project. It runs the checks `feat doctor` runs, in this process, so what
	// comes back describes the environment the dashboard is running in
	// (ADR-064).
	Diagnose(ctx context.Context, id string) (api.Diagnosis, error)
}

// Daemon identifies the daemon the dashboard is talking to, for the footer.
type Daemon struct {
	// Version is the daemon's build version.
	Version string
	// Socket is where it is listening.
	Socket string
}

// PublicationEditor is one publication draft on its way through the user's
// editor and back.
//
// It is an interface rather than a path because the dashboard reaches no
// filesystem of its own: what it does is run the command, read what came back,
// and let go. What the user had open is what is sent, which is the whole reason
// the draft goes through an editor at all (ADR-070).
type PublicationEditor interface {
	// Command opens the draft. It takes over this terminal until the editor
	// exits.
	Command() tea.ExecCommand
	// Read returns what the user approved, in the words they left in the file.
	Read() ([]api.ApprovedPublication, error)
	// Close removes the document. It holds the description of somebody's change
	// and belongs nowhere durable.
	Close()
}
