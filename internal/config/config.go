package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is the configuration schema this build understands.
//
// It is the version a file declares in its `version` field. A file that
// declares another version is rejected rather than interpreted, because a
// configuration Feat half-understands would produce resources the user did not
// ask for.
const SchemaVersion = 1

// Config is one project's configuration, as authored in YAML.
//
// The Go type is the file format: the JSON Schema in schema/ describes these
// fields, and a test keeps the two in step. That is deliberate, and it is the
// opposite of the choice made for stored documents in internal/store/fs, which
// are a separate representation from the domain. Here the file is the only
// representation there is, so a second copy would be a second thing to keep
// correct rather than a boundary worth having.
//
// A parsed Config is not usable until Resolve has expanded its paths and filled
// its defaults, and Validate has accepted it. Load does all three.
type Config struct {
	// Version is the configuration schema version. It must be SchemaVersion.
	Version int `yaml:"version"`
	// Project identifies the project.
	Project ProjectSection `yaml:"project"`
	// Repositories are the Git repositories participating in the project,
	// keyed by their project-local identifier.
	Repositories map[string]Repository `yaml:"repositories"`
	// Git configures base resolution and the names of generated branches and
	// worktrees.
	Git GitSection `yaml:"git"`
	// Agent configures the coding agent and where it runs.
	Agent AgentSection `yaml:"agent"`
	// Runtime configures the application services a task may run. It is absent
	// for a project with no application runtime.
	Runtime *RuntimeSection `yaml:"runtime"`
	// Review configures the external commands review opens.
	Review ReviewSection `yaml:"review"`
	// Checks are per-repository verification commands, keyed by repository
	// identifier.
	Checks map[string][]Check `yaml:"checks"`
	// Notifications configures attention notifications.
	Notifications NotificationsSection `yaml:"notifications"`
	// Resources configures resource sampling.
	Resources ResourcesSection `yaml:"resources"`

	// path is the file the configuration was read from. It is not a field of
	// the file itself.
	path string
	// source is the file's bytes, kept so that a validation problem can be
	// shown in place.
	source []byte
	// resolved records that Resolve has run, so that a caller cannot validate
	// or use unexpanded paths by mistake.
	resolved bool
}

// ProjectSection identifies the project.
type ProjectSection struct {
	// ID identifies the project locally and must match the file name.
	ID string `yaml:"id"`
	// Name is the display name. It defaults to the identifier.
	Name string `yaml:"name"`
	// PrimaryRepository is the editable repository used as the default task
	// working directory (FR-PROJ-003).
	PrimaryRepository string `yaml:"primary_repository"`
}

// Repository is one Git repository participating in the project.
type Repository struct {
	// Name is the display name. It defaults to the repository identifier.
	Name string `yaml:"name"`
	// HostPath is the ordinary checkout on the host. A leading "~" is expanded.
	HostPath string `yaml:"host_path"`
	// Agent is where the agent's execution environment puts this repository.
	Agent RepositoryAgent `yaml:"agent"`
	// Runtime is what this repository contributes to the application runtime.
	// It is absent for a repository whose code no service runs.
	Runtime *RepositoryRuntime `yaml:"runtime"`
	// DefaultBranch is the branch a base policy resolves against.
	DefaultBranch string `yaml:"default_branch"`
	// Remote is the Git remote to fetch before resolving a base.
	Remote string `yaml:"remote"`
	// DefaultAccess is the repository's default participation in a task. It is
	// required rather than defaulted: whether an agent may write to a
	// repository is not a decision to make on the user's behalf.
	DefaultAccess string `yaml:"default_access"`
}

// RepositoryAgent is where the agent's own execution environment puts one
// repository.
//
// It is separate from RepositoryRuntime because the two answer different
// questions with different owners: where the agent's devcontainer mounts a
// worktree is the user's free choice, and where an application's own services
// expect their source is a fact about that application's Compose files. One
// field could not say both (ADR-065 evidence 5).
type RepositoryAgent struct {
	// ContainerPath is where task worktrees are mounted in the devcontainer.
	ContainerPath string `yaml:"container_path"`
}

