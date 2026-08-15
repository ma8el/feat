package cli

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// scripted builds a prompter over a script of answers.
func scripted(script string) (*prompter, *bytes.Buffer) {
	var out bytes.Buffer
	return &prompter{in: bufio.NewReader(strings.NewReader(script)), out: &out}, &out
}

// TestAskTakesTheProposalOnlyForAnEmptyLine checks the rule the whole
// conversation rests on: Enter accepts what is in brackets, and anything typed
// replaces it.
func TestAskTakesTheProposalOnlyForAnEmptyLine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		script   string
		proposed string
		want     string
	}{
		{"an empty line takes the proposal", "\n", "main", "main"},
		{"an answer replaces it", "trunk\n", "main", "trunk"},
		{"surrounding spaces are not part of the answer", "  trunk  \n", "main", "trunk"},
		{"a final answer without a newline still counts", "trunk", "main", "trunk"},
		{"an empty line with no proposal is empty", "\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := scripted(tc.script)

			got, err := p.ask("Branch", tc.proposed)
			if err != nil {
				t.Fatalf("asking: %v", err)
			}
			if got != tc.want {
				t.Errorf("the answer is %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAskReportsInputThatRanOut checks that the end of the input is not read as
// somebody accepting every remaining proposal.
func TestAskReportsInputThatRanOut(t *testing.T) {
	p, _ := scripted("")

	if _, err := p.ask("Branch", "main"); !errors.Is(err, errAnswersEnded) {
		t.Errorf("an exhausted script answers with %v, want the answers to have ended", err)
	}

	p, _ = scripted("")
	if _, err := p.confirm("Write it?", true); !errors.Is(err, errAnswersEnded) {
		t.Errorf("an exhausted script confirms with %v, want the answers to have ended", err)
	}
}

// TestConfirmReadsOnlyYesAndNoAndTheProposal checks that a word the prompt did
// not offer is asked again rather than taken as agreement.
func TestConfirmReadsOnlyYesAndNoAndTheProposal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		script   string
		proposed bool
		want     bool
	}{
		{"an empty line takes the proposal", "\n", true, true},
		{"and takes it when it is no", "\n", false, false},
		{"yes is yes", "yes\n", false, true},
		{"n is no", "n\n", true, false},
		{"anything else is asked again", "maybe\nn\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, out := scripted(tc.script)

			got, err := p.confirm("Write it?", tc.proposed)
			if err != nil {
				t.Fatalf("confirming: %v", err)
			}
			if got != tc.want {
				t.Errorf("the answer is %v, want %v\n%s", got, tc.want, out.String())
			}
		})
	}
}
