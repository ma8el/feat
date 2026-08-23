package daemon

import (
	"strconv"
	"sync"
	"testing"
)

func TestRepeatsReportsAFailureOnceUntilItChanges(t *testing.T) {
	repeats := newRepeats()

	if !repeats.changed("task:a", "docker is not running") {
		t.Error("the first report of a failure was suppressed")
	}
	for range 100 {
		if repeats.changed("task:a", "docker is not running") {
			t.Fatal("a failure that has not changed was reported again")
		}
	}
	if !repeats.changed("task:a", "the workspace has gone") {
		t.Error("a different failure was suppressed")
	}
}

func TestRepeatsSeparatesSubjects(t *testing.T) {
	repeats := newRepeats()

	if !repeats.changed("task:a", "same") {
		t.Error("the first report for task a was suppressed")
	}
	if !repeats.changed("task:b", "same") {
		t.Error("task b was suppressed by task a's identical failure")
	}
}

// TestRepeatsReportsAFailureThatComesBack is why callers clear on success: a
// failure that recurs after a recovery is news, not a repeat.
func TestRepeatsReportsAFailureThatComesBack(t *testing.T) {
	repeats := newRepeats()

	if !repeats.changed("task:a", "docker is not running") {
		t.Fatal("the first report was suppressed")
	}
	repeats.clear("task:a")
	if !repeats.changed("task:a", "docker is not running") {
		t.Error("a failure that recurred after a recovery was suppressed")
	}
}

// TestRepeatsToleratesNoTracker keeps a service built without one — every test
// that constructs a service directly — from panicking in a poll loop.
func TestRepeatsToleratesNoTracker(t *testing.T) {
	var repeats *repeats

	if !repeats.changed("task:a", "anything") {
		t.Error("a nil tracker suppressed a report; it should suppress nothing")
	}
	repeats.clear("task:a")
}

func TestRepeatsIsSafeUnderConcurrentPolls(t *testing.T) {
	repeats := newRepeats()

	var group sync.WaitGroup
	for worker := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for round := range 200 {
				subject := "task:" + strconv.Itoa(worker)
				repeats.changed(subject, strconv.Itoa(round%3))
				if round%5 == 0 {
					repeats.clear(subject)
				}
			}
		}()
	}
	group.Wait()
}