// RepositoryRuntime is what one repository contributes to the project's
// application runtime.
//
// A runtime is composed of its repositories: each brings its own Compose files,
// resolved against its own checkout, and Feat generates the `include` document
// that joins them. Listing two repositories' files together instead would
// resolve every relative path against the first one's directory, so the second
// repository's build contexts and bind sources would point into the first
// (ADR-065 evidence 2).
type RepositoryRuntime struct {
	// ComposeFiles are the Compose files this repository brings. A relative path
	// resolves against the repository's own checkout, which is also the project
	// directory of its include entry, so nothing relative ever crosses a
	// repository boundary.
	ComposeFiles []string `yaml:"compose_files"`
	// ContainerPath is where this repository's own services expect their source.
	// It is what a task worktree is mounted at, and it has nothing to do with
	// where the agent's container mounts the same repository.
	ContainerPath string `yaml:"container_path"`
	// Services are the services this repository asks Feat to manage.
	Services []string `yaml:"services"`
	// Reachable are the services of this repository a user reaches from the
	// host. Feat allocates a published port for each of them.
	Reachable []string `yaml:"reachable"`
}

// GitSection configures base resolution and generated names.
type GitSection struct {
	// BasePolicy is how a task's base commit is resolved: remote, local,
	// current, or explicit.
	BasePolicy string `yaml:"base_policy"`
	// FetchBeforeTask reports whether Feat fetches configured remotes before
	// resolving bases. Unset means true.
	FetchBeforeTask *bool `yaml:"fetch_before_task"`
	// BranchTemplate generates task branch names.
	BranchTemplate string `yaml:"branch_template"`
	// WorktreeRoot generates the directory holding a task's worktrees.
	WorktreeRoot string `yaml:"worktree_root"`
}

// FetchesBeforeTask reports whether remotes are fetched before base
// resolution. The default is true, which is what FR-GIT-001 asks for; fetching
// never mutates the ordinary checkout.
func (g GitSection) FetchesBeforeTask() bool {
	return g.FetchBeforeTask == nil || *g.FetchBeforeTask
}

// AgentSection configures the coding agent.
type AgentSection struct {
	// Provider is the agent adapter. Claude Code is the only v0 provider.
	Provider string `yaml:"provider"`
	// Execution says where the agent runs.
	Execution ExecutionSection `yaml:"execution"`
	// Claude holds provider-specific settings.
	Claude ClaudeSection `yaml:"claude"`
	// Capabilities declare what the agent environment is allowed to reach.
	Capabilities CapabilitiesSection `yaml:"capabilities"`
}

// ExecutionSection configures the agent's execution environment.
type ExecutionSection struct {
	// Mode is host or devcontainer. It is required: it decides whether there
	// is a container boundary around the agent at all.
	Mode string `yaml:"mode"`
	// ComposeFiles are the base Compose files defining the devcontainer.
	ComposeFiles []string `yaml:"compose_files"`
	// Service is the Compose service the agent runs in.
	Service string `yaml:"service"`
	// User is the container user the agent runs as. It must not be root.
	User string `yaml:"user"`
	// WorkingDirectory is the agent's working directory inside the execution
	// environment.
	WorkingDirectory string `yaml:"working_directory"`
	// ControlPath is where the task control workspace is mounted inside the
	// execution environment.
	ControlPath string `yaml:"control_path"`
}

// Devcontainer reports whether the agent runs inside a container.
func (e ExecutionSection) Devcontainer() bool { return e.Mode == ModeDevcontainer }

// ClaudeSection holds Claude Code settings.
type ClaudeSection struct {
	// ConfigVolume is the dedicated Claude configuration volume, which keeps
	// one interactive login out of the user's own ~/.claude.
	//
	// It is optional. Without it Feat mounts nothing and sets no
	// CLAUDE_CONFIG_DIR, leaving the provider's configuration to whatever the
	// project's own Compose files supply — which a project that deliberately
	// mounts the user's ~/.claude is entitled to do (ADR-033).
	ConfigVolume string `yaml:"config_volume"`
	// ConfigPath is where ConfigVolume is mounted in the devcontainer, and the
	// value of CLAUDE_CONFIG_DIR. It means nothing without a volume.
	ConfigPath string `yaml:"config_path"`
	// IdleGracePeriod is how long an end-of-turn signal waits before the task
	// is reported idle. Idle never means complete.
	IdleGracePeriod string `yaml:"idle_grace_period"`

	idleGracePeriod time.Duration
}

// IdleGrace returns the parsed idle grace period.
func (c ClaudeSection) IdleGrace() time.Duration { return c.idleGracePeriod }

