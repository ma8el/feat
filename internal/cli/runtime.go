package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/daemon"
)

const runtimeLong = `Manage the application services of one task.

The lifecycle is manual: Feat starts nothing on your behalf and stops nothing
when a task reaches review or approval. Each task's services run under their own
Compose project, so an action reaches one task and no other.

Destroying removes the containers and networks the task owns. Volumes are always
retained, and resources the project declares external — a shared staging
database, for instance — are never touched.`

func newRuntimeCommand(env *environment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Control a task's application runtime",
		Long:  runtimeLong,
	}
	cmd.AddCommand(
		newRuntimeActionCommand(env, api.RuntimeCreate, "create <task>",
			"Create the task's application containers without starting them"),
		newRuntimeActionCommand(env, api.RuntimeStart, "start <task>",
			"Start the task's application services"),
		newRuntimeActionCommand(env, api.RuntimeStop, "stop <task>",
			"Stop the task's application services, keeping their containers"),
		newRuntimeActionCommand(env, api.RuntimeObserve, "status <task>",
			"Show what the task's application services are doing"),
		newRuntimeLogsCommand(env),
		newRuntimeDestroyCommand(env),
	)
	return cmd
}

// newRuntimeActionCommand builds the four actions that need no confirmation.
func newRuntimeActionCommand(env *environment, action api.RuntimeAction, use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  withTaskArgument(short + "."),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRuntimeClient(env, cmd, func(caller *client.Client) error {
				announce(cmd.ErrOrStderr(), action, args[0])

				status, err := caller.Runtime(cmd.Context(), args[0], action)
				if err != nil {
					return err
				}
				printRuntime(cmd.OutOrStdout(), status)
				return nil
			})
		},
	}
}

// announce says that a slow action has begun.
//
// Compose is not asked to narrate: the daemon runs it and this command holds one
// request open until it answers, which for a first create or start is the images
// being pulled and the builds being run — minutes, against about a second for
// every start after it (ADR-034 evidence 14). A command that printed nothing for
// that long would read as a hang, and interrupting it is how a user would leave
// Compose stopped part way through creating their application.
//
// On the error stream, so that whatever parses this command's output reads the
// same thing it always did.
func announce(err io.Writer, action api.RuntimeAction, task string) {
	if action != api.RuntimeCreate && action != api.RuntimeStart {
		return
	}
	printf(err, "feat: asking the daemon to %s the services of task %s; "+
		"the first one pulls images and runs builds, which takes minutes\n", action, task)
}

const destroyLong = `Remove the containers and networks of the task's application services.

Volumes are retained: removing one is a separate choice, and Feat does not make
it for you. Resources the project declares external are never touched.

The command asks for confirmation unless --yes is given.`

func newRuntimeDestroyCommand(env *environment) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "destroy <task>",
		Short: "Remove the task's application containers and networks, retaining volumes",
		Long:  withTaskArgument(destroyLong),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRuntimeClient(env, cmd, func(caller *client.Client) error {
				if !yes {
					confirmed, err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(),
						"Remove the containers and networks of task "+args[0]+"? Volumes are retained.")
					if err != nil {
						return err
					}
					if !confirmed {
						printf(cmd.OutOrStdout(), "nothing was removed\n")
						return nil
					}
				}

				status, err := caller.Runtime(cmd.Context(), args[0], api.RuntimeDestroy)
				if err != nil {
					return err
				}
				printRuntime(cmd.OutOrStdout(), status)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}

const logsLong = `Open the normal Docker Compose logs of the task's application services.

Feat does not aggregate, store, or re-render them: it resolves which Compose
project belongs to the task and runs the ordinary command, following the output
until you interrupt it.`

func newRuntimeLogsCommand(env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "logs <task>",
		Short: "Follow the task's normal Compose logs",
		Long:  withTaskArgument(logsLong),
		Args:  checkArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRuntimeClient(env, cmd, func(caller *client.Client) error {
				command, err := caller.RuntimeLogs(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return runLogs(cmd.Context(), command, cmd.OutOrStdout(), cmd.ErrOrStderr())
			})
		},
	}
}

// withRuntimeClient runs an action against a daemon, or reports that none is
// running.
func withRuntimeClient(env *environment, cmd *cobra.Command, action func(*client.Client) error) error {
	layout, err := env.resolve()
	if err != nil {
		return err
	}
	if status := daemon.Inspect(layout); !status.Running() {
		return &NotRunningError{Socket: layout.Socket}
	}

	caller := client.New(layout.Socket)
	defer caller.Close()

	if err := action(caller); err != nil {
		if errors.Is(err, client.ErrDaemonNotRunning) {
			return &NotRunningError{Socket: layout.Socket}
		}
		return err
	}
	return nil
}

// logsProgram is the only executable this command will run.
//
// The daemon builds the command and this process runs it, which is the same
// division `feat attach` uses for native tmux. The client still checks what it
// was handed: they are the same user, and a client that ran whatever it received
// would be one nobody could reason about.
const logsProgram = "docker"

// logsCommand checks what the daemon returned and builds the process.
//
// It is shared by `feat runtime logs` and by the dashboard's logs action, so
// that there is one place where a returned command becomes a process and one
// place where it is checked — the division `feat attach` uses for tmux targets.
func logsCommand(ctx context.Context, command api.RuntimeCommand) (*exec.Cmd, error) {
	if base := filepath.Base(command.Program); base != logsProgram {
		return nil, fmt.Errorf("the daemon returned the program %q for the task's logs, and this command runs "+
			"only %s", command.Program, logsProgram)
	}
	if !filepath.IsAbs(command.Program) {
		return nil, fmt.Errorf("the daemon returned the non-absolute program %q for the task's logs", command.Program)
	}
	for _, argument := range command.Arguments {
		if strings.ContainsAny(argument, "\x00\n\r") {
			return nil, fmt.Errorf("the daemon returned an argument containing a newline for the task's logs")
		}
	}

	// #nosec G204 -- the program is checked above to be the absolute path of the
	// container tool, and every argument is one vector element that never reaches
	// a shell.
	process := exec.CommandContext(ctx, command.Program, command.Arguments...)
	process.Dir = command.Directory
	return process, nil
}

