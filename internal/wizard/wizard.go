package wizard

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
)

// Kind is how an answer is given, which is what an asker needs to know to ask.
//
// It is the whole of the presentation the flow decides. A line conversation
// prints the options in brackets and reads a word; a dialog draws a list and
// moves a cursor down it. Both are answering the same question.
type Kind string

const (
	// KindText is free text.
	KindText Kind = "text"
	// KindChoice is one of Options, and nothing else.
	KindChoice Kind = "choice"
	// KindConfirm is yes or no.
	KindConfirm Kind = "confirm"
)

// Section is the part of the configuration a question belongs to.
//
// It is what lets an asker show where the user is without knowing what the
// questions are: the sections are fixed and few, and the questions inside one
// depend on the answers.
type Section string

const (
	// SectionProject is what the project is called.
	SectionProject Section = "project"
	// SectionRepositories is which repositories take part.
	SectionRepositories Section = "repositories"
	// SectionAgent is where the agent runs and what it may reach.
	SectionAgent Section = "agent"
	// SectionServices is the application runtime.
	SectionServices Section = "services"
	// SectionTracker is where the project's tickets come from.
	//
	// It is its own section rather than a question of the project's, because a
	// forge and a tracker are different questions with different owners and the
	// configuration keeps them apart for that reason: the forge belongs to a
	// repository and is asked with one, and the tracker belongs to the project
	// and is asked once (ADR-071).
	SectionTracker Section = "tracker"
)

// Sections are the sections in the order they are asked, for an asker that
// wants to show the whole path rather than the step.
//
// The application comes before the agent, which is the reverse of the order
// these were first asked in. The agent's Compose question can then offer the
// files the application did not claim, where before it could only propose
// nothing: which files define a container the agent works in is a question
// about what is left over, and nothing was left over yet (ADR-100).
//
// Verification is not among them. `checks:` is still configuration, still
// validated by `feat doctor`, and still documented in the example file — it is
// no longer a question, because a gate configured in passing is a gate that
// fails on the machine it was configured from (ADR-078).
func Sections() []Section {
	return []Section{
		SectionProject, SectionRepositories, SectionServices, SectionAgent, SectionTracker,
	}
}

// Question is one thing to ask, and everything needed to ask it.
type Question struct {
	// ID identifies the question for as long as the flow asks it. It is stable
	// across runs and is what a test or a label refers to rather than the
	// prompt, which is prose.
	ID string
	// Section is where in the configuration this belongs.
	Section Section
	// Heading names the section, and is set only on the first question of one.
	Heading string
	// Detail explains what is being asked, in the words the user needs before
	// deciding. It is set on the first question of a group rather than on every
	// question of one: the section's own opening question, the first repository
	// asked for a mount point, the first repeat of a file loop.
	//
	// An empty line is a paragraph break, and every asker draws it as one blank
	// line and nothing else — no indent, no bullet, no styling. It is what
	// separates a warning from the sentence saying what the field is.
	//
	// The mount questions are the exception, and are the only one: both carry
	// the rule Compose merges by on every question of their group, because that
	// field's failure is silent and the second repository is where the sentence
	// is needed most. It is an exception on recorded grounds rather than a
	// precedent for the next block of prose (ADR-082).
	Detail []string
	// Notes are what the previous answer established: what Git said about a
	// checkout, which services a Compose file declares, what Feat assumed. They
	// belong to this question because they are what it is asked in light of, and
	// a question that found something out about its own proposals adds to them
	// for the same reason.
	Notes []string
	// Prompt is the question itself, without punctuation or decoration.
	Prompt string
	// Proposed is what an empty answer accepts, always: an asker may show it in
	// brackets or as a placeholder, and either way Enter takes it. A question
	// with no proposal and no Optional must be answered.
	Proposed string
	// Candidates are the values an asker may offer as completions of a text
	// answer, and Step assembles them: the proposal first, then whatever else the
	// flow found beside it. A closed question has none — its answers are Options,
	// and nothing is typed into one.
	//
	// They are offered, never required. What is answered is what the user gives,
	// an empty answer still takes Proposed, and an asker with no way to complete
	// ignores the list — which is what keeps the two askers the same
	// conversation (ADR-077).
	Candidates []string
	// Kind is how the answer is given.
	Kind Kind
	// Options are the acceptable answers of a KindChoice question.
	Options []string
	// Optional reports that an empty answer is itself an answer — no check, no
	// more Compose files, no environment file. It applies once Proposed is
	// empty, because a proposal is what an empty answer takes first.
	Optional bool
}

// Review is the configuration the answers compose.
type Review struct {
	// Path is where the file would be written.
	Path string
	// Text is the file, exactly as it was validated and exactly as it will be
	// written: the same bytes rather than the same rendering twice.
	Text []byte
}

// Options configure a wizard.
type Options struct {
	// Host answers what the machine can answer. It is required.
	Host Host
	// ConfigDir is the project configuration directory the file is written to.
	ConfigDir string
	// Resolve is what the composed configuration is resolved against, so that
	// what is reviewed is what this machine would load.
	Resolve config.Options
	// ID preselects the project identifier, which `feat project init <project>`
	// supplies. An empty value asks for one.
	ID string
}

// Wizard is one project configuration being answered.
//
// It is a state machine over config.Draft: Step says what to ask, Answer
// applies one answer and moves on, and Back returns to the previous question
// with everything the answer changed undone. Nothing is written until Write,
// and Write is the only method that touches the disk.
type Wizard struct {
	host      Host
	configDir string
	resolve   config.Options
	state
	// history holds one snapshot per answered question, which is what Back
	// restores. It is the whole state rather than the answer, because an answer
	// changes more than the field it names: a repository's access decides
	// whether it is asked for a mount point, and a mode decides whether the
	// devcontainer is asked about at all.
	history []state
}

// state is everything an answer can change.
type state struct {
	stage stage
	draft config.Draft
	notes []string

	// sawRemote records that at least one checkout really had a remote, as
	// opposed to having had one assumed for it. The base policy turns on the
	// difference, and the draft cannot hold it: every repository is written with
	// a remote name, because configuration requires one.
	sawRemote bool
	// pending is the repository being answered, complete only when its three
	// questions have been.
	pending config.DraftRepository
	// checkout is what Git said about the repository being answered.
	checkout Checkout
	// mount is which repository is being asked for a container path in the
	// agent's own container, and contributor is which is being asked what it
	// brings to the application. They are separate cursors because they walk the
	// repositories for different reasons: the first is where the agent works,
	// the second is what the user runs.
	mount       int
	contributor int
	// contribution is the repository being answered's part of the application,
	// complete only when its questions have been.
	contribution config.DraftRepositoryRuntime
	// composition is what the answered Compose files were read to propose.
	composition Composition
	// files, envFiles, and services accumulate the answers of the loops that
	// take more than one: Compose files for the agent or the application, the
	// environment files passed to Compose, and the services Feat manages.
	files    []string
	envFiles []string
	services []string
}

// stage is where the flow is. The order below is the order questions are asked
// in; which of them are asked depends on the answers.
type stage int

const (
	stageID stage = iota
	stageName
	stageRepositoryPath
	stageRepositoryID
	stageRepositoryAccess
	stageRepositoryForge
	stageAnotherRepository
	// stageEditable is asked only when no repository can be written to, and
	// stagePrimary only when more than one can.
	stageEditable
	stagePrimary
	stageRuntimeWanted
	// The application stages, asked once per repository: a runtime is composed
	// of its repositories, so what it is made of is answered where the code is.
	stageRuntimeRepository
	stageRuntimeCompose
	stageRuntimeServices
	stageRuntimeMount
	stageRuntimeReachable
	stageRuntimeEnvFile
	// The agent's own environment, after the application rather than before it,
	// so that its Compose question knows which files are already spoken for.
	stageMode
	// The devcontainer stages, asked only in that mode.
	stageComposeFile
	stageService
	stageUser
	stageMount
	stageClaudeVolume
	stageVolumeName
	// stageTracker is asked last, because it is the one question whose answer
	// may not exist yet: a tracker is a command the user writes, and asking for
	// it earlier would put a thing to go away and build in the middle of
	// configuring a project (ADR-100).
	stageTracker
	// stageComplete is every question answered, and the file not yet written.
	stageComplete
)

