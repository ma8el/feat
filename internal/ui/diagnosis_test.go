package ui

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// diagnosisReport is a machine with one thing wrong with it, which is the
// report worth rendering: an error with an action, a warning, a skipped check,
// and a pass.
func diagnosisReport() api.Diagnosis {
	return api.Diagnosis{
		Environment: "this terminal",
		Projects: []api.ProjectDiagnosis{{
			ID: "example", File: "/config/example.yaml",
			Findings: []api.Finding{
				{Check: "repositories.core", Severity: api.SeverityOK, Summary: "a Git repository"},
				{
					Check: "agent.execution.service", Severity: api.SeverityError,
					Summary: "no service dev in the Compose files",
					Action:  "name a service the Compose files define",
				},
				{
					Check: "capabilities.github_cli", Severity: api.SeverityWarning,
					Summary: "gh is not authenticated",
					Action:  "run gh auth login in the agent environment",
				},
				{
					Check: "runtime.services", Severity: api.SeveritySkipped,
					Summary: "no daemon is running, so registration was not read",
				},
			},
		}},
		Host: []api.Finding{{Check: "git", Severity: api.SeverityOK, Summary: "git version 2.52.0"}},
	}
}

// TestDiagnosisOpensForTheSelectedTasksProject checks the standalone screen: the
// checks `feat doctor` runs, read where the tasks they are about are (ADR-063).
func TestDiagnosisOpensForTheSelectedTasksProject(t *testing.T) {
	backend := newFakeBackend()
	backend.diagnosis = diagnosisReport()

	model := press(t, sized(dashboard(backend, liveTask()), 120, 32), "D")

	if model.screen != screenDiagnosis {
		t.Fatalf("D left the screen at %v, want the diagnosis", model.screen)
	}
	// The subject is the project rather than the task: a Compose service either
	// exists for every task in a project or for none of them.
	if len(backend.diagnosed) != 1 || backend.diagnosed[0] != liveTask().ProjectID {
		t.Fatalf("the checks ran for %v, want the selected task's project", backend.diagnosed)
	}

	view := content(model)
	for _, want := range []string{
		"error", "agent.execution.service", "name a service the Compose files define",
		"warning", "gh is not authenticated",
		// A skipped check is not a pass, and says so where it is counted.
		"skipped", "skipped checks are not passing checks",
		"git version 2.52.0",
		"1 error, 1 warning, 1 skipped check, 2 checks passed",
		"checked from this terminal",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the diagnosis does not show %q:\n%s", want, view)
		}
	}
}

// TestTheReportOpensAtTheFirstProblem checks where a report that does not fit
// is opened.
//
// Most of a report is passes — seventeen of them on an ordinary project — and
// the pane holds a dozen lines. Opening at the top would mean opening on the
// checks that are fine, with the one that is not below the fold, which is the
// screen deciding not to say the thing it exists to say.
func TestTheReportOpensAtTheFirstProblem(t *testing.T) {
	backend := newFakeBackend()
	report := api.Diagnosis{Environment: "this terminal"}
	passing := make([]api.Finding, 0, 20)
	for i := range 20 {
		passing = append(passing, api.Finding{
			Check:    "repositories.core.check" + strconv.Itoa(i),
			Severity: api.SeverityOK, Summary: "a check that passed",
		})
	}
	report.Projects = []api.ProjectDiagnosis{
		{ID: "quiet", Findings: passing},
		{ID: "example", Findings: []api.Finding{{
			Check: "agent.executable", Severity: api.SeverityError,
			Summary: "claude is not installed", Action: "install Claude Code",
		}}},
	}
	backend.diagnosis = report

	view := content(press(t, sized(dashboard(backend, liveTask()), 120, 32), "D"))

	if !strings.Contains(view, "claude is not installed") {
		t.Errorf("the failing check is not on the screen it opened on:\n%s", view)
	}
	// With the heading it belongs to, because a finding on its own says what is
	// wrong without saying what it is wrong with.
	if !strings.Contains(view, "project example") {
		t.Errorf("the finding arrived without its project:\n%s", view)
	}
	// And nothing is hidden: what is above the window is counted.
	if !strings.Contains(view, "lines above") {
		t.Errorf("the report does not say that it starts above the window:\n%s", view)
	}
}

// TestTheDiagnosisFitsItsDialog pins the measurement the screen depends on.
//
// A report is longer than any dialog, so it scrolls; what must not scroll is
// everything around it. The box clamps from the bottom, so a body one line too
// tall costs the user the counts and the key that runs the checks again.
func TestTheDiagnosisFitsItsDialog(t *testing.T) {
	backend := newFakeBackend()
	report := diagnosisReport()
	for i := range 30 {
		report.Projects[0].Findings = append(report.Projects[0].Findings, api.Finding{
			Check:    "repositories.core.check" + strconv.Itoa(i),
			Severity: api.SeverityOK,
			Summary:  "a check that passed",
		})
	}
	backend.diagnosis = report

	model := press(t, sized(dashboard(backend, liveTask()), 120, 32), "D")

	drawn := model.View()
	if strings.Contains(drawn, "than fit here") {
		t.Errorf("the diagnosis overruns the dialog:\n%s", drawn)
	}
	for _, want := range []string{"checks passed", "check again", "checked from this terminal"} {
		if !strings.Contains(drawn, want) {
			t.Errorf("the dialog lost %q from the bottom:\n%s", want, drawn)
		}
	}
}

// TestDiagnosisRunsOnlyWhenAsked is the reason it is a key rather than a poll:
// the checks shell out to Git, Compose, and the container runtime.
func TestDiagnosisRunsOnlyWhenAsked(t *testing.T) {
	backend := newFakeBackend()
	model := sized(dashboard(backend, liveTask()), 120, 32)

	// Everything a dashboard does on its own: opening a task, the periodic
	// refresh, and a state read arriving.
	model = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := model.Update(tickMsg(dashboardNow))
	updated, _ = updated.(Model).Update(tasksMsg{tasks: []api.Task{liveTask()}})
	_ = updated.(Model)

	if len(backend.diagnosed) != 0 {
		t.Fatalf("the checks ran without being asked for: %v", backend.diagnosed)
	}

	// And looking again is one key, which is what the screen is for.
	opened := press(t, sized(dashboard(backend, liveTask()), 120, 32), "D")
	again := press(t, opened, "r")
	if len(backend.diagnosed) != 2 {
		t.Errorf("r ran the checks %d times, want a second pass", len(backend.diagnosed))
	}
	if closed := press(t, again, "esc"); closed.screen == screenDiagnosis {
		t.Error("esc left the diagnosis open")
	}
}

// TestADiagnosisThatCannotRunSaysSo checks the failure the screen itself has.
func TestADiagnosisThatCannotRunSaysSo(t *testing.T) {
	backend := newFakeBackend()
	backend.diagnoseErr = errors.New("the configuration directory could not be read")

	model := press(t, sized(dashboard(backend, liveTask()), 120, 32), "D")

	if view := content(model); !strings.Contains(view, "could not be read") {
		t.Errorf("the screen does not say why there are no findings:\n%s", view)
	}
}
