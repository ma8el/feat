package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/notify"
)

// fakeNotifier records what would have reached the desktop.
//
// It exists so that the suite never shows a real notification, and so that a
// test can assert the absence of one — which is most of what this slice's
// acceptance criteria are about.
type fakeNotifier struct {
	mu        sync.Mutex
	delivered []notify.Notification
	available bool
	err       error
}

func newFakeNotifier() *fakeNotifier { return &fakeNotifier{available: true} }

func (f *fakeNotifier) Notify(_ context.Context, notification notify.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.delivered = append(f.delivered, notification)
	return nil
}

func (f *fakeNotifier) Available() (bool, string) {
	if f.available {
		return true, ""
	}
	return false, "this fake delivers nothing"
}

func (f *fakeNotifier) sent() []notify.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notify.Notification(nil), f.delivered...)
}

// watchable is the session with the daemon caught up, so that changes from here
// on are news rather than history.
func (s *session) watchable() *session {
	s.service.notifiable.Store(true)
	return s
}

// TestIdleNotificationsDoNotFireImmediately is the third slice 10 acceptance
// criterion, in its first half.
//
// Two grace periods pass before the user is interrupted, and they are measured
// from different moments: the provider's own decides when an ended turn becomes
// idle, and the notification's decides how long a task must have *been* idle
// before it is worth saying. The test walks both, and requires that nothing was
// delivered until the second one expired.
func TestIdleNotificationsDoNotFireImmediately(t *testing.T) {
	live := launch(t, hostFixture, installed(), true).watchable()
	live.start(t)

	live.hook(t, "Stop", `{"session_id":"claude-session-1","stop_hook_active":false}`)
	if delivered := live.notifier.sent(); len(delivered) != 0 {
		t.Fatalf("an ended turn notified at once: %+v", delivered)
	}

	// The provider's grace expires: the task is idle and the dashboard says so.
	live.timer.fire()
	task := live.load(t)
	if task.Session.Process != domain.ProcessIdle {
		t.Fatalf("the session is %s, want idle", task.Session.Process)
	}
	if delivered := live.notifier.sent(); len(delivered) != 0 {
		t.Fatalf("becoming idle notified at once: %+v", delivered)
	}

	// The notification grace expires: now it is worth saying.
	live.timer.fire()
	delivered := live.notifier.sent()
	if len(delivered) != 1 {
		t.Fatalf("delivered %d notifications after the notification grace, want 1: %+v",
			len(delivered), delivered)
	}
	if !strings.Contains(delivered[0].Body, "idle") {
		t.Errorf("the notification does not say the agent is idle: %+v", delivered[0])
	}
	if !strings.Contains(delivered[0].Title, task.Key().String()) {
		t.Errorf("the notification does not name the task %s: %+v", task.Key(), delivered[0])
	}
}

// TestIdleNotificationsDoNotFireWhileAttached is the second half of the same
// criterion.
//
// The question is asked of tmux and per window, because a user attached to a
// project's session is looking at one of its tasks and not at the others. The
// fake reports a client watching this task's window, and nothing is delivered.
func TestIdleNotificationsDoNotFireWhileAttached(t *testing.T) {
	live := launch(t, hostFixture, installed(), true).watchable()
	live.start(t)

	live.watch(t, 1)
	live.hook(t, "Stop", `{"session_id":"claude-session-1","stop_hook_active":false}`)
	live.timer.fire()
	live.timer.fire()

	if delivered := live.notifier.sent(); len(delivered) != 0 {
		t.Fatalf("a task the user is watching was notified about: %+v", delivered)
	}

	// The same task, no longer watched, does notify. Without this the test would
	// pass against a build that never notifies about anything.
	live.watch(t, 0)
	live.hook(t, "UserPromptSubmit", `{"session_id":"claude-session-1","prompt":"go on"}`)
	live.hook(t, "Stop", `{"session_id":"claude-session-1","stop_hook_active":false}`)
	live.timer.fire()
	live.timer.fire()

	if delivered := live.notifier.sent(); len(delivered) != 1 {
		t.Fatalf("delivered %d notifications once nobody was watching, want 1: %+v",
			len(delivered), delivered)
	}
}

// TestAnAgentThatStartsTalkingAgainIsNotNotifiedAbout checks that the pending
// notification is dropped by activity.
//
// It is the case the grace period exists for: a turn that ends and immediately
// continues is not a session waiting for anybody, and a notification armed
// before that happened would arrive about a state that is over.
func TestAnAgentThatStartsTalkingAgainIsNotNotifiedAbout(t *testing.T) {
	live := launch(t, hostFixture, installed(), true).watchable()
	live.start(t)

	live.hook(t, "Stop", `{"session_id":"claude-session-1","stop_hook_active":false}`)
	live.timer.fire()
	live.hook(t, "UserPromptSubmit", `{"session_id":"claude-session-1","prompt":"carry on"}`)
	live.timer.fire()

	if delivered := live.notifier.sent(); len(delivered) != 0 {
		t.Fatalf("a session that carried on was notified about: %+v", delivered)
	}
}

