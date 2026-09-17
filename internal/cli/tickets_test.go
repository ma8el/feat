package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/tracker"
)

// ticketsReadAt is when the fixture list was read. It is fixed so that the
// documents pinned below carry one timestamp rather than the test's clock.
var ticketsReadAt = time.Date(2026, time.September, 17, 8, 42, 3, 0, time.UTC)

// ticketFixture is what a project's tracker printed: one ticket with a body,
// as a tracker reached through a web form returns it, and one filed without.
func ticketFixture() api.TicketList {
	return api.TicketList{
		ReadAt: ticketsReadAt,
		Tickets: []api.Ticket{
			{
				Reference: "142",
				Title:     "Rate-limit the export endpoint",
				Body: "Exports are unbounded and a single client can saturate the worker pool.\r\n" +
					"Add a per-token limit of 5 concurrent exports and return 429 above it.\r\n",
				URL:   "https://tracker.example.invalid/issues/142",
				State: "open",
			},
			{
				Reference: "151",
				Title:     "Rename the settings page",
				URL:       "https://tracker.example.invalid/issues/151",
				State:     "backlog",
			},
		},
	}
}

// fakeTicketLister answers as the daemon would, with a list arranged here.
type fakeTicketLister struct {
	list api.TicketList
	err  error
	// asked is the project the command sent, which is the only thing it sends.
	asked string
}

func (f *fakeTicketLister) Tickets(_ context.Context, id string) (api.TicketList, error) {
	f.asked = id
	return f.list, f.err
}

// TestTicketsShowPrintsTheComposedBrief is the whole of the second argument:
// the document printed is the one `feat implement --ticket` would put in the
// brief field, and nothing is printed with it (ADR-070).
func TestTicketsShowPrintsTheComposedBrief(t *testing.T) {
	caller := &fakeTicketLister{list: ticketFixture()}

	var out bytes.Buffer
	if err := runTickets(context.Background(), &out, caller, "app", "142", false); err != nil {
		t.Fatalf("showing a ticket the command printed: %v", err)
	}
	if caller.asked != "app" {
		t.Errorf("the command asked the daemon about %q, want app", caller.asked)
	}

	_, want := api.NewTicketReference(ticketFixture().Tickets[0], ticketsReadAt).ComposeBrief()
	if out.String() != want {
		t.Errorf("the document is not the composed brief.\n\ngot:\n%s\nwant:\n%s", out.String(), want)
	}

	// And what that document is, in its own words: a heading a task takes its
	// title from, a line naming the ticket, and the description under a heading
	// that marks where the ticket's own words begin.
	for _, line := range []string{
		"# 142: Rate-limit the export endpoint\n",
		"\nTicket 142 (open): https://tracker.example.invalid/issues/142\n",
		"\n## From the ticket\n",
		"Add a per-token limit of 5 concurrent exports and return 429 above it.\n",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the document does not contain %q:\n%s", line, out.String())
		}
	}
	if strings.Contains(out.String(), "\r") {
		t.Errorf("the document carries the tracker's carriage returns:\n%q", out.String())
	}
	// Nothing after the brief: no footer, no hint, no timestamp. A caller who
	// pipes this somewhere gets the document alone.
	if !strings.HasSuffix(out.String(), "429 above it.\n") {
		t.Errorf("the document does not end with the ticket's last line:\n%q", out.String())
	}
}

// TestTicketsShowSaysWhenATicketHasNoDescription checks the other shape a
// ticket has, so that an empty body reads as a fact rather than as a document
// that ends early.
func TestTicketsShowSaysWhenATicketHasNoDescription(t *testing.T) {
	var out bytes.Buffer
	if err := runTickets(context.Background(), &out, &fakeTicketLister{list: ticketFixture()}, "app", "151", false); err != nil {
		t.Fatalf("showing a ticket with no body: %v", err)
	}
	if !strings.Contains(out.String(), "The ticket has no description.") {
		t.Errorf("the document does not say the ticket has no description:\n%s", out.String())
	}
	if strings.Contains(out.String(), "From the ticket") {
		t.Errorf("the document opens a section for a description it does not have:\n%s", out.String())
	}
}

// TestTicketsShowRefusesWhatTheCommandDidNotPrint checks the two ways a
// reference fails to name one ticket, and that neither leaves anything on
// standard output: the document is there or nothing is (ADR-099).
func TestTicketsShowRefusesWhatTheCommandDidNotPrint(t *testing.T) {
	t.Run("not among them", func(t *testing.T) {
		var out bytes.Buffer
		err := runTickets(context.Background(), &out, &fakeTicketLister{list: ticketFixture()}, "app", "999", false)
		if err == nil {
			t.Fatal("a reference the command did not print was shown")
		}
		for _, want := range []string{"no ticket 999", "142", "151"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the failure does not say %q: %v", want, err)
			}
		}
		if out.Len() != 0 {
			t.Errorf("a failed run printed this on standard output:\n%s", out.String())
		}
	})

	t.Run("printed twice", func(t *testing.T) {
		list := ticketFixture()
		list.Tickets[0].Source = "github"
		list.Tickets = append(list.Tickets, api.Ticket{
			Reference: "142", Title: "A story with the same key", URL: "https://sc.example.invalid/142",
			State: "started", Source: "shortcut",
		})

		var out bytes.Buffer
		err := runTickets(context.Background(), &out, &fakeTicketLister{list: list}, "app", "142", false)
		if err == nil {
			t.Fatal("an ambiguous reference was resolved to one of its tickets")
		}
		if !strings.Contains(err.Error(), "2 tickets called 142") || !strings.Contains(err.Error(), "github and shortcut") {
			t.Errorf("the failure does not name the trackers: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("a failed run printed this on standard output:\n%s", out.String())
		}
	})

	t.Run("the tracker failed", func(t *testing.T) {
		var out bytes.Buffer
		err := runTickets(context.Background(), &out, &fakeTicketLister{err: errors.New("the tracker command gh failed: not logged in")},
			"app", "142", false)
		if err == nil || !strings.Contains(err.Error(), "not logged in") {
			t.Errorf("the tracker's failure did not reach the caller: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("a failed run printed this on standard output:\n%s", out.String())
		}
	})
}

// TestTicketDocument pins what `feat tickets <project> <ticket> --json` prints:
// the reference a task from the ticket would record, snapshot and all, so that
// a caller reads the same shape a task carries.
func TestTicketDocument(t *testing.T) {
	var out bytes.Buffer
	if err := runTickets(context.Background(), &out, &fakeTicketLister{list: ticketFixture()}, "app", "142", true); err != nil {
		t.Fatalf("printing a ticket: %v", err)
	}
	compareDocument(t, "ticket.json", out.String())

	var document api.TicketReference
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatalf("the document does not parse: %v\n%s", err, out.String())
	}
	if !document.Snapshot.TakenAt.Equal(ticketsReadAt) {
		t.Errorf("taken_at = %s, want the moment the list was read, %s", document.Snapshot.TakenAt, ticketsReadAt)
	}
}

