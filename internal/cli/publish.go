package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
)

const publishLong = `Open one merge request per changed repository of a task.

Feat publishes from this machine, with the authentication you already have here.
The agent never holds a provider credential and never opens a merge request: what
it writes is a draft — a title and a description for each repository it changed —
and what you read and edit is what is sent.

Publishing shows what it would do, opens the draft in your editor, and asks once.
It then pushes each task branch and opens each merge request one repository at a
time, recording every result before the next one begins. Nothing is undone: if
the third of five fails, the first two are open, the fourth and fifth are still
attempted, and publishing again skips whatever already has a merge request.

The push runs with hooks disabled, because a task's repositories share their
hooks with your own checkout and the agent can write them. Where a repository has
a pre-push hook, this says so before you approve.`

func newPublishCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "publish <task>",
		Short: "Open a merge request per changed repository",
		Long:  withTaskArgument(publishLong),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Publishing is interactive because reading the draft is the whole
			// control: agent-authored text bound for somewhere durable is read
			// by a person before it is sent, and there is nobody to read it in a
			// pipe (ADR-070).
			if !env.interactive {
				return errors.New("`feat task publish` needs a terminal, because nothing is sent until you " +
					"have read and approved what the agent wrote")
			}

			layout, err := env.resolve()
			if err != nil {
				return err
			}
			// An explicit action against a running daemon rather than a reason
			// to start one, which is the rule every mutation follows (ADR-028).
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}

			caller := client.New(layout.Socket)
			defer caller.Close()

			return publish(cmd, caller, env, args[0])
		},
	}
}

// publisher is what publishing needs from the daemon.
//
// It is an interface so that the document, the editor, and the confirmation can
// be tested without a socket: what this command is mostly made of is the
// round trip from what the daemon composed, through a file the user edits, to
// what is sent back.
type publisher interface {
	PlanPublication(ctx context.Context, id string) (api.PublicationStatus, error)
	ApplyPublication(ctx context.Context, id string, request api.PublishRequest) (api.PublicationStatus, error)
}

// publish runs the whole flow: plan, read, edit, confirm, apply.
func publish(cmd *cobra.Command, caller publisher, env *environment, task string) error {
	out := cmd.OutOrStdout()

	plan, err := caller.PlanPublication(cmd.Context(), task)
	if err != nil {
		return err
	}
	printPublicationPlan(out, plan)

	if len(api.OfferedDrafts(plan.Drafts)) == 0 {
		// Either the task has nothing to publish, or everything it has is
		// already on a forge or describes a commit that is no longer current.
		// The table above says which, per repository, and none of the three is
		// something a user can edit their way out of: there is no document to
		// open, and saying they removed one would blame them for it.
		printf(out, "\nnothing to publish\n")
		return nil
	}

	approved, err := editPublication(cmd, plan, env)
	if err != nil {
		return err
	}
	if len(approved) == 0 {
		printf(out, "\nevery repository was removed from the draft, so nothing was published\n")
		return nil
	}
	if stale := api.StaleApprovals(plan.Drafts, approved); len(stale) > 0 {
		// Reported and never resolved. Feat does not re-compose a draft on the
		// user's behalf, because they would be publishing words they never read
		// (ADR-031's rule for a changed draft, ADR-070's for a stale one). The
		// document does not offer a stale repository, so this is what a document
		// that disagrees with its plan reaches — before the confirmation rather
		// than after it, so the answer is not spent on something that cannot
		// happen.
		return fmt.Errorf("the agent's draft for %s describes a commit that is no longer current, "+
			"so nothing was published. Ask the agent for a fresh draft with the commit each one describes",
			strings.Join(stale, ", "))
	}

	printPublicationApproval(out, approved)
	ask := &prompter{in: bufio.NewReader(cmd.InOrStdin()), out: out}
	confirmed, err := ask.confirm(fmt.Sprintf("open %d merge request(s) now?", len(approved)), false)
	if err != nil {
		return err
	}
	if !confirmed {
		printf(out, "nothing was published\n")
		return nil
	}

	status, err := caller.ApplyPublication(cmd.Context(), task, api.PublishRequest{Repositories: approved})
	if err != nil {
		return err
	}
	printPublicationResult(out, status)
	return nil
}

