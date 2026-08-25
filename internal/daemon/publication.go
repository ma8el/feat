package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/review"
)

// PublicationPlan composes what publishing this task would do.
//
// It records nothing. What it returns is the document the user reads and edits:
// the agent's words for each repository, together with what Feat already knows —
// the forge, the remote, the base branch, and the commit that would be pushed
// (ADR-070).
//
// A repository that cannot be published becomes a note rather than a failure,
// for the reason a review command that will not expand does: the others are
// still publishable, and a user is better served by being told which one is not
// and why.
func (s *service) PlanPublication(ctx context.Context, id domain.TaskID) (api.PublicationResult, error) {
	// Held for the whole action, as a review action is: it reads the task, the
	// project's configuration, and every worktree, and it must not compose a
	// document from a task another request is halfway through changing.
	defer s.locks.lock(id)()

	task, cfg, err := s.reviewTask(ctx, id)
	if err != nil {
		return api.PublicationResult{}, err
	}

	draft, notes := s.publicationDraft(ctx, task)
	drafts, composeNotes := s.composePublication(ctx, cfg, task, draft)
	return api.PublicationResult{
		Task:   task,
		Drafts: drafts,
		Editor: publicationEditor(cfg),
		Notes:  append(notes, composeNotes...),
	}, nil
}

