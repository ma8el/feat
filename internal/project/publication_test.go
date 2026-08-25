package project_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/project"
)

// forged adds a forge declaration to one repository of the fixture.
func forged(t *testing.T, w *world, repository, kind string) {
	t.Helper()

	rewrite(t, w, "  "+repository+":\n    host_path:",
		"  "+repository+":\n    forge:\n      kind: "+kind+"\n    host_path:")
}

// writeHook gives one repository a pre-push hook, where real Git keeps it.
//
// The hook directory is what Git answers rather than where the fixture happens
// to put it: the check resolves it with `rev-parse --git-path hooks`, and a test
// that skipped that would be checking a path Feat does not use.
func writeHook(t *testing.T, w *world, repository string) {
	t.Helper()

	w.runner.output["git rev-parse --git-path hooks"] = filepath.Join(".git", "hooks")
	hooks := filepath.Join(w.home, "repos", "app", repository, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatalf("creating the hook directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("writing the hook: %v", err)
	}
}

// TestAProjectThatPublishesNowhereIsAskedNothingAboutPublishing is the rule a
// check with nothing to report follows.
//
// The fixture declares no forge, so there is no machine to ask about and no
// finding to make. A diagnostic that reported on every project's ability to
// publish would be reporting on something most of them never do.
func TestAProjectThatPublishesNowhereIsAskedNothingAboutPublishing(t *testing.T) {
	w := arrange(t)
	report := w.only(t, w.diagnose(t))

	for _, found := range report.Findings {
		if strings.HasPrefix(found.Check, "publication.") {
			t.Errorf("a project with no forge was diagnosed for publication: %+v", found)
		}
		if strings.HasSuffix(found.Check, ".publication") {
			t.Errorf("a repository with no forge was asked about its push: %+v", found)
		}
	}
}

// TestTheHostIsAskedWhetherItCanPublish is the check nothing else makes.
//
// agent.capabilities.gitlab_cli describes the agent's environment and is probed
// inside the container on a devcontainer project, so it answers a different
// question about a different machine. Publication runs on the host in both
// modes, and this is the only thing that asks the host (ADR-070, ADR-074).
func TestTheHostIsAskedWhetherItCanPublish(t *testing.T) {
	w := arrange(t)
	forged(t, w, "api", "gitlab")

	report := w.only(t, w.diagnose(t))
	found := finding(t, report.Findings, "publication.gitlab")
	if found.Severity != project.SeverityOK {
		t.Fatalf("an installed, authenticated glab was reported as %s: %s", found.Severity, found.Summary)
	}
	if !strings.Contains(found.Summary, "glab") || !strings.Contains(found.Summary, "api") {
		t.Errorf("the finding does not say what was checked for whom: %q", found.Summary)
	}

	// It is the host that was asked, not a container.
	var asked bool
	for _, call := range w.runner.calls {
		if call == "glab auth status" {
			asked = true
		}
		if strings.HasPrefix(call, "docker exec") && strings.Contains(call, "glab auth status") {
			t.Errorf("the publication check asked a container: %q", call)
		}
	}
	if !asked {
		t.Errorf("nothing asked the host whether glab is authenticated: %v", w.runner.calls)
	}
}

// TestAHostThatCannotPublishIsAWarning covers the two ways it fails.
//
// Both warn rather than fail: a project may be configured before anybody has
// logged in, and a task that is never published works without any of this.
func TestAHostThatCannotPublishIsAWarning(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		w := arrange(t)
		forged(t, w, "api", "gitlab")
		w.runner.missing["glab"] = true

		found := finding(t, w.only(t, w.diagnose(t)).Findings, "publication.gitlab")
		if found.Severity != project.SeverityWarning {
			t.Errorf("a missing glab was reported as %s", found.Severity)
		}
		if !strings.Contains(found.Summary, "not installed") {
			t.Errorf("summary = %q", found.Summary)
		}
		if !strings.Contains(found.Action, "install glab") {
			t.Errorf("action = %q", found.Action)
		}
	})

	t.Run("not authenticated", func(t *testing.T) {
		w := arrange(t)
		forged(t, w, "api", "gitlab")
		w.runner.failing["glab auth status"] = true

		found := finding(t, w.only(t, w.diagnose(t)).Findings, "publication.gitlab")
		if found.Severity != project.SeverityWarning {
			t.Errorf("an unauthenticated glab was reported as %s", found.Severity)
		}
		if !strings.Contains(found.Summary, "not authenticated") {
			t.Errorf("summary = %q", found.Summary)
		}
		// The action says why this authentication is the one that matters.
		if !strings.Contains(found.Action, "glab auth login") ||
			!strings.Contains(found.Action, "never given a provider credential") {
			t.Errorf("action = %q", found.Action)
		}
	})
}

