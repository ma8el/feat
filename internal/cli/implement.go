package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/brief"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/ui"
)

const implementLong = `Prepare and launch a task.

You choose the project, write the task brief, and select which repositories the
task may read and write. Feat then fetches, resolves each repository's base
commit, and proposes the branches and worktree paths it would create.

An invocation that says what the task is creates it, terminal or not: pass
--project and either --brief or --file. One that does not opens the preparation
screen with whatever it was given, which is what --tui asks for when the
invocation would otherwise have been enough.

Where the brief comes from is a question preparation asks, after the project and
before the brief itself: type it here, compose it from one of the project's
tickets, or import a Markdown file you have already written. --ticket and --file
are answers to that question, so a run that passes one skips it. --brief is the
text itself, and --file - reads it from standard input.

--ticket runs the project's configured tracker command and matches the reference
it names against the ones that command printed. Feat composes a brief from the
ticket into the field you are editing, and what you confirm is that composed
brief rather than the ticket it came from. That reading is the whole control, so
--ticket needs a terminal and never creates a task on its own.

The review step also chooses how the session begins: straight into the work, or
planning first and waiting for your approval before anything is edited. Press p
to change it, or pass --plan to arrive with it already on. Either way the plan is
approved in the task's own terminal, between you and the agent.

Nothing is created until you confirm that proposal, and confirming creates
exactly what was displayed: a draft that changed in between is refused rather
than launched. On the screen the confirmation is a key press; without one it is
the invocation, and --dry-run prints the same proposal and creates nothing.`

// implementOptions is what one run of `feat implement` was asked for.
//
// It is a value rather than eight arguments because every field is optional and
// most of them are strings, so a call site that swapped two would compile.
type implementOptions struct {
	project      string
	brief        string
	file         string
	ticket       string
	repositories []string
	planFirst    bool
	dryRun       bool
	tui          bool
	asJSON       bool
}

// complete reports whether the invocation says what the task is.
//
// A project and a brief are the whole of it. Everything else Feat resolves —
// which repositories, from which commits, onto which branches and paths — and
// --dry-run is how a caller reads that before it exists.
//
// --ticket is deliberately not a brief for this purpose. It names a document
// somebody else may have written, which becomes the agent's instructions, and
// what a user approves is the brief Feat composed from it rather than the
// ticket (ADR-070). There is nobody to read it in a pipe.
func (o implementOptions) complete() bool {
	return o.project != "" && (o.brief != "" || o.file != "")
}

func newImplementCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "implement",
		Short: "Prepare and launch a task",
		Long:  implementLong,
		Args:  checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := readImplementFlags(cmd)
			if err != nil {
				return err
			}
			if err := opts.check(env.interactive); err != nil {
				return err
			}

			// Read before anything else is arranged, and read once. The screen
			// starts from the same brief a headless run would create from, so a
			// --brief the user takes to the screen with --tui arrives in the
			// field rather than being dropped on the way.
			document, source, err := opts.readBrief(cmd.InOrStdin())
			if err != nil {
				return err
			}

			layout, err := env.resolve()
			if err != nil {
				return err
			}
			// Preparation writes state, so it is an explicit mutation and does
			// not start a daemon, for the reason ADR-028 gives for
			// `feat project add`.
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}

			caller := client.New(layout.Socket)
			defer caller.Close()

			if opts.complete() && !opts.tui {
				return createTask(cmd, caller, opts, document, source)
			}

			return ui.Run(cmd.Context(), ui.Options{
				Backend:   &backend{client: caller, env: env},
				Daemon:    describeBackend(cmd, caller, layout),
				Project:   opts.project,
				Prepare:   true,
				Brief:     document,
				Source:    source,
				Ticket:    opts.ticket,
				PlanFirst: opts.planFirst,
			})
		},
	}
	cmd.Flags().String("file", "", "read the task brief from a Markdown file, or from standard input with -")
	cmd.Flags().String("brief", "", "the task brief itself")
	cmd.Flags().String("project", "", "prepare the task in this project")
	// The reference is the tracker's own, exactly as its command printed it.
	// Feat parses no part of one: it re-runs the command and matches (ADR-071).
	cmd.Flags().String("ticket", "", "compose the brief from this ticket of the project's tracker")
	// It presets the review step's toggle rather than deciding anything: the
	// step still appears, the key still moves it, and nothing is created until
	// the user confirms what is on the screen.
	cmd.Flags().Bool("plan", false, "start the agent in plan mode, so it proposes a plan before it changes anything")
	cmd.Flags().StringArray("repository", nil,
		"select one repository as <id> or <id>:read_write|read_only, replacing the project's defaults")
	cmd.Flags().Bool("dry-run", false, "print what would be created and create nothing")
	cmd.Flags().Bool("tui", false, "open the preparation screen even when the invocation says what the task is")
	addJSONFlag(cmd)
	return cmd
}

