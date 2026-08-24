package tracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MaxOutputBytes bounds what a tracker command may print.
//
// It is the bound a control message carries, for the reason a control message
// carries one: a ticket becomes a task brief, and a brief is what the agent is
// told to do (ADR-071). A document past the bound is refused by size rather
// than by the memory it took to find that out.
const MaxOutputBytes = 256 << 10

// Ticket is one ticket, in the shape schema/feat-tickets.schema.json publishes.
//
// Feat's shape is the contract rather than the tracker's, because reading an
// arbitrary shape would mean a mapping language in configuration and a mapping
// language has no end. Whatever the tracker is, the configured command maps its
// output onto this (ADR-071).
//
// It is sized by what Feat acts on rather than by what trackers offer: no story
// points, epics, sprints, or custom fields, which Feat would carry without
// doing anything with them. Anything richer belongs in the brief, which is
// Markdown and holds whatever the user wants.
type Ticket struct {
	// Reference is the tracker's own identifier for the ticket, as you would
	// type it to a person. Feat never parses one: it is matched against what
	// the command emitted and carried as it was read.
	Reference string `json:"reference"`
	// Title is the ticket's title.
	Title string `json:"title"`
	// Body is the ticket's description, and may be empty. It is text a brief is
	// composed from rather than instructions Feat acts on.
	Body string `json:"body"`
	// URL is where the ticket can be read.
	URL string `json:"url"`
	// State is the ticket's state in the tracker's own vocabulary. Feat maps it
	// onto no vocabulary of its own: trackers do not agree on states.
	State string `json:"state"`
	// Source is which tracker the ticket came from, and is empty for a project
	// drawing on one. A command that merges two labels each ticket with the one
	// it came from, and Feat carries it into the task's ticket reference as the
	// provider (ADR-071).
	Source string `json:"source,omitempty"`
}

// ticketProperties is the published shape's property list, in the order the
// schema declares them, so that a message about one reads the same way twice.
var ticketProperties = []string{"reference", "title", "body", "url", "state", "source"}

// requiredProperties are the properties the published shape requires. The
// source is not among them, because a project drawing on one tracker has
// nothing to disambiguate (ADR-071).
var requiredProperties = []string{"reference", "title", "body", "url", "state"}

// valuedProperties are the properties the published shape gives a minimum
// length. The body is not among them: a ticket filed with no description is a
// ticket, and the brief composed from it says so.
var valuedProperties = []string{"reference", "title", "url", "state"}

// Parse validates a tracker command's output against the published shape.
//
// Nothing partial is returned. A list Feat half-read would mean a picker
// offering some of somebody's tickets without saying which are missing, and the
// remedy for either failure is the same: fix the command's mapping. `feat
// doctor` runs this so that a tracker emitting the wrong shape is found when
// the user asks whether the project is configured (ADR-071).
func Parse(output []byte) ([]Ticket, error) {
	if len(output) > MaxOutputBytes {
		return nil, &RejectionError{Oversized: true, Reason: fmt.Sprintf(
			"is %d bytes, and the limit is %d", len(output), MaxOutputBytes)}
	}

	document := bytes.TrimSpace(output)
	if len(document) == 0 {
		return nil, &RejectionError{
			Reason: "is empty, and a tracker command prints a list of tickets, " +
				"which is `[]` when the user has none",
		}
	}

	var entries []json.RawMessage
	if err := decode(document, &entries); err != nil {
		var mismatch *json.UnmarshalTypeError
		if errors.As(err, &mismatch) {
			return nil, &RejectionError{Reason: "is " + describeJSON(mismatch.Value) +
				", and a tracker command prints a list of tickets"}
		}
		return nil, &RejectionError{Reason: "is not JSON: " + err.Error()}
	}

	tickets := make([]Ticket, 0, len(entries))
	for i, entry := range entries {
		ticket, err := parseTicket(entry)
		if err != nil {
			return nil, &RejectionError{Position: i + 1, Reason: err.Error()}
		}
		tickets = append(tickets, ticket)
	}
	return tickets, nil
}

// decode reads one JSON document and refuses anything after it, so that a
// command printing a list followed by a warning is a mistake with a name rather
// than a list that silently loses the warning.
func decode(document []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("carries more than one document, and a tracker command prints one list")
	}
	return nil
}