// TestBothBuiltForgesAreAskedAboutTheHost checks that the check is about the
// forge a repository declares rather than about one of them.
//
// GitHub was reported as unbuildable here until its adapter landed, which is the
// finding this replaced: what a user needs to know now is whether the machine
// can drive gh, exactly as it needs to know about glab.
func TestBothBuiltForgesAreAskedAboutTheHost(t *testing.T) {
	w := arrange(t)
	forged(t, w, "api", "gitlab")
	forged(t, w, "web", "github")

	report := w.only(t, w.diagnose(t))
	for _, expected := range []struct{ check, tool, repository string }{
		{"publication.gitlab", "glab", "api"},
		{"publication.github", "gh", "web"},
	} {
		found := finding(t, report.Findings, expected.check)
		if found.Severity != project.SeverityOK {
			t.Errorf("%s: an installed, authenticated %s was reported as %s: %s",
				expected.check, expected.tool, found.Severity, found.Summary)
		}
		if !strings.Contains(found.Summary, expected.tool) ||
			!strings.Contains(found.Summary, expected.repository) {
			t.Errorf("%s: the finding does not say what was checked for whom: %q",
				expected.check, found.Summary)
		}
		if !slices.Contains(w.runner.calls, expected.tool+" auth status") {
			t.Errorf("%s: nothing asked the host about %s: %v", expected.check, expected.tool, w.runner.calls)
		}
	}
}

// TestOneForgeIsAskedAboutOnceHoweverManyRepositoriesDeclareIt keeps a
// five-repository project from being told the same thing five times.
func TestOneForgeIsAskedAboutOnceHoweverManyRepositoriesDeclareIt(t *testing.T) {
	w := arrange(t)
	forged(t, w, "api", "gitlab")
	forged(t, w, "web", "gitlab")
	w.runner.missing["glab"] = true

	var found []project.Finding
	for _, candidate := range w.only(t, w.diagnose(t)).Findings {
		if candidate.Check == "publication.gitlab" {
			found = append(found, candidate)
		}
	}
	if len(found) != 1 {
		t.Fatalf("one forge produced %d findings, want one: %+v", len(found), found)
	}
	// It still names what is affected.
	for _, repository := range []string{"api", "web"} {
		if !strings.Contains(found[0].Summary, repository) {
			t.Errorf("the finding does not name repository %s: %q", repository, found[0].Summary)
		}
	}
}

// TestThePrePushReportIsNamedForWhatItIsAbout pins the check name.
//
// It was `repositories.<id>.forge`, which is the configuration field that
// decides whether the check runs and reads as a check on the forge declaration.
// The forge declaration is validated when the configuration loads; this is about
// what a push will skip.
func TestThePrePushReportIsNamedForWhatItIsAbout(t *testing.T) {
	w := arrange(t)
	forged(t, w, "api", "gitlab")
	writeHook(t, w, "api")

	report := w.only(t, w.diagnose(t))
	found := finding(t, report.Findings, "repositories.api.publication")
	if found.Severity != project.SeverityWarning {
		t.Errorf("a pre-push hook was reported as %s", found.Severity)
	}
	if !strings.Contains(found.Summary, "pre-push") {
		t.Errorf("summary = %q", found.Summary)
	}

	for _, candidate := range report.Findings {
		if candidate.Check == "repositories.api.forge" {
			t.Errorf("the check still borrows the forge field's name: %+v", candidate)
		}
	}
}
