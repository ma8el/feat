package config

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// Draft is a project configuration before it is a file.
//
// It exists so that the answers to a question and the text written down are the
// same thing twice rather than two representations that can disagree: a caller
// collects answers into a Draft, and Config renders it, parses the rendering,
// resolves it, and validates it. What that returns is a configuration Feat
// itself accepts, which is the only kind worth writing to disk.
//
// It is deliberately smaller than Config. A generated file states what the user
// decided and leaves every default out, because a default written down is a
// value that stops following Feat when Feat's own changes — and
// `feat project show` prints the resolved configuration, so a default left out
// of the file is still a default the user can read.
type Draft struct {
	// ID identifies the project and names its file.
	ID string
	// Name is the display name. It is written even when it equals the
	// identifier, because it is the one field a user is most likely to want to
	// change afterwards.
	Name string
	// Repositories are the project's repositories, in the order they were
	// answered. Rendering sorts them; the order here is the caller's.
	Repositories []DraftRepository
	// Primary names the repository a task works in by default.
	Primary string
	// BasePolicy is written only when it is set. An empty policy leaves the
	// default in place, which is what a project with an ordinary remote wants.
	BasePolicy string
	// Execution says where the agent runs.
	Execution DraftExecution
	// Capabilities declare what the agent environment may reach.
	Capabilities DraftCapabilities
	// Runtime configures the application services a task may run. A nil value
	// writes no runtime section, which is a project with no application
	// services.
	Runtime *DraftRuntime
	// Checks are verification commands, in the order they were answered.
	Checks []DraftCheck
}

// DraftRepository is one repository of a drafted project.
type DraftRepository struct {
	// ID identifies the repository within the project.
	ID string
	// HostPath is the ordinary checkout on this machine.
	HostPath string
	// ContainerPath is where task worktrees are mounted in a devcontainer. It
	// is written only when there is one.
	ContainerPath string
	// DefaultBranch is the branch a base policy resolves against.
	DefaultBranch string
	// Remote is the Git remote a base policy fetches.
	Remote string
	// DefaultAccess is the repository's default participation in a task.
	DefaultAccess string
}

// DraftExecution is the agent's execution environment.
type DraftExecution struct {
	// Mode is host or devcontainer.
	Mode string
	// ComposeFiles are the Compose files defining the devcontainer.
	ComposeFiles []string
	// Service is the Compose service the agent runs in.
	Service string
	// User is the non-root container user the agent runs as.
	User string
	// ClaudeConfigVolume is the dedicated Claude configuration volume. An empty
	// value mounts nothing, leaving Claude's configuration to the project's own
	// Compose files.
	ClaudeConfigVolume string
}

// DraftCapabilities are the capabilities a drafted project declares.
//
// Only the two that vary are here. The other three accept one value each, and
// rendering writes them with the sentence that says why, because a file that
// states what the agent may reach is the file somebody deciding to run Feat on
// their own work will read (docs/05-security-model.md).
type DraftCapabilities struct {
	// GitHubCLI is whether `gh` is disabled, optional, or required.
	GitHubCLI string
	// GitLabCLI is whether `glab` is disabled, optional, or required.
	GitLabCLI string
}

// DraftRuntime is the application runtime of a drafted project.
type DraftRuntime struct {
	// ComposeFiles are the Compose files defining the application services.
	ComposeFiles []string
	// EnvFiles are environment files passed to Compose by path. Feat records
	// the paths and never reads what is in them.
	EnvFiles []string
	// Services are the services Feat manages for a task.
	Services []string
}

// DraftCheck is one verification command of a drafted project.
type DraftCheck struct {
	// Repository is the repository the check runs for.
	Repository string
	// ID identifies the check within its repository.
	ID string
	// Command is the check's argument vector.
	Command []string
	// Execution is agent or host.
	Execution string
}

