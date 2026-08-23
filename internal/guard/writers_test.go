package guard

import (
	"go/ast"
	"strconv"
	"strings"
	"testing"
)

// storagePackages are the import paths that reach persistent state.
var storagePackages = []string{
	"github.com/ma8el/feat/internal/store",
	"github.com/ma8el/feat/internal/store/fs",
}

// storageReaders are the only packages allowed to import them.
//
// The daemon is the sole reader and writer of persistent state (ADR-008), so
// everything else asks it over the socket. storetest is fixtures, and the store's
// own packages obviously import themselves.
var storageReaders = []string{
	"internal/daemon/",
	"internal/store/",
}

// TestOnlyTheDaemonReachesPersistentState checks that the daemon is the only
// state writer.
//
// depguard enforces the same boundary for the packages that exist today; this
// test states the rule positively, so that a package added later is covered
// without anyone remembering to add it to the lint configuration. Test files are
// exempt: a test may arrange state directly, which is how the daemon's own tests
// set up a project without an endpoint that can create one yet.
func TestOnlyTheDaemonReachesPersistentState(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range goFiles(t, root) {
		if strings.HasSuffix(rel, "_test.go") || hasPrefixAny(rel, storageReaders) {
			continue
		}
		_, parsed := parseGoFile(t, root, rel)

		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			for _, storage := range storagePackages {
				if path != storage {
					continue
				}
				t.Errorf("%s imports %s\n"+
					"\tThe daemon is the only reader and writer of persistent state (ADR-008).\n"+
					"\tReach it through internal/client, or move the code into internal/daemon.",
					rel, path)
			}
		}
	}
}

// TestGuardPackageHasNoRuntimeCode keeps internal/guard what ADR-025 says it is:
// invariant tests, with nothing importable at runtime.
func TestGuardPackageHasNoRuntimeCode(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range goFiles(t, root) {
		if !strings.HasPrefix(rel, "internal/guard/") {
			continue
		}
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		_, parsed := parseGoFile(t, root, rel)

		for _, declaration := range parsed.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				t.Errorf("internal/guard contains runtime code: %s declares %s\n"+
					"\tThe package holds repository-wide invariant tests only (ADR-025).",
					rel, function.Name.Name)
			}
		}
	}
}