// Defaults proposed where Git had no answer. They are the values configuration
// itself defaults to, so a repository that acquires a remote later needs no
// change here.
const (
	defaultRemoteName = "origin"
	defaultBranchName = "main"
)

// New builds a wizard.
//
// A preselected identifier is validated and checked against the configuration
// directory here, because it names the file: a project that is already
// configured is refused before a conversation whose answers would have nowhere
// to go.
func New(opts Options) (*Wizard, error) {
	w := &Wizard{host: opts.Host, configDir: opts.ConfigDir, resolve: opts.Resolve}

	if opts.ID != "" {
		if err := domain.ProjectID(opts.ID).Validate(); err != nil {
			return nil, err
		}
		if err := w.unconfigured(opts.ID); err != nil {
			return nil, err
		}
		w.draft.ID = opts.ID
		w.stage = stageName
	}
	return w, nil
}

// ID is the project's identifier, once it has been answered.
func (w *Wizard) ID() string { return w.draft.ID }

// Complete reports that every question has been answered.
func (w *Wizard) Complete() bool { return w.stage == stageComplete }

// Step returns the question to ask now, and whether there is one.
func (w *Wizard) Step() (Question, bool) {
	if w.stage == stageComplete {
		return Question{}, false
	}
	question := w.question()
	// What the last answer established, then what the question itself found: the
	// order the two became true in. This assigned rather than appended, so a
	// question that had something to say about its own proposals said it to
	// nobody — the other Compose files beside a repository were derived, written
	// into a note, and dropped here on the way out.
	question.Notes = append(append([]string(nil), w.notes...), question.Notes...)
	question.Candidates = candidates(question)
	return question, true
}

// Back returns to the previous question, undoing what its answer changed.
//
// It reports whether there was one. The first question has nothing behind it,
// and neither has a run whose identifier was supplied rather than asked for.
func (w *Wizard) Back() bool {
	if len(w.history) == 0 {
		return false
	}
	w.state = w.history[len(w.history)-1]
	w.history = w.history[:len(w.history)-1]
	return true
}

// Answer applies one answer to the current question.
//
// A returned error is a rejection the user can correct: the same question comes
// back, and the text says what was wrong with the answer in the terms it was
// given in. Nothing is changed by a rejected answer.
func (w *Wizard) Answer(ctx context.Context, value string) error {
	if w.stage == stageComplete {
		return errors.New("every question has been answered")
	}

	question := w.question()
	answer := strings.TrimSpace(value)
	if answer == "" {
		// An empty answer takes the proposal wherever there is one, which is what
		// the brackets and the placeholder both promise. Optional says what an
		// empty answer means once there is nothing left to propose: no check, no
		// more Compose files, no environment file.
		switch {
		case question.Proposed != "":
			answer = question.Proposed
		case !question.Optional:
			return errors.New("an answer is needed here")
		}
	}
	if question.Kind == KindChoice {
		if err := oneOf(answer, question.Options); err != nil {
			return err
		}
	}
	if question.Kind == KindConfirm {
		yes, err := affirmative(answer)
		if err != nil {
			return err
		}
		return w.commit(func() error { return w.confirmed(yes) })
	}
	return w.commit(func() error { return w.apply(ctx, answer) })
}

// commit runs one answer against a snapshot, so that a rejected answer leaves
// nothing behind and an accepted one can be stepped back out of.
func (w *Wizard) commit(apply func() error) error {
	previous := w.clone()
	w.notes = nil

	if err := apply(); err != nil {
		w.state = previous
		return err
	}
	w.history = append(w.history, previous)
	return nil
}

