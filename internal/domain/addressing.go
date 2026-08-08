package domain

import (
	"sort"
	"strings"
)

// taskIDLength is the length of a task identifier in canonical form.
const taskIDLength = 36

// taskRefHyphens are the positions a canonical task identifier holds a "-".
//
// A reference is a prefix of that layout, so this is what says where a hyphen
// belongs rather than a regular expression per accepted length.
var taskRefHyphens = map[int]bool{8: true, 13: true, 18: true, 23: true}

// TaskRef is how a user names a task.
//
// It exists because the identifier a user can see and the identifier a command
// accepted were not the same thing. Lists show the eight-character key derived
// from a task identifier — the dashboard, `feat task list`, and every desktop
// notification — while the identifier itself appears only in the dashboard's
// task detail. A reference closes that: the key, the whole identifier, and any
// prefix of it are all ways of naming the same task.
//
// It is deliberately not a free-form search. A reference is a prefix of a task
// identifier and nothing else, so no title, branch, or repository name can ever
// become a way of addressing a task by accident.
type TaskRef string

// String returns the reference as a plain string.
func (r TaskRef) String() string { return string(r) }

// Validate reports whether the reference could name a task.
//
// The rule is the shape of the identifier it abbreviates: lowercase hexadecimal
// with a "-" exactly where a canonical identifier has one. Case is folded before
// the check, because an identifier copied out of somewhere else is still that
// identifier.
func (r TaskRef) Validate() error {
	invalid := func(reason string) error {
		return &ValidationError{Entity: "task", Field: "reference " + quoted(r), Reason: reason}
	}

	if r == "" {
		return invalid("must not be empty")
	}
	if len(r) > taskIDLength {
		return invalid("is longer than a task identifier, which is 36 characters")
	}

	for i, c := range r.normalized() {
		if taskRefHyphens[i] {
			if c != '-' {
				return invalid(`must hold "-" where a task identifier does, at positions 9, 14, 19, and 24`)
			}
			continue
		}
		if !isHexDigit(c) {
			return invalid("must be a task identifier or the start of one, " +
				"which is lowercase hexadecimal")
		}
	}
	return nil
}

// Exact returns the identifier when the reference is already a whole one.
//
// It is what lets a caller holding a full identifier skip resolution entirely:
// an identifier names exactly one task by construction, so there is nothing to
// resolve and no reason to read every task to find that out.
func (r TaskRef) Exact() (TaskID, bool) {
	if len(r) != taskIDLength {
		return "", false
	}
	id := TaskID(r.normalized())
	if id.Validate() != nil {
		return "", false
	}
	return id, true
}

// normalized folds the reference to the case a task identifier is written in.
func (r TaskRef) normalized() string { return strings.ToLower(string(r)) }

// AmbiguousMatch is one task a reference could have named.
//
// It carries the key and the project rather than the whole task, because it
// exists to be read back to the user: those are the two columns `feat task list`
// prints, so a user can find the row the message is talking about.
type AmbiguousMatch struct {
	// Key is the task's short human-facing identifier.
	Key TaskKey
	// Project is the project that owns the task.
	Project ProjectID
}

// AmbiguousTaskError reports a reference that names more than one task.
//
// Ambiguity is reported rather than resolved to either candidate. It is the rule
// ADR-029 applied to a colliding branch name, for the same reason: a user acting
// on a task Feat picked for them would be acting on something they did not
// choose, and `feat cleanup` is one of the commands that takes a task.
type AmbiguousTaskError struct {
	// Ref is what the user typed, unchanged.
	Ref TaskRef
	// Matches are the tasks it names, ordered by key.
	Matches []AmbiguousMatch
}

func (e *AmbiguousTaskError) Error() string {
	var b strings.Builder
	b.WriteString(quoted(e.Ref))
	b.WriteString(" names more than one task: ")
	for i, match := range e.Matches {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(match.Key.String())
		b.WriteString(" (")
		b.WriteString(match.Project.String())
		b.WriteString(")")
	}
	return b.String()
}

// Unwrap reports the error class.
func (e *AmbiguousTaskError) Unwrap() error { return ErrInvalid }

// ResolveTask picks the one task a reference names.
//
// It reports false rather than an error when nothing matches, because what to
// say about a task that is not there depends on where the question came from,
// and this package knows nothing about commands. Ambiguity is an error here,
// because that answer is the same wherever it was asked.
//
// Every task is a candidate, archived ones included: a cancelled draft becomes
// archived and `feat cleanup` still has to be able to name one. An archived task
// can therefore make a reference ambiguous, which is reported. Preferring a live
// task would be the guess this function exists to refuse.
func ResolveTask(ref TaskRef, tasks []*Task) (*Task, bool, error) {
	if err := ref.Validate(); err != nil {
		return nil, false, err
	}
	prefix := ref.normalized()

	var found []*Task
	for _, task := range tasks {
		if task != nil && strings.HasPrefix(string(task.ID), prefix) {
			found = append(found, task)
		}
	}

	switch len(found) {
	case 0:
		return nil, false, nil
	case 1:
		return found[0], true, nil
	default:
		return nil, false, &AmbiguousTaskError{Ref: ref, Matches: describeMatches(found)}
	}
}

// describeMatches renders the candidates in a stable order, so that one message
// does not depend on the order projects were read in.
func describeMatches(tasks []*Task) []AmbiguousMatch {
	matches := make([]AmbiguousMatch, 0, len(tasks))
	for _, task := range tasks {
		matches = append(matches, AmbiguousMatch{Key: task.Key(), Project: task.ProjectID})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Key < matches[j].Key })
	return matches
}

// isHexDigit reports whether a rune is one a task identifier is written with.
func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// quoted renders a reference for a message, so that whitespace or an empty
// value is visible rather than invisible.
func quoted(ref TaskRef) string { return `"` + string(ref) + `"` }
