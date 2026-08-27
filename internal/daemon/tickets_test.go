package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/tracker"
)

// trackerFixture is the preparation fixture with a tracker section, so that a
// project can be asked for its tickets.
const trackerFixture = prepareFixture + `
tracker:
  kind: command
  command: ["tickets-for-me", "--assigned"]
`

// fakeTracker answers with what a project's ticket command is meant to have
// printed. The daemon runs no tracker of its own, so this is where a test says
// what somebody's tickets are.
type fakeTracker struct {
	output  []byte
	err     error
	command tracker.Command
	runs    int
}

func (f *fakeTracker) Run(_ context.Context, command tracker.Command) ([]byte, error) {
	f.command = command
	f.runs++
	return f.output, f.err
}

// arrangeTracker registers a project whose tickets come from the given answer.
func arrangeTracker(t *testing.T, answer *fakeTracker) *drafting {
	t.Helper()

	arranged := arrangeConfigured(t, trackerFixture)
	arranged.service.tracker = answer
	return arranged
}

// TestAProjectsTicketsAreWhatItsCommandPrinted is the whole of what the tracker
// is: a configured command decides which tickets are the user's, and Feat
// carries what it printed without parsing any of it (ADR-071).
func TestAProjectsTicketsAreWhatItsCommandPrinted(t *testing.T) {
	answer := &fakeTracker{output: []byte(`[
	  {"reference":"ACME-14","title":"Reset links expire","body":"After five minutes.",
	   "url":"https://app.shortcut.com/acme/story/14","state":"Ready for Dev","source":"shortcut"},
	  {"reference":"#42","title":"Export the daily report","body":"",
	   "url":"https://github.com/acme/planning/issues/42","state":"open","source":"github"}
	]`)}
	arranged := arrangeTracker(t, answer)

	list, err := arranged.service.Tickets(context.Background(), "app")
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}

	if len(list.Tickets) != 2 {
		t.Fatalf("read %d tickets, want 2", len(list.Tickets))
	}
	if list.Tickets[0].State != "Ready for Dev" {
		t.Errorf("state = %q, and a tracker's own vocabulary is carried rather than mapped",
			list.Tickets[0].State)
	}
	if list.Tickets[1].Source != "github" {
		t.Errorf("source = %q, and a merged command's label is what a task records as the provider",
			list.Tickets[1].Source)
	}
	if list.ReadAt.IsZero() {
		t.Error("the list does not say when it was read, and that is what versions a snapshot")
	}
	if list.ReadAt.After(arranged.now) {
		t.Errorf("the list says it was read at %s, which is after now", list.ReadAt)
	}
}

// TestTheTrackerCommandRunsAsConfiguredWithNoFilter checks that Feat adds
// nothing to the command: a filter vocabulary would have to map onto every
// tracker's query language, so which tickets are the user's is the command's
// decision (ADR-071).
func TestTheTrackerCommandRunsAsConfiguredWithNoFilter(t *testing.T) {
	answer := &fakeTracker{output: []byte(`[]`)}
	arranged := arrangeTracker(t, answer)

	if _, err := arranged.service.Tickets(context.Background(), "app"); err != nil {
		t.Fatalf("Tickets: %v", err)
	}

	if answer.command.Program != "tickets-for-me" {
		t.Errorf("program = %q", answer.command.Program)
	}
	if got := strings.Join(answer.command.Arguments, " "); got != "--assigned" {
		t.Errorf("arguments = %q, want the configured %q and nothing else", got, "--assigned")
	}
	if answer.command.Directory != arranged.env.Home {
		t.Errorf("the command ran in %q, want the user's home directory %q; `feat doctor` runs it "+
			"there too, so that a project which answers one answers the other",
			answer.command.Directory, arranged.env.Home)
	}
}

