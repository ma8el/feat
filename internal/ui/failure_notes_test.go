package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// refusedTask is the task the daemon's refusals below are about.
func refusedTask() api.Task {
	task := liveTask()
	task.ID, task.Key = "0fcda894-c14d-4112-a747-1776bda75115", "0fcda894"
	return task
}

// TestARefusalReadsAsANoteRatherThanAnErrorLog is what the two screens that draw
// one were doing wrong.
//
// Pressing V on a task whose agent has not asked for review, and opening the
// runtime of a project that configures none, are both answered by the daemon
// refusing. Neither is a fault, and both were drawn as a red line beginning
// "invalid request:" and carrying the full task identifier — which is a log
// entry rather than something to read.
func TestARefusalReadsAsANoteRatherThanAnErrorLog(t *testing.T) {
	task := refusedTask()
	refused := fmt.Errorf("%w: checks can only run for a task whose agent has asked for review, and task %s is %s",
		api.ErrInvalid, task.ID, task.Workflow)

	note := daemonNote(refused, task, 0)
	plain := ansi.Strip(note)

	if strings.Contains(plain, api.ErrInvalid.Error()) {
		t.Errorf("the wire's classification is still on the line: %q", plain)
	}
	if strings.Contains(plain, task.ID) {
		t.Errorf("the full identifier is still on the line, and the header already names the task: %q", plain)
	}
	if !strings.Contains(plain, task.Key) {
		t.Errorf("the note does not say which task it is about: %q", plain)
	}
	// Labelled as a note, which is how both screens already draw something that
	// needs the user and is not a verdict against the work. The colour is not
	// asserted: lipgloss renders without one where there is no terminal, so every
	// style compares equal here and a test that checked would pass either way.
	// The label is the part that survives into a plain-text terminal anyway, which
	// is the same reason the panel names a draft rather than only colouring it.
	if !strings.HasPrefix(plain, "note ") {
		t.Errorf("a refusal is not drawn as a note: %q", plain)
	}
}

// TestAFailureIsStillDrawnAsOne keeps the distinction the note is for. Something
// that broke is not something the user chose to do from the wrong place.
func TestAFailureIsStillDrawnAsOne(t *testing.T) {
	task := refusedTask()
	broken := errors.New("the daemon could not complete the request")

	note := daemonNote(broken, task, 0)
	if !strings.Contains(note, failureStyle.Render(broken.Error())) {
		t.Errorf("a failure lost the failure colour: %q", note)
	}
	if strings.Contains(ansi.Strip(note), "note") {
		t.Errorf("a failure is drawn as a note: %q", note)
	}
}

// TestTheRuntimeRefusalKeepsThePathItSendsTheUserTo is the half that was cut off.
//
// The runtime body is the one tab that is not re-flowed before it is drawn, so a
// sentence longer than the region was truncated at the edge with an ellipsis —
// and this sentence ends in the configuration file to add a runtime section to,
// which is the only part of it that says what to do.
func TestTheRuntimeRefusalKeepsThePathItSendsTheUserTo(t *testing.T) {
	task := refusedTask()
	path := "/srv/config/feat/projects/example.yaml"
	refused := fmt.Errorf("%w: project %s configures no application runtime, so task %s has no "+
		"services to manage. Add a runtime section to %s",
		api.ErrInvalid, task.ProjectID, task.ID, path)

	backend := newFakeBackend()
	model := sized(dashboard(backend, task), 120, 32)
	model.selected = task.ID
	opened := press(t, model, "R")
	opened.runtime.err = refused

	body := ansi.Strip(opened.runtimeBody())
	if strings.Contains(body, "…") {
		t.Errorf("the refusal is cut rather than wrapped:\n%s", body)
	}
	if !strings.Contains(flowed(body), path) {
		t.Errorf("the refusal lost the path it sends the user to:\n%s", body)
	}
}
