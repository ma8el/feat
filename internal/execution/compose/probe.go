package compose

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/execution"
)

// ContainerClients are the executables inside an environment that speak a
// container runtime's API.
//
// Asking for one name was asking the wrong question. `podman` and `nerdctl` both
// speak the Docker API — podman ships a `docker` alias for exactly that reason —
// so an image carrying either has the capability agent.capabilities.docker
// declares denied, and reporting "no Docker client" about it would be a claim
// Feat had not checked.
//
// Each is probed by running it, as the hook tools are: --version creates
// nothing, and only the environment reporting no such executable means absent.
var ContainerClients = []string{Executable, "podman", "nerdctl"}

// EscalationTools are the executables that hand the agent back the privilege
// its user does not have, each with a way of asking whether they would do it
// without anybody being there to type a password.
//
// `id -u` answers who the agent starts as, and a session is longer than its
// first instant. An image that installs `sudo` and writes a NOPASSWD rule
// answers the root refusal with `dev` and gives the agent uid 0 for the asking,
// so the requirement is satisfied at the moment it is measured and not
// afterwards. What that reaches is the runtime's default capability set, which
// is smaller than a privileged container's and is not small: CAP_DAC_OVERRIDE
// is in Docker's default set, so every writable bind mount becomes writable
// regardless of what owns the files on the host — which is most of what the
// non-root requirement was written for. What it does not reach is
// CAP_SYS_ADMIN, which the default set excludes, so the read-only control
// workspace stays read-only by the kernel's rule.
//
// Each is probed by running it, as the hook tools are. `-n` is the whole of the
// question: it makes the tool fail rather than prompt when a password would be
// needed, so exiting zero means the privilege is there for the asking and
// anything else means it is not. `true` creates nothing.
//
// This is a list rather than a check for `sudo` because sudo is not the only
// binary that hands back privilege and the dogfood image is not the only image.
// What it cannot see is a rule narrow enough to exclude `true` — a sudoers file
// permitting one command — and Feat does not claim to have read a sudoers file
// it never opens. What is reported is what was established.
var EscalationTools = []struct {
	Name      string
	Arguments []string
}{
	{"sudo", []string{"-n", "true"}},
	{"doas", []string{"-n", "true"}},
}

// HookTools are the executables the generated provider hooks need, each with a
// harmless way of running it.
//
// The hooks are shell scripts that copy a payload into the control outbox, and
// an image missing one of these produces a session that runs perfectly well
// while Feat never hears from it again. That is the failure ADR-032 was written
// against, so it is checked rather than assumed.
//
// Each tool is probed by running it, because `command -v` is a shell builtin
// and `exec` starts a program rather than a shell: asking that way would report
// every tool as missing on every image. Every invocation below creates nothing
// and prints at most a word. `sh -c :` is the exception that proves the rule —
// the only way to find out whether a shell exists is to run one, and the script
// is a constant with nothing interpolated into it.
var HookTools = []struct {
	Name      string
	Arguments []string
}{
	{"sh", []string{"-c", ":"}},
	{"mktemp", []string{"-u"}},
	{"date", []string{"-u", "+%Y"}},
	{"cat", []string{"/dev/null"}},
	// --help because these three cannot be run harmlessly otherwise. Whether it
	// exits 0 or prints usage and exits 1 does not matter: both mean the image
	// has the tool, and only "no such executable" means it does not.
	{"touch", []string{"--help"}},
	{"mv", []string{"--help"}},
	{"rm", []string{"--help"}},
}