// TestAReviewRequestNotifiesWithoutExposingTaskContent is the fourth slice 10
// acceptance criterion.
//
// The task's brief carries a marker no notification may contain. The check is
// worth more than it looks: the notification is composed from a Subject that has
// no field a brief, an agent's words, a path, or a configured value could reach,
// so this passes because there is no way to fail it rather than because a filter
// caught something.
func TestAReviewRequestNotifiesWithoutExposingTaskContent(t *testing.T) {
	live := launch(t, hostFixture, installed(), true).watchable()
	live.start(t)

	const secret = "hunter2-should-never-be-notified"
	live.setBrief(t, "Add a rate limit. The staging password is "+secret+".")

	live.emit(t, control.TypeReviewRequested, `{"summary":"`+secret+`"}`)

	delivered := live.notifier.sent()
	if len(delivered) != 1 {
		t.Fatalf("delivered %d notifications for a review request, want 1: %+v", len(delivered), delivered)
	}

	task := live.load(t)
	text := delivered[0].Title + " " + delivered[0].Body
	if strings.Contains(text, secret) {
		t.Errorf("the notification carries task content: %q", text)
	}
	if !strings.Contains(text, task.Key().String()) {
		t.Errorf("the notification does not identify the task %s: %q", task.Key(), text)
	}
	if !strings.Contains(text, "review") {
		t.Errorf("the notification does not say what happened: %q", text)
	}
}

// TestAFailedSessionIsNotifiedAboutOnce checks that one death produces one
// notification.
//
// A session that fails moves both the process and the workflow, and each is
// recorded as its own event. A user told twice about one failure learns to read
// the second one as noise, so the workflow wins and the process is reported only
// when the workflow stayed where it was.
func TestAFailedSessionIsNotifiedAboutOnce(t *testing.T) {
	live := launch(t, hostFixture, installed(), true).watchable()
	live.start(t)

	live.hook(t, "StopFailure", `{"session_id":"claude-session-1","error":"the process exited with status 1"}`)

	delivered := live.notifier.sent()
	if len(delivered) != 1 {
		t.Fatalf("delivered %d notifications for one failed session, want 1: %+v",
			len(delivered), delivered)
	}
	if !strings.Contains(delivered[0].Body, "failed") {
		t.Errorf("the notification does not say the task failed: %+v", delivered[0])
	}
}

// TestNotificationsAreRecordedAsTaskEvents checks the durable trace.
//
// A desktop notification is gone the moment it is dismissed. Recording one keeps
// the answer to "what did Feat interrupt me about", and gives slice 13 the
// figures it has to measure false idle notifications from.
func TestNotificationsAreRecordedAsTaskEvents(t *testing.T) {
	live := launch(t, hostFixture, installed(), true).watchable()
	live.start(t)
	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	log, err := live.service.store.Events().Replay(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("reading the task's events: %v", err)
	}

	var recorded int
	for _, event := range log.Events {
		if event.Type == domain.EventNotificationSent {
			recorded++
			if event.To != string(notify.ConditionReviewRequested) {
				t.Errorf("the recorded notification is %q, want %q",
					event.To, notify.ConditionReviewRequested)
			}
		}
	}
	if recorded != 1 {
		t.Fatalf("recorded %d notification events, want 1", recorded)
	}
}

// TestStartupCatchUpDoesNotNotify checks that a restart reports the present
// rather than the past.
//
// The daemon applies the control messages that arrived while it was stopped
// before it serves anything. Without this, restarting Feat in the morning would
// announce every turn that ended overnight.
func TestStartupCatchUpDoesNotNotify(t *testing.T) {
	live := launch(t, hostFixture, installed(), true)
	// Deliberately not made notifiable: this is the state the daemon is in while
	// it catches up, before Serve declares itself ready.
	live.start(t)
	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	if delivered := live.notifier.sent(); len(delivered) != 0 {
		t.Fatalf("catching up after a restart interrupted the user: %+v", delivered)
	}

	task := live.load(t)
	if task.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("the task is %s, want review_requested: catching up must still record what happened",
			task.Workflow)
	}
}

// TestADisabledDesktopIsNotNotifiedTo checks that the project's own setting is
// read.
func TestADisabledDesktopIsNotNotifiedTo(t *testing.T) {
	fixture := hostFixture + `
notifications:
  desktop: false
`
	live := launch(t, fixture, installed(), true).watchable()
	live.start(t)
	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	if delivered := live.notifier.sent(); len(delivered) != 0 {
		t.Fatalf("a project that turned desktop notifications off was notified to: %+v", delivered)
	}
}

// TestSuppressionCanBeTurnedOff checks the other half of the same setting: a
// user who wants to be told even while watching is told.
func TestSuppressionCanBeTurnedOff(t *testing.T) {
	fixture := hostFixture + `
notifications:
  suppress_while_attached: false
`
	live := launch(t, fixture, installed(), true).watchable()
	live.start(t)
	live.watch(t, 1)
	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	if delivered := live.notifier.sent(); len(delivered) != 1 {
		t.Fatalf("delivered %d notifications with suppression off, want 1: %+v",
			len(delivered), delivered)
	}
}

// watch arranges how many clients are looking at the task's tmux window.
func (s *session) watch(t *testing.T, viewers int) {
	t.Helper()

	task := s.load(t)
	if task.Session == nil {
		t.Fatalf("the task has no session to watch")
	}
	s.tmux.Watch(task.Session.Tmux.Window, viewers)
}

// load returns the task as it is now recorded.
func (s *session) load(t *testing.T) *domain.Task {
	t.Helper()

	task, err := s.service.store.Tasks().Load(context.Background(), s.ref)
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}
	return task
}

// setBrief rewrites the launched task's brief.
//
// A task's brief is frozen once it leaves draft, which is the point: this writes
// the stored document directly, so that the test can put something in it that no
// notification may ever carry.
func (s *session) setBrief(t *testing.T, brief string) {
	t.Helper()

	task := s.load(t)
	task.Brief = brief
	if err := s.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("rewriting the brief: %v", err)
	}
}
