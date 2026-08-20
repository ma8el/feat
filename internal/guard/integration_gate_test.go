package guard

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// runPattern is the -run expression `make test-real` and CI both use to select
// the opt-in tier. It is written out again here on purpose: this test's whole
// job is to notice when a gated test stops matching it, which it could not do by
// reading the same value the runner reads.
var runPattern = regexp.MustCompile(`^Test(Real|Binary)`)

// envIntegrationName is the variable that opts a run in to the tier.
const envIntegrationName = "FEAT_INTEGRATION"

// integrationPackage is the package a gated test asks whether it may run.
const integrationPackage = "integrationtest"

// TestEveryGatedTestIsNamedForTheRunPattern checks that a test which refuses to
// run without FEAT_INTEGRATION is one the integration runner selects.
//
// The two halves are held together by convention alone: the gate is a check
// inside the function, and the selection is `-run 'TestBinary|TestReal'` on the
// command line. A gated test named anything else runs nowhere — not under
// `go test ./...`, which its own gate turns off, and not under `make test-real`,
// which never selects it — and nothing says so. It reads as a proof the
// repository has and the machine is never asked to make.
//
// What counts as gated is a call to integrationtest.Enabled, a read of the
// variable by name, or a demand made of this run through
// integrationtest.Unavailable — directly, or through a helper in the same
// package, which is how every file in the tier is written. A test that gated
// itself some third way would not be seen here, which is the reason the
// mechanism is one package rather than a habit.
func TestEveryGatedTestIsNamedForTheRunPattern(t *testing.T) {
	root := repoRoot(t)

	units := testPackages(t, root)
	if len(units) == 0 {
		t.Fatalf("no test packages found under %s", root)
	}

	gatedTests := 0
	for _, unit := range units {
		for _, name := range unit.gated() {
			function := unit.functions[name]
			if !isTestFunction(function.decl) {
				continue
			}
			gatedTests++
			if runPattern.MatchString(name) {
				continue
			}
			t.Errorf("%s:%d: %s gates on %s but the integration runner does not select it\n"+
				"\tThe tier is selected with -run 'TestBinary|TestReal' (Makefile test-real, and CI).\n"+
				"\tA gated test named anything else runs in neither tier and reports nothing.\n"+
				"\tRename it to begin with TestReal or TestBinary.",
				function.file, function.line, name, envIntegrationName)
		}
	}

	// A convention this test cannot find any instance of is a convention that
	// has been renamed out from under it, which is the failure it exists to
	// prevent happening silently.
	if gatedTests == 0 {
		t.Errorf("no gated test was found anywhere in the repository, "+
			"so this guard is checking nothing. Either the tier is gone or it stopped gating on %s.",
			envIntegrationName)
	}
}

// function is one declaration, with the file it was read from.
type function struct {
	decl *ast.FuncDecl
	file string
	line int
}

// testPackage is the test files of one package, which is the scope a
// package-local call resolves in.
type testPackage struct {
	functions map[string]function
	// integrationConsts are package-level constants whose value is the name of
	// the opt-in variable. Two daemon files were invisible to a grep for that
	// name because they share one.
	integrationConsts map[string]bool
}

// testPackages parses every _test.go file in the repository, grouped by the
// package each belongs to.
func testPackages(t *testing.T, root string) []testPackage {
	t.Helper()

	units := map[string]*testPackage{}
	for _, rel := range goFiles(t, root) {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		fset, parsed := parseGoFile(t, root, rel)

		key := filepath.Dir(rel) + ":" + parsed.Name.Name
		unit := units[key]
		if unit == nil {
			unit = &testPackage{
				functions:         map[string]function{},
				integrationConsts: map[string]bool{},
			}
			units[key] = unit
		}

		for _, decl := range parsed.Decls {
			switch typed := decl.(type) {
			case *ast.FuncDecl:
				if typed.Recv != nil || typed.Body == nil {
					continue
				}
				unit.functions[typed.Name.Name] = function{
					decl: typed,
					file: rel,
					line: fset.Position(typed.Pos()).Line,
				}
			case *ast.GenDecl:
				collectIntegrationConsts(typed, unit.integrationConsts)
			}
		}
	}

	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	ordered := make([]testPackage, 0, len(units))
	for _, key := range keys {
		ordered = append(ordered, *units[key])
	}
	return ordered
}

