package guard

import (
	"go/ast"
	"go/token"
	"path"
	"strconv"
	"testing"
)

// shellExecutables are interpreters that would turn an argument vector back
// into an interpolated command string.
var shellExecutables = map[string]bool{
	"bash": true,
	"csh":  true,
	"dash": true,
	"fish": true,
	"ksh":  true,
	"sh":   true,
	"tcsh": true,
	"zsh":  true,
}

// commandArgIndex maps an os/exec constructor to the position of its command
// name argument.
var commandArgIndex = map[string]int{
	"Command":        0,
	"CommandContext": 1,
}

// TestNoShellCommandInterpolation checks the CLAUDE.md architectural rule that
// Git, tmux, and Docker Compose are invoked through argument vectors rather
// than interpolated shell commands.
//
// It has nothing to reject until the adapter slices land. It exists so that the
// first attempt to reach for a shell fails in CI instead of in review.
func TestNoShellCommandInterpolation(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range goFiles(t, root) {
		fset, parsed := parseGoFile(t, root, rel)

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}

			index, ok := commandArgIndex[selector.Sel.Name]
			if !ok || len(call.Args) <= index {
				return true
			}
			lit, ok := call.Args[index].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				// A command resolved at runtime, typically from configuration.
				return true
			}
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}

			if shellExecutables[path.Base(name)] {
				t.Errorf("%s:%d: exec.%s spawns the shell %q\n"+
					"\tUse an argument vector for Git, tmux, and Docker Compose (CLAUDE.md architectural rules).\n"+
					"\tA configured shell command is allowed only where the configuration model gives it explicit trust semantics.",
					rel, fset.Position(lit.Pos()).Line, selector.Sel.Name, name)
			}
			return true
		})
	}
}