// Inspect asks a running container what it is.
//
// Every question run inside the container is asked as the user the agent will
// run as, because the answer for another user is not the answer to the
// question. The paths are the agent's own view of them.
//
// What is asked of the container runtime about the container is what the rules
// read: its mounts, its environment, and what it was granted beyond them. A
// question nobody asks is a rule nobody can enforce, which is how a privileged
// container passed a check that refuses a home-directory mount.
func (e *Environment) Inspect(ctx context.Context, writable []string) (execution.Report, error) {
	// The container is resolved here rather than passed in: the environment
	// knows which one is its own, and a caller that had to find out first could
	// find out wrongly.
	state, err := e.Observe(ctx)
	if err != nil {
		return execution.Report{}, err
	}
	if !state.Running {
		return execution.Report{}, fmt.Errorf("service %s of Compose project %s is not running, so there is nothing to inspect",
			e.spec.Service, e.spec.Identity)
	}

	report := execution.Report{Container: state.Container, Unwritable: make(map[string]string)}

	identity, err := e.Run(ctx, execution.Command{Program: "id", Arguments: []string{"-u"}})
	if err != nil {
		return report, fmt.Errorf("asking service %s who it runs as: %w", e.spec.Service, err)
	}
	if identity.Succeeded() {
		if uid, convErr := strconv.Atoi(strings.TrimSpace(identity.Stdout)); convErr == nil {
			report.UID, report.UIDKnown = uid, true
		}
	}
	if name, nameErr := e.Run(ctx, execution.Command{Program: "id", Arguments: []string{"-un"}}); nameErr == nil {
		report.User = strings.TrimSpace(name.Stdout)
	}

	// A client that speaks a container runtime's API is refused whether or not a
	// socket happens to be mounted today: it is a capability the project
	// declared denied, and a mount can be added by an edit nobody reviewed.
	for _, client := range ContainerClients {
		present, err := e.present(ctx, client, []string{"--version"})
		if err != nil {
			return report, err
		}
		if present {
			report.DockerClients = append(report.DockerClients, client)
		}
	}

	// What the agent can become, beside what it starts as. It is asked here and
	// answered by a warning rather than a refusal: a project may want its agent
	// to install a package mid-session, and Feat cannot tell that choice apart
	// from an image that inherited the line from a template. Both are told.
	for _, tool := range EscalationTools {
		granted, err := e.escalates(ctx, tool.Name, tool.Arguments)
		if err != nil {
			return report, err
		}
		if granted {
			report.Escalation = append(report.Escalation, tool.Name)
		}
	}

	for _, tool := range HookTools {
		present, err := e.present(ctx, tool.Name, tool.Arguments)
		if err != nil {
			return report, err
		}
		if !present {
			report.MissingTools = append(report.MissingTools, tool.Name)
		}
	}

	for _, directory := range writable {
		if reason := e.probeWrite(ctx, directory); reason != "" {
			report.Unwritable[directory] = reason
		}
	}

	mounts, err := e.Mounts(ctx, state.Container)
	if err != nil {
		return report, err
	}
	report.Mounts = mounts

	endpoints, err := e.Endpoints(ctx, state.Container)
	if err != nil {
		return report, err
	}
	report.DockerVariables = endpoints

	// What the container was granted beyond its mounts. It is the third
	// question and the one that decides whether the other two mean anything:
	// a privileged container remounts what the mount rules hold read-only.
	privileges, err := e.Privileges(ctx, state.Container)
	if err != nil {
		return report, err
	}
	report.Privileges = privileges
	return report, nil
}

