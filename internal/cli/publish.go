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
	"regexp"
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

	if len(plan.Drafts) == 0 {
		printf(out, "\nnothing to publish\n")
		return nil
	}
	if stale := stalePublications(plan.Drafts); len(stale) > 0 {
		// Reported and never resolved. Feat does not re-compose a draft on the
		// user's behalf, because they would be publishing words they never read
		// (ADR-031's rule for a changed draft, ADR-070's for a stale one).
		return fmt.Errorf("the agent's draft for %s describes a commit that is no longer current, "+
			"so nothing was published. Ask the agent for a fresh draft with the commit each one describes",
			strings.Join(stale, ", "))
	}

	document, err := editPublication(cmd, plan, env, task)
	if err != nil {
		return err
	}
	approved, err := readPublicationDocument(document, plan.Drafts)
	if err != nil {
		return err
	}
	if len(approved) == 0 {
		printf(out, "\nevery repository was removed from the draft, so nothing was published\n")
		return nil
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

// stalePublications names the repositories whose draft describes another commit.
func stalePublications(drafts []api.PublicationDraft) []string {
	var stale []string
	for _, draft := range drafts {
		if draft.Stale {
			stale = append(stale, draft.RepositoryID)
		}
	}
	return stale
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
	printPublicationRecord(out, plan.Publication)
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
	printPublicationRecord(out, status.Publication)
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

// editPublication writes the draft, opens it, and returns what came back.
//
// The file is this process's, in a directory only this user can read, and it is
// removed afterwards: it holds the description of a change, which is the user's
// and belongs nowhere durable.
func editPublication(
	cmd *cobra.Command, plan api.PublicationStatus, env *environment, task string,
) (string, error) {
	directory, err := os.MkdirTemp("", "feat-publish-")
	if err != nil {
		return "", fmt.Errorf("preparing the publication draft: %w", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()

	path := filepath.Join(directory, "publication-"+publicationSlug(task)+".md")
	if err := os.WriteFile(path, []byte(publicationDocument(plan)), 0o600); err != nil {
		return "", fmt.Errorf("writing the publication draft: %w", err)
	}

	editor, err := publicationEditor(plan.Editor, env, path)
	if err != nil {
		return "", err
	}
	editor.Stdin, editor.Stdout, editor.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := editor.Run(); err != nil {
		return "", fmt.Errorf("the editor did not finish: %w", err)
	}

	edited, err := os.ReadFile(path) // #nosec G304 -- the path is one this function just created
	if err != nil {
		return "", fmt.Errorf("reading the edited publication draft: %w", err)
	}
	return string(edited), nil
}

// publicationEditor builds the editor process.
//
// The project's configured editor keeps its own flags — `code -w` has to stay
// `code -w`, or the editor returns before the user has typed anything — and the
// document is appended as the thing to open. A project that configures none
// falls back to this process's own environment, which is where $EDITOR is and
// where the daemon cannot look (FR-REV-003).
func publicationEditor(configured api.EditorCommand, env *environment, path string) (*exec.Cmd, error) {
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

	// #nosec G204 -- the program is the project's own configuration or the
	// user's own $EDITOR, every argument is one vector element, and the file is
	// one this process just created; nothing reaches a shell.
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

// publicationMarker starts one repository's section of the draft.
//
// It is a line nothing in a Markdown description produces by accident, and it is
// the only structure the document has: everything after it is the words, exactly
// as they will be sent.
var publicationMarker = regexp.MustCompile(`^=== ([A-Za-z0-9][A-Za-z0-9._-]*) ===$`)

// publicationDocument renders the draft the user edits.
func publicationDocument(plan api.PublicationStatus) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Publication for %s — %s\n", plan.Task.Key, plan.Task.Title)
	b.WriteString("#\n")
	b.WriteString("# Feat opens one merge request per section below, from this machine, with your own\n")
	b.WriteString("# credentials. The title and the description were written by the agent: read them\n")
	b.WriteString("# before you approve, because they can carry anything the agent read.\n")
	b.WriteString("#\n")
	b.WriteString("# In each section the first non-empty line is the title and everything after it is\n")
	b.WriteString("# the description, sent exactly as it stands — including lines beginning with \"#\",\n")
	b.WriteString("# which are Markdown headings there rather than comments. Only these lines above\n")
	b.WriteString("# the first \"===\" marker are comments.\n")
	b.WriteString("#\n")
	b.WriteString("# Delete a whole section to leave that repository unpublished.\n")

	for _, draft := range plan.Drafts {
		if draft.Published != nil {
			// Already on the forge. It is named in the plan above and left out
			// of the document, because editing words that have already been
			// sent would suggest that saving them changes something.
			continue
		}
		fmt.Fprintf(&b, "\n=== %s ===\n", draft.RepositoryID)
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(draft.Title))
		if body := strings.TrimRight(draft.Body, "\n"); body != "" {
			b.WriteString(body + "\n")
		}
	}
	return b.String()
}

// readPublicationDocument reads the edited draft back.
//
// What it returns is what the user approved, word for word, together with the
// commit the plan composed each section against — so that the daemon can refuse
// a repository whose branch moved while the editor was open.
func readPublicationDocument(document string, drafts []api.PublicationDraft) ([]api.ApprovedPublication, error) {
	known := make(map[string]api.PublicationDraft, len(drafts))
	for _, draft := range drafts {
		known[draft.RepositoryID] = draft
	}

	var (
		approved []api.ApprovedPublication
		current  *api.ApprovedPublication
		body     []string
		seen     = make(map[string]bool, len(drafts))
	)
	flush := func() {
		if current == nil {
			return
		}
		current.Body = strings.TrimSpace(strings.Join(body, "\n"))
		approved = append(approved, *current)
		current, body = nil, nil
	}

	for _, line := range strings.Split(document, "\n") {
		marker := publicationMarker.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if marker != nil {
			flush()
			id := marker[1]
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
		if current.Title == "" {
			if strings.TrimSpace(line) == "" {
				continue
			}
			current.Title = strings.TrimSpace(line)
			continue
		}
		body = append(body, line)
	}
	flush()

	for _, one := range approved {
		if one.Title == "" {
			return nil, fmt.Errorf("repository %s has no title, and a merge request needs one. "+
				"Write one, or delete the section to leave it unpublished", one.RepositoryID)
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
