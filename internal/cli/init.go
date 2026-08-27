package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
	"github.com/ma8el/feat/internal/wizard"
)

const initLong = `Write a project's configuration by answering questions.

A project is one YAML file. This asks what has to be decided — which
repositories take part, where the agent runs, whether a task runs application
services — and fills in everything Feat has a default for, so the file it
produces states your decisions and nothing else.

What it can find out, it finds out rather than asking: whether a directory is a
Git repository, which remote and default branch it has, which Compose files are
beside it, and which services they define. What it proposes is shown in
brackets, and pressing Enter accepts it.

The whole file is displayed before anything is written, and it has already been
loaded and validated by then: what you are shown is a configuration Feat
accepts. Nothing is written until you say so, an existing configuration is never
overwritten, and no project is registered without being asked.

The dashboard asks the same questions on ` + "`p`" + `, for a machine that is already
running Feat.`

func newProjectInitCommand(env *environment) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init [<project>]",
		Short: "Write a project's configuration by answering questions",
		Long:  initLong,
		Args:  checkArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := env.resolve()
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

			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			flow, err := env.wizard(id)
			if err != nil {
				return err
			}

			conversation := &conversation{
				prompter: prompter{in: bufio.NewReader(cmd.InOrStdin()), out: cmd.OutOrStdout()},
				wizard:   flow,
				env:      env,
				layout:   layout,
				dryRun:   dryRun,
			}
			return conversation.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the configuration instead of writing it")
	return cmd
}

// wizard builds the project wizard for this machine.
//
// It is on the environment because both askers need one built the same way: the
// command below, and the dashboard through its backend (ADR-063).
func (e *environment) wizard(id string) (*wizard.Wizard, error) {
	layout, options, err := e.project()
	if err != nil {
		return nil, err
	}
	current, err := e.current()
	if err != nil {
		return nil, err
	}
	return wizard.New(wizard.Options{
		Host:      &machineHost{process: current, runner: e.runner},
		ConfigDir: layout.ProjectConfigDir(),
		Resolve:   options,
		ID:        id,
	})
}

// conversation is one run of `feat project init`: the shared questions, asked a
// line at a time.
//
// It owns the presentation and nothing else. Which question comes next, what it
// proposes, and whether an answer is acceptable are the wizard's; the headings,
// the indentation, and the brackets around a proposal are this file's.
type conversation struct {
	prompter

	wizard *wizard.Wizard
	env    *environment
	layout paths.Layout
	dryRun bool

	// section is the last section a heading was printed for, so that each is
	// announced once however many questions it turns out to hold.
	section wizard.Section
}

// errAnswersEnded reports input that stopped before the conversation did.
var errAnswersEnded = errors.New(
	"the answers ended before the configuration was complete, so nothing was written")

func (c *conversation) run(ctx context.Context) error {
	c.say("This writes one project's configuration by asking about it.\n")
	c.say("Press Enter to accept the value in brackets. Nothing is written until you confirm.\n")

	for {
		question, ok := c.wizard.Step()
		if !ok {
			return c.finish(ctx)
		}
		if err := c.put(ctx, question); err != nil {
			return err
		}
	}
}

// put asks one question until it is answered acceptably.
//
// A rejected answer is answered with the reason and the same question, because
// the alternative is a command that fails at the end of a conversation over
// something the user could have corrected when they typed it.
func (c *conversation) put(ctx context.Context, question wizard.Question) error {
	// What the last answer established, and what this question found out about
	// what it is proposing, said under the answer they follow and before the
	// question they are the context for. They are read off the question, as the
	// dialog reads them, so that a sentence the flow writes reaches both askers
	// or neither.
	for _, note := range question.Notes {
		c.say("    %s\n", note)
	}
	c.announce(question)

	for {
		answer, err := c.read(question)
		if err != nil {
			return err
		}
		if err := c.wizard.Answer(ctx, answer); err != nil {
			c.say("    %v\n", err)
			continue
		}
		return nil
	}
}

// announce opens a section, or a group of questions inside one.
//
// The blank line and the heading are what separate the parts of the file from
// each other on a terminal that has only one column to say it in: a section is
// announced once however many questions it turns out to hold, and a group
// inside one — a second repository, the mount points — is separated from what
// came before it.
func (c *conversation) announce(question wizard.Question) {
	if question.Section != c.section || len(question.Detail) > 0 {
		c.say("\n")
	}
	// Whenever there is one, rather than once per section: a section can hold
	// more than one headed group — where the agent runs and which provider CLI
	// it expects are both about the agent — and the flow sets a heading on the
	// first question of a group for exactly that reason.
	if question.Heading != "" {
		c.say("%s\n", question.Heading)
	}
	c.section = question.Section

	for _, line := range question.Detail {
		c.say("  %s\n", line)
	}
}

