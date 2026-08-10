package control

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

// Names inside one task's control workspace.
//
// The tree is split by who writes to it. task.md, context/, and inbox/ are
// written by the host and read by the agent; outbox/ and reports/ are written
// by the agent and read by the host; agent/ is host-only and holds what the
// provider adapter generated plus the record of which events have been applied.
//
// The split is what lets an execution environment mount the parts with
// different modes, and it is why deduplication never needs the host to write
// into the directory the agent owns (ADR-032).
const (
	briefName     = "task.md"
	contextDir    = "context"
	inboxDir      = "inbox"
	outboxDir     = "outbox"
	reportsDir    = "reports"
	agentDir      = "agent"
	processedName = "processed.jsonl"
)

// File modes.
//
// A control workspace holds a task brief and the agent's reports, which belong
// to one user in exactly the way the state directory does. Where an execution
// environment runs the agent as another user, granting that user access is the
// execution adapter's job and is bounded by the mount it creates: it is not a
// reason to widen these here (ADR-032).
const (
	dirPerm    fs.FileMode = 0o700
	filePerm   fs.FileMode = 0o600
	scriptPerm fs.FileMode = 0o700
)

// Options configure a workspace.
type Options struct {
	// Now supplies the current time. A nil value uses the wall clock.
	Now func() time.Time
	// ParseGrace is how long a document that does not parse is given before it
	// is recorded as malformed. Zero uses the default.
	//
	// It exists because a write in progress and a malformed document look
	// identical to a reader, and only one of them is the agent's mistake.
	ParseGrace time.Duration
}

// Workspace is one task's control workspace on the host.
//
// It owns the layout, the atomic writes, the validation of what it reads, and
// the record of what it has already applied. It does not decide what a message
// means: that is the provider adapter's job for a provider event and the
// daemon's for everything else.
type Workspace struct {
	root string
	task domain.TaskID
	now  func() time.Time
	// parseGrace bounds how long an unparseable document is treated as a write
	// in progress.
	parseGrace time.Duration

	// mu guards the fields below. One workspace is polled by the daemon and
	// written by its own launch path, which may overlap.
	mu sync.Mutex
	// processed is the set of applied event identifiers, loaded once and kept
	// in step with the record on disk.
	processed map[string]bool
	loaded    bool
	// firstSeen records when an unparseable outbox entry was first noticed, so
	// that the grace above can be measured against something.
	firstSeen map[string]time.Time
}

// defaultParseGrace is how long an unparseable document is given before it is
// recorded as malformed. It is generous relative to the time it takes to write
// a few kilobytes and short relative to a person noticing a stuck task.
const defaultParseGrace = 3 * time.Second

// Open returns the control workspace of one task under the control root.
//
// It creates nothing. Resolving where a workspace belongs and creating it are
// separate acts, and only a task that has been confirmed gets the second one.
func Open(root string, project domain.ProjectID, task domain.TaskID, opts Options) (*Workspace, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("the control root %q must be an absolute path", root)
	}
	// Both identifiers are validated before either reaches a path, so no
	// caller-supplied text can escape the directory it names.
	if err := project.Validate(); err != nil {
		return nil, err
	}
	if err := task.Validate(); err != nil {
		return nil, err
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	grace := opts.ParseGrace
	if grace <= 0 {
		grace = defaultParseGrace
	}
	return &Workspace{
		root:       filepath.Join(root, project.String(), task.String()),
		task:       task,
		now:        now,
		parseGrace: grace,
		processed:  make(map[string]bool),
		firstSeen:  make(map[string]time.Time),
	}, nil
}

// Task returns the task the workspace belongs to.
func (w *Workspace) Task() domain.TaskID { return w.task }

// Root returns the workspace directory.
func (w *Workspace) Root() string { return w.root }

// BriefPath returns the path of the task brief.
func (w *Workspace) BriefPath() string { return filepath.Join(w.root, briefName) }

// ContextDir returns the host-written context directory.
func (w *Workspace) ContextDir() string { return filepath.Join(w.root, contextDir) }

// InboxDir returns the host-written message directory.
func (w *Workspace) InboxDir() string { return filepath.Join(w.root, inboxDir) }

// OutboxDir returns the agent-written message directory.
func (w *Workspace) OutboxDir() string { return filepath.Join(w.root, outboxDir) }

