package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
)

const initLong = `Write a project's configuration by answering questions.

A project is one YAML file. This asks what has to be decided — which
repositories take part, where the agent runs, what verifies the work — and fills
in everything Feat has a default for, so the file it produces states your
decisions and nothing else.

What it can find out, it finds out rather than asking: whether a directory is a
Git repository, which remote and default branch it has, which Compose files are
beside it, and which services they define. What it proposes is shown in
brackets, and pressing Enter accepts it.

The whole file is displayed before anything is written, and it has already been
loaded and validated by then: what you are shown is a configuration Feat
accepts. Nothing is written until you say so, an existing configuration is never
overwritten, and no project is registered without being asked.`

func newProjectInitCommand(env *environment) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init [<project>]",
		Short: "Write a project's configuration by answering questions",
		Long:  initLong,
		Args:  checkArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, options, err := env.project()
			if err != nil {
				return err
			}
			process, err := env.current()
			if err != nil {
				return err
			}

			// The command is a conversation, and there is nobody to converse
			// with in a pipe. Saying so beats asking questions into a stream
			// that cannot answer them, and the hand-written route still exists.
			if !env.interactive {
				return fmt.Errorf(
					"`feat project init` needs a terminal, because it works by asking questions: "+
						"to write a configuration without one, copy docs/examples/project.yaml to %s",
					layout.ProjectConfigDir())
			}

			w := &wizard{
				prompter: prompter{in: bufio.NewReader(cmd.InOrStdin()), out: cmd.OutOrStdout()},
				layout:   layout,
				options:  options,
				process:  process,
				runner:   env.runner,
				dryRun:   dryRun,
			}
			return w.run(cmd.Context(), args)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the configuration instead of writing it")
	return cmd
}

// wizard is one run of `feat project init`.
//
// It collects answers into a config.Draft and renders that once, at the end.
// The draft is the only representation of what was decided: nothing is written
// down as it is answered, so an interrupted run leaves nothing behind, and the
// file the user is shown is rendered from the same value the file is written
// from.
type wizard struct {
	prompter

	layout  paths.Layout
	options config.Options
	process paths.Environment
	runner  project.Runner
	dryRun  bool

	draft config.Draft
	// sawRemote records that at least one checkout really had a remote, as
	// opposed to having had one assumed for it. The base policy turns on the
	// difference, and the draft cannot hold it: every repository is written
	// with a remote name, because configuration requires one.
	sawRemote bool
}

// errAnswersEnded reports input that stopped before the conversation did.
var errAnswersEnded = errors.New(
	"the answers ended before the configuration was complete, so nothing was written")

func (w *wizard) run(ctx context.Context, args []string) error {
	w.say("This writes one project's configuration by asking about it.\n")
	w.say("Press Enter to accept the value in brackets. Nothing is written until you confirm.\n")

	if err := w.identify(args); err != nil {
		return err
	}
	if err := w.repositories(ctx); err != nil {
		return err
	}
	if err := w.primary(); err != nil {
		return err
	}
	if err := w.execution(); err != nil {
		return err
	}
	if err := w.capabilities(); err != nil {
		return err
	}
	if err := w.runtime(); err != nil {
		return err
	}
	if err := w.checks(); err != nil {
		return err
	}
	return w.finish(ctx)
}

// identify settles the project's identifier and name.
//
// The identifier is settled first because it names the file, and a project that
// is already configured is refused here rather than after a conversation whose
// answers would have nowhere to go.
func (w *wizard) identify(args []string) error {
	id := ""
	if len(args) > 0 {
		id = args[0]
		if err := domain.ProjectID(id).Validate(); err != nil {
			return err
		}
	}

	if id == "" {
		suggested := config.Slug(filepath.Base(w.workingDirectory()))
		answer, err := w.askUntil("\nProject identifier", suggested, func(value string) error {
			return domain.ProjectID(value).Validate()
		})
		if err != nil {
			return err
		}
		id = answer
	}

	// Both extensions are looked for, because either is a configuration this
	// project already has, and overwriting one is not this command's business.
	if existing, err := config.Find(w.layout.ProjectConfigDir(), id); err == nil {
		return fmt.Errorf(
			"project %s is already configured at %s: edit that file, or remove it before writing another",
			id, existing)
	} else if !errors.Is(err, config.ErrNotFound) {
		return err
	}

	name, err := w.ask("Display name", id)
	if err != nil {
		return err
	}

	w.draft.ID = id
	w.draft.Name = name
	return nil
}

