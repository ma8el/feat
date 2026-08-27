package control

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ma8el/feat/internal/domain"
)

// Bounds on one publication draft.
//
// They are narrower than the envelope's own limit because what they bound is
// prose bound for somebody else's server: a title becomes a line in a merge
// request list, and a description becomes a page somebody reads. A draft over
// either bound is refused rather than truncated, because a description cut in
// half is still sent and still reads as the agent's account.
const (
	// MaxDraftRepositories bounds how many repositories one draft covers. A
	// publication is one merge request per changed repository, and a task binds
	// the repositories its project configures, so a draft naming more than this
	// is not describing a task Feat prepared.
	MaxDraftRepositories = 32
	// MaxDraftTitle bounds one merge request title.
	MaxDraftTitle = 200
	// MaxDraftBody bounds one merge request description.
	MaxDraftBody = 32 << 10
)

// PublicationDraft is what the agent proposes publishing, one entry per
// repository.
//
// It is an account rather than an action, which is why it requires no
// capability: it asks Feat for nothing. What it carries is the one part of a
// publication the agent is the only one who knows — the words describing what
// it did — and the host composes the request from those words together with
// what Feat already knows: the remote, the base branch, and the task
// (ADR-070).
//
// Nothing here is sent anywhere on the strength of the agent having written it.
// The description can carry whatever the agent read, including text injected
// through a dependency or an issue body, so a user reading it before it is sent
// is the control that exists; this type's validation is a bound on shape and
// size and is never a judgement about content.
type PublicationDraft struct {
	// Repositories are the per-repository drafts, in the order the agent wrote
	// them.
	Repositories []RepositoryDraft `json:"repositories"`
}

// RepositoryDraft is one repository's part of a publication draft.
type RepositoryDraft struct {
	// RepositoryID names the repository within the project, as the task's own
	// bindings name it.
	RepositoryID string `json:"repository"`
	// Title is the merge request's title.
	Title string `json:"title"`
	// Body is the merge request's description. It may be empty: a description
	// nobody wrote is better than one invented here.
	Body string `json:"body"`
	// Commit is the commit this draft describes.
	//
	// It is carried by the draft rather than deduced later because the draft
	// and the review request are separate messages and two messages can drift.
	// A draft describing a commit that is no longer current is refused rather
	// than published, as a confirmation fingerprint refuses a draft that
	// changed after it was displayed (ADR-070, ADR-031).
	Commit string `json:"commit"`
}

// commitPattern is a full object name, which is what a draft names.
//
// An abbreviation is refused rather than resolved: resolving one would mean
// asking Git what the agent meant, and the point of the field is that the draft
// says which commit it describes.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Repository returns the draft written for one repository.
func (d PublicationDraft) Repository(id domain.RepositoryID) (RepositoryDraft, bool) {
	for _, entry := range d.Repositories {
		if entry.RepositoryID == id.String() {
			return entry, true
		}
	}
	return RepositoryDraft{}, false
}

// DecodePublicationDraft reads the payload of a publication-draft message.
//
// The payload is validated here rather than left to the provider adapter, which
// is the opposite of a provider event: a provider event carries the provider's
// own document and only its adapter knows what is in it, while a draft is a
// document this protocol defines and every provider writes the same one.
//
// A payload this refuses is the agent's mistake and is settled as refused, so
// the returned error is a *RejectionError: the agent is told once, and the
// message is neither applied nor offered again.
func DecodePublicationDraft(message Message) (PublicationDraft, error) {
	if message.Type != TypePublicationDraft {
		return PublicationDraft{}, &RejectionError{
			File:   message.File(),
			Reason: "is a " + string(message.Type) + " and was read as a publication draft",
		}
	}

	var draft PublicationDraft
	if len(message.Payload) == 0 {
		return PublicationDraft{}, &RejectionError{
			File:   message.File(),
			Reason: "carries no publication draft, and a draft is what the message is for",
		}
	}
	if err := json.Unmarshal(message.Payload, &draft); err != nil {
		return PublicationDraft{}, &RejectionError{
			File:   message.File(),
			Reason: "carries a publication draft this build cannot read: " + err.Error(),
		}
	}
	if err := draft.validate(message.File()); err != nil {
		return PublicationDraft{}, err
	}
	return draft, nil
}

// validate checks the shape and the bounds of a decoded draft.
func (d PublicationDraft) validate(file string) error {
	refuse := func(format string, args ...any) error {
		return &RejectionError{File: file, Reason: fmt.Sprintf(format, args...)}
	}

	if len(d.Repositories) == 0 {
		return refuse("carries a publication draft for no repository, and a publication is one merge request " +
			"per changed repository")
	}
	if len(d.Repositories) > MaxDraftRepositories {
		return refuse("carries a publication draft for %d repositories, and the limit is %d",
			len(d.Repositories), MaxDraftRepositories)
	}

	seen := make(map[string]bool, len(d.Repositories))
	for _, entry := range d.Repositories {
		id := domain.RepositoryID(entry.RepositoryID)
		if err := id.Validate(); err != nil {
			return refuse("drafts a merge request against %s, which is not a repository identifier: %v",
				quote(entry.RepositoryID), err)
		}
		if seen[entry.RepositoryID] {
			return refuse("drafts two merge requests for repository %s, and a publication opens one per repository",
				quote(entry.RepositoryID))
		}
		seen[entry.RepositoryID] = true

		if strings.TrimSpace(entry.Title) == "" {
			return refuse("drafts a merge request for repository %s with no title", quote(entry.RepositoryID))
		}
		if len(entry.Title) > MaxDraftTitle {
			return refuse("drafts a title of %d bytes for repository %s, and the limit is %d",
				len(entry.Title), quote(entry.RepositoryID), MaxDraftTitle)
		}
		if strings.ContainsAny(entry.Title, "\r\n") {
			return refuse("drafts a title for repository %s that spans more than one line",
				quote(entry.RepositoryID))
		}
		if len(entry.Body) > MaxDraftBody {
			return refuse("drafts a description of %d bytes for repository %s, and the limit is %d",
				len(entry.Body), quote(entry.RepositoryID), MaxDraftBody)
		}
		if !commitPattern.MatchString(entry.Commit) {
			return refuse("drafts a merge request for repository %s describing %s, "+
				"which is not a full commit; a draft names the commit it describes so that one written "+
				"before further work is refused rather than published",
				quote(entry.RepositoryID), quote(entry.Commit))
		}
	}
	return nil
}
