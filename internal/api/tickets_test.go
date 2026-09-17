package api

import (
	"fmt"
	"strings"
	"testing"
)

// TestFindTicketMatchesWhatTheCommandEmitted is the whole of the matching
// rule: the reference is compared as the command printed it, and nothing in it
// is parsed (ADR-071).
func TestFindTicketMatchesWhatTheCommandEmitted(t *testing.T) {
	tickets := []Ticket{
		{Reference: "ACME-14", Title: "Rotate the signing key"},
		{Reference: "#42", Title: "Export the daily report"},
	}

	found, err := FindTicket(tickets, "#42")
	if err != nil {
		t.Fatalf("finding a ticket the command printed: %v", err)
	}
	if found.Title != "Export the daily report" {
		t.Errorf("found %+v, want the ticket the reference names", found)
	}

	// The match is exact. "42" is not "#42": a tracker whose references carry a
	// prefix printed the prefix, and Feat does not know which part is the number.
	if _, err := FindTicket(tickets, "42"); err == nil {
		t.Error("a reference that differs from what the command printed was matched")
	}
}

// TestFindTicketSaysWhatTheCommandPrinted checks the answer to a reference that
// is not in the list. Which tickets are the user's is the command's decision, so
// the answer names what it printed rather than claiming the ticket does not
// exist.
func TestFindTicketSaysWhatTheCommandPrinted(t *testing.T) {
	tickets := []Ticket{{Reference: "ACME-14"}, {Reference: "#42"}}

	_, err := FindTicket(tickets, "ACME-99")
	if err == nil {
		t.Fatal("a reference the command did not print was accepted")
	}
	for _, want := range []string{"no ticket ACME-99", "ACME-14", "#42"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, err)
		}
	}

	_, err = FindTicket(nil, "ACME-99")
	if err == nil || !strings.Contains(err.Error(), "it printed nothing") {
		t.Errorf("an empty list is not described as nothing: %v", err)
	}
}

// TestFindTicketBoundsWhatItRepeatsBack keeps the failure readable for a user
// whose command prints a long backlog: five references and a count, rather than
// all of them.
func TestFindTicketBoundsWhatItRepeatsBack(t *testing.T) {
	tickets := make([]Ticket, 0, 8)
	for i := 1; i <= 8; i++ {
		tickets = append(tickets, Ticket{Reference: fmt.Sprintf("T-%d", i)})
	}

	_, err := FindTicket(tickets, "T-9")
	if err == nil {
		t.Fatal("a reference the command did not print was accepted")
	}
	if want := "T-1, T-2, T-3, T-4, T-5 and 3 more"; !strings.Contains(err.Error(), want) {
		t.Errorf("the failure does not list %q: %v", want, err)
	}
	if strings.Contains(err.Error(), "T-6") {
		t.Errorf("the failure lists more than five references: %v", err)
	}
}

// TestFindTicketRefusesToChooseBetweenTrackers checks a merged command that
// labelled two tickets with the same key. Feat picks neither, and says which
// trackers they came from so that the user can.
func TestFindTicketRefusesToChooseBetweenTrackers(t *testing.T) {
	tickets := []Ticket{
		{Reference: "42", Source: "github"},
		{Reference: "42", Source: "shortcut"},
		{Reference: "43"},
	}

	_, err := FindTicket(tickets, "42")
	if err == nil {
		t.Fatal("an ambiguous reference was resolved to one of its tickets")
	}
	for _, want := range []string{"2 tickets called 42", "github and shortcut", "select one from the list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, err)
		}
	}

	// A merged command that forgot to label one of them is named for what it is,
	// so the message still says two trackers rather than one and a blank.
	_, err = FindTicket([]Ticket{{Reference: "7", Source: "jira"}, {Reference: "7"}}, "7")
	if err == nil || !strings.Contains(err.Error(), "jira and an unlabelled tracker") {
		t.Errorf("an unlabelled ticket is not named as such: %v", err)
	}
}