// repositories collects the repositories that take part in the project.
func (w *wizard) repositories(ctx context.Context) error {
	w.say("\nRepositories\n")
	w.say("  Every repository a task may read or write is one of these. Feat never\n")
	w.say("  changes the checkouts themselves; it creates worktrees beside them.\n")

	suggested := w.workingDirectory()
	for {
		repository, err := w.repository(ctx, suggested)
		if err != nil {
			return err
		}
		w.draft.Repositories = append(w.draft.Repositories, repository)

		another, err := w.confirm("\n  Add another repository?", false)
		if err != nil {
			return err
		}
		if !another {
			// A repository with no remote cannot have a base resolved from one,
			// and the default policy resolves from one. This is the only value
			// the wizard decides for the user, and it decides it from what it
			// found rather than from a preference.
			if !w.sawRemote {
				w.draft.BasePolicy = config.PolicyLocal
				w.say("\n  No repository has a remote, so bases are resolved from the local default branch.\n")
			}
			return nil
		}
		// The directory that suggested the first repository has been used, and
		// suggesting it again would propose a repository already configured.
		suggested = ""
	}
}

// repository asks about one repository and asks Git about it.
func (w *wizard) repository(ctx context.Context, suggested string) (config.DraftRepository, error) {
	var checkout project.Checkout

	// The answer itself is not kept. What matters is the working tree Git
	// resolved from it, because that is the path a repository is configured by:
	// an answer naming a subdirectory configures the checkout it is in.
	_, err := w.askUntil("\n  Path of the checkout on this machine", suggested, func(value string) error {
		path, err := w.absolute(value)
		if err != nil {
			return err
		}
		found, err := project.Inspect(ctx, w.runner, path)
		if err != nil {
			return err
		}
		checkout = found
		return nil
	})
	if err != nil {
		return config.DraftRepository{}, err
	}
	w.describe(checkout)
	w.sawRemote = w.sawRemote || checkout.Remote != ""

	id, err := w.askUntil("  Identifier for this repository", config.Slug(filepath.Base(checkout.Root)),
		func(value string) error {
			if err := domain.RepositoryID(value).Validate(); err != nil {
				return err
			}
			for _, existing := range w.draft.Repositories {
				if existing.ID == value {
					return fmt.Errorf("repository %s is already part of this project", value)
				}
			}
			return nil
		})
	if err != nil {
		return config.DraftRepository{}, err
	}

	// The first repository is the one a task works in unless the user says
	// otherwise, so it is proposed read_write; a later one is proposed
	// selectable, which is the mode that decides per task rather than for every
	// task at once.
	proposed := string(domain.DefaultAccessReadWrite)
	if len(w.draft.Repositories) > 0 {
		proposed = string(domain.DefaultAccessSelectable)
	}
	access, err := w.choose("  How does it take part in a task by default?", accessModes(), proposed)
	if err != nil {
		return config.DraftRepository{}, err
	}

	return config.DraftRepository{
		ID:            id,
		HostPath:      checkout.Root,
		DefaultBranch: orDefault(checkout.DefaultBranch, defaultBranchName),
		Remote:        orDefault(checkout.Remote, defaultRemoteName),
		DefaultAccess: access,
	}, nil
}

