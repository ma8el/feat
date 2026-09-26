package feat_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/schematest"
)

// publishedAt is where every published schema resolves (ADR-102).
const publishedAt = "https://raw.githubusercontent.com/ma8el/feat/main/schema/"

// TestThePublishedSchemasKeepTheirPromise holds each published schema to the
// facts a caller was promised: its names, types, required fields, and enumerated
// values, as recorded under testdata/published (ADR-102).
func TestThePublishedSchemasKeepTheirPromise(t *testing.T) {
	for _, published := range []struct {
		name string
		// configuration is true for a file a user writes, where a new required
		// field makes an existing file invalid.
		configuration bool
	}{
		{"feat-project", true},
		{"feat-output", false},
	} {
		t.Run(published.name, func(t *testing.T) {
			path := "schema/" + published.name + ".schema.json"
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			root := schematest.Read(t, path, body)
			if want := publishedAt + filepath.Base(path); root.ID != want {
				t.Errorf("%s has $id %q, want %q\n\tThe published URL does not move (ADR-102).", path, root.ID, want)
			}

			current := map[string]bool{}
			collect(t, root, "", current)
			record := filepath.Join("testdata", "published", published.name+".txt")
			recorded := readRecord(t, record)

			for fact := range recorded {
				if !current[fact] {
					t.Errorf("%s no longer says %q\n"+
						"\tA removed, renamed, or retyped field breaks ADR-102. Restore it, or make the break in a\n"+
						"\tminor release, list it in CHANGELOG.md, and delete the line from %s.", path, fact, record)
				}
			}
			var added []string
			for fact := range current {
				if recorded[fact] {
					continue
				}
				added = append(added, fact)
				field, isRequired := strings.CutSuffix(fact, " required")
				if published.configuration && isRequired && recorded[parent(field)+" type=object"] {
					t.Errorf("%s makes %s required, and a file written before it lacks it\n"+
						"\tAn added configuration field is optional (ADR-102).", path, field)
				}
			}
			if len(added) > 0 {
				sort.Strings(added)
				t.Errorf("%s says what %s does not record; add these lines to it:\n%s",
					path, record, strings.Join(added, "\n"))
			}
		})
	}
}

// collect records the facts one schema node states, under a dotted path.
func collect(t *testing.T, node *schematest.Schema, path string, facts map[string]bool) {
	t.Helper()
	at := path
	if at == "" {
		at = "(root)"
	}
	if node.Ref != "" {
		facts[at+" ref="+strings.TrimPrefix(node.Ref, "#/$defs/")] = true
	}
	if node.Type != nil {
		facts[fmt.Sprintf("%s type=%v", at, node.Type)] = true
	}
	for _, value := range append(node.Enum, node.Const) {
		if value != nil {
			encoded, _ := json.Marshal(value)
			facts[at+" = "+string(encoded)] = true
		}
	}
	for _, name := range node.Required {
		facts[join(path, name)+" required"] = true
	}
	for name, property := range node.Properties {
		collect(t, property, join(path, name), facts)
	}
	for name, def := range node.Defs {
		collect(t, def, join(path, "$defs."+name), facts)
	}
	if node.Items != nil {
		collect(t, node.Items, at+"[]", facts)
	}
	var values schematest.Schema
	if json.Unmarshal(node.AdditionalProperties, &values) == nil {
		collect(t, &values, join(path, "*"), facts)
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// parent is the object a dotted path's last field belongs to. $defs names are
// dotted too, so a field of a definition has the definition as its parent.
func parent(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return "(root)"
	}
	return path[:i]
}

func readRecord(t *testing.T, path string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	facts := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			facts[line] = true
		}
	}
	return facts
}
