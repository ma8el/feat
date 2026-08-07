package control_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/control"
)

// TestAVerdictIsReadableByAShellScript pins the one property the document's
// shape exists for: the first line carries the format, the version, and the
// status, in that order and separated by single spaces, so that the generated
// helper can read all three without parsing anything.
//
// The helper is a POSIX shell script, and a document that needed a JSON parser
// on that side would put interpretation into a generated script — which is the
// thing ADR-032 keeps out of them.
func TestAVerdictIsReadableByAShellScript(t *testing.T) {
	workspace, _ := newWorkspace(t)

	if err := workspace.WriteVerification("abc123", control.Verification{
		Status: control.VerificationFailed,
		Report: "check test (api): failed\n  2 failed, 82 passed",
	}); err != nil {
		t.Fatalf("writing a verdict: %v", err)
	}

	name, err := control.VerificationName("abc123")
	if err != nil {
		t.Fatalf("naming a verdict: %v", err)
	}
	document, err := os.ReadFile(filepath.Join(workspace.InboxDir(), name))
	if err != nil {
		t.Fatalf("reading the verdict back: %v", err)
	}

	lines := strings.Split(string(document), "\n")
	fields := strings.Fields(lines[0])
	if len(fields) != 3 {
		t.Fatalf("the first line is %q, want a format, a version, and a status", lines[0])
	}
	if fields[0] != "feat-verification" {
		t.Errorf("the document names itself %q", fields[0])
	}
	if fields[1] != "1" {
		t.Errorf("the schema version is %q, want 1", fields[1])
	}
	if fields[2] != control.VerificationFailed {
		t.Errorf("the status is %q, want %q", fields[2], control.VerificationFailed)
	}
	if !strings.Contains(string(document), "2 failed, 82 passed") {
		t.Error("the report the agent has to act on is not in the document")
	}
}

// TestAVerdictIsNamedAfterItsRequest checks that an answer to one review request
// can never be read as the answer to the next one.
func TestAVerdictIsNamedAfterItsRequest(t *testing.T) {
	first, err := control.VerificationName("aaa")
	if err != nil {
		t.Fatalf("naming a verdict: %v", err)
	}
	second, err := control.VerificationName("bbb")
	if err != nil {
		t.Fatalf("naming a verdict: %v", err)
	}
	if first == second {
		t.Fatalf("two requests are answered by the same file %q", first)
	}
	if strings.HasSuffix(first, ".json") {
		t.Errorf("a verdict is named %q, which the outbox reader would try to read as a message", first)
	}
}

// TestAVerdictNameCannotLeaveTheInbox checks the rule every name in this package
// obeys: a message identifier reaches a path, so it is a plain name or it is
// refused.
func TestAVerdictNameCannotLeaveTheInbox(t *testing.T) {
	for _, request := range []string{"", ".", "..", "../../etc/passwd", "a/b", `a\b`, "a\nb", strings.Repeat("a", 200)} {
		if _, err := control.VerificationName(request); err == nil {
			t.Errorf("the request identifier %q was accepted as a file name", request)
		}
	}
}

// TestAVerdictStatusIsFromTheDocumentedSet checks that a status the helper does
// not know cannot be written, because the helper would have to guess what it
// meant.
func TestAVerdictStatusIsFromTheDocumentedSet(t *testing.T) {
	workspace, _ := newWorkspace(t)

	if err := workspace.WriteVerification("abc123", control.Verification{Status: "probably fine"}); err == nil {
		t.Fatal("an undocumented verdict status was written")
	}
}

// TestAVerdictIsNotReadAsAControlMessage checks that writing into the inbox does
// not produce anything in the outbox reader's view. The two directions are
// separate directories for exactly this reason.
func TestAVerdictIsNotReadAsAControlMessage(t *testing.T) {
	workspace, _ := newWorkspace(t)

	if err := workspace.WriteVerification("abc123", control.Verification{
		Status: control.VerificationPassed,
	}); err != nil {
		t.Fatalf("writing a verdict: %v", err)
	}

	messages, rejected, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading the outbox: %v", err)
	}
	if len(messages) != 0 || len(rejected) != 0 {
		t.Errorf("the outbox reader found %d messages and %d rejections after a verdict was written",
			len(messages), len(rejected))
	}
}
