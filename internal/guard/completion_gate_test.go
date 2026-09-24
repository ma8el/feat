package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// The completion gate is `make check`, which CLAUDE.md declares work complete
// against. Two of its properties live in configuration rather than code, and
// both were once wrong: the integration tier downgraded a missing tool to a
// skip, and a skipped package prints "ok"; and CI ran the tier without -count=1
// against a warm cache, so a re-run could replay a cached PASS.
//
// Nothing in a Go test suite notices either. These two tests hold that
// configuration, so an edit to a Makefile line or a workflow step cannot return
// the gate to reporting on nothing.

const (
	makefilePath = "Makefile"
	workflowPath = ".github/workflows/ci.yml"

	envIntegrationRequire = "FEAT_INTEGRATION_REQUIRE"
	countOnce             = "-count=1"
)

// TestTheCompletionGateRunsTheIntegrationTierAndDemandsItsTools reads the
// Makefile. Running test-real is what puts the tier in the gate, -count=1 stops
// Go replaying a cached pass about tools it cannot observe, and
// FEAT_INTEGRATION_REQUIRE turns an absent tool from a skip into a failure.
func TestTheCompletionGateRunsTheIntegrationTierAndDemandsItsTools(t *testing.T) {
	makefile := readRepoFile(t, makefilePath)

	prerequisites, found := targetPrerequisites(makefile, "check")
	if !found {
		t.Fatalf("%s has no check target, which is the completion gate CLAUDE.md names", makefilePath)
	}
	if !contains(prerequisites, "test-real") {
		t.Errorf("%s: the check target is %v and does not run test-real\n"+
			"\tThe integration tier is the only thing in this repository that proves anything "+
			"about Git, tmux, Docker, or a container boundary.", makefilePath, prerequisites)
	}

	recipe, found := targetRecipe(makefile, "test-real")
	if !found {
		t.Fatalf("%s has no test-real target", makefilePath)
	}
	if !strings.Contains(recipe, countOnce) {
		t.Errorf("%s: the test-real recipe does not pass %s\n"+
			"\tThese tests assert about tools outside the process, which the test cache cannot see, "+
			"so a previous PASS replays unchanged (ADR-035 evidence 13).\n\t%s",
			makefilePath, countOnce, recipe)
	}
	if !strings.Contains(recipe, envIntegrationRequire) {
		t.Errorf("%s: the test-real recipe does not set %s\n"+
			"\tWithout a demand, a missing Docker, tmux, or Git is a skip; a package whose selected "+
			"tests all skipped prints \"ok\"; and the gate reports green having run nothing.\n\t%s",
			makefilePath, envIntegrationRequire, recipe)
	}
}

// TestCIRunsTheIntegrationTierForReal reads the workflow. It asserts of every
// step that opts in to the tier what the Makefile asserts of test-real, because
// CI had neither property and a green tick meant less than the same command on a
// laptop.
func TestCIRunsTheIntegrationTierForReal(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, workflowPath)), &workflow); err != nil {
		t.Fatalf("reading %s: %v", workflowPath, err)
	}

	steps := 0
	for job, definition := range workflow.Jobs {
		for _, step := range definition.Steps {
			if step.Env[envIntegrationName] == "" {
				continue
			}
			steps++

			where := workflowPath + ": " + job + " / " + step.Name
			if !strings.Contains(step.Run, countOnce) {
				t.Errorf("%s runs the integration tier without %s\n"+
					"\t-run is a cacheable flag and the test cache tracks nothing these tests observe "+
					"through a subprocess, so a re-run can replay a cached PASS and exercise no tool "+
					"at all (ADR-035 evidence 13).\n\t%s", where, countOnce, strings.TrimSpace(step.Run))
			}
			if step.Env[envIntegrationRequire] == "" {
				t.Errorf("%s runs the integration tier without setting %s\n"+
					"\tA missing tool is then a skip, and a package whose selected tests all skipped "+
					"prints \"ok\". The runner image would have to lose Docker for anyone to find out.\n"+
					"\tName the tools this leg demands; a leg that demands fewer says so here.", where, envIntegrationRequire)
			}
		}
	}

	if steps == 0 {
		t.Errorf("no step in %s sets %s, so CI runs no integration tests at all",
			workflowPath, envIntegrationName)
	}
}

// readRepoFile reads one file from the module root.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(raw)
}

// targetPrerequisites returns the names a Make target depends on.
func targetPrerequisites(makefile, target string) ([]string, bool) {
	for _, line := range strings.Split(makefile, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != target {
			continue
		}
		// This repository writes no double-colon rule and no assignment such
		// as "check: = x", so a leading "=" is the only ambiguity worth
		// refusing.
		if strings.HasPrefix(rest, "=") {
			continue
		}
		if comment, _, found := strings.Cut(rest, "##"); found {
			rest = comment
		}
		return strings.Fields(rest), true
	}
	return nil, false
}

// targetRecipe returns the command lines of a Make target.
func targetRecipe(makefile, target string) (string, bool) {
	lines := strings.Split(makefile, "\n")
	for i, line := range lines {
		name, _, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != target {
			continue
		}
		var recipe []string
		for _, line := range lines[i+1:] {
			if strings.HasPrefix(line, "\t") {
				recipe = append(recipe, strings.TrimPrefix(line, "\t"))
				continue
			}
			// Comments and blank lines sit inside a recipe in this Makefile.
			// Anything else at column zero ends it.
			if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
				continue
			}
			break
		}
		return strings.Join(recipe, "\n"), true
	}
	return "", false
}

// contains reports whether a list holds a value.
func contains(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}
