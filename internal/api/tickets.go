package api

import (
	"fmt"
	"strings"
)

// FindTicket returns the ticket a reference names among what the tracker command
// printed.
//
// Feat parses no reference. It matches what the user typed against what the
// command emitted, so issue numbers, story keys, and anything else work without
// Feat knowing the shape of one (ADR-071). A reference the command did not print
// is reported together with the ones it did.
//
// It lives here so that the preparation screen and `feat tickets` share one
// matcher rather than each keeping its own.
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
		// A merged command labels each ticket with its tracker, and two trackers
		// can use the same key. Feat picks neither.
		return Ticket{}, fmt.Errorf("the project's tracker printed %d tickets called %s, from %s; "+
			"select one from the list instead", len(matched), reference, sources(matched))
	}
}

// references lists what the command printed, for a failure about one it did not.
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
