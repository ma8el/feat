package tracker

import (
	"errors"
	"strings"
	"testing"
)

// TestOutputThatDoesNotConformIsRefusedByName covers what a tracker command can
// print that the published shape does not allow.
//
// Every case is a mapping mistake somebody will make, and what each one is
// checked for is that the refusal names what was wrong: the command is the
// user's own, so a message that only says "invalid" leaves them to guess which
// of six fields it meant. The `number` case is the one `gh` produces without a
// mapping at all, which is why the shape's own field list is in the message.
func TestOutputThatDoesNotConformIsRefusedByName(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		output string
		want   []string
	}{
		{
			name:   "no output at all",
			output: "",
			want:   []string{"is empty", "`[]`"},
		},
		{
			name:   "output that is not JSON",
			output: "gh: command failed\n",
			want:   []string{"is not JSON", `it begins "gh: command failed"`},
		},
		{
			// The mistake this quoting was added for: a heredoc that swallowed
			// two lines of the script, so the command printed its own source and
			// the refusal could only name the character a parser gave up on.
			name:   "a command that printed its own source",
			output: "echo 'reading tickets' >&2\ncat <<'JSON'\n[]\n",
			want:   []string{"is not JSON", `it begins "echo 'reading tickets' >&2"`},
		},
		{
			name:   "a JSON null, which decodes into anything and means nothing",
			output: "null\n",
			want:   []string{"is null", "prints a list of tickets", "`[]`"},
		},
		{
			name:   "a single ticket rather than a list",
			output: `{"reference":"1","title":"t","body":"","url":"u","state":"open"}`,
			want:   []string{"is an object", "prints a list of tickets"},
		},
		{
			name:   "a list of strings",
			output: `["ACME-1"]`,
			want:   []string{"ticket 1", "is a string", "a ticket is an object"},
		},
		{
			name:   "a second document after the list",
			output: `[] {"warning":"rate limited"}`,
			want:   []string{"more than one document"},
		},
		{
			name:   "the tracker's own field names",
			output: `[{"number":7,"title":"t","body":"","url":"u","state":"open"}]`,
			want: []string{"ticket 1", `"number"`, "the published shape does not have",
				`"reference"`, `"source"`},
		},
		{
			name:   "a required field left out",
			output: `[{"reference":"1","title":"t","url":"u","state":"open"}]`,
			want:   []string{"ticket 1", `has no "body"`, "requires"},
		},
		{
			name:   "a number where the shape carries a string",
			output: `[{"reference":7,"title":"t","body":"","url":"u","state":"open"}]`,
			want:   []string{"ticket 1", `"reference"`, "is a number", "carries a string"},
		},
		{
			name:   "a null description",
			output: `[{"reference":"1","title":"t","body":null,"url":"u","state":"open"}]`,
			want:   []string{"ticket 1", `"body"`, "is null"},
		},
		{
			name:   "a state the command left blank",
			output: `[{"reference":"1","title":"t","body":"","url":"u","state":"  "}]`,
			want:   []string{"ticket 1", `empty "state"`},
		},
		{
			name: "the second ticket of a list, so the position is the ticket's own",
			output: `[{"reference":"1","title":"t","body":"","url":"u","state":"open"},` +
				`{"reference":"","title":"t","body":"","url":"u","state":"open"}]`,
			want: []string{"ticket 2", `empty "reference"`},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tickets, err := Parse([]byte(testCase.output))
			if err == nil {
				t.Fatalf("the output was accepted as %d tickets, and it does not conform", len(tickets))
			}
			if tickets != nil {
				t.Errorf("%d tickets were returned beside the refusal, "+
					"and a list Feat half-read is one nobody can act on", len(tickets))
			}
			for _, want := range testCase.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			var rejection *RejectionError
			if !errors.As(err, &rejection) {
				t.Errorf("the refusal is %T, and output that does not conform is an answer "+
					"rather than a failure of Feat", err)
			}
		})
	}
}