// TestTicketListDocument pins what `feat tickets <project> --json` prints: the
// daemon's own answer, unchanged.
func TestTicketListDocument(t *testing.T) {
	var out bytes.Buffer
	if err := runTickets(context.Background(), &out, &fakeTicketLister{list: ticketFixture()}, "app", "", true); err != nil {
		t.Fatalf("printing a ticket list: %v", err)
	}
	compareDocument(t, "ticket-list.json", out.String())
}

// TestTicketsListStillPrintsTheTable keeps the one-argument form what it was,
// with the new hint beside the old one.
func TestTicketsListStillPrintsTheTable(t *testing.T) {
	var out bytes.Buffer
	if err := runTickets(context.Background(), &out, &fakeTicketLister{list: ticketFixture()}, "app", "", false); err != nil {
		t.Fatalf("listing tickets: %v", err)
	}
	for _, want := range []string{
		"TICKET", "STATE", "TITLE",
		"142", "open", "Rate-limit the export endpoint",
		"151", "backlog", "Rename the settings page",
		"read one with `feat tickets app <ticket>`",
		"start one with `feat implement --project app --ticket <ticket>`",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the table does not contain %q:\n%s", want, out.String())
		}
	}
	// The table is a list, not a body: a description stays out of it.
	if strings.Contains(out.String(), "worker pool") {
		t.Errorf("the table carries a ticket's description:\n%s", out.String())
	}
}

// trackerFixture is the project fixture with a tracker section, so that the
// daemon has a command to run.
const trackerFixture = projectFixture + `
tracker:
  kind: command
  command: ["tickets-for-me", "--assigned"]
`

// fakeTracker stands in for the configured command, so that the daemon the
// harness starts asks nobody's tracker for anything.
type fakeTracker struct {
	output []byte
}

func (f *fakeTracker) Run(context.Context, tracker.Command) ([]byte, error) {
	return f.output, nil
}

// TestTicketsReachTheDaemonUnderBothNames runs the whole path once: the command
// asks the daemon, the daemon runs the tracker, and the reference is matched
// against what it printed. `feat tickets` is the same command under a shorter
// name, so its output is the same bytes (ADR-040).
func TestTicketsReachTheDaemonUnderBothNames(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", trackerFixture)
	m.tracker = &fakeTracker{output: []byte(`[
		{"reference": "142", "title": "Rate-limit the export endpoint",
		 "body": "Add a per-token limit.", "url": "https://tracker.example.invalid/issues/142", "state": "open"},
		{"reference": "151", "title": "Rename the settings page",
		 "body": "", "url": "https://tracker.example.invalid/issues/151", "state": "backlog"}
	]`)}
	m.serve(t)

	if code, _, stderr := m.run(t, "project", "add", "app"); code != ExitOK {
		t.Fatalf("registering: exit code = %d\nstderr: %s", code, stderr)
	}

	code, long, stderr := m.run(t, "project", "tickets", "app", "142")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
	}
	code, short, stderr := m.run(t, "tickets", "app", "142")
	if code != ExitOK {
		t.Fatalf("under the short name: exit code = %d\nstderr: %s", code, stderr)
	}
	if long != short {
		t.Errorf("the two names print different documents.\n\nfeat project tickets:\n%s\nfeat tickets:\n%s", long, short)
	}
	for _, want := range []string{
		"# 142: Rate-limit the export endpoint",
		"Ticket 142 (open): https://tracker.example.invalid/issues/142",
		"## From the ticket",
		"Add a per-token limit.",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("the document does not contain %q:\n%s", want, long)
		}
	}

	// A reference the command did not print is an ordinary failure: nothing on
	// standard output, the reason on standard error, and the exit code says so.
	code, stdout, stderr := m.run(t, "tickets", "app", "999")
	if code != ExitError {
		t.Errorf("exit code = %d, want %d for a ticket the command did not print", code, ExitError)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a failed run printed this on standard output:\n%s", stdout)
	}
	if !strings.Contains(stderr, "no ticket 999") || !strings.Contains(stderr, "142") {
		t.Errorf("standard error does not say what the command printed instead:\n%s", stderr)
	}

	// And the list, under the short name, is the table it always was.
	code, stdout, stderr = m.run(t, "tickets", "app")
	if code != ExitOK {
		t.Fatalf("listing: exit code = %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "TICKET") || !strings.Contains(stdout, "151") {
		t.Errorf("the list does not show what the tracker printed:\n%s", stdout)
	}
}
