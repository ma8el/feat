package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
)

// loadReplacing edits the complete fixture and loads the result.
func loadReplacing(t *testing.T, old, replacement string) (*config.Config, error) {
	t.Helper()
	base := fixture(t, "app.yaml")
	body := strings.Replace(base, old, replacement, 1)
	if body == base && old != "" {
		t.Fatalf("the fixture no longer contains %q", old)
	}
	dir := write(t, "app.yaml", body)
	opts, _ := testOptions(t, nil)
	return config.Load(dir, "app", opts)
}

// TestValidationRejectsUnsafeConfiguration checks the rules that keep a
// configuration from producing resources the user did not ask for.
//
// Each case names the configuration path the problem must be reported against,
// because a rejection that does not say which value is wrong leaves the user to
// bisect their own file.
func TestValidationRejectsUnsafeConfiguration(t *testing.T) {
	for name, testCase := range map[string]struct {
		old, new string
		path     string
		contains string
	}{
		"unknown schema version": {
			old: "version: 1", new: "version: 2",
			path: "version", contains: "schema version 1",
		},
		"project id is unsafe": {
			old: "  id: app", new: "  id: ../escape",
			path: "project.id", contains: "lowercase",
		},
		"primary repository is not a repository": {
			old: "  primary_repository: api", new: "  primary_repository: missing",
			path: "project.primary_repository", contains: "not one of this project's repositories",
		},
		"primary repository cannot be edited": {
			old: "  primary_repository: api", new: "  primary_repository: infra",
			path: "project.primary_repository", contains: "must be one a task can edit",
		},
		"access mode is missing": {
			old: "    default_access: read_write\n", new: "",
			path: "repositories.api.default_access", contains: "how the repository takes part",
		},
		"access mode is not a mode": {
			old: "    default_access: read_write", new: "    default_access: writable",
			path: "repositories.api.default_access", contains: "not an access mode",
		},
		"container path is relative": {
			old: "      container_path: /srv/api", new: "      container_path: srv/api",
			path:     "repositories.api.agent.container_path",
			contains: "absolute path inside the execution environment",
		},
		"container paths overlap": {
			old: "      container_path: /srv/web", new: "      container_path: /srv/api/inner",
			path: "repositories.web.agent.container_path", contains: "cannot be mounted inside one another",
		},
		"base policy is unknown": {
			old: "  base_policy: remote", new: "  base_policy: whatever",
			path: "git.base_policy", contains: "not a base policy",
		},
		"branch template uses an unknown placeholder": {
			old: `  branch_template: "feat/{task_key}-{slug}"`, new: `  branch_template: "feat/{ticket}-{task_key}"`,
			path: "git.branch_template", contains: "does not expand",
		},
		"branch template is not task scoped": {
			old: `  branch_template: "feat/{task_key}-{slug}"`, new: `  branch_template: "feat/{project_id}"`,
			path: "git.branch_template", contains: "two tasks share it",
		},
		"branch template expands to an invalid ref": {
			old: `  branch_template: "feat/{task_key}-{slug}"`, new: `  branch_template: "feat/../{task_key}"`,
			path: "git.branch_template", contains: "Git rejects",
		},
		"branch template has a stray brace": {
			old: `  branch_template: "feat/{task_key}-{slug}"`, new: `  branch_template: "feat/{task_key-{slug}"`,
			path: "git.branch_template", contains: "unmatched",
		},
		"agent provider is unsupported": {
			old: "  provider: claude", new: "  provider: codex",
			path: "agent.provider", contains: "only agent this version supports",
		},
		"execution user is root": {
			old: "    user: developer", new: "    user: root",
			path: "agent.execution.user", contains: "non-root",
		},
		"execution user is uid zero": {
			old: "    user: developer", new: `    user: "0:0"`,
			path: "agent.execution.user", contains: "non-root",
		},
		"working directory is not mounted": {
			old: "    working_directory: /srv/api", new: "    working_directory: /elsewhere",
			path: "agent.execution.working_directory", contains: "not inside any repository",
		},
		"control path shadows a repository": {
			old: "    control_path: /feat", new: "    control_path: /srv/api/control",
			path: "agent.execution.control_path", contains: "must be separate from every repository",
		},
		"claude config path shadows a repository": {
			old:  "    config_volume: example-claude-config",
			new:  "    config_volume: example-claude-config\n    config_path: /srv/api/.claude",
			path: "agent.claude.config_path", contains: "must not be mounted inside a repository",
		},
		"claude config path shadows the control workspace": {
			old:  "    config_volume: example-claude-config",
			new:  "    config_volume: example-claude-config\n    config_path: /feat/claude",
			path: "agent.claude.config_path", contains: "overlaps the control workspace",
		},
		"claude config path has no volume to mount": {
			old: "    config_volume: example-claude-config", new: "    config_path: /feat-claude",
			path: "agent.claude.config_path", contains: "names no volume to mount there",
		},
		"docker capability is granted": {
			old: "    docker: denied", new: "    docker: allowed",
			path: "agent.capabilities.docker", contains: "never receives a Docker socket",
		},
		"network capability is restricted": {
			old: "    network: unrestricted", new: "    network: allowlist",
			path: "agent.capabilities.network", contains: "does not implement network restriction",
		},
		"provider cli level is unknown": {
			old: "    gitlab_cli: required", new: "    gitlab_cli: maybe",
			path: "agent.capabilities.gitlab_cli", contains: "not a capability level",
		},
		"runtime start policy is automatic": {
			old: "  start_policy: manual", new: "  start_policy: automatic",
			path: "runtime.start_policy", contains: "explicit user action",
		},
		"runtime project name is not task scoped": {
			old: `  project_name_template: "feat-{project_id}-{task_id}"`, new: `  project_name_template: "feat-{project_id}"`,
			path: "runtime.project_name_template", contains: "two tasks share it",
		},
		"runtime project name is invalid for compose": {
			old: `  project_name_template: "feat-{project_id}-{task_id}"`, new: `  project_name_template: "Feat/{task_id}"`,
			path: "runtime.project_name_template", contains: "Docker Compose rejects",
		},
		"review command uses an unknown placeholder": {
			old: `    command: ["git", "diff", "{base_commit}"]`, new: `    command: ["git", "diff", "{base}"]`,
			path: "review.diff.command[2]", contains: "does not expand",
		},
		"review program is a placeholder": {
			old: `    command: ["nvim", "{repository_path}"]`, new: `    command: ["{editor}", "{repository_path}"]`,
			path: "review.editor.command", contains: "program to run is fixed",
		},
		"check names an unknown repository": {
			old: "checks:\n  api:", new: "checks:\n  absent:",
			path: "checks.absent", contains: "not one of this project's repositories",
		},
		"check execution is unknown": {
			old: "      execution: agent", new: "      execution: somewhere",
			path: "checks.api[0].execution", contains: "where the check runs",
		},
		"config volume is not a volume name": {
			old: "    config_volume: example-claude-config", new: "    config_volume: not/a/volume",
			path: "agent.claude.config_volume", contains: "volume name",
		},
		"forge is not one Feat publishes to": {
			old: "      kind: gitlab", new: "      kind: bitbucket",
			path: "repositories.api.forge.kind", contains: "not a forge Feat publishes to",
		},
		"forge names no kind": {
			old: "      kind: gitlab", new: "      kind: \"\"",
			path: "repositories.api.forge.kind", contains: "which forge this repository publishes to",
		},
		"tracker kind is unknown": {
			old: "  kind: command", new: "  kind: shortcut",
			path: "tracker.kind", contains: "running a command that prints them",
		},
		"tracker command is empty": {
			old: `  command: ["tickets-for-me"]`, new: "  command: []",
			path: "tracker.command", contains: "argument vector",
		},
		"tracker program is a placeholder": {
			old: `  command: ["tickets-for-me"]`, new: `  command: ["{tracker}"]`,
			path: "tracker.command", contains: "program to run is fixed",
		},
		"tracker command uses a placeholder": {
			old: `  command: ["tickets-for-me"]`, new: `  command: ["tickets-for-me", "{task_id}"]`,
			path: "tracker.command[1]", contains: "does not expand",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadReplacing(t, testCase.old, testCase.new)
			invalid := configError(t, err)

			problem := problemAt(t, invalid, testCase.path)
			if !strings.Contains(problem.Reason, testCase.contains) {
				t.Errorf("problem at %s is %q, which does not explain %q",
					testCase.path, problem.Reason, testCase.contains)
			}
		})
	}
}