// question is the current question without the notes and without the proposal's
// place at the head of the candidates, both of which Step attaches.
func (w *Wizard) question() Question {
	switch w.stage {
	case stageID:
		return Question{
			ID: "project.id", Section: SectionProject, Kind: KindText,
			Prompt:   "Project identifier",
			Proposed: config.Slug(filepath.Base(w.host.WorkingDirectory())),
		}

	case stageName:
		return Question{
			ID: "project.name", Section: SectionProject, Kind: KindText,
			Prompt: "Display name", Proposed: w.draft.ID,
		}

	case stageRepositoryPath:
		question := Question{
			ID: "repository.path", Section: SectionRepositories, Kind: KindText,
			Prompt: "Path of the checkout on this machine",
		}
		if len(w.draft.Repositories) == 0 {
			question.Heading = "Repositories"
			question.Detail = []string{
				"Every repository a task may read or write is one of these. Feat never",
				"changes the checkouts themselves; it creates worktrees beside them.",
			}
			// Only the first is proposed from the working directory. Proposing it
			// again would propose a repository already configured.
			question.Proposed = w.host.WorkingDirectory()
		}
		return question

	case stageRepositoryID:
		return Question{
			ID: "repository.id", Section: SectionRepositories, Kind: KindText,
			Prompt:   "Identifier for this repository",
			Proposed: config.Slug(filepath.Base(w.checkout.Root)),
		}

	case stageRepositoryAccess:
		// The first repository is the one a task works in unless the user says
		// otherwise, so it is proposed read_write; a later one is proposed
		// selectable, which is the mode that decides per task rather than for
		// every task at once.
		proposed := string(domain.DefaultAccessReadWrite)
		if len(w.draft.Repositories) > 0 {
			proposed = string(domain.DefaultAccessSelectable)
		}
		return Question{
			ID: "repository.access", Section: SectionRepositories, Kind: KindChoice,
			Prompt: "How does it take part in a task by default?",
			// The two a user picks between most often are first.
			Options:  accessModes(),
			Proposed: proposed,
		}

	case stageRepositoryForge:
		question := Question{
			ID: "repository.forge", Section: SectionRepositories, Kind: KindChoice,
			Prompt: "Where does " + w.pending.ID + " publish merge requests?",
			// none last: it is the answer for a repository Feat leaves alone,
			// and the forges are what the question is about.
			Options:  forgeOptions(),
			Proposed: noForge,
		}
		switch kind := forgeFor(w.checkout.RemoteURL); {
		case kind != "":
			// The inference ADR-071 allows, made where it can be made: a
			// proposal the user accepts into their own file, rather than a value
			// Feat derives behind them. Where it came from is said for the reason
			// every other read proposal says it.
			question.Proposed = kind
			question.Notes = append(question.Notes,
				"read from "+w.checkout.Remote+": "+w.checkout.RemoteURL)
		case w.checkout.RemoteURL != "":
			// The self-hosted case, which is most of why this field is declared
			// at all. Saying that the host was read and not recognised is what
			// tells a user the default is a default rather than a finding.
			question.Notes = append(question.Notes,
				w.checkout.Remote+" is "+w.checkout.RemoteURL+
					", which is not a host Feat recognises; a self-hosted instance is not guessable")
		}
		if w.firstForge() {
			question.Detail = []string{
				"Where a task's finished work is proposed. Feat pushes the task branch and",
				"opens one merge request per changed repository, from this machine with the",
				"forge's own command line and the authentication you already have there — the",
				"agent is never given a token, and the words it writes are shown to you before",
				"anything is sent.",
				"",
				"It is per repository because a repository lives on exactly one forge and a",
				"task may span several. Choose none for a repository Feat never publishes.",
			}
		}
		return question

	case stageAnotherRepository:
		return Question{
			ID: "repository.another", Section: SectionRepositories, Kind: KindConfirm,
			Prompt: "Add another repository?", Proposed: "n",
		}

	case stageEditable:
		return Question{
			ID: "project.editable", Section: SectionRepositories, Kind: KindChoice,
			Detail: []string{
				"A task works in one repository by default, and that one must be editable.",
			},
			Prompt:   "Which repository should a task be able to edit?",
			Options:  identifiers(w.draft.Repositories),
			Proposed: w.draft.Repositories[0].ID,
		}

	case stagePrimary:
		editable := editable(w.draft.Repositories)
		return Question{
			ID: "project.primary", Section: SectionRepositories, Kind: KindChoice,
			Prompt:   "Which repository does a task work in by default?",
			Options:  editable,
			Proposed: editable[0],
		}

	case stageMode:
		return Question{
			ID: "agent.mode", Section: SectionAgent, Kind: KindChoice,
			Heading: "Where the agent runs",
			Detail: []string{
				"host runs Claude Code in the task's own worktree, with no container",
				"boundary. devcontainer runs it as a non-root user in a Compose service,",
				"which is the mode that keeps a task's tools and its dependencies inside",
				"the task.",
			},
			Prompt:   "Execution mode",
			Options:  []string{config.ModeHost, config.ModeDevcontainer},
			Proposed: config.ModeHost,
		}

	case stageComposeFile:
		question := Question{
			ID: "agent.compose", Section: SectionAgent, Kind: KindText,
			Prompt: "Compose file",
		}
		// What is beside the repositories and is not already the application's,
		// which is a list this question could not have before the application was
		// asked about first: until then nothing was claimed, so everything was a
		// candidate and the honest proposal was none (ADR-100).
		found := w.unclaimedComposeFiles()
		question.Candidates = found

		others := found
		if len(w.files) == 0 {
			question.Detail = []string{
				"The Compose files that define the container the agent works in, and the",
				"service it runs in. Feat starts that service for a task and never gives it",
				"Docker. They are not the application's own Compose files: those are the ones",
				"you have just been asked about, and they are left out of what is offered here.",
			}
			if len(others) > 0 {
				question.Proposed, others = others[0], others[1:]
			}
		} else {
			// Finishing is only offered once there is one, because the section is
			// meaningless without it. The repeat proposes nothing, for the reason
			// the application's loop proposes nothing: an empty answer means "no
			// more", and a proposal is what an empty answer takes (ADR-077).
			question.Prompt, question.Optional = overridePrompt(""), true
		}
		if len(others) > 0 {
			note := "others found beside your repositories: " + strings.Join(others, ", ")
			if question.Proposed == "" {
				note += "; press tab to use one of them"
			}
			question.Notes = append(question.Notes, note)
		}
		if claimed := w.claimedComposeFiles(); len(claimed) > 0 {
			// Why a file the user knows is there is not in the list. Without it,
			// a file Feat deliberately withheld and a file Feat never found look
			// identical from here.
			question.Notes = append(question.Notes,
				"not offered, because the application already claims them: "+strings.Join(claimed, ", "))
		}
		return question

	case stageService:
		return Question{
			ID: "agent.service", Section: SectionAgent, Kind: KindText,
			Prompt:   "Service the agent runs in",
			Proposed: suggestService(w.host.ComposeServices(w.files...)),
		}

	case stageUser:
		return Question{
			ID: "agent.user", Section: SectionAgent, Kind: KindText,
			Prompt: "Container user the agent runs as",
		}

	case stageMount:
		repository := w.draft.Repositories[w.mount]
		question := Question{
			ID: "agent.mount", Section: SectionAgent, Kind: KindText,
			Prompt: "Mount point for " + repository.ID,
			// Feat generates this mount itself, so a path of its own is a
			// legitimate answer where the files state none — unlike the runtime
			// path, which has to match where somebody else's services expect the
			// code. It is only an invention when the files do say where this
			// repository goes and are not asked.
			Proposed: "/srv/" + repository.ID,
		}
		if composition := w.agentComposition(repository.HostPath); composition.ContainerPath != "" {
			question.Proposed = composition.ContainerPath
			// Where the proposal came from, said in the one case that used to say
			// nothing. A transcription is safe to accept and an invention is only
			// safe where the files mount this repository nowhere, and until this
			// note the two arrived looking identical (ADR-082).
			question.Notes = append(question.Notes, readFrom(w.draft.Execution.ComposeFiles))
		} else if len(composition.Undecided) > 0 {
			// Why the default is the default. An entry Feat left unread is not an
			// entry that said nothing, and a user who cannot tell the two apart
			// has no way to know whether their own file already answered this.
			question.Notes = append(question.Notes,
				"not read, because they interpolate: "+strings.Join(composition.Undecided, "; "))
		}
		if w.firstMount() {
			question.Detail = []string{
				"Where each repository's task worktrees are mounted in the devcontainer.",
				"",
				"WARNING: Feat generates an override using this path as the worktree's",
				"source. In case your devcontainer's Compose files already mount the",
				"repository code itself, you have to choose the exact same mount point here,",
				"so that it gets overridden by Compose. Otherwise the worktree and the",
				"repository will be mounted simultaneously.",
			}
			return question
		}
		// Repeated rather than said once, which the type's own convention does not
		// do. The warning is what makes an answer right, and the second repository
		// is where it is needed most: under the convention that question is nothing
		// but "Mount point for <id>" (ADR-082).
		question.Detail = []string{
			"WARNING: still the devcontainer, and still an override. Where these Compose",
			"files already mount this repository's code itself, choose the exact same",
			"mount point, so that it gets overridden by Compose. Otherwise the worktree",
			"and the repository will be mounted simultaneously.",
		}
		return question

	case stageClaudeVolume:
		return Question{
			ID: "agent.volume", Section: SectionAgent, Kind: KindConfirm,
			Detail: []string{
				"Claude's own configuration can live in a volume of its own, so that one",
				"interactive login is not your ~/.claude in every task container.",
			},
			Prompt: "Give Claude a configuration volume?", Proposed: "y",
		}

	case stageVolumeName:
		return Question{
			ID: "agent.volume.name", Section: SectionAgent, Kind: KindText,
			Prompt: "Volume name", Proposed: "feat-claude",
		}

	case stageRuntimeWanted:
		return Question{
			ID: "runtime.wanted", Section: SectionServices, Kind: KindConfirm,
			Heading: "Application services",
			// What the agent's environment is and how it differs from this is
			// drawn on the agent's own Compose question, which is asked after
			// these and names the files this section claimed. Saying it here as
			// well presumed both halves before the user had been shown either,
			// and a project whose agent runs on this host has no second
			// environment to be told apart from.
			Detail: []string{
				"The runtime for the application under development. Feat creates one per",
				"task, and in this version its services start only when you ask.",
			},
			Prompt: "Does a task run application services?", Proposed: "n",
		}

	case stageRuntimeRepository:
		repository := w.draft.Repositories[w.contributor]
		question := Question{
			ID: "runtime.repository", Section: SectionServices, Kind: KindConfirm,
			Prompt: "Does " + repository.ID + " bring Compose files to the application?",
		}
		question.Proposed = "n"
		if len(w.host.ComposeFiles(repository.HostPath)) > 0 {
			question.Proposed = "y"
		}
		if w.firstContributor() {
			question.Detail = []string{
				"Each repository brings its own Compose files, and Feat generates the",
				"include document that joins them. Listing them all together instead would",
				"resolve every relative path against one repository's directory, so a second",
				"repository's services would be built from the first.",
			}
		}
		return question

	case stageRuntimeCompose:
		question := Question{
			ID: "runtime.compose", Section: SectionServices, Kind: KindText,
			Prompt: "Compose file for " + w.draft.Repositories[w.contributor].ID,
		}
		// Every file beside the repository that has not been answered yet, at both
		// ends of the loop: all of them at the first question, and what is left of
		// them at each repeat.
		found := w.unansweredContributions()
		question.Candidates = found

		others := found
		if len(w.files) == 0 {
			if len(others) > 0 {
				question.Proposed, others = others[0], others[1:]
			}
		} else {
			// The repeat offers what is left and proposes none of it. The proposal
			// is what an empty answer takes, and an empty answer here means "no
			// more": they were two meanings for one key, and finishing lost — a
			// user pressing Enter at "blank to finish [/some/path]" added the
			// bracketed file instead, twice, and ended up with an application's
			// files defining the container their agent runs in. Tab is where the
			// rest of the files went, so the two no longer share a key (ADR-077).
			question.Prompt = overridePrompt(w.draft.Repositories[w.contributor].ID)
			question.Optional = true
		}
		// The files that are not in the field, named in the same words wherever
		// the loop is, because they are the same thing in both places. Saying it
		// only once left the repeat as a prompt about finishing over an empty
		// field, which reads as a loop with nothing left in it — and the reason
		// this loop repeats is that Compose merges a base with the overrides
		// beside it.
		if len(others) > 0 {
			note := "others found beside it: " + strings.Join(others, ", ")
			if question.Proposed == "" {
				// And the key that reaches them, where nothing in the field shows
				// what it would give. ADR-077 left this clause to the asker because
				// one of the two had no such key to name; both have one now, and a
				// sentence each was a sentence that could drift (ADR-084). A
				// question that proposes something has that value under the cursor
				// already and needs no sentence about it.
				note += "; press tab to use one of them"
			}
			question.Notes = append(question.Notes, note)
		}
		return question

	case stageRuntimeServices:
		return Question{
			ID: "runtime.services", Section: SectionServices, Kind: KindText,
			Prompt: "Services Feat manages from " + w.draft.Repositories[w.contributor].ID,
			// The services that run this repository's code, rather than every
			// service its files declare: what is left out is named in a note, and
			// is still an answer a user may type (ADR-100).
			Proposed: strings.Join(w.running(), " "),
		}

	case stageRuntimeMount:
		repository := w.draft.Repositories[w.contributor]
		question := Question{
			ID: "runtime.mount", Section: SectionServices, Kind: KindText,
			Prompt:   "Where those services expect " + repository.ID + "'s source",
			Proposed: w.composition.ContainerPath,
			Optional: true,
		}
		if w.composition.ContainerPath == "" {
			// Asked in every execution mode, and asked even when nothing could be
			// read: this is the value that decides whether the user's own services
			// run their task, and a project whose agent is host-native has services
			// all the same (ADR-065 evidence 1 and 6).
			question.Prompt += ", or blank if they do not"
		} else {
			// The same finding as the agent mount's, for the same reason: this is
			// the strongest thing the flow learns about its own proposals, and it
			// was the one proposal that arrived without saying where it came from
			// (ADR-082).
			question.Notes = append(question.Notes, readFrom(w.contribution.ComposeFiles))
		}
		if w.firstRuntimeMount() {
			// The field with no safety net gets an explanation at all, which is the
			// inversion ADR-082 corrects: the agent's path, whose failure a launch
			// refuses, had a detail block and this one had none. It is the same
			// warning in the same words, and it ends where the two fields differ —
			// nothing here refuses a mismatch.
			question.Detail = []string{
				"Where this repository's own services expect its source.",
				"",
				"WARNING: Feat generates an override using this path as the worktree's",
				"source. In case your application's Compose files already mount the",
				"repository code itself, you have to choose the exact same mount point here,",
				"so that it gets overridden by Compose. Otherwise the worktree and the",
				"ordinary checkout will be mounted simultaneously, your services will go on",
				"reading the checkout, and there will be no error anywhere.",
			}
			return question
		}
		question.Detail = []string{
			"WARNING: still an override, and still these services' own mount point. In",
			"case their Compose files already mount this repository's code itself, choose",
			"the exact same mount point, so that it gets overridden by Compose. Otherwise",
			"the worktree and the ordinary checkout will be mounted simultaneously, with",
			"no error anywhere.",
		}
		return question

	case stageRuntimeReachable:
		return Question{
			ID: "runtime.reachable", Section: SectionServices, Kind: KindText,
			Prompt:   "Which of them do you reach from this machine?",
			Proposed: strings.Join(intersect(w.composition.Reachable, w.contribution.Services), " "),
			Optional: true,
		}

	case stageRuntimeEnvFile:
		question := Question{
			ID: "runtime.env", Section: SectionServices, Kind: KindText,
			Prompt: "Environment file, or blank for none", Optional: true,
		}
		if len(w.envFiles) == 0 {
			question.Detail = []string{
				"Environment files are passed to Compose by path. Feat never reads what is",
				"in them, and never copies a value out of them into anything it generates.",
			}
		}
		return question

	case stageTracker:
		return Question{
			ID: "tracker.command", Section: SectionTracker, Kind: KindText,
			Heading: "Tickets",
			Detail: []string{
				"Where this project's tickets come from: a command of yours that prints them",
				"as JSON. `feat project tickets` lists what it printed, and",
				"`feat implement --ticket <reference>` composes a task brief from one, which",
				"you read and edit before it is what the agent is told to do.",
				"",
				"Feat runs it on this machine as you and expands nothing, so name a program on",
				"your path or write the path in full. It is passed no filter: which tickets are",
				"yours is the command's decision, and `feat doctor` runs it and checks that",
				"what it printed is the shape Feat publishes.",
				"",
				"Leave it blank for a project whose tasks are all written by hand, or for one",
				"whose tracker command you have not written yet. The section can be added to",
				"the file afterwards.",
			},
			Prompt:   "Command that prints your tickets, or blank for none",
			Optional: true,
		}

	}
	return Question{}
}

