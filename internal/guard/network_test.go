package guard

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/ma8el/feat"

// networkImports are the packages that can open a connection to a network host.
// The depguard rule network-stays-in-the-socket denies the same list.
var networkImports = map[string]bool{
	"net":        true,
	"net/http":   true,
	"net/rpc":    true,
	"net/smtp":   true,
	"crypto/tls": true,
	"log/syslog": true,
}

// socketPackages are the Unix-domain socket and its client (ADR-009, ADR-027).
var socketPackages = map[string]bool{
	module + "/internal/api":    true,
	module + "/internal/client": true,
	module + "/internal/daemon": true,
}

// reviewedDependencies are third-party packages that import a network package
// without reaching a host. Each entry was read, and an entry nothing needs any
// more fails the test so the list cannot outlive its review.
var reviewedDependencies = map[string]string{
	"github.com/spf13/pflag": "imports net to parse IP and IPNet flag values",
}

// TestOnlyTheSocketReachesTheNetwork is the transitive half of ADR-103. depguard
// sees only a package's own imports, so this walks every package linked into the
// binary on each release platform and names any importer of a network package.
func TestOnlyTheSocketReachesTheNetwork(t *testing.T) {
	root := repoRoot(t)
	reviewedSeen := map[string]bool{}

	for _, goos := range []string{"darwin", "linux"} {
		command := exec.Command("go", "list", "-deps",
			"-f", "{{.ImportPath}} {{.Standard}} {{join .Imports \" \"}}", "./cmd/feat")
		command.Dir = root
		command.Env = append(os.Environ(), "GOOS="+goos)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("go list for %s failed: %v", goos, err)
		}

		sawClient := false
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				t.Fatalf("go list for %s printed %q, which is not a package line", goos, line)
			}
			pkg, standard, imports := fields[0], fields[1] == "true", fields[2:]
			sawClient = sawClient || pkg == module+"/internal/client"
			if standard || socketPackages[pkg] {
				continue
			}
			for _, imported := range imports {
				if !networkImports[imported] && !strings.HasPrefix(imported, "golang.org/x/net") {
					continue
				}
				if _, reviewed := reviewedDependencies[pkg]; reviewed {
					reviewedSeen[pkg] = true
					continue
				}
				t.Errorf("%s: %s imports %s; only the socket and its client may reach the network (ADR-103)",
					goos, pkg, imported)
			}
		}
		if !sawClient {
			t.Fatalf("go list for %s did not list internal/client, so it did not list the binary", goos)
		}
	}

	for pkg := range reviewedDependencies {
		if !reviewedSeen[pkg] {
			t.Errorf("%s no longer imports a network package; remove it from reviewedDependencies", pkg)
		}
	}
}
