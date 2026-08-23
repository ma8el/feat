package execution_test

import (
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
)

const (
	task    = domain.TaskID("11111111-1111-4111-8111-111111111111")
	project = domain.ProjectID("app")
)

// valid returns a specification that passes, so that each case below changes
// exactly one thing and the failure names that thing.
func valid() execution.Spec {
	return execution.Spec{
		Project:          project,
		Task:             task,
		Identity:         "feat-agent-app-11111111-1111-4111-8111-111111111111",
		Files:            []string{"/repos/app/infra/docker-compose.yml"},
		Directory:        "/repos/app/infra",
		OverridePath:     "/state/projects/app/tasks/" + string(task) + "/execution/compose.override.yaml",
		Service:          "dev",
		User:             "dev",
		WorkingDirectory: "/srv/api",
		Mounts: []execution.Mount{
			{Source: "/worktrees/api", Target: "/srv/api", Description: "the api task worktree"},
			{Source: "/state/control/app/" + string(task), Target: "/feat", Description: "the control workspace"},
		},
		ForbiddenSources: []execution.ForbiddenSource{
			{Path: "/repos/app/api", Kind: execution.ForbiddenCheckout},
		},
	}
}

// TestASpecificationIsCheckedBeforeItCanCreateAnything covers the values that
// end up in a command that mounts the user's filesystem.
//
// Each case is a way a specification can be wrong that nothing downstream would
// catch: a container would simply be created with the wrong mounts, and every
// record Feat kept about it would be correct.
func TestASpecificationIsCheckedBeforeItCanCreateAnything(t *testing.T) {
	for name, testCase := range map[string]struct {
		change   func(*execution.Spec)
		contains string
	}{
		"no identity": {
			change:   func(s *execution.Spec) { s.Identity = "" },
			contains: "makes an action affect one task",
		},
		"no files": {
			change:   func(s *execution.Spec) { s.Files = nil },
			contains: "names no files",
		},
		"relative file": {
			change:   func(s *execution.Spec) { s.Files = []string{"docker-compose.yml"} },
			contains: "must be absolute",
		},
		"uncleaned override path": {
			change:   func(s *execution.Spec) { s.OverridePath = "/state/../state/override.yaml" },
			contains: "written cleanly",
		},
		"no service": {
			change:   func(s *execution.Spec) { s.Service = "" },
			contains: "names no service",
		},
		"root user": {
			change:   func(s *execution.Spec) { s.User = "root" },
			contains: "must not be root",
		},
		"numeric root user": {
			change:   func(s *execution.Spec) { s.User = "0" },
			contains: "must not be root",
		},
		"relative working directory": {
			change:   func(s *execution.Spec) { s.WorkingDirectory = "api" },
			contains: "must be absolute",
		},
		"nothing mounted": {
			change:   func(s *execution.Spec) { s.Mounts = nil },
			contains: "mounts nothing",
		},
		"two mounts at one target": {
			change: func(s *execution.Spec) {
				s.Mounts = append(s.Mounts, execution.Mount{Source: "/elsewhere", Target: "/srv/api"})
			},
			contains: "would hide the other",
		},
		"a volume over a mount": {
			change: func(s *execution.Spec) {
				s.Volumes = []execution.Volume{{Name: "claude", Target: "/feat"}}
			},
			contains: "is already mounted",
		},
		"the ordinary checkout is mounted": {
			change: func(s *execution.Spec) {
				s.Mounts[0].Source = "/repos/app/api"
			},
			contains: "the task was supposed to leave alone",
		},
		"a mount source with a trailing separator": {
			change: func(s *execution.Spec) {
				s.Mounts[0].Source = "/repos/app/api/"
			},
			// Refused for being uncleaned, before the comparison below is
			// reached. Both refuse it; this one refuses it first.
			contains: "written cleanly",
		},
		"the ordinary checkout is named with a trailing separator": {
			change: func(s *execution.Spec) {
				s.ForbiddenSources = []execution.ForbiddenSource{
					{Path: "/repos/app/api/", Kind: execution.ForbiddenCheckout},
				}
				s.Mounts[0].Source = "/repos/app/api"
			},
			contains: "the task was supposed to leave alone",
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec := valid()
			testCase.change(&spec)

			err := spec.Validate()
			if err == nil {
				t.Fatalf("the specification was accepted")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("the message does not explain the problem\n got: %v\nwant: %q", err, testCase.contains)
			}
		})
	}

	if err := valid().Validate(); err != nil {
		t.Fatalf("the unmodified specification was refused: %v", err)
	}
}

// TestMountingTheOrdinaryCheckoutIsRefused is the narrow statement of ADR-033
// evidence 1, at the layer that can make it without a container.
//
// The failure it prevents is silent: the agent holds both its task worktree and
// the user's own working copy, and everything Feat records about the task is
// correct. Leaving the user's own checkout alone is what this protects.
func TestMountingTheOrdinaryCheckoutIsRefused(t *testing.T) {
	spec := valid()
	spec.Mounts = append(spec.Mounts, execution.Mount{
		Source: "/repos/app/api", Target: "/srv/ordinary",
	})

	err := spec.Validate()
	if err == nil {
		t.Fatal("a specification mounting the user's own checkout was accepted")
	}
	for _, expected := range []string{"/repos/app/api", "/srv/ordinary", "leave alone"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not name %q: %v", expected, err)
		}
	}
}

// TestACommandIsAnArgumentVector checks that nothing reaches a container
// runtime that it would read as one of its own flags.
func TestACommandIsAnArgumentVector(t *testing.T) {
	for name, testCase := range map[string]struct {
		command  execution.Command
		contains string
	}{
		"no program": {
			command:  execution.Command{},
			contains: "must name a program",
		},
		"program reads as a flag": {
			command:  execution.Command{Program: "--user"},
			contains: "must not begin with",
		},
		"argument carries a newline": {
			command:  execution.Command{Program: "claude", Arguments: []string{"one\ntwo"}},
			contains: "NUL or a newline",
		},
		"relative directory": {
			command:  execution.Command{Program: "claude", Directory: "api"},
			contains: "must be absolute",
		},
		"variable name carries an equals sign": {
			command: execution.Command{
				Program: "claude", Variables: map[string]string{"A=B": "c"},
			},
			contains: "must not contain",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := testCase.command.Validate()
			if err == nil {
				t.Fatalf("the command was accepted")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("the message does not explain the problem\n got: %v\nwant: %q", err, testCase.contains)
			}
		})
	}
}

// TestVariablesRenderInAFixedOrder pins that a generated command is the same
// command every time.
//
// Environment entries reach an argument vector, and Go's map iteration order
// does not repeat. Without this, a test that pins a command would pass or fail
// depending on the run, which is worse than not pinning it at all.
func TestVariablesRenderInAFixedOrder(t *testing.T) {
	command := execution.Command{
		Program: "claude",
		Variables: map[string]string{
			"CLAUDE_CONFIG_DIR": "/feat-claude",
			"FEAT_TASK":         string(task),
			"A_FIRST":           "1",
		},
	}

	want := []string{"A_FIRST=1", "CLAUDE_CONFIG_DIR=/feat-claude", "FEAT_TASK=" + string(task)}
	for range 20 {
		entries := command.Entries()
		if len(entries) != len(want) {
			t.Fatalf("got %d entries, want %d", len(entries), len(want))
		}
		for i, entry := range entries {
			if entry != want[i] {
				t.Fatalf("entry %d is %q, want %q", i, entry, want[i])
			}
		}
	}
}