// CapabilitiesSection declares what the agent environment may reach.
//
// The capabilities are independent of one another on purpose: full Git access
// and an authenticated provider CLI say nothing about Docker, and Feat must not
// let one imply another (docs/05-security-model.md).
type CapabilitiesSection struct {
	// Docker is the agent's Docker access. Only "denied" is accepted: Feat has
	// no mechanism that grants an agent Docker, so any other value would be a
	// promise the binary does not keep.
	Docker string `yaml:"docker"`
	// Network is the agent's outbound network access. Only "unrestricted" is
	// accepted, because Feat implements no network restriction and does not
	// claim data-loss prevention.
	Network string `yaml:"network"`
	// Git is the agent's Git access. Only "full" is accepted for the same
	// reason.
	Git string `yaml:"git"`
	// GitHubCLI is whether `gh` is disabled, optional, or required.
	GitHubCLI string `yaml:"github_cli"`
	// GitLabCLI is whether `glab` is disabled, optional, or required.
	GitLabCLI string `yaml:"gitlab_cli"`
}

// RuntimeSection configures the application services of a task.
//
// What the application is made of is not here: the Compose files and the
// managed services belong to the repositories that bring them
// (RepositoryRuntime). What is left is the whole runtime's own settings — how
// it is driven, when it starts, what is layered over it, and what its Compose
// project is called.
type RuntimeSection struct {
	// Provider is the runtime adapter. Compose is the only v0 provider.
	Provider string `yaml:"provider"`
	// StartPolicy is when services start. Only "manual" exists in v0:
	// application services start by explicit user action (FR-RUN-005).
	StartPolicy string `yaml:"start_policy"`
	// StaticOverrides are user-authored override files applied after the
	// generated include and before Feat's generated task override. A relative
	// path inside one resolves against Feat's own generated directory, so they
	// are best written absolute.
	StaticOverrides []string `yaml:"static_overrides"`
	// EnvFiles are host-side environment files passed to Compose by path. Feat
	// records the paths and never reads their contents.
	EnvFiles []string `yaml:"env_files"`
	// ProjectNameTemplate generates the Compose project name, which is what
	// makes a runtime action affect one task and no other.
	ProjectNameTemplate string `yaml:"project_name_template"`
	// PortRange is the host ports Feat may publish a task's reachable services
	// on, written "<first>-<last>". Feat allocates one port per reachable
	// service per task from it and releases them when the runtime is destroyed.
	PortRange string `yaml:"port_range"`
	// BindAddress is the host address Feat publishes an allocated port on when
	// the project's own Compose files named none.
	//
	// It defaults to the loopback address, so a task's services answer on the
	// machine running them and nowhere else. The alternative is not a smaller
	// exposure written differently: a port bound to every interface is reachable
	// from every other machine on whatever network this one is on, and from every
	// container on this one — which is one task's agent able to dial another
	// task's database. Widening it is a decision a user makes, for the case that
	// wants it, such as reaching a dev server from a phone on the same network.
	//
	// An address the project's own file named is kept as it is: this is the
	// default for a publication that named none, not an address applied over one.
	BindAddress string `yaml:"bind_address"`

	portRange PortRange
}

// Ports returns the parsed host port range.
func (r RuntimeSection) Ports() PortRange { return r.portRange }

// PortRange is the span of host ports a project's tasks may be published on.
//
// It is a range rather than a single port because a published port is global to
// the machine: the whole point is that a second task gets a different one, and
// what bounds the choice has to be the user's, since these are ports on their
// own machine.
type PortRange struct {
	// First and Last are inclusive.
	First int
	Last  int
}

// Empty reports whether the range holds no port at all.
func (p PortRange) Empty() bool { return p.First == 0 || p.Last < p.First }

// Size is how many ports the range holds.
func (p PortRange) Size() int {
	if p.Empty() {
		return 0
	}
	return p.Last - p.First + 1
}

// String renders the range the way it is written in configuration.
func (p PortRange) String() string { return strconv.Itoa(p.First) + "-" + strconv.Itoa(p.Last) }

// Contains reports whether a port lies in the range.
func (p PortRange) Contains(port int) bool { return !p.Empty() && port >= p.First && port <= p.Last }

// ParsePortRange reads the "<first>-<last>" form.
//
// Both ends are required. A single number would be a range of one, which is a
// project that can run one task's application and is far more likely to be a
// typing mistake than a decision.
func ParsePortRange(value string) (PortRange, error) {
	first, last, found := strings.Cut(strings.TrimSpace(value), "-")
	if !found {
		return PortRange{}, fmt.Errorf("must be written %q, but is %q", "<first>-<last>", value)
	}
	from, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return PortRange{}, fmt.Errorf("must begin with a port number, but begins with %q", first)
	}
	to, err := strconv.Atoi(strings.TrimSpace(last))
	if err != nil {
		return PortRange{}, fmt.Errorf("must end with a port number, but ends with %q", last)
	}
	return PortRange{First: from, Last: to}, nil
}

