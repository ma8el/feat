//go:build darwin || linux

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// This file holds everything about daemon ownership that is specific to a Unix
// platform: the advisory lock, file ownership, and process signalling. Feat
// targets macOS and Linux (docs/08-v0-scope.md), so there is no fallback
// implementation. A port to another platform adds a file beside this one rather
// than weakening the lock, because a lock the kernel does not release on process
// death cannot answer whether a recorded daemon is still alive.

// fileLock is an exclusive advisory lock on a file.
//
// The kernel releases it when the holding process dies, including on SIGKILL and
// including a crash, which is what makes it a trustworthy answer to "is the
// recorded daemon still running". A process identifier alone cannot answer that,
// because identifiers are reused.
type fileLock struct {
	file *os.File
}

// acquireLock takes the lock without blocking. It returns errLockHeld when
// another process holds it.
func acquireLock(path string, perm os.FileMode) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, perm)
	if err != nil {
		return nil, fmt.Errorf("opening the ownership lock %s: %w", path, err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLockHeld
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return &fileLock{file: file}, nil
}

// release unlocks and closes the file. The lock file itself is left behind on
// purpose: it is the stable name the next daemon locks, and an empty file is
// cheaper to keep than a name two starting daemons could race to create.
func (l *fileLock) release() error {
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// fileOwner returns the user identifier that owns a file.
func fileOwner(info os.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}

// signalStop asks a process to shut down gracefully.
func signalStop(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signalling pid %d: %w", pid, err)
	}
	return nil
}

// detach puts a spawned daemon in its own session, so that it survives the
// client that started it and never receives the terminal's signals: closing the
// window that ran `feat` must not stop the agents the daemon is supervising.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// processExists reports whether a process identifier is live. Signal 0 performs
// the permission and existence checks without delivering anything.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	// EPERM means the process exists and belongs to somebody else, which is
	// still an existing process.
	return err == nil || errors.Is(err, syscall.EPERM)
}
