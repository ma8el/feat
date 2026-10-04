package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
)

// TestCleanupAsksOnceAndListsEveryRisk is ADR-110 on the command line: one
// answer removes every class and archives the task.
func TestCleanupAsksOnceAndListsEveryRisk(t *testing.T) {
	plan := api.CleanupPlan{
		TaskKey: "FEAT-12",
		Token:   "0f1e2d3c",
		Classes: []api.CleanupClass{
			{Class: "terminal", Title: "terminal", Targets: []api.CleanupTarget{{Identity: "@3"}}},
			{
				Class: "branches", Title: "branches",
				Targets: []api.CleanupTarget{{
					Identity: "feat/x", Repository: "api",
					Warnings: []string{"the branch is not merged into main"},
				}},
				Warnings: []string{"the branch is not merged into main"},
			},
			{
				Class: "volumes", Title: "volumes",
				Targets:  []api.CleanupTarget{{Identity: "db"}},
				Warnings: []string{"removing a volume discards whatever it holds"},
			},
		},
	}

	var out bytes.Buffer
	selection, ok, err := askCleanup(strings.NewReader("y\n"), &out, plan)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want a confirmed cleanup", ok, err)
	}
	for _, want := range []string{
		"branches of api: the branch is not merged into main",
		"volumes: removing a volume discards whatever it holds",
		"Clean up and archive task FEAT-12 anyway? [y/N]",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the prompt does not carry %q:\n%s", want, out.String())
		}
	}
	if strings.Count(out.String(), "[y/N]") != 1 {
		t.Errorf("the command asked more than once:\n%s", out.String())
	}
	if !selection.Archive || selection.Token != "0f1e2d3c" || len(selection.Classes) != 3 {
		t.Errorf("selection = %+v, want every class, the token, and an archive", selection)
	}
	if got := selection.Classes[2].ConfirmedWarnings; len(got) != 1 {
		t.Errorf("volume confirmations = %v, want the standing warning echoed", got)
	}

	_, ok, _ = askCleanup(strings.NewReader("\n"), &bytes.Buffer{}, plan)
	if ok {
		t.Error("an empty answer confirmed the cleanup")
	}
}