// ReviewSection configures the external commands review opens.
type ReviewSection struct {
	// Diff opens the change of one repository against its recorded base.
	Diff Command `yaml:"diff"`
	// Editor opens one repository for editing.
	Editor Command `yaml:"editor"`
	// Status shows the Git status of one repository.
	Status Command `yaml:"status"`
}

// Command is an external command, held as an argument vector rather than a
// string so that nothing is ever handed to a shell to re-split (CLAUDE.md
// architectural rules).
type Command struct {
	// Command is the executable followed by its arguments. Arguments may
	// contain placeholders, which are expanded into single vector elements.
	Command []string `yaml:"command"`
}

// Empty reports whether no command is configured.
func (c Command) Empty() bool { return len(c.Command) == 0 }

// Check is one verification command for one repository.
type Check struct {
	// ID identifies the check within its repository.
	ID string `yaml:"id"`
	// Command is the check's argument vector.
	Command []string `yaml:"command"`
	// Execution is where the check runs: agent or host.
	Execution string `yaml:"execution"`
}

// NotificationsSection configures attention notifications.
type NotificationsSection struct {
	// Desktop enables desktop notifications. Unset means true.
	Desktop *bool `yaml:"desktop"`
	// IdleGracePeriod is how long idle waits before it is worth reporting.
	IdleGracePeriod string `yaml:"idle_grace_period"`
	// SuppressWhileAttached suppresses notifications for a task the user is
	// already looking at. Unset means true.
	SuppressWhileAttached *bool `yaml:"suppress_while_attached"`

	idleGracePeriod time.Duration
}

// DesktopEnabled reports whether desktop notifications are enabled.
func (n NotificationsSection) DesktopEnabled() bool { return n.Desktop == nil || *n.Desktop }

// SuppressedWhileAttached reports whether notifications are suppressed while
// the user is attached to the task (FR-AGENT-007).
func (n NotificationsSection) SuppressedWhileAttached() bool {
	return n.SuppressWhileAttached == nil || *n.SuppressWhileAttached
}

// IdleGrace returns the parsed idle grace period.
func (n NotificationsSection) IdleGrace() time.Duration { return n.idleGracePeriod }

// ResourcesSection configures resource sampling.
type ResourcesSection struct {
	// SampleInterval is how often whole-machine and per-task resources are
	// sampled. Sampling is observational and never blocks task creation.
	SampleInterval string `yaml:"sample_interval"`

	sampleInterval time.Duration
}

// Sample returns the parsed sample interval.
func (r ResourcesSection) Sample() time.Duration { return r.sampleInterval }

// Path returns the file the configuration was read from. It is empty for
// configuration parsed from memory.
func (c *Config) Path() string { return c.path }

// ID returns the project identifier.
func (c *Config) ID() string { return c.Project.ID }