// readImplementFlags collects the invocation.
func readImplementFlags(cmd *cobra.Command) (implementOptions, error) {
	var opts implementOptions
	var err error

	for _, read := range []struct {
		flag   string
		assign func(string)
	}{
		{"project", func(v string) { opts.project = v }},
		{"brief", func(v string) { opts.brief = v }},
		{"file", func(v string) { opts.file = v }},
		{"ticket", func(v string) { opts.ticket = v }},
	} {
		value, readErr := cmd.Flags().GetString(read.flag)
		if readErr != nil {
			return implementOptions{}, readErr
		}
		read.assign(value)
	}

	for _, read := range []struct {
		flag   string
		assign func(bool)
	}{
		{"plan", func(v bool) { opts.planFirst = v }},
		{"dry-run", func(v bool) { opts.dryRun = v }},
		{"tui", func(v bool) { opts.tui = v }},
	} {
		value, readErr := cmd.Flags().GetBool(read.flag)
		if readErr != nil {
			return implementOptions{}, readErr
		}
		read.assign(value)
	}

	if opts.repositories, err = cmd.Flags().GetStringArray("repository"); err != nil {
		return implementOptions{}, err
	}
	opts.asJSON = wantsJSON(cmd)
	return opts, nil
}

// check refuses an invocation that cannot mean what it says.
//
// Every refusal here is a combination where carrying on would either discard
// something the user asked for or quietly ignore it. A flag that does nothing is
// worse than a flag that is refused: the user believes it worked.
func (o implementOptions) check(interactive bool) error {
	switch {
	case o.ticket != "" && o.file != "",
		o.ticket != "" && o.brief != "":
		// A brief comes from one source. Composing from a ticket over a
		// document the user chose to import would silently discard one of the
		// two things they asked for.
		return errors.New(
			"a task brief comes from one source: pass --brief, --file, or --ticket, and not two of them")
	case o.brief != "" && o.file != "":
		return errors.New(
			"a task brief comes from one source: pass --brief or --file, not both")
	case o.tui && o.dryRun:
		return errors.New(
			"--dry-run prints what would be created, and --tui opens the screen that creates it; pass one")
	}

	if o.project != "" {
		// Validated here so that a malformed identifier is a usage error rather
		// than something the daemon has to explain.
		if err := domain.ProjectID(o.project).Validate(); err != nil {
			return err
		}
	}

	if o.tui && !interactive {
		return errors.New("--tui asks for the preparation screen, and there is no terminal to open it in")
	}

	// The flags that only describe a headless run. Accepting one and opening a
	// screen instead would answer a different question from the one asked.
	if !o.complete() || o.tui {
		switch {
		case o.file == "-":
			// The preparation screen reads keys from standard input, so a run
			// that opens it cannot also read a brief from there.
			return errors.New("--file - reads the task brief from standard input, " +
				"which the preparation screen reads your keys from; pass --project to create the task instead")
		case o.dryRun:
			return errors.New("--dry-run needs the task the run would create: " +
				"pass --project and either --brief or --file")
		case o.asJSON:
			return errors.New("--json describes a document this run does not print: " +
				"pass --project and either --brief or --file, or read the result with `feat task list --json`")
		case len(o.repositories) > 0:
			return errors.New("--repository selects repositories for a task this run creates: " +
				"pass --project and either --brief or --file, or choose them on the preparation screen")
		}
	}

	if interactive || o.complete() {
		return nil
	}
	if o.ticket != "" {
		// The composed brief is what the user approves, not the ticket it came
		// from (ADR-070), and a pipe has nobody to read it. The unattended path
		// from a ticket is a v0 non-goal rather than a missing flag.
		return errors.New("`feat implement --ticket` needs a terminal, because what you approve is the brief " +
			"Feat composed from the ticket rather than the ticket itself; " +
			"pass --brief or --file to create a task without one")
	}
	return errors.New("`feat implement` needs a terminal to compose a task; " +
		"to create one without a terminal, pass --project and either --brief or --file")
}