// collectIntegrationConsts records constants whose value is the opt-in
// variable's name.
func collectIntegrationConsts(decl *ast.GenDecl, into map[string]bool) {
	if decl.Tok != token.CONST {
		return
	}
	for _, spec := range decl.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range value.Names {
			if i >= len(value.Values) {
				continue
			}
			literal, ok := value.Values[i].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			if unquoted, err := strconv.Unquote(literal.Value); err == nil && unquoted == envIntegrationName {
				into[name.Name] = true
			}
		}
	}
}

// gated returns the names, in source order of name, of the functions in this
// package that refuse to run outside the integration tier.
func (p testPackage) gated() []string {
	gated := map[string]bool{}
	for name, function := range p.functions {
		if p.gatesDirectly(function.decl) {
			gated[name] = true
		}
	}

	// The gate usually lives in a helper — requireTmux, realDocker — so the
	// property propagates to whatever calls it, and to whatever calls that.
	for changed := true; changed; {
		changed = false
		for name, function := range p.functions {
			if gated[name] {
				continue
			}
			for callee := range callees(function.decl) {
				if gated[callee] {
					gated[name] = true
					changed = true
					break
				}
			}
		}
	}

	names := make([]string, 0, len(gated))
	for name := range gated {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// gatesDirectly reports whether a function's own body decides on the tier.
func (p testPackage) gatesDirectly(decl *ast.FuncDecl) bool {
	found := false
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}

		if pkg.Name == integrationPackage && gatesThroughHelper(selector.Sel.Name, call) {
			found = true
		}
		if pkg.Name == "os" && selector.Sel.Name == "Getenv" && len(call.Args) == 1 &&
			p.namesTheVariable(call.Args[0]) {
			found = true
		}
		return true
	})
	return found
}

// gatesThroughHelper reports whether a call into the integrationtest package is
// this function deciding whether it may run.
//
// Enabled is the question. Unavailable is a demand, and it counts only when it
// is made of the test itself: the package's own tests pass a recorder to it to
// observe what it does, and they belong in the unit tier.
func gatesThroughHelper(name string, call *ast.CallExpr) bool {
	switch name {
	case "Enabled":
		return true
	case "Unavailable":
		if len(call.Args) == 0 {
			return false
		}
		arg, ok := call.Args[0].(*ast.Ident)
		return ok && arg.Name == "t"
	default:
		return false
	}
}

// namesTheVariable reports whether an expression is the opt-in variable's name.
func (p testPackage) namesTheVariable(arg ast.Expr) bool {
	switch typed := arg.(type) {
	case *ast.BasicLit:
		unquoted, err := strconv.Unquote(typed.Value)
		return err == nil && unquoted == envIntegrationName
	case *ast.Ident:
		return p.integrationConsts[typed.Name]
	case *ast.SelectorExpr:
		pkg, ok := typed.X.(*ast.Ident)
		return ok && pkg.Name == integrationPackage && typed.Sel.Name == "Env"
	default:
		return false
	}
}

// callees returns the names of the package-local functions a declaration calls.
func callees(decl *ast.FuncDecl) map[string]bool {
	called := map[string]bool{}
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok {
				called[ident.Name] = true
			}
		}
		return true
	})
	return called
}

// isTestFunction reports whether a declaration is one `go test` would run.
func isTestFunction(decl *ast.FuncDecl) bool {
	rest, found := strings.CutPrefix(decl.Name.Name, "Test")
	if !found {
		return false
	}
	// "Test" alone, and "Testify", are not test functions: the character after
	// the prefix must not be lower case.
	if rest == "" || strings.ToUpper(rest[:1]) != rest[:1] {
		return false
	}
	return decl.Type.Params != nil && len(decl.Type.Params.List) == 1
}