// runLogs runs the Compose logs command with this process's own streams.
func runLogs(ctx context.Context, command api.RuntimeCommand, stdout, stderr io.Writer) error {
	process, err := logsCommand(ctx, command)
	if err != nil {
		return err
	}
	process.Stdout = stdout
	process.Stderr = stderr

	if err := process.Run(); err != nil {
		if ctx.Err() != nil {
			// Interrupting the logs is how a user leaves them, not a failure.
			return nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("docker compose logs exited with status %d", exit.ExitCode())
		}
		return fmt.Errorf("running docker compose logs: %w", err)
	}
	return nil
}

// confirm asks a yes-or-no question, defaulting to no.
//
// Anything other than an explicit yes is a no, including an unreadable answer: a
// command that removes something should never proceed because it could not tell
// what it was told.
func confirm(in io.Reader, out io.Writer, question string) (bool, error) {
	printf(out, "%s [y/N]: ", question)

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// printRuntime renders what an action observed.
//
// The services are listed one per line rather than as a summary, because a
// runtime is degraded exactly when they disagree with each other, and a user
// looking at a degraded runtime wants to know which one.
func printRuntime(out io.Writer, status api.RuntimeStatus) {
	runtime := status.Task.Runtime
	if runtime == nil {
		printf(out, "task %s has no application runtime\n", status.Task.Key)
		return
	}

	printf(out, "%s  %s", status.Task.Key, runtime.State)
	if runtime.Health != "" && runtime.Health != "unknown" {
		printf(out, ", health %s", runtime.Health)
	}
	printf(out, "\n")
	printf(out, "compose project  %s\n", runtime.Identity)

	if len(status.Services) > 0 {
		// The source column appears only when there is something to say: most
		// projects manage every service they define, and a column reading
		// "configured" all the way down would be a column nobody reads.
		dependencies := dependencies(status.Services)

		rows := &table{}
		if dependencies {
			rows.add("SERVICE", "STATE", "HEALTH", "STATUS", "SOURCE")
		} else {
			rows.add("SERVICE", "STATE", "HEALTH", "STATUS")
		}
		for _, service := range status.Services {
			cells := []string{service.Name, valueOr(service.State, absent),
				valueOr(service.Health, absent), valueOr(service.Status, absent)}
			if dependencies {
				cells = append(cells, source(service))
			}
			rows.add(cells...)
		}
		printf(out, "\n")
		rows.render(out, "")

		if dependencies {
			printf(out, "\na dependency is a service Compose started because a configured one needs it.\n")
			printf(out, "It belongs to this task, and Feat starts, stops, and removes it with the rest.\n")
		}
	}

	// The allocations rather than the observed publications: they are the same
	// ports once the services are up, and they exist before anything is started,
	// which is when a user most wants to know where their application will be.
	// What was observed is the answer to a different question and is printed
	// only when it disagrees.
	if len(runtime.Allocations) > 0 {
		printf(out, "\nports, allocated for this task\n")
		for _, allocation := range runtime.Allocations {
			printf(out, "  %s  %d -> %s\n", allocation.Service, allocation.ContainerPort, allocation.Address)
		}
	}
	if unexpected := unallocated(runtime); len(unexpected) > 0 {
		printf(out, "\nports published without an allocation\n")
		for _, port := range unexpected {
			printf(out, "  %s  %d -> %d\n", port.Service, port.ContainerPort, port.HostPort)
		}
		printf(out, "\nthese are bound by containers Feat did not publish, so a second task cannot have\n")
		printf(out, "them. Recreating the runtime writes the generated override again.\n")
	}
	if len(runtime.Volumes) > 0 {
		// Named because they are retained: a resource nobody can see is a
		// resource nobody will remove (FR-CLEAN-004).
		printf(out, "\nvolumes, retained\n")
		for _, volume := range runtime.Volumes {
			printf(out, "  %s\n", volume)
		}
	}
	for _, note := range status.Notes {
		printf(out, "\nnote: %s\n", note)
	}
}

// unallocated are the observed publications Feat did not allocate.
//
// A container from before this build, or one started from a generated override
// that has since been rewritten, can still hold a port of the project's own —
// and a host port is global to the machine, so that is exactly what stops the
// next task. Saying it is the difference between a user who recreates the
// runtime and one who wonders why their second task will not start.
func unallocated(runtime *api.Runtime) []api.Port {
	allocated := make(map[string]bool, len(runtime.Allocations))
	for _, allocation := range runtime.Allocations {
		allocated[fmt.Sprintf("%s/%d", allocation.Service, allocation.HostPort)] = true
	}

	var unexpected []api.Port
	for _, port := range runtime.Ports {
		if !allocated[fmt.Sprintf("%s/%d", port.Service, port.HostPort)] {
			unexpected = append(unexpected, port)
		}
	}
	return unexpected
}

// dependencies reports whether anything in the task's Compose project is there
// because a configured service needs it.
func dependencies(services []api.RuntimeService) bool {
	for _, service := range services {
		if !service.Managed {
			return true
		}
	}
	return false
}

// source says where a service came from, in the user's terms: the project's
// configuration, or another service's needs.
func source(service api.RuntimeService) string {
	if service.Managed {
		return "configured"
	}
	return "dependency"
}

// valueOr renders a value, or the absent marker when it is empty.
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