// readBrief returns the brief this run was given, and where it came from.
//
// It answers for both paths, so that a brief the user takes to the screen with
// --tui is the same brief a headless run would have created from. A run that
// was given none gets an empty document, which is the screen's starting state.
//
// A brief typed into a flag and a brief piped in are both text the caller
// supplied, so both are recorded as a prompt. Only a file has a path worth
// recording, and the client is what reads it: no caller-supplied filesystem
// path crosses the socket (ADR-028).
func (o implementOptions) readBrief(stdin io.Reader) (string, api.Source, error) {
	if o.brief != "" {
		return o.brief, api.Source{Kind: string(domain.SourcePrompt)}, nil
	}
	if o.file == "-" {
		text, err := brief.ReadFrom(stdin)
		if err != nil {
			return "", api.Source{}, err
		}
		if strings.TrimSpace(text) == "" {
			return "", api.Source{}, errors.New("standard input held no task brief")
		}
		return text, api.Source{Kind: string(domain.SourcePrompt)}, nil
	}
	return readImportedBrief(o.file)
}

// createTask records a draft, resolves it, and launches what was resolved.
//
// The confirmation is the invocation. What the fingerprint defends against is a
// draft that changed between the plan and the launch, and here it still can:
// the value carried back is the one this run was just given, so a draft that
// moved underneath it is refused rather than launched (ADR-031, ADR-099). It is
// the same confirmation `daemon.PrepareTask` supplies itself.
//
// Nothing that was created is undone: a launch that fails leaves a failed task
// whose record names a superset of what exists (ADR-029). A failure before the
// launch has created nothing, and its draft is archived rather than left in the
// list — see discardDraft for why this path differs from the screen's.
func createTask(cmd *cobra.Command, caller drafter, opts implementOptions, document string, source api.Source) error {
	ctx := cmd.Context()

	selection, err := resolveSelection(ctx, caller, opts)
	if err != nil {
		return err
	}

	title := brief.Title(document)
	draft, err := caller.CreateDraft(ctx, api.CreateDraft{
		ProjectID: opts.project,
		Title:     title,
		Brief:     document,
		Source:    source,
	})
	if err != nil {
		return err
	}

	if len(selection) > 0 {
		// The update replaces the draft's whole editable shape, so the title
		// and brief go back unchanged with the selection.
		if _, err := caller.UpdateDraft(ctx, draft.ID, api.UpdateDraft{
			Title:        title,
			Brief:        document,
			Repositories: selection,
		}); err != nil {
			return discardDraft(ctx, caller, draft, err)
		}
	}

	plan, err := caller.PlanDraft(ctx, draft.ID)
	if err != nil {
		return discardDraft(ctx, caller, draft, err)
	}

	if opts.dryRun {
		// Discarded before anything is printed, so that a run which cannot
		// discard it reports that instead of printing a document and then
		// failing. The record is archived rather than removed, which is what
		// cancelling a draft has always done.
		if _, err := caller.CancelDraft(ctx, draft.ID); err != nil {
			return fmt.Errorf("the draft was resolved and could not be discarded, "+
				"so task %s is still a draft: %w", draft.Key, err)
		}
		if opts.asJSON {
			return emitJSON(cmd.OutOrStdout(), plan)
		}
		printProposal(cmd.OutOrStdout(), plan)
		return nil
	}

	task, err := caller.LaunchDraft(ctx, draft.ID,
		api.Confirmation{Fingerprint: plan.Fingerprint, PlanFirst: opts.planFirst})
	if err != nil {
		return fmt.Errorf("task %s was not launched: %w", draft.Key, err)
	}

	if opts.asJSON {
		return emitJSON(cmd.OutOrStdout(), task)
	}
	printCreatedTask(cmd.OutOrStdout(), task, plan.Notes)
	return nil
}

