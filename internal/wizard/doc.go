// Package wizard is the sequence of questions that composes a project
// configuration, independent of who asks them.
//
// There are two askers. `feat project init` asks as a line conversation at a shell,
// and the dashboard asks as a dialog. The questions, the proposals, the validation,
// and the order are the same in both, so one flow serves both rather than two that
// would drift (ADR-063).
//
// This package holds the flow alone. It reaches no terminal, renders no screen, and
// runs no process; what it needs to know about the machine it asks through Host,
// which the caller supplies. The answers accumulate in a config.Draft, and Review
// renders that draft, parses it back, resolves it, and validates it, so either
// asker displays a configuration Feat accepts (ADR-062).
package wizard
