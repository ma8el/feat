package review_test

import (
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/review"
)

const publicationCommit = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"

// publication is a well-formed approved publication.
func publication() review.PublicationRequest {
	return review.PublicationRequest{
		RepositoryID: "api",
		Forge:        domain.ForgeGitLab,
		Remote:       "origin",
		Branch:       "feat/7f3a1c2e-rate-limit",
		BaseBranch:   "main",
		Commit:       publicationCommit,
		Title:        "Add a rate limit to the public API",
		Body:         "It is per token.",
		Directory:    "/state/feat/worktrees/app/7f3a1c2e/api",
		Worktrees: []string{
			"/state/feat/worktrees/app/7f3a1c2e/api",
			"/state/feat/worktrees/app/7f3a1c2e/store",
		},
	}
}

// TestAnApprovedPublicationIsCheckedBeforeItIsSent pins the ordinary case.
func TestAnApprovedPublicationIsCheckedBeforeItIsSent(t *testing.T) {
	checked, err := review.NewPublication(publication())
	if err != nil {
		t.Fatalf("checking a publication: %v", err)
	}
	if checked.RepositoryID != "api" || checked.Forge != domain.ForgeGitLab {
		t.Errorf("the checked publication is %+v", checked)
	}
	if checked.Directory != publication().Directory {
		t.Errorf("directory = %q, want the task's own worktree", checked.Directory)
	}
	// The title is trimmed and the description is not: leading whitespace in a
	// Markdown description is the author's.
	one := publication()
	one.Title = "  Add a rate limit  "
	one.Body = "  indented\n"
	checked, err = review.NewPublication(one)
	if err != nil {
		t.Fatalf("checking a publication: %v", err)
	}
	if checked.Title != "Add a rate limit" {
		t.Errorf("title = %q", checked.Title)
	}
	if checked.Body != "  indented\n" {
		t.Errorf("body = %q, want the words as they were approved", checked.Body)
	}
}

// TestAPublicationCannotEscapeItsOwnTask is the rule a viewer command follows,
// applied to something that reaches a network.
//
// Not "inside the worktree root", which would let one task publish from
// another's directory, and not "any absolute path", which is not a rule at all.
func TestAPublicationCannotEscapeItsOwnTask(t *testing.T) {
	cases := map[string]struct {
		damage   func(*review.PublicationRequest)
		contains string
	}{
		"another task's worktree": {
			damage:   func(r *review.PublicationRequest) { r.Directory = "/state/feat/worktrees/app/aaaa1111/api" },
			contains: "not one of this task's worktrees",
		},
		"a shared system directory": {
			damage: func(r *review.PublicationRequest) {
				r.Directory = "/"
				r.Worktrees = []string{"/"}
			},
			contains: "shared system directory",
		},
		"a relative path": {
			damage: func(r *review.PublicationRequest) {
				r.Directory = "worktrees/api"
				r.Worktrees = []string{"worktrees/api"}
			},
			contains: "not an absolute path",
		},
		"no directory at all": {
			damage:   func(r *review.PublicationRequest) { r.Directory = "" },
			contains: "no worktree to run in",
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			one := publication()
			test.damage(&one)

			_, err := review.NewPublication(one)
			if err == nil {
				t.Fatal("the publication was accepted")
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Errorf("the refusal is %q, and it does not say %q", err, test.contains)
			}
		})
	}
}

// TestAPublicationRefusesWhatAMergeRequestCannotCarry checks the rest.
//
// Nothing here judges the words. What a description says is the agent's, and the
// control on it is that a person read it before it was sent; these are the rules
// about what survives an argument vector and what a forge needs (ADR-070).
func TestAPublicationRefusesWhatAMergeRequestCannotCarry(t *testing.T) {
	cases := map[string]struct {
		damage   func(*review.PublicationRequest)
		contains string
	}{
		"no title": {
			damage:   func(r *review.PublicationRequest) { r.Title = "   " },
			contains: "has no title",
		},
		"a title over more than one line": {
			damage:   func(r *review.PublicationRequest) { r.Title = "one\ntwo" },
			contains: "spanning more than one line",
		},
		"a title over the limit": {
			damage: func(r *review.PublicationRequest) {
				r.Title = strings.Repeat("t", review.MaxPublicationTitle+1)
			},
			contains: "and the limit is",
		},
		"a description over the limit": {
			damage: func(r *review.PublicationRequest) {
				r.Body = strings.Repeat("b", review.MaxPublicationBody+1)
			},
			contains: "and the limit is",
		},
		"a NUL in the description": {
			damage:   func(r *review.PublicationRequest) { r.Body = "a\x00b" },
			contains: "carries a NUL",
		},
		"a remote that is an option": {
			damage:   func(r *review.PublicationRequest) { r.Remote = "--upload-pack=x" },
			contains: "reads as an option",
		},
		"a forge Feat does not publish to": {
			damage:   func(r *review.PublicationRequest) { r.Forge = "bitbucket" },
			contains: "not a forge Feat publishes to",
		},
		"an abbreviated commit": {
			damage:   func(r *review.PublicationRequest) { r.Commit = "1a2b3c4" },
			contains: "not a full commit",
		},
		"a branch merging into itself": {
			damage:   func(r *review.PublicationRequest) { r.BaseBranch = r.Branch },
			contains: "into itself",
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			one := publication()
			test.damage(&one)

			_, err := review.NewPublication(one)
			if err == nil {
				t.Fatal("the publication was accepted")
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Errorf("the refusal is %q, and it does not say %q", err, test.contains)
			}
		})
	}
}

// TestASkippedRepositoryCountsAsPublished keeps the two reasons distinct.
//
// A repository that already has a merge request is skipped as already published
// rather than refused as stale, and it is still a repository whose work is on
// the forge. Reporting it as a failure would send a user looking for something
// to fix (ADR-073).
func TestASkippedRepositoryCountsAsPublished(t *testing.T) {
	results := []review.Result{
		{RepositoryID: "api", Outcome: review.PublishedOutcome},
		{RepositoryID: "store", Outcome: review.SkippedOutcome},
		{RepositoryID: "docs", Outcome: review.FailedOutcome, Detail: "protected branch"},
	}

	if !results[1].Published() {
		t.Error("a repository skipped as already published reads as unpublished")
	}
	if results[2].Published() {
		t.Error("a repository that failed reads as published")
	}

	summary := review.SummarizePublication(results)
	for _, want := range []string{"1 published", "1 failed", "1 already published"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary %q does not say %q", summary, want)
		}
	}
	if summary := review.SummarizePublication(results[:1]); summary != "1 published" {
		t.Errorf("a publication with nothing to report says %q", summary)
	}
}