// TestAskingForTicketsRunsTheCommandEveryTime is why nothing caches the list:
// Feat passes no filter, so a held list could not be re-filtered, and a ticket
// that changed is found by running the command again and comparing (ADR-071).
func TestAskingForTicketsRunsTheCommandEveryTime(t *testing.T) {
	answer := &fakeTracker{output: []byte(`[]`)}
	arranged := arrangeTracker(t, answer)

	for range 2 {
		if _, err := arranged.service.Tickets(context.Background(), "app"); err != nil {
			t.Fatalf("Tickets: %v", err)
		}
	}
	if answer.runs != 2 {
		t.Errorf("the command ran %d times for two requests", answer.runs)
	}
}

// TestOutputThatDoesNotConformIsRefusedNamingWhatWasWrong checks that a mapping
// mistake reaches the user as something they can fix rather than as an empty
// list.
func TestOutputThatDoesNotConformIsRefusedNamingWhatWasWrong(t *testing.T) {
	arranged := arrangeTracker(t, &fakeTracker{
		output: []byte(`[{"number":7,"title":"t","body":"","url":"u","state":"open"}]`),
	})

	_, err := arranged.service.Tickets(context.Background(), "app")
	if err == nil {
		t.Fatal("output that does not conform was accepted")
	}
	if !errors.Is(err, api.ErrInvalid) {
		t.Errorf("the failure is %v, and a project whose command maps wrongly is a bad request", err)
	}
	for _, want := range []string{`"number"`, "published shape"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestAProjectWithNoTrackerHasNowhereToReadTicketsFrom checks that the absence
// is reported as one rather than as an empty list, which would say that the user
// has no tickets.
func TestAProjectWithNoTrackerHasNowhereToReadTicketsFrom(t *testing.T) {
	answer := &fakeTracker{output: []byte(`[]`)}
	arranged := arrangeDrafting(t)
	arranged.service.tracker = answer

	_, err := arranged.service.Tickets(context.Background(), "app")
	if err == nil {
		t.Fatal("a project with no tracker answered with a ticket list")
	}
	if !errors.Is(err, api.ErrNotFound) {
		t.Errorf("the failure is %v, want a not-found", err)
	}
	if !strings.Contains(err.Error(), "configures no tracker") {
		t.Errorf("the failure does not say what is missing: %v", err)
	}
	if answer.runs != 0 {
		t.Error("a command ran for a project that configures no tracker")
	}
}

// TestAnUnregisteredProjectHasNoTickets checks that the project is resolved
// before its configuration is read, so that the answer is about the project
// rather than about a file.
func TestAnUnregisteredProjectHasNoTickets(t *testing.T) {
	arranged := arrangeTracker(t, &fakeTracker{output: []byte(`[]`)})

	_, err := arranged.service.Tickets(context.Background(), "absent")
	if !errors.Is(err, api.ErrNotFound) {
		t.Errorf("the failure is %v, want a not-found", err)
	}
}

// TestADraftFromATicketRecordsWhatItWasComposedFrom checks the record a task
// keeps of the ticket it came from, which is what lets a merge request name what
// it closes and what a ticket observed again later is compared against.
func TestADraftFromATicketRecordsWhatItWasComposedFrom(t *testing.T) {
	arranged := arrangeTracker(t, &fakeTracker{output: []byte(`[]`)})

	draft, err := arranged.service.CreateDraft(context.Background(), api.DraftRequest{
		Project: "app",
		Title:   "ACME-14: Reset links expire",
		Brief:   "# ACME-14: Reset links expire\n\nTicket ACME-14 (open): https://example.test/14\n",
		Source: domain.TaskSource{
			Kind: domain.SourceTicket,
			Ticket: &domain.ExternalTaskReference{
				Provider:  "shortcut",
				Reference: "ACME-14",
				URL:       "https://example.test/14",
				Snapshot: domain.TicketSnapshot{
					Title:   "Reset links expire",
					Body:    "After five minutes.",
					State:   "open",
					TakenAt: arranged.now.Add(-time.Minute),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	recorded := arranged.reload(t, draft.ID)
	if recorded.Source.Kind != domain.SourceTicket {
		t.Fatalf("source kind = %q", recorded.Source.Kind)
	}
	if recorded.Source.Ticket == nil {
		t.Fatal("the task records no ticket")
	}
	if recorded.Source.Ticket.Provider != "shortcut" {
		t.Errorf("provider = %q, and a merged command's label is what fills it",
			recorded.Source.Ticket.Provider)
	}
	if recorded.Source.Ticket.Snapshot.State != "open" {
		t.Errorf("state = %q", recorded.Source.Ticket.Snapshot.State)
	}
}

// TestATicketRecordThatCannotBeTrueIsRefused covers what the daemon checks about
// a ticket a client sent back, which is what the domain cannot know: how large
// it is, and that it was not read in the future.
func TestATicketRecordThatCannotBeTrueIsRefused(t *testing.T) {
	arranged := arrangeTracker(t, &fakeTracker{output: []byte(`[]`)})

	ticket := func(edit func(*domain.ExternalTaskReference)) domain.TaskSource {
		reference := &domain.ExternalTaskReference{
			Reference: "ACME-14",
			URL:       "https://example.test/14",
			Snapshot: domain.TicketSnapshot{
				Title: "Reset links expire", State: "open", TakenAt: arranged.now,
			},
		}
		edit(reference)
		return domain.TaskSource{Kind: domain.SourceTicket, Ticket: reference}
	}

	for _, testCase := range []struct {
		name   string
		source domain.TaskSource
		want   string
	}{
		{
			name: "a snapshot from the future",
			source: ticket(func(r *domain.ExternalTaskReference) {
				r.Snapshot.TakenAt = arranged.now.Add(time.Hour)
			}),
			want: "in the future",
		},
		{
			name: "a description past what a brief may hold",
			source: ticket(func(r *domain.ExternalTaskReference) {
				r.Snapshot.Body = strings.Repeat("x", maxBriefBytes+1)
			}),
			want: "the limit is",
		},
		{
			name:   "a ticket source with no ticket",
			source: domain.TaskSource{Kind: domain.SourceTicket},
			want:   "must carry the ticket",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := arranged.service.CreateDraft(context.Background(), api.DraftRequest{
				Project: "app", Title: "ACME-14", Brief: "brief", Source: testCase.source,
			})
			if err == nil {
				t.Fatal("the draft was recorded")
			}
			if !errors.Is(err, api.ErrInvalid) {
				t.Errorf("the failure is %v, want an invalid request", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("the refusal does not say %q: %v", testCase.want, err)
			}
		})
	}
}

// slowTracker never answers, so that what a daemon does with a tracker command
// that will not finish can be observed without waiting for the real budget.
type slowTracker struct{ started chan struct{} }

func (s *slowTracker) Run(ctx context.Context, _ tracker.Command) ([]byte, error) {
	close(s.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestATrackerThatWillNotAnswerIsEndedByTheDaemon checks that the daemon holds
// the bound rather than leaving a request open indefinitely. It is half of a
// contract: the client waits for this plus a margin, so that the process which
// knows what it was waiting for is the one that answers.
func TestATrackerThatWillNotAnswerIsEndedByTheDaemon(t *testing.T) {
	arranged := arrangeConfigured(t, trackerFixture)
	arranged.service.tracker = &slowTracker{started: make(chan struct{})}
	arranged.service.ticketOverride = 50 * time.Millisecond

	started := time.Now()
	_, err := arranged.service.Tickets(context.Background(), "app")
	if err == nil {
		t.Fatal("a tracker command that never answered was accepted")
	}
	if elapsed := time.Since(started); elapsed > api.TicketTimeout {
		t.Errorf("the request took %s, and the daemon's budget was 50ms", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the failure is %v, and what ended the run was the daemon's budget", err)
	}
}
