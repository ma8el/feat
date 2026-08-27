package config_test

import (
	"encoding/json"
	"os"
	gopath "path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
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

// schema is enough of JSON Schema to walk the document's shape.
//
// It is decoded strictly, so a keyword added to the published schema fails here
// rather than being walked past. The alternative is a drift test that quietly
// stops covering the part of the schema it does not understand.
type schema struct {
	Schema               string             `json:"$schema"`
	ID                   string             `json:"$id"`
	Title                string             `json:"title"`
	Ref                  string             `json:"$ref"`
	Type                 any                `json:"type"`
	Properties           map[string]*schema `json:"properties"`
	PropertyNames        *schema            `json:"propertyNames"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Items                *schema            `json:"items"`
	MinItems             int                `json:"minItems"`
	MinLength            int                `json:"minLength"`
	MinProperties        int                `json:"minProperties"`
	Pattern              string             `json:"pattern"`
	Default              any                `json:"default"`
	Defs                 map[string]*schema `json:"$defs"`
	Description          string             `json:"description"`
	Required             []string           `json:"required"`
	Enum                 []any              `json:"enum"`
	Const                any                `json:"const"`
}

// loadSchema reads the published project schema.
func loadSchema(t *testing.T) *schema { return readSchema(t, schemaFile) }

// readSchema reads one published schema.
func readSchema(t *testing.T, path string) *schema {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var document schema
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		// The struct above covers the keywords this test walks. A keyword it
		// does not know means the schema grew something this test should learn
		// about rather than ignore.
		t.Fatalf("%s uses a keyword this test does not model: %v", path, err)
	}
	return &document
}

// resolve follows a local "$ref".
func (s *schema) resolve(t *testing.T, root *schema) *schema {
	t.Helper()
	if s.Ref == "" {
		return s
	}
	name := strings.TrimPrefix(s.Ref, "#/$defs/")
	target, ok := root.Defs[name]
	if !ok {
		t.Fatalf("the schema refers to %q, which it does not define", s.Ref)
	}
	return target
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
	compareObject(t, root, root, reflect.TypeOf(config.Config{}), "")
}

// TestSettingsSchemaMatchesTheSettingsStruct is the same drift check for the
// global settings file, which is the other document a user hand-edits.
func TestSettingsSchemaMatchesTheSettingsStruct(t *testing.T) {
	root := readSchema(t, settingsSchemaFile)
	compareObject(t, root, root, reflect.TypeOf(config.Settings{}), "")
}

// compareObject checks one struct against one schema object.
func compareObject(t *testing.T, root, node *schema, structType reflect.Type, path string) {
	t.Helper()

	fields := yamlFields(structType)
	described := node.Properties

	for name := range fields {
		if _, ok := described[name]; !ok {
			t.Errorf("%s: the Go type has %q and the schema does not\n"+
				"\tAdd it to %s, or the field will work but be reported as unknown by an editor.",
				join(path), name, document(root))
		}
	}
	for name := range described {
		if _, ok := fields[name]; !ok {
			t.Errorf("%s: the schema has %q and the Go type does not\n"+
				"\tRemove it from %s, or configuration an editor accepts will be rejected by Feat.",
				join(path), name, document(root))
		}
	}

	for name, field := range fields {
		property, ok := described[name]
		if !ok {
			continue
		}
		compareField(t, root, property, field, join(path)+"."+name)
	}
}

// compareField descends into a field's type.
func compareField(t *testing.T, root, property *schema, fieldType reflect.Type, path string) {
	t.Helper()

	property = property.resolve(t, root)

	switch fieldType.Kind() {
	case reflect.Pointer:
		compareField(t, root, property, fieldType.Elem(), path)

	case reflect.Struct:
		compareObject(t, root, property, fieldType, path)

	case reflect.Slice:
		element := fieldType.Elem()
		if element.Kind() != reflect.Struct {
			return
		}
		if property.Items == nil {
			t.Errorf("%s: the Go type is a list of objects and the schema describes no items", path)
			return
		}
		compareObject(t, root, property.Items.resolve(t, root), element, path+"[]")

	case reflect.Map:
		// A mapping keyed by an identifier: repositories, checks, and external
		// resources. The schema describes the value under
		// additionalProperties.
		values := additional(t, root, property)
		if values == nil {
			t.Errorf("%s: the Go type is a mapping and the schema describes no value shape", path)
			return
		}
		element := fieldType.Elem()
		if element.Kind() == reflect.Slice {
			element = element.Elem()
			if values.Items == nil {
				t.Errorf("%s: the Go type is a mapping to lists and the schema describes no items", path)
				return
			}
			values = values.Items.resolve(t, root)
		}
		if element.Kind() == reflect.Struct {
			compareObject(t, root, values, element, path+".*")
		}
	}
}

// additional returns the schema of a mapping's values.
func additional(t *testing.T, root, node *schema) *schema {
	t.Helper()
	if len(node.AdditionalProperties) == 0 {
		return nil
	}
	var values schema
	if err := json.Unmarshal(node.AdditionalProperties, &values); err != nil {
		// "additionalProperties": false, which is a closed object rather than a
		// mapping.
		return nil
	}
	return values.resolve(t, root)
}

// yamlFields returns a struct's YAML field names.
func yamlFields(structType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for i := range structType.NumField() {
		field := structType.Field(i)
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			// Unexported bookkeeping such as the parsed durations and the file
			// the configuration came from.
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		fields[name] = field.Type
	}
	return fields
}

func join(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}

// document names the schema file a drift message should point at.
//
// It is taken from the schema's own "$id", so that a message names the file the
// reader has to edit rather than whichever one this test was first written for.
func document(root *schema) string {
	if root.ID == "" {
		return schemaFile
	}
	return "schema/" + gopath.Base(root.ID)
}

// TestSchemaDescribesEveryField keeps the schemas useful in an editor, where the
// description is the whole point of publishing one.
func TestSchemaDescribesEveryField(t *testing.T) {
	for _, file := range []string{schemaFile, settingsSchemaFile, ticketSchemaFile} {
		t.Run(filepath.Base(file), func(t *testing.T) {
			root := readSchema(t, file)

			var walk func(node *schema, path string)
			walk = func(node *schema, path string) {
				for name, property := range node.Properties {
					where := path + "." + name
					if property.Description == "" && property.Ref == "" {
						t.Errorf("%s has no description", where)
					}
					if property.Ref == "" {
						walk(property, where)
					}
				}
			}
			walk(root, "")
			for name, definition := range root.Defs {
				walk(definition, "$defs."+name)
			}
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
	ticket := root.Items.resolve(t, root)

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
func checkEnums(t *testing.T, root *schema, cfg *config.Config) {
	t.Helper()

	for _, check := range []struct {
		path  string
		value string
	}{
		{"$defs.git.properties.base_policy", cfg.Git.BasePolicy},
		{"$defs.agent.properties.provider", cfg.Agent.Provider},
		{"$defs.execution.properties.mode", cfg.Agent.Execution.Mode},
		{"$defs.capabilities.properties.docker", cfg.Agent.Capabilities.Docker},
		{"$defs.capabilities.properties.network", cfg.Agent.Capabilities.Network},
		{"$defs.capabilities.properties.git", cfg.Agent.Capabilities.Git},
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
func vocabulary(t *testing.T, root *schema, path string) []string {
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
