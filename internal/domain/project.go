package domain

import (
	"path/filepath"
	"time"
)

// Project is a locally registered development topology.
//
// The entity holds the parts of a project that tasks depend on: identity and
// repository topology. The agent, runtime, review, and notification profiles
// stay in the configuration model, which resolves them into an immutable launch
// snapshot per task (docs/07-configuration-model.md).
type Project struct {
	// ID identifies the project locally.
	ID ProjectID
	// Name is the display name.
	Name string
	// PrimaryRepository is the editable repository used as the default task
	// working directory.
	PrimaryRepository RepositoryID
	// Repositories are the Git repositories participating in the project.
	Repositories []Repository
	// CreatedAt is when the project was registered.
	CreatedAt time.Time
	// UpdatedAt is when the snapshot last changed.
	UpdatedAt time.Time
}

// Repository is a Git repository participating in a project.
type Repository struct {
	// ID identifies the repository within its project.
	ID RepositoryID
	// Name is the display name.
	Name string
	// HostPath is the absolute path of the ordinary checkout on the host.
	// Configuration expands "~" before it reaches the domain.
	HostPath string
	// ContainerPath is the absolute path the repository is mounted at in a
	// devcontainer. It is empty for host-native projects.
	ContainerPath string
	// DefaultBranch is the branch a task's base policy resolves against.
	DefaultBranch string
	// Remote is the Git remote to fetch before resolving a base.
	Remote string
	// DefaultAccess is the repository's default participation in a task.
	DefaultAccess DefaultAccess
}

// NewProject creates a registered project.
func NewProject(id ProjectID, name string, primary RepositoryID, repositories []Repository, now time.Time) (*Project, error) {
	project := &Project{
		ID:                id,
		Name:              name,
		PrimaryRepository: primary,
		Repositories:      repositories,
		CreatedAt:         normalizeTime(now),
		UpdatedAt:         normalizeTime(now),
	}
	if err := project.Validate(); err != nil {
		return nil, err
	}
	return project, nil
}

// Repository returns the repository with the given identifier.
func (p *Project) Repository(id RepositoryID) (Repository, bool) {
	for _, repository := range p.Repositories {
		if repository.ID == id {
			return repository, true
		}
	}
	return Repository{}, false
}

// Validate reports whether the project is internally consistent.
//
// It checks what the domain owns: identity, uniqueness, and the primary
// repository rule. Whether a host path really contains a Git repository is a
// host question that belongs to project diagnostics.
func (p *Project) Validate() error {
	if err := p.ID.Validate(); err != nil {
		return err
	}
	if p.Name == "" {
		return &ValidationError{Entity: "project", ID: p.ID.String(), Field: "name", Reason: "must not be empty"}
	}
	if p.CreatedAt.IsZero() {
		return &ValidationError{Entity: "project", ID: p.ID.String(), Field: "created_at", Reason: "must be set"}
	}
	if p.UpdatedAt.Before(p.CreatedAt) {
		return &ValidationError{Entity: "project", ID: p.ID.String(), Field: "updated_at", Reason: "must not precede created_at"}
	}
	if len(p.Repositories) == 0 {
		return &ValidationError{Entity: "project", ID: p.ID.String(), Field: "repositories", Reason: "must contain at least one repository"}
	}

	seen := make(map[RepositoryID]bool, len(p.Repositories))
	for _, repository := range p.Repositories {
		if err := repository.Validate(p.ID); err != nil {
			return err
		}
		if seen[repository.ID] {
			return &ValidationError{
				Entity: "project",
				ID:     p.ID.String(),
				Field:  "repositories",
				Reason: "must not repeat repository " + repository.ID.String(),
			}
		}
		seen[repository.ID] = true
	}

	primary, ok := p.Repository(p.PrimaryRepository)
	if !ok {
		return &ValidationError{
			Entity: "project",
			ID:     p.ID.String(),
			Field:  "primary_repository",
			Reason: "must name a repository of this project, but " + p.PrimaryRepository.String() + " is not one",
		}
	}
	if !primary.DefaultAccess.CanBeReadWrite() {
		return &ValidationError{
			Entity: "project",
			ID:     p.ID.String(),
			Field:  "primary_repository",
			Reason: "must name a repository a task can edit, but " + primary.ID.String() + " defaults to " + string(primary.DefaultAccess),
		}
	}
	return nil
}

// Validate reports whether the repository entry is internally consistent.
func (r Repository) Validate(project ProjectID) error {
	// The identifier is checked with the project in the subject, so that a
	// message about one repository of several says which project it is in.
	id := project.String() + "/" + r.ID.String()
	if err := validateSafeID("repository", id, "id", r.ID.String()); err != nil {
		return err
	}
	if r.Name == "" {
		return &ValidationError{Entity: "repository", ID: id, Field: "name", Reason: "must not be empty"}
	}
	if !isAbsPath(r.HostPath) {
		return &ValidationError{
			Entity: "repository",
			ID:     id,
			Field:  "host_path",
			Reason: "must be absolute, but is " + quote(r.HostPath),
		}
	}
	// The container path is optional because host-native execution has none,
	// but a relative one would produce an unpredictable mount.
	if r.ContainerPath != "" && !isAbsSlashPath(r.ContainerPath) {
		return &ValidationError{
			Entity: "repository",
			ID:     id,
			Field:  "container_path",
			Reason: "must be an absolute path inside the execution environment, but is " + quote(r.ContainerPath),
		}
	}
	if r.DefaultBranch == "" {
		return &ValidationError{Entity: "repository", ID: id, Field: "default_branch", Reason: "must not be empty"}
	}
	if r.Remote == "" {
		return &ValidationError{Entity: "repository", ID: id, Field: "remote", Reason: "must not be empty"}
	}
	if !r.DefaultAccess.Valid() {
		return &ValidationError{
			Entity: "repository",
			ID:     id,
			Field:  "default_access",
			Reason: "must be a documented access mode, but is " + quote(string(r.DefaultAccess)),
		}
	}
	return nil
}

// isAbsPath reports whether a host path is absolute.
func isAbsPath(path string) bool { return filepath.IsAbs(path) }

// isAbsSlashPath reports whether a path is absolute inside an execution
// environment. Container paths are always slash-separated, so they cannot be
// judged with filepath, whose meaning depends on the host operating system.
func isAbsSlashPath(path string) bool {
	return len(path) > 0 && path[0] == '/'
}

// quote renders a value inside double quotes so an error message shows an empty
// or space-padded value clearly.
func quote(value string) string { return `"` + value + `"` }

// normalizeTime puts a timestamp in UTC and strips the monotonic clock reading,
// so that a stored timestamp and a reloaded one compare equal.
func normalizeTime(t time.Time) time.Time { return t.UTC().Round(0) }
