package config

import (
	"github.com/goccy/go-yaml"
)

// replaced reports a configuration written in the shape this build no longer
// reads.
//
// There is no version bump and no compatibility period: Feat is used by its
// author and nobody else, so a migration path would buy ceremony (ADR-065). The
// break is still a break the user should not have to diagnose, and strict
// decoding would report each of these fields as an unknown key — which is what
// it says about a typo, and a user who reads it as one goes looking for a
// spelling mistake in a field they spelled correctly.
//
// It runs before the strict decode, so a file in the old shape produces the
// three sentences that name the replacements rather than three sentences about
// keys Feat does not know. A file the lenient decode cannot read is left to the
// strict decode, which reports the syntax error with the line it is on.
func replaced(file string, data []byte) error {
	// Every field is a pointer or a slice, so "absent" and "present but empty"
	// are distinguishable: `container_path:` with nothing after it is still the
	// old shape, and still worth naming.
	var document struct {
		Repositories map[string]struct {
			ContainerPath *string `yaml:"container_path"`
		} `yaml:"repositories"`
		Runtime *struct {
			ComposeFiles []string `yaml:"compose_files"`
			Services     []string `yaml:"services"`
		} `yaml:"runtime"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil
	}

	found := &problems{}
	for _, id := range sortedKeys(document.Repositories) {
		if document.Repositories[id].ContainerPath == nil {
			continue
		}
		found.add("repositories."+id+".container_path",
			"has been replaced by two fields, because it was answering two questions. "+
				"repositories."+id+".agent.container_path is where the agent's devcontainer mounts the "+
				"worktree, which is yours to choose; repositories."+id+".runtime.container_path is where "+
				"this repository's own services expect their source, which is a fact about its Compose files")
	}

	if document.Runtime != nil {
		if document.Runtime.ComposeFiles != nil {
			found.add("runtime.compose_files",
				"has been replaced by repositories.<id>.runtime.compose_files: a runtime is composed of its "+
					"repositories, each bringing its own Compose files resolved against its own checkout, and "+
					"Feat generates the include document that joins them")
		}
		if document.Runtime.Services != nil {
			found.add("runtime.services",
				"has been replaced by repositories.<id>.runtime.services: a service is managed by the "+
					"repository whose code it runs, which is what lets Feat say where that code comes from")
		}
	}
	return found.err(file, data)
}
