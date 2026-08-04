package daemon

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/paths"
)

// testBuild is the build identity the tests publish, so that a health response
// and an endpoint record are comparable against fixed values.
var testBuild = Build{Version: "v0.0.0-test", Commit: "0123456", GoVersion: "go1.26.0", Platform: "test/arch"}

// testLayout returns a layout under temporary directories.
func testLayout(t *testing.T) paths.Layout {
	t.Helper()

	root := t.TempDir()
	runtime := shortDir(t)
	return paths.Layout{
		Config:  filepath.Join(root, "config"),
		State:   filepath.Join(root, "state"),
		Runtime: runtime,
		Socket:  filepath.Join(runtime, "feat.sock"),
	}
}

// shortDir returns a temporary directory with a short path.
//
// t.TempDir() embeds the test's name, which on macOS pushes a socket path past
// the length the platform allows for one.
func shortDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "feat")
	if err != nil {
		t.Fatalf("creating a temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// recordingLogger returns a logger and a function that returns everything logged
// so far, so a test can check that a recovery was reported rather than performed
// silently.
func recordingLogger() (*slog.Logger, func() string) {
	var written strings.Builder
	logger := slog.New(slog.NewTextHandler(&written, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, written.String
}

// TestAcquireClaimsTheRuntimeDirectory covers the slice 2 acceptance criterion
// that socket permissions restrict access to the current user, and checks what a
// claim consists of.
func TestAcquireClaimsTheRuntimeDirectory(t *testing.T) {
	layout := testLayout(t)
	started := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)

	ownership, err := Acquire(layout, testBuild, started, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(func() { _ = ownership.Release() })

	if got := ownership.Listener().Addr().Network(); got != "unix" {
		t.Errorf("listener network = %q, want unix: no TCP listener may be opened", got)
	}

	endpoint := ownership.Endpoint()
	if endpoint.PID != os.Getpid() {
		t.Errorf("endpoint pid = %d, want %d", endpoint.PID, os.Getpid())
	}
	if endpoint.Socket != layout.Socket {
		t.Errorf("endpoint socket = %q, want %q", endpoint.Socket, layout.Socket)
	}
	if endpoint.Version != testBuild.Version || endpoint.Commit != testBuild.Commit {
		t.Errorf("endpoint build = %q/%q, want %q/%q",
			endpoint.Version, endpoint.Commit, testBuild.Version, testBuild.Commit)
	}
	if !endpoint.StartedAt.Equal(started) {
		t.Errorf("endpoint started_at = %s, want %s", endpoint.StartedAt, started)
	}

	// The socket, the lock, and the record all describe one user's daemon.
	for _, test := range []struct {
		path string
		want os.FileMode
	}{
		{layout.Socket, 0o600},
		{layout.LockFile(), 0o600},
		{layout.EndpointFile(), 0o600},
		{layout.Runtime, 0o700},
	} {
		info, err := os.Lstat(test.path)
		if err != nil {
			t.Fatalf("stat %s: %v", test.path, err)
		}
		if got := info.Mode().Perm(); got != test.want {
			t.Errorf("%s mode = %v, want %v", test.path, got, test.want)
		}
	}

	// The published record must be what another process reads back.
	published, err := ReadEndpoint(layout)
	if err != nil {
		t.Fatalf("ReadEndpoint: %v", err)
	}
	if published != endpoint {
		t.Errorf("published record = %+v, want %+v", published, endpoint)
	}
}

// TestReleaseRemovesTheClaim checks that a released daemon leaves nothing that
// would make the next one think it is still running.
func TestReleaseRemovesTheClaim(t *testing.T) {
	layout := testLayout(t)

	ownership, err := Acquire(layout, testBuild, time.Now(), nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := ownership.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	for _, path := range []string{layout.Socket, layout.EndpointFile()} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists after Release; err = %v", path, err)
		}
	}
	// The lock file itself stays: it is the name the next daemon locks, and two
	// daemons racing to create it would be worse than an empty file.
	if _, err := os.Lstat(layout.LockFile()); err != nil {
		t.Errorf("the lock file was removed: %v", err)
	}

	// Releasing twice is what a failed acquisition does, so it must be safe.
	if err := ownership.Release(); err != nil {
		t.Errorf("second Release: %v", err)
	}

	// The directory is claimable again.
	second, err := Acquire(layout, testBuild, time.Now(), nil)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	_ = second.Release()
}

func TestAcquireRefusesASecondDaemon(t *testing.T) {
	layout := testLayout(t)

	first, err := Acquire(layout, testBuild, time.Now(), nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	_, err = Acquire(layout, testBuild, time.Now(), nil)

	var running *AlreadyRunningError
	if !errors.As(err, &running) {
		t.Fatalf("error = %v, want an *AlreadyRunningError", err)
	}
	if !running.HasEndpoint {
		t.Error("the error does not name the running daemon, though a record was published")
	}
	if running.Endpoint.PID != os.Getpid() {
		t.Errorf("reported pid = %d, want %d", running.Endpoint.PID, os.Getpid())
	}
	if !strings.Contains(running.Error(), layout.Socket) {
		t.Errorf("the message does not name the socket: %v", running)
	}
}

// TestAcquireReclaimsAStaleSocket is the slice 2 acceptance criterion that a
// stale socket is diagnosed and safely recovered.
//
// The socket file is left behind deliberately, which is what a killed daemon
// leaves: the lock is free because the kernel released it, and nothing answers.
func TestAcquireReclaimsAStaleSocket(t *testing.T) {
	layout := testLayout(t)

	abandonSocket(t, layout.Socket)
	if _, err := os.Lstat(layout.Socket); err != nil {
		t.Fatalf("the stale socket was not left behind: %v", err)
	}

	logger, logged := recordingLogger()

	ownership, err := Acquire(layout, testBuild, time.Now(), logger)
	if err != nil {
		t.Fatalf("Acquire over a stale socket: %v", err)
	}
	t.Cleanup(func() { _ = ownership.Release() })

	if !Answering(layout.Socket) {
		t.Error("the reclaimed socket does not answer")
	}
	// Recovery is reported, not silent: the next person debugging this needs to
	// know a socket was removed.
	if !strings.Contains(logged(), "reclaiming a stale socket") {
		t.Errorf("the reclaim was not logged:\n%s", logged())
	}
}

// TestAcquireRefusesALiveForeignSocket is the other half of stale-socket
// recovery, and the half that protects a running daemon: something is serving
// the path without holding the lock, so the path must not be taken.
func TestAcquireRefusesALiveForeignSocket(t *testing.T) {
	layout := testLayout(t)

	if err := os.MkdirAll(layout.Runtime, 0o700); err != nil {
		t.Fatalf("preparing the runtime directory: %v", err)
	}
	listener, err := net.Listen("unix", layout.Socket)
	if err != nil {
		t.Fatalf("listening on %s: %v", layout.Socket, err)
	}
	defer func() { _ = listener.Close() }()

	_, err = Acquire(layout, testBuild, time.Now(), nil)

	var foreign *ForeignSocketError
	if !errors.As(err, &foreign) {
		t.Fatalf("error = %v, want a *ForeignSocketError", err)
	}
	if !Answering(layout.Socket) {
		t.Error("the live socket was removed; its clients would have been disconnected")
	}
}

func TestAcquireRefusesSomethingElseAtTheSocketPath(t *testing.T) {
	layout := testLayout(t)

	if err := os.MkdirAll(layout.Runtime, 0o700); err != nil {
		t.Fatalf("preparing the runtime directory: %v", err)
	}
	if err := os.WriteFile(layout.Socket, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("writing the file in the way: %v", err)
	}

	_, err := Acquire(layout, testBuild, time.Now(), nil)
	if err == nil {
		t.Fatal("a regular file at the socket path was accepted")
	}
	if !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("the error does not explain what is in the way: %v", err)
	}
	// A file Feat did not create is not Feat's to remove.
	if _, statErr := os.Lstat(layout.Socket); statErr != nil {
		t.Errorf("the file was removed: %v", statErr)
	}
}

func TestAcquireRejectsASymlinkedRuntimeDirectory(t *testing.T) {
	layout := testLayout(t)

	target := shortDir(t)
	link := filepath.Join(shortDir(t), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}
	layout.Runtime = link
	layout.Socket = filepath.Join(link, "feat.sock")

	_, err := Acquire(layout, testBuild, time.Now(), nil)

	var unsafe *UnsafeDirectoryError
	if !errors.As(err, &unsafe) {
		t.Fatalf("error = %v, want an *UnsafeDirectoryError", err)
	}
	if !strings.Contains(unsafe.Error(), paths.EnvRuntimeOverride) {
		t.Errorf("the error does not say how to move the directory: %v", unsafe)
	}
}

// TestAcquireRestrictsAnOpenRuntimeDirectory covers the case Feat can fix: the
// directory is the user's own but is readable by others, which an older build or
// a permissive umask can produce.
func TestAcquireRestrictsAnOpenRuntimeDirectory(t *testing.T) {
	layout := testLayout(t)

	if err := os.MkdirAll(layout.Runtime, 0o700); err != nil {
		t.Fatalf("preparing the runtime directory: %v", err)
	}
	if err := os.Chmod(layout.Runtime, 0o755); err != nil {
		t.Fatalf("loosening the runtime directory: %v", err)
	}

	ownership, err := Acquire(layout, testBuild, time.Now(), nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(func() { _ = ownership.Release() })

	info, err := os.Lstat(layout.Runtime)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("runtime directory mode = %v, want 0700", got)
	}
}

// abandonSocket leaves a socket file behind with nothing listening on it, which
// is what a daemon that was killed leaves.
func abandonSocket(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("preparing the runtime directory: %v", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on %s: %v", path, err)
	}
	unix, ok := listener.(*net.UnixListener)
	if !ok {
		t.Fatalf("listener is a %T, want a *net.UnixListener", listener)
	}
	// Closing without unlinking is exactly what a process that dies does.
	unix.SetUnlinkOnClose(false)
	if err := unix.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}
}
