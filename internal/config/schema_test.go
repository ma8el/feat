package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/schematest"
)

// The JSON Schemas Feat publishes.
const (
	// schemaFile describes the project configuration file, for editor support.
	schemaFile = "../../schema/feat-project.schema.json"
	// settingsSchemaFile describes the global settings file. It is a second
	// document rather than a section of the first, because the two files are
	// two files: an editor asked about ~/.config/feat/settings.yaml has to be
	// told what that one may contain (ADR-079).
	settingsSchemaFile = "../../schema/feat-settings.schema.json"
	// ticketSchemaFile describes what a project's tracker command prints. It is
	// the other half of the tracker section in this package: the configuration
	// says which command to run, and this says what its output has to be
	// (ADR-071).
	ticketSchemaFile = "../../schema/feat-tickets.schema.json"
)

// readSchema reads one published schema.
//
// The model it decodes into, and the walk below, are internal/schematest's: the
// same technique holds the output documents to their own types, and the two
// cannot share a test file because a file under internal/config may not import
// internal/api (ADR-094, ADR-099).
func readSchema(t *testing.T, path string) *schematest.Schema {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return schematest.Read(t, path, body)
}

// loadSchema reads the published project schema.
func loadSchema(t *testing.T) *schematest.Schema { return readSchema(t, schemaFile) }

// against compares one schema with the configuration structs it describes.
func against(root *schematest.Schema) schematest.Comparison {
	return schematest.Comparison{Root: root, Tag: "yaml", Fallback: schemaFile}
}

// TestSchemaMatchesTheConfigurationStructs keeps the published schema and the
// Go types from drifting apart.
//
// The schema is hand-written, so nothing makes it follow a struct on its own.
// A field added to Config without a schema entry would be accepted by Feat and
// underlined by the user's editor; a field left in the schema after it was
// removed would be the reverse. Both directions are checked.
func TestSchemaMatchesTheConfigurationStructs(t *testing.T) {
	root := loadSchema(t)
	against(root).CompareObject(t, root, reflect.TypeOf(config.Config{}), "")
}

// TestSettingsSchemaMatchesTheSettingsStruct is the same drift check for the
// global settings file, which is the other document a user hand-edits.
func TestSettingsSchemaMatchesTheSettingsStruct(t *testing.T) {
	root := readSchema(t, settingsSchemaFile)
	against(root).CompareObject(t, root, reflect.TypeOf(config.Settings{}), "")
}

// TestSchemaDescribesEveryField keeps the schemas useful in an editor, where the
// description is the whole point of publishing one.
func TestSchemaDescribesEveryField(t *testing.T) {
	for _, file := range []string{schemaFile, settingsSchemaFile, ticketSchemaFile} {
		t.Run(filepath.Base(file), func(t *testing.T) {
			schematest.Described(t, readSchema(t, file))
		})
	}
}

// TestTicketSchemaPublishesTheShapeFeatActsOn pins the published ticket shape.
//
// It is the contract a user's tracker command is written against, so a field
// added to it is a field every one of those commands may have to produce, and a
// field removed is one they may already be producing. ADR-071 sizes it by what
// Feat acts on: a reference, a title, a body, a URL, a state, and an optional
// source. Anything richer belongs in the brief.
func TestTicketSchemaPublishesTheShapeFeatActsOn(t *testing.T) {
	root := readSchema(t, ticketSchemaFile)

	if root.Type != "array" {
		t.Errorf("the document is %v, and a tracker command prints a list of tickets", root.Type)
	}
	if root.Items == nil {
		t.Fatal("the document describes no ticket")
	}
	ticket := root.Items.Resolve(t, root)

	want := []string{"reference", "title", "body", "url", "state", "source"}
	var got []string
	for name := range ticket.Properties {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a ticket carries %v, and the published shape is %v", got, want)
	}

	// Everything but the source, which a project drawing on one tracker has
	// nothing to disambiguate.
	required := append([]string(nil), ticket.Required...)
	sort.Strings(required)
	if !reflect.DeepEqual(required, []string{"body", "reference", "state", "title", "url"}) {
		t.Errorf("a ticket requires %v", required)
	}

	if string(ticket.AdditionalProperties) != "false" {
		t.Errorf("a ticket allows properties beyond the published shape: %s\n"+
			"\tFeat carries what it acts on, so a field it does not know is a mapping mistake "+
			"rather than something to keep.", ticket.AdditionalProperties)
	}
}

// TestSchemaAcceptsTheExampleConfigurations checks the schema against the files
// it is meant to describe, to the extent this test models JSON Schema: every
// field present in an example must be a field the schema knows.
func TestSchemaAcceptsTheExampleConfigurations(t *testing.T) {
	root := loadSchema(t)

	examples, err := filepath.Glob(filepath.Join("testdata", "projects", "*.yaml"))
	if err != nil {
		t.Fatalf("listing the examples: %v", err)
	}
	examples = append(examples, filepath.Join("..", "..", "docs", "examples", "project.yaml"))
	sort.Strings(examples)

	for _, example := range examples {
		t.Run(filepath.Base(example), func(t *testing.T) {
			body, err := os.ReadFile(example)
			if err != nil {
				t.Skipf("no example at %s", example)
			}
			parsed, err := config.Parse("", body)
			if err != nil {
				t.Fatalf("the example does not parse: %v", err)
			}
			// Parsing already proved every field is one the Go type knows, and
			// the drift test proved the schema knows the same fields. What is
			// left is that the enumerated values in the example are values the
			// schema allows.
			checkEnums(t, root, parsed)
		})
	}
}

// checkEnums verifies the example's closed-vocabulary values against the
// schema, since a wrong value there is the mistake a schema most helps with.
func checkEnums(t *testing.T, root *schematest.Schema, cfg *config.Config) {
	t.Helper()

	for _, check := range []struct {
		path  string
		value string
	}{
		{"$defs.git.properties.base_policy", cfg.Git.BasePolicy},
		{"$defs.agent.properties.provider", cfg.Agent.Provider},
		{"$defs.execution.properties.mode", cfg.Agent.Execution.Mode},
		{"$defs.capabilities.properties.docker", cfg.Agent.Capabilities.Docker},
	} {
		if check.value == "" {
			// Absent in the file, so a default applies and the schema records
			// it rather than the file.
			continue
		}
		allowed := vocabulary(t, root, check.path)
		if len(allowed) == 0 {
			continue
		}
		if !containsString(allowed, check.value) {
			t.Errorf("the example sets %s to %q, which the schema does not allow: %v",
				check.path, check.value, allowed)
		}
	}
}

// vocabulary returns the values a schema node allows.
func vocabulary(t *testing.T, root *schematest.Schema, path string) []string {
	t.Helper()

	node := root
	for _, segment := range strings.Split(path, ".") {
		switch segment {
		case "$defs":
			continue
		case "properties":
			continue
		default:
			if next, ok := node.Defs[segment]; ok && node.Defs != nil {
				node = next
				continue
			}
			next, ok := node.Properties[segment]
			if !ok {
				t.Fatalf("the schema has no node at %s", path)
			}
			node = next
		}
	}

	var allowed []string
	if node.Const != nil {
		allowed = append(allowed, node.Const.(string))
	}
	for _, value := range node.Enum {
		if text, ok := value.(string); ok {
			allowed = append(allowed, text)
		}
	}
	return allowed
}

func containsString(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
