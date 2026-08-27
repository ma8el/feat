package forge_test

import (
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
)

// TestOnlyABuiltForgeIsAvailable is the question `feat doctor` and the daemon
// both ask before a repository is published.
//
// A forge kind can be added to the domain and to configuration before its
// adapter exists — that is the order GitHub arrived in — and while it is in that
// state a user must be told at configuration time rather than after a branch has
// been pushed and the request cannot be opened (ADR-074).
func TestOnlyABuiltForgeIsAvailable(t *testing.T) {
	for _, kind := range forge.Built {
		if !forge.Available(kind) {
			t.Errorf("%s is built and reads as unavailable", kind)
		}
		if !kind.Valid() {
			t.Errorf("%s is built and is not a forge the domain knows", kind)
		}
	}

	// A kind no adapter covers. It is not a configuration a user can write —
	// validation refuses a kind outside the domain's own — so this is the guard
	// for the next forge to be added rather than a state anybody is in today.
	if forge.Available(domain.ForgeKind("bitbucket")) {
		t.Error("a forge with no adapter reads as available")
	}
	if forge.Available("") {
		t.Error("an undeclared forge reads as available")
	}
}