// ReportsDir returns the agent-written report directory.
func (w *Workspace) ReportsDir() string { return filepath.Join(w.root, reportsDir) }

// AgentDir returns the host-only directory holding generated provider files.
func (w *Workspace) AgentDir() string { return filepath.Join(w.root, agentDir) }

// Create makes the workspace tree.
//
// It is idempotent, so a task whose launch failed part way through can be
// retried without first being cleaned up. A directory that already exists as
// something other than a directory is refused rather than written through,
// which is the earliest point at which a tampered workspace can be caught.
func (w *Workspace) Create() error {
	for _, dir := range []string{
		w.root, w.ContextDir(), w.InboxDir(), w.OutboxDir(), w.ReportsDir(), w.AgentDir(),
	} {
		if err := w.prepareDirectory(dir); err != nil {
			return err
		}
	}
	return nil
}

// Exists reports whether the workspace directory is present.
func (w *Workspace) Exists() bool {
	info, err := os.Stat(w.root)
	return err == nil && info.IsDir()
}

// Remove deletes the workspace tree, and reports whether there was one.
//
// This is the audit trail ADR-032 kept intact until cleanup, so removing it is
// only ever reached from a cleanup the user confirmed. The path is the one this
// workspace computed from a validated project and task identifier under a root
// the daemon resolved, never one a caller supplied — and it is checked again
// here, because this function deletes a directory tree and the cost of the check
// is nothing next to the cost of being wrong.
func (w *Workspace) Remove() (bool, error) {
	if !filepath.IsAbs(w.root) || paths.Broad(w.root) {
		return false, fmt.Errorf("refusing to remove the control workspace %q: it is not a directory Feat owns", w.root)
	}
	// The workspace lives two levels below the control root, at
	// <root>/<project>/<task>, so anything shallower is not one whatever else it
	// may be.
	if paths.Depth(w.root) < 4 {
		return false, fmt.Errorf("refusing to remove the control workspace %q: it is not deep enough to be one", w.root)
	}
	if !w.Exists() {
		return false, nil
	}
	if err := os.RemoveAll(w.root); err != nil {
		return false, fmt.Errorf("removing the control workspace %s: %w", w.root, err)
	}
	return true, nil
}

// WriteBrief records the confirmed task brief.
//
// The brief is what the agent receives, so it is written verbatim and by atomic
// replacement: an agent that reads it while it is being written must see the
// previous document or the new one, never half of each.
func (w *Workspace) WriteBrief(brief string) error {
	return w.replaceFile(w.BriefPath(), []byte(brief), filePerm)
}

// WriteAgentFile records one generated provider file in the host-only area.
//
// The name is a relative path inside agent/, so that an adapter can group its
// hooks and helpers, and it is checked rather than trusted: this package builds
// the path, and a name that could leave the directory is refused.
func (w *Workspace) WriteAgentFile(name string, data []byte, executable bool) error {
	path, err := w.AgentPath(name)
	if err != nil {
		return err
	}
	mode := filePerm
	if executable {
		mode = scriptPerm
	}
	return w.replaceFile(path, data, mode)
}

// AgentPath resolves one path inside the host-only agent directory.
func (w *Workspace) AgentPath(name string) (string, error) {
	if err := checkRelative(name); err != nil {
		return "", fmt.Errorf("resolving a generated agent file: %w", err)
	}
	return filepath.Join(w.AgentDir(), filepath.FromSlash(name)), nil
}

// processedPath returns the host-only record of applied event identifiers.
func (w *Workspace) processedPath() string { return filepath.Join(w.AgentDir(), processedName) }

// errNotRegular is what every file operation in this package refuses over.
//
// A path built from validated identifiers says where a file belongs. It says
// nothing about what is there, and the tree is reachable from a container: the
// two facts together are what turned the daemon's own bookkeeping write into a
// write somewhere else entirely.
var errNotRegular = errors.New("not a regular file")

