package guard

import (
	"go/ast"
	"go/token"
	"strconv"
	"testing"
)

// unixNetworks are the network names a Unix-domain socket may use.
var unixNetworks = map[string]bool{
	"unix":       true,
	"unixgram":   true,
	"unixpacket": true,
}

// forbiddenListeners are the ways to open a listener that cannot be a
// Unix-domain socket. Calling one is a TCP or UDP listener by construction.
var forbiddenListeners = map[string]string{
	"ListenTCP":           "opens a TCP listener",
	"ListenUDP":           "opens a UDP listener",
	"ListenMulticastUDP":  "opens a UDP listener",
	"ListenAndServe":      "opens a TCP listener",
	"ListenAndServeTLS":   "opens a TCP listener",
	"ListenAndServeMutex": "opens a TCP listener",
}

// networkArgIndex maps the functions that take a network name to its position.
var networkArgIndex = map[string]int{
	"Listen":       0,
	"ListenPacket": 0,
	"Dial":         0,
	"DialTimeout":  0,
	"DialContext":  1,
}

// TestNoNetworkListenerOrDial checks that no TCP listener is opened, and the
// security rule that the local API is a Unix-domain socket only (ADR-009).
//
// It is a source rule rather than a runtime assertion because that is where it
// can be complete: a test can only observe the listeners a particular code path
// opened, while this covers every one in the repository, including in tests.
// Tests are deliberately included — an HTTP test server that binds a loopback
// port would make the rule true of the product and false of its test suite.
func TestNoNetworkListenerOrDial(t *testing.T) {
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
			if !ok {
				return true
			}
			// Only the standard library packages that can open a socket are
			// interesting; a method with the same name on another type is not.
			if pkg.Name != "net" && pkg.Name != "http" && pkg.Name != "httptest" {
				return true
			}
			name := selector.Sel.Name
			line := fset.Position(call.Pos()).Line

			if reason, forbidden := forbiddenListeners[name]; forbidden {
				t.Errorf("%s:%d: %s.%s %s\n"+
					"\tThe local API is a Unix-domain socket only, and no TCP listener may be opened\n"+
					"\t(docs/05-security-model.md, ADR-009).",
					rel, line, pkg.Name, name, reason)
				return true
			}

			index, checked := networkArgIndex[name]
			if !checked || len(call.Args) <= index {
				return true
			}
			literal, ok := call.Args[index].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				// A network chosen at runtime. Nothing in Feat does this, and if
				// something starts to, the reviewer should see a variable
				// network rather than a hidden "tcp".
				t.Errorf("%s:%d: %s.%s takes its network from a variable\n"+
					"\tPass \"unix\" literally, so that this rule can be checked.",
					rel, line, pkg.Name, name)
				return true
			}
			network, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			if !unixNetworks[network] {
				t.Errorf("%s:%d: %s.%s uses the %q network\n"+
					"\tFeat listens and dials on Unix-domain sockets only (ADR-009).",
					rel, line, pkg.Name, name, network)
			}
			return true
		})
	}
}
