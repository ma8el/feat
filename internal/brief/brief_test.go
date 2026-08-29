package brief_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/brief"
)

// TestAFileIsReadAndReportedByItsAbsolutePath is what the callers need from
// this package: the text, and the path a task records so that it can say where
// its brief came from.
func TestAFileIsReadAndReportedByItsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(path, []byte("# Rename the runtime\n\nDo it everywhere.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	text, absolute, err := brief.Read(path)
	if err != nil {
		t.Fatalf("reading a brief: %v", err)
	}
	if !strings.Contains(text, "Rename the runtime") {
		t.Errorf("text = %q, want the file's own words", text)
	}
	if absolute != path {
		t.Errorf("absolute = %q, want %q", absolute, path)
	}
}

// TestARelativePathIsMadeAbsolute checks the half of the contract the caller
// records: a task says where its brief came from, and a path relative to
// whichever directory a client happened to be started in says nothing.
func TestARelativePathIsMadeAbsolute(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte("Do the thing."), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	_, absolute, err := brief.Read("brief.md")
	if err != nil {
		t.Fatalf("reading a brief: %v", err)
	}
	if !filepath.IsAbs(absolute) {
		t.Errorf("absolute = %q, want an absolute path", absolute)
	}
	// The temporary directory is a symbolic link on macOS, so what is compared
	// is the file rather than the spelling of the directory above it.
	if filepath.Base(absolute) != "brief.md" {
		t.Errorf("absolute = %q, want the file that was read", absolute)
	}
}

// TestALeadingTildeIsExpanded is the path a user types on the import screen,
// where no shell has been anywhere near it.
func TestALeadingTildeIsExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "notes.md")
	if err := os.WriteFile(path, []byte("Written in an editor first."), 0o600); err != nil {
		t.Fatal(err)
	}

	text, absolute, err := brief.Read("~/notes.md")
	if err != nil {
		t.Fatalf("reading a brief under ~: %v", err)
	}
	if text != "Written in an editor first." {
		t.Errorf("text = %q", text)
	}
	if absolute != path {
		t.Errorf("absolute = %q, want %q", absolute, path)
	}
}

// TestAnotherUsersHomeIsRefusedRatherThanResolved follows paths.Expand, which
// declines to reach into a home directory that is not the user's own.
func TestAnotherUsersHomeIsRefusedRatherThanResolved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, _, err := brief.Read("~someone/brief.md"); err == nil {
		t.Fatal("a path in another user's home was resolved")
	}
}

func TestADirectoryIsNotABrief(t *testing.T) {
	dir := t.TempDir()

	_, _, err := brief.Read(dir)
	if err == nil {
		t.Fatal("a directory was accepted as a task brief")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error = %v, want it to say what was wrong with the path", err)
	}
}

// TestAFileOverTheLimitIsRefusedBeforeItIsRead is why the size is taken from the
// directory entry: the refusal must not require loading what it is refusing.
func TestAFileOverTheLimitIsRefusedBeforeItIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.md")
	if err := os.WriteFile(path, make([]byte, brief.MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := brief.Read(path)
	if err == nil {
		t.Fatal("a file over the limit was read")
	}
	for _, want := range []string{path, "the limit is"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to say %q", err, want)
		}
	}
}

// TestAFileAtTheLimitIsRead checks that the bound is a limit rather than an
// off-by-one refusal of the largest brief it promises to accept.
func TestAFileAtTheLimitIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.md")
	if err := os.WriteFile(path, make([]byte, brief.MaxBytes), 0o600); err != nil {
		t.Fatal(err)
	}

	text, _, err := brief.Read(path)
	if err != nil {
		t.Fatalf("reading a brief of exactly the limit: %v", err)
	}
	if len(text) != brief.MaxBytes {
		t.Errorf("read %d bytes, want %d", len(text), brief.MaxBytes)
	}
}

func TestAMissingFileSaysWhatItWasReading(t *testing.T) {
	_, _, err := brief.Read(filepath.Join(t.TempDir(), "nothing.md"))
	if err == nil {
		t.Fatal("a file that does not exist was read")
	}
	if !strings.Contains(err.Error(), "reading the task brief") {
		t.Errorf("error = %v, want it to name what it was doing", err)
	}
}

func TestAnEmptyPathIsRefused(t *testing.T) {
	if _, _, err := brief.Read("   "); err == nil {
		t.Fatal("an empty path was accepted")
	}
}

// TestAnEmptyFileIsNotThisPackagesToRefuse records the boundary: a document with
// no text is refused on the screen the user typed the path into, where they can
// do something about it, and this package returns what the file holds.
func TestAnEmptyFileIsNotThisPackagesToRefuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	text, _, err := brief.Read(path)
	if err != nil {
		t.Fatalf("reading an empty file: %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want the empty file's own contents", text)
	}
}
