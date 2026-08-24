package control_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/control"
)

// commit is a full object name, which is what a draft has to carry.
const commit = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"

// draftMessage renders a publication-draft message with the given payload.
func draftMessage(id, payload string) string {
	return `{"schema_version":1,"id":"` + id + `","task_id":"` + testTask +
		`","type":"publication_draft","occurred_at":"2026-08-06T12:00:00Z","payload":` + payload + `}`
}

// draftPayload is one well-formed repository draft.
func draftPayload(repository, commit string) string {
	document, err := json.Marshal(map[string]any{
		"repositories": []map[string]string{
			{"repository": repository, "title": "Add the rate limit", "body": "What changed.", "commit": commit},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(document)
}

// TestAPublicationDraftAsksForNothing pins the capability a draft requires.
//
// It is the reason the type exists at all: the agent's knowledge is carried as
// data rather than as an action, so a draft is weaker than a runtime request,
// which at least asks. A draft that required a capability would be refused by
// the protocol before anybody could read it, and the one control ADR-070 relies
// on — a person reading the words before they are sent — would never happen.
func TestAPublicationDraftAsksForNothing(t *testing.T) {
	if got := control.TypePublicationDraft.Requires(); got != control.CapabilityNone {
		t.Errorf("a publication draft requires %q, and it asks for nothing", got)
	}
	if !control.TypePublicationDraft.Valid() {
		t.Error("the publication-draft type is not in the documented vocabulary")
	}
}

// TestAPublicationDraftIsValidatedByTheProtocol checks the payload rules.
//
// The payload is validated here rather than in the provider adapter, which is
// the opposite of a provider event: every provider writes the same draft, so
// every provider reaches the same rules rather than a copy of them.
func TestAPublicationDraftIsValidatedByTheProtocol(t *testing.T) {
	cases := map[string]struct {
		payload  string
		contains string
	}{
		"no repositories": {
			payload:  `{"repositories":[]}`,
			contains: "for no repository",
		},
		"an identifier that is not one": {
			payload:  `{"repositories":[{"repository":"../etc","title":"t","commit":"` + commit + `"}]}`,
			contains: "not a repository identifier",
		},
		"the same repository twice": {
			payload: `{"repositories":[` +
				`{"repository":"api","title":"t","commit":"` + commit + `"},` +
				`{"repository":"api","title":"u","commit":"` + commit + `"}]}`,
			contains: "two merge requests for repository",
		},
		"no title": {
			payload:  `{"repositories":[{"repository":"api","title":"   ","commit":"` + commit + `"}]}`,
			contains: "with no title",
		},
		"a title over more than one line": {
			payload:  `{"repositories":[{"repository":"api","title":"one\ntwo","commit":"` + commit + `"}]}`,
			contains: "more than one line",
		},
		"a title over the limit": {
			payload: `{"repositories":[{"repository":"api","title":"` +
				strings.Repeat("t", control.MaxDraftTitle+1) + `","commit":"` + commit + `"}]}`,
			contains: "and the limit is",
		},
		"a description over the limit": {
			payload: `{"repositories":[{"repository":"api","title":"t","body":"` +
				strings.Repeat("b", control.MaxDraftBody+1) + `","commit":"` + commit + `"}]}`,
			contains: "and the limit is",
		},
		"an abbreviated commit": {
			payload:  `{"repositories":[{"repository":"api","title":"t","commit":"1a2b3c4"}]}`,
			contains: "which is not a full commit",
		},
		"no commit at all": {
			payload:  `{"repositories":[{"repository":"api","title":"t"}]}`,
			contains: "which is not a full commit",
		},
		"a payload that is not a draft": {
			payload:  `"a title"`,
			contains: "cannot read",
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			workspace, _ := newWorkspace(t)
			write(t, workspace, "draft.json", draftMessage("draft", test.payload))

			// The envelope is well formed, so the message is delivered and the
			// payload is what refuses it.
			messages, _, err := workspace.Pending()
			if err != nil {
				t.Fatalf("reading the outbox: %v", err)
			}
			if len(messages) != 1 {
				t.Fatalf("the outbox delivered %d messages, want 1", len(messages))
			}
			_, err = control.DecodePublicationDraft(messages[0])
			if err == nil {
				t.Fatal("the payload was accepted")
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Errorf("the refusal is %q, and it does not say %q", err, test.contains)
			}
			var rejection *control.RejectionError
			if !errors.As(err, &rejection) {
				t.Errorf("the refusal is %T, and a bad payload is the agent's mistake, "+
					"which is settled rather than retried", err)
			}
		})
	}
}

// TestAWellFormedPublicationDraftIsRead checks the ordinary case.
func TestAWellFormedPublicationDraftIsRead(t *testing.T) {
	workspace, _ := newWorkspace(t)
	write(t, workspace, "draft.json", draftMessage("draft", draftPayload("api", commit)))

	messages, _, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading the outbox: %v", err)
	}
	draft, err := control.DecodePublicationDraft(messages[0])
	if err != nil {
		t.Fatalf("decoding the draft: %v", err)
	}

	entry, found := draft.Repository("api")
	if !found {
		t.Fatal("the draft has no entry for api")
	}
	if entry.Title != "Add the rate limit" || entry.Commit != commit {
		t.Errorf("the draft reads %+v", entry)
	}
	if _, found := draft.Repository("store"); found {
		t.Error("the draft has an entry for a repository it never named")
	}
}

// TestTheLatestDraftIsReadableAfterItWasApplied is what publication depends on.
//
// A draft is applied when it arrives — the task's history says the agent wrote
// one — and read again when the user asks to publish, which may be hours later
// and after a daemon restart. Pending deliberately never returns a message
// twice, so the read-back is a separate question about the same outbox: the
// account of what the agent sent.
func TestTheLatestDraftIsReadableAfterItWasApplied(t *testing.T) {
	workspace, moment := newWorkspace(t)

	write(t, workspace, "first.json", draftMessage("first", draftPayload("api", commit)))
	touch(t, filepath.Join(workspace.OutboxDir(), "first.json"), moment.at)
	messages, _, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading the outbox: %v", err)
	}
	for _, message := range messages {
		if err := workspace.MarkProcessed(message, control.OutcomeApplied, ""); err != nil {
			t.Fatalf("settling %s: %v", message.ID, err)
		}
	}
	if delivered, _, err := workspace.Pending(); err != nil || len(delivered) != 0 {
		t.Fatalf("a settled message was delivered again: %d, %v", len(delivered), err)
	}

	// A second draft, written later. The newest is the one that describes the
	// work, so it is the one a publication composes from.
	moment.advance(time.Minute)
	later := "2a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	write(t, workspace, "second.json", draftMessage("second", draftPayload("api", later)))
	touch(t, filepath.Join(workspace.OutboxDir(), "second.json"), moment.at)

	message, found, err := workspace.Latest(control.TypePublicationDraft)
	if err != nil {
		t.Fatalf("reading the latest draft: %v", err)
	}
	if !found {
		t.Fatal("the outbox holds two drafts and Latest found none")
	}
	draft, err := control.DecodePublicationDraft(message)
	if err != nil {
		t.Fatalf("decoding the latest draft: %v", err)
	}
	entry, _ := draft.Repository("api")
	if entry.Commit != later {
		t.Errorf("the latest draft describes %s, want the newest one, %s", entry.Commit, later)
	}
}

