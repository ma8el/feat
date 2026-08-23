package domain

import (
	"errors"
	"strings"
	"testing"
)

// The identifiers this file resolves between. The first two share the prefix
// "7f", which is what makes ambiguity testable without contriving a collision;
// the third shares nothing with either.
const (
	firstTask  = TaskID("7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c")
	secondTask = TaskID("7fb90cd4-1e2f-4a3b-8c4d-5e6f7a8b9c0d")
	thirdTask  = TaskID("a10b2c3d-4e5f-4061-9273-8495a6b7c8d9")
)

// TestATaskIsNamedByWhatAUserCanSee is ADR-038's rule that a task can be named
// by what a user can see.
//
// Every list prints the eight-character key and nothing else, so the key has to
// name a task. The whole identifier keeps working because the dashboard's task
// detail prints that, and a prefix works because there is no reason for it not
// to once the key does.
func TestATaskIsNamedByWhatAUserCanSee(t *testing.T) {
	tasks := []*Task{taskWithID(t, firstTask), taskWithID(t, thirdTask)}

	for _, ref := range []TaskRef{
		"7f3a1c2e",                             // the key every list prints
		"7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c", // the identifier it abbreviates
		"7f3a",                                 // a prefix of it
		"7",                                    // the shortest thing that still names one task
		"7f3a1c2e-5b6d",                        // a prefix that reaches past a hyphen
		"7F3A1C2E",                             // pasted from somewhere that upper-cased it
	} {
		task, found, err := ResolveTask(ref, tasks)
		if err != nil {
			t.Errorf("%q: %v", ref, err)
			continue
		}
		if !found {
			t.Errorf("%q names no task", ref)
			continue
		}
		if task.ID != firstTask {
			t.Errorf("%q names %s, want %s", ref, task.ID, firstTask)
		}
	}
}

// TestAnAmbiguousReferenceIsReportedRatherThanResolved is the other half of that
// criterion: a name that matches two tasks is reported rather than resolved to
// either.
//
// The message has to be actable on, which means naming the candidates in the
// terms the lists print them in. A user who is told only that their reference
// was ambiguous has been told to guess again.
func TestAnAmbiguousReferenceIsReportedRatherThanResolved(t *testing.T) {
	tasks := []*Task{taskWithID(t, firstTask), taskWithID(t, secondTask), taskWithID(t, thirdTask)}

	task, found, err := ResolveTask("7f", tasks)
	if err == nil {
		t.Fatalf("%q resolved to %v (found=%v)", "7f", task, found)
	}
	if task != nil || found {
		t.Errorf("an ambiguous reference also returned a task: %v (found=%v)", task, found)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("the refusal does not match ErrInvalid: %v", err)
	}

	var ambiguous *AmbiguousTaskError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("the refusal is not an *AmbiguousTaskError: %v", err)
	}
	if ambiguous.Ref != "7f" {
		t.Errorf("the refusal reports the reference %q, want what the user typed", ambiguous.Ref)
	}

	want := []AmbiguousMatch{
		{Key: firstTask.Key(), Project: testProject},
		{Key: secondTask.Key(), Project: testProject},
	}
	if len(ambiguous.Matches) != len(want) {
		t.Fatalf("the refusal names %d tasks, want %d: %+v", len(ambiguous.Matches), len(want), ambiguous.Matches)
	}
	for i, match := range ambiguous.Matches {
		if match != want[i] {
			t.Errorf("match %d is %+v, want %+v", i, match, want[i])
		}
	}

	for _, expected := range []string{firstTask.Key().String(), secondTask.Key().String(), string(testProject)} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not name %q: %v", expected, err)
		}
	}
}

// TestAmbiguityIsReportedInAStableOrder keeps one message from depending on the
// order storage happened to read projects in. A user comparing two runs should
// not have to work out whether anything changed.
func TestAmbiguityIsReportedInAStableOrder(t *testing.T) {
	forward := []*Task{taskWithID(t, firstTask), taskWithID(t, secondTask)}
	backward := []*Task{taskWithID(t, secondTask), taskWithID(t, firstTask)}

	_, _, first := ResolveTask("7f", forward)
	_, _, second := ResolveTask("7f", backward)

	if first == nil || second == nil {
		t.Fatalf("one order was not ambiguous: %v / %v", first, second)
	}
	if first.Error() != second.Error() {
		t.Errorf("the message depends on the order tasks were read in:\n%v\n%v", first, second)
	}
}

