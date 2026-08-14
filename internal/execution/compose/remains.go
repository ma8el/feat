package compose

import (
	"context"
	"fmt"
	"strings"

	"github.com/ma8el/feat/internal/execution"
)

// Project is one task's agent Compose project addressed by its name alone.
//
// It is the other half of Environment, and what separates them is what each one
// needs in order to exist. An Environment is built from a specification —
// mounts, files, a service, a user — because creating a container requires every
// one of them. What is already on the machine requires none of it: Compose
// labels each container, network, and volume with the project it belongs to, so
// the name is enough to ask what exists and enough to remove it.
//
// That is what makes it the answer for a launch that failed after its container
// existed. Such a task records no environment — the session that would have
// carried it is never created — and the Compose file that would have to be read
// may be the very thing that was edited, since a change to it is what makes a
// launch slow enough to be interrupted in the first place. A name derived from
// the two identifiers survives both (ADR-033).
//
// It observes and removes; it never creates. A container this can find is one
// something else already made.
type Project struct {
	runner   Runner
	docker   string
	identity string
}

// ByName returns the Compose project with this name.
//
// The name is checked rather than trusted, for the reason removeVolumes checks a
// volume name: it becomes an argument to Docker, and a value that could be read
// as a flag is not one Feat passes on.
func ByName(identity string, opts Options) (*Project, error) {
	name := strings.TrimSpace(identity)
	if name == "" || strings.HasPrefix(name, "-") {
		return nil, fmt.Errorf("%q is not a Compose project name Feat will pass to Docker", identity)
	}

	runner := opts.Runner
	if runner == nil {
		runner = HostRunner{}
	}
	docker, err := runner.Look(Executable)
	if err != nil {
		return nil, fmt.Errorf("asking what Compose project %s still has needs Docker, and %w", name, err)
	}
	return &Project{runner: runner, docker: docker, identity: name}, nil
}

// Identity returns the Compose project name.
func (p *Project) Identity() string { return p.identity }

// Kind is what a remaining resource is.
type Kind string

// The kinds. Volumes are deliberately absent: they are a class of their own
// everywhere in cleanup, are retained by default, and are enumerated by Volumes
// rather than here (FR-CLEAN-004).
const (
	KindContainer Kind = "container"
	KindNetwork   Kind = "network"
)

// Remaining is one container or network carrying a Compose project's name.
type Remaining struct {
	// Kind is what it is.
	Kind Kind
	// Name is what Docker calls it.
	Name string
	// Status is what Docker says about a container, such as `Exited (137) 3
	// hours ago`. It is empty for a network.
	Status string
}

// Remains is everything a Compose project still has.
type Remains []Remaining

// Empty reports whether the project has nothing left.
func (r Remains) Empty() bool { return len(r) == 0 }

// Containers returns the containers among them.
func (r Remains) Containers() Remains {
	var found Remains
	for _, entry := range r {
		if entry.Kind == KindContainer {
			found = append(found, entry)
		}
	}
	return found
}

// Describe renders what is there for a person, in the order Docker reported it.
func (r Remains) Describe() string {
	parts := make([]string, 0, len(r))
	for _, entry := range r {
		part := string(entry.Kind) + " " + entry.Name
		if entry.Status != "" {
			part += " (" + entry.Status + ")"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

// Remains reports the containers and networks the project still has.
//
// Both are asked for, because either can outlive the other: a container removed
// by hand leaves the network behind, and a container that exited keeps it. A
// launch that was interrupted between the two leaves whichever it had reached.
//
// Neither question reads a Compose file. `ps` given a project name and no file
// answers from what Docker holds, which is the only source that can still be
// right about a project whose file has changed since (ADR-028's rule that Feat
// never renders a project's own configuration applies here as a consequence
// rather than as a precaution).
func (p *Project) Remains(ctx context.Context) (Remains, error) {
	found, err := p.containers(ctx)
	if err != nil {
		return nil, err
	}
	networks, err := p.networks(ctx)
	if err != nil {
		return nil, err
	}
	return append(found, networks...), nil
}

// containers asks Compose which containers carry the project name.
func (p *Project) containers(ctx context.Context) (Remains, error) {
	output, err := p.runner.Run(ctx, execution.Invocation{
		Program:   p.docker,
		Arguments: []string{"compose", "--project-name", p.identity, "ps", "--all", "--format", "json"},
	})
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		return nil, fmt.Errorf("asking Compose project %s what containers it has failed: %s",
			p.identity, firstLine(output.Stderr, output.Stdout))
	}

	parsed, err := parseContainers(output.Stdout)
	if err != nil {
		return nil, fmt.Errorf("reading the containers of Compose project %s: %w", p.identity, err)
	}
	var found Remains
	for _, one := range parsed {
		name := one.Name
		if name == "" {
			name = one.ID
		}
		found = append(found, Remaining{Kind: KindContainer, Name: name, Status: one.Status})
	}
	return found, nil
}

// networks asks Docker which networks carry the project name.
//
// By Compose's own label, as listVolumes does. A network Compose created for
// this project is labelled with it; a network the user attached the service to
// carries their own project's label or none, and is therefore not something this
// can name or remove.
func (p *Project) networks(ctx context.Context) (Remains, error) {
	output, err := p.runner.Run(ctx, execution.Invocation{
		Program: p.docker,
		Arguments: []string{
			"network", "ls",
			"--filter", "label=" + composeProjectLabel + "=" + p.identity,
			"--format", "{{.Name}}",
		},
	})
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		return nil, fmt.Errorf("listing the networks of Compose project %s failed: %s",
			p.identity, firstLine(output.Stderr, output.Stdout))
	}

	var found Remains
	for line := range strings.SplitSeq(output.Stdout, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			found = append(found, Remaining{Kind: KindNetwork, Name: name})
		}
	}
	return found, nil
}

// Destroy removes the containers and networks of the project.
//
// It is Environment.Destroy without the files, and it keeps every one of that
// method's rules: no --volumes, so a volume survives to be removed as the
// separate choice it is; no --remove-orphans, because an orphan is a container
// Feat did not put there; and nothing outside this project name, so the user's
// own containers are untouched by construction.
//
// A project with nothing left is not an error. Compose says so and exits zero,
// which is the same answer removeVolumes gives a volume that has already gone: a
// removal of something absent is a success, because what the user asked for is
// true.
func (p *Project) Destroy(ctx context.Context) error {
	output, err := p.runner.Run(ctx, execution.Invocation{
		Program:   p.docker,
		Arguments: []string{"compose", "--project-name", p.identity, "down"},
	})
	if err != nil {
		return err
	}
	if !output.Succeeded() {
		return fmt.Errorf("removing the containers and networks of Compose project %s failed: %s",
			p.identity, lastLine(output.Stderr, output.Stdout))
	}
	return nil
}

// Volumes lists the named volumes carrying the project's name.
func (p *Project) Volumes(ctx context.Context) ([]string, error) {
	return listVolumes(ctx, p.runner, p.docker, p.identity)
}

// RemoveVolumes removes the named volumes, one at a time, and reports which
// were removed.
func (p *Project) RemoveVolumes(ctx context.Context, names []string) ([]string, error) {
	return removeVolumes(ctx, p.runner, p.docker, names)
}