// apply records one text or choice answer and advances.
func (w *Wizard) apply(ctx context.Context, answer string) error {
	switch w.stage {
	case stageID:
		if err := domain.ProjectID(answer).Validate(); err != nil {
			return err
		}
		if err := w.unconfigured(answer); err != nil {
			return err
		}
		w.draft.ID = answer
		w.stage = stageName

	case stageName:
		w.draft.Name = answer
		w.stage = stageRepositoryPath

	case stageRepositoryPath:
		// The answer itself is not kept. What matters is the working tree Git
		// resolved from it, because that is the path a repository is configured
		// by: an answer naming a subdirectory configures the checkout it is in.
		path, err := w.host.Absolute(answer)
		if err != nil {
			return err
		}
		checkout, err := w.host.Inspect(ctx, path)
		if err != nil {
			return err
		}
		w.checkout = checkout
		w.sawRemote = w.sawRemote || checkout.Remote != ""
		w.notes = describe(checkout)
		w.stage = stageRepositoryID

	case stageRepositoryID:
		if err := domain.RepositoryID(answer).Validate(); err != nil {
			return err
		}
		for _, existing := range w.draft.Repositories {
			if existing.ID == answer {
				return fmt.Errorf("repository %s is already part of this project", answer)
			}
		}
		w.pending = config.DraftRepository{
			ID:            answer,
			HostPath:      w.checkout.Root,
			DefaultBranch: orDefault(w.checkout.DefaultBranch, defaultBranchName),
			Remote:        orDefault(w.checkout.Remote, defaultRemoteName),
		}
		w.stage = stageRepositoryAccess

	case stageRepositoryAccess:
		w.pending.DefaultAccess = answer
		if !domain.DefaultAccess(answer).Permits(domain.TaskAccessReadWrite) {
			// A repository no task may ever write to is one no task may ever
			// publish, so there is nothing to ask it. The rule is the domain's
			// rather than a mode named here: read-only is the one mode a task
			// cannot promote, and publication refuses a binding that is not
			// read-write in every place it looks at one.
			w.keepRepository()
			break
		}
		w.stage = stageRepositoryForge

	case stageRepositoryForge:
		// none is the absence of the section rather than a value in it: the
		// configuration has no forge kind meaning "nowhere", and a repository
		// that publishes nowhere is one the section is left off (ADR-071).
		if answer != noForge {
			w.pending.Forge = answer
		}
		w.keepRepository()

	case stageEditable:
		for i, repository := range w.draft.Repositories {
			if repository.ID == answer {
				w.draft.Repositories[i].DefaultAccess = string(domain.DefaultAccessReadWrite)
			}
		}
		w.draft.Primary = answer
		w.stage = stageRuntimeWanted

	case stagePrimary:
		w.draft.Primary = answer
		w.stage = stageRuntimeWanted

	case stageMode:
		w.draft.Execution.Mode = answer
		if answer != config.ModeDevcontainer {
			w.stage = stageTracker
			break
		}
		w.files = nil
		w.stage = stageComposeFile

	case stageComposeFile:
		if answer == "" {
			w.draft.Execution.ComposeFiles = w.files
			w.notes = w.declaredServices()
			w.stage = stageService
			break
		}
		if err := w.addFile(answer); err != nil {
			return err
		}

	case stageService:
		w.draft.Execution.Service = answer
		w.stage = stageUser

	case stageUser:
		if name, _, _ := strings.Cut(answer, ":"); name == "root" || name == "0" {
			return errors.New("the agent must not run as root in the devcontainer")
		}
		w.draft.Execution.User = answer
		w.mount = -1
		w.stage = stageMount
		w.nextMount()

	case stageMount:
		if err := containerPath(answer); err != nil {
			return err
		}
		if err := w.unoccupied(answer); err != nil {
			return err
		}
		w.draft.Repositories[w.mount].AgentContainerPath = answer
		w.nextMount()

	case stageVolumeName:
		w.draft.Execution.ClaudeConfigVolume = answer
		w.stage = stageTracker

	case stageRuntimeCompose:
		if answer == "" {
			if len(w.files) == 0 {
				return errors.New("name at least one Compose file, or say this repository brings none")
			}
			w.contribution.ComposeFiles = w.files
			w.readComposition()
			w.stage = stageRuntimeServices
			break
		}
		if err := w.addFile(answer); err != nil {
			return err
		}

	case stageRuntimeServices:
		services := strings.Fields(answer)
		if len(services) == 0 {
			return errors.New("name at least one, separated by spaces")
		}
		w.contribution.Services = services
		w.notes = w.provenance(services)
		w.stage = stageRuntimeMount

	case stageRuntimeMount:
		if answer != "" {
			if err := containerPath(answer); err != nil {
				return err
			}
		}
		w.contribution.ContainerPath = answer
		w.stage = stageRuntimeReachable

	case stageRuntimeReachable:
		reachable := strings.Fields(answer)
		for _, service := range reachable {
			if !containsString(w.contribution.Services, service) {
				return fmt.Errorf("%q is not one of the services you named: %s",
					service, strings.Join(w.contribution.Services, ", "))
			}
		}
		w.contribution.Reachable = reachable
		w.keepContribution()
		w.nextContributor()

	case stageRuntimeEnvFile:
		if answer == "" {
			w.draft.Runtime = &config.DraftRuntime{EnvFiles: w.envFiles}
			w.stage = stageMode
			break
		}
		path, err := w.host.Absolute(answer)
		if err != nil {
			return err
		}
		w.envFiles = append(w.envFiles, path)
		w.notes = w.existence(path)

	case stageTracker:
		if answer == "" {
			w.stage = stageComplete
			break
		}
		command, err := commandWords(answer)
		if err != nil {
			return err
		}
		w.draft.Tracker = command
		w.stage = stageComplete

	}
	return nil
}