// describe reports what Git said about a checkout, and what Feat assumed where
// Git said nothing.
//
// The two are separate sentences on purpose. Once a value is in the file, one
// that was established and one that was assumed look identical, and the moment
// to tell them apart is while the user can still see where each came from.
func (w *wizard) describe(checkout project.Checkout) {
	w.say("    a Git repository at %s\n", checkout.Root)

	switch {
	case checkout.Remote == "" && checkout.DefaultBranch == "":
		w.say("    no remote and no branch checked out; %s and %s are assumed\n",
			defaultRemoteName, defaultBranchName)
	case checkout.Remote == "":
		w.say("    no remote; the default branch %s is assumed to be local\n", checkout.DefaultBranch)
	case checkout.DefaultBranch == "":
		w.say("    remote %s, and no branch checked out; %s is assumed\n", checkout.Remote, defaultBranchName)
	default:
		w.say("    remote %s, default branch %s\n", checkout.Remote, checkout.DefaultBranch)
	}
}

// primary settles which repository a task works in by default.
//
// The choice is limited to the repositories a task can edit, because the
// primary repository is where the agent works: a project whose primary
// repository can never be written to has no editable workspace at all
// (FR-PROJ-003).
func (w *wizard) primary() error {
	var editable []string
	for _, repository := range w.draft.Repositories {
		if domain.DefaultAccess(repository.DefaultAccess).CanBeReadWrite() {
			editable = append(editable, repository.ID)
		}
	}

	if len(editable) == 0 {
		w.say("\n  A task works in one repository by default, and that one must be editable.\n")
		chosen, err := w.choose("  Which repository should a task be able to edit?",
			identifiers(w.draft.Repositories), w.draft.Repositories[0].ID)
		if err != nil {
			return err
		}
		for i, repository := range w.draft.Repositories {
			if repository.ID == chosen {
				w.draft.Repositories[i].DefaultAccess = string(domain.DefaultAccessReadWrite)
			}
		}
		w.draft.Primary = chosen
		return nil
	}

	if len(editable) == 1 {
		w.draft.Primary = editable[0]
		return nil
	}

	chosen, err := w.choose("\n  Which repository does a task work in by default?", editable, editable[0])
	if err != nil {
		return err
	}
	w.draft.Primary = chosen
	return nil
}

// execution settles where the agent runs, and everything that follows from it.
func (w *wizard) execution() error {
	w.say("\nWhere the agent runs\n")
	w.say("  host runs Claude Code in the task's own worktree, with no container\n")
	w.say("  boundary. devcontainer runs it as a non-root user in a Compose service,\n")
	w.say("  which is the mode that keeps a task's tools and its dependencies inside\n")
	w.say("  the task.\n")

	mode, err := w.choose("  Execution mode", []string{config.ModeHost, config.ModeDevcontainer}, config.ModeHost)
	if err != nil {
		return err
	}
	w.draft.Execution.Mode = mode

	if mode != config.ModeDevcontainer {
		return nil
	}
	return w.devcontainer()
}

