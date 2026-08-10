package fs

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
)

// reviewDocument is the stored form of a task's review state.
//
// It carried a status and a decision time until ADR-047. Both were the user's
// decision, which the task's own workflow state records, and a document written
// by an earlier build still carries them: they are ignored on read and gone from
// the file the next save writes. The schema version does not move for that,
// because nothing has to be upgraded — no information is lost that the task
// snapshot beside it does not already hold.
type reviewDocument struct {
	SchemaVersion     int                        `json:"schema_version"`
	ID                string                     `json:"id"`
	UpdatedAt         time.Time                  `json:"updated_at"`
	CompletionSummary string                     `json:"completion_summary,omitempty"`
	RequestedAt       *time.Time                 `json:"requested_at,omitempty"`
	Repositories      []repositoryChangeDocument `json:"repositories,omitempty"`
	Checks            []checkDocument            `json:"checks,omitempty"`
}

type repositoryChangeDocument struct {
	RepositoryID string    `json:"repository_id"`
	BaseCommit   string    `json:"base_commit"`
	HeadCommit   string    `json:"head_commit,omitempty"`
	ChangedFiles int       `json:"changed_files"`
	Insertions   int       `json:"insertions"`
	Deletions    int       `json:"deletions"`
	Dirty        bool      `json:"dirty"`
	SummarizedAt time.Time `json:"summarized_at"`
}

type checkDocument struct {
	ID           string     `json:"id"`
	RepositoryID string     `json:"repository_id,omitempty"`
	Status       string     `json:"status"`
	Reporter     string     `json:"reporter"`
	Detail       string     `json:"detail,omitempty"`
	RanAt        *time.Time `json:"ran_at,omitempty"`
}

type reviewStore struct{ store *Store }

// Save records the review.
func (r reviewStore) Save(ctx context.Context, ref store.TaskRef, review *domain.Review) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if review == nil {
		return errors.New("saving a review requires a review")
	}
	if err := review.Validate(); err != nil {
		return err
	}
	if review.TaskID != ref.Task {
		return errors.New("review of task " + review.TaskID.String() + " cannot be stored under " + ref.String())
	}
	dir, err := r.store.taskDir(ref)
	if err != nil {
		return err
	}

	defer r.store.lock("review:" + ref.String())()
	return r.store.writeSnapshot(reviewCodec, filepath.Join(dir, reviewFile), encodeReview(review))
}

// Load returns one task's review state.
func (r reviewStore) Load(ctx context.Context, ref store.TaskRef) (*domain.Review, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := r.store.taskDir(ref)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, reviewFile)

	var document reviewDocument
	if err := r.store.readSnapshot(reviewCodec, "review", ref.String(), path, &document); err != nil {
		return nil, err
	}

	review := decodeReview(document)
	if err := review.Validate(); err != nil {
		return nil, corrupt("review", ref.String(), path, err)
	}
	if review.TaskID != ref.Task {
		return nil, corrupt("review", ref.String(), path,
			errors.New("the snapshot records a review of task "+review.TaskID.String()))
	}
	return review, nil
}

func encodeReview(review *domain.Review) reviewDocument {
	document := reviewDocument{
		SchemaVersion:     reviewSchemaVersion,
		ID:                review.TaskID.String(),
		UpdatedAt:         review.UpdatedAt.UTC(),
		CompletionSummary: review.CompletionSummary,
		RequestedAt:       optionalTime(review.RequestedAt),
	}
	for _, change := range review.Repositories {
		document.Repositories = append(document.Repositories, repositoryChangeDocument{
			RepositoryID: change.RepositoryID.String(),
			BaseCommit:   change.BaseCommit,
			HeadCommit:   change.HeadCommit,
			ChangedFiles: change.ChangedFiles,
			Insertions:   change.Insertions,
			Deletions:    change.Deletions,
			Dirty:        change.Dirty,
			SummarizedAt: change.SummarizedAt.UTC(),
		})
	}
	for _, check := range review.Checks {
		document.Checks = append(document.Checks, checkDocument{
			ID:           check.ID,
			RepositoryID: check.RepositoryID.String(),
			Status:       string(check.Status),
			Reporter:     string(check.Reporter),
			Detail:       check.Detail,
			RanAt:        optionalTime(check.RanAt),
		})
	}
	return document
}

func decodeReview(document reviewDocument) *domain.Review {
	review := &domain.Review{
		TaskID:            domain.TaskID(document.ID),
		CompletionSummary: document.CompletionSummary,
		RequestedAt:       timeValue(document.RequestedAt),
		UpdatedAt:         document.UpdatedAt.UTC(),
	}
	for _, change := range document.Repositories {
		review.Repositories = append(review.Repositories, domain.RepositoryChange{
			RepositoryID: domain.RepositoryID(change.RepositoryID),
			BaseCommit:   change.BaseCommit,
			HeadCommit:   change.HeadCommit,
			ChangedFiles: change.ChangedFiles,
			Insertions:   change.Insertions,
			Deletions:    change.Deletions,
			Dirty:        change.Dirty,
			SummarizedAt: change.SummarizedAt.UTC(),
		})
	}
	for _, check := range document.Checks {
		review.Checks = append(review.Checks, domain.Check{
			ID:           check.ID,
			RepositoryID: domain.RepositoryID(check.RepositoryID),
			Status:       domain.CheckStatus(check.Status),
			Reporter:     domain.CheckReporter(check.Reporter),
			Detail:       check.Detail,
			RanAt:        timeValue(check.RanAt),
		})
	}
	return review
}
