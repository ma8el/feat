package compose

import (
	"context"
	"fmt"
	"strings"

	"github.com/ma8el/feat/internal/execution"
)

// composeProjectLabel is the label Docker Compose puts on every resource it
// creates, naming the project it belongs to. It is Compose's own, not Feat's:
// asking Docker which volumes carry it is how the exact set a task owns is
// resolved without reading any Compose file, and therefore without rendering
// the values of the project's environment files (ADR-028, ADR-034).
const composeProjectLabel = "com.docker.compose.project"

// Stop stops this task's agent containers and keeps them.
//
// `stop` rather than `down`: what a user is asking for is the task's agent to
// sleep, and the container that comes back has the same identity, the same
// generated mounts, and the same volumes as the one that went away. Removing it
// is cleanup's, and cleanup asks first.
//
// It names no service, so it stops the whole of this task's Compose project.
// Stopping only the agent service would leave whatever the devcontainer's own
// file starts beside it — a database, a message broker — running and holding its
// resources with no agent to use them, which is the shape ADR-034 evidence 12
// recorded for the application runtime and is no different here.
func (e *Environment) Stop(ctx context.Context) (execution.State, error) {
	output, err := e.runner.Run(ctx, e.invoke("stop"))
	if err != nil {
		return execution.State{}, err
	}
	if !output.Succeeded() {
		return execution.State{}, fmt.Errorf("stopping the agent containers of Compose project %s failed: %s",
			e.spec.Identity, lastLine(output.Stderr, output.Stdout))
	}
	return e.Observe(ctx)
}

// Destroy removes the containers and networks of this task's agent environment.
//
// It is the method ADR-033 deferred to whatever owns what cleanup retains.
// Three things it deliberately does not do, each a rule rather than an omission:
//
//   - no --volumes, so every volume survives. A task's Claude configuration
//     volume in particular holds a login, and removing it is a separate choice a
//     user makes explicitly (FR-CLEAN-004);
//   - no --remove-orphans, because an orphan is a container Feat did not put
//     there;
//   - nothing outside this Compose project, so the user's own containers are
//     untouched by construction rather than by exclusion.
func (e *Environment) Destroy(ctx context.Context) (execution.State, error) {
	output, err := e.runner.Run(ctx, e.invoke("down"))
	if err != nil {
		return execution.State{}, err
	}
	if !output.Succeeded() {
		reported := lastLine(output.Stderr, output.Stdout)
		return execution.State{}, fmt.Errorf("removing the agent containers of Compose project %s failed: %s",
			e.spec.Identity, reported)
	}
	return e.Observe(ctx)
}

// Volumes lists the named volumes Compose labelled with this task's project.
//
// It asks Docker rather than reading the Compose files, so the answer is what
// exists rather than what was declared, and a volume the project declares
// external carries another project's label — or none — and cannot appear here.
// That makes "cleanup never touches an external resource" a property of the
// enumeration rather than a filter somebody has to remember to apply.
func (e *Environment) Volumes(ctx context.Context) ([]string, error) {
	return listVolumes(ctx, e.runner, e.docker, e.spec.Directory, e.spec.Identity)
}

// RemoveVolumes removes the named volumes, one at a time, and reports which
// were removed.
//
// By name rather than through `docker compose down --volumes`, which is all or
// nothing: a plan that names exactly what will go, and a command that removes
// exactly that, is what FR-CLEAN-001 means by resolving the exact task-owned
// resources. A volume that is already gone is not an error, and a volume still
// in use is reported with the reason rather than forced (ADR-037).
func (e *Environment) RemoveVolumes(ctx context.Context, names []string) ([]string, error) {
	return removeVolumes(ctx, e.runner, e.docker, e.spec.Directory, names)
}

// listVolumes is shared by the two methods above and by nothing else. The
// application-runtime adapter has its own copy, because the two adapters are
// separate concepts and ADR-034 pays a hundred lines to keep them so.
//
// The directory is where the command runs. These two read no file and would
// answer the same from anywhere, so it buys nothing here beyond the rule it
// keeps whole: no invocation this adapter makes runs from a directory Feat did
// not choose.
func listVolumes(ctx context.Context, runner Runner, docker, directory, identity string) ([]string, error) {
	output, err := runner.Run(ctx, execution.Invocation{
		Program: docker,
		Arguments: []string{
			"volume", "ls",
			"--filter", "label=" + composeProjectLabel + "=" + identity,
			"--format", "{{.Name}}",
		},
		Directory: directory,
	})
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		return nil, fmt.Errorf("listing the volumes of Compose project %s failed: %s",
			identity, firstLine(output.Stderr, output.Stdout))
	}

	var names []string
	for line := range strings.SplitSeq(output.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names, nil
}

func removeVolumes(ctx context.Context, runner Runner, docker, directory string, names []string) ([]string, error) {
	var removed []string
	for _, name := range names {
		if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
			return removed, fmt.Errorf("%q is not a volume name Feat will pass to Docker", name)
		}
		output, err := runner.Run(ctx, execution.Invocation{
			Program:   docker,
			Arguments: []string{"volume", "rm", name},
			Directory: directory,
		})
		if err != nil {
			return removed, err
		}
		if !output.Succeeded() {
			reported := firstLine(output.Stderr, output.Stdout)
			if strings.Contains(strings.ToLower(reported), "no such volume") {
				// Already gone. A cleanup of something that is not there is a
				// success: the user asked for it to be absent, and it is.
				continue
			}
			return removed, fmt.Errorf("removing volume %s failed: %s", name, reported)
		}
		removed = append(removed, name)
	}
	return removed, nil
}
