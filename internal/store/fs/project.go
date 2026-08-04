package fs

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
)

// projectDocument is the stored form of a project.
//
// The first three fields are the header every generated document carries
// (docs/07-configuration-model.md). The document is a deliberate copy of the
// domain type rather than the domain type itself: the file format is a
// compatibility surface with a migration policy, and it must not change because
// a Go field was renamed.
type projectDocument struct {
	SchemaVersion     int                  `json:"schema_version"`
	ID                string               `json:"id"`
	UpdatedAt         time.Time            `json:"updated_at"`
	Name              string               `json:"name"`
	PrimaryRepository string               `json:"primary_repository"`
	CreatedAt         time.Time            `json:"created_at"`
	Repositories      []repositoryDocument `json:"repositories"`
}

type repositoryDocument struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path,omitempty"`
	DefaultBranch string `json:"default_branch"`
	Remote        string `json:"remote"`
	DefaultAccess string `json:"default_access"`
}

type projectStore struct{ store *Store }

// Save records the project.
func (p projectStore) Save(ctx context.Context, project *domain.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if project == nil {
		return errors.New("saving a project requires a project")
	}
	if err := project.Validate(); err != nil {
		return err
	}
	dir, err := p.store.projectDir(project.ID)
	if err != nil {
		return err
	}

	defer p.store.lock("project:" + project.ID.String())()
	return p.store.writeSnapshot(projectCodec, filepath.Join(dir, projectFile), encodeProject(project))
}

// Load returns one project.
func (p projectStore) Load(ctx context.Context, id domain.ProjectID) (*domain.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := p.store.projectDir(id)
	if err != nil {
		return nil, err
	}
	return p.store.loadProject(id, filepath.Join(dir, projectFile))
}

// List returns every registered project, ordered by identifier.
//
// Entries that are not project directories are skipped rather than reported:
// the state directory is a directory on the user's machine, and an unrelated
// file in it is not a corrupt project.
func (p projectStore) List(ctx context.Context) ([]*domain.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := listDir(filepath.Join(p.store.root, projectsDir))
	if err != nil {
		return nil, err
	}

	projects := make([]*domain.Project, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := domain.ProjectID(entry.Name())
		if id.Validate() != nil {
			continue
		}
		path := filepath.Join(p.store.root, projectsDir, entry.Name(), projectFile)
		project, err := p.store.loadProject(id, path)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (s *Store) loadProject(id domain.ProjectID, path string) (*domain.Project, error) {
	var document projectDocument
	if err := s.readSnapshot(projectCodec, "project", id.String(), path, &document); err != nil {
		return nil, err
	}

	project := decodeProject(document)
	if err := project.Validate(); err != nil {
		return nil, corrupt("project", id.String(), path, err)
	}
	return project, nil
}

func encodeProject(project *domain.Project) projectDocument {
	repositories := make([]repositoryDocument, 0, len(project.Repositories))
	for _, repository := range project.Repositories {
		repositories = append(repositories, repositoryDocument{
			ID:            repository.ID.String(),
			Name:          repository.Name,
			HostPath:      repository.HostPath,
			ContainerPath: repository.ContainerPath,
			DefaultBranch: repository.DefaultBranch,
			Remote:        repository.Remote,
			DefaultAccess: string(repository.DefaultAccess),
		})
	}
	return projectDocument{
		SchemaVersion:     projectSchemaVersion,
		ID:                project.ID.String(),
		UpdatedAt:         project.UpdatedAt.UTC(),
		Name:              project.Name,
		PrimaryRepository: project.PrimaryRepository.String(),
		CreatedAt:         project.CreatedAt.UTC(),
		Repositories:      repositories,
	}
}

func decodeProject(document projectDocument) *domain.Project {
	var repositories []domain.Repository
	for _, repository := range document.Repositories {
		repositories = append(repositories, domain.Repository{
			ID:            domain.RepositoryID(repository.ID),
			Name:          repository.Name,
			HostPath:      repository.HostPath,
			ContainerPath: repository.ContainerPath,
			DefaultBranch: repository.DefaultBranch,
			Remote:        repository.Remote,
			DefaultAccess: domain.DefaultAccess(repository.DefaultAccess),
		})
	}
	return &domain.Project{
		ID:                domain.ProjectID(document.ID),
		Name:              document.Name,
		PrimaryRepository: domain.RepositoryID(document.PrimaryRepository),
		Repositories:      repositories,
		CreatedAt:         document.CreatedAt.UTC(),
		UpdatedAt:         document.UpdatedAt.UTC(),
	}
}
