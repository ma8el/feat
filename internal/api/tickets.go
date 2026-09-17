package api

import (
	"fmt"
	"strings"
)

// FindTicket returns the ticket a reference names among what the tracker
// command printed.
//
// Feat parses no reference. What it does is match what the user typed against
// what the command emitted, so a tracker whose references are issue numbers,
// story keys, or anything else works without Feat knowing the shape of one
// (ADR-071). A reference the command did not emit is reported with what it did,
// because the command decides what the user's tickets are and the answer may
// simply be that this one is not among them.
//
// It lives here rather than in either caller because the preparation screen and
// `feat tickets` answer the same question, and two matchers would be two answers
// the moment one of them changed.
func FindTicket(tickets []Ticket, reference string) (Ticket, error) {
	var matched []Ticket
	for _, ticket := range tickets {
		if ticket.Reference == reference {
			matched = append(matched, ticket)
		}
	}

	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return Ticket{}, fmt.Errorf("the project's tracker printed no ticket %s; it printed %s",
			reference, references(tickets))
	default:
		// A merged command labels each ticket with the tracker it came from, and
		// two trackers can use the same key. Feat picks neither.
		return Ticket{}, fmt.Errorf("the project's tracker printed %d tickets called %s, from %s; "+
			"select one from the list instead", len(matched), reference, sources(matched))
	}
}

// references names what the command printed, for a reference it did not.
func references(tickets []Ticket) string {
	if len(tickets) == 0 {
		return "nothing"
	}
	const shown = 5
	named := make([]string, 0, shown)
	for _, ticket := range tickets[:min(shown, len(tickets))] {
		named = append(named, ticket.Reference)
	}
	listed := strings.Join(named, ", ")
	if len(tickets) > shown {
		return fmt.Sprintf("%s and %d more", listed, len(tickets)-shown)
	}
	return listed
}

// sources names the trackers a merged command drew an ambiguous reference from.
func sources(tickets []Ticket) string {
	named := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket.Source == "" {
			named = append(named, "an unlabelled tracker")
			continue
		}
		named = append(named, ticket.Source)
	}
	return strings.Join(named, " and ")
}
