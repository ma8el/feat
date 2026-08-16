package project

import (
	"time"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
)

// FromConfig maps a validated configuration onto the domain entity.
//
// The two are deliberately not the same type. Configuration is the whole
// project profile — agent execution, runtime, review commands, capabilities —
// and the domain entity is the part tasks depend on: identity and repository
// topology. Everything else is resolved into an immutable launch snapshot per
// task, so that editing the YAML never changes what a running task is doing
// (docs/07-configuration-model.md).
//
// The configuration must already be valid. This function nonetheless returns
// the domain's own validation error rather than assuming it: the two rule sets
// are maintained separately, and the day they disagree, the disagreement should
// stop a project from being registered rather than be recorded as state.
func FromConfig(cfg *config.Config, now time.Time) (*domain.Project, error) {
	ids := cfg.RepositoryIDs()
	repositories := make([]domain.Repository, 0, len(ids))
	for _, id := range ids {
		repository, _ := cfg.Repository(id)
		repositories = append(repositories, domain.Repository{
			ID:            domain.RepositoryID(id),
			Name:          repository.Name,
			HostPath:      repository.HostPath,
			ContainerPath: repository.Agent.ContainerPath,
			DefaultBranch: repository.DefaultBranch,
			Remote:        repository.Remote,
			DefaultAccess: domain.DefaultAccess(repository.DefaultAccess),
		})
	}

	return domain.NewProject(
		domain.ProjectID(cfg.Project.ID),
		cfg.Project.Name,
		domain.RepositoryID(cfg.Project.PrimaryRepository),
		repositories,
		now,
	)
}

// Update applies a reloaded configuration to an already registered project.
//
// Re-registering an edited configuration updates the record and keeps the
// original registration time, so that a project does not appear to have been
// created today because its YAML was edited today.
func Update(existing *domain.Project, cfg *config.Config, now time.Time) (*domain.Project, error) {
	updated, err := FromConfig(cfg, now)
	if err != nil {
		return nil, err
	}
	updated.CreatedAt = existing.CreatedAt
	if err := updated.Validate(); err != nil {
		return nil, err
	}
	return updated, nil
}
