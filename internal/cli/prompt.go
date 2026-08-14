package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// prompter asks questions and reads answers.
//
// The reader is held for the whole conversation rather than built per question,
// because a new buffered reader discards what the previous one buffered, which
// for a sequence of prompts loses the answers a user typed ahead.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// say writes one line of the conversation.
func (p *prompter) say(format string, args ...any) { printf(p.out, format, args...) }

// ask puts one question and returns the answer, or the proposed value when the
// answer is empty.
func (p *prompter) ask(question, proposed string) (string, error) {
	if proposed != "" {
		printf(p.out, "%s [%s]: ", question, proposed)
	} else {
		printf(p.out, "%s: ", question)
	}

	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the answer: %w", err)
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		if errors.Is(err, io.EOF) {
			// Input that ran out is not somebody accepting every remaining
			// proposal. There are more questions, and there is nobody left to
			// answer them.
			return "", errAnswersEnded
		}
		return proposed, nil
	}
	return answer, nil
}

// askUntil repeats a question until the answer passes.
//
// A rejected answer is answered with the reason and the same question, because
// the alternative is a command that fails at the end of a conversation over
// something the user could have corrected when they typed it.
func (p *prompter) askUntil(question, proposed string, check func(string) error) (string, error) {
	for {
		answer, err := p.ask(question, proposed)
		if err != nil {
			return "", err
		}
		if answer == "" {
			p.say("    an answer is needed here\n")
			continue
		}
		if err := check(answer); err != nil {
			p.say("    %v\n", err)
			continue
		}
		return answer, nil
	}
}

// choose puts a question with a closed set of answers.
func (p *prompter) choose(question string, options []string, proposed string) (string, error) {
	return p.askUntil(fmt.Sprintf("%s (%s)", question, strings.Join(options, "/")), proposed,
		func(value string) error {
			for _, option := range options {
				if value == option {
					return nil
				}
			}
			return fmt.Errorf("%q is not one of %s", value, strings.Join(options, ", "))
		})
}

// confirm puts a yes-or-no question.
//
// An answer that is neither is asked again rather than read as the proposal.
// Half of these questions propose "yes", and one of them writes a file: a word
// the prompt did not offer is somebody answering a different question, and
// taking it as agreement is the one reading that cannot be taken back.
func (p *prompter) confirm(question string, proposed bool) (bool, error) {
	hint := "y/N"
	if proposed {
		hint = "Y/n"
	}

	for {
		printf(p.out, "%s [%s]: ", question, hint)

		line, err := p.in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, fmt.Errorf("reading the answer: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		case "":
			if errors.Is(err, io.EOF) {
				// An answer that is only the end of the input is not the default
				// being accepted: a question nobody answered must not be read as
				// permission.
				return false, errAnswersEnded
			}
			return proposed, nil
		default:
			p.say("    answer \"y\" or \"n\"\n")
		}
	}
}

// notEmpty rejects an empty answer, naming what was asked for.
func notEmpty(what string) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("name " + what)
		}
		return nil
	}
}
