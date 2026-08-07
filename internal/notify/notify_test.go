package notify

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// TestTheNotifiableStatesArePinned holds the policy to the conditions
// docs/06-technical-architecture.md lists.
//
// It is a table in the shape ADR-026 used for the workflow transitions and
// ADR-032 for agent events: what Feat will interrupt somebody for is a product
// decision, so changing one has to mean editing the table that documents it
// rather than a condition buried in a call site.
func TestTheNotifiableStatesArePinned(t *testing.T) {
	workflow := map[domain.WorkflowState]Condition{
		domain.WorkflowReviewRequested:    ConditionReviewRequested,
		domain.WorkflowReadyForReview:     ConditionReadyForReview,
		domain.WorkflowVerificationFailed: ConditionVerificationFailed,
		domain.WorkflowFailed:             ConditionTaskFailed,
	}
	for state, want := range workflow {
		got, ok := ForWorkflow(state)
		if !ok || got != want {
			t.Errorf("ForWorkflow(%s) = %q, %v; want %q, true", state, got, ok, want)
		}
	}

	// The states nobody is interrupted for, and the reason each is absent.
	for state, why := range map[domain.WorkflowState]string{
		domain.WorkflowDraft:            "a draft is something the user is holding",
		domain.WorkflowPreparing:        "the user just launched it",
		domain.WorkflowWorking:          "the agent working is what was asked for",
		domain.WorkflowVerifying:        "checks running is not a result",
		domain.WorkflowChangesRequested: "the user asked for the changes",
		domain.WorkflowApproved:         "the user approved it",
		domain.WorkflowArchived:         "the user cleaned it up",
	} {
		if condition, ok := ForWorkflow(state); ok {
			t.Errorf("ForWorkflow(%s) notifies with %q, but %s", state, condition, why)
		}
	}
}

// TestAnEndOfTurnIsNotNotifiable is the absence that matters most.
//
// Idle is not a state a task arrives in — it is a state it stays in, and how
// long it has stayed is what decides whether it is worth interrupting somebody.
// A process state that mapped to the idle condition would notify the moment the
// provider's grace expired, which is what the third acceptance criterion of this
// slice forbids.
func TestAnEndOfTurnIsNotNotifiable(t *testing.T) {
	for _, state := range []domain.ProcessState{
		domain.ProcessStarting, domain.ProcessRunning, domain.ProcessIdle, domain.ProcessStopped,
	} {
		if condition, ok := ForProcess(state); ok {
			t.Errorf("ForProcess(%s) notifies with %q; only a failure is worth interrupting for, "+
				"and idle waits for its own grace period", state, condition)
		}
	}
	if condition, ok := ForProcess(domain.ProcessFailed); !ok || condition != ConditionSessionFailed {
		t.Errorf("ForProcess(failed) = %q, %v; want %q, true", condition, ok, ConditionSessionFailed)
	}
}

// TestOnlyAFailedRuntimeIsNotifiable checks that a stop the user asked for is
// not reported back to them as news (FR-RUN-005).
func TestOnlyAFailedRuntimeIsNotifiable(t *testing.T) {
	for _, state := range []domain.RuntimeState{
		domain.RuntimeAbsent, domain.RuntimeCreating, domain.RuntimeStopped, domain.RuntimeStarting,
		domain.RuntimeRunning, domain.RuntimeDegraded, domain.RuntimeRemoving,
	} {
		if condition, ok := ForRuntime(state); ok {
			t.Errorf("ForRuntime(%s) notifies with %q, but only a failure is news", state, condition)
		}
	}
	if condition, ok := ForRuntime(domain.RuntimeFailed); !ok || condition != ConditionRuntimeFailed {
		t.Errorf("ForRuntime(failed) = %q, %v; want %q, true", condition, ok, ConditionRuntimeFailed)
	}
}

// TestASubjectCarriesIdentificationAndNothingElse is how the fourth acceptance
// criterion is kept rather than remembered.
//
// A notification leaves the daemon and lands somewhere the user did not choose,
// so the way to keep task content out of it is to have no way to put it in.
// Compose reads a Subject and a duration, and this pins what a Subject may hold:
// adding a field for the brief, an agent's summary, or a repository path would
// fail here, where the reason is written down.
func TestASubjectCarriesIdentificationAndNothingElse(t *testing.T) {
	want := []string{"Key", "Title", "Project"}

	subject := reflect.TypeOf(Subject{})
	got := make([]string, 0, subject.NumField())
	for i := range subject.NumField() {
		got = append(got, subject.Field(i).Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a notification subject carries %v, want %v.\n"+
			"A field here is a field a notification can carry off this machine.", got, want)
	}
}

