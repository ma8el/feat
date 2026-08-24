// Package tracker runs a project's configured ticket command and validates what
// it printed.
//
// There is no adapter per service. A tracker CLI already prints JSON and holds
// its own credential, so the only thing a native adapter would add is the
// mapping from that CLI's field names to the shape Feat publishes as
// schema/feat-tickets.schema.json — and that mapping belongs to the command,
// where a user can change it without waiting for a release (ADR-071). What this
// package owns is therefore the other side of that contract: running the
// command as an argument vector, bounding what it may print, and refusing
// output that does not conform with a message naming what was wrong.
//
// It receives final values and reads neither configuration nor persistent
// state, under the rule ADR-029 established for Git: the caller resolves the
// project's tracker section into a Command. A `tracker-stays-an-adapter`
// depguard rule makes that mechanical.
//
// Feat passes the command no filter. A filter vocabulary would have to map onto
// every tracker's query language, and iteration is exactly where that fails, so
// what the user's tickets are is the command's decision (ADR-071).
//
// Composing a brief is here as well, because a brief composed from a ticket is
// the only thing Feat does with one. What the user confirms is that composed
// brief rather than the ticket it came from: a ticket is written by whoever
// filed it and becomes the agent's instructions, so reviewing one document and
// sending another would make the confirmation a formality (ADR-070).
package tracker
