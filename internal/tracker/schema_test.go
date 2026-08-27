package tracker

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ticketSchemaFile is the shape Feat publishes for a tracker command's output.
const ticketSchemaFile = "../../schema/feat-tickets.schema.json"

// schema is enough of JSON Schema to read what this package enforces.
type schema struct {
	Ref                  string             `json:"$ref"`
	Type                 string             `json:"type"`
	Items                *schema            `json:"items"`
	Properties           map[string]*schema `json:"properties"`
	Required             []string           `json:"required"`
	MinLength            int                `json:"minLength"`
	AdditionalProperties *bool              `json:"additionalProperties"`
	Defs                 map[string]*schema `json:"$defs"`
}

// TestParseEnforcesThePublishedShape keeps the schema and this package from
// drifting apart.
//
// Both are hand-written, and they are two halves of one promise: the schema is
// what a user writes their command against, and this package is what accepts
// the command's output. A property added to one and not the other would mean
// either a field an editor accepts and Feat refuses, or a field Feat carries
// that nothing documents (ADR-071).
func TestParseEnforcesThePublishedShape(t *testing.T) {
	ticket := publishedTicket(t)

	t.Run("properties", func(t *testing.T) {
		published := names(ticket.Properties)
		enforced := append([]string(nil), ticketProperties...)
		sort.Strings(enforced)
		if !reflect.DeepEqual(published, enforced) {
			t.Errorf("the schema publishes %v and this package knows %v", published, enforced)
		}
		for _, name := range published {
			if !strings.Contains(fieldTag(t, name), name) {
				t.Errorf("the Ticket type does not decode %q", name)
			}
		}
	})

	t.Run("required", func(t *testing.T) {
		published := append([]string(nil), ticket.Required...)
		sort.Strings(published)
		enforced := append([]string(nil), requiredProperties...)
		sort.Strings(enforced)
		if !reflect.DeepEqual(published, enforced) {
			t.Errorf("the schema requires %v and this package requires %v", published, enforced)
		}
	})

	t.Run("minimum length", func(t *testing.T) {
		var published []string
		for name, property := range ticket.Properties {
			if property.MinLength > 0 {
				published = append(published, name)
			}
		}
		sort.Strings(published)
		enforced := append([]string(nil), valuedProperties...)
		sort.Strings(enforced)
		if !reflect.DeepEqual(published, enforced) {
			t.Errorf("the schema gives %v a minimum length and this package insists on %v",
				published, enforced)
		}
	})

	t.Run("nothing beyond the shape", func(t *testing.T) {
		if ticket.AdditionalProperties == nil || *ticket.AdditionalProperties {
			t.Error("the schema allows properties beyond the published shape, " +
				"and this package refuses one as a mapping mistake")
		}
	})
}

// TestTheDocumentIsAListOfTickets pins the one thing about the document itself:
// a tracker command prints a list, so a single ticket is a mapping mistake with
// a name rather than a list of one.
func TestTheDocumentIsAListOfTickets(t *testing.T) {
	root := readSchema(t)
	if root.Type != "array" {
		t.Errorf("the schema's document is %q, and this package reads a list", root.Type)
	}
}

// publishedTicket returns the schema of one ticket.
func publishedTicket(t *testing.T) *schema {
	t.Helper()

	root := readSchema(t)
	if root.Items == nil {
		t.Fatal("the schema describes no ticket")
	}
	name := strings.TrimPrefix(root.Items.Ref, "#/$defs/")
	ticket, ok := root.Defs[name]
	if !ok {
		t.Fatalf("the schema refers to %q, which it does not define", root.Items.Ref)
	}
	return ticket
}

func readSchema(t *testing.T) *schema {
	t.Helper()

	body, err := os.ReadFile(ticketSchemaFile)
	if err != nil {
		t.Fatalf("reading %s: %v", ticketSchemaFile, err)
	}
	var root schema
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("reading %s: %v", ticketSchemaFile, err)
	}
	return &root
}

func names(properties map[string]*schema) []string {
	out := make([]string, 0, len(properties))
	for name := range properties {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// fieldTag returns the JSON tag of the Ticket field decoding a property.
func fieldTag(t *testing.T, property string) string {
	t.Helper()

	structType := reflect.TypeOf(Ticket{})
	for i := range structType.NumField() {
		tag := structType.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name == property {
			return tag
		}
	}
	return ""
}
