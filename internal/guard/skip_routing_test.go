package guard

import (
	"go/ast"
	"go/token"
	"sort"
	"testing"
)

// unavailableHelper is the one way out of a gated test that the run's demand
// list can turn into a failure.
const unavailableHelper = integrationPackage + ".Unavailable"

// TestEverySkipTheGatedTierCanReachIsDemandable checks that a gated test which
// gives up on this machine gives up through the demand mechanism.
//
// It is the sibling of TestEveryGatedTestIsNamedForTheRunPattern, and it guards
// the half that one does not. That test checks a gated function's *name*, so the
// integration runner selects it. This checks what the function does when the
// machine will not answer: a bare t.Skip prints "ok" for the package, so a run
// that demanded Docker and got a Docker which could not start a container
// reports green having proved nothing — which is the whole of G6-05, one layer
// in from where it was fixed.
//
// It is not a hypothetical. The demand mechanism landed, and one batch later a
// new t.Skipf on a Docker that would not start a privileged container removed
// the only real-Docker proof of a critical fix, with nothing to object
// (execution/compose/integration_test.go:883, and the non-root skip at :64 that
// reached every TestReal in the file through realTask).
//
// The scope is everything a gated test can reach, not the gated tests
// themselves. Both of those skips were in helpers — one of them a helper that
// asks nothing about the tier — and a guard that read only the test functions
// would have seen neither.
//
// The one skip that is not a bailout is the tier opt-in itself: `if
// !integrationtest.Enabled() { t.Skipf("set FEAT_INTEGRATION=1 …") }` is how a
// run says it did not ask for this tier, and there is nothing to demand of a
// machine that was never asked. It is recognised by the question it is under
// rather than by its text, so a skip cannot be exempted by wording.
func TestEverySkipTheGatedTierCanReachIsDemandable(t *testing.T) {
	root := repoRoot(t)

	units := testPackages(t, root)
	if len(units) == 0 {
		t.Fatalf("no test packages found under %s", root)
	}

	examined := 0
	for _, unit := range units {
		for _, name := range unit.reachableFromGatedTests() {
			examined++
			for _, skip := range unit.unroutedSkips(unit.functions[name]) {
				t.Errorf("%s:%d: %s gives up on this machine with %s, outside the demand path\n"+
					"\tA skipped package still prints \"ok\", so a run that demanded the tool this is "+
					"about reports green having proved nothing (G6-05).\n"+
					"\tCall %s(t, integrationtest.<Tool>, \"why\") instead: it fails when the run "+
					"demanded that tool and skips when it did not.\n"+
					"\tThe one exception is the tier opt-in, which is a skip under "+
					"if !%s.Enabled().",
					skip.file, skip.line, name, skip.call, unavailableHelper, integrationPackage)
			}
		}
	}

	// The same reasoning as the sibling test's own count: a convention nothing
	// matches is one that has been renamed out from under its guard.
	if examined == 0 {
		t.Errorf("no function reachable from a gated test was found anywhere in the repository, "+
			"so this guard is checking nothing. Either the tier is gone or it stopped gating on %s.",
			envIntegrationName)
	}
}

// skipCall is one bailout, named where it was written.
type skipCall struct {
	call string
	file string
	line int
}

// reachableFromGatedTests returns the names, sorted, of the functions in this
// package that a gated test runs: the gated tests themselves and, transitively,
// the package-local functions they call.
//
// It walks callers to callees, which is the opposite direction from gated(). A
// helper is gated when it decides the tier for whoever calls it; a helper is
// reachable when somebody gated calls it. agentUser is the case that needs both
// to be separate: it asks nothing about the tier, so it is not gated, and every
// TestReal in its file reaches it through realTask.
func (p testPackage) reachableFromGatedTests() []string {
	reachable := map[string]bool{}
	for _, name := range p.gated() {
		if isTestFunction(p.functions[name].decl) {
			reachable[name] = true
		}
	}

	for changed := true; changed; {
		changed = false
		for name := range reachable {
			for callee := range callees(p.functions[name].decl) {
				if _, local := p.functions[callee]; !local || reachable[callee] {
					continue
				}
				reachable[callee] = true
				changed = true
			}
		}
	}

	names := make([]string, 0, len(reachable))
	for name := range reachable {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unroutedSkips returns the calls in a function that end a test without asking
// what this run demanded.
//
// Any receiver counts, not only one named t: a helper takes testing.TB under
// whatever name its author chose, and the skip is the same skip.
func (p testPackage) unroutedSkips(fn function) []skipCall {
	exempt := p.tierBranches(fn.decl)

	var found []skipCall
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isSkip(selector.Sel.Name) {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if within(exempt, call.Pos()) {
			return true
		}
		position := fn.fset.Position(call.Pos())
		found = append(found, skipCall{
			call: receiver.Name + "." + selector.Sel.Name,
			file: fn.file,
			line: position.Line,
		})
		return true
	})
	return found
}

// span is a half-open range of source positions.
type span struct{ from, to token.Pos }

// tierBranches returns the parts of a body that run because of what this run
// asked for rather than because of what this machine can do.
//
// Both arms of the question are exempt. `if !Enabled() { skip }` and
// `if Enabled() { … } else { skip }` say the same thing, and a guard that
// recognised only the first would be a guard against one spelling.
func (p testPackage) tierBranches(decl *ast.FuncDecl) []span {
	var exempt []span
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok || !p.asksTheTier(branch.Cond) {
			return true
		}
		exempt = append(exempt, span{branch.Body.Pos(), branch.Body.End()})
		if branch.Else != nil {
			exempt = append(exempt, span{branch.Else.Pos(), branch.Else.End()})
		}
		return true
	})
	return exempt
}

// asksTheTier reports whether a condition is the question "did this run opt in".
func (p testPackage) asksTheTier(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(node ast.Node) bool {
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
		if pkg.Name == integrationPackage && selector.Sel.Name == "Enabled" {
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

// isSkip reports whether a method name ends a test as skipped.
func isSkip(name string) bool {
	switch name {
	case "Skip", "Skipf", "SkipNow":
		return true
	default:
		return false
	}
}

// within reports whether a position falls inside any of the spans.
func within(spans []span, at token.Pos) bool {
	for _, s := range spans {
		if at >= s.from && at < s.to {
			return true
		}
	}
	return false
}