// confirmed records one yes-or-no answer and advances.
func (w *Wizard) confirmed(yes bool) error {
	switch w.stage {
	case stageAnotherRepository:
		if yes {
			w.stage = stageRepositoryPath
			return nil
		}
		if !w.sawRemote {
			// A repository with no remote cannot have a base resolved from one,
			// and the default policy resolves from one. This is the only value
			// the wizard decides for the user, and it decides it from what it
			// found rather than from a preference.
			w.draft.BasePolicy = config.PolicyLocal
			w.notes = []string{
				"No repository has a remote, so bases are resolved from the local default branch.",
			}
		}
		w.stage = w.afterRepositories()

	case stageClaudeVolume:
		if yes {
			w.stage = stageVolumeName
			return nil
		}
		w.stage = stageTracker

	case stageRuntimeWanted:
		if !yes {
			w.stage = stageMode
			return nil
		}
		w.contributor = -1
		w.nextContributor()

	case stageRuntimeRepository:
		if !yes {
			w.nextContributor()
			return nil
		}
		w.files = nil
		w.contribution = config.DraftRepositoryRuntime{}
		w.composition = Composition{}
		w.stage = stageRuntimeCompose
	}
	return nil
}

// nextContributor moves to the next repository the application may be composed
// of, or past them to the runtime's own questions.
//
// Every repository is asked, whatever its access and whatever the execution
// mode. A repository omitted from tasks by default still has an ordinary
// checkout with Compose files in it, and a host-native agent's project has
// application containers exactly as a containerised one's does.
func (w *Wizard) nextContributor() {
	if w.contributor+1 < len(w.draft.Repositories) {
		w.contributor, w.stage = w.contributor+1, stageRuntimeRepository
		return
	}
	w.envFiles = nil
	w.stage = stageRuntimeEnvFile
}

// firstContributor reports whether the repository under the cursor is the first
// one asked about, which is the one the explanation belongs to.
func (w *Wizard) firstContributor() bool { return w.contributor == 0 }

// keepRepository records the repository being answered and moves to the offer of
// another.
//
// Two answers end a repository, because one of them is asked only sometimes: a
// repository a task may write to is asked where it publishes, and one that can
// never be written to is finished at its access mode.
func (w *Wizard) keepRepository() {
	w.draft.Repositories = append(w.draft.Repositories, w.pending)
	w.pending = config.DraftRepository{}
	w.stage = stageAnotherRepository
}

// firstForge reports whether the repository being answered is the first one
// asked where it publishes, which is the one the explanation belongs to.
//
// Not the first repository, for the reason firstRuntimeMount is not either: a
// repository no task may write to is never asked, so the group starts at the
// first one that may be.
func (w *Wizard) firstForge() bool {
	for _, repository := range w.draft.Repositories {
		if domain.DefaultAccess(repository.DefaultAccess).Permits(domain.TaskAccessReadWrite) {
			return false
		}
	}
	return true
}

// keepContribution records the answered contribution on its repository.
//
// The Compose files are written relative to the checkout where they are inside
// it, which is how a repository names the files it brings: they resolve against
// its own checkout, so a project moved to another machine needs one path
// changed rather than one per file.
func (w *Wizard) keepContribution() {
	repository := &w.draft.Repositories[w.contributor]
	contribution := w.contribution
	contribution.ComposeFiles = relativeTo(repository.HostPath, contribution.ComposeFiles)
	repository.Runtime = &contribution
}

// readComposition reads what the answered Compose files propose.
//
// A repository's application files are read against that repository's own
// checkout, which is both the directory their relative paths resolve against
// and the repository being asked about: Feat gives this repository's include
// entry that same directory, so the two are one here.
func (w *Wizard) readComposition() {
	repository := w.draft.Repositories[w.contributor]
	w.composition = w.host.Compose(repository.HostPath, repository.HostPath, w.files...)

	if len(w.composition.Services) > 0 {
		w.notes = append(w.notes, "services defined there: "+strings.Join(w.composition.Services, ", "))
	}
	if idle := w.idle(); len(idle) > 0 {
		// Why the proposal is shorter than the list above it. Feat addresses the
		// managed services by name and Compose starts what they depend on, so a
		// database left out of the proposal still runs — what it is spared is
		// being created, started, stopped, and destroyed per task, and being
		// given an override entry for a worktree it has no use for (ADR-100).
		w.notes = append(w.notes,
			"not proposed, because they run none of this repository's code: "+strings.Join(idle, ", ")+
				" — Feat starts whatever the managed services depend on, and name one here to "+
				"manage it yourself")
	}
	if len(w.composition.Undecided) > 0 {
		// Named rather than resolved. Feat never interpolates a "${...}", so an
		// entry containing one is a value it could not derive, and saying which
		// one is what makes the missing proposal actionable.
		w.notes = append(w.notes,
			"not read, because they interpolate: "+strings.Join(w.composition.Undecided, "; "))
	}
}

