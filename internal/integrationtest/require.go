package integrationtest

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	// Env opts a run in to the tests that drive the real tools.
	Env = "FEAT_INTEGRATION"

	// EnvRequire names the tools such a run demands, separated by commas. A
	// demanded tool that is missing or unanswering fails the run; an undemanded
	// one skips it, as the whole tier did before.
	EnvRequire = "FEAT_INTEGRATION_REQUIRE"
)

// Tool names a real tool an integration run can demand.
type Tool string

const (
	// Git is the Git command line.
	Git Tool = "git"

	// Docker is the Docker CLI, its daemon, and the Compose plugin. They are
	// one name because they fail as one thing: a stopped Docker Desktop takes
	// all three away, and no test in this repository needs the CLI without the
	// daemon behind it.
	Docker Tool = "docker"

	// Tmux is the tmux server this build drives on its own socket.
	Tmux Tool = "tmux"

	// Claude is an installed, authenticated Claude Code. It is demandable and
	// is demanded by nothing by default: the tests behind it spend the
	// maintainer's tokens and interrupt a real account, so a run that wants
	// them says so.
	Claude Tool = "claude"

	// Notify is this platform's own desktop notifier. It is demandable and,
	// like Claude, demanded by nothing by default: no CI runner has a desktop,
	// and on a platform Feat has no notifier for the tests behind it can never
	// pass. What it buys is that a maintainer running the tier on their own
	// machine can say so and find out, rather than having the proofs that a
	// notification reached a desktop skip quietly on the one machine where they
	// could have run.
	Notify Tool = "notify"
)

// Tools are the names EnvRequire accepts.
//
// A value outside this list is a failure rather than an unknown-and-ignored
// name: "FEAT_INTEGRATION_REQUIRE=dockr" that quietly demanded nothing would be
// the same silent green this package exists to remove.
var Tools = []Tool{Git, Docker, Tmux, Claude, Notify}

// Enabled reports whether this run is opted in to the integration tier.
func Enabled() bool {
	return os.Getenv(Env) != ""
}

// Requirements returns the tools this run demands, in the order they are named,
// or an error naming a value that is not a tool.
//
// An unset or empty variable demands nothing, which is what a bare
// `go test -run TestReal ./...` does and what every run did before.
func Requirements() ([]Tool, error) {
	return parse(os.Getenv(EnvRequire))
}

// parse reads a comma-separated requirement list.
func parse(value string) ([]Tool, error) {
	var required []Tool
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		tool := Tool(field)
		if !known(tool) {
			return nil, fmt.Errorf("%s names %q, which is not a tool this repository knows how to demand. "+
				"The names are %s", EnvRequire, field, names())
		}
		if !contains(required, tool) {
			required = append(required, tool)
		}
	}
	return required, nil
}

// known reports whether a name is one of Tools.
func known(tool Tool) bool {
	return contains(Tools, tool)
}

// contains reports whether a tool is in a list.
func contains(list []Tool, tool Tool) bool {
	for _, candidate := range list {
		if candidate == tool {
			return true
		}
	}
	return false
}

// names renders the demandable tools for an error message.
func names() string {
	rendered := make([]string, 0, len(Tools))
	for _, tool := range Tools {
		rendered = append(rendered, string(tool))
	}
	sort.Strings(rendered)
	return strings.Join(rendered, ", ")
}

// Required reports whether this run demands the named tool.
//
// The error is the malformed requirement list, and callers are expected to fail
// on it rather than to read it as "not required": a typo that silently demanded
// nothing would restore the defect exactly.
func Required(tool Tool) (bool, error) {
	required, err := Requirements()
	if err != nil {
		return false, err
	}
	return contains(required, tool), nil
}

// TB is the part of *testing.T this package uses.
//
// It is an interface so that the behaviour below — which of Skipf and Fatalf a
// missing tool reaches — is itself testable, and so that this package does not
// import testing.
type TB interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Unavailable ends a test whose tool is missing, unreachable, or answered
// wrongly.
//
// It fails when the run demands that tool and skips when it does not. The
// reason is the caller's, because "no Docker daemon is reachable" and "this
// machine cannot run a container to measure" are different findings about the
// same demand and the failure has to say which one happened.
func Unavailable(t TB, tool Tool, format string, args ...any) {
	t.Helper()

	reason := fmt.Sprintf(format, args...)

	required, err := Required(tool)
	if err != nil {
		t.Fatalf("%s.\n\t%v", reason, err)
		return
	}
	if !required {
		t.Skipf("%s (set %s=%s to make this a failure)", reason, EnvRequire, tool)
		return
	}
	t.Fatalf("%s.\n\tThis run demands %s (%s=%s), so the proofs behind this test did not run "+
		"and the gate must not report green. Install or start it, or name a smaller set of tools.",
		reason, tool, EnvRequire, os.Getenv(EnvRequire))
}

// Absence is a demanded tool that did not answer, and why.
type Absence struct {
	Tool   Tool
	Reason error
}

// Probe answers whether one tool is present and answering on this machine.
type Probe func(Tool) error

// Missing returns the demanded tools that did not answer, in the order demanded.
//
// The probe is a parameter because this package may not run processes, and
// because a preflight that could not be tested without uninstalling Docker
// would be the same untested guard it replaces.
func Missing(required []Tool, probe Probe) []Absence {
	var absent []Absence
	for _, tool := range required {
		if err := probe(tool); err != nil {
			absent = append(absent, Absence{Tool: tool, Reason: err})
		}
	}
	return absent
}
