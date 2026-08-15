package wizard

import "context"

// Host is what the questions need to know about this machine.
//
// It is an interface rather than a call into internal/project because of who
// drives the flow. The dashboard is a client and reaches no adapter of its own
// (ADR-031), so the implementation that runs Git and reads Compose files lives
// where the other host commands are built, and the flow names none of it.
//
// Every method answers a question a user would otherwise have to answer by
// hand. Nothing here decides anything: a Host reports what is true, and the
// wizard proposes.
type Host interface {
	// Inspect asks Git about a directory. An error means the path is not a
	// checkout, and its text is put to the user as the reason the answer was
	// refused, so it is written for them rather than for a log.
	Inspect(ctx context.Context, path string) (Checkout, error)
	// ComposeFiles returns the Compose files found beside a directory, in a
	// stable order.
	ComposeFiles(dir string) []string
	// ComposeServices returns the services the given Compose files declare.
	ComposeServices(files ...string) []string
	// Exists reports whether a path is there now. A file that is not is worth
	// saying and is not worth refusing: a Compose file that is generated, or
	// that lives on a branch not checked out right now, is still the right
	// value.
	Exists(path string) bool
	// Absolute expands a leading "~" and resolves a relative path against the
	// directory the wizard was started in, so that an answer typed the way a
	// shell would take it is the path Feat records.
	Absolute(value string) (string, error)
	// WorkingDirectory is where the wizard was started, which is what the first
	// repository and the project identifier are proposed from.
	WorkingDirectory() string
}

// Checkout is what Git said about a directory.
//
// An empty Remote or DefaultBranch is Git having no answer rather than an
// error, and the difference reaches the user: a value that was established and
// one that was assumed look identical once they are in the file, and the moment
// to tell them apart is while the answer is still being given.
type Checkout struct {
	// Root is the working tree Git resolved, which is the path a repository is
	// configured by: an answer naming a subdirectory configures the checkout it
	// is in.
	Root string
	// Remote is the first remote the repository has, or empty for none.
	Remote string
	// DefaultBranch is the branch the remote publishes, or the branch checked
	// out where there is no remote, or empty for neither.
	DefaultBranch string
}