// agentComposition reads what the agent's own Compose files say about one
// repository.
//
// Two directories rather than one, because here they are two. The paths in
// those files resolve against the first file's own directory, which is what the
// daemon passes Compose as the project directory when it starts the agent
// (ADR-033), so reading them any other way would answer a different question
// from the one Compose will be asked. What is asked about is a repository, and
// each is asked separately: the agent's container holds every repository a task
// takes, and one file can name a different path for each of them.
//
// It reads the files again for each question rather than keeping what it read.
// The answers before this one can be stepped back into and given differently,
// and a proposal derived from a file the user has since replaced is worse than
// one derived twice.
func (w *Wizard) agentComposition(hostPath string) Composition {
	files := w.draft.Execution.ComposeFiles
	if len(files) == 0 || hostPath == "" {
		return Composition{}
	}
	return w.host.Compose(filepath.Dir(files[0]), hostPath, files...)
}

// running are the services that run this repository's code: the ones whose
// files mount it, and the ones built from it.
//
// It is what the managed-services question proposes, and it is narrower than
// what the files declare. A managed service is one Feat creates, starts, stops,
// and destroys for a task, and one the generated override writes an entry for —
// so a database, which runs none of the project's code and has no worktree to be
// given, is not one a task manages. It still runs: Feat addresses the managed
// services by name and Compose brings up what they depend on (ADR-100).
//
// The order is the composition's, which is the order the files declare, so that
// the proposal reads the way the file does.
func (w *Wizard) running() []string {
	var found []string
	for _, service := range w.composition.Services {
		if containsString(w.composition.Mounted, service) ||
			containsString(w.composition.Baked, service) {
			found = append(found, service)
		}
	}
	return found
}

// idle are the services the files declare that run none of this repository's
// code, which is the rest of them.
func (w *Wizard) idle() []string {
	var found []string
	running := w.running()
	for _, service := range w.composition.Services {
		if !containsString(running, service) {
			found = append(found, service)
		}
	}
	return found
}

// provenance says which of the named services build this repository into their
// image rather than mounting it.
//
// It is said while the user is deciding, because it changes what the next
// question is worth to them. Feat points such a service's build context at the
// task's worktree, so it runs the task's code with no mount at all — and a
// container path it never reads is a container path this repository may not have
// (ADR-065 evidence 4).
func (w *Wizard) provenance(services []string) []string {
	baked := intersect(w.composition.Baked, services)
	if len(baked) == 0 {
		return nil
	}
	return []string{"built from this repository rather than mounting it, so Feat builds them from the " +
		"task's worktree and a change shows once the image is built again: " + strings.Join(baked, ", ")}
}

// afterRepositories is which question follows the last repository: the primary
// repository is asked for only when the answers left a choice.
//
// The choice is limited to the repositories a task can edit, because the primary
// repository is where the agent works: a project whose primary repository can
// never be written to has no editable workspace at all (FR-PROJ-003).
func (w *Wizard) afterRepositories() stage {
	switch editable := editable(w.draft.Repositories); len(editable) {
	case 0:
		return stageEditable
	case 1:
		w.draft.Primary = editable[0]
		return stageRuntimeWanted
	default:
		return stagePrimary
	}
}

// nextMount moves to the next repository that is mounted, or past the mounts.
//
// A repository that takes no part in a task by default is not mounted, so there
// is nothing to ask about it.
func (w *Wizard) nextMount() {
	for i := w.mount + 1; i < len(w.draft.Repositories); i++ {
		if domain.DefaultAccess(w.draft.Repositories[i].DefaultAccess) == domain.DefaultAccessOmitted {
			continue
		}
		w.mount, w.stage = i, stageMount
		return
	}
	w.stage = stageClaudeVolume
}

// unoccupied rejects a mount point that overlaps one another repository already
// has.
//
// Configuration refuses two repositories mounted inside one another: it does not
// fail at Compose, it produces a container where one repository shadows part of
// another, which is far harder to recognise later than a refusal now. That check
// runs when the composed file is loaded back, which is after the last question —
// so a conversation that met it there ended, taking every answer with it. This
// asks the same question of the answer being given, in the words of the question
// that can still be answered differently, and the rule itself is
// config.PathsOverlap so that the two cannot drift apart.
//
// It became reachable when this stopped being a made-up value: two proposals of
// "/srv/<id>" are always siblings, and a path read out of the agent's own
// Compose files can be the parent of the next repository's default.
func (w *Wizard) unoccupied(answer string) error {
	for i, repository := range w.draft.Repositories {
		if i == w.mount || repository.AgentContainerPath == "" {
			continue
		}
		if config.PathsOverlap(answer, repository.AgentContainerPath) {
			return fmt.Errorf(
				"%s overlaps %s, where %s is mounted: two repositories cannot be mounted inside one another",
				answer, repository.AgentContainerPath, repository.ID)
		}
	}
	return nil
}

// firstMount reports whether the mount under the cursor is the first one asked
// for, which is the one the fullest explanation belongs to.
func (w *Wizard) firstMount() bool {
	for i := range w.draft.Repositories[:w.mount] {
		if domain.DefaultAccess(w.draft.Repositories[i].DefaultAccess) != domain.DefaultAccessOmitted {
			return false
		}
	}
	return true
}

// firstRuntimeMount reports whether the repository under the cursor is the
// first one asked where its own services expect its source.
//
// Not the first repository: a repository that brings no Compose files is never
// asked, so the group starts at the first that does. A contribution is recorded
// on its repository once that repository's questions are done, which is what
// makes an earlier one visible from here.
func (w *Wizard) firstRuntimeMount() bool {
	for i := range w.draft.Repositories[:w.contributor] {
		if w.draft.Repositories[i].Runtime != nil {
			return false
		}
	}
	return true
}

// readFrom names the files a proposal was read out of.
//
// It is the note the flow owed its strongest finding. A path Feat transcribed
// out of the user's own Compose files and a path Feat made up are two proposals
// a user has to treat differently — accepting the first is always right, and
// accepting the second where the files did say something is the mismatch this
// field fails silently on — and they arrived on identical questions (ADR-082).
func readFrom(files []string) string {
	return "read from " + strings.Join(files, ", ")
}

// addFile records one Compose file, saying so when it is not there yet.
func (w *Wizard) addFile(answer string) error {
	path, err := w.host.Absolute(answer)
	if err != nil {
		return err
	}
	w.files = append(w.files, path)
	w.notes = w.existence(path)
	return nil
}

// existence reports a path that does not exist, and says nothing about one that
// does.
//
// It is not a refusal. A Compose file that is generated, or that lives on a
// branch not checked out right now, is still the right value, and `feat doctor`
// asks the same question again later.
func (w *Wizard) existence(path string) []string {
	if w.host.Exists(path) {
		return nil
	}
	return []string{path + " does not exist yet"}
}

// declaredServices reports what the answered Compose files define.
func (w *Wizard) declaredServices() []string {
	services := w.host.ComposeServices(w.files...)
	if len(services) == 0 {
		return nil
	}
	return []string{"services defined there: " + strings.Join(services, ", ")}
}

// unansweredContributions are the Compose files found beside the repository
// being asked about that this loop has not been given yet.
//
// It never offers another repository's file: what a repository brings is its
// own. It drops the ones already answered because the loop asks the same
// question again after each of them, and a list that offered a file the user has
// just given would be proposing a duplicate as the way to finish.
func (w *Wizard) unansweredContributions() []string {
	var found []string
	for _, path := range w.host.ComposeFiles(w.draft.Repositories[w.contributor].HostPath) {
		if containsString(w.files, path) {
			continue
		}
		found = append(found, path)
	}
	return found
}

