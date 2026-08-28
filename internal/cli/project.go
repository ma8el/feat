package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

const projectLong = `Register and inspect projects.

A project is described by a YAML file in the configuration directory, one file
per project, named after the project's identifier. ` + "`feat project init`" + ` writes
one by asking about the project; registering it tells the daemon the project
exists. Either way the file stays where it is and remains the source of truth.
Run ` + "`feat doctor`" + ` before registering: it validates the file and checks
the host without registering anything.`

func newProjectCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage registered projects",
		Long:  projectLong,
	}
	cmd.AddCommand(
		newProjectAddCommand(env),
		newProjectInitCommand(env),
		newProjectListCommand(env),
		newProjectShowCommand(env),
		newProjectTicketsCommand(env),
	)
	return cmd
}

func newProjectAddCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "add <project>",
		Short: "Register a project from its YAML configuration",
		Long: `Register the project configured in <config>/projects/<project>.yaml, where
<config> is Feat's configuration directory — ~/.config/feat unless
XDG_CONFIG_HOME says otherwise.

Registering a project that is already registered is not an error: its
configuration is re-read and the record updated, which is what to run after
editing the file. Tasks that are already running keep the configuration they
were launched with.`,
		Args: checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, options, err := env.project()
			if err != nil {
				return err
			}
			id := args[0]

			// Validating here first turns the common failure into a message with
			// the offending line in it, rather than one flattened through the
			// socket. The daemon validates it again regardless: it is the one
			// that writes.
			if _, err := config.Load(layout.ProjectConfigDir(), id, options); err != nil {
				return configFailure(err)
			}

			// The daemon is the only writer of persistent state (ADR-008), so
			// registration is a request rather than a file this process writes.
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}
			caller := client.New(layout.Socket)
			defer caller.Close()

			registration, err := caller.RegisterProject(cmd.Context(), id)
			if err != nil {
				return err
			}

			printRegistration(cmd.OutOrStdout(), registration)
			return nil
		},
	}
}

// printRegistration reports what the daemon recorded.
//
// It is shared with `feat project init`, which registers through the same call:
// a user who registered from the wizard and a user who ran the command are
// looking at the same thing, and should be told it the same way.
func printRegistration(out io.Writer, registration api.Registration) {
	verb := "updated"
	if registration.Created {
		verb = "registered"
	}
	printf(out, "%s %s (%s)\n", verb, registration.Project.ID, registration.Project.Name)
	for _, repository := range registration.Project.Repositories {
		marker := " "
		if repository.ID == registration.Project.PrimaryRepository {
			marker = "*"
		}
		printf(out, "  %s %-16s %s\n", marker, repository.ID, repository.HostPath)
	}
	printf(out, "\nrun `feat doctor` to check this project against the host\n")
}

func newProjectListCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Long: `List the projects the daemon has registered, and those configured but not
registered yet.

The two are different states: a configuration file is something you wrote, and a
registration is something Feat knows about.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, _, err := env.project()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			configured, err := config.List(layout.ProjectConfigDir())
			if err != nil {
				return err
			}

			if status := daemon.Inspect(layout); !status.Running() {
				// Without a daemon there is no registry to read. The
				// configuration directory is still readable, and reporting what
				// is in it says more than reporting nothing.
				printConfigured(out, configured, layout.ProjectConfigDir())
				return &NotRunningError{Socket: layout.Socket}
			}
			caller := client.New(layout.Socket)
			defer caller.Close()

			registered, err := caller.Projects(cmd.Context())
			if err != nil {
				return err
			}
			if len(registered) == 0 && len(configured) == 0 {
				printf(out, "no projects are registered\n")
				printf(out, "run `feat project init` to configure one at %s, or write it there by hand\n",
					layout.ProjectConfigDir())
				return nil
			}

			known := make(map[string]bool, len(registered))
			printf(out, "%-20s %-7s %s\n", "PROJECT", "REPOS", "PRIMARY")
			for _, project := range registered {
				known[project.ID] = true
				printf(out, "%-20s %-7d %s\n", project.ID, len(project.Repositories), project.PrimaryRepository)
			}

			var pending []string
			for _, id := range configured {
				if !known[id] {
					pending = append(pending, id)
				}
			}
			if len(pending) > 0 {
				printf(out, "\nconfigured but not registered: %s\n", strings.Join(pending, ", "))
				printf(out, "register one with `feat project add <project>`\n")
			}
			return nil
		},
	}
}

func newProjectShowCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "show <project>",
		Short: "Show a project's resolved configuration",
		Long: `Print the configuration Feat will act on: paths expanded, defaults filled in,
and every repository's place on the host and in the execution environment.

