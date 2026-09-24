// Package brief reads a task brief and says what to call it.
//
// It is one rule with two callers that cannot import each other:
// `feat implement --file` reads the file before a screen exists, and the import
// screen reads the file a user typed into it. internal/ui cannot import
// internal/cli, so the policy lives in a package both call (ADR-083). Title is
// here for the same reason, because a second guess would be a second answer.
//
// The client reads the file and the daemon never learns its path, because no
// caller-supplied filesystem path crosses the socket (ADR-028). The text is what
// is sent; the path is recorded only so a task can say where its brief came
// from, and the caller builds that record.
//
// This package does not decide whether the text is a brief anybody wants. An
// empty document is refused where the user can do something about it.
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

// MaxBytes bounds an imported brief on the client side. The daemon applies its
// own limit of the same size, and this one avoids reading a whole disk image
// into memory before hearing it.
const MaxBytes = 256 << 10

// Read returns the text of the file at path and the absolute path it was read
// from. It expands a leading "~", refuses a directory, and refuses a file above
// MaxBytes. The size comes from the directory entry, so an over-large file is
// refused without being loaded.
func Read(path string) (text, absolute string, err error) {
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("name the file the task brief is in")
	}

	expanded := path
	if strings.HasPrefix(path, "~") {
		// Only a "~" needs the home directory. An ordinary path resolves
		// without one, so a machine that cannot name a home keeps working.
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

// ReadFrom returns the text of a brief arriving on a stream, which is what
// `feat implement --file -` reads. It carries no path, because piped text is not
// a document on disk and has no origin to record.
//
// MaxBytes is enforced by reading one byte past it. A stream has no size to ask
// for in advance, so an over-large one is caught by not ending where it had to.
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

// Title derives a title from a brief: the first Markdown heading, or failing
// that the first line of text. A caller who can offer an edit should; one who
// cannot gets a title from the user's own words rather than none.
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
