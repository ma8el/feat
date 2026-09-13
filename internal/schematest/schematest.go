// Package schematest holds a published JSON Schema against the Go types it
// describes.
//
// Feat publishes hand-written schemas, so nothing makes one follow a struct on
// its own. A field added to a type without a schema entry would work and be
// underlined by whatever reads the schema; a field left in the schema after it
// was removed would be the reverse. Both directions are what this checks.
//
// It is a package rather than a helper in whichever test wrote it first because
// there are two kinds of document now and they live on opposite sides of a
// depguard rule: the configuration a user writes is internal/config's, and the
// output a command prints is internal/api's, which files under internal/config
// may not import (ADR-099). The technique is one technique, so it is in one
// place — which is what ADR-094 asks for wherever duplication is not mandated
// by a boundary.
//
// It models enough of JSON Schema to walk a document's shape and no more, and
// it decodes strictly: a keyword added to a published schema fails here rather
// than being walked past. The alternative is a drift check that quietly stops
// covering the part of the schema it does not understand.
package schematest

import (
	"encoding/json"
	gopath "path"
	"reflect"
	"strings"
	"testing"
)

// Schema is enough of JSON Schema to walk a document's shape.
type Schema struct {
	Schema               string             `json:"$schema"`
	ID                   string             `json:"$id"`
	Title                string             `json:"title"`
	Ref                  string             `json:"$ref"`
	Type                 any                `json:"type"`
	Properties           map[string]*Schema `json:"properties"`
	PropertyNames        *Schema            `json:"propertyNames"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Items                *Schema            `json:"items"`
	MinItems             int                `json:"minItems"`
	MinLength            int                `json:"minLength"`
	MinProperties        int                `json:"minProperties"`
	Pattern              string             `json:"pattern"`
	Format               string             `json:"format"`
	Default              any                `json:"default"`
	Defs                 map[string]*Schema `json:"$defs"`
	Description          string             `json:"description"`
	Required             []string           `json:"required"`
	Enum                 []any              `json:"enum"`
	Const                any                `json:"const"`
}

// Read decodes one published schema from the bytes of its file.
//
// path names the file in the failure message, so that a reader is sent to the
// document rather than to this package.
func Read(t *testing.T, path string, body []byte) *Schema {
	t.Helper()

	var document Schema
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		// The struct above covers the keywords this package walks. A keyword it
		// does not know means the schema grew something this check should learn
		// about rather than ignore.
		t.Fatalf("%s uses a keyword this check does not model: %v", path, err)
	}
	return &document
}

// Resolve follows a local "$ref".
func (s *Schema) Resolve(t *testing.T, root *Schema) *Schema {
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

// Comparison is one schema checked against one set of Go types.
//
// Tag is the struct tag the field names come from: "yaml" for a configuration
// file a user writes, "json" for a document a command prints. Fallback names
// the schema file when the document carries no "$id" of its own.
type Comparison struct {
	Root     *Schema
	Tag      string
	Fallback string
}

// CompareObject checks one struct against one schema object, in both
// directions, and descends into every field.
//
// path is where the object sits in the document, and is empty at the root.
func (c Comparison) CompareObject(t *testing.T, node *Schema, structType reflect.Type, path string) {
	t.Helper()

	fields := c.fields(structType)
	described := node.Properties

	for name := range fields {
		if _, ok := described[name]; !ok {
			t.Errorf("%s: the Go type has %q and the schema does not\n"+
				"\tAdd it to %s, or the field will be there and be reported as unknown by whatever reads it.",
				join(path), name, c.document())
		}
	}
	for name := range described {
		if _, ok := fields[name]; !ok {
			t.Errorf("%s: the schema has %q and the Go type does not\n"+
				"\tRemove it from %s, or the schema promises a field nothing produces.",
				join(path), name, c.document())
		}
	}

	for name, field := range fields {
		property, ok := described[name]
		if !ok {
			continue
		}
		c.compareField(t, property, field, join(path)+"."+name)
	}
}

// compareField descends into a field's type.
func (c Comparison) compareField(t *testing.T, property *Schema, fieldType reflect.Type, path string) {
	t.Helper()

	property = property.Resolve(t, c.Root)

	switch fieldType.Kind() {
	case reflect.Pointer:
		c.compareField(t, property, fieldType.Elem(), path)

	case reflect.Struct:
		if isOpaque(fieldType) {
			// A type that marshals itself, such as a timestamp. Its fields are
			// not the document's fields.
			return
		}
		c.CompareObject(t, property, fieldType, path)

	case reflect.Slice:
		element := fieldType.Elem()
		if element.Kind() != reflect.Struct || isOpaque(element) {
			return
		}
		if property.Items == nil {
			t.Errorf("%s: the Go type is a list of objects and the schema describes no items", path)
			return
		}
		c.CompareObject(t, property.Items.Resolve(t, c.Root), element, path+"[]")

	case reflect.Map:
		// A mapping keyed by an identifier: repositories, checks, and external
		// resources. The schema describes the value under additionalProperties.
		values := c.Additional(t, property)
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
			values = values.Items.Resolve(t, c.Root)
		}
		if element.Kind() == reflect.Struct && !isOpaque(element) {
			c.CompareObject(t, values, element, path+".*")
		}
	}
}

// Additional returns the schema of a mapping's values, or nil where the node is
// a closed object rather than a mapping.
func (c Comparison) Additional(t *testing.T, node *Schema) *Schema {
	t.Helper()
	if len(node.AdditionalProperties) == 0 {
		return nil
	}
	var values Schema
	if err := json.Unmarshal(node.AdditionalProperties, &values); err != nil {
		// "additionalProperties": false, which is a closed object rather than a
		// mapping.
		return nil
	}
	return values.Resolve(t, c.Root)
}

// fields returns a struct's field names under the tag this comparison reads.
func (c Comparison) fields(structType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for i := range structType.NumField() {
		field := structType.Field(i)
		tag := field.Tag.Get(c.Tag)
		if tag == "" || tag == "-" {
			// Bookkeeping the document does not carry, such as the parsed
			// durations and the file a configuration came from.
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		fields[name] = field.Type
	}
	return fields
}

// document names the schema file a drift message should point at.
//
// It is taken from the schema's own "$id", so that a message names the file the
// reader has to edit rather than whichever one a check was first written for.
func (c Comparison) document() string {
	if c.Root.ID == "" {
		return c.Fallback
	}
	return "schema/" + gopath.Base(c.Root.ID)
}

// Described reports every property that carries no description, which is the
// whole point of publishing a schema: a reader of one is asking what a field
// means.
func Described(t *testing.T, root *Schema) {
	t.Helper()

	var walk func(node *Schema, path string)
	walk = func(node *Schema, path string) {
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

// isOpaque reports whether a struct marshals itself rather than by its fields.
//
// time.Time is the one that occurs here, and walking into it would demand that
// a schema describe the wall clock, the monotonic reading, and the location.
func isOpaque(structType reflect.Type) bool {
	return structType.PkgPath() == "time" && structType.Name() == "Time"
}

func join(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}