// TestTheTrackerAndTheForgeAreEachOptional checks that a project may configure
// either, both, or neither.
//
// They answer different questions with different owners — where the code goes
// and where the tickets come from — and a forge hosts its own issues only
// sometimes, so neither may imply the other (ADR-071).
func TestTheTrackerAndTheForgeAreEachOptional(t *testing.T) {
	const tracker = `tracker:
  kind: command
  # Whatever prints this project's tickets as JSON in the shape of
  # schema/feat-tickets.schema.json. Feat passes no filter of its own: which
  # tickets are the user's is the command's decision.
  command: ["tickets-for-me"]

`
	const forge = `    forge:
      kind: gitlab
`

	for name, removed := range map[string]string{
		"without a tracker": tracker,
		"without a forge":   forge,
	} {
		t.Run(name, func(t *testing.T) {
			loaded, err := loadReplacing(t, removed, "")
			if err != nil {
				t.Fatalf("a project configured %s was rejected:\n%v", name, err)
			}
			if removed == tracker && loaded.Tracker != nil {
				t.Error("the tracker section was removed and the configuration still has one")
			}
			if removed == forge {
				api, _ := loaded.Repository("api")
				if api.Forge != nil {
					t.Error("the forge was removed and the repository still has one")
				}
			}
		})
	}
}

