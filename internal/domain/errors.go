package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Error classes. Every typed error in this package unwraps to exactly one of
// them, so a caller can match the class with errors.Is and reach for the
// details with errors.As.
var (
	// ErrInvalid marks a value that cannot be part of a valid aggregate.
	ErrInvalid = errors.New("invalid value")
	// ErrInvalidTransition marks a state change the domain does not allow.
	ErrInvalidTransition = errors.New("invalid state transition")
	// ErrInvariant marks an attempt to break one of the invariants in
	// docs/03-domain-model.md.
	ErrInvariant = errors.New("domain invariant violated")
)

// ValidationError reports a value that cannot be part of a valid aggregate.
type ValidationError struct {
	// Entity names the kind of entity, such as "task" or "repository".
	Entity string
	// ID identifies the entity when it is known.
	ID string
	// Field names the offending field.
	Field string
	// Reason completes the sentence "<field> ...", such as "must not be empty".
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s %s", subject(e.Entity, e.ID), e.Field, e.Reason)
}

// Unwrap reports the error class.
func (e *ValidationError) Unwrap() error { return ErrInvalid }

// TransitionError reports a rejected state change.
//
// It carries the states involved and, when the target state was reachable in
// principle, the precondition that blocked it, so the message can name what the
// caller has to do next.
type TransitionError struct {
	// Entity names the kind of entity, such as "task".
	Entity string
	// ID identifies the entity.
	ID string
	// Dimension names the state dimension, such as "workflow". Process,
	// attention, workflow, and runtime states are separate dimensions.
	Dimension string
	// From is the current state.
	From string
	// To is the rejected target state.
	To string
	// Allowed lists the states reachable from From, when the target itself is
	// unreachable.
	Allowed []string
	// Reason explains an unmet precondition, when the target is reachable but
	// the entity is not ready for it.
	Reason string
}

func (e *TransitionError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: cannot change %s state from %s to %s",
		subject(e.Entity, e.ID), e.Dimension, e.From, e.To)
	if e.Reason != "" {
		fmt.Fprintf(&b, ": %s", e.Reason)
	}
	if len(e.Allowed) > 0 {
		fmt.Fprintf(&b, " (reachable states: %s)", strings.Join(e.Allowed, ", "))
	}
	return b.String()
}

// Unwrap reports the error class.
func (e *TransitionError) Unwrap() error { return ErrInvalidTransition }

// InvariantError reports an attempt to break a modelling invariant.
type InvariantError struct {
	// Entity names the kind of entity, such as "task".
	Entity string
	// ID identifies the entity.
	ID string
	// Invariant is the numbered invariant in docs/03-domain-model.md, or zero
	// for a rule the document states in prose rather than in the list.
	Invariant int
	// Rule states the invariant in one clause.
	Rule string
	// Reason explains what the caller attempted.
	Reason string
}

func (e *InvariantError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", subject(e.Entity, e.ID), e.Rule)
	if e.Invariant > 0 {
		fmt.Fprintf(&b, " (docs/03-domain-model.md invariant %d)", e.Invariant)
	}
	if e.Reason != "" {
		fmt.Fprintf(&b, ": %s", e.Reason)
	}
	return b.String()
}

// Unwrap reports the error class.
func (e *InvariantError) Unwrap() error { return ErrInvariant }

// subject renders the "<entity> <id>" prefix shared by every domain error, so
// that a message always names the resource it is about.
func subject(entity, id string) string {
	if id == "" {
		return entity
	}
	return entity + " " + id
}
