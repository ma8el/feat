package control

import (
	"fmt"
	"path/filepath"
	"strings"
)

// The completion gate's verdict, as the agent's side of the protocol sees it.
//
// It is the first thing written into the inbox, which ADR-032 defined as
// host-written and agent-read and left without a writer until there was
// something to say. What it says is what a gate decided about a review request
// the agent made.
//
// It is a line-oriented document rather than JSON, and that is deliberate: the
// only thing that reads it is the generated helper, which is a POSIX shell
// script, and ADR-032's reason for keeping parsing out of generated scripts
// applies to reading exactly as it does to writing. The first line carries the
// format name, the schema version, and the status, so a reader can tell all
// three with one `read` and refuse a document from a version it does not know.
const (
	// verificationFormat names the document, so that a file that is not one is
	// recognised rather than half-read.
	verificationFormat = "feat-verification"
	// VerificationSchemaVersion is the version this build writes and the
	// generated helper understands.
	VerificationSchemaVersion = 1
	// verificationPrefix is the inbox file-name prefix.
	verificationPrefix = "verification-"
	// verificationSuffix is the inbox file-name extension. It is deliberately
	// not the outbox's, so that nothing ever reads one directory's documents as
	// the other's.
	verificationSuffix = ".txt"
)

// Verification statuses.
const (
	// VerificationAccepted reports that the daemon has the request and is
	// running the checks. It is written before they start, so that a helper
	// waiting for an answer can tell "nobody is listening" from "this is going
	// to take a while".
	VerificationAccepted = "accepted"
	// VerificationPassed reports that every check that ran passed.
	VerificationPassed = "passed"
	// VerificationFailed reports that a check failed or did not report.
	VerificationFailed = "failed"
	// VerificationSkipped reports that there was nothing to run.
	VerificationSkipped = "skipped"
)

// Verification is a gate's answer to one review request.
type Verification struct {
	// Status is one of the statuses above.
	Status string
	// Report is what to tell the agent: which checks failed and what they
	// printed, or a line saying the work is with the user now.
	//
	// It is the agent's own environment's output being handed back to the agent
	// that produced the work, which is why there is nothing to redact here that
	// was not already in front of it.
	Report string
}

// VerificationName is the inbox file carrying the verdict of one request.
//
// It is named after the request rather than being a single well-known file,
// because a task asks for review more than once and an answer to a previous
// request must never be read as the answer to this one.
func VerificationName(request string) (string, error) {
	if !safeName(request) {
		return "", fmt.Errorf("%q is not a plain message identifier", request)
	}
	if len(request) > maxIDBytes {
		return "", fmt.Errorf("the message identifier is %d bytes, and the limit is %d", len(request), maxIDBytes)
	}
	return verificationPrefix + request + verificationSuffix, nil
}

// WriteVerification records a gate's answer where the waiting agent will find
// it.
//
// The write is atomic, as every write in this package is: the helper polls for
// this file, and a partial document would be read as a verdict.
func (w *Workspace) WriteVerification(request string, verification Verification) error {
	name, err := VerificationName(request)
	if err != nil {
		return fmt.Errorf("recording a verification result: %w", err)
	}
	switch verification.Status {
	case VerificationAccepted, VerificationPassed, VerificationFailed, VerificationSkipped:
	default:
		return fmt.Errorf("recording a verification result: %q is not a documented status", verification.Status)
	}

	var document strings.Builder
	fmt.Fprintf(&document, "%s %d %s\n", verificationFormat, VerificationSchemaVersion, verification.Status)
	if report := strings.TrimRight(verification.Report, "\n"); report != "" {
		document.WriteString(report)
		document.WriteString("\n")
	}
	return replaceFile(filepath.Join(w.InboxDir(), name), []byte(document.String()), filePerm)
}