// TestTheTrackerKindHasADefault checks that a project need not write the one
// value the field can hold.
//
// It is filled in rather than left empty for the reason every other default is:
// `feat project show` prints what Feat will act on, and a default a user cannot
// see is one they cannot check.
func TestTheTrackerKindHasADefault(t *testing.T) {
	loaded, err := loadReplacing(t, "  kind: command\n", "")
	if err != nil {
		t.Fatalf("loading a tracker with no kind: %v", err)
	}
	if loaded.Tracker == nil {
		t.Fatal("the configuration declares a tracker and none was loaded")
	}
	if loaded.Tracker.Kind != config.TrackerCommand {
		t.Errorf("tracker kind = %q, want the default %q", loaded.Tracker.Kind, config.TrackerCommand)
	}
}

// TestWorktreeRootMustBeADirectoryFeatOwns checks the rule that protects the
// only configured path Feat later deletes from.
func TestWorktreeRootMustBeADirectoryFeatOwns(t *testing.T) {
	const field = "git.worktree_root"
	const original = `  worktree_root: "~/.local/share/feat/worktrees/{project_id}/{task_id}"`

	for name, testCase := range map[string]struct {
		root     string
		contains string
	}{
		"filesystem root":         {root: "/", contains: "must contain"},
		"shared temp directory":   {root: "/tmp", contains: "must contain"},
		"one level below root":    {root: "/{task_id}", contains: "shared directory"},
		"system directory":        {root: "/var/{task_id}/work", contains: "shared directory"},
		"inside a checkout":       {root: "~/repos/app/api/{task_id}", contains: "overlaps the checkout"},
		"relative":                {root: "worktrees/{task_id}", contains: "absolute"},
		"no per-task placeholder": {root: "~/worktrees/{project_id}", contains: "two tasks share it"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadReplacing(t, original, `  worktree_root: "`+testCase.root+`"`)
			invalid := configError(t, err)

			problem := problemAt(t, invalid, field)
			if testCase.contains != "" && !strings.Contains(problem.Reason, testCase.contains) {
				t.Errorf("problem is %q, which does not explain %q", problem.Reason, testCase.contains)
			}
		})
	}
}

