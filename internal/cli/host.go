package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
	"github.com/ma8el/feat/internal/wizard"
)

// machineHost answers the wizard's questions about this machine.
//
// It is the one place the flow's questions meet the host: Git is run here,
// Compose files are read here, and paths are expanded here. The flow itself
// names none of it, which is what lets the dashboard drive the same questions
// without reaching an adapter of its own (ADR-031, ADR-063).
type machineHost struct {
	process paths.Environment
	runner  project.Runner
}

var _ wizard.Host = (*machineHost)(nil)

// Inspect asks Git what a directory is.
func (h *machineHost) Inspect(ctx context.Context, path string) (wizard.Checkout, error) {
	checkout, err := project.Inspect(ctx, h.runner, path)
	if err != nil {
		return wizard.Checkout{}, err
	}
	return wizard.Checkout{
		Root:          checkout.Root,
		Remote:        checkout.Remote,
		DefaultBranch: checkout.DefaultBranch,
	}, nil
}

// ComposeFiles returns the Compose files beside a checkout.
func (h *machineHost) ComposeFiles(dir string) []string { return project.ComposeFiles(dir) }

// ComposeServices returns the services the given Compose files declare.
func (h *machineHost) ComposeServices(files ...string) []string {
	return project.ComposeServices(files...)
}

// Compose reads what one repository's Compose files propose.
//
// The reading is internal/project's, and the shape is the wizard's: a
// proposal's whole job is to be put back to the user in the terms of the
// question, and the flow names no adapter of its own.
func (h *machineHost) Compose(root string, files ...string) wizard.Composition {
	composition := project.ComposeComposition(root, root, files...)
	services := composition.Names()

	proposed := wizard.Composition{
		Services:  services,
		Reachable: composition.Published(services),
		Undecided: composition.Undecided,
	}
	proposed.ContainerPath, _ = composition.SourceTarget(services)
	for _, service := range composition.Services {
		if service.BuildsFromSource {
			proposed.Baked = append(proposed.Baked, service.Name)
		}
	}
	return proposed
}

// Exists reports whether a path is there now.
func (h *machineHost) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Absolute expands a leading "~" and resolves a relative path against the
// directory the wizard was started in, so that an answer typed the way a shell
// would take it is the path Feat records.
func (h *machineHost) Absolute(value string) (string, error) {
	expanded, err := h.process.Expand(value)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded), nil
	}
	return filepath.Join(h.WorkingDirectory(), expanded), nil
}

// WorkingDirectory returns the directory the process was started in, or the home
// directory when it cannot be read — which happens when it has been removed
// underneath the process, and is not a reason to refuse to configure a project.
func (h *machineHost) WorkingDirectory() string {
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return h.process.Home
}