// Publish opens one merge request per approved repository.
//
// The order is ADR-073's, and it is the order task preparation already uses for
// the same hazard in a weaker form: plan every repository, record the plan, then
// apply one at a time, recording each result before the next begins. A worktree
// Feat forgot is on the user's disk; a merge request Feat forgot is on somebody
// else's server.
//
// Nothing is rolled back. A failure on one repository does not stop the others,
// because the failures that are common to all of them produce the same error
// however many are attempted, and the ones that are local to a repository leave
// the user with one thing to fix rather than an unknown number still unattempted.
func (s *service) ApplyPublication(
	ctx context.Context, id domain.TaskID, request api.PublishRequest,
) (api.PublicationResult, error) {
	defer s.locks.lock(id)()

	task, cfg, err := s.reviewTask(ctx, id)
	if err != nil {
		return api.PublicationResult{}, err
	}
	if len(request.Repositories) == 0 {
		return api.PublicationResult{}, fmt.Errorf(
			"%w: a publication names the repositories to publish, and this one names none", api.ErrInvalid)
	}

	approved, err := s.checkApproved(ctx, cfg, task, request)
	if err != nil {
		return api.PublicationResult{}, err
	}

	// The plan, recorded before anything is attempted. Every repository that
	// could exist on a forge afterwards is written down first, so an
	// interruption at any point names what it had not yet attempted rather than
	// leaving it to be discovered (ADR-073, ADR-029).
	entries := make([]domain.RepositoryPublication, 0, len(approved))
	for _, one := range approved {
		entries = append(entries, domain.RepositoryPublication{
			RepositoryID: one.checked.RepositoryID,
			Forge:        one.checked.Forge,
			Remote:       one.checked.Remote,
			BaseBranch:   one.checked.BaseBranch,
			Commit:       one.checked.Commit,
		})
	}
	if err := task.PlanPublication(entries, s.now()); err != nil {
		return api.PublicationResult{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return api.PublicationResult{}, err
	}
	s.record(ctx, task, domain.Event{
		Type: domain.EventPublicationChanged,
		Detail: fmt.Sprintf("publishing %d repository(s): %s",
			len(entries), publicationNames(entries)),
	})

	notes := s.applyPublication(ctx, task, approved)
	return api.PublicationResult{Task: task, Editor: publicationEditor(cfg), Notes: notes}, nil
}

// approvedPublication is one repository's checked publication together with the
// adapter that will open it.
type approvedPublication struct {
	checked review.Publication
	adapter forge.Adapter
}

// applyPublication publishes one repository at a time.
//
// Each result is recorded before the next repository begins, which is what makes
// an interrupted publication recoverable: the record says what exists and what
// was never attempted, and re-publishing skips the ones that already have a
// merge request — as already published, never as stale (ADR-073).
func (s *service) applyPublication(
	ctx context.Context, task *domain.Task, approved []approvedPublication,
) []string {
	// Detached from the request that started it, and bounded by the adapters'
	// own timeouts rather than by a caller. A merge request that was opened and
	// not written down is the one failure this ordering cannot make safe, and a
	// client that hung up between the forge answering and the record being saved
	// would produce exactly that. What a caller giving up costs is the response,
	// which is a screen; what it must not cost is the record.
	ctx = context.WithoutCancel(ctx)

	byRepository := make(map[domain.RepositoryID]approvedPublication, len(approved))
	for _, one := range approved {
		byRepository[one.checked.RepositoryID] = one
	}

	var (
		notes   []string
		results []review.Result
	)
	// The record's own order, which is the order the plan asked for.
	for _, planned := range append([]domain.RepositoryPublication(nil), task.Publication.Repositories...) {
		if planned.State == domain.PublicationPublished {
			opened := domain.MergeRequest{}
			if planned.Request != nil {
				opened = *planned.Request
			}
			results = append(results, review.Result{
				RepositoryID: planned.RepositoryID,
				Outcome:      review.SkippedOutcome,
				Request:      opened,
				Detail:       "it already published as " + opened.URL,
			})
			notes = append(notes, fmt.Sprintf(
				"%s already published as %s and was not attempted again", planned.RepositoryID, opened.URL))
			continue
		}

		one, requested := byRepository[planned.RepositoryID]
		if !requested {
			// Recorded by an earlier plan and not named by this one. It is left
			// exactly as it is rather than attempted or removed: a record that
			// can forget what it planned is the hazard this ordering removes.
			continue
		}

		result := s.publishRepository(ctx, one)
		results = append(results, result)
		notes = append(notes, result.Skipped...)

		if err := s.recordPublication(ctx, task, result); err != nil {
			// The repository was attempted and what came of it could not be
			// written down, which is the one failure this ordering cannot make
			// safe. It is reported rather than swallowed, and the repositories
			// after it still run: stopping would add a second unattempted set to
			// a record that is already behind the world.
			s.logger.ErrorContext(ctx, "recording what a repository's publication produced",
				slog.String("task", task.ID.String()),
				slog.String("repository", result.RepositoryID.String()),
				slog.String("outcome", string(result.Outcome)),
				slog.Any("error", err))

			what := "was published as " + result.Request.URL
			if result.Outcome == review.FailedOutcome {
				what = "did not publish"
			}
			notes = append(notes, fmt.Sprintf(
				"%s %s and the result could not be recorded (%v): look on the forge before publishing "+
					"it again", result.RepositoryID, what, err))
		}
	}

	s.record(ctx, task, domain.Event{
		Type:   domain.EventPublicationChanged,
		Detail: "publication finished: " + review.SummarizePublication(results),
	})
	return notes
}

// publishRepository pushes one branch and opens one merge request.
func (s *service) publishRepository(ctx context.Context, one approvedPublication) review.Result {
	result := review.Result{RepositoryID: one.checked.RepositoryID, Outcome: review.PublishedOutcome}

	report, err := s.git.Push(ctx, git.PushRequest{
		Worktree: one.checked.Directory,
		Remote:   one.checked.Remote,
		Branch:   one.checked.Branch,
		Commit:   one.checked.Commit,
	})
	// The report is kept whether the push worked or not: what it names was
	// decided before the push ran, and a user who depends on a pre-push hook
	// needs to know it was skipped either way (ADR-070).
	for _, skipped := range report.Skipped {
		result.Skipped = append(result.Skipped,
			one.checked.RepositoryID.String()+": "+skipped)
	}
	if err != nil {
		result.Outcome = review.FailedOutcome
		result.Detail = "pushing " + one.checked.Branch + " to " + one.checked.Remote + " failed: " + err.Error()
		return result
	}

	opened, err := one.adapter.Open(ctx, forge.Request{
		Directory:    one.checked.Directory,
		Remote:       one.checked.Remote,
		SourceBranch: one.checked.Branch,
		TargetBranch: one.checked.BaseBranch,
		Title:        one.checked.Title,
		Body:         one.checked.Body,
	})
	if err != nil {
		result.Outcome = review.FailedOutcome
		// The branch is on the remote and the request is not, which is a state
		// worth naming: publishing again opens the request without pushing
		// anything new.
		result.Detail = err.Error() + " (the branch " + one.checked.Branch + " is on " +
			one.checked.Remote + "; publishing again opens the request from it)"
		return result
	}
	result.Request = opened
	return result
}

// recordPublication writes down what one repository's publication produced.
func (s *service) recordPublication(ctx context.Context, task *domain.Task, result review.Result) error {
	switch result.Outcome {
	case review.PublishedOutcome:
		if err := task.RecordPublished(result.RepositoryID, result.Request, s.now()); err != nil {
			return err
		}
	case review.FailedOutcome:
		if err := task.RecordPublicationFailure(result.RepositoryID, result.Detail, s.now()); err != nil {
			return err
		}
	default:
		return nil
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return err
	}

	detail := result.RepositoryID.String() + " published as " + result.Request.URL
	if result.Outcome == review.FailedOutcome {
		detail = result.RepositoryID.String() + " did not publish: " + result.Detail
	}
	s.record(ctx, task, domain.Event{
		Type:         domain.EventPublicationChanged,
		RepositoryID: result.RepositoryID,
		Detail:       detail,
	})
	return nil
}

// checkApproved turns what the user approved into publications that may run.
//
// Every refusal here happens before the plan is recorded and before anything is
// pushed, so a request this rejects leaves nothing behind. That is deliberate:
// every refusal it makes is about the request rather than about a forge — a
// repository that cannot be published, a repository this publication does not
// offer, words a merge request cannot carry, a draft describing a commit that is
// no longer current — and a partial state is only ever the price of reaching
// somebody else's server.
func (s *service) checkApproved(
	ctx context.Context, cfg *config.Config, task *domain.Task, request api.PublishRequest,
) ([]approvedPublication, error) {
	worktrees := publicationWorktrees(task)
	drafted, _ := s.publicationDraft(ctx, task)

	var (
		approved []approvedPublication
		stale    []string
		seen     = make(map[string]bool, len(request.Repositories))
	)
	for _, one := range request.Repositories {
		if seen[one.RepositoryID] {
			return nil, fmt.Errorf("%w: repository %s is named twice, and a publication opens one merge "+
				"request per repository", api.ErrInvalid, one.RepositoryID)
		}
		seen[one.RepositoryID] = true

		id := domain.RepositoryID(one.RepositoryID)
		publishable, err := s.publishable(cfg, task, id)
		if err != nil {
			return nil, err
		}

		// A repository that already published is not examined at all. It is not
		// asked whether its words are current, because re-publishing skips it as
		// already published and a staleness question would turn that skip into a
		// refusal — the confusion ADR-073 keeps the two reasons apart to avoid.
		// It is not asked for a title either: the words that were sent are on
		// the forge, and the plan below keeps the entry exactly as it was
		// recorded.
		if task.Publication != nil {
			if recorded, found := task.Publication.Repository(id); found &&
				recorded.State == domain.PublicationPublished {
				approved = append(approved, approvedPublication{
					checked: review.Publication{
						RepositoryID: id,
						Forge:        recorded.Forge,
						Remote:       recorded.Remote,
						Branch:       publishable.binding.Branch,
						BaseBranch:   recorded.BaseBranch,
						Commit:       recorded.Commit,
					},
					adapter: publishable.adapter,
				})
				continue
			}
		}

		// What the plan offered, asked again rather than trusted. A repository
		// the plan left out was never displayed and so was never approved, and
		// an approval naming it anyway would push a branch and ask for a merge
		// request with nothing in it. The answer comes from the function the
		// plan composes with, so the two cannot drift into disagreeing about
		// what may be published (ADR-070). Only the refusal is read here: what
		// an offer has to say about a repository it offers anyway is a note
		// under the plan, where the user is reading.
		offer := s.offerPublication(ctx, publishable.binding)
		if offer.refusal != "" {
			return nil, fmt.Errorf("%w: the publication was not attempted, because %s. "+
				"Nothing was pushed and nothing was opened", api.ErrInvalid, offer.refusal)
		}

		reason, current := stalePublication(id, offer.head, drafted, one)
		if !current {
			stale = append(stale, reason)
			continue
		}

		checked, err := review.NewPublication(review.PublicationRequest{
			RepositoryID: id,
			Forge:        publishable.forge,
			Remote:       publishable.remote,
			Branch:       publishable.binding.Branch,
			BaseBranch:   publishable.baseBranch,
			Commit:       one.Commit,
			Title:        one.Title,
			Body:         one.Body,
			Directory:    publishable.binding.WorktreePath,
			Worktrees:    worktrees,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
		}
		approved = append(approved, approvedPublication{checked: checked, adapter: publishable.adapter})
	}

	if len(stale) > 0 {
		// Reported and never resolved, which is the rule a confirmation
		// fingerprint follows: Feat does not silently re-compose a draft,
		// because the user would be publishing words they never read (ADR-031).
		return nil, fmt.Errorf("%w: the publication was not attempted, because %s. "+
			"Nothing was pushed and nothing was opened. Ask the agent for a fresh draft, "+
			"then publish again", api.ErrInvalid, strings.Join(stale, "; and "))
	}
	if len(approved) == 0 {
		return nil, fmt.Errorf("%w: nothing was left to publish", api.ErrInvalid)
	}
	return approved, nil
}

// stalePublication reports whether the words being published still describe the
// repository as it is.
//
// Two commits are compared against the repository's current head: the one the
// approval was composed against, and the one the agent's draft describes. They
// are usually the same value and they answer different questions — the first is
// whether what was displayed is still what would be sent, and the second is
// whether the agent wrote its description before it finished working.
//
// The head is the one the offer just read, rather than one read again here: a
// publication that asked twice could refuse a repository as stale against a
// commit its own offer never saw.
func stalePublication(
	id domain.RepositoryID, head string, drafted control.PublicationDraft,
	approved api.ApprovedPublication,
) (reason string, current bool) {
	if approved.Commit != head {
		return fmt.Sprintf("%s was %s when the draft was displayed and is %s now, "+
			"so what would be sent is not what was read", id, short(approved.Commit), short(head)), false
	}
	if entry, found := drafted.Repository(id); found && entry.Commit != head {
		return fmt.Sprintf("the agent's draft for %s describes %s and the branch is now at %s, "+
			"so the description was written before the work finished",
			id, short(entry.Commit), short(head)), false
	}
	return "", true
}

// publishableRepository is one repository a publication can reach.
type publishableRepository struct {
	binding    domain.TaskRepository
	forge      domain.ForgeKind
	adapter    forge.Adapter
	remote     string
	baseBranch string
}

// publishable resolves one repository's publication inputs, or says why it has
// none.
func (s *service) publishable(
	cfg *config.Config, task *domain.Task, id domain.RepositoryID,
) (publishableRepository, error) {
	binding, bound := task.Repository(id)
	if !bound {
		return publishableRepository{}, fmt.Errorf("%w: task %s does not hold repository %s",
			api.ErrInvalid, task.ID, id)
	}
	if binding.Access != domain.TaskAccessReadWrite {
		return publishableRepository{}, fmt.Errorf(
			"%w: task %s holds repository %s read-only, so it has no branch to open a merge request from",
			api.ErrInvalid, task.ID, id)
	}

	repository, configured := cfg.Repository(id.String())
	if !configured {
		return publishableRepository{}, fmt.Errorf(
			"%w: project %s no longer configures repository %s", api.ErrInvalid, task.ProjectID, id)
	}
	if repository.Forge == nil {
		return publishableRepository{}, fmt.Errorf(
			"%w: repository %s configures no forge, so Feat has nowhere to open a merge request. "+
				"Set repositories.%s.forge.kind", api.ErrInvalid, id, id)
	}
	kind := domain.ForgeKind(repository.Forge.Kind)
	adapter, built := s.forges[kind]
	if !built {
		return publishableRepository{}, fmt.Errorf(
			"%w: repository %s publishes to %s, and this build opens merge requests on %s",
			api.ErrInvalid, id, kind, forgeNames(s.forges))
	}

	return publishableRepository{
		binding:    *binding,
		forge:      kind,
		adapter:    adapter,
		remote:     repository.Remote,
		baseBranch: repository.DefaultBranch,
	}, nil
}

// publicationOffer is what one repository would contribute to a publication.
//
// It is the answer to a question both halves of publishing ask: the plan, to
// decide what the user is shown, and the approval, to decide what it may send.
// One function answers it so that a repository the plan left out cannot be
// published by naming it anyway.
type publicationOffer struct {
	// head is the commit a merge request would be opened from.
	head string
	// refusal is why there is nothing to publish, and is empty when there is.
	// It is a whole sentence, because it is read as a note under a plan and as
	// a refusal of an approval.
	refusal string
	// notes are what a person should know about a repository that is offered
	// anyway.
	notes []string
}

// offerPublication decides whether one repository has something to publish.
func (s *service) offerPublication(ctx context.Context, binding domain.TaskRepository) publicationOffer {
	head, err := s.git.Commit(ctx, binding.WorktreePath, "HEAD")
	if err != nil {
		return publicationOffer{refusal: fmt.Sprintf(
			"%s has nothing to publish: what it has checked out could not be read (%v)",
			binding.RepositoryID, err)}
	}

	offer := publicationOffer{head: head}
	ahead, err := s.git.Count(ctx, binding.WorktreePath, binding.BaseCommit, "HEAD")
	switch {
	case err != nil:
		offer.notes = append(offer.notes, fmt.Sprintf(
			"how far %s is ahead of its base could not be counted (%v); it is offered for publication "+
				"anyway", binding.RepositoryID, err))
	case ahead == 0:
		offer.refusal = fmt.Sprintf(
			"%s has no commit beyond the base it started from, so there is nothing to open a merge "+
				"request for", binding.RepositoryID)
	}
	return offer
}

// composePublication builds the document the user reads, one row per repository
// that can be published.
func (s *service) composePublication(
	ctx context.Context, cfg *config.Config, task *domain.Task, drafted control.PublicationDraft,
) ([]api.PublicationDraft, []string) {
	var (
		drafts []api.PublicationDraft
		notes  []string
	)
	for _, binding := range task.Repositories {
		if binding.Access != domain.TaskAccessReadWrite {
			continue
		}
		publishable, err := s.publishable(cfg, task, binding.RepositoryID)
		if err != nil {
			notes = append(notes, strings.TrimPrefix(err.Error(), api.ErrInvalid.Error()+": "))
			continue
		}

		offer := s.offerPublication(ctx, binding)
		notes = append(notes, offer.notes...)
		if offer.refusal != "" {
			notes = append(notes, offer.refusal)
			continue
		}
		head := offer.head

		row := api.PublicationDraft{
			RepositoryID: binding.RepositoryID.String(),
			Forge:        string(publishable.forge),
			Remote:       publishable.remote,
			Branch:       binding.Branch,
			BaseBranch:   publishable.baseBranch,
			Commit:       head,
		}
		if entry, found := drafted.Repository(binding.RepositoryID); found {
			row.Title, row.Body, row.DraftCommit = entry.Title, entry.Body, entry.Commit
			row.Stale = entry.Commit != head
			if row.Stale {
				notes = append(notes, fmt.Sprintf(
					"the agent's draft for %s describes %s and the branch is now at %s: it is refused as "+
						"stale rather than published. Ask the agent for a fresh draft",
					binding.RepositoryID, short(entry.Commit), short(head)))
			}
		} else {
			notes = append(notes, fmt.Sprintf(
				"the agent wrote no draft for %s; write a title before approving the publication",
				binding.RepositoryID))
		}
		row.Body = withTicket(row.Body, task)
		if task.Publication != nil {
			if recorded, found := task.Publication.Repository(binding.RepositoryID); found &&
				recorded.State == domain.PublicationPublished && recorded.Request != nil {
				row.Published = &api.MergeRequest{
					Reference: recorded.Request.Reference,
					URL:       recorded.Request.URL,
				}
			}
		}
		if skipped, err := s.git.SuppressedHooks(ctx, binding.WorktreePath); err == nil {
			row.Skipped = skipped
			for _, one := range skipped {
				notes = append(notes, binding.RepositoryID.String()+": "+one)
			}
		}
		drafts = append(drafts, row)
	}
	return drafts, notes
}

// withTicket adds the ticket a task came from to a draft's description.
//
// It is the one thing Feat adds to the agent's prose, and it is added to the
// draft rather than to the request: the user reads it, can delete it, and what
// is sent is what they read (ADR-070). The agent is not asked for it, because it
// is not something the agent knows — the brief it was given is the composed
// brief, not the ticket it came from.
//
// A task from a prompt or a Markdown file has no ticket, and nothing is added.
func withTicket(body string, task *domain.Task) string {
	ticket := task.Source.Ticket
	if ticket == nil {
		return body
	}

	reference := ticket.Reference
	if ticket.URL != "" {
		reference = ticket.URL
	}
	if reference == "" {
		return body
	}
	if strings.Contains(body, reference) {
		// The agent named it already, which is likely enough that adding a
		// second copy would be a line the user deletes every time.
		return body
	}
	if body = strings.TrimRight(body, "\n"); body != "" {
		body += "\n\n"
	}
	return body + "Task " + task.Key().String() + " — " + reference
}

// publicationDraft reads the draft the agent wrote for this task.
//
// It is read back from the control workspace rather than held in the task's
// record, because that is where the agent wrote it and where it stays: the
// outbox is the account of what the agent sent, and the draft is the one
// message a publication has to read again long after it was applied. The
// provider adapter parses it, as it parses every agent-authored message, so a
// second provider reaches the same code rather than a copy of it.
//
// A task with no draft is not an error. A user can publish work the agent never
// described, and what they get is a document with the words left to them.
func (s *service) publicationDraft(ctx context.Context, task *domain.Task) (control.PublicationDraft, []string) {
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return control.PublicationDraft{}, []string{
			"the agent's publication draft could not be read (" + err.Error() + ")"}
	}
	message, found, err := workspace.Latest(control.TypePublicationDraft)
	if err != nil {
		return control.PublicationDraft{}, []string{
			"the agent's publication draft could not be read (" + err.Error() + ")"}
	}
	if !found {
		return control.PublicationDraft{}, []string{
			"the agent wrote no publication draft for this task, so the words are yours to write"}
	}

	event, applicable, err := s.agent.ParseEvent(ctx, message)
	if err != nil || !applicable || event.Draft == nil {
		reason := "this build could not read it"
		if err != nil {
			reason = err.Error()
		}
		return control.PublicationDraft{}, []string{
			"the agent's publication draft was not usable (" + reason + "), so the words are yours to write"}
	}
	return *event.Draft, nil
}

// publicationEditor is how the client opens the draft.
//
// It is the project's configured editor command with the argument naming what to
// open left off, because what this opens is a draft rather than a repository:
// the command names an editor and its flags, and the client appends the document
// it wrote. An unconfigured editor leaves the program empty, and the client falls
// back to what its own environment names — which is the daemon's whole reason for
// not resolving it here (FR-REV-003).
func publicationEditor(cfg *config.Config) api.EditorCommand {
	if cfg.Review.Editor.Empty() {
		return api.EditorCommand{Arguments: []string{}}
	}

	command := cfg.Review.Editor.Command
	arguments := make([]string, 0, len(command)-1)
	for _, argument := range command[1:] {
		if strings.Contains(argument, "{") {
			// The slot that names what to open. Every placeholder in this
			// command is about a repository, and none of them is this document,
			// so the argument is dropped rather than expanded into something it
			// does not mean.
			continue
		}
		arguments = append(arguments, argument)
	}
	return api.EditorCommand{Program: command[0], Arguments: arguments}
}

// publicationWorktrees lists every worktree path the task recorded.
func publicationWorktrees(task *domain.Task) []string {
	worktrees := make([]string, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		if binding.WorktreePath != "" {
			worktrees = append(worktrees, binding.WorktreePath)
		}
	}
	return worktrees
}

// publicationNames renders the repositories a plan covers.
func publicationNames(entries []domain.RepositoryPublication) string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.RepositoryID.String())
	}
	return strings.Join(names, ", ")
}

// forgeNames renders the forges this build publishes to, so that a refusal says
// what would have been accepted.
func forgeNames(forges map[domain.ForgeKind]forge.Adapter) string {
	names := make([]string, 0, len(forges))
	for kind := range forges {
		names = append(names, string(kind))
	}
	if len(names) == 0 {
		return "no forge at all"
	}
	sort.Strings(names)
	return strings.Join(names, " and ")
}

// short renders a commit at the length a person reads.
func short(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

// publicationFor describes what publishing this task would cover, so that the
// provider adapter asks the agent for a draft only where there is somewhere to
// publish.
//
// It names the repositories this task holds read-write whose configuration
// declares a forge. A task with none gets an empty value, and the agent is never
// told to write a document Feat has nowhere to send (ADR-070).
func publicationFor(cfg *config.Config, task *domain.Task) agent.Publication {
	var repositories []string
	for _, binding := range task.Repositories {
		if binding.Access != domain.TaskAccessReadWrite {
			continue
		}
		repository, configured := cfg.Repository(binding.RepositoryID.String())
		if !configured || repository.Forge == nil {
			continue
		}
		repositories = append(repositories, binding.RepositoryID.String())
	}
	return agent.Publication{Repositories: repositories}
}
