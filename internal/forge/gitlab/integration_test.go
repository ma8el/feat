package gitlab_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/forge/gitlab"
	"github.com/ma8el/feat/internal/integrationtest"
)

// TestRealGlabAcceptsTheFlagsThisAdapterPasses is the verification
// docs/06-technical-architecture.md requires of a provider CLI.
//
// A fake runner pins the argument vector, which is enough to know what Feat
// sends and not enough to know that glab still accepts it. The adapter was
// written on a machine with no glab on it, so this is where the flags are
// checked against the installed one — and the failure it exists to catch is the
// quiet kind: a renamed flag turns every publication into a refusal the user
// reads as their own project being wrong.
//
// It asks glab a question and needs nothing else. There is no account, no
// network, and no project involved: `glab mr create --help` prints its own
// flags. That is why glab is demandable without being demanded — a maintainer
// with one installed can make this mandatory, and nobody is made to install it.
//
// The version it ran against is logged rather than asserted, so that a failure
// here can be read beside gitlab.Verified without this test deciding which
// releases a user may have.
func TestRealGlabAcceptsTheFlagsThisAdapterPasses(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that use a real glab", integrationtest.Env)
	}
	if _, err := exec.LookPath(gitlab.Executable); err != nil {
		integrationtest.Unavailable(t, integrationtest.Glab, "glab is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if version, err := exec.CommandContext(ctx, gitlab.Executable, "--version").Output(); err == nil {
		t.Logf("checking the flags against %s; this adapter was written against %s",
			strings.TrimSpace(string(version)), gitlab.Verified)
	}

	command := exec.CommandContext(ctx, gitlab.Executable, "mr", "create", "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("`glab mr create --help`: %v\n%s", err, output)
	}
	help := string(output)

	for _, flag := range gitlab.Flags {
		if !strings.Contains(help, flag) {
			t.Errorf("the installed glab does not document %s.\n"+
				"\tThis build passes it for every merge request it opens, so publication would fail "+
				"for a reason the user cannot act on. It was written against glab %s; compare the "+
				"adapter with `glab mr create --help`.",
				flag, gitlab.Verified)
		}
	}
}