// present reports whether an executable exists in the environment.
//
// A tool that runs and fails is still present: the question here is whether the
// image has it, and "it ran and disagreed" is a different answer from "there is
// nothing to run". Only the environment reporting no such executable means
// absent.
func (e *Environment) present(ctx context.Context, program string, arguments []string) (bool, error) {
	_, err := e.Run(ctx, execution.Command{Program: program, Arguments: arguments})
	if errors.Is(err, ErrNotInEnvironment) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// escalates reports whether an executable hands the agent privilege without
// asking anybody for it.
//
// Presence is not the question, which is what separates this from present(): an
// image carrying `sudo` whose sudoers file grants the agent nothing is an image
// where the non-root requirement holds, and warning about it would teach a user
// to ignore the warning. Only a tool that ran and exited zero counts — a tool
// that ran and refused has just demonstrated the requirement holding.
func (e *Environment) escalates(ctx context.Context, program string, arguments []string) (bool, error) {
	output, err := e.Run(ctx, execution.Command{Program: program, Arguments: arguments})
	if errors.Is(err, ErrNotInEnvironment) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return output.Succeeded(), nil
}

// probeWrite reports why a directory cannot be written to, or "" when it can.
//
// It writes a file rather than reading permission bits. Across a bind mount the
// bits are the host's and the identity is the container's, and whether those two
// agree is exactly the question: on Docker Desktop they are reconciled by the
// file-sharing layer, and on Linux they are not (ADR-033 evidence 5).
func (e *Environment) probeWrite(ctx context.Context, directory string) string {
	probe := path.Join(directory, ".feat-write-probe")

	output, err := e.Run(ctx, execution.Command{Program: "touch", Arguments: []string{probe}})
	if err != nil {
		return err.Error()
	}
	if !output.Succeeded() {
		return firstLine(output.Stderr, output.Stdout)
	}
	// Best effort: a probe file left behind is untidy, and failing the launch
	// over it would be worse than untidy.
	_, _ = e.Run(ctx, execution.Command{Program: "rm", Arguments: []string{"-f", probe}})
	return ""
}

// Check reports every reason the container may not run an agent.
//
// It returns them together rather than one at a time, for the reason
// configuration validation does: a devcontainer is edited by hand, and finding
// three problems one launch at a time is three times the work.
func (e *Environment) Check(report execution.Report) error {
	var problems []error

	switch {
	case !report.UIDKnown:
		problems = append(problems, fmt.Errorf(
			"the agent's identity in service %s could not be read, so Feat cannot tell whether it would run as root",
			e.spec.Service))
	case report.UID == 0:
		problems = append(problems, fmt.Errorf(
			"the agent would run as root in service %s (uid 0, %q). The security model requires a non-root agent "+
				"user: give the image a non-root user and name it in agent.execution.user",
			e.spec.Service, report.User))
	}

	if len(report.DockerClients) > 0 {
		problems = append(problems, fmt.Errorf(
			"service %s has a client that speaks the Docker API (%s), and agent.capabilities.docker is "+
				"denied. An agent that can reach a container daemon can reach the host; remove it from the image",
			e.spec.Service, strings.Join(report.DockerClients, ", ")))
	}

	if len(report.DockerVariables) > 0 {
		problems = append(problems, fmt.Errorf(
			"service %s sets %s in the agent's environment, which points a client at a container daemon over "+
				"the network, and agent.capabilities.docker is denied. A daemon reached that way is the same "+
				"capability as a mounted socket. Feat has not read the values, only the names; remove those "+
				"entries from the Compose files that define service %s",
			e.spec.Service, strings.Join(report.DockerVariables, ", "), e.spec.Service))
	}

	if len(report.MissingTools) > 0 {
		problems = append(problems, fmt.Errorf(
			"service %s is missing %s, which the generated hooks use to report what the session is doing. "+
				"Without them the agent would run and Feat would never hear from it",
			e.spec.Service, strings.Join(report.MissingTools, ", ")))
	}

	for _, directory := range sorted(report.Unwritable) {
		problems = append(problems, fmt.Errorf(
			"the agent cannot write to %s in service %s: %s. Its user is %s (uid %d), and across a bind mount "+
				"that user must be able to write what the host owns; on Linux this usually means the container "+
				"user's uid does not match yours",
			directory, e.spec.Service, report.Unwritable[directory], report.User, report.UID))
	}

	if err := e.CheckPrivileges(report.Privileges); err != nil {
		problems = append(problems, err)
	}

	if err := e.CheckMounts(report.Mounts); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// Warnings reports what the container grants that the launch will not refuse
// over.
//
// A refusal answers a rule; this answers a requirement that holds only until
// somebody asks otherwise, which is a different thing and reads wrongly as
// either of the two answers Check has. Dropping the grant instead was
// considered and rejected: a project that wants its agent to install packages
// mid-session is entitled to say so, and a launch that refused it would be
// answered by whichever edit made the check stop looking. What Feat can do
// honestly is say what it found, every time, so the choice is made out loud.
func (e *Environment) Warnings(report execution.Report) []string {
	var warnings []string

	if len(report.Escalation) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"the agent runs as %s (uid %d) in service %s, and %s there returns root without a password, so the "+
				"non-root requirement holds only until the agent asks. With the runtime's default capabilities "+
				"that is enough to write through every writable mount whatever owns the files on the host, and "+
				"not enough to remount a read-only one. If the image does not need it, remove %s and its sudoers "+
				"entry from the Compose files that build service %s",
			report.User, report.UID, e.spec.Service, strings.Join(report.Escalation, ", "),
			strings.Join(report.Escalation, "/"), e.spec.Service))
	}
	return warnings
}

// sorted returns a map's keys in a fixed order, so a message with several
// problems reads the same way every time.
func sorted(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
