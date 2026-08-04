package daemon

import (
	"errors"
	"fmt"
	iofs "io/fs"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/ma8el/feat/internal/paths"
)

// probeTimeout bounds the connect attempt that decides whether a socket is
// stale. A local daemon that is alive accepts immediately.
const probeTimeout = 250 * time.Millisecond

// Ownership is one daemon's exclusive claim on the runtime directory: the
// advisory lock, the listening socket, and the published endpoint record.
type Ownership struct {
	layout   paths.Layout
	lock     *fileLock
	listener net.Listener
	endpoint Endpoint
	logger   *slog.Logger
}

// Acquire claims the runtime directory and starts listening.
//
// The sequence answers the three questions that a PID file alone cannot:
//
//  1. the advisory lock says whether a daemon is alive, because the kernel
//     releases it even when the process is killed;
//  2. with the lock held, a socket that refuses connections is stale, and the
//     path can be reclaimed;
//  3. a socket that answers while the lock is free belongs to something else,
//     and Feat refuses rather than disconnecting its clients.
//
// Everything from the probe to the published record happens while holding the
// lock, so two daemons starting at the same time cannot both reclaim the path.
func Acquire(layout paths.Layout, build Build, now time.Time, logger *slog.Logger) (*Ownership, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if err := ensureRuntimeDir(layout.Runtime, logger); err != nil {
		return nil, err
	}

	lock, err := acquireLock(layout.LockFile(), runtimeFilePerm)
	if err != nil {
		if errors.Is(err, errLockHeld) {
			// The lock is the authority on liveness; the record only explains
			// who holds it, and a daemon that is still starting has not written
			// one yet.
			endpoint, readErr := ReadEndpoint(layout)
			return nil, &AlreadyRunningError{Endpoint: endpoint, HasEndpoint: readErr == nil}
		}
		return nil, err
	}

	ownership := &Ownership{layout: layout, lock: lock, logger: logger}
	if err := ownership.reclaimSocket(); err != nil {
		return nil, errors.Join(err, lock.release())
	}

	listener, err := net.Listen("unix", layout.Socket)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("listening on %s: %w", layout.Socket, err), lock.release())
	}
	ownership.listener = listener

	// net.Listen creates the socket with the process umask applied, which on a
	// normal desktop leaves it group- and world-readable. The API is a control
	// surface for one user (docs/05-security-model.md), so the mode is set
	// explicitly rather than inherited.
	if err := os.Chmod(layout.Socket, runtimeFilePerm); err != nil {
		return nil, errors.Join(
			fmt.Errorf("restricting %s to the current user: %w", layout.Socket, err),
			ownership.Release())
	}

	ownership.endpoint = Endpoint{
		SchemaVersion: endpointSchemaVersion,
		PID:           os.Getpid(),
		Socket:        layout.Socket,
		Version:       build.Version,
		Commit:        build.Commit,
		StartedAt:     now.UTC().Round(0),
	}
	if err := writeEndpoint(layout.EndpointFile(), ownership.endpoint); err != nil {
		return nil, errors.Join(err, ownership.Release())
	}

	return ownership, nil
}

// Listener returns the socket the daemon serves on.
func (o *Ownership) Listener() net.Listener { return o.listener }

// Endpoint returns the published record.
func (o *Ownership) Endpoint() Endpoint { return o.endpoint }

// Release gives up ownership: it stops listening, removes the socket and the
// record, and unlocks. It is safe to call twice, because a failed acquisition
// releases what it had already taken.
func (o *Ownership) Release() error {
	var failures []error

	if o.listener != nil {
		// Closing a Unix listener removes the socket file it created.
		if err := o.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			failures = append(failures, fmt.Errorf("closing the socket: %w", err))
		}
		o.listener = nil
	}
	if o.endpoint.PID != 0 {
		if err := os.Remove(o.layout.EndpointFile()); err != nil && !errors.Is(err, iofs.ErrNotExist) {
			failures = append(failures, fmt.Errorf("removing the endpoint record: %w", err))
		}
		o.endpoint = Endpoint{}
	}
	if o.lock != nil {
		if err := o.lock.release(); err != nil {
			failures = append(failures, err)
		}
		o.lock = nil
	}

	return errors.Join(failures...)
}

// reclaimSocket removes a socket left behind by a daemon that is gone.
//
// It runs with the ownership lock held, so nothing else is starting at the same
// time, and a socket that still answers therefore belongs to a process that
// never took the lock.
func (o *Ownership) reclaimSocket() error {
	socket := o.layout.Socket

	info, err := os.Lstat(socket)
	if errors.Is(err, iofs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", socket, err)
	}

	if Answering(socket) {
		return &ForeignSocketError{Socket: socket, Lock: o.layout.LockFile()}
	}

	if info.Mode()&os.ModeSocket == 0 {
		// Something that is not a socket is in the way. Feat does not remove a
		// regular file or a directory it did not create.
		return fmt.Errorf("%s exists and is not a socket (%s): remove it or set %s to another directory",
			socket, info.Mode().Type(), paths.EnvRuntimeOverride)
	}

	o.logger.Warn("reclaiming a stale socket",
		slog.String("socket", socket),
		slog.String("reason", "the ownership lock was free and nothing answered on the socket"))

	if err := os.Remove(socket); err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return fmt.Errorf("removing the stale socket %s: %w", socket, err)
	}
	return nil
}

// Answering reports whether something accepts connections on a socket path.
//
// It is the only reliable way to tell a stale socket file from a live one, and
// it is deliberately a connect and an immediate close: the daemon answers before
// any request is written, so nothing has to be sent to find out.
func Answering(socket string) bool {
	connection, err := net.DialTimeout("unix", socket, probeTimeout)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

// ensureRuntimeDir creates the runtime directory and refuses one that cannot
// hold an ownership claim.
//
// The fallback location is shared with other users (docs/06-technical-architecture.md),
// so a directory somebody else owns, or that anybody else can write to, is a
// directory where another user could place a socket or a lock. A too-permissive
// directory that Feat does own is repaired rather than rejected, because that is
// a mode this program can fix without guessing.
func ensureRuntimeDir(dir string, logger *slog.Logger) error {
	if err := os.MkdirAll(dir, runtimeDirPerm); err != nil {
		return fmt.Errorf("creating the runtime directory %s: %w", dir, err)
	}

	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspecting the runtime directory %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &UnsafeDirectoryError{Dir: dir, Reason: "it is a symbolic link, so its target could be replaced"}
	}
	if !info.IsDir() {
		return &UnsafeDirectoryError{Dir: dir, Reason: "it is not a directory"}
	}

	if owner, ok := fileOwner(info); ok && owner != os.Getuid() {
		return &UnsafeDirectoryError{
			Dir:    dir,
			Reason: fmt.Sprintf("it belongs to user %d and this process runs as user %d", owner, os.Getuid()),
		}
	}

	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		logger.Warn("restricting the runtime directory to the current user",
			slog.String("directory", dir),
			slog.String("mode", mode.String()))
		if err := os.Chmod(dir, runtimeDirPerm); err != nil {
			return &UnsafeDirectoryError{
				Dir:    dir,
				Reason: fmt.Sprintf("it allows access to other users (%s) and could not be restricted: %v", mode, err),
			}
		}
	}
	return nil
}
