package guard

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoReferenceProjectIdentifiers checks the slice 0 acceptance criterion
// that no project-specific path or service name is compiled into the binary.
func TestNoReferenceProjectIdentifiers(t *testing.T) {
	root := repoRoot(t)
	rules := loadIdentifierRules(t)

	for _, rel := range goFiles(t, root) {
		if hasPrefixAny(rel, rules.allowed) {
			continue
		}

		fset, parsed := parseGoFile(t, root, rel)
		ast.Inspect(parsed, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				// Not a literal we can interpret; nothing to check.
				return true
			}

			position := fset.Position(lit.Pos())
			for _, forbidden := range rules.substrings {
				if strings.Contains(value, forbidden) {
					t.Errorf("%s:%d: string literal contains reference-project identifier %q\n"+
						"\tliteral: %s\n"+
						"\tCLAUDE.md scope rule 3 forbids hard-coding the reference project's names, paths, and services.\n"+
						"\tMove it to configuration, or record a deliberate exemption in %s.",
						rel, position.Line, forbidden, lit.Value, identifierRulesPath)
				}
			}
			for _, forbidden := range rules.exact {
				if value == forbidden {
					t.Errorf("%s:%d: string literal is the reference-project identifier %q\n"+
						"\tCLAUDE.md scope rule 3 forbids hard-coding the reference project's names, paths, and services.\n"+
						"\tMove it to configuration, or record a deliberate exemption in %s.",
						rel, position.Line, forbidden, identifierRulesPath)
				}
			}
			return true
		})
	}
}

const identifierRulesPath = "internal/guard/testdata/reference-identifiers.txt"

type identifierRules struct {
	substrings []string
	exact      []string
	allowed    []string
}

func loadIdentifierRules(t *testing.T) identifierRules {
	t.Helper()

	path := filepath.Join("testdata", "reference-identifiers.txt")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var rules identifierRules
	for number, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		directive, value, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("%s:%d: expected \"<directive>: <value>\", got %q", path, number+1, line)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			t.Fatalf("%s:%d: directive %q has an empty value", path, number+1, directive)
		}

		switch strings.TrimSpace(directive) {
		case "substring":
			rules.substrings = append(rules.substrings, value)
		case "exact":
			rules.exact = append(rules.exact, value)
		case "allow":
			rules.allowed = append(rules.allowed, value)
		default:
			t.Fatalf("%s:%d: unknown directive %q", path, number+1, directive)
		}
	}

	if len(rules.substrings) == 0 && len(rules.exact) == 0 {
		t.Fatalf("%s defines no forbidden identifiers", path)
	}
	return rules
}