// openLeaf opens one file of the control workspace, refusing anything that is
// not a regular file.
//
// Both extra flags earn their place. O_NOFOLLOW means the descriptor is the
// file the path names rather than whatever a symbolic link put in its way, so a
// link planted in the tree cannot redirect a host write to a file elsewhere on
// the machine. O_NONBLOCK means a named pipe answers immediately instead of
// blocking in the kernel until somebody writes to it: one goroutine polls every
// task's workspace, so an open that never returned would stop control
// processing for every task at once.
//
// The kind is then read from the descriptor rather than from the path, so what
// was checked is what is used.
func openLeaf(path string, flag int, perm fs.FileMode) (*os.File, error) {
	file, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%s is a symbolic link and so %w", path, errNotRegular)
	}
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("%s is %s and so %w", path, describeKind(info.Mode()), errNotRegular)
	}
	return file, nil
}

// describeKind names what was found where a regular file was expected, so that
// a refusal says what is there rather than only what is not.
func describeKind(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeDevice != 0:
		return "a device"
	default:
		return "not a file"
	}
}

// checkDirectory refuses a directory of the workspace that is not one.
func (w *Workspace) checkDirectory(dir string) error { return w.walkDirectory(dir, false) }

// prepareDirectory is checkDirectory, creating the components that are absent.
func (w *Workspace) prepareDirectory(dir string) error { return w.walkDirectory(dir, true) }

// walkDirectory checks every component of a workspace directory from the root
// down, and creates the missing ones when asked to.
//
// O_NOFOLLOW protects the last component of a path and nothing above it, so a
// directory replaced by a symbolic link would still send an atomic replacement
// somewhere else. Each component is therefore stat'ed without following links,
// and anything that is not a directory is refused by name. The layout of a
// control workspace is Feat's own: there is no case in which one of these is
// legitimately a link.
func (w *Workspace) walkDirectory(dir string, create bool) error {
	relative, err := filepath.Rel(w.root, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to use %s for task %s: it is not inside the task's control workspace %s",
			dir, w.task, w.root)
	}

	if create {
		// The root's own parents are the control root and the project
		// directory, which belong to Feat rather than to this workspace.
		if err := os.MkdirAll(w.root, dirPerm); err != nil {
			return fmt.Errorf("creating the control workspace %s of task %s: %w", w.root, w.task, err)
		}
	}

	chain := []string{w.root}
	current := w.root
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		if component == "." {
			continue
		}
		current = filepath.Join(current, component)
		chain = append(chain, current)
	}

	for _, step := range chain {
		info, err := os.Lstat(step)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if !create {
				// Nothing is there to be wrong, and the operation that follows
				// reports the absence better than this can.
				return nil
			}
			if err := os.Mkdir(step, dirPerm); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("creating %s in the control workspace of task %s: %w", step, w.task, err)
			}
		case err != nil:
			return fmt.Errorf("reading %s in the control workspace of task %s: %w", step, w.task, err)
		case !info.IsDir():
			return fmt.Errorf("refusing to use %s in the control workspace of task %s: it is %s, "+
				"and the layout of a control workspace is Feat's own",
				step, w.task, describeKind(info.Mode()))
		}
	}
	return nil
}

// replaceFile writes data to path by writing a temporary file in the same
// directory, flushing it, and renaming it over the target.
//
// The rename is what makes the write atomic: a reader sees the previous file or
// the new one, never a mixture, whatever moment the process dies at. The
// temporary name is dot-prefixed so that a crash before the rename leaves
// something every reader in this package already ignores.
//
// Neither step follows a symbolic link. The directory is checked component by
// component first; the staging file is created exclusively, which fails on a
// name a link already occupies rather than writing through it; and a rename
// replaces a link rather than the file it points at.
func (w *Workspace) replaceFile(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := w.prepareDirectory(dir); err != nil {
		return err
	}

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	if err := temp.Chmod(mode); err != nil {
		return discard(temp, fmt.Errorf("setting permissions on %s: %w", temp.Name(), err))
	}
	if _, err := temp.Write(data); err != nil {
		return discard(temp, fmt.Errorf("writing %s: %w", temp.Name(), err))
	}
	// Flush before the rename, so that a machine that loses power after the
	// directory entry reaches the disk does not leave an empty file behind.
	if err := temp.Sync(); err != nil {
		return discard(temp, fmt.Errorf("flushing %s: %w", temp.Name(), err))
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(temp.Name())
		return fmt.Errorf("closing %s: %w", temp.Name(), err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		_ = os.Remove(temp.Name())
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// discard removes a temporary file after a write failure and returns the
// failure that caused it.
func discard(temp *os.File, err error) error {
	_ = temp.Close()
	_ = os.Remove(temp.Name())
	return err
}
