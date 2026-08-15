// Package wizard is the sequence of questions that composes a project
// configuration, independent of who asks them.
//
// It exists because there are two askers. `feat project init` asks them as a
// line conversation at a shell, and the dashboard asks them as a dialog, and
// the questions, the proposals, the validation, and the order are the same
// questions in both — a pair that would agree on the day it was written and
// drift afterwards (ADR-063).
//
// What is here is the flow and nothing else. It reaches no terminal, renders no
// screen, and runs no process: what it needs to know about the machine it asks
// through Host, which the caller supplies. The answers accumulate in a
// config.Draft, and Review renders that draft, parses it back, resolves it, and
// validates it, so that what either asker displays is a configuration Feat
// accepts rather than a proposal that might not be (ADR-062).
package wizard