// printPublicationPlan renders what publishing would do.
func printPublicationPlan(out io.Writer, plan api.PublicationStatus) {
	printf(out, "%s  %s\n", plan.Task.Key, plan.Task.Title)

	if len(plan.Drafts) > 0 {
		rows := &table{}
		rows.add("REPOSITORY", "FORGE", "BRANCH", "INTO", "COMMIT", "STATE")
		for _, draft := range plan.Drafts {
			state := "to publish"
			switch {
			case draft.Published != nil:
				state = "published as " + draft.Published.URL
			case draft.Stale:
				state = "stale draft"
			case strings.TrimSpace(draft.Title) == "":
				state = "no title yet"
			}
			rows.add(draft.RepositoryID, draft.Forge, draft.Branch, draft.BaseBranch,
				short(draft.Commit), state)
		}
		printf(out, "\n")
		rows.render(out, "")
	}
	printPublicationRecord(out, plan.Task.Publication)
	for _, note := range plan.Notes {
		printf(out, "\nnote: %s\n", note)
	}
}

// printPublicationApproval says what is about to be sent, in the words that will
// be sent.
func printPublicationApproval(out io.Writer, approved []api.ApprovedPublication) {
	printf(out, "\nabout to open:\n")
	for _, one := range approved {
		printf(out, "  %s  %s\n", one.RepositoryID, one.Title)
	}
}

// printPublicationResult renders what a publication produced.
func printPublicationResult(out io.Writer, status api.PublicationStatus) {
	printPublicationRecord(out, status.Task.Publication)
	for _, note := range status.Notes {
		printf(out, "\nnote: %s\n", note)
	}
}

// printPublicationRecord renders what the task has recorded, which after an
// interruption is also what is left to do.
func printPublicationRecord(out io.Writer, publication *api.Publication) {
	if publication == nil || len(publication.Repositories) == 0 {
		return
	}

	rows := &table{}
	rows.add("REPOSITORY", "STATE", "MERGE REQUEST")
	for _, entry := range publication.Repositories {
		detail := absent
		switch {
		case entry.Request != nil:
			detail = entry.Request.URL
		case entry.Failure != "":
			detail = entry.Failure
		case entry.State == "planned":
			detail = "not attempted"
		}
		rows.add(entry.RepositoryID, entry.State, detail)
	}
	printf(out, "\n")
	rows.render(out, "")
}

// publicationDraft is one draft document on its way through an editor.
//
// It is what both clients use. The file, the editor command, and the read-back
// are the same in a terminal and on the dashboard, and the only difference is
// who runs the editor: this process, which has the terminal, or Bubble Tea,
// which has to release it first. One document, one parser, and no second way for
// the two clients to disagree about what the user approved.
//
// The directory is this process's, in a place only this user can read, and Close
// removes it: it holds the description of somebody's change, which is theirs and
// belongs nowhere durable. A caller that never closes — a process killed while
// the editor is still open — leaves it to the system, as it leaves any editor's
// own temporary file.
type publicationDraft struct {
	directory string
	path      string
	command   *exec.Cmd
	drafts    []api.PublicationDraft
}

// newPublicationDraft writes the document and builds the editor that opens it.
func newPublicationDraft(plan api.PublicationStatus, env *environment) (*publicationDraft, error) {
	directory, err := os.MkdirTemp("", "feat-publish-")
	if err != nil {
		return nil, fmt.Errorf("preparing the publication draft: %w", err)
	}

	draft := &publicationDraft{
		directory: directory,
		// Named from the task's own key rather than from what the caller was
		// given, which may be a full identifier or a key depending on what the
		// user typed.
		path:   filepath.Join(directory, "publication-"+publicationSlug(plan.Task.Key)+".md"),
		drafts: plan.Drafts,
	}
	if err := os.WriteFile(draft.path, []byte(publicationDocument(plan)), 0o600); err != nil {
		draft.Close()
		return nil, fmt.Errorf("writing the publication draft: %w", err)
	}

	command, err := documentEditor(plan.Editor, env, draft.path)
	if err != nil {
		draft.Close()
		return nil, err
	}
	draft.command = command
	return draft, nil
}

// Read returns what the user approved, in the words they left in the file.
func (d *publicationDraft) Read() ([]api.ApprovedPublication, error) {
	edited, err := os.ReadFile(d.path) // #nosec G304 -- the path is one newPublicationDraft created
	if err != nil {
		return nil, fmt.Errorf("reading the edited publication draft: %w", err)
	}
	return readPublicationDocument(string(edited), d.drafts)
}

// Close removes the document.
func (d *publicationDraft) Close() { _ = os.RemoveAll(d.directory) }