// parseTicket checks one entry against the published shape.
//
// The properties are read as raw values first, because the two things the
// schema says that a Go struct cannot — that a property Feat does not know is a
// mapping mistake rather than something to keep, and that each required
// property is present — are both about which keys are there rather than about
// what they hold.
func parseTicket(entry json.RawMessage) (Ticket, error) {
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(entry, &properties); err != nil {
		var mismatch *json.UnmarshalTypeError
		if errors.As(err, &mismatch) {
			return Ticket{}, fmt.Errorf("is %s, and a ticket is an object", describeJSON(mismatch.Value))
		}
		return Ticket{}, fmt.Errorf("is not readable as an object: %w", err)
	}

	// "additionalProperties": false. A field Feat does not know is a mapping
	// mistake — `gh` says `number` where the published shape says `reference` —
	// rather than something to carry.
	var unknown []string
	for name := range properties {
		if !known(name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Ticket{}, fmt.Errorf("carries %s, which the published shape does not have; it has %s",
			quoted(unknown), quoted(ticketProperties))
	}

	for _, name := range requiredProperties {
		value, present := properties[name]
		if !present {
			return Ticket{}, fmt.Errorf("has no %q, which the published shape requires", name)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Ticket{}, fmt.Errorf("has a %q that is null, and the published shape carries a string", name)
		}
	}

	var ticket Ticket
	if err := json.Unmarshal(entry, &ticket); err != nil {
		var mismatch *json.UnmarshalTypeError
		if errors.As(err, &mismatch) && mismatch.Field != "" {
			return Ticket{}, fmt.Errorf("has a %q that is %s, and the published shape carries a string",
				mismatch.Field, describeJSON(mismatch.Value))
		}
		return Ticket{}, fmt.Errorf("could not be read: %w", err)
	}

	// The published shape gives each of these a minimum length of one. A value
	// that is only whitespace is refused with the same message: it is a length
	// the schema allows and a value Feat cannot act on, and both are fixed by
	// the command producing something.
	for _, name := range valuedProperties {
		if strings.TrimSpace(value(ticket, name)) == "" {
			return Ticket{}, fmt.Errorf("has an empty %q, and Feat acts on it", name)
		}
	}
	return ticket, nil
}

// known reports whether a property is one the published shape has.
func known(name string) bool {
	for _, property := range ticketProperties {
		if property == name {
			return true
		}
	}
	return false
}

// value returns one of a ticket's properties by its published name. It exists
// so that the minimum-length rule is one loop over the schema's own list rather
// than four copies of the same three lines.
func value(ticket Ticket, name string) string {
	switch name {
	case "reference":
		return ticket.Reference
	case "title":
		return ticket.Title
	case "body":
		return ticket.Body
	case "url":
		return ticket.URL
	case "state":
		return ticket.State
	case "source":
		return ticket.Source
	default:
		return ""
	}
}

// describeJSON names a JSON value's type the way a sentence would.
func describeJSON(kind string) string {
	switch kind {
	case "bool":
		return "a boolean"
	case "array", "object":
		return "an " + kind
	case "null":
		return "null"
	default:
		return "a " + kind
	}
}

// quoted renders a list of property names for a message.
func quoted(names []string) string {
	rendered := make([]string, 0, len(names))
	for _, name := range names {
		rendered = append(rendered, fmt.Sprintf("%q", name))
	}
	switch len(rendered) {
	case 0:
		return ""
	case 1:
		return rendered[0]
	default:
		return strings.Join(rendered[:len(rendered)-1], ", ") + " and " + rendered[len(rendered)-1]
	}
}

// RejectionError explains why a tracker command's output was not accepted.
//
// It is a distinct type because output that does not conform is an answer
// rather than a failure of Feat: `feat doctor` reports it as a finding about
// the project's configuration, and a ticket list refuses to be shown rather
// than showing part of one.
type RejectionError struct {
	// Position is the 1-based place of the ticket the problem is in, and zero
	// when the problem is with the document as a whole. It is a position rather
	// than a reference because a ticket Feat could not read is one whose
	// reference it may not have.
	Position int
	// Oversized reports that the command printed more than Feat will read.
	//
	// It is a field rather than something to read out of the message, because
	// the two refusals are fixed differently: a mapping is corrected in the
	// command's filter, and a command printing somebody's whole backlog is asked
	// for less of it.
	Oversized bool
	// Reason says what was wrong, phrased to follow "the tracker's output" or
	// "ticket N of the tracker's output".
	Reason string
}

func (e *RejectionError) Error() string {
	if e.Position == 0 {
		return "the tracker's output " + e.Reason
	}
	return fmt.Sprintf("ticket %d of the tracker's output %s", e.Position, e.Reason)
}
