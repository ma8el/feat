package fs

import (
	"fmt"
	"os"
	"path/filepath"
)

// Points inside an atomic replacement where a test can interrupt the store.
const (
	pointTempCreated = "temp_created"
	pointTempWritten = "temp_written"
	pointTempSynced  = "temp_synced"
	pointRenamed     = "renamed"
)

// interruption is a test hook. Returning an error makes the store behave as if
// the process died at that point: the error reaches the caller and nothing is
// cleaned up, which is what a real crash leaves behind.
type interruption func(point, path string) error

// at reports an interruption when a test installed one.
func (s *Store) at(point, path string) error {
	if s.interrupt == nil {
		return nil
	}
	return s.interrupt(point, path)
}

// replaceFile writes data to path by writing a temporary file in the same
// directory, flushing it, and renaming it over the target.
//
// The rename is what makes a snapshot safe: a reader either sees the previous
// file or the new one, never a half-written mixture, whatever moment the
// process dies at. A crash before the rename leaves a temporary file behind.
// That file is inert, because every read opens an exact snapshot name and every
// listing ignores anything else; reporting and removing such leftovers belongs
// to reconciliation.
func (s *Store) replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	if err := temp.Chmod(filePerm); err != nil {
		return discard(temp, fmt.Errorf("setting permissions on %s: %w", temp.Name(), err))
	}
	if err := s.at(pointTempCreated, path); err != nil {
		_ = temp.Close()
		return err
	}

	if _, err := temp.Write(data); err != nil {
		return discard(temp, fmt.Errorf("writing %s: %w", temp.Name(), err))
	}
	if err := s.at(pointTempWritten, path); err != nil {
		_ = temp.Close()
		return err
	}

	// Flush before the rename. Without it the directory entry can reach the
	// disk before the contents, which turns a crash into an empty snapshot.
	if err := temp.Sync(); err != nil {
		return discard(temp, fmt.Errorf("flushing %s: %w", temp.Name(), err))
	}
	if err := temp.Close(); err != nil {
		return discard(temp, fmt.Errorf("closing %s: %w", temp.Name(), err))
	}
	if err := s.at(pointTempSynced, path); err != nil {
		return err
	}

	if err := os.Rename(temp.Name(), path); err != nil {
		_ = os.Remove(temp.Name())
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	if err := s.at(pointRenamed, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// appendLine appends one newline-terminated record to a log.
//
// It repairs an incomplete final record first. A crash during an earlier append
// can leave a record without its newline, and appending after it would join two
// records into one unreadable line: an interrupted write must cost the record
// it was writing, never the record after it.
func (s *Store) appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	// Read-write, because repairing an interrupted append means reading the end
	// of the log before writing to it.
	//nolint:gosec // G304: the path is built from validated identifiers under the store root
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	end, err := truncateIncompleteRecord(file)
	if err != nil {
		return fmt.Errorf("repairing %s: %w", path, err)
	}
	if _, err := file.WriteAt(line, end); err != nil {
		return fmt.Errorf("appending to %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flushing %s: %w", path, err)
	}
	return nil
}

// truncateIncompleteRecord drops a trailing record that has no newline and
// returns the offset the next record starts at.
func truncateIncompleteRecord(file *os.File) (int64, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}

	last := make([]byte, 1)
	if _, err := file.ReadAt(last, size-1); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		return size, nil
	}

	complete, err := lastNewlineOffset(file, size)
	if err != nil {
		return 0, err
	}
	if err := file.Truncate(complete); err != nil {
		return 0, err
	}
	return complete, nil
}

// lastNewlineOffset returns the offset just past the final newline in the file,
// or zero when the file holds no complete record.
func lastNewlineOffset(file *os.File, size int64) (int64, error) {
	const window = 4096

	buffer := make([]byte, window)
	for end := size; end > 0; {
		start := end - window
		if start < 0 {
			start = 0
		}
		chunk := buffer[:end-start]
		if _, err := file.ReadAt(chunk, start); err != nil {
			return 0, err
		}
		for i := len(chunk) - 1; i >= 0; i-- {
			if chunk[i] == '\n' {
				return start + int64(i) + 1, nil
			}
		}
		end = start
	}
	return 0, nil
}

// readFile reads a state file. The path is always built from a validated
// identifier under the store root.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // G304: paths are built from validated identifiers under the store root
}

// syncDir flushes a directory entry so that a completed rename survives a crash
// of the machine, not only of the process.
func syncDir(dir string) error {
	handle, err := os.Open(dir) //nolint:gosec // G304: the directory is the parent of a path built under the store root
	if err != nil {
		return fmt.Errorf("opening %s: %w", dir, err)
	}
	defer func() { _ = handle.Close() }()

	if err := handle.Sync(); err != nil {
		return fmt.Errorf("flushing %s: %w", dir, err)
	}
	return nil
}

// discard removes a temporary file after an ordinary write failure and returns
// the failure that caused it.
func discard(temp *os.File, err error) error {
	_ = temp.Close()
	_ = os.Remove(temp.Name())
	return err
}