// TestHostExecutionRejectsDevcontainerFields checks that configuration which
// would be silently ignored is refused instead.
//
// A user who configured a service and a non-root user must not be left
// believing their agent runs in a container when the mode says it does not.
func TestHostExecutionRejectsDevcontainerFields(t *testing.T) {
	base := fixture(t, "app.yaml")
	body := strings.Replace(base, "    mode: devcontainer", "    mode: host", 1)
	dir := write(t, "app.yaml", body)
	opts, _ := testOptions(t, nil)

	_, err := config.Load(dir, "app", opts)
	invalid := configError(t, err)

	for _, path := range []string{
		"agent.execution.compose_files",
		"agent.execution.service",
		"agent.execution.user",
		"agent.execution.control_path",
		"agent.execution.working_directory",
	} {
		problem := problemAt(t, invalid, path)
		if !strings.Contains(problem.Reason, "host") {
			t.Errorf("problem at %s is %q, which does not mention the execution mode", path, problem.Reason)
		}
	}
}

// TestDevcontainerRequiresContainerPaths checks that a repository a task can
// select has somewhere to be mounted.
func TestDevcontainerRequiresContainerPaths(t *testing.T) {
	_, err := loadReplacing(t, "    agent:\n      container_path: /srv/web\n", "")
	invalid := configError(t, err)

	problem := problemAt(t, invalid, "repositories.web.agent.container_path")
	if !strings.Contains(problem.Reason, "devcontainer") {
		t.Errorf("problem is %q, which does not say why a container path is needed", problem.Reason)
	}
}

// TestARuntimeThatCouldMountNothingIsRefused is ADR-065 evidence 1, refused
// before anything can run.
//
// A project reached this twice: no repository said where its services expect
// their source, so the generated override carried no mounts at all, every
// service ran the user's ordinary checkout, and every record Feat kept about the
// task stayed correct. It needs no Docker to diagnose and no file to read, so it
// is refused where the message can still name the repository and the services it
// is about.
func TestARuntimeThatCouldMountNothingIsRefused(t *testing.T) {
	_, err := loadReplacing(t, "      container_path: /app\n", "")
	invalid := configError(t, err)

	problem := problemAt(t, invalid, "repositories.api.runtime.container_path")
	for _, expected := range []string{"app", "worker", "ordinary checkout"} {
		if !strings.Contains(problem.Reason, expected) {
			t.Errorf("problem is %q, which does not name %q", problem.Reason, expected)
		}
	}
}

// TestARepositoryThatBakesItsCodeNeedsNoContainerPath is the other half of the
// same rule, and the reason it is asked of the runtime rather than of each
// repository.
//
// A service whose image is built from its repository runs the task's worktree
// once Feat redirects its build context, and it may have no mount anywhere and
// want none: mounting a worktree into a multi-stage build that ends in a web
// server is meaningless at best. Configuration cannot see a build context, so a
// rule that required a container path of every contributing repository would
// refuse a project that is correct.
func TestARepositoryThatBakesItsCodeNeedsNoContainerPath(t *testing.T) {
	base := fixture(t, "app.yaml")
	body := strings.Replace(base, `  web:
    host_path: ~/repos/app/web
    agent:
      container_path: /srv/web
`, `  web:
    host_path: ~/repos/app/web
    agent:
      container_path: /srv/web
    runtime:
      compose_files:
        - docker-compose.yml
      services:
        - frontend
`, 1)
	if body == base {
		t.Fatal("the fixture no longer holds the repository this case adds a contribution to")
	}
	dir := write(t, "app.yaml", body)
	opts, _ := testOptions(t, nil)

	if _, err := config.Load(dir, "app", opts); err != nil {
		t.Errorf("a repository whose services bake their code was refused: %v", err)
	}
}