// devcontainerDir is the directory the Dev Containers specification puts a
// project's container definition in.
//
// It is a place worth looking rather than a rule about what is there: Feat
// neither reads `devcontainer.json` nor implements that specification, and its
// own `devcontainer` mode means the agent runs in a configured Compose service.
// What it is used for here is an ordering — a Compose file kept in that
// directory is a better first proposal for the agent's own container than a file
// at the root of a checkout, which is ordinarily the application's.
const devcontainerDir = ".devcontainer"

// unclaimedComposeFiles are the Compose files found beside the project's
// repositories that nothing has spoken for: not the application's, and not
// already answered in this loop.
//
// Every repository is looked beside, rather than the primary one only. The
// agent's container is the project's and is kept wherever the user keeps it, and
// a project whose devcontainer sits beside its second repository is not unusual.
//
// The ones in a `.devcontainer` directory come first, because that is the
// directory the thing this question is about is ordinarily kept in. The rest
// follow in the order they were found, which is Compose's own order of
// preference.
func (w *Wizard) unclaimedComposeFiles() []string {
	claimed := w.claimedComposeFiles()

	var devcontainer, beside []string
	for _, repository := range w.draft.Repositories {
		for _, path := range w.host.ComposeFiles(repository.HostPath) {
			if containsString(w.files, path) || containsString(claimed, path) ||
				containsString(devcontainer, path) || containsString(beside, path) {
				continue
			}
			if filepath.Base(filepath.Dir(path)) == devcontainerDir {
				devcontainer = append(devcontainer, path)
				continue
			}
			beside = append(beside, path)
		}
	}
	return append(devcontainer, beside...)
}

// claimedComposeFiles are the Compose files the application already took.
//
// They are read back off the repositories rather than remembered, because that
// is where the answer was recorded and because stepping back out of one of those
// answers has to take the claim with it. A contribution's files were rewritten
// relative to its checkout when it was kept, so they are resolved against it
// again here (keepContribution).
func (w *Wizard) claimedComposeFiles() []string {
	var claimed []string
	for _, repository := range w.draft.Repositories {
		if repository.Runtime == nil {
			continue
		}
		for _, file := range repository.Runtime.ComposeFiles {
			if !filepath.IsAbs(file) {
				file = filepath.Join(repository.HostPath, file)
			}
			claimed = append(claimed, file)
		}
	}
	return claimed
}

// unconfigured reports a project that already has a configuration.
//
// Both extensions are looked for, because either is a configuration this project
// already has, and overwriting one is not this wizard's business.
func (w *Wizard) unconfigured(id string) error {
	existing, err := config.Find(w.configDir, id)
	switch {
	case err == nil:
		return fmt.Errorf(
			"project %s is already configured at %s: edit that file, or remove it before writing another",
			id, existing)
	case errors.Is(err, config.ErrNotFound):
		return nil
	default:
		return err
	}
}

// Review renders the answers, loads the rendering back, and returns the text
// that survived it.
//
// What comes back is therefore a configuration Feat accepts rather than a
// proposal that might not be: a rule the questions did not cover fails while the
// answers still exist, naming its field, rather than after the file is on disk.
func (w *Wizard) Review() (Review, error) {
	file, err := config.File(w.configDir, w.draft.ID)
	if err != nil {
		return Review{}, err
	}
	_, rendered, err := w.draft.Config(file, w.resolve)
	if err != nil {
		return Review{}, err
	}
	return Review{Path: file, Text: rendered}, nil
}

// describe reports what Git said about a checkout, and what Feat assumed where
// Git said nothing.
//
// The two are separate sentences on purpose. Once a value is in the file, one
// that was established and one that was assumed look identical, and the moment
// to tell them apart is while the user can still see where each came from.
func describe(checkout Checkout) []string {
	notes := []string{"a Git repository at " + checkout.Root}
	switch {
	case checkout.Remote == "" && checkout.DefaultBranch == "":
		notes = append(notes, fmt.Sprintf("no remote and no branch checked out; %s and %s are assumed",
			defaultRemoteName, defaultBranchName))
	case checkout.Remote == "":
		notes = append(notes, fmt.Sprintf("no remote; the default branch %s is assumed to be local",
			checkout.DefaultBranch))
	case checkout.DefaultBranch == "":
		notes = append(notes, fmt.Sprintf("remote %s, and no branch checked out; %s is assumed",
			checkout.Remote, defaultBranchName))
	default:
		notes = append(notes, fmt.Sprintf("remote %s, default branch %s",
			checkout.Remote, checkout.DefaultBranch))
	}
	return notes
}

// overridePrompt asks a file loop's second question and every one after it.
//
// Both loops asked for the next file with the same words as the first and
// "(blank to finish)" on the end, so the repeat said what to do with it and
// never what it was: a user who had given the one Compose file they knew about
// had no reason to think another existed. Naming it does that in the place a
// user reads at the moment of answering, and the noun is Compose's rather than
// Feat's — `compose.override.yaml` is the file Compose itself picks up beside a
// base, and overriding an earlier file is exactly what a later one does.
//
// A repository is named where there is one, because the application's loop runs
// once per repository and the answer belongs to whichever it is on.
func overridePrompt(repository string) string {
	if repository == "" {
		return "Compose override file (blank to finish)"
	}
	return "Compose override file for " + repository + " (blank to finish)"
}

// candidates are the values an asker may complete a text answer to.
//
// The proposal is the head of them, because it is the value an empty answer
// takes: a list whose first entry was something else would be two answers to one
// question, and the two askers would stop being the same conversation. The rest
// are what the flow found beside it — the lists it derives and has until now had
// nowhere to put, because a proposal is one value and a question has one of them
// (ADR-077).
func candidates(question Question) []string {
	if question.Kind != KindText {
		return nil
	}
	found := make([]string, 0, len(question.Candidates)+1)
	if question.Proposed != "" {
		found = append(found, question.Proposed)
	}
	for _, candidate := range question.Candidates {
		if candidate == "" || containsString(found, candidate) {
			continue
		}
		found = append(found, candidate)
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

// oneOf rejects an answer a closed question does not offer.
func oneOf(answer string, options []string) error {
	for _, option := range options {
		if answer == option {
			return nil
		}
	}
	return fmt.Errorf("%q is not one of %s", answer, strings.Join(options, ", "))
}

// affirmative reads a yes-or-no answer.
//
// An answer that is neither is refused rather than read as the proposal. Half of
// these questions propose "yes", and one of them decides whether a file is
// written: a word the question did not offer is somebody answering a different
// question, and taking it as agreement is the one reading that cannot be taken
// back.
func affirmative(answer string) (bool, error) {
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, errors.New(`answer "y" or "n"`)
	}
}

// containerPath rejects a mount point that is not a usable absolute path inside
// a container. The overlap rules are configuration's, and the draft is validated
// against them before anything is written.
func containerPath(value string) error {
	switch {
	case !strings.HasPrefix(value, "/"):
		return errors.New("a mount point is an absolute path inside the container, such as /srv/api")
	case value == "/":
		return errors.New("a mount point must not be the container's filesystem root")
	}
	return nil
}

// noForge is the answer for a repository Feat never publishes.
//
// It is not a forge kind. The configuration has no value meaning "nowhere" —
// the section is simply absent — so this is a word the question offers and the
// answer drops, rather than one that reaches a file (ADR-071).
const noForge = "none"

// forgeOptions are the answers the forge question offers: the forges a
// repository may declare, and then none.
//
// The forges are the domain's list rather than one written here, so that the
// question cannot offer a kind the configuration would refuse, nor miss one it
// would accept (ADR-100).
func forgeOptions() []string {
	kinds := domain.ForgeKinds()
	options := make([]string, 0, len(kinds)+1)
	for _, kind := range kinds {
		options = append(options, string(kind))
	}
	return append(options, noForge)
}

// forgeFor proposes the forge a remote URL names, or nothing where its host is
// not one Feat recognises.
//
// Exactly those two hosts, and no subdomain of either: a GitHub Enterprise
// instance and a self-hosted GitLab are both on hosts nobody can derive a forge
// from, and this is a proposal a user accepts rather than a value Feat writes —
// so being wrong costs them a correction and being silent costs them one
// keystroke (ADR-071).
func forgeFor(remoteURL string) string {
	switch remoteHost(remoteURL) {
	case "github.com":
		return string(domain.ForgeGitHub)
	case "gitlab.com":
		return string(domain.ForgeGitLab)
	default:
		return ""
	}
}

// remoteHost is the host a Git remote points at, in either of the two forms a
// clone leaves behind, and empty for a remote that names no host at all.
//
// The second form is the reason this is not net/url: `git@github.com:acme/api`
// is what an SSH clone writes and it is not a URL, so parsing it as one yields a
// scheme of "git@github.com" and no host. A path with no colon before its first
// slash is a local remote, which names no host and proposes nothing.
func remoteHost(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}

	if scheme := strings.Index(remote, "://"); scheme >= 0 {
		authority := remote[scheme+len("://"):]
		if slash := strings.Index(authority, "/"); slash >= 0 {
			authority = authority[:slash]
		}
		return hostOf(authority)
	}

	colon := strings.Index(remote, ":")
	if slash := strings.Index(remote, "/"); colon < 0 || (slash >= 0 && slash < colon) {
		return ""
	}
	return hostOf(remote[:colon])
}

