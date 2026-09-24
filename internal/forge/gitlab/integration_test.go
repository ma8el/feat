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
// docs/06-technical-architecture.md requires of a provider CLI. A fake runner
// pins what Feat sends and cannot know that glab still accepts it. A renamed
// flag would turn every publication into a refusal the user reads as their own
// fault.
//
// It needs no account, no network, and no project: `glab mr create --help`
// prints its own flags. That is why a maintainer with glab installed can make
// this mandatory without anybody being made to install it.
//
// The version it ran against is logged rather than asserted, so a failure can be
// read beside gitlab.Verified without this test deciding which releases a user
// may have.
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