// TestThePortRangeIsCheckedBeforeAnythingAllocatesFromIt covers the range Feat
// publishes a task's reachable services on.
//
// Each of these produces the same failure at a create — a service that cannot be
// published — and each of them is visible in the file. A range the daemon cannot
// allocate from is worth refusing where the message can name the field.
func TestThePortRangeIsCheckedBeforeAnythingAllocatesFromIt(t *testing.T) {
	for name, testCase := range map[string]struct{ value, contains string }{
		"not a range":     {value: "21000", contains: "<first>-<last>"},
		"ends first":      {value: "21100-21000", contains: "ends before it begins"},
		"privileged":      {value: "80-1000", contains: "privilege"},
		"beyond the last": {value: "65000-70000", contains: "privilege"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadReplacing(t,
				`  project_name_template: "feat-{project_id}-{task_id}"`,
				"  project_name_template: \"feat-{project_id}-{task_id}\"\n  port_range: \""+
					testCase.value+"\"")
			invalid := configError(t, err)

			problem := problemAt(t, invalid, "runtime.port_range")
			if !strings.Contains(problem.Reason, testCase.contains) {
				t.Errorf("problem is %q, which does not say %q", problem.Reason, testCase.contains)
			}
		})
	}
}

// TestARangeTooNarrowForOneTaskIsRefused is the case a user cannot recover from
// by finishing with another task.
//
// A range holding fewer ports than the project has reachable services cannot
// publish a single task, so every create would report an exhausted range with
// nothing holding it. That is a fact about the file rather than about what is
// running, and it is refused where it can say so.
func TestARangeTooNarrowForOneTaskIsRefused(t *testing.T) {
	base := fixture(t, "app.yaml")
	body := strings.Replace(base, "      reachable:\n        - app\n",
		"      reachable:\n        - app\n        - worker\n", 1)
	body = strings.Replace(body, `  project_name_template: "feat-{project_id}-{task_id}"`,
		"  project_name_template: \"feat-{project_id}-{task_id}\"\n  port_range: \"21000-21000\"", 1)

	dir := write(t, "app.yaml", body)
	opts, _ := testOptions(t, nil)

	invalid := configError(t, mustFail(config.Load(dir, "app", opts)))
	problem := problemAt(t, invalid, "runtime.port_range")
	if !strings.Contains(problem.Reason, "one task alone would exhaust it") {
		t.Errorf("problem is %q, which does not say that one task could not be published", problem.Reason)
	}
}

// mustFail passes an error through, discarding the value that came with it.
func mustFail[T any](_ T, err error) error { return err }

// TestThePortRangeHasADefault keeps a project that never thought about ports
// running.
//
// The reachable declaration was collected before anything allocated from it, so
// a project written then names no range — and a range is a decision about this
// machine's own ports rather than about the project, which is exactly the kind
// of value that should have a default the user can see.
func TestThePortRangeHasADefault(t *testing.T) {
	cfg, err := loadReplacing(t, "", "")
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	ports := cfg.Runtime.Ports()
	if ports.Empty() || ports.Size() < 2 {
		t.Fatalf("the default port range is %v, which cannot publish two tasks", ports)
	}
	if printed := cfg.Runtime.PortRange; printed != ports.String() {
		t.Errorf("the resolved configuration prints %q and allocates from %v: a default a user "+
			"cannot see is a default they cannot check", printed, ports)
	}
}

// TestTheBindAddressDefaultsToThisMachineAlone is the default half of G4-01.
//
// Publishing is Feat's act here rather than the user's: the project wrote a
// container port, Feat chose the host port that replaces it, and nobody asked
// for the service to answer on whatever network the machine is joined to. A
// project that never thought about this gets the narrowest binding, and can read
// which one it got.
func TestTheBindAddressDefaultsToThisMachineAlone(t *testing.T) {
	cfg, err := loadReplacing(t, "", "")
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if address := cfg.Runtime.BindAddress; address != "127.0.0.1" {
		t.Errorf("the default bind address is %q, and a project that named none should be published "+
			"on this machine alone", address)
	}

	var printed bool
	for _, section := range cfg.Describe() {
		if section.Title != "runtime" {
			continue
		}
		for _, field := range section.Fields {
			if field.Name == "bind_address" {
				printed = true
			}
		}
	}
	if !printed {
		t.Error("`feat project show` does not print the bind address: it decides who can reach this " +
			"project's services, and a default a user cannot see is a default they cannot check")
	}
}

