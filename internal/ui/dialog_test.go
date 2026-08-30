package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/wizard"
)

// widestLine is a document's widest line and where in it that line is.
//
// Both halves are used. The width is what the block has to measure at every
// offset, and it is asserted against rather than a constant so that a test does
// not encode a fixture's dimensions. The position is what makes the test mean
// anything: a widest line that is on the screen from the start proves nothing
// about scrolling.
func widestLine(lines []string) (width, at int) {
	for i, line := range lines {
		if measured := ansi.StringWidth(line); measured > width {
			width, at = measured, i
		}
	}
	return width, at
}

// scrollingKeepsItsWidth is the shape of the four tests below.
//
// dialogBox shrinks the box to the widest line of what it is handed, and a body
// that scrolls hands it a window rather than a document — so the widest
// *visible* line decided the width, and scrolling changed which lines those
// were. The document is rendered at two offsets, one showing its widest line and
// one not, and the block has to measure the same both times.
//
// It is checked against the document's own widest line rather than against the
// two offsets agreeing, because two offsets agreeing is also what a body that
// pads everything to the full region would do, and that is the other defect
// (see wizardModel.wrap).
func scrollingKeepsItsWidth(t *testing.T, name string, widest int, top, bottom string) {
	t.Helper()

	first, last := blockWidth(top), blockWidth(bottom)
	if first != last {
		t.Errorf("the %s is %d cells at the top and %d at the end", name, first, last)
	}
	if first != widest {
		t.Errorf("the %s is %d cells at the top for a document %d wide, so it measured "+
			"the window rather than the document", name, first, widest)
	}
	if last != widest {
		t.Errorf("the %s is %d cells at the end for a document %d wide", name, last, widest)
	}
}

// TestThePublicationDraftKeepsItsWidthWhileItIsRead is the reported defect.
//
// Reading a publication draft, a long line further down the document widened the
// box the moment it came into view. The rule is reviewView's, which has had it
// since it was written: every line is drawn in the width of the document's
// widest, and the measure is the whole document rather than the window, because
// the window is what changes.
func TestThePublicationDraftKeepsItsWidthWhileItIsRead(t *testing.T) {
	backend := newFakeBackend()
	model := sized(publishable(t, backend), 120, 32)
	model.publication.status.Drafts[0].Body = longDescription()
	// A short branch, so that the entry's own first line is not the widest thing
	// in the document: the point is a line the reader has to scroll to.
	model.publication.status.Drafts[0].Branch = "feat/export"
	// And that line: a merge request this task already opened, drawn after the
	// words and wrapped to nothing.
	model.publication.status.Task.Publication = &api.Publication{
		Repositories: []api.PublicationRepository{{
			RepositoryID: "core", State: "published",
			Request: &api.MergeRequest{
				Reference: "core!1",
				URL:       "https://forge.example.test/group/core/-/merge_requests/1",
			},
		}},
	}

	width, height := model.publicationDocumentSize()
	lines, _ := model.publicationLines(width)
	widest := requireScrollable(t, lines, width, height)

	model.publication.scroll = 0
	top := model.publicationWindow(width, height)
	model.publication.scroll = len(lines) - height
	bottom := model.publicationWindow(width, height)

	scrollingKeepsItsWidth(t, "draft", widest, top, bottom)
}

// TestTheDiagnosisReportKeepsItsWidthWhileItIsRead is the same defect in the
// report `feat doctor` draws.
func TestTheDiagnosisReportKeepsItsWidthWhileItIsRead(t *testing.T) {
	// A report of its own rather than the shared fixture, whose widest line is
	// already at the region's limit and would leave nothing for scrolling to
	// change.
	report := api.Diagnosis{
		Environment: "this terminal",
		Projects:    []api.ProjectDiagnosis{{ID: "example", File: "/config/example.yaml"}},
		Host: []api.Finding{
			{Check: "git", Severity: api.SeverityOK, Summary: "git version 2.52.0"},
		},
	}
	for i := range 30 {
		report.Projects[0].Findings = append(report.Projects[0].Findings, api.Finding{
			Check: "repositories.core.check" + strconv.Itoa(i), Severity: api.SeverityOK,
			Summary: "a check that passed",
		})
	}
	// The host section is drawn after the projects, so a wide finding in it is a
	// wide line the reader has to scroll to.
	report.Host = append(report.Host, api.Finding{
		Check: "compose", Severity: api.SeverityWarning,
		Summary: "the Compose CLI answered, but slowly enough to be worth saying so",
	})

	backend := newFakeBackend()
	backend.diagnosis = report
	model := press(t, sized(dashboard(backend, liveTask()), 120, 32), "D")

	inner, tallest := model.dialogLimits()
	width, height := inner-cardChrome, tallest-diagnosisChrome
	lines := model.diagnosis.lines()
	widest := requireScrollable(t, lines, width, height)

	// The report opens at the first problem rather than at the top, so the top is
	// asked for rather than assumed.
	model.diagnosis.scroll = 0
	top := model.diagnosis.body(width, height)
	model.diagnosis.scroll = len(lines) - height
	bottom := model.diagnosis.body(width, height)

	scrollingKeepsItsWidth(t, "report", widest, top, bottom)
}

