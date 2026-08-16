package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/paths"
)

// draftOptions resolves a draft against a machine that is not this one, so that
// the tests do not depend on the home directory or the state directory of
// whoever runs them.
func draftOptions() Options {
	return Options{
		Env:      paths.Environment{Home: "/user", Getenv: func(string) string { return "" }},
		StateDir: "/state/feat",
	}
}

// hostDraft is the smallest project the wizard can produce: one repository, the
// agent on this host, no runtime and no checks.
func hostDraft() Draft {
	return Draft{
		ID:      "app",
		Name:    "Example Application",
		Primary: "api",
		Repositories: []DraftRepository{{
			ID:            "api",
			HostPath:      "/checkouts/api",
			DefaultBranch: "main",
			Remote:        "origin",
			DefaultAccess: "read_write",
		}},
		Execution: DraftExecution{Mode: ModeHost},
	}
}

// devcontainerDraft is a two-repository project whose agent runs in a container,
// with an application runtime and a check.
func devcontainerDraft() Draft {
	draft := hostDraft()
	draft.Repositories[0].AgentContainerPath = "/srv/api"
	draft.Repositories[0].Runtime = &DraftRepositoryRuntime{
		ComposeFiles:  []string{"docker-compose.yml"},
		ContainerPath: "/app",
		Services:      []string{"app", "worker"},
		Reachable:     []string{"app"},
	}
	draft.Repositories = append(draft.Repositories, DraftRepository{
		ID:                 "store",
		HostPath:           "/checkouts/store",
		AgentContainerPath: "/srv/store",
		DefaultBranch:      "main",
		Remote:             "origin",
		DefaultAccess:      "selectable",
	})
	draft.Execution = DraftExecution{
		Mode:               ModeDevcontainer,
		ComposeFiles:       []string{"/checkouts/api/docker-compose.yml"},
		Service:            "dev",
		User:               "developer",
		ClaudeConfigVolume: "feat-claude",
	}
	draft.Capabilities = DraftCapabilities{GitHubCLI: CLIOptional}
	draft.Runtime = &DraftRuntime{EnvFiles: []string{"/checkouts/api/.env"}}
	draft.Checks = []DraftCheck{{
		Repository: "api",
		ID:         "test",
		Command:    []string{"go", "test", "./..."},
		Execution:  ExecutionAgent,
	}}
	return draft
}

// TestADraftRendersAConfigurationFeatAccepts is the property the whole type
// exists for: what the wizard offers to write is what Feat would load.
func TestADraftRendersAConfigurationFeatAccepts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		draft Draft
	}{
		{"host", hostDraft()},
		{"devcontainer", devcontainerDraft()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join("/config/feat/projects", tc.draft.ID+".yaml")

			cfg, rendered, err := tc.draft.Config(file, draftOptions())
			if err != nil {
				t.Fatalf("the rendered configuration does not load: %v\n\n%s", err, tc.draft.Render())
			}
			// The text that comes back is the text that was validated, which is
			// what lets a caller display and write it without rendering again.
			if string(rendered) != string(tc.draft.Render()) {
				t.Errorf("the validated text is not the draft's rendering:\n%s", rendered)
			}
			if cfg.ID() != tc.draft.ID {
				t.Errorf("project id is %q, want %q", cfg.ID(), tc.draft.ID)
			}
			if cfg.Project.PrimaryRepository != tc.draft.Primary {
				t.Errorf("primary repository is %q, want %q", cfg.Project.PrimaryRepository, tc.draft.Primary)
			}
			if len(cfg.Repositories) != len(tc.draft.Repositories) {
				t.Errorf("%d repositories, want %d", len(cfg.Repositories), len(tc.draft.Repositories))
			}
		})
	}
}

// TestADraftWritesWhatWasDecidedAndNothingElse pins which fields reach the
// file. A default written down is a value that stops following Feat, and a
// field a mode does not use is one the configuration rejects.
func TestADraftWritesWhatWasDecidedAndNothingElse(t *testing.T) {
	host := string(hostDraft().Render())

	for _, absent := range []string{
		"branch_template", "worktree_root", "base_policy", "provider",
		"container_path", "compose_files", "service:", "user:", "control_path",
		"config_volume", "runtime:", "checks:", "review:", "notifications:", "resources:",
	} {
		if strings.Contains(host, absent) {
			t.Errorf("a host-mode project with no runtime writes %q:\n%s", absent, host)
		}
	}
	for _, present := range []string{
		"version: 1", "id: app", "primary_repository: api",
		"host_path: /checkouts/api", "default_access: read_write", "mode: host",
		"docker: denied", "network: unrestricted", "git: full",
		"github_cli: disabled", "gitlab_cli: disabled",
	} {
		if !strings.Contains(host, present) {
			t.Errorf("a host-mode project does not write %q:\n%s", present, host)
		}
	}
}