// drafter is what creating a task needs from the daemon.
//
// It is an interface so that the whole flow can be tested without a socket,
// which is what `publisher` exists for in the command beside this one. What it
// is mostly made of is the order of four requests and what happens when one of
// them fails.
type drafter interface {
	Project(ctx context.Context, id string) (api.Project, error)
	CreateDraft(ctx context.Context, request api.CreateDraft) (api.Task, error)
	UpdateDraft(ctx context.Context, id string, request api.UpdateDraft) (api.Task, error)
	PlanDraft(ctx context.Context, id string) (api.DraftPlan, error)
	LaunchDraft(ctx context.Context, id string, confirmation api.Confirmation) (api.Task, error)
	CancelDraft(ctx context.Context, id string) (api.Task, error)
}

// discardDraft reports a failure before anything was created, and archives the
// draft it happened on.
//
// The daemon leaves such a draft alone, because on the preparation screen it is
// a task the user can still change and resolve again. There is no screen here:
// the run is over, and a run that tried again would record a second draft rather
// than edit this one. So this archives it, which is what cancelling a draft has
// always done — the record survives and says what was started and abandoned,
// and `feat task list` counts it rather than showing it.
//
// Nothing is destroyed by archiving one. Planning creates no branch, no
// worktree, and no container, so a draft that failed to resolve owns nothing at
// all.
func discardDraft(ctx context.Context, caller drafter, draft api.Task, cause error) error {
	if _, err := caller.CancelDraft(ctx, draft.ID); err != nil {
		return fmt.Errorf("task %s was not launched: %w\n"+
			"\tIts draft could not be discarded either (%v), so it is still there: "+
			"open it with `feat implement --tui`", draft.Key, cause, err)
	}
	return fmt.Errorf("no task was created: %w", cause)
}

// resolveSelection turns --repository into the selection a draft records.
//
// A bare identifier takes the repository's configured default access, which is
// what the project already says about it. The three defaults that are not a task
// access — selectable, stable_read_only, and omitted — are a choice the project
// deliberately left open, so this refuses rather than making it.
func resolveSelection(ctx context.Context, caller drafter, opts implementOptions) ([]api.DraftRepository, error) {
	if len(opts.repositories) == 0 {
		return nil, nil
	}

	type request struct {
		id     string
		access string
	}
	requests := make([]request, 0, len(opts.repositories))
	bare := false
	for _, value := range opts.repositories {
		id, access, explicit := strings.Cut(value, ":")
		if err := domain.RepositoryID(id).Validate(); err != nil {
			return nil, err
		}
		if explicit && !domain.TaskAccess(access).Valid() {
			return nil, fmt.Errorf("--repository %s asks for %q, and a task reads a repository "+
				"as read_write or read_only", id, access)
		}
		if !explicit {
			bare = true
		}
		requests = append(requests, request{id: id, access: access})
	}

	defaults := map[string]string{}
	if bare {
		project, err := caller.Project(ctx, opts.project)
		if err != nil {
			return nil, err
		}
		for _, repository := range project.Repositories {
			defaults[repository.ID] = repository.DefaultAccess
		}
	}

	selection := make([]api.DraftRepository, 0, len(requests))
	for _, asked := range requests {
		access := asked.access
		if access == "" {
			configured, known := defaults[asked.id]
			if !known {
				return nil, fmt.Errorf("project %s has no repository %s", opts.project, asked.id)
			}
			if !domain.TaskAccess(configured).Valid() {
				return nil, fmt.Errorf("project %s configures repository %s as %s, which is a choice it leaves "+
					"to the task; ask for %s:read_write or %s:read_only",
					opts.project, asked.id, configured, asked.id, asked.id)
			}
			access = configured
		}
		selection = append(selection, api.DraftRepository{RepositoryID: asked.id, Access: access})
	}
	return selection, nil
}