// editPublication opens the draft in the user's editor and returns what they
// approved.
//
// This process runs the editor and waits: `feat task publish` holds the terminal
// already, where the dashboard has to hand it over and be told afterwards.
func editPublication(
	cmd *cobra.Command, plan api.PublicationStatus, env *environment,
) ([]api.ApprovedPublication, error) {
	draft, err := newPublicationDraft(plan, env)
	if err != nil {
		return nil, err
	}
	defer draft.Close()

	editor := draft.command
	editor.Stdin, editor.Stdout, editor.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := editor.Run(); err != nil {
		return nil, fmt.Errorf("the editor did not finish: %w", err)
	}
	return draft.Read()
}

// documentEditor builds the process that opens one document.
//
// The configured editor keeps its own flags — `code -w` has to stay `code -w`,
// or the editor returns before the user has typed anything — and the document is
// appended as the thing to open. A machine that configures none falls back to
// this process's own environment, which is where $EDITOR is and where the daemon
// cannot look (FR-REV-003).
//
// It is shared by the publication draft and the settings file, which are the two
// documents Feat asks somebody to edit.
func documentEditor(configured api.EditorCommand, env *environment, path string) (*exec.Cmd, error) {
	program, arguments := configured.Program, configured.Arguments
	if strings.TrimSpace(program) == "" {
		fields := strings.Fields(environmentEditor(env))
		if len(fields) == 0 {
			return nil, errors.New("no editor is configured; set $EDITOR, or configure review.editor.command")
		}
		program, arguments = fields[0], fields[1:]
	}
	if strings.HasPrefix(program, "-") {
		return nil, fmt.Errorf("the editor %q would be read as an option", program)
	}

	// #nosec G204 -- the program is the machine's own configuration or the
	// user's own $EDITOR, every argument is one vector element, and the file is
	// one this process just created or already owns; nothing reaches a shell.
	return exec.Command(program, append(append([]string(nil), arguments...), path)...), nil
}

// environmentEditor returns the editor this process's environment names.
func environmentEditor(env *environment) string {
	lookup := os.Getenv
	if current, err := env.current(); err == nil && current.Getenv != nil {
		lookup = current.Getenv
	}
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(lookup(name)); value != "" {
			return value
		}
	}
	return fallbackEditor
}

// publicationFenceWidth is the narrowest section marker a document uses.
const publicationFenceWidth = 3

// publicationMarker reports the repository a line names, if the line is one of
// this document's section markers.
//
// The marker is the only structure the document has: everything under it is the
// words, exactly as they will be sent. What decides a marker is therefore the
// fence the document was written with rather than the shape alone, because a
// description is prose and prose can contain the shape.
func publicationMarker(line, fence string) (string, bool) {
	id, found := strings.CutPrefix(line, fence+" ")
	if !found {
		return "", false
	}
	id, found = strings.CutSuffix(id, " "+fence)
	if !found || !publicationName(id) {
		return "", false
	}
	return id, true
}

// publicationName reports whether a marker names something that could be a
// repository, so that ordinary prose between two runs of "=" stays prose.
func publicationName(id string) bool {
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return id != ""
}

// publicationFence is the marker width a plan's document uses.
//
// It widens until no line of any draft is one, the way a Markdown code fence
// widens around a snippet containing backticks. The agent wrote those lines and
// they can carry anything it read, including a line shaped exactly like a
// marker; a fixed marker would let that line become structure, which is the one
// thing the words must never become — it would cut a description short at the
// injected line, refuse the whole publication because a repository then appears
// twice, or manufacture a section for a repository the user never approved. The
// fence makes all three unreachable from words alone.
//
// The reader computes it from the same drafts the writer did, so the document
// does not have to declare it and an edited document cannot lie about it.
func publicationFence(drafts []api.PublicationDraft) string {
	fence := strings.Repeat("=", publicationFenceWidth)
	for publicationMarked(drafts, fence) {
		fence += "="
	}
	return fence
}

// publicationMarked reports whether any words a plan composed contain a line
// this fence would read as a marker.
func publicationMarked(drafts []api.PublicationDraft, fence string) bool {
	for _, draft := range drafts {
		for _, words := range []string{draft.Title, draft.Body} {
			for _, line := range strings.Split(words, "\n") {
				if _, marker := publicationMarker(strings.TrimRight(line, "\r"), fence); marker {
					return true
				}
			}
		}
	}
	return false
}

