package guard

import (
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
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
// A file that must spawn a shell because a shell is what it is testing is
// exempted through testdata/shell-exemptions.txt, which states the reason. The
// exemption lives there rather than in a //nolint comment so that every
// deliberate use stays visible in one reviewable place (ADR-025).
func TestNoShellCommandInterpolation(t *testing.T) {
	root := repoRoot(t)
	exempt := loadShellExemptions(t)

	for _, rel := range goFiles(t, root) {
		if exempt(rel) {
			continue
		}
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
					"\tA configured shell command is allowed only where the configuration model gives it explicit trust semantics.\n"+
					"\tIf the shell is what this code tests, record the exemption in %s.",
					rel, fset.Position(lit.Pos()).Line, selector.Sel.Name, name, shellExemptionsPath)
			}
			return true
		})
	}
}

const shellExemptionsPath = "internal/guard/testdata/shell-exemptions.txt"

// loadShellExemptions returns a predicate reporting whether a repository-relative
// path is permitted to spawn a shell.
func loadShellExemptions(t *testing.T) func(string) bool {
	t.Helper()

	file := filepath.Join("testdata", "shell-exemptions.txt")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}

	var allowed []string
	for number, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		directive, value, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("%s:%d: expected \"<directive>: <value>\", got %q", file, number+1, line)
		}
		value = strings.TrimSpace(value)
		if strings.TrimSpace(directive) != "allow" || value == "" {
			t.Fatalf("%s:%d: the only directive is \"allow: <prefix>\", got %q", file, number+1, line)
		}
		allowed = append(allowed, value)
	}

	return func(rel string) bool {
		for _, prefix := range allowed {
			if strings.HasPrefix(rel, prefix) {
				return true
			}
		}
		return false
	}
}