// read puts one question and returns the answer as typed.
//
// The project's own two questions are unindented and the rest are indented under
// their section's heading, which is the shape the conversation has: the first
// two are about the file itself, and everything after them is about a part of
// it.
func (c *conversation) read(question wizard.Question) (string, error) {
	prompt := question.Prompt
	if question.Section != wizard.SectionProject {
		prompt = "  " + prompt
	}

	switch question.Kind {
	case wizard.KindConfirm:
		// The answer goes back as the word the flow asked for. The prompt is
		// where "y" and "yes" are the same thing, and where a word that is
		// neither is asked again.
		yes, err := c.confirm(prompt, question.Proposed == "y")
		if err != nil {
			return "", err
		}
		if yes {
			return "y", nil
		}
		return "n", nil
	case wizard.KindChoice:
		return c.ask(fmt.Sprintf("%s (%s)", prompt, strings.Join(question.Options, "/")), question.Proposed)
	default:
		return c.ask(prompt, question.Proposed)
	}
}

// finish renders the answers, validates them, and offers to write them.
func (c *conversation) finish(ctx context.Context) error {
	review, err := c.wizard.Review()
	if err != nil {
		return fmt.Errorf(
			"the answers do not make a configuration Feat accepts, so nothing was written:\n%w",
			configFailure(err))
	}

	c.say("\n%s\n\n%s\n", review.Path, review.Text)
	if c.dryRun {
		c.say("Nothing was written: this was a dry run.\n")
		return nil
	}

	write, err := c.confirm("Write it?", true)
	if err != nil {
		return err
	}
	if !write {
		c.say("Nothing was written.\n")
		return nil
	}

	file, err := c.wizard.Write()
	if err != nil {
		return err
	}
	c.say("\nwrote %s\n", file)
	// Said where the file has just been written, and said once. The wizard asks
	// nothing about verification, so this is the only place a user learns the
	// feature exists — and it is opt-in rather than hidden precisely because the
	// gates worth having are the ones somebody opened the file for (ADR-078).
	c.say("\nNo verification checks are configured. Add a `checks:` block to that file\n")
	c.say("for a gate that has to pass before work is reviewed; " +
		"docs/examples/project.yaml\n")
	c.say("has a worked example, and `feat doctor` validates what you write.\n")

	if err := c.diagnose(ctx); err != nil {
		return err
	}
	return c.register(ctx)
}

// diagnose offers to check the new project against this machine.
//
// It is offered rather than done, because it runs commands and can take a
// moment, and it is offered at all because the questions could not ask the
// host anything: whether the Compose service exists, whether the agent is
// installed, and whether a remote resolves are exactly the answers this file
// now depends on.
func (c *conversation) diagnose(ctx context.Context) error {
	checkNow, err := c.confirm("\nCheck it against this machine now?", true)
	if err != nil || !checkNow {
		return err
	}

	layout, options, err := c.env.project()
	if err != nil {
		return err
	}
	report, err := project.Diagnose(ctx, project.Options{
		ConfigDir: layout.ProjectConfigDir(),
		Resolve:   options,
		Runner:    c.env.runner,
		Projects:  []string{c.wizard.ID()},
	})
	if err != nil {
		return err
	}
	c.say("\n")
	printReport(c.out, report, layout.ProjectConfigDir())

	if report.Failed() {
		// The findings are on the screen, and the file exists: this is not a
		// failed command, it is a project with work left to do on it.
		c.say("\nFix what is marked ERROR, then run `feat doctor` again.\n")
	}
	return nil
}

// register offers to register the project with a running daemon.
//
// Registration is a separate act with a command of its own, and it stays that
// way: what is offered here is the next step, not a step the wizard takes
// because it was already talking to the user.
func (c *conversation) register(ctx context.Context) error {
	id := c.wizard.ID()
	if status := daemon.Inspect(c.layout); !status.Running() {
		c.say("\nNext:\n")
		c.say("  feat daemon start\n")
		c.say("  feat project add %s\n", id)
		return nil
	}

	now, err := c.confirm("\nRegister it with the running daemon now?", true)
	if err != nil {
		return err
	}
	if !now {
		c.say("\nRegister it later with `feat project add %s`.\n", id)
		return nil
	}

	caller := client.New(c.layout.Socket)
	defer caller.Close()

	registration, err := caller.RegisterProject(ctx, id)
	if err != nil {
		return err
	}
	c.say("\n")
	printRegistration(c.out, registration)
	return nil
}