// devcontainer collects what a containerised agent needs.
func (w *wizard) devcontainer() error {
	w.say("\n  The Compose files that define the devcontainer, and the service the agent\n")
	w.say("  runs in. Feat starts that service for a task and never gives it Docker.\n")

	files, err := w.composeFiles("  Compose file", w.composeCandidates())
	if err != nil {
		return err
	}
	w.draft.Execution.ComposeFiles = files

	services := project.ComposeServices(files...)
	if len(services) > 0 {
		w.say("    services defined there: %s\n", strings.Join(services, ", "))
	}
	service, err := w.askUntil("  Service the agent runs in", suggestService(services), notEmpty("a service name"))
	if err != nil {
		return err
	}
	w.draft.Execution.Service = service

	user, err := w.askUntil("  Container user the agent runs as", "", func(value string) error {
		if err := notEmpty("a user")(value); err != nil {
			return err
		}
		if name, _, _ := strings.Cut(value, ":"); name == "root" || name == "0" {
			return errors.New("the agent must not run as root in the devcontainer")
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.draft.Execution.User = user

	w.say("\n  Where each repository's task worktrees are mounted in that container.\n")
	for i, repository := range w.draft.Repositories {
		if domain.DefaultAccess(repository.DefaultAccess) == domain.DefaultAccessOmitted {
			continue
		}
		path, err := w.askUntil("  Mount point for "+repository.ID, "/srv/"+repository.ID, containerPath)
		if err != nil {
			return err
		}
		w.draft.Repositories[i].ContainerPath = path
	}

	w.say("\n  Claude's own configuration can live in a volume of its own, so that one\n")
	w.say("  interactive login is not your ~/.claude in every task container.\n")
	dedicated, err := w.confirm("  Give Claude a configuration volume?", true)
	if err != nil {
		return err
	}
	if !dedicated {
		return nil
	}
	volume, err := w.askUntil("  Volume name", "feat-claude", notEmpty("a volume name"))
	if err != nil {
		return err
	}
	w.draft.Execution.ClaudeConfigVolume = volume
	return nil
}

// capabilities settles the two capabilities that vary.
//
// The other three do not vary, and the wizard does not pretend they do: Feat
// has no mechanism that grants an agent Docker, restricts its network, or
// limits its Git access, so there is nothing to ask (docs/05-security-model.md).
func (w *wizard) capabilities() error {
	w.say("\nProvider CLI\n")
	w.say("  The agent may have an authenticated `gh` or `glab` in its environment, to\n")
	w.say("  open pull or merge requests. This declares which one to expect; Feat\n")
	w.say("  reports whether it is there and never installs it.\n")

	const (
		none = "none"
		both = "both"
	)
	choice, err := w.choose("  Which provider CLI does the agent use?", []string{none, "gh", "glab", both}, none)
	if err != nil {
		return err
	}
	if choice == "gh" || choice == both {
		w.draft.Capabilities.GitHubCLI = config.CLIOptional
	}
	if choice == "glab" || choice == both {
		w.draft.Capabilities.GitLabCLI = config.CLIOptional
	}
	return nil
}

// runtime settles the application services a task may run.
func (w *wizard) runtime() error {
	w.say("\nApplication services\n")
	w.say("  The application under development, run per task. They are separate from\n")
	w.say("  the agent's own environment, and in this version they start only when you\n")
	w.say("  ask for them.\n")

	wanted, err := w.confirm("  Does a task run application services?", false)
	if err != nil || !wanted {
		return err
	}

	files, err := w.composeFiles("  Compose file", w.composeCandidates())
	if err != nil {
		return err
	}

	detected := project.ComposeServices(files...)
	if len(detected) > 0 {
		w.say("    services defined there: %s\n", strings.Join(detected, ", "))
	}
	services, err := w.askWords("  Services Feat manages for a task", detected)
	if err != nil {
		return err
	}

	w.say("\n  Environment files are passed to Compose by path. Feat never reads what is\n")
	w.say("  in them, and never copies a value out of them into anything it generates.\n")
	envFiles, err := w.optionalFiles("  Environment file")
	if err != nil {
		return err
	}

	w.draft.Runtime = &config.DraftRuntime{
		ComposeFiles: files,
		EnvFiles:     envFiles,
		Services:     services,
	}
	return nil
}

// checks settles one verification command for the primary repository.
//
// One, rather than a set: a check is the thing most easily added to the file
// later, and the value of asking at all is that a project arrives with the
// review gate doing something. The file says where more of them go.
func (w *wizard) checks() error {
	w.say("\nVerification\n")
	w.say("  A command that has to pass before work is reviewed. A check that fails\n")
	w.say("  returns to the agent's own loop, so this is what tells the agent it is\n")
	w.say("  not finished yet.\n")

	command, err := w.ask("  Command that verifies "+w.draft.Primary+", or blank for none", "")
	if err != nil {
		return err
	}
	vector := strings.Fields(command)
	if len(vector) == 0 {
		return nil
	}
	// Splitting on spaces is all this does, and the file is shown before it is
	// written, so a command that needed quoting is visible as the wrong
	// argument vector rather than discovered when a gate runs it.
	w.say("    the command runs as %s, and is split on spaces; edit the file for anything else\n",
		strings.Join(vector, " "))

	id, err := w.askUntil("  Name for this check", config.Slug(command), func(value string) error {
		return domain.RepositoryID(value).Validate()
	})
	if err != nil {
		return err
	}

	execution := config.ExecutionAgent
	if w.draft.Execution.Mode == config.ModeDevcontainer {
		execution, err = w.choose("  Where does it run?",
			[]string{config.ExecutionAgent, config.ExecutionHost}, config.ExecutionAgent)
		if err != nil {
			return err
		}
	}

	w.draft.Checks = append(w.draft.Checks, config.DraftCheck{
		Repository: w.draft.Primary,
		ID:         id,
		Command:    vector,
		Execution:  execution,
	})
	return nil
}

// finish renders the draft, validates it, and offers to write it.
func (w *wizard) finish(ctx context.Context) error {
	file, err := config.File(w.layout.ProjectConfigDir(), w.draft.ID)
	if err != nil {
		return err
	}

	// Validated before it is displayed, so that what the user is shown is a
	// configuration Feat accepts rather than a proposal that might not be. A
	// failure here is a rule the questions did not cover, and it is reported
	// with the field it is about, exactly as a hand-edited file's would be.
	//
	// The text that comes back is the text that was validated, and it is what is
	// displayed and written: the file the user sees is the file they get.
	_, rendered, err := w.draft.Config(file, w.options)
	if err != nil {
		return fmt.Errorf(
			"the answers do not make a configuration Feat accepts, so nothing was written:\n%w",
			configFailure(err))
	}

	w.say("\n%s\n\n%s\n", file, rendered)
	if w.dryRun {
		w.say("Nothing was written: this was a dry run.\n")
		return nil
	}

	write, err := w.confirm("Write it?", true)
	if err != nil {
		return err
	}
	if !write {
		w.say("Nothing was written.\n")
		return nil
	}
	if err := writeNew(file, rendered); err != nil {
		return err
	}
	w.say("\nwrote %s\n", file)

	if err := w.diagnose(ctx); err != nil {
		return err
	}
	return w.register(ctx)
}

// diagnose offers to check the new project against this machine.
//
// It is offered rather than done, because it runs commands and can take a
// moment, and it is offered at all because the questions could not ask the
// host anything: whether the Compose service exists, whether the agent is
// installed, and whether a remote resolves are exactly the answers this file
// now depends on.
func (w *wizard) diagnose(ctx context.Context) error {
	checkNow, err := w.confirm("\nCheck it against this machine now?", true)
	if err != nil || !checkNow {
		return err
	}

	report, err := project.Diagnose(ctx, project.Options{
		ConfigDir: w.layout.ProjectConfigDir(),
		Resolve:   w.options,
		Runner:    w.runner,
		Projects:  []string{w.draft.ID},
	})
	if err != nil {
		return err
	}
	w.say("\n")
	printReport(w.out, report, w.layout.ProjectConfigDir())

	if report.Failed() {
		// The findings are on the screen, and the file exists: this is not a
		// failed command, it is a project with work left to do on it.
		w.say("\nFix what is marked ERROR, then run `feat doctor` again.\n")
	}
	return nil
}

// register offers to register the project with a running daemon.
//
// Registration is a separate act with a command of its own, and it stays that
// way: what is offered here is the next step, not a step the wizard takes
// because it was already talking to the user.
func (w *wizard) register(ctx context.Context) error {
	if status := daemon.Inspect(w.layout); !status.Running() {
		w.say("\nNext:\n")
		w.say("  feat daemon start\n")
		w.say("  feat project add %s\n", w.draft.ID)
		return nil
	}

	now, err := w.confirm("\nRegister it with the running daemon now?", true)
	if err != nil {
		return err
	}
	if !now {
		w.say("\nRegister it later with `feat project add %s`.\n", w.draft.ID)
		return nil
	}

	caller := client.New(w.layout.Socket)
	defer caller.Close()

	registration, err := caller.RegisterProject(ctx, w.draft.ID)
	if err != nil {
		return err
	}
	w.say("\n")
	printRegistration(w.out, registration)
	return nil
}

// composeCandidates returns the Compose files found beside the project's
// repositories, in the order the repositories were answered.
func (w *wizard) composeCandidates() []string {
	var found []string
	seen := make(map[string]bool)
	for _, repository := range w.draft.Repositories {
		for _, file := range project.ComposeFiles(repository.HostPath) {
			if seen[file] {
				continue
			}
			seen[file] = true
			found = append(found, file)
		}
	}
	return found
}

// composeFiles asks for one or more Compose files, proposing what was found.
//
// At least one is required, because both sections that ask for them are
// meaningless without one.
func (w *wizard) composeFiles(question string, candidates []string) ([]string, error) {
	if len(candidates) > 0 {
		w.say("    found beside your repositories: %s\n", strings.Join(candidates, ", "))
	}

	var files []string
	for {
		suggested := ""
		if len(candidates) > len(files) {
			suggested = candidates[len(files)]
		}

		prompt := question
		if len(files) > 0 {
			prompt = question + " (blank to finish)"
		}
		answer, err := w.ask(prompt, suggested)
		if err != nil {
			return nil, err
		}
		if answer == "" {
			if len(files) > 0 {
				return files, nil
			}
			w.say("    at least one Compose file is needed here\n")
			continue
		}

		path, err := w.absolute(answer)
		if err != nil {
			w.say("    %v\n", err)
			continue
		}
		if _, err := os.Stat(path); err != nil {
			// Not a refusal: a Compose file that is generated, or that lives on
			// a branch not checked out right now, is still the right value.
			// `feat doctor` asks the same question again later.
			w.say("    %s does not exist yet\n", path)
		}
		files = append(files, path)
	}
}

// optionalFiles asks for any number of existing files, and accepts none.
func (w *wizard) optionalFiles(question string) ([]string, error) {
	var files []string
	for {
		answer, err := w.ask(question+", or blank for none", "")
		if err != nil {
			return nil, err
		}
		if answer == "" {
			return files, nil
		}
		path, err := w.absolute(answer)
		if err != nil {
			w.say("    %v\n", err)
			continue
		}
		if _, err := os.Stat(path); err != nil {
			w.say("    %s does not exist yet\n", path)
		}
		files = append(files, path)
	}
}

// askWords asks for a list of words, such as service names.
func (w *wizard) askWords(question string, suggested []string) ([]string, error) {
	var words []string
	_, err := w.askUntil(question, strings.Join(suggested, " "), func(value string) error {
		words = strings.Fields(value)
		if len(words) == 0 {
			return errors.New("name at least one, separated by spaces")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return words, nil
}

// absolute expands a leading "~" and resolves a relative path against the
// working directory, so that an answer typed the way a shell would take it is
// the path Feat records.
func (w *wizard) absolute(value string) (string, error) {
	expanded, err := w.process.Expand(value)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded), nil
	}
	return filepath.Join(w.workingDirectory(), expanded), nil
}

// workingDirectory returns the directory the command was run in, or the home
// directory when the working directory cannot be read — which happens when it
// has been removed underneath the process, and is not a reason to refuse to
// configure a project.
func (w *wizard) workingDirectory() string {
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return w.process.Home
}

// Defaults the wizard proposes where Git had no answer. They are the same
// values configuration itself defaults to, so a repository that acquires a
// remote later needs no change here.
const (
	defaultRemoteName = "origin"
	defaultBranchName = "main"
)

// prompter asks questions and reads answers.
//
// The reader is held for the whole conversation rather than built per question,
// because a new buffered reader discards what the previous one buffered, which
// for a sequence of prompts loses the answers a user typed ahead.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// say writes one line of the conversation.
func (p *prompter) say(format string, args ...any) { printf(p.out, format, args...) }

// ask puts one question and returns the answer, or the proposed value when the
// answer is empty.
func (p *prompter) ask(question, proposed string) (string, error) {
	if proposed != "" {
		printf(p.out, "%s [%s]: ", question, proposed)
	} else {
		printf(p.out, "%s: ", question)
	}

	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the answer: %w", err)
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		if errors.Is(err, io.EOF) {
			// Input that ran out is not somebody accepting every remaining
			// proposal. There are more questions, and there is nobody left to
			// answer them.
			return "", errAnswersEnded
		}
		return proposed, nil
	}
	return answer, nil
}

// askUntil repeats a question until the answer passes.
//
// A rejected answer is answered with the reason and the same question, because
// the alternative is a command that fails at the end of a conversation over
// something the user could have corrected when they typed it.
func (p *prompter) askUntil(question, proposed string, check func(string) error) (string, error) {
	for {
		answer, err := p.ask(question, proposed)
		if err != nil {
			return "", err
		}
		if answer == "" {
			p.say("    an answer is needed here\n")
			continue
		}
		if err := check(answer); err != nil {
			p.say("    %v\n", err)
			continue
		}
		return answer, nil
	}
}

// choose puts a question with a closed set of answers.
func (p *prompter) choose(question string, options []string, proposed string) (string, error) {
	return p.askUntil(fmt.Sprintf("%s (%s)", question, strings.Join(options, "/")), proposed,
		func(value string) error {
			for _, option := range options {
				if value == option {
					return nil
				}
			}
			return fmt.Errorf("%q is not one of %s", value, strings.Join(options, ", "))
		})
}

// confirm puts a yes-or-no question.
func (p *prompter) confirm(question string, proposed bool) (bool, error) {
	hint := "y/N"
	if proposed {
		hint = "Y/n"
	}
	printf(p.out, "%s [%s]: ", question, hint)

	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	case "":
		if errors.Is(err, io.EOF) {
			// An answer that is only the end of the input is not the default
			// being accepted: a question nobody answered must not be read as
			// permission.
			return false, errAnswersEnded
		}
		return proposed, nil
	default:
		return proposed, nil
	}
}

// writeNew writes a file that must not already exist.
//
// The exclusive create is the check: a file that appeared between the question
// and the answer is somebody else's, and this is the one place in the command
// where that race has a file at the end of it.
func writeNew(file string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), configDirPerm); err != nil {
		return fmt.Errorf("creating the configuration directory %s: %w", filepath.Dir(file), err)
	}

	// #nosec G304 -- the path is the configuration directory joined with a
	// validated project identifier, which config.File is what enforces
	handle, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, configFilePerm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists, and nothing was written to it", file)
		}
		return fmt.Errorf("creating %s: %w", file, err)
	}

	if _, err := handle.Write(content); err != nil {
		_ = handle.Close()
		// A configuration that is half a file is worse than none: the next
		// command would read it and report a parse error about a file the user
		// never wrote.
		if removeErr := os.Remove(file); removeErr != nil {
			return fmt.Errorf("writing %s: %w (and it could not be removed either: %w)", file, err, removeErr)
		}
		return fmt.Errorf("writing %s: %w", file, err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

// Permissions for what the wizard creates. They are the ones the rest of Feat
// uses for files it owns: a project configuration names paths in the user's
// home and is nobody else's business.
const (
	configDirPerm  os.FileMode = 0o700
	configFilePerm os.FileMode = 0o600
)

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

// identifiers returns the repository identifiers of a draft.
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

// notEmpty rejects an empty answer, naming what was asked for.
func notEmpty(what string) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("name " + what)
		}
		return nil
	}
}

// containerPath rejects a mount point that is not a usable absolute path inside
// a container. The overlap rules are configuration's, and the draft is
// validated against them before anything is written.
func containerPath(value string) error {
	switch {
	case !strings.HasPrefix(value, "/"):
		return errors.New("a mount point is an absolute path inside the container, such as /srv/api")
	case value == "/":
		return errors.New("a mount point must not be the container's filesystem root")
	}
	return nil
}

// orDefault returns the value, or the fallback when there is none.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
