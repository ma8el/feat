package config_test

import (
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
)

// taskValues are the values a task preparation supplies.
func taskValues() config.Values {
	return config.Values{
		ProjectID:    "app",
		TaskID:       "0f8fad5b-d9cb-469f-a165-70867728950e",
		TaskKey:      "0f8fad5b",
		RepositoryID: "api",
		Slug:         "add-a-rate-limit",
	}
}

// TestExpandFillsTheDocumentedPlaceholders checks the vocabulary a branch name
// and a worktree path are built from.
func TestExpandFillsTheDocumentedPlaceholders(t *testing.T) {
	for _, tc := range []struct {
		template string
		want     string
	}{
		{"feat/{task_key}-{slug}", "feat/0f8fad5b-add-a-rate-limit"},
		{"{project_id}/{repository_id}/{task_id}", "app/api/0f8fad5b-d9cb-469f-a165-70867728950e"},
		{"no placeholders at all", "no placeholders at all"},
		{"{task_key}{task_key}", "0f8fad5b0f8fad5b"},
	} {
		got, err := config.Expand(tc.template, taskValues())
		if err != nil {
			t.Errorf("expanding %q: %v", tc.template, err)
			continue
		}
		if got != tc.want {
			t.Errorf("expanding %q gave %q, want %q", tc.template, got, tc.want)
		}
	}
}

// TestExpandRefusesWhatItCannotFill checks the closed vocabulary.
//
// A name Feat does not expand must never survive into a branch name, a path, or
// a command argument, and neither must a placeholder this particular expansion
// has no value for: an empty expansion is how two tasks end up sharing one
// branch.
func TestExpandRefusesWhatItCannotFill(t *testing.T) {
	for _, tc := range []struct {
		name     string
		template string
		values   config.Values
		want     string
	}{
		{
			name:     "an unknown placeholder",
			template: "feat/{repo}-{slug}",
			values:   taskValues(),
			want:     "does not expand",
		},
		{
			name:     "a placeholder with no value in this expansion",
			template: "feat/{task_key}-{slug}",
			values:   config.Values{TaskKey: "0f8fad5b"},
			want:     "no value for it",
		},
		{
			name:     "a mistyped placeholder",
			template: "feat/{task_key-{slug}",
			values:   taskValues(),
			want:     "unmatched",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expanded, err := config.Expand(tc.template, tc.values)
			if err == nil {
				t.Fatalf("expanding %q gave %q, want an error", tc.template, expanded)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expanding %q reported %v, want a message containing %q", tc.template, err, tc.want)
			}
		})
	}
}

// TestExpandIsNotRecursive checks that a substituted value cannot introduce a
// placeholder of its own. Every value Feat substitutes is a validated
// identifier, but the rule is what makes that safe rather than lucky.
func TestExpandIsNotRecursive(t *testing.T) {
	values := taskValues()
	values.Slug = "{task_id}"

	got, err := config.Expand("feat/{slug}", values)
	if err != nil {
		t.Fatalf("expanding: %v", err)
	}
	if got != "feat/{task_id}" {
		t.Errorf("expanding gave %q, want the value substituted once and left alone", got)
	}
}

// TestUsesFindsAPlaceholder checks the question the worktree root asks: a root
// that already names the repository is one directory per repository, and one
// that does not needs the repository appended.
func TestUsesFindsAPlaceholder(t *testing.T) {
	for template, want := range map[string]bool{
		"/state/{project_id}/{task_id}/{repository_id}": true,
		"/state/{project_id}/{task_id}":                 false,
		"/state/{repository_id}":                        true,
		"/state/static":                                 false,
	} {
		if got := config.Uses(template, config.PlaceholderRepositoryID); got != want {
			t.Errorf("Uses(%q) = %t, want %t", template, got, want)
		}
	}
}

// TestProjectPrefixNamesTheDirectoryAProjectKeeps separates the three
// directories a worktree root generates, because each of them is treated
// differently and only their boundaries say which is which.
//
// The fixed prefix is shared by every project on the machine, a task's own
// directory goes when the task is cleaned up, and what is between them belongs
// to the project: it survives the last task and is not a directory nobody
// claims. A layout that generates nothing between them answers with the fixed
// prefix, so both rules apply from there rather than from a directory that does
// not exist.
func TestProjectPrefixNamesTheDirectoryAProjectKeeps(t *testing.T) {
	for _, tc := range []struct {
		template string
		project  string
		want     string
	}{
		{"/state/worktrees/{project_id}/{task_id}", "app", "/state/worktrees/app"},
		{"/state/worktrees/{project_id}/{task_id}/{repository_id}", "app", "/state/worktrees/app"},
		// Deeper: everything fixed for the project is the project's, not only
		// the segment the placeholder is in.
		{"/state/{project_id}/tasks/{task_id}", "app", "/state/app/tasks"},
		// No directory of its own: one per task, or one for every project.
		{"/state/worktrees/{task_id}", "app", "/state/worktrees"},
		{"/state/worktrees/{project_id}-{task_id}", "app", "/state/worktrees"},
		{"/state/worktrees/fixed/{task_key}", "app", "/state/worktrees/fixed"},
		// A template naming no task is not a worktree root a task is created
		// under, and this says nothing more about it than the fixed prefix does.
		{"/state/worktrees/{project_id}", "app", "/state/worktrees"},
		{"/state/worktrees/{project_id}/{task_id}", "", "/state/worktrees"},
	} {
		if got := config.ProjectPrefix(tc.template, tc.project); got != tc.want {
			t.Errorf("ProjectPrefix(%q, %q) = %q, want %q", tc.template, tc.project, got, tc.want)
		}
	}
}

// TestSlugIsSafeInABranchName checks what a task title becomes.
//
// The slug reaches a branch name, so what matters is that nothing survives it
// that Git, a filesystem, or a command line would treat as something other than
// text.
func TestSlugIsSafeInABranchName(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  string
	}{
		{"Add a rate limit", "add-a-rate-limit"},
		{"Fix: the API/v2 endpoint (again!)", "fix-the-api-v2-endpoint-again"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"--dangerous --flags--", "dangerous-flags"},
		{"a..b", "a-b"},
		{"tabs\tand\nnewlines", "tabs-and-newlines"},
		{"", "task"},
		{"日本語", "task"},
		{strings.Repeat("very long title ", 10), "very-long-title-very-long-title-very"},
	} {
		got := config.Slug(tc.title)
		if got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.title, got, tc.want)
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("Slug(%q) = %q, which starts or ends with a separator", tc.title, got)
		}
		if strings.ContainsAny(got, " ~^:?*[\\/.") {
			t.Errorf("Slug(%q) = %q, which contains a character Git rejects in a ref", tc.title, got)
		}
	}
}