It is the resolved configuration rather than the text of the file. Files that may
hold secrets are listed by path; their contents are never read.`,
		Args: checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, options, err := env.project()
			if err != nil {
				return err
			}

			cfg, err := config.Load(layout.ProjectConfigDir(), args[0], options)
			if err != nil {
				return configFailure(err)
			}

			out := cmd.OutOrStdout()
			if mounts := mountTable(cfg); !mounts.empty() {
				mounts.render(out, "")
				printf(out, "\n* primary repository: where a task works by default\n")
			}
			for _, section := range cfg.Describe() {
				printf(out, "\n%s\n", section.Title)
				for _, field := range section.Fields {
					if field.Note != "" {
						printf(out, "  %-32s %s  (%s)\n", field.Name, field.Value, field.Note)
						continue
					}
					printf(out, "  %-32s %s\n", field.Name, field.Value)
				}
			}
			printf(out, "\nconfiguration is separate from registration: `feat project list` shows what is registered\n")
			return nil
		},
	}
}

func newProjectTicketsCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "tickets <project>",
		Short: "List the tickets the project's tracker prints",
		Long: `Run the project's configured tracker command and list what it printed.

The command decides which tickets are yours. Feat passes it no filter and parses
no part of what comes back beyond checking it against the shape it publishes,
which is why a state here is the tracker's own word rather than one of Feat's.

The reference in the first column is what ` + "`feat implement --ticket`" + ` takes: it
runs this same command again and matches what you typed against what the command
printed.

Nothing is created by listing. Run ` + "`feat doctor`" + ` to check the command itself,
which validates its output without a running daemon.`,
		Args: checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			id := args[0]
			if err := domain.ProjectID(id).Validate(); err != nil {
				return err
			}

			// The daemon runs the tracker command, because that is where every
			// credentialed provider call is made and where a ticket becomes a
			// task (ADR-070). Listing changes nothing, and it still does not
			// start a daemon: a command that reaches somebody's tracker should
			// not start a background process to do it.
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}
			caller := client.New(layout.Socket)
			defer caller.Close()

			list, err := caller.Tickets(cmd.Context(), id)
			if err != nil {
				return err
			}

			printTickets(cmd.OutOrStdout(), id, list)
			return nil
		},
	}
}

// printTickets renders what a tracker printed.
//
// The tracker column is shown only where a ticket carries one, because a project
// drawing on one tracker has nothing to disambiguate and a column of blanks says
// less than no column at all (ADR-071).
func printTickets(out io.Writer, id string, list api.TicketList) {
	if len(list.Tickets) == 0 {
		printf(out, "%s's tracker printed no tickets\n", id)
		printf(out, "which tickets are yours is the command's decision; "+
			"run `feat doctor` to see the command Feat ran\n")
		return
	}

	labelled := false
	for _, ticket := range list.Tickets {
		if ticket.Source != "" {
			labelled = true
			break
		}
	}

	tickets := &table{header: []string{"TICKET", "STATE", "TITLE"}}
	if labelled {
		tickets.header = []string{"TICKET", "STATE", "TRACKER", "TITLE"}
	}
	for _, ticket := range list.Tickets {
		if labelled {
			tickets.add(ticket.Reference, ticket.State, orNone(ticket.Source), ticket.Title)
			continue
		}
		tickets.add(ticket.Reference, ticket.State, ticket.Title)
	}
	tickets.render(out, "")

	printf(out, "\nread at %s\n", list.ReadAt.Local().Format(time.RFC3339))
	printf(out, "start one with `feat implement --project %s --ticket <ticket>`\n", id)
}

// printConfigured lists the configuration files present, for a run with no
// daemon to ask.
func printConfigured(out io.Writer, ids []string, dir string) {
	if len(ids) == 0 {
		// A machine with no configuration and no daemon is a first run, and the
		// error that follows this asks for the daemon. The wizard comes before
		// that: it writes a file without one, and ends by saying to start it.
		printf(out, "no projects are configured in %s\n", dir)
		printf(out, "run `feat project init` to write one\n")
		return
	}
	printf(out, "configured in %s: %s\n", dir, strings.Join(ids, ", "))
	printf(out, "registration is unknown without a running daemon\n")
}

// configFailure renders a configuration error for a terminal.
//
// The annotated form shows each problem where it is in the file, which is most
// of the work of fixing it.
func configFailure(err error) error {
	var invalid *config.Error
	if errors.As(err, &invalid) {
		return errors.New(invalid.Annotated())
	}
	return err
}

// registeredProjects reports which projects the daemon knows about.
//
// A daemon that is not running produces no answer rather than a wrong one, so
// that `feat doctor` can run before one exists and say that it does not know.
func registeredProjects(ctx context.Context, layout paths.Layout) func(string) bool {
	if status := daemon.Inspect(layout); !status.Running() {
		return nil
	}
	caller := client.New(layout.Socket)
	defer caller.Close()

	projects, err := caller.Projects(ctx)
	if err != nil {
		return nil
	}
	known := make(map[string]bool, len(projects))
	for _, project := range projects {
		known[project.ID] = true
	}
	return func(id string) bool { return known[id] }
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}
