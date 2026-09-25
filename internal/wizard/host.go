package wizard

import "context"

// Host is what the questions need to know about this machine.
//
// It is an interface rather than a call into internal/project because the dashboard
// is a client and reaches no adapter of its own (ADR-031). The implementation that
// runs Git and reads Compose files lives where the other host commands are built,
// and the flow names none of it.
//
// A Host reports what is true and decides nothing; the wizard proposes.
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
	// Compose reads what Compose files propose about one repository. Relative paths
	// resolve against projectDir, the directory Compose itself will be given, and
	// repository is the checkout being asked about. The two are one directory for a
	// repository's own application files, and two for the agent container, whose
	// files live wherever the user keeps them. It reads structure and never a value:
	// no environment entry, no build argument, and no environment file.
	Compose(projectDir, repository string, files ...string) Composition
	// Exists reports whether a path is there now. A missing file is worth saying and
	// not worth refusing, because a Compose file that is generated, or that lives on
	// a branch not checked out, is still the right value.
	Exists(path string) bool
	// Absolute expands a leading "~" and resolves a relative path against the
	// directory the wizard was started in, so an answer typed the way a shell would
	// take it is the path Feat records.
	Absolute(value string) (string, error)
	// WorkingDirectory is where the wizard was started, which is what the first
	// repository and the project identifier are proposed from.
	WorkingDirectory() string
}

// Composition is what one repository's own Compose files propose. Every field is
// derived rather than decided: the questions carry these as proposals, and Feat
// writes the answers rather than what it inferred (ADR-065).
type Composition struct {
	// Services are the services the files declare, in name order.
	Services []string
	// ContainerPath is where those services agree they mount the repository itself.
	// It is empty when they mount it nowhere, or at more than one path, and the
	// wizard then asks rather than guesses.
	ContainerPath string
	// Reachable are the services that publish a host port.
	Reachable []string
	// Mounted are the services that mount the repository itself, at whatever path
	// each of them names. With Baked they are the services that run this
	// repository's code, which is what Feat proposes to manage. A database beside
	// them runs none of it, and Compose starts it anyway as a dependency (ADR-100).
	Mounted []string
	// Baked are the services built from this repository. Such a service has no mount
	// to replace, so Feat points its build context at the task's worktree instead,
	// which the user needs while deciding what to manage and what path to give.
	Baked []string
	// Undecided names the entries Feat left unread because they interpolate a
	// "${...}" it must not resolve. The wizard shows them, so a missing proposal
	// comes with somewhere to look.
	Undecided []string
}

// Checkout is what Git said about a directory. An empty Remote or DefaultBranch is
// Git having no answer rather than an error, and the wizard says so, because an
// established value and an assumed one look identical once they are in the file.
type Checkout struct {
	// Root is the working tree Git resolved, which is the path a repository is
	// configured by. An answer naming a subdirectory configures the checkout it is
	// in.
	Root string
	// Remote is the first remote the repository has, or empty for none.
	Remote string
	// RemoteURL is where that remote points, as Git has it written down. The forge
	// question proposes from it, and it is empty for a repository with no remote,
	// which is a repository that publishes nowhere.
	RemoteURL string
	// DefaultBranch is the branch the remote publishes, or the branch checked
	// out where there is no remote, or empty for neither.
	DefaultBranch string
}
