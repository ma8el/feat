package compose

import (
	"context"
	"fmt"
	"path/filepath"
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
	runner    Runner
	docker    string
	identity  string
	directory string
}

// ByName returns the Compose project with this name, asked from this directory.
//
// The name is checked rather than trusted, for the reason removeVolumes checks a
// volume name: it becomes an argument to Docker, and a value that could be read
// as a flag is not one Feat passes on.
//
// The directory is required, and required to be absolute, because Compose has
// one when it is not given one: an invocation with no --project-directory and no
// working directory runs in whatever directory the daemon inherited from the
// shell that started it, and Compose discovers its files by walking up from
// there. A daemon started from an application repository — the repository that
// by construction holds the Compose files — would then be asking about this
// project through that repository's compose.yaml. What this type acts on must
// not depend on where `feat daemon start` was typed, so the caller names a
// directory Feat owns and every invocation below carries it.
func ByName(identity, directory string, opts Options) (*Project, error) {
	name := strings.TrimSpace(identity)
	if name == "" || strings.HasPrefix(name, "-") {
		return nil, fmt.Errorf("%q is not a Compose project name Feat will pass to Docker", identity)
	}
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("asking what Compose project %s still has needs an absolute directory to ask from, "+
			"and %q is not one", name, directory)
	}

	runner := opts.Runner
	if runner == nil {
		runner = HostRunner{}
	}
	docker, err := runner.Look(Executable)
	if err != nil {
		return nil, fmt.Errorf("asking what Compose project %s still has needs Docker, and %w", name, err)
	}
	return &Project{runner: runner, docker: docker, identity: name, directory: directory}, nil
}

// invoke builds one command of this project, run from the directory the caller
// named.
//
// Compose invocations also carry it as --project-directory, which is what the
// flag means: the directory a project's relative paths resolve against. This
// project has no files and therefore no relative paths, so what the flag buys
// here is the negative — Compose does not go looking for a directory of its own,
// and no file it discovers can become the model for a project Feat addresses by
// name alone.
func (p *Project) invoke(arguments ...string) execution.Invocation {
	return execution.Invocation{
		Program:   p.docker,
		Arguments: arguments,
		Directory: p.directory,
	}
}

// compose builds one Compose command of this project, by name and from the
// named directory.
func (p *Project) compose(arguments ...string) execution.Invocation {
	base := []string{
		"compose",
		"--project-name", p.identity,
		"--project-directory", p.directory,
	}
	return p.invoke(append(base, arguments...)...)
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
	// State is the state Docker names, such as `running` or `exited`. It is
	// Docker's own word rather than a flag of Feat's, for the reason Status is
	// kept verbatim: what the container runtime called something is what a
	// diagnostic should quote.
	//
	// Status is prose meant for a person and State is one of a fixed set, which
	// is why a rule reads this one. It is empty for a network, and empty for a
	// container Compose reported without one.
	State string
}

// Stopped reports whether Docker says the container's process has ended.
//
// Only `exited` and `dead` say so. Everything else — running, paused,
// restarting, created, removing, and a state Compose did not report at all —
// counts as not stopped, because this answer decides whether a directory a
// container mounts may be removed (ADR-059): a state nothing established must
// come out the way an unanswerable question does, which is the careful one.
//
// `created` is in the careful half deliberately. A container that was never
// started is unlikely to hold anything, and nothing has measured that, so it is
// not the place to spend the evidence ADR-059's rule was written from.
func (r Remaining) Stopped() bool {
	switch strings.ToLower(strings.TrimSpace(r.State)) {
	case "exited", "dead":
		return true
	default:
		return false
	}
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

// Live returns the containers that have not stopped.
//
// It is what the control-workspace ordering rule reads, and the distinction is
// the whole of it: ADR-059's evidence is that removing the tree failed while the
// container was up and succeeded once it had died, so a project reduced to
// exited containers is one whose mounts are released. Reporting those as holding
// the workspace refuses a cleanup in exactly the state the removal works in —
// which is what `feat task stop` overnight leaves behind (ADR-057).
func (r Remains) Live() Remains {
	var found Remains
	for _, entry := range r.Containers() {
		if !entry.Stopped() {
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
	output, err := p.runner.Run(ctx, p.compose("ps", "--all", "--format", "json"))
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
		found = append(found, Remaining{Kind: KindContainer, Name: name, Status: one.Status, State: one.State})
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
	output, err := p.runner.Run(ctx, p.invoke(
		"network", "ls",
		"--filter", "label="+composeProjectLabel+"="+p.identity,
		"--format", "{{.Name}}",
	))
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
	output, err := p.runner.Run(ctx, p.compose("down"))
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
	return listVolumes(ctx, p.runner, p.docker, p.directory, p.identity)
}

// RemoveVolumes removes the named volumes, one at a time, and reports which
// were removed.
func (p *Project) RemoveVolumes(ctx context.Context, names []string) ([]string, error) {
	return removeVolumes(ctx, p.runner, p.docker, p.directory, names)
}
