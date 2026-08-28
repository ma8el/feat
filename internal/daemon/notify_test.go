package daemon

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/notify"
)

// fakeNotifier records what would have reached the desktop.
//
// It exists so that the suite never shows a real notification, and so that a
// test can assert the absence of one — which is most of what the notification
// rules are about.
//
// It can also be pointed at this platform's own notifier, which is how the
// opt-in walk in notify_integration_test.go reaches a real desktop while these
// assertions still see what was handed over. A fake notifier proves the daemon
// asked; it never proves anybody was told.
type fakeNotifier struct {
	mu        sync.Mutex
	delivered []notify.Notification
	available bool
	err       error
	// deliver is this platform's own notifier when a test asked for a real
	// delivery, and nil in every other test.
	deliver notify.Notifier
}

func newFakeNotifier() *fakeNotifier { return &fakeNotifier{available: true} }

func (f *fakeNotifier) Notify(ctx context.Context, notification notify.Notification) error {
	f.mu.Lock()
	fail, onwards := f.err, f.deliver
	f.mu.Unlock()

	if fail != nil {
		return fail
	}
	// Handed over before it is recorded, so that what the fake reports as sent is
	// what the platform accepted rather than what was attempted — the same order
	// notifyTask records its event in.
	if onwards != nil {
		if err := onwards.Notify(ctx, notification); err != nil {
			return err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, notification)
	return nil
}

func (f *fakeNotifier) Available() (bool, string) {
	f.mu.Lock()
	onwards := f.deliver
	f.mu.Unlock()

	if onwards != nil {
		// The real answer, because that is what decides whether the daemon drops
		// a notification before composing one.
		return onwards.Available()
	}
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

// delivering points the session's notifier at this platform's own, so that a
// notification it hands over reaches a real desktop.
//
// Only the opt-in walk calls it. Everything else keeps the fake, because a suite
// that showed a notification for every task it launched would be a suite nobody
// could run twice.
func (s *session) delivering() *session {
	s.notifier.mu.Lock()
	defer s.notifier.mu.Unlock()
	s.notifier.deliver = notify.Host()
	return s
}

// TestIdleNotificationsDoNotFireImmediately is the grace-period rule, in its
// first half.
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

// TestAReviewRequestNotifiesWithoutExposingTaskContent is the rule that a
// notification never carries task content.
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
// the answer to "what did Feat interrupt me about", and gives the figures that
// false idle notifications are measured from.
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

// disabledDesktop is the settings file of a user who does not want to be
// interrupted at all.
const disabledDesktop = `version: 1

notifications:
  desktop: false
`

// TestADisabledDesktopIsNotNotifiedTo checks that the user's own setting is
// read.
func TestADisabledDesktopIsNotNotifiedTo(t *testing.T) {
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		writeSettings(t, options.Layout, disabledDesktop)
	}).watchable()
	live.start(t)
	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	if delivered := live.notifier.sent(); len(delivered) != 0 {
		t.Fatalf("a project that turned desktop notifications off was notified to: %+v", delivered)
	}
}

// TestSuppressionCanBeTurnedOff checks the other half of the same setting: a
// user who wants to be told even while watching is told.
func TestSuppressionCanBeTurnedOff(t *testing.T) {
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		writeSettings(t, options.Layout, `version: 1

notifications:
  suppress_while_attached: false
`)
	}).watchable()
	live.start(t)
	live.watch(t, 1)
	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	if delivered := live.notifier.sent(); len(delivered) != 1 {
		t.Fatalf("delivered %d notifications with suppression off, want 1: %+v",
			len(delivered), delivered)
	}
}

// TestFailedApplicationServicesAreNotifiedAbout covers the one notifiable
// condition that had no test reaching it.
//
// It was found by walking every condition against a real desktop: six arrived
// and this one did not, because the fixture could not express a
// container that exited non-zero and so could not produce a failed runtime at
// all. What the notification path needed was already right; what was missing was
// any test that got there.
//
// A stopped runtime is deliberately not notifiable, and the second half checks
// that: v0 stops services only when a user asks, so a stop is something they just
// did rather than news (FR-RUN-005).
func TestFailedApplicationServicesAreNotifiedAbout(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)
	arranged.service.notifiable.Store(true)

	arranged.answerExitedFor(task, "exited", "Exited (1) 1 second ago", 1)
	arranged.service.pollRuntimes(context.Background())

	if state := arranged.reload(t, task.ID).Runtime.State; state != domain.RuntimeFailed {
		t.Fatalf("the observed runtime is %q, want failed", state)
	}
	delivered := arranged.notifier.sent()
	if len(delivered) != 1 {
		t.Fatalf("delivered %d notifications for failed services, want 1: %+v", len(delivered), delivered)
	}
	if !strings.Contains(delivered[0].Body, "application services failed") {
		t.Errorf("the notification does not say the services failed: %+v", delivered[0])
	}
	if !strings.Contains(delivered[0].Title, task.Key().String()) {
		t.Errorf("the notification does not name the task %s: %+v", task.Key(), delivered[0])
	}

	// A clean stop is not news.
	arranged.answerFor(task, "exited", "Exited (0) 1 second ago")
	arranged.service.pollRuntimes(context.Background())

	if after := arranged.notifier.sent(); len(after) != 1 {
		t.Errorf("stopping services the user stopped notified them about it: %+v", after[1:])
	}
}

