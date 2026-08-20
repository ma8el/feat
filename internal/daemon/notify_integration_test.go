package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/integrationtest"
	"github.com/ma8el/feat/internal/notify"
	"github.com/ma8el/feat/internal/review"
	"github.com/ma8el/feat/internal/store"
)

// deliveredHint is what to run when one of these did not appear.
//
// macOS decides per application whether a notification is shown, drops an
// unauthorised one without saying so, and exits 0 either way, so Feat can report
// that it handed one over and never that one was seen. This log is the only place
// the difference is written down.
const deliveredHint = "Compare these against the desktop. For any that did not appear:\n" +
	"  log show --last 5m --predicate 'process == \"usernoted\"' --style compact | grep ScriptEditor2\n" +
	"\"Presenting … as banner\" means macOS showed it and the question is where you were\n" +
	"looking; no line at all means it never arrived. A notification Feat dropped on\n" +
	"purpose says so in the daemon's log as \"not interrupting the user about a task\"."

// TestRealNotificationReachesTheDesktop is the slice 13 acceptance criterion that
// every notifiable condition has been shown to reach a real desktop once, from
// the state change that produces it.
//
// It exists because slices 10 and 11 each added notifications with unit tests
// over a fake notifier, and a fake notifier proves the daemon asked rather than
// that anybody was told. The defect that prompted it was a task passing its
// completion gate, reaching ready_for_review, and the user never hearing: the
// state, the event, and the review record were all correct, so what was missing
// was the interruption.
//
// Each subtest drives the real state change through the real service — a hook, a
// control message, a gate over the project's own checks, a runtime observation —
// with this platform's own notifier installed. What it asserts is that Feat
// handed one over and recorded that it had. Whether it was then shown is between
// the user and their desktop, which is what deliveredHint is for.
//
// Run it with -count=1. It exists for a side effect outside the process, and a
// cached result replays --- PASS without producing one, which looks exactly like
// a notification the platform swallowed (ADR-035 evidence 13).
func TestRealNotificationReachesTheDesktop(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to deliver real desktop notifications", integrationtest.Env)
	}
	if available, reason := notify.Host().Available(); !available {
		t.Skipf("this platform delivers no desktop notifications: %s", reason)
	}

	tests := []struct {
		condition notify.Condition
		deliver   func(t *testing.T) (*service, store.TaskRef)
	}{
		{
			// A turn ends, the provider's grace makes the task idle, and the
			// notification grace makes it worth saying. Both are walked.
			condition: notify.ConditionIdle,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				live := launch(t, hostFixture, installed(), true).delivering().watchable()
				live.start(t)
				live.hook(t, "Stop", `{"session_id":"claude-session-1","stop_hook_active":false}`)
				live.timer.fire()
				live.timer.fire()
				return live.service, live.ref
			},
		},
		{
			// A project with no completion gate: the request itself is what
			// reaches the user, because nothing else is going to.
			condition: notify.ConditionReviewRequested,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				live := launch(t, hostFixture, installed(), true).delivering().watchable()
				live.start(t)
				live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
				return live.service, live.ref
			},
		},
		{
			// The reported defect. A gate runs the project's configured checks,
			// they pass, and the task arrives with the user — which is the
			// interruption ADR-036 suppressed the earlier one in favour of.
			condition: notify.ConditionReadyForReview,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				live := launchForReview(t, nil)
				live.session.delivering().watchable()
				live.checks.outputs["unit"] = review.Output{Stdout: "84 passed"}
				live.checks.outputs["schema"] = review.Output{}

				live.requestReview(t, `{"summary":"ready"}`)
				return live.service, live.ref
			},
		},
		{
			condition: notify.ConditionVerificationFailed,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				live := launchForReview(t, nil)
				live.session.delivering().watchable()
				live.checks.outputs["unit"] = review.Output{Stdout: "2 failed, 82 passed", ExitCode: 1}
				live.checks.outputs["schema"] = review.Output{}

				live.requestReview(t, `{"summary":"ready"}`)
				return live.service, live.ref
			},
		},
		{
			// A check that never ran. It is the user's to fix and nobody else's,
			// which is the whole reason it is a condition of its own: the agent
			// cannot edit the configuration governing its own gate, so a
			// notification is the only thing that reaches somebody who can
			// (ADR-055).
			condition: notify.ConditionVerificationBlocked,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				live := launchForReview(t, nil)
				live.session.delivering().watchable()
				live.checks.errs["unit"] = errors.New("run-tests is not installed in the agent's environment")
				live.checks.outputs["schema"] = review.Output{}

				live.requestReview(t, `{"summary":"ready"}`)
				return live.service, live.ref
			},
		},
		{
			// An agent that dies while working takes the task with it.
			condition: notify.ConditionTaskFailed,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				live := launch(t, hostFixture, installed(), true).delivering().watchable()
				live.start(t)
				live.hook(t, "StopFailure",
					`{"session_id":"claude-session-1","error":"the process exited with status 1"}`)
				return live.service, live.ref
			},
		},
		{
			// The same death after the task had already asked for review: the
			// workflow stays where it was, so this is the case nothing else
			// reports. The review request is deliberately delivered before the
			// daemon is notifiable, so that only the session failure is news.
			condition: notify.ConditionSessionFailed,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				live := launch(t, hostFixture, installed(), true).delivering()
				live.start(t)
				live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
				live.watchable()
				live.hook(t, "StopFailure",
					`{"session_id":"claude-session-1","error":"the process exited with status 1"}`)
				return live.service, live.ref
			},
		},
		{
			// A task's application services failing, which is observed rather
			// than reported: nothing restarts them and nothing else says so.
			condition: notify.ConditionRuntimeFailed,
			deliver: func(t *testing.T) (*service, store.TaskRef) {
				arranged := arrangeConfigured(t, runtimeFixture)
				task := arranged.launched(t)
				arranged.answerFor(task, "running", "Up 2 seconds")
				arranged.act(t, task.ID, api.RuntimeStart)

				// Set here rather than through Options, because this harness
				// builds its daemon before a test can reach it. The notifier it
				// was given is already this platform's own.
				notifier := newFakeNotifier()
				notifier.deliver = notify.Host()
				arranged.service.notifier = notifier
				arranged.service.notifiable.Store(true)

				arranged.answerExitedFor(task, "exited", "Exited (1) 1 second ago", 1)
				arranged.service.pollRuntimes(context.Background())
				return arranged.service, store.TaskRef{Project: "app", Task: task.ID}
			},
		},
	}

	walked := make(map[notify.Condition]bool, len(tests))
	for _, test := range tests {
		walked[test.condition] = true

		t.Run(string(test.condition), func(t *testing.T) {
			service, ref := test.deliver(t)
			body, ok := deliveredNotification(t, service, ref, test.condition)
			if !ok {
				t.Fatalf("nothing was delivered for %s: the state change happened and the user was not told",
					test.condition)
			}
			t.Logf("delivered: %s", body)
		})
	}

	// A condition added later has to be walked too. A list extended by hand is a
	// list that eventually is not.
	for _, condition := range notify.Conditions() {
		if !walked[condition] {
			t.Errorf("%s reaches no real desktop in this suite: every notifiable condition needs one",
				condition)
		}
	}

	t.Log(deliveredHint)
}

// deliveredNotification reports what the task's own event log says Feat
// interrupted the user about.
//
// The event log rather than the notifier, because it is what the daemon writes
// after the platform accepted delivery: asserting on it checks the whole path
// from the state change to the hand-over, rather than that a test double was
// called.
func deliveredNotification(
	t *testing.T, service *service, ref store.TaskRef, condition notify.Condition,
) (string, bool) {
	t.Helper()

	log, err := service.store.Events().Replay(context.Background(), ref)
	if err != nil {
		t.Fatalf("reading the task's events: %v", err)
	}
	for _, event := range log.Events {
		if event.Type == domain.EventNotificationSent && event.To == string(condition) {
			return event.Detail, true
		}
	}
	return "", false
}
