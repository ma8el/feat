package feat_test

import (
	"bytes"
	"os"
	"testing"

	feat "github.com/ma8el/feat"
)

// TestTheEmittedDocumentsAreTheRepositorysOwn pins each embed to the canonical
// file it claims to be.
//
// An embed cannot drift from the path it names, but it can be re-pointed at a
// copy, and a copy is a second document to keep in step — the drift ADR-093
// refuses. Comparing against the canonical path keeps "the binary prints the
// repository's file" a checked property rather than a remembered one.
func TestTheEmittedDocumentsAreTheRepositorysOwn(t *testing.T) {
	documents := []struct {
		name string
		path string
		got  []byte
	}{
		{"project schema", "schema/feat-project.schema.json", feat.ProjectSchema()},
		{"project example", "docs/examples/project.yaml", feat.ProjectExample()},
	}

	for _, document := range documents {
		want, err := os.ReadFile(document.path)
		if err != nil {
			t.Fatalf("reading %s: %v", document.path, err)
		}
		if len(want) == 0 {
			t.Fatalf("%s is empty, and an empty document is nothing to emit", document.path)
		}
		if !bytes.Equal(document.got, want) {
			t.Errorf("the embedded %s is not %s\n"+
				"\tThe embed must name the canonical file, not a copy of it (ADR-093).",
				document.name, document.path)
		}
	}
}
