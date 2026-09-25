package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/notify"
)

// Why a notification was not delivered.
//
// Each is a policy Feat applies on purpose, phrased as the user's own setting or
// situation rather than as an internal state, because somebody reads these when
// they were not told about something they expected to hear about.
const (
	dropCatchingUp = "the daemon was still catching up on what happened while it was stopped"
	dropDisabled   = "your settings set notifications.desktop to false"
	dropWatched    = "you are attached to this task's terminal, " +
		"and your settings set notifications.suppress_while_attached to true"
	dropUncomposed = "this build has no notification text for that condition"
)

// notifyTask interrupts the user about one task, once, and records that it did.
//
// It is called from the few places a change worth interrupting somebody for is
// recorded, never from the event recorder: a task whose agent dies produces both
// a process change and a workflow change, and a second notification about one
// death would be read as noise.
//
// A delivery that fails is logged and nothing more, because a desktop that cannot
// show a notification must never cost the state change it was announcing.
//
// Every path that does not deliver says which policy stopped it. A notification
// that never arrives leaves nothing to inspect, so the log is the only place
// "Feat decided not to" can be told from "the desktop swallowed it".
func (s *service) notifyTask(ctx context.Context, task *domain.Task, condition notify.Condition, idle time.Duration) {
	if !s.notifiable.Load() {
		// The daemon is applying control messages that arrived while it was stopped,
		// and a restart that fired one notification per turn that ended overnight
		// would be interrupting the user about the past (ADR-035).
		s.dropped(ctx, task, condition, dropCatchingUp)
		return
	}
	if s.undeliverable != "" {
		s.dropped(ctx, task, condition, s.undeliverable)
		return
	}

	policy := s.notifyPolicy()
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
	// what was attempted. macOS drops an unauthorised notification without saying
	// so, and Feat never claims more than that it delivered one.
	s.record(ctx, task, domain.Event{
		Type:   domain.EventNotificationSent,
		To:     string(condition),
		Detail: notification.Body,
	})
}

// dropped records that Feat decided not to interrupt the user, and why. It logs
// at info, because the reader is a user working out why they were not told
// something rather than somebody debugging Feat.
//
// It is a log line rather than a task event: a suppressed notification is not
// something that happened to the task, and an event would publish.
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

// notifyPolicy is what the user decided about being interrupted. It is one answer
// for the machine rather than one per project, because whether a notification may
// be shown is macOS's question and whether the user is already looking at the
// task is tmux's, and neither varies by repository (ADR-079).
//
// It reads nothing. The settings were resolved when the daemon started, and a
// file that could not be read left the defaults in place, so a user whose YAML
// has a typo still hears that their agent is waiting; `feat doctor` diagnoses the
// typo.
func (s *service) notifyPolicy() notify.Policy {
	return notify.Policy{
		Desktop:               s.settings.Notifications.DesktopEnabled(),
		SuppressWhileAttached: s.settings.Notifications.SuppressedWhileAttached(),
		IdleGrace:             s.settings.Notifications.IdleGrace(),
	}
}

// watching reports whether somebody is looking at this task's terminal.
//
// It asks tmux rather than remembering that somebody ran `feat attach`, because a
// user who detached or switched windows stops watching without telling Feat. The
// question is per window: a user attached to a project's session is looking at
// one of its tasks, not at all of them.
//
// A tmux that cannot answer is treated as nobody watching, so the notification is
// delivered. An unnecessary notification is noise, and a missing one is the
// failure notifications exist to prevent.
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

// notifyIdle interrupts the user about a task that has stayed idle. It re-reads
// the task when the grace period expires, because the agent may have started
// talking again, the user may have attached, or the task may have been archived.
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
// The two grace periods are measured from different moments. The provider's,
// agent.claude.idle_grace_period, decides when an ended turn becomes idle, which
// is a fact about how the agent is driven and so lives in the project's
// configuration. This one, notifications.idle_grace_period, is measured from the
// idle transition and is a fact about the person, so it is the machine's
// (ADR-079).
//
// Measuring both from the end of the turn was rejected: a notification grace
// shorter than the provider's would expire before the task was idle, so no
// notification would ever be delivered (ADR-035).
func (s *service) armIdleNotice(ctx context.Context, task *domain.Task, idleSince time.Time) {
	grace := s.notifyPolicy().Grace()

	id := task.ID
	s.idleNotice.arm(id, grace, func() {
		s.notifyIdle(context.WithoutCancel(ctx), id, idleSince)
	})
}
