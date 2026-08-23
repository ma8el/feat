package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/notify"
)

// Why a notification was not delivered.
//
// Each is a policy Feat applies on purpose, and each is phrased as the user's own
// setting or situation rather than as an internal state, because these are what
// somebody reads when they were not told about something they expected to hear
// about. Four of the five used to be a silent return, which made "why did I not
// get a notification" a question the daemon's own log could not answer.
const (
	dropCatchingUp = "the daemon was still catching up on what happened while it was stopped"
	dropDisabled   = "this project sets notifications.desktop to false"
	dropWatched    = "you are attached to this task's terminal, " +
		"and this project sets notifications.suppress_while_attached to true"
	dropUncomposed = "this build has no notification text for that condition"
)

// notifyTask interrupts the user about one task, once, and records that it did.
//
// Everything about this is conservative. It is called from the few places a
// change worth interrupting somebody for is recorded, never from the event
// recorder itself: a task whose agent dies produces both a process change and a
// workflow change, and a user who was told twice about one death would learn to
// read the second one as noise.
//
// A delivery that fails is logged and nothing more. A notification is the least
// important thing Feat does with a task, and a desktop that cannot show one must
// never cost the state change it was announcing.
//
// Every path that does not deliver says which policy stopped it. A notification
// that never arrives is invisible by construction — there is nothing to inspect
// afterwards, and the state change it was about is correct either way — so the
// log is the only place the difference between "Feat decided not to" and "the
// desktop swallowed it" can be established.
func (s *service) notifyTask(ctx context.Context, task *domain.Task, condition notify.Condition, idle time.Duration) {
	if !s.notifiable.Load() {
		// Startup catch-up. The daemon is applying control messages that arrived
		// while it was stopped, and a restart that fired a notification for every
		// turn that ended overnight would be interrupting the user about the past
		// (ADR-035).
		s.dropped(ctx, task, condition, dropCatchingUp)
		return
	}
	if s.undeliverable != "" {
		s.dropped(ctx, task, condition, s.undeliverable)
		return
	}

	policy := s.notifyPolicy(ctx, task)
	if !policy.Desktop {
		s.dropped(ctx, task, condition, dropDisabled)
		return
	}
	if policy.SuppressWhileAttached && s.watching(ctx, task) {
		s.dropped(ctx, task, condition, dropWatched)
		return
	}

	notification, ok := notify.Compose(condition, notify.Subject{
		Key:     task.Key().String(),
		Title:   task.Title,
		Project: task.ProjectID.String(),
	}, idle)
	if !ok {
		s.dropped(ctx, task, condition, dropUncomposed)
		return
	}

	if err := s.notifier.Notify(ctx, notification); err != nil {
		s.logger.WarnContext(ctx, "delivering a desktop notification",
			slog.String("task", task.ID.String()), slog.Any("error", err))
		return
	}
	// Recorded after delivery, so the log says what was handed over rather than
	// what was attempted. What the user then saw is between them and their
	// desktop: macOS drops an unauthorised notification without saying so, and
	// Feat never claims more than that it delivered one.
	s.record(ctx, task, domain.Event{
		Type:   domain.EventNotificationSent,
		To:     string(condition),
		Detail: notification.Body,
	})
}

// dropped records that Feat decided not to interrupt the user, and why.
//
// At info rather than debug, because the reader is a user working out why they
// were not told something rather than somebody debugging Feat. It is a log line
// rather than a task event: a suppressed notification is not something that
// happened to the task, and an event would publish, which is a step towards a
// notification about not having sent a notification.
func (s *service) dropped(
	ctx context.Context, task *domain.Task, condition notify.Condition, reason string,
) {
	s.logger.InfoContext(ctx, "not interrupting the user about a task",
		slog.String("task", task.ID.String()),
		slog.String("key", task.Key().String()),
		slog.String("project", task.ProjectID.String()),
		slog.String("condition", string(condition)),
		slog.String("reason", reason))
}

// notifyPolicy is what the task's project decided about being interrupted.
//
// A configuration that cannot be read falls back to the default rather than to
// silence. The project is registered, so the file existed once; a user whose YAML
// is temporarily broken should still hear that their agent is waiting for them,
// and `feat doctor` is where a broken file is diagnosed.
func (s *service) notifyPolicy(ctx context.Context, task *domain.Task) notify.Policy {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		s.logger.WarnContext(ctx, "reading a project's notification settings",
			slog.String("project", task.ProjectID.String()), slog.Any("error", err))
		return notify.DefaultPolicy()
	}
	return notify.Policy{
		Desktop:               cfg.Notifications.DesktopEnabled(),
		SuppressWhileAttached: cfg.Notifications.SuppressedWhileAttached(),
		IdleGrace:             cfg.Notifications.IdleGrace(),
	}
}

// watching reports whether somebody is looking at this task's terminal.
//
// It asks tmux rather than remembering that somebody once ran `feat attach`: a
// user who detached, or who switched to another task's window in the same
// session, stops watching without telling Feat anything. The question is asked
// per window for the same reason — a user attached to a project's session is
// looking at one of its tasks, not at all of them.
//
// A tmux that cannot answer is treated as nobody watching, so the notification
// is delivered. Of the two mistakes, an unnecessary notification is noise and a
// missing one is the failure notifications exist to prevent.
func (s *service) watching(ctx context.Context, task *domain.Task) bool {
	if task.Session == nil {
		return false
	}
	terminal, found, err := s.terminals.Find(ctx, task.ProjectID, task.ID)
	if err != nil {
		s.logger.WarnContext(ctx, "asking tmux whether a task is being watched",
			slog.String("task", task.ID.String()), slog.Any("error", err))
		return false
	}
	return found && terminal.Watched()
}

// notifyIdle interrupts the user about a task that has stayed idle.
//
// It runs when the notification grace period expires, and it re-reads the task
// first. Everything it checks can have changed in the meantime: the agent may
// have started talking again, the user may have attached, or the task may have
// been archived. Deciding at arming time and delivering blindly would notify
// about a state that is over.
func (s *service) notifyIdle(ctx context.Context, id domain.TaskID, since time.Time) {
	task, err := s.Task(ctx, id)
	if err != nil {
		s.logger.WarnContext(ctx, "an idle task could not be loaded to notify about",
			slog.String("task", id.String()), slog.Any("error", err))
		return
	}
	if task.Session == nil || task.Session.Process != domain.ProcessIdle {
		// It went idle and then did something else. Nothing to say.
		return
	}
	if task.Workflow == domain.WorkflowArchived {
		return
	}
	s.notifyTask(ctx, task, notify.ConditionIdle, s.now().Sub(since))
}

// armIdleNotice starts the period after which a task that stayed idle is worth
// interrupting the user about.
//
// The two grace periods are measured from different moments on purpose. The
// provider's own grace, agent.claude.idle_grace_period, decides when an ended
// turn becomes idle, because a turn that ends and immediately continues is not a
// session waiting for anybody. This one, notifications.idle_grace_period,
// decides how long a task must have *been* idle before Feat interrupts somebody
// about it, and it is measured from the idle transition.
//
// Measuring both from the end of the turn was the other candidate and was
// rejected: a notification grace shorter than the provider's would then expire
// before the task was idle and no notification would ever be delivered, which is
// a configuration that silently turns off the thing it configures (ADR-035).
func (s *service) armIdleNotice(ctx context.Context, task *domain.Task, idleSince time.Time) {
	grace := s.notifyPolicy(ctx, task).Grace()

	id := task.ID
	s.idleNotice.arm(id, grace, func() {
		s.notifyIdle(context.WithoutCancel(ctx), id, idleSince)
	})
}
