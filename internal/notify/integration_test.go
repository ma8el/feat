package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/integrationtest"
)

// TestRealNotificationIsDelivered hands a notification to this platform's own
// notifier.
//
// It checks that Feat delivered one, which is all Feat can know. macOS decides per
// application whether a notification is shown, drops an unauthorised one without
// saying so, and exits 0 either way, so Feat reports delivery and never sight, as
// the README says.
//
// Run it with -count=1. The test exists for a side effect outside the process, and a
// cached result replays --- PASS without producing one, which looks like a
// notification the platform swallowed (ADR-035 evidence 13).
func TestRealNotificationIsDelivered(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to deliver a real desktop notification", integrationtest.Env)
	}

	notifier := Host()
	available, reason := notifier.Available()
	if !available {
		// Through the demand, which is why notify is a tool name at all. No CI runner
		// has a desktop, so this skips on almost every machine and must stay a skip.
		// The demand lets a maintainer on the one machine that could deliver ask for a
		// failure instead.
		integrationtest.Unavailable(t, integrationtest.Notify,
			"this platform delivers no desktop notifications: %s", reason)
	}

	notification, ok := Compose(ConditionIdle, Subject{
		Key: "0000test", Title: "Feat's own test suite", Project: "feat",
	}, 5*time.Second)
	if !ok {
		t.Fatal("Compose reports no notification")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := notifier.Notify(ctx, notification); err != nil {
		t.Fatalf("delivering %+v: %v", notification, err)
	}
}

// TestRealNotifierRefusesTextItWouldMisread checks the guard on the two values
// that reach an argument vector.
//
// A task's title is text the user typed. The script reads its arguments out of argv
// rather than having them pasted into it, so a quotation mark is harmless. A value
// the runner would read as one of its own options is not, which is the class of
// defect ADR-029 refused for Git remotes.
func TestRealNotifierRefusesTextItWouldMisread(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run this against the real notifier", integrationtest.Env)
	}
	notifier := Host()
	if available, reason := notifier.Available(); !available {
		integrationtest.Unavailable(t, integrationtest.Notify,
			"this platform delivers no desktop notifications: %s", reason)
	}

	for _, notification := range []Notification{
		{Title: "-e", Body: "a body"},
		{Title: "feat", Body: "-e"},
		{Title: "feat", Body: ""},
		{Title: "feat", Body: "two\nlines"},
	} {
		if err := notifier.Notify(context.Background(), notification); err == nil {
			t.Errorf("the notifier accepted %+v", notification)
		} else if !strings.Contains(err.Error(), "notification") {
			t.Errorf("the refusal of %+v does not say what was refused: %v", notification, err)
		}
	}
}