// TestEveryConditionComposesAMessage checks that each notifiable condition says
// what happened and which task it happened to.
func TestEveryConditionComposesAMessage(t *testing.T) {
	subject := Subject{Key: "7f3a1c2e", Title: "Add a scheduled export job", Project: "example"}

	for condition, want := range map[Condition]string{
		ConditionIdle:               "idle",
		ConditionReviewRequested:    "review",
		ConditionReadyForReview:     "ready",
		ConditionVerificationFailed: "checks",
		ConditionTaskFailed:         "failed",
		ConditionSessionFailed:      "session",
		ConditionRuntimeFailed:      "services",
	} {
		notification, ok := Compose(condition, subject, 12*time.Second)
		if !ok {
			t.Fatalf("Compose(%s) reports no notification", condition)
		}
		if !strings.Contains(notification.Body, want) {
			t.Errorf("Compose(%s) body %q does not contain %q", condition, notification.Body, want)
		}
		if !strings.Contains(notification.Title, subject.Key) {
			t.Errorf("Compose(%s) title %q does not name the task", condition, notification.Title)
		}
		if !strings.Contains(notification.Body, subject.Title) {
			t.Errorf("Compose(%s) body %q does not name the task", condition, notification.Body)
		}
	}

	if _, ok := Compose("something_else", subject, 0); ok {
		t.Error("Compose reports a notification for a condition this build does not model")
	}
}

// TestAnIdleNotificationSaysHowLong checks the one measurement that reaches the
// text, and that it is rounded the way somebody would say it out loud.
func TestAnIdleNotificationSaysHowLong(t *testing.T) {
	subject := Subject{Key: "7f3a1c2e", Title: "Add a rate limit"}

	for idle, want := range map[time.Duration]string{
		5 * time.Second:                       "5s",
		90*time.Second + 400*time.Millisecond: "2m",
		2*time.Hour + 3*time.Minute:           "2h",
	} {
		notification, ok := Compose(ConditionIdle, subject, idle)
		if !ok {
			t.Fatal("Compose reports no idle notification")
		}
		if !strings.Contains(notification.Body, want) {
			t.Errorf("an idle notification after %s says %q, want it to contain %q",
				idle, notification.Body, want)
		}
	}
}

// TestALongTitleIsShortened keeps the part that says what happened on the line.
func TestALongTitleIsShortened(t *testing.T) {
	long := strings.Repeat("a very long task title ", 20)
	notification, ok := Compose(ConditionReviewRequested, Subject{Key: "7f3a1c2e", Title: long}, 0)
	if !ok {
		t.Fatal("Compose reports no notification")
	}
	if len([]rune(notification.Body)) > maxTitle+60 {
		t.Errorf("a notification body is %d runes long: %q", len([]rune(notification.Body)), notification.Body)
	}
	if !strings.Contains(notification.Body, "…") {
		t.Errorf("a shortened title is not marked as shortened: %q", notification.Body)
	}
}

// TestATitleWithANewlineStaysOneLine checks that a task's title, which is text
// the user typed, cannot break the notification it appears in.
func TestATitleWithANewlineStaysOneLine(t *testing.T) {
	notification, ok := Compose(ConditionIdle, Subject{
		Key: "7f3a1c2e", Title: "Add a rate limit\nand a second line\r\nand a third",
	}, time.Second)
	if !ok {
		t.Fatal("Compose reports no notification")
	}
	if strings.ContainsAny(notification.Body, "\n\r") || strings.ContainsAny(notification.Title, "\n\r") {
		t.Errorf("a notification carries a newline: %q / %q", notification.Title, notification.Body)
	}
}

// TestTheDefaultPolicyNotifies checks what a project that configures nothing
// gets: notifications on, suppressed while watching, and a grace that is not
// zero.
func TestTheDefaultPolicyNotifies(t *testing.T) {
	policy := DefaultPolicy()
	if !policy.Desktop || !policy.SuppressWhileAttached {
		t.Errorf("the default policy is %+v, want desktop notifications suppressed while attached", policy)
	}
	if policy.Grace() <= 0 {
		t.Errorf("the default idle grace is %s, and a zero grace would notify immediately", policy.Grace())
	}
	if (Policy{}).Grace() != DefaultIdleGrace {
		t.Errorf("an unset grace is %s, want the default %s", (Policy{}).Grace(), DefaultIdleGrace)
	}
}
