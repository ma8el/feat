package cli

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// conversation builds a prompter over a script of answers.
func conversation(script string) (*prompter, *bytes.Buffer) {
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
			p, _ := conversation(tc.script)

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
	p, _ := conversation("")

	if _, err := p.ask("Branch", "main"); !errors.Is(err, errAnswersEnded) {
		t.Errorf("an exhausted script answers with %v, want the answers to have ended", err)
	}

	p, _ = conversation("")
	if _, err := p.confirm("Write it?", true); !errors.Is(err, errAnswersEnded) {
		t.Errorf("an exhausted script confirms with %v, want the answers to have ended", err)
	}
}

// TestAskUntilRepeatsTheQuestionWithTheReason checks that a rejected answer is
// refused where it was typed, with the reason and the same question.
func TestAskUntilRepeatsTheQuestionWithTheReason(t *testing.T) {
	p, out := conversation("Not An Id\n\nfine\n")

	got, err := p.askUntil("Identifier", "", func(value string) error {
		if strings.ContainsAny(value, " ") {
			return errors.New("an identifier has no spaces in it")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("asking: %v", err)
	}
	if got != "fine" {
		t.Errorf("the answer is %q, want %q", got, "fine")
	}

	transcript := out.String()
	if !strings.Contains(transcript, "an identifier has no spaces in it") {
		t.Errorf("the rejection does not say why:\n%s", transcript)
	}
	if !strings.Contains(transcript, "an answer is needed here") {
		t.Errorf("an empty answer to a question with no proposal is accepted:\n%s", transcript)
	}
	if strings.Count(transcript, "Identifier") != 3 {
		t.Errorf("the question was not put again for each rejection:\n%s", transcript)
	}
}

// TestChooseAcceptsOnlyWhatItOffered checks the closed questions.
func TestChooseAcceptsOnlyWhatItOffered(t *testing.T) {
	p, out := conversation("container\ndevcontainer\n")

	got, err := p.choose("Execution mode", []string{"host", "devcontainer"}, "host")
	if err != nil {
		t.Fatalf("choosing: %v", err)
	}
	if got != "devcontainer" {
		t.Errorf("the answer is %q, want %q", got, "devcontainer")
	}
	if !strings.Contains(out.String(), `"container" is not one of host, devcontainer`) {
		t.Errorf("the rejection does not name what was offered:\n%s", out.String())
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
			p, out := conversation(tc.script)

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