// TestADevcontainerDraftWritesEveryFieldItsModeNeeds checks the fields the
// container mode makes mandatory, and the two optional sections.
func TestADevcontainerDraftWritesEveryFieldItsModeNeeds(t *testing.T) {
	rendered := string(devcontainerDraft().Render())

	for _, present := range []string{
		"mode: devcontainer",
		"- /checkouts/api/docker-compose.yml",
		"service: dev",
		"user: developer",
		"config_volume: feat-claude",
		"github_cli: optional",
		"container_path: /srv/api",
		"container_path: /srv/store",
		"default_access: selectable",
		"runtime:",
		"- /checkouts/api/.env",
		"- worker",
		"checks:",
		"- id: test",
		"execution: agent",
	} {
		if !strings.Contains(rendered, present) {
			t.Errorf("the rendering does not write %q:\n%s", present, rendered)
		}
	}
}

// TestADraftKeepsTheDefaultsItLeavesOut checks that leaving a field out is not
// leaving a value out: Resolve fills them in, and `feat project show` is where
// the user reads them.
func TestADraftKeepsTheDefaultsItLeavesOut(t *testing.T) {
	cfg, _, err := hostDraft().Config("/config/feat/projects/app.yaml", draftOptions())
	if err != nil {
		t.Fatalf("loading the rendered configuration: %v", err)
	}

	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		{"git.base_policy", cfg.Git.BasePolicy, PolicyRemote},
		{"git.branch_template", cfg.Git.BranchTemplate, defaultBranchTemplate},
		{"agent.provider", cfg.Agent.Provider, ProviderClaude},
		{"agent.capabilities.docker", cfg.Agent.Capabilities.Docker, CapabilityDenied},
	} {
		if field.got != field.want {
			t.Errorf("%s resolves to %q, want %q", field.name, field.got, field.want)
		}
	}
	if want := "/state/feat/worktrees/{project_id}/{task_id}"; cfg.Git.WorktreeRoot != want {
		t.Errorf("git.worktree_root resolves to %q, want %q", cfg.Git.WorktreeRoot, want)
	}
}

// TestADraftIsRefusedBeforeItIsAFile checks that a draft Feat would not accept
// fails where the answers still are, rather than after something is written.
func TestADraftIsRefusedBeforeItIsAFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		draft   func(Draft) Draft
		problem string
	}{
		{
			name:    "a primary repository that is not one of the repositories",
			draft:   func(d Draft) Draft { d.Primary = "web"; return d },
			problem: "project.primary_repository",
		},
		{
			name: "a devcontainer with no mount for a repository",
			draft: func(d Draft) Draft {
				d.Execution = DraftExecution{
					Mode:         ModeDevcontainer,
					ComposeFiles: []string{"/checkouts/api/docker-compose.yml"},
					Service:      "dev",
					User:         "developer",
				}
				return d
			},
			problem: "repositories.api.agent.container_path",
		},
		{
			name: "an agent running as root",
			draft: func(d Draft) Draft {
				d.Repositories[0].AgentContainerPath = "/srv/api"
				d.Execution = DraftExecution{
					Mode:         ModeDevcontainer,
					ComposeFiles: []string{"/checkouts/api/docker-compose.yml"},
					Service:      "dev",
					User:         "root",
				}
				return d
			},
			problem: "agent.execution.user",
		},
		{
			name:    "a project identifier that does not match the file",
			draft:   func(d Draft) Draft { d.ID = "other"; return d },
			problem: "project.id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := tc.draft(hostDraft())

			_, _, err := draft.Config("/config/feat/projects/app.yaml", draftOptions())
			if err == nil {
				t.Fatalf("the draft was accepted:\n%s", draft.Render())
			}
			if !strings.Contains(err.Error(), tc.problem) {
				t.Errorf("the failure does not name %s: %v", tc.problem, err)
			}
		})
	}
}

// TestADraftQuotesAValueThatWouldBeReadAsSomethingElse checks the one thing a
// hand-written renderer gets wrong: a value YAML reads as a boolean, a number,
// or a comment.
func TestADraftQuotesAValueThatWouldBeReadAsSomethingElse(t *testing.T) {
	draft := hostDraft()
	draft.Name = "no"
	draft.Repositories[0].DefaultBranch = "0755"
	draft.Repositories[0].HostPath = "/checkouts/api #1"

	cfg, _, err := draft.Config("/config/feat/projects/app.yaml", draftOptions())
	if err != nil {
		t.Fatalf("loading the rendered configuration: %v\n\n%s", err, draft.Render())
	}
	if cfg.Project.Name != "no" {
		t.Errorf("project.name loaded as %q, want %q", cfg.Project.Name, "no")
	}

	repository, _ := cfg.Repository("api")
	if repository.DefaultBranch != "0755" {
		t.Errorf("default_branch loaded as %q, want %q", repository.DefaultBranch, "0755")
	}
	if want := "/checkouts/api #1"; repository.HostPath != want {
		t.Errorf("host_path loaded as %q, want %q", repository.HostPath, want)
	}
}
