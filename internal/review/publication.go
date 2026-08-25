package review

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

// Bounds on one approved publication.
//
// They bound what the user approved rather than what the agent drafted: the
// draft has its own limits where it is read, and what arrives here has been
// through an editor. The numbers are the same because the destination is —
// a title is a line in a merge request list either way — and the two are
// deliberately separate checks, because a document that passed one of them
// years ago is not evidence about the other.
const (
	// MaxPublicationTitle bounds a merge request title.
	MaxPublicationTitle = 200
	// MaxPublicationBody bounds a merge request description.
	MaxPublicationBody = 32 << 10
)

// commitPattern is a full object name, which is what a publication carries.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// PublicationRequest is one repository's publication, as composed and approved.
//
// It reaches this package with every value already resolved: the daemon read
// the project's configuration, the agent's draft, and the user's edits, and
// this package decides whether what came out may be sent. That is the same
// division New uses for a viewer command, and the difference is what happens
// afterwards — a viewer command is run and forgotten, and a publication has a
// result to record (ADR-070, ADR-073).
type PublicationRequest struct {
	// RepositoryID is the repository this publication is for.
	RepositoryID domain.RepositoryID
	// Forge is the forge its configuration declares.
	Forge domain.ForgeKind
	// Remote is the Git remote the branch is pushed to.
	Remote string
	// Branch is the task branch the merge request is opened from.
	Branch string
	// BaseBranch is the branch it asks to merge into.
	BaseBranch string
	// Commit is the commit the approved text describes, and the commit the push
	// puts on the remote.
	Commit string
	// Title and Body are the words, as the user approved them.
	Title string
	Body  string
	// Directory is the task worktree the push and the forge command run in.
	Directory string
	// Worktrees are every worktree path this task recorded. The directory must
	// be one of them, which is what keeps a publication inside the task it
	// belongs to.
	Worktrees []string
}

// Publication is one checked publication, ready to be applied.
//
// It is built by NewPublication, which is the only way to make one whose fields
// have been examined.
type Publication struct {
	// RepositoryID is the repository this publication is for.
	RepositoryID domain.RepositoryID
	// Forge is the forge to open the request on.
	Forge domain.ForgeKind
	// Remote, Branch, BaseBranch, and Commit are what the push and the request
	// are made of.
	Remote     string
	Branch     string
	BaseBranch string
	Commit     string
	// Title and Body are the approved words.
	Title string
	Body  string
	// Directory is the task worktree both commands run in.
	Directory string
}

// NewPublication checks an approved publication and returns it, or says why it
// may not be sent.
//
// The rules are about what a merge request needs and what survives an argument
// vector, and the working-directory rule is the same one a viewer command
// passes: one of this task's own recorded worktrees, and not a shared system
// directory. A publication reaches a network, so the check is if anything
// stricter — but it is deliberately not a judgement about the words. What the
// description says is the agent's, and the control on it is that a person read
// it (ADR-070).
func NewPublication(request PublicationRequest) (Publication, error) {
	if err := request.RepositoryID.Validate(); err != nil {
		return Publication{}, err
	}
	subject := "the publication of repository " + request.RepositoryID.String()

	if !request.Forge.Valid() {
		return Publication{}, fmt.Errorf("%s names %q, which is not a forge Feat publishes to",
			subject, request.Forge)
	}
	for _, field := range []struct{ name, value string }{
		{"remote", request.Remote},
		{"branch", request.Branch},
		{"base branch", request.BaseBranch},
	} {
		if strings.TrimSpace(field.value) == "" {
			return Publication{}, fmt.Errorf("%s names no %s", subject, field.name)
		}
		if strings.HasPrefix(field.value, "-") {
			return Publication{}, fmt.Errorf("%s has the %s %q, which a command line reads as an option",
				subject, field.name, field.value)
		}
	}
	if request.Branch == request.BaseBranch {
		return Publication{}, fmt.Errorf("%s would merge %s into itself", subject, request.Branch)
	}
	if !commitPattern.MatchString(request.Commit) {
		return Publication{}, fmt.Errorf("%s describes %q, which is not a full commit", subject, request.Commit)
	}

	title := strings.TrimSpace(request.Title)
	switch {
	case title == "":
		return Publication{}, fmt.Errorf("%s has no title, and a merge request needs one. "+
			"Write one in the draft before approving it", subject)
	case len(title) > MaxPublicationTitle:
		return Publication{}, fmt.Errorf("%s has a title of %d bytes, and the limit is %d",
			subject, len(title), MaxPublicationTitle)
	case strings.ContainsAny(title, "\r\n"):
		return Publication{}, fmt.Errorf("%s has a title spanning more than one line", subject)
	}
	if len(request.Body) > MaxPublicationBody {
		return Publication{}, fmt.Errorf("%s has a description of %d bytes, and the limit is %d",
			subject, len(request.Body), MaxPublicationBody)
	}
	if strings.ContainsRune(title, 0) || strings.ContainsRune(request.Body, 0) {
		return Publication{}, fmt.Errorf("%s carries a NUL, which no argument survives", subject)
	}

	directory, err := publicationDirectory(subject, request)
	if err != nil {
		return Publication{}, err
	}

	return Publication{
		RepositoryID: request.RepositoryID,
		Forge:        request.Forge,
		Remote:       request.Remote,
		Branch:       request.Branch,
		BaseBranch:   request.BaseBranch,
		Commit:       request.Commit,
		Title:        title,
		Body:         request.Body,
		Directory:    directory,
	}, nil
}