// Config renders the draft and loads the rendering back, returning the
// configuration and the text it was read from.
//
// The text is returned rather than left to be rendered again by the caller, so
// that what a caller displays and writes is the exact text that was validated.
// Rendering is deterministic, but "the same because it is the same bytes" is a
// property worth having rather than one worth re-establishing.
//
// The file path is where the configuration is meant to live: parsing compares
// it with the project identifier, so a draft that would be written under
// another name is refused here rather than after the file exists.
//
// A failure is a *Error, exactly as a hand-edited file's would be, and it names
// the field: the draft and the file have the same field names, so a problem
// found in the rendering is a problem the caller can put back to the user in
// the terms they answered in.
func (d Draft) Config(file string, opts Options) (*Config, []byte, error) {
	rendered := d.Render()

	cfg, err := Parse(file, rendered)
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Resolve(opts); err != nil {
		return nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	return cfg, rendered, nil
}

// Render writes the draft as YAML.
//
// The result is commented, because the file outlives the conversation that
// produced it: what a user does next with a generated project is edit it, and a
// file that says what its fields mean is one they can edit without leaving the
// editor. The comments say what the value is for, never what the user answered.
func (d Draft) Render() []byte {
	doc := &document{}

	doc.comment(0,
		"Feat project configuration, written by `feat project init`.",
		"",
		"It is yours to edit. Feat re-reads it on `feat project add "+d.ID+"`, and",
		"`feat project show "+d.ID+"` prints it with every default filled in, which is",
		"how to see the fields this file leaves out.",
		"",
		"docs/examples/project.yaml in the Feat repository documents every field, and",
		"schema/feat-project.schema.json describes the file for an editor.",
	)
	doc.blank()
	doc.field(0, "version", SchemaVersion)

	d.renderProject(doc)
	d.renderRepositories(doc)
	d.renderGit(doc)
	d.renderAgent(doc)
	d.renderRuntime(doc)
	d.renderChecks(doc)

	return doc.bytes()
}

func (d Draft) renderProject(doc *document) {
	doc.blank()
	doc.key(0, "project")
	doc.field(1, "id", d.ID)
	doc.field(1, "name", d.Name)
	doc.comment(1, "Where a task works by default. It must be a repository a task can edit.")
	doc.field(1, "primary_repository", d.Primary)
}

func (d Draft) renderRepositories(doc *document) {
	doc.blank()
	doc.key(0, "repositories")
	for i, repository := range d.Repositories {
		if i > 0 {
			doc.blank()
		}
		doc.key(1, repository.ID)
		doc.field(2, "host_path", repository.HostPath)
		if repository.ContainerPath != "" {
			doc.comment(2, "Where this repository's task worktrees are mounted in the devcontainer.")
			doc.field(2, "container_path", repository.ContainerPath)
		}
		doc.field(2, "default_branch", repository.DefaultBranch)
		doc.field(2, "remote", repository.Remote)
		doc.comment(2, "read_write, read_only, selectable, stable_read_only, or omitted.")
		doc.field(2, "default_access", repository.DefaultAccess)
	}
}

func (d Draft) renderGit(doc *document) {
	if d.BasePolicy == "" {
		return
	}
	doc.blank()
	doc.key(0, "git")
	doc.comment(1,
		"How a task's base commit is resolved: remote, local, current, or explicit.",
		"Feat records the commit it resolves, and that commit never moves again for",
		"the lifetime of the task.")
	doc.field(1, "base_policy", d.BasePolicy)
}

func (d Draft) renderAgent(doc *document) {
	doc.blank()
	doc.key(0, "agent")
	doc.blank()
	doc.key(1, "execution")
	doc.comment(2,
		"\"host\" runs the agent in the primary task worktree, with no container",
		"boundary. \"devcontainer\" runs it as a non-root user in a Compose service.")
	doc.field(2, "mode", d.Execution.Mode)

	if d.Execution.Mode == ModeDevcontainer {
		doc.list(2, "compose_files", d.Execution.ComposeFiles)
		doc.field(2, "service", d.Execution.Service)
		doc.comment(2, "The agent must not run as root.")
		doc.field(2, "user", d.Execution.User)

		if d.Execution.ClaudeConfigVolume != "" {
			doc.blank()
			doc.key(1, "claude")
			doc.comment(2,
				"A dedicated volume for Claude's own configuration, so that one",
				"interactive login is not your ~/.claude in every task container.")
			doc.field(2, "config_volume", d.Execution.ClaudeConfigVolume)
		}
	}

	doc.blank()
	doc.key(1, "capabilities")
	doc.comment(2,
		"These three accept one value each. Feat has no mechanism that varies them,",
		"so any other value would be a promise the binary does not keep: the agent",
		"never receives Docker, Feat implements no network restriction and claims no",
		"data-loss prevention, and a Git worktree shares repository metadata with the",
		"agent.")
	doc.field(2, "docker", CapabilityDenied)
	doc.field(2, "network", CapabilityUnrestricted)
	doc.field(2, "git", CapabilityFull)
	doc.comment(2,
		"disabled, optional, or required. \"required\" fails a task launch when the",
		"CLI is missing from the agent's environment.")
	doc.field(2, "github_cli", orDisabled(d.Capabilities.GitHubCLI))
	doc.field(2, "gitlab_cli", orDisabled(d.Capabilities.GitLabCLI))
}

func (d Draft) renderRuntime(doc *document) {
	if d.Runtime == nil {
		return
	}
	doc.blank()
	doc.comment(0, "The application services a task may run. They start only when you ask.")
	doc.key(0, "runtime")
	doc.list(1, "compose_files", d.Runtime.ComposeFiles)
	if len(d.Runtime.EnvFiles) > 0 {
		doc.comment(1,
			"Passed to Docker Compose by path. Feat never reads what is in them, and",
			"never copies a value out of them into anything it generates.")
		doc.list(1, "env_files", d.Runtime.EnvFiles)
	}
	doc.comment(1, "The services Feat starts, stops, and destroys for one task.")
	doc.list(1, "services", d.Runtime.Services)
}

func (d Draft) renderChecks(doc *document) {
	if len(d.Checks) == 0 {
		return
	}
	doc.blank()
	doc.comment(0,
		"Verification commands, per repository. A task that requests review runs",
		"them, and a check that fails returns to the agent's own loop.")
	doc.key(0, "checks")

	for _, repository := range d.checkedRepositories() {
		doc.key(1, repository)
		for _, check := range d.Checks {
			if check.Repository != repository {
				continue
			}
			doc.item(2, "id", check.ID)
			doc.list(3, "command", check.Command)
			doc.comment(3, "\"agent\" runs it in the agent's environment; \"host\" runs it on this machine.")
			doc.field(3, "execution", check.Execution)
		}
	}
}

// checkedRepositories returns the repositories that have checks, in the order
// the checks were answered, so that the rendering groups them without
// reordering what the user said.
func (d Draft) checkedRepositories() []string {
	var order []string
	seen := make(map[string]bool, len(d.Checks))
	for _, check := range d.Checks {
		if seen[check.Repository] {
			continue
		}
		seen[check.Repository] = true
		order = append(order, check.Repository)
	}
	return order
}

// orDisabled fills in the capability level for a CLI nobody chose. It is
// written down rather than left out, because the two provider CLIs are the
// capabilities that do vary, and a file that names one and not the other reads
// as though the other were undecided.
func orDisabled(level string) string {
	if level == "" {
		return CLIDisabled
	}
	return level
}

// document builds a YAML document as text.
//
// The alternative is to marshal a Config, and it was not taken: a marshalled
// struct carries every zero value the type has and no comments at all, and the
// comments are most of what makes a generated file editable. Every scalar still
// goes through the YAML encoder, so quoting is decided by the same library that
// parses the result, and Draft.Config parses what this produces before anybody
// is offered it.
type document struct {
	b strings.Builder
}

// nesting is one level of YAML indentation.
const nesting = "  "

// blank ends a section.
func (d *document) blank() { d.b.WriteString("\n") }

// comment writes one or more comment lines at a level of indentation.
func (d *document) comment(level int, lines ...string) {
	for _, line := range lines {
		if line == "" {
			d.b.WriteString(strings.Repeat(nesting, level) + "#\n")
			continue
		}
		d.b.WriteString(strings.Repeat(nesting, level) + "# " + line + "\n")
	}
}

// key writes a mapping key with nothing after it.
func (d *document) key(level int, name string) {
	d.b.WriteString(strings.Repeat(nesting, level) + name + ":\n")
}

// field writes one scalar field.
func (d *document) field(level int, name string, value any) {
	d.b.WriteString(strings.Repeat(nesting, level) + name + ": " + scalar(value) + "\n")
}

// list writes a sequence field, or nothing at all when the sequence is empty:
// an empty list in a generated file is a field the user has to work out the
// meaning of before they can delete it.
func (d *document) list(level int, name string, values []string) {
	if len(values) == 0 {
		return
	}
	d.key(level, name)
	for _, value := range values {
		d.b.WriteString(strings.Repeat(nesting, level+1) + "- " + scalar(value) + "\n")
	}
}

// item writes the first field of a sequence element, which is the one that
// carries the dash.
func (d *document) item(level int, name string, value any) {
	d.b.WriteString(strings.Repeat(nesting, level) + "- " + name + ": " + scalar(value) + "\n")
}

// bytes returns the document.
func (d *document) bytes() []byte { return []byte(d.b.String()) }

// scalar renders one value as a YAML scalar.
//
// The encoder decides whether a value needs quoting, so a repository directory
// called "no", a branch called "0755", and a path with a "#" in it survive
// being written down. A value the encoder renders across several lines, or
// cannot render at all, is written as a double-quoted Go string instead: it
// keeps the field on one line, and it is close enough to YAML's own quoting
// that the parse Draft.Config runs is what judges it.
func scalar(value any) string {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%q", value)
	}
	rendered := strings.TrimRight(string(encoded), "\n")
	if strings.Contains(rendered, "\n") {
		return fmt.Sprintf("%q", value)
	}
	return rendered
}
