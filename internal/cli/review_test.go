package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
)

// reviewStatus is a plausible response for one task under review.
func reviewStatus() api.ReviewStatus {
	return api.ReviewStatus{
		Task: api.Task{Key: "7f3a1c2e", Title: "Add a scheduled export job", Workflow: "verification_failed"},
		Review: api.Review{
			Summary: "Added the export job.",
			Gated:   true,
			Checks: []api.ReviewCheck{
				{ID: "unit", RepositoryID: "core", Status: "failed", Reporter: "provider"},
				{ID: "lint", RepositoryID: "core", Status: "passed", Reporter: "agent"},
			},
		},
		Repositories: []api.ReviewRepository{
			{
				RepositoryID: "core", Access: "read_write",
				BaseCommit:   "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
				HeadCommit:   "0011223344556677889900aabbccddeeff001122",
				ChangedFiles: 7, Insertions: 214, Deletions: 36, Dirty: true, Ahead: 3,
				WorktreePath: "/srv/worktrees/app/7f3a1c2e/core",
			},
			{
				RepositoryID: "schema", Access: "read_only",
				BaseCommit:   "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c",
				WorktreePath: "/srv/worktrees/app/7f3a1c2e/schema",
			},
		},
		Commands: []api.ReviewCommand{{
			Kind: "diff", RepositoryID: "core", Program: "git",
			Arguments: []string{"diff", "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"},
			Directory: "/srv/worktrees/app/7f3a1c2e/core",
		}},
		Notes: []string{"core has 2 untracked file(s)"},
	}
}

// TestAPrintedReviewNamesEveryRepositoryAndItsBase is what `feat review` reports
// where there is no terminal to open a screen in.
//
// Every repository appears with its own recorded base, and the check results say
// who produced each one: the distinction between an enforced result and a
// claimed one has to survive being printed as much as being rendered.
func TestAPrintedReviewNamesEveryRepositoryAndItsBase(t *testing.T) {
	var out bytes.Buffer
	printReview(&out, reviewStatus())
	printed := out.String()

	for _, required := range []string{
		"core", "schema",
		"1a2b3c4d5e6f", "9f8e7d6c5b4a",
		"+214 -36", "uncommitted", "3 ahead",
		"unit", "feat", "lint", "the agent",
		"git diff 1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
		"/srv/worktrees/app/7f3a1c2e/core",
		"core has 2 untracked file(s)",
	} {
		if !strings.Contains(printed, required) {
			t.Errorf("the printed review does not mention %q:\n%s", required, printed)
		}
	}

	// The workflow is the decision, and it is stated once. There was a second
	// line reading it back off the review record, which was the same fact under
	// another name and could disagree with this one (ADR-047).
	if got := strings.Count(printed, "verification_failed"); got != 1 {
		t.Errorf("the workflow appears %d times, want once:\n%s", got, printed)
	}
	if strings.Contains(printed, "decision") {
		t.Errorf("the printed review states a decision beside the workflow:\n%s", printed)
	}
}

// TestAReviewCommandIsCheckedBeforeItRuns is the client half of slice 11's third
// acceptance criterion.
//
// The daemon expanded the command and refused anything that could leave the
// task's worktrees. This checks it again, for the reason `feat runtime logs`
// checks the Compose command it is handed: they are the same user, and a client
// that ran whatever it received would be one nobody could reason about.
func TestAReviewCommandIsCheckedBeforeItRuns(t *testing.T) {
	for _, test := range []struct {
		name    string
		command api.ReviewCommand
		want    string
	}{
		{
			name:    "no program",
			command: api.ReviewCommand{Kind: "diff", Directory: "/srv/worktrees/app/7f3a1c2e/core"},
			want:    "no program",
		},
		{
			name: "a program that would be read as an option",
			command: api.ReviewCommand{Kind: "diff", Program: "--upload-pack=curl",
				Directory: "/srv/worktrees/app/7f3a1c2e/core"},
			want: "read as an option",
		},
		{
			name:    "a relative directory",
			command: api.ReviewCommand{Kind: "editor", Program: "nvim", Directory: "core"},
			want:    "non-absolute directory",
		},
		{
			name: "an argument carrying a newline",
			command: api.ReviewCommand{Kind: "status", Program: "git",
				Arguments: []string{"status\nrm -rf /"}, Directory: "/srv/worktrees/app/7f3a1c2e/core"},
			want: "NUL or a newline",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			process, err := reviewCommand(test.command)
			if err == nil {
				t.Fatalf("the command was accepted and would have run %v", process.Args)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("the refusal is %q, want it to say %q", err, test.want)
			}
		})
	}
}

// TestAWellFormedReviewCommandKeepsItsVector checks that what the daemon
// expanded is what runs, one argument at a time and in the task's own worktree.
func TestAWellFormedReviewCommandKeepsItsVector(t *testing.T) {
	command := reviewStatus().Commands[0]

	process, err := reviewCommand(command)
	if err != nil {
		t.Fatalf("a well-formed command was refused: %v", err)
	}
	if process.Dir != command.Directory {
		t.Errorf("the command would run in %q, want %q", process.Dir, command.Directory)
	}
	if len(process.Args) != len(command.Arguments)+1 {
		t.Errorf("the vector is %v, want the program and its %d arguments", process.Args, len(command.Arguments))
	}
	if process.Args[len(process.Args)-1] != command.Arguments[len(command.Arguments)-1] {
		t.Errorf("the last argument is %q, want %q",
			process.Args[len(process.Args)-1], command.Arguments[len(command.Arguments)-1])
	}
}