// publicationDocument renders the draft the user edits.
func publicationDocument(plan api.PublicationStatus) string {
	var b strings.Builder
	fence := publicationFence(plan.Drafts)

	fmt.Fprintf(&b, "# Publication for %s — %s\n", plan.Task.Key, plan.Task.Title)
	b.WriteString("#\n")
	b.WriteString("# Feat opens one merge request per section below, from this machine, with your own\n")
	b.WriteString("# credentials. The title and the description were written by the agent: read them\n")
	b.WriteString("# before you approve, because they can carry anything the agent read.\n")
	b.WriteString("#\n")
	b.WriteString("# In each section the line under the marker is the title and everything below it\n")
	b.WriteString("# is the description, sent exactly as it stands — including lines beginning with\n")
	b.WriteString("# \"#\", which are Markdown headings there rather than comments. Only these lines\n")
	fmt.Fprintf(&b, "# above the first \"%s\" marker are comments, and only a line of exactly that\n", fence)
	b.WriteString("# width starts a section: a description containing one of its own is description.\n")
	b.WriteString("#\n")
	b.WriteString("# A section whose title line is left empty is refused rather than published.\n")
	b.WriteString("# Delete a whole section to leave that repository unpublished.\n")

	// What is left out is what editing cannot change: a repository already on the
	// forge, where saving different words would suggest they replace what was
	// sent, and one whose draft is stale, which is refused for the commit it
	// describes however it is rewritten. Both are named in the plan the user read
	// before this opened, with what to do about them.
	for _, draft := range api.OfferedDrafts(plan.Drafts) {
		fmt.Fprintf(&b, "\n%s\n", publicationSection(fence, draft.RepositoryID))
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(draft.Title))
		if body := strings.TrimRight(draft.Body, "\n"); body != "" {
			b.WriteString(body + "\n")
		}
	}
	return b.String()
}

// publicationSection renders one repository's marker line.
func publicationSection(fence, id string) string {
	return fence + " " + id + " " + fence
}

// readPublicationDocument reads the edited draft back.
//
// What it returns is what the user approved, word for word, together with the
// commit the plan composed each section against — so that the daemon can refuse
// a repository whose branch moved while the editor was open.
//
// The title is the line under the marker rather than the first non-empty line of
// the section. A section can arrive with its title slot empty — the agent wrote
// no draft for that repository, and the plan said so — and the forgiving rule
// would then promote whatever stands below it: the agent's first sentence, or
// the ticket line Feat itself added, sent as a merge request title with the
// description missing the line it took. A slot that is left empty is refused
// instead, which is what the plan already told the user would happen.
func readPublicationDocument(document string, drafts []api.PublicationDraft) ([]api.ApprovedPublication, error) {
	known := make(map[string]api.PublicationDraft, len(drafts))
	for _, draft := range drafts {
		known[draft.RepositoryID] = draft
	}
	fence := publicationFence(drafts)

	var (
		approved []api.ApprovedPublication
		current  *api.ApprovedPublication
		titled   bool
		body     []string
		seen     = make(map[string]bool, len(drafts))
	)
	flush := func() {
		if current == nil {
			return
		}
		current.Body = strings.TrimSpace(strings.Join(body, "\n"))
		approved = append(approved, *current)
		current, titled, body = nil, false, nil
	}

	for _, line := range strings.Split(document, "\n") {
		if id, marker := publicationMarker(strings.TrimRight(line, "\r"), fence); marker {
			flush()
			draft, planned := known[id]
			if !planned {
				return nil, fmt.Errorf("the draft names repository %q, which is not one this publication "+
					"planned", id)
			}
			if seen[id] {
				return nil, fmt.Errorf("the draft has two sections for repository %q, and a publication "+
					"opens one merge request per repository", id)
			}
			seen[id] = true
			current = &api.ApprovedPublication{RepositoryID: id, Commit: draft.Commit}
			continue
		}
		if current == nil {
			// Before the first marker: the header, which is comments.
			continue
		}
		if !titled {
			current.Title, titled = strings.TrimSpace(line), true
			continue
		}
		body = append(body, line)
	}
	flush()

	for _, one := range approved {
		if one.Title == "" {
			return nil, fmt.Errorf("repository %s has no title, and a merge request needs one. "+
				"Write one on the line under %q, or delete the section to leave it unpublished",
				one.RepositoryID, publicationSection(fence, one.RepositoryID))
		}
	}
	return approved, nil
}

// publicationSlug reduces a task reference to something safe in a file name.
func publicationSlug(task string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, task)
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "task"
	}
	return slug
}
