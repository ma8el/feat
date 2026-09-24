package forge_test

import (
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
)

// TestOnlyABuiltForgeIsAvailable is the question `feat doctor` and the daemon
// both ask before a repository is published. A forge kind can reach the domain
// and configuration before its adapter exists, and a user must be told then
// rather than after a branch has been pushed (ADR-074).
func TestOnlyABuiltForgeIsAvailable(t *testing.T) {
	for _, kind := range forge.Built {
		if !forge.Available(kind) {
			t.Errorf("%s is built and reads as unavailable", kind)
		}
		if !kind.Valid() {
			t.Errorf("%s is built and is not a forge the domain knows", kind)
		}
	}

	// A kind no adapter covers. Validation refuses a kind outside the domain's
	// own, so this guards the next forge to be added rather than a state
	// anybody is in today.
	if forge.Available(domain.ForgeKind("bitbucket")) {
		t.Error("a forge with no adapter reads as available")
	}
	if forge.Available("") {
		t.Error("an undeclared forge reads as available")
	}
}