// TestWhatARefusalRepeatsBackIsBounded checks the other half of quoting a
// command's own words: the output may be a quarter of a megabyte, and a
// diagnostic, a log line, and a screen all have to hold what the refusal says.
func TestWhatARefusalRepeatsBackIsBounded(t *testing.T) {
	_, err := Parse([]byte("error: " + strings.Repeat("é", 4096)))
	if err == nil {
		t.Fatal("prose was accepted as a list of tickets")
	}

	message := err.Error()
	_, quoted, found := strings.Cut(message, "it begins ")
	if !found {
		t.Fatalf("the refusal does not repeat what the command printed: %v", err)
	}
	if !strings.HasPrefix(quoted, `"error: `) {
		t.Errorf("the refusal quotes %s, and the command began with prose", quoted)
	}
	// Escaping can lengthen what is quoted, so the bound checked here is not the
	// constant itself; what it proves is that eight kilobytes of output became a
	// line rather than a wall.
	if len(quoted) > 2*maxQuotedBytes {
		t.Errorf("the refusal repeats %d bytes back, and what it quotes is bounded at %d",
			len(quoted), maxQuotedBytes)
	}
	if !strings.Contains(message, "…") {
		t.Errorf("the refusal does not say that what it quotes was cut: %v", err)
	}
	// A multi-byte character cut in half would be quoted as escaped bytes that
	// were never in the output, which is the one thing repeating a command's own
	// words back must not do.
	if strings.Contains(message, `\x`) {
		t.Errorf("the refusal quotes bytes that were not characters in the output: %v", err)
	}
}

// TestOutputPastTheBoundIsRefusedBySize is the rule that a tracker's output is
// bounded for the reason a control message is: it becomes a brief, and a brief
// is what the agent is told to do (ADR-071).
func TestOutputPastTheBoundIsRefusedBySize(t *testing.T) {
	oversized := `[{"reference":"1","title":"t","url":"u","state":"open","body":"` +
		strings.Repeat("x", MaxOutputBytes) + `"}]`

	_, err := Parse([]byte(oversized))
	if err == nil {
		t.Fatal("an oversized document was accepted")
	}
	for _, want := range []string{"is", "bytes", "the limit is"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestAConformingListIsReadAsItWasPrinted checks the shape Feat acts on: every
// published property is carried, the optional source is carried where a merged
// command labelled a ticket with one, and an empty description is a ticket
// rather than a refusal.
func TestAConformingListIsReadAsItWasPrinted(t *testing.T) {
	tickets, err := Parse([]byte(`[
	  {"reference":"ACME-14","title":"Reset links expire too quickly",
	   "body":"The link stops working after five minutes.",
	   "url":"https://app.shortcut.com/acme/story/14","state":"Ready for Dev","source":"shortcut"},
	  {"reference":"#42","title":"Export the daily report","body":"",
	   "url":"https://github.com/acme/planning/issues/42","state":"open"}
	]`))
	if err != nil {
		t.Fatalf("a conforming list was refused: %v", err)
	}
	if len(tickets) != 2 {
		t.Fatalf("read %d tickets, want 2", len(tickets))
	}

	first := tickets[0]
	if first.Reference != "ACME-14" || first.Title != "Reset links expire too quickly" {
		t.Errorf("the first ticket is %+v", first)
	}
	if first.State != "Ready for Dev" {
		t.Errorf("state = %q, and a tracker's own vocabulary is carried rather than mapped", first.State)
	}
	if first.Source != "shortcut" {
		t.Errorf("source = %q, and it is what the task's ticket reference records as the provider", first.Source)
	}
	if tickets[1].Source != "" {
		t.Errorf("source = %q for a ticket the command did not label", tickets[1].Source)
	}
	if tickets[1].Body != "" {
		t.Errorf("body = %q, and a ticket filed with no description is a ticket", tickets[1].Body)
	}
}

// TestAnEmptyListIsAnAnswer checks that a user with no tickets is told so rather
// than shown a failure: what the user's tickets are is the command's decision,
// and "none" is one of its answers (ADR-071).
func TestAnEmptyListIsAnAnswer(t *testing.T) {
	tickets, err := Parse([]byte("[]\n"))
	if err != nil {
		t.Fatalf("an empty list was refused: %v", err)
	}
	if len(tickets) != 0 {
		t.Errorf("read %d tickets from an empty list", len(tickets))
	}
}
