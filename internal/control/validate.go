package control

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// messageSuffix is the extension an outbox entry must carry.
//
// Requiring it is what lets a writer stage a document under any other name and
// rename it into place, and it means a stray file left in the directory by
// something else is ignored rather than reported as a broken message.
const messageSuffix = ".json"

// maxNameBytes bounds an outbox file name.
const maxNameBytes = 128

// checkEntry reports whether one directory entry may be read as a control
// message.
//
// The security model requires path-traversal prevention and size limits on the
// control workspace. Both are enforced here, before anything is opened: the
// name must be a plain file name directly in the directory, the entry must be a
// regular file rather than a symbolic link or a directory, and its size must be
// within the bound.
//
// A dot-prefixed name is skipped rather than refused. That is the staging name
// this package and the generated hooks both write before renaming, so a file
// carrying one is a write in progress, not a message.
func checkEntry(entry fs.DirEntry) (skip bool, err error) {
	name := entry.Name()
	if strings.HasPrefix(name, ".") {
		return true, nil
	}
	if !strings.HasSuffix(name, messageSuffix) {
		return true, nil
	}
	if len(name) > maxNameBytes {
		return false, &RejectionError{
			File:   name,
			Reason: fmt.Sprintf("has a name of %d bytes, and the limit is %d", len(name), maxNameBytes),
		}
	}
	if !safeName(name) {
		return false, &RejectionError{File: name, Reason: "does not have a plain file name"}
	}
	if entry.IsDir() {
		return false, &RejectionError{File: name, Reason: "is a directory"}
	}
	// The entry comes from a directory read, which does not follow symbolic
	// links, so a link reports itself as one here rather than as the file it
	// points at.
	info, err := entry.Info()
	if err != nil {
		return false, fmt.Errorf("reading the control message %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return false, &RejectionError{
			File:   name,
			Reason: "is not a regular file, and only regular files are read as messages",
		}
	}
	if info.Size() > MaxMessageBytes {
		return false, &RejectionError{
			File:   name,
			Reason: fmt.Sprintf("is %d bytes, and the limit is %d", info.Size(), MaxMessageBytes),
		}
	}
	return false, nil
}

// safeName reports whether a value is a plain file-name component.
//
// It rejects anything that could leave the directory it names, anything a
// filesystem or a log line would read as structure, and the two relative names
// that mean somewhere else.
func safeName(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	if strings.ContainsAny(value, `/\`) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// checkRelative reports whether a slash-separated relative path stays inside
// the directory it is resolved against.
//
// It is used for the files a provider adapter generates, which are named by the
// adapter rather than by a user, and checked anyway: this package builds the
// path, so this package is where the rule belongs.
func checkRelative(name string) error {
	if name == "" {
		return fmt.Errorf("the name must not be empty")
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("the name %q must be relative", name)
	}
	if path.Clean(name) != name {
		return fmt.Errorf("the name %q must be written cleanly, without \".\", \"..\", or repeated separators", name)
	}
	for _, component := range strings.Split(name, "/") {
		if !safeName(component) {
			return fmt.Errorf("the name %q contains a component that is not a plain name", name)
		}
	}
	return nil
}
