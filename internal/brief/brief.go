// Package brief reads a task brief and says what to call it.
//
// It is one rule with two callers, and neither may import the other:
// `feat implement --file` reads the file before a screen exists, and the import
// screen reads the file a user typed into it. internal/ui cannot import
// internal/cli — the dependency runs the other way — so the policy lives in a
// package both can call rather than in whichever of them wrote it first
// (ADR-083). Title is here for the same reason and arrived the same way: the
// screen guessed a title from an imported document long before a headless run
// needed one, and a second guess would be a second answer.
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
	"io"
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

// ReadFrom returns the text of a brief arriving on a stream.
//
// It is what `feat implement --file -` reads, and it carries no path: a brief a
// caller piped in is text they supplied rather than a document on disk, so
// there is nothing to record as its origin.
//
// MaxBytes is enforced by reading one byte past it. A stream has no size to ask
// for in advance, so the only way to refuse an over-large one is to notice that
// it did not end where it had to.
func ReadFrom(reader io.Reader) (string, error) {
	content, err := io.ReadAll(io.LimitReader(reader, MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading the task brief: %w", err)
	}
	if len(content) > MaxBytes {
		return "", fmt.Errorf("the task brief is longer than the limit of %d bytes", MaxBytes)
	}
	return string(content), nil
}

// Title derives a title from a brief.
//
// The first Markdown heading is what the document calls itself; failing that,
// the first line of text is. A caller who can change it should offer that —
// which is why guessing is worth doing at all — and a caller who cannot has a
// title derived from words they wrote rather than none.
func Title(document string) string {
	for _, line := range strings.Split(document, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if heading := strings.TrimLeft(line, "#"); heading != line {
			return truncate(strings.TrimSpace(heading))
		}
		return truncate(line)
	}
	return ""
}

// titleLimit keeps a derived title to something a task row can show.
const titleLimit = 72

func truncate(title string) string {
	runes := []rune(title)
	if len(runes) <= titleLimit {
		return title
	}
	return strings.TrimSpace(string(runes[:titleLimit]))
}