// TestAnArchivedTaskIsStillAddressable checks that a task nothing lists any more
// can still be named.
//
// A cancelled draft becomes archived and `feat task cleanup` still has to reach one,
// so archived tasks are candidates. The consequence is that one can make a
// reference ambiguous, which is reported rather than resolved by preferring the
// live task: preferring one would be the guess this whole rule refuses.
func TestAnArchivedTaskIsStillAddressable(t *testing.T) {
	archived := taskWithID(t, firstTask)
	archived.Workflow = WorkflowArchived

	task, found, err := ResolveTask("7f3a1c2e", []*Task{archived})
	if err != nil || !found {
		t.Fatalf("an archived task could not be named: found=%v err=%v", found, err)
	}
	if task.ID != firstTask {
		t.Errorf("resolved %s, want the archived %s", task.ID, firstTask)
	}

	live := taskWithID(t, secondTask)
	if _, _, err := ResolveTask("7f", []*Task{archived, live}); err == nil {
		t.Error("an archived task was silently passed over to resolve a live one")
	}
}

// TestAReferenceThatNamesNothingIsNotAnError checks the distinction the resolver
// draws for its caller.
//
// What to say about a task that is not there depends on where the question came
// from, and this package knows nothing about commands. Ambiguity is an error
// because that answer is the same wherever it was asked.
func TestAReferenceThatNamesNothingIsNotAnError(t *testing.T) {
	task, found, err := ResolveTask("deadbeef", []*Task{taskWithID(t, firstTask)})

	if err != nil {
		t.Errorf("a reference that names nothing produced an error: %v", err)
	}
	if found || task != nil {
		t.Errorf("a reference that names nothing resolved to %v (found=%v)", task, found)
	}
}

// TestAReferenceIsAPrefixOfAnIdentifierAndNothingElse checks the shape rule.
//
// A reference is deliberately not a search: it is a prefix of a task identifier,
// so no title, branch, or path a user typed can become a way of addressing a
// task, and nothing from a request can traverse a directory.
func TestAReferenceIsAPrefixOfAnIdentifierAndNothingElse(t *testing.T) {
	valid := []TaskRef{
		"7",
		"7f3a1c2e",
		"7f3a1c2e-",
		"7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c",
		"7F3A1C2E-5B6D-4A80-9C1F-2D3E4F5A6B7C",
	}
	for _, ref := range valid {
		if err := ref.Validate(); err != nil {
			t.Errorf("%q is rejected: %v", ref, err)
		}
	}

	invalid := []TaskRef{
		"",
		".",
		"..",
		"../7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c",
		"7f3a1c2e/",
		"7f3a1c2e ",
		"not-a-uuid",
		"7f3a1c2e5b6d",                          // no hyphen where the identifier has one
		"7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c0", // longer than an identifier
		"café",
		"7f3a1c2e\n",
	}
	for _, ref := range invalid {
		err := ref.Validate()
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%q is accepted as a task reference", ref)
			continue
		}
		var validation *ValidationError
		if errors.As(err, &validation) && !strings.Contains(validation.Field, string(ref)) {
			t.Errorf("%q: the refusal does not repeat what was typed: %q", ref, validation.Field)
		}
	}
}

// TestResolvingRefusesAMalformedReferenceBeforeReadingAnything checks that the
// shape rule runs first. A reference that could never name a task should not
// decide anything by matching nothing.
func TestResolvingRefusesAMalformedReferenceBeforeReadingAnything(t *testing.T) {
	_, found, err := ResolveTask("../escape", []*Task{taskWithID(t, firstTask)})

	if !errors.Is(err, ErrInvalid) {
		t.Errorf("a malformed reference was resolved rather than refused: found=%v err=%v", found, err)
	}
}

// TestAWholeIdentifierNeedsNoResolution checks the path the dashboard takes.
//
// It holds identifiers already, so every request it makes would otherwise read
// every task to learn what it knew before it asked.
func TestAWholeIdentifierNeedsNoResolution(t *testing.T) {
	id, exact := TaskRef(firstTask).Exact()
	if !exact {
		t.Fatalf("%q is not recognised as a whole identifier", firstTask)
	}
	if id != firstTask {
		t.Errorf("Exact returned %q, want %q", id, firstTask)
	}

	if upper, exact := TaskRef(strings.ToUpper(string(firstTask))).Exact(); !exact || upper != firstTask {
		t.Errorf("an upper-cased identifier is %q (exact=%v), want %q", upper, exact, firstTask)
	}

	for _, ref := range []TaskRef{
		"",
		"7f3a1c2e",
		"7f3a1c2e-5b6d-1a80-9c1f-2d3e4f5a6b7c", // version 1, so not one Feat generated
		"7f3a1c2e-5b6d-4a80-1c1f-2d3e4f5a6b7c", // wrong variant
		"../../../etc/passwd-4a80-9c1f-2d3e4f",
	} {
		if id, exact := ref.Exact(); exact {
			t.Errorf("%q is treated as a whole identifier: %q", ref, id)
		}
	}
}

// taskWithID builds a task with a chosen identifier, so that a test can arrange
// the collisions resolution is about.
func taskWithID(t *testing.T, id TaskID) *Task {
	t.Helper()

	task, err := NewTask(id, testProject, "a task", TaskSource{Kind: SourcePrompt}, origin)
	if err != nil {
		t.Fatalf("creating a task with the identifier %s: %v", id, err)
	}
	return task
}
