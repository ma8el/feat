package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

// Accepted file extensions, in the order a directory listing prefers them.
var extensions = []string{".yaml", ".yml"}

// Options are the process facts resolving a configuration depends on.
type Options struct {
	// Env expands a leading "~" and reads the environment a default may come
	// from, such as $EDITOR. Its Home must be set.
	Env paths.Environment
	// StateDir is Feat's state directory, which the default worktree root is
	// placed under. An empty value leaves the default worktree root
	// unresolved, which validation then rejects.
	StateDir string
}

// File returns the configuration file path for a project identifier.
//
// The identifier is validated before it is joined into a path, so a caller
// cannot reach outside the configuration directory with one.
func File(dir, id string) (string, error) {
	if err := domain.ProjectID(id).Validate(); err != nil {
		return "", err
	}
	return filepath.Join(dir, id+extensions[0]), nil
}

// Find returns the configuration file for a project identifier.
//
// Both accepted extensions are looked for. Finding two is an error rather than
// a preference: which of them Feat used would otherwise depend on a rule the
// user has no reason to know, and the one they edited might be the other one.
func Find(dir, id string) (string, error) {
	if err := domain.ProjectID(id).Validate(); err != nil {
		return "", err
	}

	var found []string
	for _, extension := range extensions {
		candidate := filepath.Join(dir, id+extension)
		if _, err := os.Stat(candidate); err == nil {
			found = append(found, candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("looking for the configuration of project %s: %w", id, err)
		}
	}

	switch len(found) {
	case 0:
		expected, err := File(dir, id)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w for %s: write one at %s", ErrNotFound, id, expected)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf(
			"project %s is configured twice, by %s and %s: keep one of them",
			id, found[0], found[1])
	}
}

// List returns the project identifiers configured in a directory, in order.
//
// A file whose name is not a valid project identifier is skipped rather than
// reported: the configuration directory belongs to the user, and a note to
// themselves left next to their configuration is not a broken project.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the project configuration directory %s: %w", dir, err)
	}

	seen := make(map[string]bool, len(entries))
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		extension := filepath.Ext(name)
		if !slices.Contains(extensions, extension) {
			continue
		}
		id := strings.TrimSuffix(name, extension)
		if domain.ProjectID(id).Validate() != nil || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// Load reads, resolves, and validates the configuration of one project.
//
// It is the only way to obtain a Config that is safe to use: Parse alone
// returns unexpanded paths and unfilled defaults, and Resolve alone returns a
// configuration nothing has checked.
func Load(dir, id string, opts Options) (*Config, error) {
	file, err := Find(dir, id)
	if err != nil {
		return nil, err
	}
	return LoadFile(file, opts)
}

// LoadFile reads, resolves, and validates one configuration file.
func LoadFile(file string, opts Options) (*Config, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNotFound, file)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}

	config, err := Parse(file, data)
	if err != nil {
		return nil, err
	}
	if err := config.Resolve(opts); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// Parse decodes one configuration document.
//
// Decoding is strict in both directions that matter to a hand-edited file: a
// field Feat does not know is an error rather than a value silently ignored,
// and a key given twice is an error rather than a value silently discarded.
// Either would let a user believe they had configured something they had not.
func Parse(file string, data []byte) (*Config, error) {
	config := &Config{path: file, source: data}

	if err := yaml.UnmarshalWithOptions(data, config, yaml.Strict()); err != nil {
		return nil, decodingError(file, data, err)
	}

	// The file name carries the project identifier, so a document that names a
	// different one has two answers to the same question.
	//
	// An identifier that is not a valid one is left to Validate, which says why.
	// Comparing it here would answer a malformed identifier by suggesting the
	// user rename their file to match it.
	if file != "" && domain.ProjectID(config.Project.ID).Validate() == nil {
		stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if stem != config.Project.ID {
			return nil, &Error{
				File:   file,
				source: data,
				Problems: []Problem{{
					Path: "project.id",
					Reason: fmt.Sprintf(
						"is %q, but the file is named %q: rename the file to %s%s, or change the identifier",
						config.Project.ID, filepath.Base(file), config.Project.ID, extensions[0]),
				}},
			}
		}
	}
	return config, nil
}

// decodingError turns a YAML decoding failure into a configuration error.
//
// The decoder already knows the line, the column, and the surrounding lines of
// what it rejected, which is most of what makes an unknown field actionable.
// That excerpt is kept for Annotated and left out of Error, so that the same
// failure reads well both in a terminal and in a log line.
func decodingError(file string, data []byte, err error) error {
	return &Error{
		File:   file,
		source: data,
		Problems: []Problem{{
			Reason:  strings.TrimSpace(yaml.FormatError(err, false, false)),
			excerpt: strings.TrimRight(yaml.FormatError(err, false, true), "\n"),
		}},
	}
}