// printProposal renders what a dry run would have created.
//
// It does not lead with the task's key, because the draft it was resolved on has
// been discarded: what this describes is a task, not a record that exists.
func printProposal(out io.Writer, plan api.DraftPlan) {
	printf(out, "would create in project %s  %s\n", plan.Task.ProjectID, plan.Task.Title)
	renderTaskRepositories(out, plan.Task)
	for _, note := range plan.Notes {
		printf(out, "\nnote: %s\n", note)
	}
	printf(out, "\nnothing was created\n")
}

// printCreatedTask renders a launched task.
func printCreatedTask(out io.Writer, task api.Task, notes []string) {
	printf(out, "created task %s  %s\n", task.Key, task.Title)
	renderTaskRepositories(out, task)
	for _, note := range notes {
		printf(out, "\nnote: %s\n", note)
	}
	printf(out, "\nattach with `feat attach %s`\n", task.Key)
}

// renderTaskRepositories prints what each repository of a task starts from.
//
// The base commit is there because it is the one value in a task that never
// moves again, and the worktree path because it is where the work will be.
func renderTaskRepositories(out io.Writer, task api.Task) {
	if len(task.Repositories) == 0 {
		return
	}
	rows := &table{}
	rows.add("REPOSITORY", "ACCESS", "BASE", "BRANCH", "WORKTREE")
	for _, binding := range task.Repositories {
		rows.add(
			binding.RepositoryID,
			binding.Access,
			valueOr(short(binding.BaseCommit), absent),
			valueOr(binding.Branch, absent),
			valueOr(binding.WorktreePath, absent),
		)
	}
	printf(out, "\n")
	rows.render(out, "")
}

// readImportedBrief reads an imported Markdown brief.
//
// The client reads it, not the daemon: the file is one the user named, and no
// caller-supplied filesystem path crosses the socket (ADR-028). What is sent is
// the content, with the path recorded only so that the task can say where its
// brief came from.
//
// The reading itself is internal/brief's, because the import screen applies the
// same rule and internal/ui cannot import this package (ADR-083). What stays
// here is the source the flag implies: the package that reads the file knows
// nothing about the DTO, so each caller builds its own.
func readImportedBrief(file string) (string, api.Source, error) {
	if file == "" {
		return "", api.Source{Kind: string(domain.SourcePrompt)}, nil
	}

	text, path, err := brief.Read(file)
	if err != nil {
		return "", api.Source{}, err
	}
	return text, api.Source{Kind: string(domain.SourceMarkdown), Reference: path}, nil
}

// describeBackend reports which daemon the dashboard is talking to.
func describeBackend(cmd *cobra.Command, caller *client.Client, layout paths.Layout) ui.Daemon {
	described := ui.Daemon{Socket: layout.Socket}
	if health, err := caller.Health(cmd.Context()); err == nil {
		described.Version = health.Daemon.Version
	}
	return described
}
