//go:build !darwin

package notify

import (
	"context"
	"fmt"
	"runtime"
)

// unavailable explains why this build delivers nothing. It names the platform that
// is supported, under the rule ADR-028 established for a diagnostic a build cannot
// run: an absent capability says so rather than reporting success it has not earned.
// The TUI's attention badges work everywhere and are unaffected.
const unavailable = "Feat delivers desktop notifications on macOS only; support for this platform is v0.2 work"

// Absent is the notifier of a platform Feat does not deliver on yet.
type Absent struct{}

var _ Notifier = Absent{}

// Host returns the notifier for this platform.
func Host() Notifier { return Absent{} }

// Available reports that this build cannot deliver, and why.
func (Absent) Available() (bool, string) {
	return false, unavailable + " (this is " + runtime.GOOS + ")"
}

// Notify refuses rather than pretending. A caller checks Available first and never
// reaches this, and one that did not is told it delivered nothing rather than left
// to assume it did.
func (Absent) Notify(_ context.Context, _ Notification) error {
	return fmt.Errorf("%s", unavailable)
}
