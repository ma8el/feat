package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
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
	// Events delivers daemon state changes until the context ends. Handle
	// returning an error ends the subscription.
	Events(ctx context.Context, handle func(api.Event) error) error
	// Resources returns the daemon's most recent resource sample. It is read
	// separately from tasks because it is an observation nobody stores, with its
	// own time and its own failure mode (FR-UI-005).
	Resources(ctx context.Context) (api.ResourceReport, error)

	// CreateDraft records a new task draft and creates nothing else.
	CreateDraft(ctx context.Context, request api.CreateDraft) (api.Task, error)
	// UpdateDraft replaces a draft's title, brief, and repository selection.
	UpdateDraft(ctx context.Context, id string, request api.UpdateDraft) (api.Task, error)
	// PlanDraft resolves the draft's bases and proposes its branches and
	// worktree paths, creating nothing.
	PlanDraft(ctx context.Context, id string) (api.DraftPlan, error)
	// LaunchDraft confirms a draft, carrying the fingerprint of the plan the
	// user was shown.
	LaunchDraft(ctx context.Context, id, fingerprint string) (api.Task, error)
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
}

// Daemon identifies the daemon the dashboard is talking to, for the footer.
type Daemon struct {
	// Version is the daemon's build version.
	Version string
	// Socket is where it is listening.
	Socket string
}