// TestTheCleanupInventoryKeepsItsWidthWhileItIsRead is the same defect in the
// inventory, where the long line is a resource identity.
func TestTheCleanupInventoryKeepsItsWidthWhileItIsRead(t *testing.T) {
	plan := longCleanupPlan(6)
	// A class at the foot whose target is named at length, which is what a
	// container or a worktree path looks like next to `class0/one`.
	plan.Classes = append(plan.Classes, api.CleanupClass{
		Class: "containers", Title: "containers",
		Targets: []api.CleanupTarget{{
			Identity: "feat-agent-example-7f3a1c2e-dev-run-a1b2c3d4e5f6",
			Detail:   "created when the task was launched", Present: true,
		}},
	})

	backend := newFakeBackend()
	model := sized(openCleanupPlan(t, backend, plan), 120, 32)

	width, height := model.cleanupInventorySize()
	lines := model.cleanupLines(width)
	// One line of the region belongs to the note that says there is more, so the
	// window is that much shorter than the space.
	widest := requireScrollable(t, lines, width, height-1)

	model.cleanup.scroll = 0
	top := model.cleanupInventory(width, height)
	// Past the end; the inventory clamps it to the last window, which is the
	// scroller this slice leaves alone.
	model.cleanup.scroll = len(lines)
	bottom := model.cleanupInventory(width, height)

	scrollingKeepsItsWidth(t, "inventory", widest, top, bottom)
}

// TestTheWizardReviewKeepsItsWidthWhileItIsRead guards the lift.
//
// The wizard is where this rule was written and the one body that already had
// it. Moving the measurement into dialog.go so three other bodies could share it
// must not cost the body it came from.
func TestTheWizardReviewKeepsItsWidthWhileItIsRead(t *testing.T) {
	// A file whose widest line is its last: short entries, and one long path at
	// the foot, which is what a repository list looks like.
	var text strings.Builder
	for i := range 40 {
		text.WriteString("  - id: repo" + strconv.Itoa(i) + "\n")
	}
	text.WriteString("    path: /srv/checkouts/example/a-repository-with-a-long-name\n")

	model := sized(dashboard(newFakeBackend()), 120, 32)
	model.screen = screenWizard
	model.wizard = wizardModel{
		asked: true, step: wizardReviewing,
		review: wizard.Review{Path: "/config/example.yaml", Text: []byte(text.String())},
	}
	model.wizard.resize(model.preparationSize())

	lines := strings.Split(strings.TrimRight(text.String(), "\n"), "\n")
	height := model.wizard.reviewHeight()
	widest := requireScrollable(t, lines, model.wizard.width-2, height)

	model.wizard.scroll = 0
	top := model.wizard.reviewView()
	model.wizard.scroll = len(lines) - height
	bottom := model.wizard.reviewView()

	scrollingKeepsItsWidth(t, "configuration", widest, top, bottom)
}

// requireScrollable checks that a document is one this measurement can say
// anything about, and returns its widest line.
//
// Two ways it could not be. A widest line inside the first window is on the
// screen from the start, so no offset ever removes it; and a widest line past
// the region is clamped to the region at every offset, so the block is the same
// width whether or not anything measured the document.
func requireScrollable(t *testing.T, lines []string, width, height int) int {
	t.Helper()

	widest, at := widestLine(lines)
	if at < height {
		t.Fatalf("the widest line is line %d of a %d-line window, so it is on the screen "+
			"from the start and scrolling to it proves nothing", at, height)
	}
	if widest > width {
		t.Fatalf("the widest line is %d cells in a %d-cell region, so it is clamped at "+
			"every offset and this proves nothing", widest, width)
	}
	return widest
}