// publicationDirectory reports where the push and the forge command may run.
func publicationDirectory(subject string, request PublicationRequest) (string, error) {
	if request.Directory == "" {
		return "", fmt.Errorf("%s has no worktree to run in", subject)
	}
	directory := filepath.Clean(request.Directory)
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("%s would run in %q, which is not an absolute path", subject, request.Directory)
	}
	if paths.Broad(directory) {
		return "", fmt.Errorf("%s would run in %s, which is a shared system directory", subject, directory)
	}
	for _, worktree := range request.Worktrees {
		if worktree != "" && filepath.Clean(worktree) == directory {
			return directory, nil
		}
	}
	return "", fmt.Errorf("%s would run in %s, which is not one of this task's worktrees (%s)",
		subject, directory, list(request.Worktrees))
}

// PublicationOutcome is what came of one repository's publication.
//
// It is its own vocabulary rather than the gate's Outcome: a check that failed
// and a merge request that was not opened are different findings about
// different things, and one type covering both would let a caller compare them.
type PublicationOutcome string

// Publication outcomes.
const (
	// PublishedOutcome is a repository whose merge request was opened.
	PublishedOutcome PublicationOutcome = "published"
	// FailedOutcome is a repository whose publication failed. It does not stop
	// the others: where the cause is common the user reads it several times,
	// and where it is local to one repository the rest still land (ADR-073).
	FailedOutcome PublicationOutcome = "failed"
	// SkippedOutcome is a repository this publication did not attempt because
	// it has already published.
	//
	// It is deliberately its own outcome rather than a kind of failure, and
	// deliberately not the refusal that says a draft is stale: keeping those two
	// apart is what lets a refusal keep one meaning, which is that the agent's
	// draft describes a commit that is no longer current and never merely that
	// this ran before (ADR-073).
	SkippedOutcome PublicationOutcome = "skipped"
)

// Result is what came of one repository's publication.
//
// This is what a publication has and a viewer command does not: something to
// record. The recording itself is the task's, because the daemon is the only
// writer of persistent state; what is here is the finding it writes down.
type Result struct {
	// RepositoryID is the repository the result is about.
	RepositoryID domain.RepositoryID
	// Outcome is what happened.
	Outcome PublicationOutcome
	// Request is the merge request that was opened, on a published result and
	// on a skipped one that names the request opened earlier.
	Request domain.MergeRequest
	// Detail says why, in the words of whatever decided: the forge's own
	// refusal on a failure, and the reason nothing was attempted on a skip.
	Detail string
	// Skipped names what the push did not run in this repository — a configured
	// core.hooksPath, or a pre-push hook — so that a user who depends on one
	// learns it from the publication rather than afterwards (ADR-070).
	Skipped []string
}

// Published reports whether a merge request was opened for this repository,
// now or earlier.
func (r Result) Published() bool {
	return r.Outcome == PublishedOutcome || r.Outcome == SkippedOutcome
}

// SummarizePublication renders what a whole publication amounted to.
//
// It counts rather than lists, because a task can publish to several
// repositories and this is one line in a history. What failed is named where a
// user acts on it: on the screen, beside the repository it failed for.
func SummarizePublication(results []Result) string {
	var published, failed, skipped int
	for _, result := range results {
		switch result.Outcome {
		case PublishedOutcome:
			published++
		case FailedOutcome:
			failed++
		case SkippedOutcome:
			skipped++
		}
	}

	parts := []string{fmt.Sprintf("%d published", published)}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d already published", skipped))
	}
	return strings.Join(parts, ", ")
}