// TestABindAddressThatIsNotAnAddressIsRefused keeps a value Compose would take
// out of the generated override.
//
// It reaches the document as a host_ip and Compose binds it. A name would be
// resolved by Docker at a moment Feat cannot see, to an address Feat could not
// then tell the user their service was at — and one resolving to several would
// not be one binding at all.
func TestABindAddressThatIsNotAnAddressIsRefused(t *testing.T) {
	for name, value := range map[string]string{
		"a host name":           "localhost",
		"an address and a port": "127.0.0.1:8080",
		"a range":               "127.0.0.0/8",
		"nonsense":              "everywhere",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadReplacing(t,
				`  project_name_template: "feat-{project_id}-{task_id}"`,
				"  project_name_template: \"feat-{project_id}-{task_id}\"\n  bind_address: \""+value+"\"")
			invalid := configError(t, err)

			problem := problemAt(t, invalid, "runtime.bind_address")
			if !strings.Contains(problem.Reason, "literal IP address") {
				t.Errorf("problem is %q, which does not say what a bind address has to be", problem.Reason)
			}
		})
	}
}

// TestAProjectMayAskForEveryInterface keeps the widening available to the user
// who wants it.
//
// The default is narrow because nobody chose it, not because the wide answer is
// wrong: a dev server a phone on the same network should reach is a real case,
// and the point of the key is that a user can say so. What they may not do is
// get it without saying so.
func TestAProjectMayAskForEveryInterface(t *testing.T) {
	cfg, err := loadReplacing(t,
		`  project_name_template: "feat-{project_id}-{task_id}"`,
		"  project_name_template: \"feat-{project_id}-{task_id}\"\n  bind_address: \"0.0.0.0\"")
	if err != nil {
		t.Fatalf("a project that asked for every interface was refused: %v", err)
	}
	if address := cfg.Runtime.BindAddress; address != "0.0.0.0" {
		t.Errorf("the configured bind address is %q, and the project asked for 0.0.0.0", address)
	}
}

// TestTwoReachableServicesCannotShareAGeneratedVariable refuses the collision
// before it can deliver one service's address to another.
//
// Feat tells every managed service the host address of each reachable one, as
// FEAT_HOST_URL_<service> upper-cased with everything that is not a letter or a
// digit replaced. A Compose service name may contain dots and hyphens and an
// environment variable name may not, so the rendering is lossy — and two
// services that render alike would be one address arriving under both names.
func TestTwoReachableServicesCannotShareAGeneratedVariable(t *testing.T) {
	_, err := loadReplacing(t, "      services:\n        - app\n        - worker\n      reachable:\n        - app\n",
		"      services:\n        - app-1\n        - app.1\n      reachable:\n        - app-1\n        - app.1\n")
	invalid := configError(t, err)

	problem := problemAt(t, invalid, "repositories.api.runtime.reachable[1]")
	for _, expected := range []string{"app-1", "app.1", "FEAT_HOST_PORT_APP_1"} {
		if !strings.Contains(problem.Reason, expected) {
			t.Errorf("problem is %q, which does not name %q", problem.Reason, expected)
		}
	}
}