// TestEveryDroppedNotificationSaysWhichPolicyDroppedIt is the rule that each
// policy which drops a notification drops it for a reason a user would
// recognise.
//
// A notification that never arrives is invisible by construction. The state
// change it was about is recorded correctly either way, so there is nothing to
// inspect afterwards and no way to tell a policy Feat applied on purpose from a
// notification the desktop swallowed — which is exactly the question the
// maintainer was left with when a task reached ready_for_review in silence.
// Four of these five used to be a silent return.
func TestEveryDroppedNotificationSaysWhichPolicyDroppedIt(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		drop   func(t *testing.T) (*session, *syncBuffer)
	}{
		{
			name:   "still catching up",
			reason: dropCatchingUp,
			drop: func(t *testing.T) (*session, *syncBuffer) {
				// Deliberately not made notifiable: this is the state the daemon
				// is in while it applies what arrived while it was stopped.
				live, logs := launchLogged(t, hostFixture, nil)
				live.start(t)
				live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
				return live, logs
			},
		},
		{
			name:   "this platform delivers none",
			reason: "this fake delivers nothing",
			drop: func(t *testing.T) (*session, *syncBuffer) {
				live, logs := launchLogged(t, hostFixture, func(o *Options) {
					o.Notifier = &fakeNotifier{available: false}
				})
				live.watchable().start(t)
				live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
				return live, logs
			},
		},
		{
			name:   "the settings turned them off",
			reason: dropDisabled,
			drop: func(t *testing.T) (*session, *syncBuffer) {
				live, logs := launchLogged(t, hostFixture, func(o *Options) {
					writeSettings(t, o.Layout, disabledDesktop)
				})
				live.watchable().start(t)
				live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
				return live, logs
			},
		},
		{
			name:   "the user is looking at it",
			reason: dropWatched,
			drop: func(t *testing.T) (*session, *syncBuffer) {
				live, logs := launchLogged(t, hostFixture, nil)
				live.watchable().start(t)
				live.watch(t, 1)
				live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
				return live, logs
			},
		},
		{
			name:   "nothing to say about that condition",
			reason: dropUncomposed,
			drop: func(t *testing.T) (*session, *syncBuffer) {
				// Reached directly, because every condition the tables map has
				// text. The branch exists so that a condition added without one
				// is dropped visibly rather than silently, which is the whole
				// point of this test.
				live, logs := launchLogged(t, hostFixture, nil)
				live.watchable().start(t)
				live.service.notifyTask(context.Background(), live.load(t), "a-condition-nobody-wrote", 0)
				return live, logs
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			live, logs := test.drop(t)

			if delivered := live.notifier.sent(); len(delivered) != 0 {
				t.Fatalf("the notification was not dropped: %+v", delivered)
			}

			written := logs.String()
			if !strings.Contains(written, "not interrupting the user") {
				t.Fatalf("the drop was silent:\n%s", written)
			}
			if !strings.Contains(written, test.reason) {
				t.Errorf("the log does not say why:\nwant %q\n%s", test.reason, written)
			}
			if key := live.load(t).Key().String(); !strings.Contains(written, key) {
				t.Errorf("the log does not name the task %s:\n%s", key, written)
			}
		})
	}
}

// TestADeliveredNotificationIsNotReportedAsDropped keeps the test above from
// passing against a daemon that logs the reason and then notifies anyway, or one
// that drops everything.
func TestADeliveredNotificationIsNotReportedAsDropped(t *testing.T) {
	live, logs := launchLogged(t, hostFixture, nil)
	live.watchable().start(t)

	live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)

	if delivered := live.notifier.sent(); len(delivered) != 1 {
		t.Fatalf("delivered %d notifications, want 1: %+v", len(delivered), delivered)
	}
	if written := logs.String(); strings.Contains(written, "not interrupting the user") {
		t.Errorf("a delivered notification was also reported as dropped:\n%s", written)
	}
}

// launchLogged launches a task with the daemon's own log captured, so that a
// test can read the decision the daemon recorded rather than only its effect.
func launchLogged(t *testing.T, fixture string, adjust func(*Options)) (*session, *syncBuffer) {
	t.Helper()

	logs := &syncBuffer{}
	live := launchWith(t, fixture, installed(), true, func(options *Options) {
		options.Logger = slog.New(slog.NewTextHandler(logs, nil))
		if adjust != nil {
			adjust(options)
		}
	})
	return live, logs
}

// syncBuffer is a log destination safe to read while the daemon is still
// writing. The gate, the pollers, and the idle timers all log from their own
// goroutines, and a test that read a plain buffer would fail under -race for a
// reason that has nothing to do with what it is checking.
type syncBuffer struct {
	mu      sync.Mutex
	written strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.written.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.written.String()
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
