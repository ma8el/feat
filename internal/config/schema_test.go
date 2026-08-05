package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
)

// schemaFile is the JSON Schema published for editor support.
const schemaFile = "../../schema/feat-project.schema.json"

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

// load reads the published schema.
func loadSchema(t *testing.T) *schema {
	t.Helper()

	body, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("reading %s: %v", schemaFile, err)
	}

	var document schema
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		// The struct above covers the keywords this test walks. A keyword it
		// does not know means the schema grew something this test should learn
		// about rather than ignore.
		t.Fatalf("the schema uses a keyword this test does not model: %v", err)
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

// compareObject checks one struct against one schema object.
func compareObject(t *testing.T, root, node *schema, structType reflect.Type, path string) {
	t.Helper()

	fields := yamlFields(structType)
	described := node.Properties

	for name := range fields {
		if _, ok := described[name]; !ok {
			t.Errorf("%s: the Go type has %q and the schema does not\n"+
				"\tAdd it to %s, or the field will work but be reported as unknown by an editor.",
				join(path), name, schemaFile)
		}
	}
	for name := range described {
		if _, ok := fields[name]; !ok {
			t.Errorf("%s: the schema has %q and the Go type does not\n"+
				"\tRemove it from %s, or configuration an editor accepts will be rejected by Feat.",
				join(path), name, schemaFile)
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

// TestSchemaDescribesEveryField keeps the schema useful in an editor, where the
// description is the whole point of publishing one.
func TestSchemaDescribesEveryField(t *testing.T) {
	root := loadSchema(t)

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
		{"$defs.capabilities.properties.github_cli", cfg.Agent.Capabilities.GitHubCLI},
		{"$defs.capabilities.properties.gitlab_cli", cfg.Agent.Capabilities.GitLabCLI},
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