// TestEveryProblemIsReportedTogether checks that validation collects problems
// rather than stopping at the first.
//
// A configuration file is edited by hand. Finding four mistakes one round trip
// at a time is four times the work of seeing them together.
func TestEveryProblemIsReportedTogether(t *testing.T) {
	base := fixture(t, "app.yaml")
	body := base
	for _, replacement := range [][2]string{
		{"  base_policy: remote", "  base_policy: whatever"},
		{"    docker: denied", "    docker: allowed"},
		{"    user: developer", "    user: root"},
		{"  start_policy: manual", "  start_policy: automatic"},
	} {
		body = strings.Replace(body, replacement[0], replacement[1], 1)
	}
	dir := write(t, "app.yaml", body)
	opts, _ := testOptions(t, nil)

	_, err := config.Load(dir, "app", opts)
	invalid := configError(t, err)

	if len(invalid.Problems) != 4 {
		t.Errorf("reported %d problems, want 4:\n%s", len(invalid.Problems), invalid)
	}
	for _, path := range []string{
		"agent.capabilities.docker",
		"agent.execution.user",
		"git.base_policy",
		"runtime.start_policy",
	} {
		problemAt(t, invalid, path)
	}
}

// TestFileNameMustMatchProjectIdentifier keeps two answers to one question out
// of the configuration directory.
func TestFileNameMustMatchProjectIdentifier(t *testing.T) {
	dir := write(t, "other.yaml", fixture(t, "app.yaml"))
	opts, _ := testOptions(t, nil)

	_, err := config.LoadFile(filepath.Join(dir, "other.yaml"), opts)
	invalid := configError(t, err)

	problem := problemAt(t, invalid, "project.id")
	if !strings.Contains(problem.Reason, "other.yaml") {
		t.Errorf("problem is %q, which does not name the file", problem.Reason)
	}
}

// TestOneProjectIsConfiguredOnce rejects a project configured by two files,
// rather than picking one by a rule the user has no reason to know.
func TestOneProjectIsConfiguredOnce(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"app.yaml", "app.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("version: 1\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	_, err := config.Find(dir, "app")
	if err == nil {
		t.Fatal("expected two configuration files to be reported")
	}
	if !strings.Contains(err.Error(), "configured twice") {
		t.Errorf("error is %q, which does not explain the ambiguity", err)
	}
}

// TestMissingConfigurationIsDistinguishable checks that "no such project" is
// its own error, because it needs a different action from the user than a file
// that is present and wrong.
func TestMissingConfigurationIsDistinguishable(t *testing.T) {
	opts, _ := testOptions(t, nil)

	_, err := config.Load(t.TempDir(), "absent", opts)
	if !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("error is %v, want one matching config.ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "absent.yaml") {
		t.Errorf("error is %q, which does not say where the file belongs", err)
	}
}

// TestProjectIdentifierCannotEscapeTheConfigurationDirectory checks that a
// caller-supplied identifier is validated before it is joined into a path.
func TestProjectIdentifierCannotEscapeTheConfigurationDirectory(t *testing.T) {
	for _, id := range []string{"../escape", "..", "/etc/passwd", "a/b", ""} {
		if _, err := config.File(t.TempDir(), id); err == nil {
			t.Errorf("identifier %q was accepted as a path component", id)
		}
		if _, err := config.Find(t.TempDir(), id); err == nil {
			t.Errorf("identifier %q was accepted by Find", id)
		}
	}
}

// TestListSkipsFilesThatAreNotProjects checks that the configuration directory
// stays the user's own directory.
func TestListSkipsFilesThatAreNotProjects(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"app.yaml", "other.yml", "notes.txt", "Draft.yaml", ".hidden.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("version: 1\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	ids, err := config.List(dir)
	if err != nil {
		t.Fatalf("listing configured projects: %v", err)
	}
	if want := []string{"app", "other"}; !equal(ids, want) {
		t.Errorf("configured projects = %v, want %v", ids, want)
	}
}

// TestListOfMissingDirectoryIsEmpty checks that a machine with no configuration
// yet reports no projects rather than an error, so `feat doctor` can run before
// anything exists.
func TestListOfMissingDirectoryIsEmpty(t *testing.T) {
	ids, err := config.List(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("listing an absent configuration directory: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("configured projects = %v, want none", ids)
	}
}