// hostOf takes the host out of an authority: neither the user in front of it nor
// the port behind it is part of one.
func hostOf(authority string) string {
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	// A bracketed authority is an IPv6 address, whose own colons are not a port
	// separator. Neither form names a forge Feat recognises; this is here so
	// that one is not mistaken for a truncated host name.
	if !strings.HasPrefix(authority, "[") {
		if colon := strings.Index(authority, ":"); colon >= 0 {
			authority = authority[:colon]
		}
	}
	return strings.ToLower(strings.Trim(authority, "[]"))
}

// commandWords splits a command the way the user typed it into the argument
// vector configuration holds.
//
// Quotes are honoured, because a real tracker command carries an argument with a
// space in it and splitting one into pieces produces a configuration that is
// wrong in a way nothing downstream can explain: `feat doctor` would run it and
// report that the output is not the published shape, which says nothing about
// the typing. Single quotes are literal, double quotes take a backslash escape,
// and a backslash outside either escapes the character after it — which is a
// shell's own reading of the same line, minus every expansion, because Feat runs
// the vector directly and expands nothing.
//
// An unterminated quote is refused where it was typed rather than corrected, for
// the reason every other rejection in this flow is: the answer is still there to
// be given again.
func commandWords(value string) ([]string, error) {
	var (
		words   []string
		current strings.Builder
		// quote is the quote character a word is inside, or nought outside one.
		quote rune
		// started reports that a word is being built, which is what tells an
		// empty quoted argument apart from the space between two words.
		started bool
	)

	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		switch {
		case char == '\\' && quote != '\'' && i+1 < len(runes):
			i++
			current.WriteRune(runes[i])
			started = true
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			current.WriteRune(char)
		case char == '\'' || char == '"':
			quote = char
			started = true
		case char == ' ' || char == '\t':
			if started {
				words = append(words, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(char)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the %c quote is never closed", quote)
	}
	if started {
		words = append(words, current.String())
	}

	if len(words) == 0 || strings.TrimSpace(words[0]) == "" {
		return nil, errors.New("name the program to run, or leave this blank for no tracker")
	}
	return words, nil
}

// accessModes lists the default access modes, in the order the question offers
// them: the two a user picks between most often first.
func accessModes() []string {
	return []string{
		string(domain.DefaultAccessReadWrite),
		string(domain.DefaultAccessSelectable),
		string(domain.DefaultAccessReadOnly),
		string(domain.DefaultAccessStableReadOnly),
		string(domain.DefaultAccessOmitted),
	}
}

// editable returns the repositories a task can be given write access to.
func editable(repositories []config.DraftRepository) []string {
	var found []string
	for _, repository := range repositories {
		if domain.DefaultAccess(repository.DefaultAccess).CanBeReadWrite() {
			found = append(found, repository.ID)
		}
	}
	return found
}

// identifiers returns every repository identifier of a draft.
func identifiers(repositories []config.DraftRepository) []string {
	ids := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		ids = append(ids, repository.ID)
	}
	return ids
}

// suggestService proposes the service the agent most likely runs in.
func suggestService(services []string) string {
	for _, name := range []string{"dev", "devcontainer", "agent"} {
		for _, service := range services {
			if service == name {
				return service
			}
		}
	}
	if len(services) == 1 {
		return services[0]
	}
	return ""
}

// orDefault returns the value, or the fallback when there is none.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// clone copies everything an answer can change, so that a rejected answer can be
// undone and an accepted one stepped back out of.
func (s state) clone() state {
	copied := s
	copied.draft = cloneDraft(s.draft)
	copied.notes = append([]string(nil), s.notes...)
	copied.contribution = cloneContribution(s.contribution)
	copied.composition = cloneComposition(s.composition)
	copied.files = append([]string(nil), s.files...)
	copied.envFiles = append([]string(nil), s.envFiles...)
	copied.services = append([]string(nil), s.services...)
	return copied
}

// cloneDraft deep-copies a draft, including every slice a later answer appends
// to. A shallow copy shares those arrays, which is how stepping back one
// question restores a draft that still has the answer in it.
func cloneDraft(draft config.Draft) config.Draft {
	copied := draft
	copied.Repositories = append([]config.DraftRepository(nil), draft.Repositories...)
	copied.Execution.ComposeFiles = append([]string(nil), draft.Execution.ComposeFiles...)
	copied.Tracker = append([]string(nil), draft.Tracker...)

	copied.Checks = make([]config.DraftCheck, len(draft.Checks))
	for i, check := range draft.Checks {
		copied.Checks[i] = check
		copied.Checks[i].Command = append([]string(nil), check.Command...)
	}
	if len(draft.Checks) == 0 {
		copied.Checks = nil
	}

	for i, repository := range draft.Repositories {
		if repository.Runtime == nil {
			continue
		}
		contribution := cloneContribution(*repository.Runtime)
		copied.Repositories[i].Runtime = &contribution
	}

	if draft.Runtime != nil {
		runtime := *draft.Runtime
		runtime.EnvFiles = append([]string(nil), draft.Runtime.EnvFiles...)
		copied.Runtime = &runtime
	}
	return copied
}

// cloneContribution deep-copies one repository's part of the application.
func cloneContribution(contribution config.DraftRepositoryRuntime) config.DraftRepositoryRuntime {
	copied := contribution
	copied.ComposeFiles = append([]string(nil), contribution.ComposeFiles...)
	copied.Services = append([]string(nil), contribution.Services...)
	copied.Reachable = append([]string(nil), contribution.Reachable...)
	return copied
}

// cloneComposition deep-copies what a repository's Compose files proposed.
func cloneComposition(composition Composition) Composition {
	copied := composition
	copied.Services = append([]string(nil), composition.Services...)
	copied.Reachable = append([]string(nil), composition.Reachable...)
	copied.Mounted = append([]string(nil), composition.Mounted...)
	copied.Baked = append([]string(nil), composition.Baked...)
	copied.Undecided = append([]string(nil), composition.Undecided...)
	return copied
}

// relativeTo rewrites the paths that are inside a directory as relative to it,
// and leaves the rest alone.
func relativeTo(dir string, paths []string) []string {
	if dir == "" || len(paths) == 0 {
		return paths
	}
	rewritten := make([]string, len(paths))
	for i, path := range paths {
		rewritten[i] = path
		relative, err := filepath.Rel(dir, path)
		if err != nil || strings.HasPrefix(relative, "..") {
			continue
		}
		rewritten[i] = relative
	}
	return rewritten
}

// intersect returns the values of the first list that are in the second,
// keeping the first list's order.
func intersect(values, allowed []string) []string {
	var found []string
	for _, value := range values {
		if containsString(allowed, value) {
			found = append(found, value)
		}
	}
	return found
}

// containsString reports whether a list holds a value.
func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