// RepositoryIDs returns the repository identifiers in a stable order.
//
// YAML mappings have no order a Go map preserves, so Feat imposes one rather
// than letting the order of a printed table depend on a hash seed.
func (c *Config) RepositoryIDs() []string {
	ids := make([]string, 0, len(c.Repositories))
	for id := range c.Repositories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Repository returns one repository by identifier.
func (c *Config) Repository(id string) (Repository, bool) {
	repository, ok := c.Repositories[id]
	return repository, ok
}

// Primary returns the primary repository.
func (c *Config) Primary() (Repository, bool) { return c.Repository(c.Project.PrimaryRepository) }

// HasRuntime reports whether the project configures an application runtime.
func (c *Config) HasRuntime() bool { return c.Runtime != nil }

// RuntimeContribution is one repository's part of the application runtime,
// already joined with the repository facts a caller would otherwise look up.
//
// It exists so that everything composing a runtime reads the same list in the
// same order — the daemon generating the include document, `feat doctor`
// checking the files, and `feat project show` printing them — rather than each
// walking the repositories with its own idea of which ones count.
type RuntimeContribution struct {
	// RepositoryID identifies the repository within the project.
	RepositoryID string
	// Directory is the repository's ordinary checkout, which is the project
	// directory of this contribution's include entry: paths inside its Compose
	// files resolve against it exactly as they do when the user runs Compose
	// there by hand.
	Directory string
	// ComposeFiles are the repository's own Compose files, absolute.
	ComposeFiles []string
	// ContainerPath is where this repository's services expect their source.
	ContainerPath string
	// Services are the services this repository asks Feat to manage.
	Services []string
	// Reachable are the services of this repository a user reaches from the
	// host.
	Reachable []string
}

// RuntimeComposition returns the repositories contributing to the application
// runtime, in repository order.
//
// A project with no runtime section contributes nothing, whatever its
// repositories say: a contribution to a runtime that does not exist is a
// configuration error rather than something to act on, and validation reports
// it as one.
func (c *Config) RuntimeComposition() []RuntimeContribution {
	if c.Runtime == nil {
		return nil
	}
	var composition []RuntimeContribution
	for _, id := range c.RepositoryIDs() {
		repository := c.Repositories[id]
		if repository.Runtime == nil {
			continue
		}
		composition = append(composition, RuntimeContribution{
			RepositoryID:  id,
			Directory:     repository.HostPath,
			ComposeFiles:  append([]string(nil), repository.Runtime.ComposeFiles...),
			ContainerPath: repository.Runtime.ContainerPath,
			Services:      append([]string(nil), repository.Runtime.Services...),
			Reachable:     append([]string(nil), repository.Runtime.Reachable...),
		})
	}
	return composition
}

// RuntimeServices returns every service the project asks Feat to manage, in
// repository order and without repetition.
//
// It is the list a create and a start target, and it is not the whole of what
// runs: Compose starts whatever those services depend on, and everything it
// starts belongs to the task's own Compose project (ADR-034).
//
// A service two repositories both name appears once. Naming it twice is not a
// mistake — a service that runs an application and a shared library it depends
// on runs the code of two repositories — but Compose is asked for it once.
func (c *Config) RuntimeServices() []string {
	var services []string
	seen := make(map[string]bool)
	for _, contribution := range c.RuntimeComposition() {
		for _, service := range contribution.Services {
			if seen[service] {
				continue
			}
			seen[service] = true
			services = append(services, service)
		}
	}
	return services
}

// RuntimeReachable returns every service a repository declares reachable, in
// repository order and without repetition.
//
// It is the list Feat allocates a host port for. A service two repositories
// both declare is reached at one address, because it is one service.
func (c *Config) RuntimeReachable() []string {
	var services []string
	seen := make(map[string]bool)
	for _, contribution := range c.RuntimeComposition() {
		for _, service := range contribution.Reachable {
			if seen[service] {
				continue
			}
			seen[service] = true
			services = append(services, service)
		}
	}
	return services
}

// Accepted values. They are the vocabularies docs/07-configuration-model.md
// documents, and validation names the accepted values when it rejects one, so
// a user never has to find this list in source.
const (
	// PolicyRemote resolves the base from the configured remote-tracking
	// branch after a fetch. It is the recommended default.
	PolicyRemote = "remote"
	// PolicyLocal resolves the base from the local default branch.
	PolicyLocal = "local"
	// PolicyCurrent resolves the base from the ordinary checkout's current
	// commit.
	PolicyCurrent = "current"
	// PolicyExplicit takes a ref supplied during task preparation.
	PolicyExplicit = "explicit"

	// ModeHost runs the agent directly in the primary task worktree.
	ModeHost = "host"
	// ModeDevcontainer runs the agent inside a configured Compose service.
	ModeDevcontainer = "devcontainer"

	// EnvHostAgent opts a daemon in to launching agents on this host even for a
	// project that configures a container.
	//
	// It is deliberately an environment variable of the daemon rather than a
	// flag on a request or a field in project configuration: a request that
	// could move an agent outside its configured boundary would be a caller
	// granting itself a capability (ADR-032).
	//
	// It is named here, beside the modes it overrides, rather than in the daemon
	// that reads it, because the two commands that print a project's execution
	// mode — `feat doctor` and `feat project show` — run without a daemon and
	// cannot see its environment. What they can do is name the variable that
	// decides whether the mode they printed is the one in force.
	EnvHostAgent = "FEAT_HOST_AGENT"

	// ProviderClaude is the Claude Code adapter.
	ProviderClaude = "claude"
	// ProviderCompose is the Docker Compose runtime adapter.
	ProviderCompose = "compose"

	// StartManual starts application services only by explicit user action.
	StartManual = "manual"

	// CapabilityDenied is the only accepted Docker capability.
	CapabilityDenied = "denied"
	// CapabilityUnrestricted is the only accepted network capability.
	CapabilityUnrestricted = "unrestricted"
	// CapabilityFull is the only accepted Git capability.
	CapabilityFull = "full"

	// CLIDisabled means Feat neither expects nor validates the provider CLI.
	CLIDisabled = "disabled"
	// CLIOptional means doctor reports availability but does not fail.
	CLIOptional = "optional"
	// CLIRequired means task launch fails when the CLI is absent or
	// unauthenticated.
	CLIRequired = "required"

	// ExecutionAgent runs a check inside the agent's execution environment.
	ExecutionAgent = "agent"
	// ExecutionHost runs a check on the trusted host.
	ExecutionHost = "host"
)