// TestLatestIgnoresEverythingThatIsNotTheTypeAsked checks that a read-back is
// about one type and judges nothing else.
//
// Delivery has already judged every entry once and told the agent. A second
// judgement made while composing a publication would refuse the publication for
// a message that has nothing to do with it.
func TestLatestIgnoresEverythingThatIsNotTheTypeAsked(t *testing.T) {
	workspace, _ := newWorkspace(t)

	write(t, workspace, "review.json", envelope("review", testTask, control.TypeReviewRequested))
	write(t, workspace, "broken.json", "{not json")
	write(t, workspace, "other.json", draftMessage("other", draftPayload("api", commit)))
	// A draft belonging to another task, which the envelope rules refuse.
	write(t, workspace, "stranger.json", strings.ReplaceAll(
		draftMessage("stranger", draftPayload("api", commit)), testTask, otherTask))

	message, found, err := workspace.Latest(control.TypePublicationDraft)
	if err != nil {
		t.Fatalf("reading the latest draft: %v", err)
	}
	if !found {
		t.Fatal("the outbox holds one usable draft and Latest found none")
	}
	if message.ID != "other" {
		t.Errorf("Latest returned %q, want the one draft that belongs to this task", message.ID)
	}

	if _, found, err := workspace.Latest(control.TypeOpenQuestion); err != nil || found {
		t.Errorf("Latest found an open question in an outbox with none: %v, %v", found, err)
	}
}

// touch moves a file's modification time, which is what orders one message
// after another.
func touch(t *testing.T, path string, at time.Time) {
	t.Helper()

	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("moving the time of %s: %v", path, err)
	}
}
