// Package brief reads a task brief out of a Markdown file the user named.
//
// It is one rule with two callers, and neither may import the other:
// `feat implement --file` reads the file before a screen exists, and the import
// screen reads the file a user typed into it. internal/ui cannot import
// internal/cli — the dependency runs the other way — so the policy lives in a
// package both can call rather than in whichever of them wrote it first
// (ADR-083).
//
// The client reads the file and the daemon never learns its path: no
// caller-supplied filesystem path crosses the socket (ADR-028). What is sent is
// the text, and the path is recorded only so that a task can say where its brief
// came from. Building that record is the caller's, because this package knows
// nothing about api.Source and a package that reads a file should not have to.
//
// What it does not decide is whether the text is a brief anybody wants. An empty
// document is refused where the user can do something about it — the screen they
// typed the path into — rather than here.
package brief

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ma8el/feat/internal/paths"
)

// MaxBytes bounds an imported brief on the client side.
//
// The daemon applies its own limit of the same size; reading a whole disk image
// into memory before being told so is worth avoiding here as well.
const MaxBytes = 256 << 10

// Read returns the text of the file at path and the absolute path it was read
// from.
//
// A leading "~" is expanded, the path is made absolute, a directory is refused,
// and so is a file above MaxBytes — the size is read from the directory entry
// rather than by reading and counting, so an over-large file is refused without
// being loaded.
func Read(path string) (text, absolute string, err error) {
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("name the file the task brief is in")
	}

	expanded := path
	if strings.HasPrefix(path, "~") {
		// Only a "~" needs the home directory, and only then is a machine that
		// cannot name one a problem: an ordinary path resolves without it, and a
		// command that already worked there goes on working.
		env, err := paths.Current()
		if err != nil {
			return "", "", err
		}
		if expanded, err = env.Expand(path); err != nil {
			return "", "", err
		}
	}

	absolute, err = filepath.Abs(expanded)
	if err != nil {
		return "", "", fmt.Errorf("resolving %s: %w", path, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", "", fmt.Errorf("reading the task brief: %w", err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("%s is a directory, not a task brief", absolute)
	}
	if info.Size() > MaxBytes {
		return "", "", fmt.Errorf("the task brief %s is %d bytes, and the limit is %d",
			absolute, info.Size(), MaxBytes)
	}

	content, err := os.ReadFile(absolute) // #nosec G304 -- the user named this file themselves
	if err != nil {
		return "", "", fmt.Errorf("reading the task brief: %w", err)
	}
	return string(content), absolute, nil
}
