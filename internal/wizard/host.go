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
	// Compose reads what Compose files propose about one repository. The project
	// directory is what their relative paths resolve against, which is the
	// directory Compose itself will be given; the repository is the checkout
	// being asked about. The two are one directory for a repository's own
	// application files, and two for the agent's own container: those files live
	// wherever the user keeps them and are asked about every repository in turn.
	// It reads structure and never a value: no environment entry, no build
	// argument, and no environment file.
	Compose(projectDir, repository string, files ...string) Composition
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

// Composition is what one repository's own Compose files propose.
//
// Every field is derived rather than decided. A derived value becomes
// configuration only when the user accepts it into their own file, and Feat
// persists nothing it inferred: the questions carry these as proposals, and the
// answers are what is written (ADR-065).
type Composition struct {
	// Services are the services the files declare, in name order.
	Services []string
	// ContainerPath is where those services agree they mount the repository
	// itself. It is empty when they mount it nowhere, or at more than one path:
	// a project Feat has nothing to propose for is one it asks about, rather
	// than one it guesses at.
	ContainerPath string
	// Reachable are the services that publish a host port.
	Reachable []string
	// Baked are the services built from this repository. Such a service has no
	// mount to replace, so Feat points its build context at the task's worktree
	// instead — which is worth saying while the user is deciding what to manage
	// and what container path to give.
	Baked []string
	// Undecided names the entries Feat left unread because they interpolate a
	// "${...}" it must not resolve. It is what turns "nothing was proposed" into
	// "here is where to look".
	Undecided []string
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
