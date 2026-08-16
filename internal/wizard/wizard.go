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
	// SectionChecks is what verifies the work.
	SectionChecks Section = "checks"
)

// Sections are the sections in the order they are asked, for an asker that
// wants to show the whole path rather than the step.
func Sections() []Section {
	return []Section{SectionProject, SectionRepositories, SectionAgent, SectionServices, SectionChecks}
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
	// Detail explains the section, in the words the user needs before deciding.
	// It is set with Heading and nowhere else.
	Detail []string
	// Notes are what the previous answer established: what Git said about a
	// checkout, which services a Compose file declares, what Feat assumed. They
	// belong to this question because they are what it is asked in light of.
	Notes []string
	// Prompt is the question itself, without punctuation or decoration.
	Prompt string
	// Proposed is what an empty answer accepts, always: an asker may show it in
	// brackets or as a placeholder, and either way Enter takes it. A question
	// with no proposal and no Optional must be answered.
	Proposed string
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
	// command is the verification command, split into its argument vector.
	command []string
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
	stageAnotherRepository
	// stageEditable is asked only when no repository can be written to, and
	// stagePrimary only when more than one can.
	stageEditable
	stagePrimary
	stageMode
	// The devcontainer stages, asked only in that mode.
	stageComposeFile
	stageService
	stageUser
	stageMount
	stageClaudeVolume
	stageVolumeName
	stageProviderCLI
	stageRuntimeWanted
	// The application stages, asked once per repository: a runtime is composed
	// of its repositories, so what it is made of is answered where the code is.
	stageRuntimeRepository
	stageRuntimeCompose
	stageRuntimeServices
	stageRuntimeMount
	stageRuntimeReachable
	stageRuntimeEnvFile
	stageCheckCommand
	stageCheckName
	stageCheckExecution
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

// Provider CLI answers. They are the question's own words rather than
// configuration values, because "both" is not a value the file has.
const (
	providerNone = "none"
	providerBoth = "both"
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

// Notes are what the last accepted answer established: what Git said about a
// checkout, which services a Compose file declares, what Feat assumed where it
// found nothing.
//
// They are also carried on the next question, which is the one they are the
// context for. An asker that draws a question at a time reads them there; one
// that prints a conversation reads them here, as soon as they are true.
func (w *Wizard) Notes() []string { return w.notes }

// Step returns the question to ask now, and whether there is one.
func (w *Wizard) Step() (Question, bool) {
	if w.stage == stageComplete {
		return Question{}, false
	}
	question := w.question()
	question.Notes = w.notes
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

// question is the current question without the notes, which Step attaches.
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
		if len(w.files) == 0 {
			question.Detail = []string{
				"The Compose files that define the container the agent works in, and the",
				"service it runs in. Feat starts that service for a task and never gives it",
				"Docker. They are not the application's own Compose files: those belong to",
				"the repositories that bring them, and are asked for further down.",
			}
			return question
		}
		// Finishing is only offered once there is one, because the section is
		// meaningless without it.
		question.Prompt, question.Optional = "Compose file (blank to finish)", true
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
			Prompt:   "Mount point for " + repository.ID,
			Proposed: "/srv/" + repository.ID,
		}
		if w.firstMount() {
			question.Detail = []string{
				"Where each repository's task worktrees are mounted in the agent's own",
				"container. Where the application's services expect that repository's source",
				"is a different question with a different answer, asked further down.",
			}
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

	case stageProviderCLI:
		return Question{
			ID: "agent.provider", Section: SectionAgent, Kind: KindChoice,
			Heading: "Provider CLI",
			Detail: []string{
				"The agent may have an authenticated `gh` or `glab` in its environment, to",
				"open pull or merge requests. This declares which one to expect; Feat",
				"reports whether it is there and never installs it.",
			},
			Prompt:   "Which provider CLI does the agent use?",
			Options:  []string{providerNone, "gh", "glab", providerBoth},
			Proposed: providerNone,
		}

	case stageRuntimeWanted:
		return Question{
			ID: "runtime.wanted", Section: SectionServices, Kind: KindConfirm,
			Heading: "Application services",
			Detail: []string{
				"The application under development, run per task. They are separate from",
				"the agent's own environment, and in this version they start only when you",
				"ask for them.",
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
		if len(w.files) == 0 {
			question.Proposed = w.proposedContribution()
			if remaining := w.remainingContributions(); len(remaining) > 0 {
				question.Notes = append(question.Notes,
					"others found beside it: "+strings.Join(remaining, ", "))
			}
			return question
		}
		question.Prompt, question.Optional = question.Prompt+" (blank to finish)", true
		return question

	case stageRuntimeServices:
		return Question{
			ID: "runtime.services", Section: SectionServices, Kind: KindText,
			Prompt:   "Services Feat manages from " + w.draft.Repositories[w.contributor].ID,
			Proposed: strings.Join(w.composition.Services, " "),
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

	case stageCheckCommand:
		return Question{
			ID: "check.command", Section: SectionChecks, Kind: KindText,
			Heading: "Verification",
			Detail: []string{
				"A command that has to pass before work is reviewed. A check that fails",
				"returns to the agent's own loop, so this is what tells the agent it is",
				"not finished yet.",
			},
			Prompt:   "Command that verifies " + w.draft.Primary + ", or blank for none",
			Optional: true,
		}

	case stageCheckName:
		return Question{
			ID: "check.name", Section: SectionChecks, Kind: KindText,
			Prompt: "Name for this check", Proposed: config.Slug(strings.Join(w.command, " ")),
		}

	case stageCheckExecution:
		return Question{
			ID: "check.execution", Section: SectionChecks, Kind: KindChoice,
			Prompt:   "Where does it run?",
			Options:  []string{config.ExecutionAgent, config.ExecutionHost},
			Proposed: config.ExecutionAgent,
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
		w.draft.Repositories = append(w.draft.Repositories, w.pending)
		w.pending = config.DraftRepository{}
		w.stage = stageAnotherRepository

	case stageEditable:
		for i, repository := range w.draft.Repositories {
			if repository.ID == answer {
				w.draft.Repositories[i].DefaultAccess = string(domain.DefaultAccessReadWrite)
			}
		}
		w.draft.Primary = answer
		w.stage = stageMode

	case stagePrimary:
		w.draft.Primary = answer
		w.stage = stageMode

	case stageMode:
		w.draft.Execution.Mode = answer
		if answer != config.ModeDevcontainer {
			w.stage = stageProviderCLI
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
		w.draft.Repositories[w.mount].AgentContainerPath = answer
		w.nextMount()

	case stageVolumeName:
		w.draft.Execution.ClaudeConfigVolume = answer
		w.stage = stageProviderCLI

	case stageProviderCLI:
		if answer == "gh" || answer == providerBoth {
			w.draft.Capabilities.GitHubCLI = config.CLIOptional
		}
		if answer == "glab" || answer == providerBoth {
			w.draft.Capabilities.GitLabCLI = config.CLIOptional
		}
		w.stage = stageRuntimeWanted

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
			w.stage = stageCheckCommand
			break
		}
		path, err := w.host.Absolute(answer)
		if err != nil {
			return err
		}
		w.envFiles = append(w.envFiles, path)
		w.notes = w.existence(path)

	case stageCheckCommand:
		vector := strings.Fields(answer)
		if len(vector) == 0 {
			w.stage = stageComplete
			break
		}
		w.command = vector
		// Splitting on spaces is all this does, and the file is shown before it
		// is written, so a command that needed quoting is visible as the wrong
		// argument vector rather than discovered when a gate runs it.
		w.notes = []string{fmt.Sprintf(
			"the command runs as %s, and is split on spaces; edit the file for anything else",
			strings.Join(vector, " "))}
		w.stage = stageCheckName

	case stageCheckName:
		if err := domain.RepositoryID(answer).Validate(); err != nil {
			return err
		}
		w.draft.Checks = append(w.draft.Checks, config.DraftCheck{
			Repository: w.draft.Primary,
			ID:         answer,
			Command:    w.command,
			Execution:  config.ExecutionAgent,
		})
		if w.draft.Execution.Mode != config.ModeDevcontainer {
			w.stage = stageComplete
			break
		}
		w.stage = stageCheckExecution

	case stageCheckExecution:
		w.draft.Checks[len(w.draft.Checks)-1].Execution = answer
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
		w.stage = stageProviderCLI

	case stageRuntimeWanted:
		if !yes {
			w.stage = stageCheckCommand
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
func (w *Wizard) readComposition() {
	repository := w.draft.Repositories[w.contributor]
	w.composition = w.host.Compose(repository.HostPath, w.files...)

	if len(w.composition.Services) > 0 {
		w.notes = append(w.notes, "services defined there: "+strings.Join(w.composition.Services, ", "))
	}
	if len(w.composition.Undecided) > 0 {
		// Named rather than resolved. Feat never interpolates a "${...}", so an
		// entry containing one is a value it could not derive, and saying which
		// one is what makes the missing proposal actionable.
		w.notes = append(w.notes,
			"not read, because they interpolate: "+strings.Join(w.composition.Undecided, "; "))
	}
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
		return stageMode
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

// firstMount reports whether the mount under the cursor is the first one asked
// for, which is the one the explanation belongs to.
func (w *Wizard) firstMount() bool {
	for i := range w.draft.Repositories[:w.mount] {
		if domain.DefaultAccess(w.draft.Repositories[i].DefaultAccess) != domain.DefaultAccessOmitted {
			return false
		}
	}
	return true
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

// proposedContribution is the first Compose file found beside the repository
// being asked about.
//
// It never proposes another repository's file: what a repository brings is its
// own. It proposes only the first, because a proposal is what an empty answer
// takes and the same key has to mean "no more" once there is one — see
// remainingContributions for what happens to the rest.
func (w *Wizard) proposedContribution() string {
	if found := w.host.ComposeFiles(w.draft.Repositories[w.contributor].HostPath); len(found) > 0 {
		return found[0]
	}
	return ""
}

// remainingContributions are the other Compose files beside the repository,
// named rather than proposed.
//
// A loop cannot both propose the next file and finish on an empty answer: they
// are two meanings for one key, and the one that lost was finishing — a user
// pressing Enter at "blank to finish" added the bracketed file instead, twice,
// and ended up with an application's files defining their agent's container.
// So the first is proposed, the rest are reported, and Enter means what the
// prompt says it means.
func (w *Wizard) remainingContributions() []string {
	found := w.host.ComposeFiles(w.draft.Repositories[w.contributor].HostPath)
	if len(found) < 2 {
		return nil
	}
	return found[1:]
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
	copied.command = append([]string(nil), s.command...)
	return copied
}

// cloneDraft deep-copies a draft, including every slice a later answer appends
// to. A shallow copy shares those arrays, which is how stepping back one
// question restores a draft that still has the answer in it.
func cloneDraft(draft config.Draft) config.Draft {
	copied := draft
	copied.Repositories = append([]config.DraftRepository(nil), draft.Repositories...)
	copied.Execution.ComposeFiles = append([]string(nil), draft.Execution.ComposeFiles...)

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
