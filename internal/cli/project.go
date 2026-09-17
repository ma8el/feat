package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	feat "github.com/ma8el/feat"
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

// newProjectCommand groups the commands that register and inspect a project.
//
// Tickets is passed in rather than built here because it also appears at the
// top level under a shorter name, and an alias holds the body of the command it
// stands for rather than a second one of its own (ADR-040).
func newProjectCommand(env *environment, tickets *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage registered projects",
		Long:  projectLong,
	}
	cmd.AddCommand(
		newProjectAddCommand(env),
		newProjectExampleCommand(),
		newProjectInitCommand(env),
		newProjectListCommand(env),
		newProjectSchemaCommand(),
		newProjectShowCommand(env),
		tickets,
	)
	return cmd
}

func newProjectSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for project configuration files",
		Long: `Print the JSON Schema a project's configuration file is described by.

It is schema/feat-project.schema.json from Feat's repository, embedded so an
installation without a checkout still has it. Point an editor at it, or read
it for what a field may hold.

` + "`feat project example`" + ` prints a worked configuration to start from.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(feat.ProjectSchema())
			return err
		},
	}
}

func newProjectExampleCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "example",
		Short: "Print a worked example of a project configuration",
		Long: `Print the worked example of a project's configuration file.

It is docs/examples/project.yaml from Feat's repository, embedded so an
installation without a checkout still has it. Its comments say where the file
belongs.

Start from it when writing a configuration by hand, and validate what you save
with ` + "`feat doctor`" + `; where a terminal exists, ` + "`feat project init`" + ` asks instead.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(feat.ProjectExample())
			return err
		},
	}
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
	cmd := &cobra.Command{
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

			if wantsJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), describeProject(cfg))
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
	addJSONFlag(cmd)
	return cmd
}

// describeProject renders a loaded configuration as the document this command
// prints.
//
// The mapping is here rather than in internal/api because this is the package
// that loads a configuration: the transport describes the shape and does not
// take a dependency on the configuration package to fill it in.
//
// It is the same two things the table shows, in the same order — the mount
// mapping, which is what a task depends on, and the resolved values beneath it.
func describeProject(cfg *config.Config) api.ProjectConfiguration {
	described := api.ProjectConfiguration{
		ID:                cfg.Project.ID,
		Name:              cfg.Project.Name,
		PrimaryRepository: cfg.Project.PrimaryRepository,
		Repositories:      make([]api.ConfiguredRepository, 0, len(cfg.Repositories)),
		Sections:          make([]api.ConfigurationSection, 0, len(cfg.Describe())),
	}

	for _, mount := range cfg.Mounts() {
		services := mount.RuntimeServices
		if services == nil {
			// A list rather than null, so that a caller can iterate every
			// repository's services without a nil check.
			services = []string{}
		}
		described.Repositories = append(described.Repositories, api.ConfiguredRepository{
			ID:              mount.RepositoryID,
			HostPath:        mount.HostPath,
			AgentPath:       mount.AgentPath,
			RuntimePath:     mount.RuntimePath,
			RuntimeServices: services,
			DefaultAccess:   mount.DefaultAccess,
			Primary:         mount.Primary,
		})
	}

	for _, section := range cfg.Describe() {
		fields := make([]api.ConfigurationField, 0, len(section.Fields))
		for _, field := range section.Fields {
			fields = append(fields, api.ConfigurationField{
				Name: field.Name, Value: field.Value, Note: field.Note,
			})
		}
		described.Sections = append(described.Sections,
			api.ConfigurationSection{Title: section.Title, Fields: fields})
	}
	return described
}

const projectTicketsLong = `Run the project's configured tracker command and show what it printed.

With a project alone, list the tickets. The command decides which tickets are
yours. Feat passes it no filter and parses no part of what comes back beyond
checking it against the shape it publishes, which is why a state here is the
tracker's own word rather than one of Feat's.

With a ticket as well, print that one ticket as the brief Feat would compose
from it: its title, a line naming the ticket, its state, and where it can be
read, and then its description under a heading that marks where the ticket's
own words begin. It is the document ` + "`feat implement --ticket`" + ` puts in the
brief field, and nothing else is printed with it.

The reference is matched exactly as the command printed it, which is what the
first column of the list shows. ` + "`feat implement --ticket`" + ` runs this same
command again and matches the same way.

--json prints the list, or the one ticket, as a JSON document instead.

Nothing is created by reading. Run ` + "`feat doctor`" + ` to check the command itself,
which validates its output without a running daemon.`

func newProjectTicketsCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tickets <project> [<ticket>]",
		Short: "List the project's tickets, or show one",
		Long:  projectTicketsLong,
		Args:  checkArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := env.resolve()
			if err != nil {
				return err
			}
			id := args[0]
			if err := domain.ProjectID(id).Validate(); err != nil {
				return err
			}
			reference := ""
			if len(args) == 2 {
				reference = args[1]
			}

			// The daemon runs the tracker command, because that is where every
			// credentialed provider call is made and where a ticket becomes a
			// task (ADR-070). Reading changes nothing, and it still does not
			// start a daemon: a command that reaches somebody's tracker should
			// not start a background process to do it.
			if status := daemon.Inspect(layout); !status.Running() {
				return &NotRunningError{Socket: layout.Socket}
			}
			caller := client.New(layout.Socket)
			defer caller.Close()

			return runTickets(cmd.Context(), cmd.OutOrStdout(), caller, id, reference, wantsJSON(cmd))
		},
	}
	addJSONFlag(cmd)
	return cmd
}

// ticketLister is what reading a project's tickets needs from the daemon.
//
// It is an interface so that what the command prints can be tested against a
// list arranged in the test, without a socket or a tracker.
type ticketLister interface {
	Tickets(ctx context.Context, id string) (api.TicketList, error)
}

// runTickets reads a project's tickets once and prints the list, or the one
// ticket a reference names.
//
// Both forms run the same command once: there is no endpoint for one ticket,
// because the tracker command prints a list and Feat matches within it
// (ADR-071). A reference that is not among what it printed is an error, and an
// error is never on standard output: the document is there or nothing is.
func runTickets(ctx context.Context, out io.Writer, caller ticketLister, id, reference string, asJSON bool) error {
	list, err := caller.Tickets(ctx, id)
	if err != nil {
		return err
	}

	if reference == "" {
		if asJSON {
			return emitJSON(out, list)
		}
		printTickets(out, id, list)
		return nil
	}

	ticket, err := api.FindTicket(list.Tickets, reference)
	if err != nil {
		return err
	}
	// The reference a task from this ticket would record, snapshot and all, so
	// that the document printed here and the brief the preparation screen
	// composes are one document rather than two renderings (ADR-070).
	found := api.NewTicketReference(ticket, list.ReadAt)
	if asJSON {
		return emitJSON(out, found)
	}
	_, brief := found.ComposeBrief()
	printf(out, "%s", brief)
	return nil
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
	printf(out, "read one with `feat tickets %s <ticket>`\n", id)
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
