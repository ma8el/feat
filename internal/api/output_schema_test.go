package api_test

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/schematest"
)

// outputSchemaFile describes the documents `feat` prints when it is asked for
// one with --json.
//
// It is not published as a compatibility promise. What it does is say what this
// build prints, in one place a person can read, so that changing the shape is a
// decision somebody made rather than a consequence of editing a struct. What is
// promised about it is a later decision (ADR-099).
const outputSchemaFile = "../../schema/feat-output.schema.json"

// documents are the printed shapes, by the name each carries in the schema.
//
// Two of them are the daemon's own answers, unchanged: a command that prints a
// document prints what it was already given, so there is one model for the
// socket and for the command line rather than a second to keep in step. The
// other two are the shapes that had none — the envelope a list needs to be an
// object, and the resolved project configuration, which `feat project show`
// reads from the configuration directory rather than over the socket.
var documents = map[string]reflect.Type{
	"task_list":             reflect.TypeOf(api.TaskList{}),
	"review_status":         reflect.TypeOf(api.ReviewStatus{}),
	"runtime_status":        reflect.TypeOf(api.RuntimeStatus{}),
	"project_configuration": reflect.TypeOf(api.ProjectConfiguration{}),
}

// TestOutputSchemaMatchesTheGoTypes keeps the published shape and the types
// that produce it from drifting apart.
//
// The schema is hand-written, so nothing makes it follow a struct on its own. A
// field added to a document without a schema entry would be printed and
// undocumented; a field left in the schema after it was removed would promise a
// caller something nothing produces. Both directions are checked, which is the
// treatment schema/feat-project.schema.json already gets (ADR-028).
func TestOutputSchemaMatchesTheGoTypes(t *testing.T) {
	root := readOutputSchema(t)
	comparison := schematest.Comparison{Root: root, Tag: "json", Fallback: outputSchemaFile}

	for _, name := range documentNames() {
		t.Run(name, func(t *testing.T) {
			document, ok := root.Defs[name]
			if !ok {
				t.Fatalf("the schema describes no %q, and a command prints one\n"+
					"\tAdd it to %s.", name, outputSchemaFile)
			}
			comparison.CompareObject(t, document, documents[name], name)
		})
	}
}

// TestOutputSchemaDescribesEveryField keeps the document worth reading. A
// caller asking what a field means is the only reason to write a schema by
// hand rather than derive one.
func TestOutputSchemaDescribesEveryField(t *testing.T) {
	schematest.Described(t, readOutputSchema(t))
}

// TestEveryDefinitionIsReachedByADocument guards the direction the drift check
// cannot see: a type that stopped being printed leaves its definition behind,
// and a definition nothing reaches describes a shape no command produces.
func TestEveryDefinitionIsReachedByADocument(t *testing.T) {
	root := readOutputSchema(t)

	reached := map[string]bool{}
	var walk func(node *schematest.Schema)
	walk = func(node *schematest.Schema) {
		if node == nil {
			return
		}
		if name, ok := reference(node.Ref); ok {
			if reached[name] {
				return
			}
			reached[name] = true
			walk(root.Defs[name])
			return
		}
		for _, property := range node.Properties {
			walk(property)
		}
		walk(node.Items)
	}
	for name := range documents {
		reached[name] = true
		walk(root.Defs[name])
	}

	var orphaned []string
	for name := range root.Defs {
		if !reached[name] {
			orphaned = append(orphaned, name)
		}
	}
	sort.Strings(orphaned)
	if len(orphaned) > 0 {
		t.Errorf("%s defines %v, which no printed document reaches\n"+
			"\tEither a command stopped printing it, or it was never printed. Remove it.",
			outputSchemaFile, orphaned)
	}
}

// reference returns the definition a local "$ref" names.
func reference(ref string) (string, bool) {
	const prefix = "#/$defs/"
	if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
		return "", false
	}
	return ref[len(prefix):], true
}

func readOutputSchema(t *testing.T) *schematest.Schema {
	t.Helper()

	body, err := os.ReadFile(outputSchemaFile)
	if err != nil {
		t.Fatalf("reading %s: %v", outputSchemaFile, err)
	}
	return schematest.Read(t, outputSchemaFile, body)
}

func documentNames() []string {
	names := make([]string, 0, len(documents))
	for name := range documents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
