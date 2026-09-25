package daemon

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ma8el/feat/internal/paths"
)

// stateDirPerm and logFilePerm keep the log as private as the state it describes:
// it names projects, tasks, and paths.
const (
	stateDirPerm os.FileMode = 0o700
	logFilePerm  os.FileMode = 0o600
)

// The daemon log's size bound.
//
// The daemon appends for as long as it runs, so without a bound the machine's
// uptime decides how large the log gets. The values are fixed rather than
// configured, because v0 has no daemon-level configuration file and inventing one
// for a single number would settle a permanent shape early.
const (
	// maxLogSize is how large the log may grow before it is rotated, and the
	// most that is carried over from a log that is already larger.
	maxLogSize int64 = 10 << 20
	// logGenerations is how many rotated files are kept beside the current one.
	// The log therefore occupies at most (logGenerations+1) * maxLogSize.
	logGenerations = 2
)

// Log is an open daemon log.
type Log struct {
	// Logger writes to the log file, and to standard error when asked.
	Logger *slog.Logger
	// Path is the log file.
	Path string

	file *os.File
}

// Close closes the log file.
func (l *Log) Close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// OpenLog opens the daemon log for appending.
//
// The daemon runs in the background, so its log is the only account of what it
// did. The format is structured JSON, because a person reads these records
// looking for one task's history among several concurrent ones.
//
// The log is rotated once it reaches maxLogSize, and a log already past the bound
// when it is opened is cut down to its most recent records by the first write.
// Nothing else prunes it.
func OpenLog(layout paths.Layout, level slog.Level, alsoStderr bool) (*Log, error) {
	file, err := openLogFile(layout)
	if err != nil {
		return nil, err
	}

	rotating, err := newRotatingFile(file, maxLogSize, logGenerations)
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	var out io.Writer = rotating
	if alsoStderr {
		// The foreground daemon is normally run by a person or a service
		// manager, and both expect to see something on standard error.
		out = io.MultiWriter(rotating, os.Stderr)
	}

	return &Log{
		Logger: slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})),
		Path:   file.Name(),
		file:   file,
	}, nil
}

// openLogFile opens the daemon log for appending, creating its directory. It is
// also where a spawned daemon's own output is sent, so that a process that dies
// before its logger exists still leaves an explanation behind.
func openLogFile(layout paths.Layout) (*os.File, error) {
	path := layout.LogFile()
	if err := os.MkdirAll(filepath.Dir(path), stateDirPerm); err != nil {
		return nil, fmt.Errorf("creating the log directory %s: %w", filepath.Dir(path), err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFilePerm)
	if err != nil {
		return nil, fmt.Errorf("opening the daemon log %s: %w", path, err)
	}
	return file, nil
}

// rotatingFile is an append-only file that is kept under a size bound.
//
// It rotates by copying and truncating rather than by renaming, because this
// process does not hold the only descriptor: `feat daemon start` opens the log
// and hands it to the process it spawns as standard output and standard error
// (see Spawn), and that descriptor refers to the inode.
//
// Renaming would send the spawned daemon's own output to the rotated file while
// its logger wrote to a new one, so a panic after the first rotation would land
// where nobody would look. Truncating in place keeps every descriptor on one
// inode, and an O_APPEND write resumes from the beginning of it.
type rotatingFile struct {
	// mu serialises writing and rotation: a slog handler may be called from any
	// goroutine, and a rotation must not run in the middle of a record.
	mu sync.Mutex
	// file is the open log, in append mode.
	file *os.File
	// path is the log's own path. Rotated generations are numbered beside it.
	path string
	// size is what this writer believes the file holds, seeded from disk and then
	// counted rather than asked of the filesystem per record. A daemon's own
	// standard error reaches the same inode without passing through here, so this
	// can undercount and rotate slightly late.
	size int64
	// limit is the size at which the file is rotated, and the most that is kept
	// from a file that is already larger.
	limit int64
	// keep is how many rotated generations survive a rotation.
	keep int
}

// newRotatingFile wraps an open log file in its size bound.
func newRotatingFile(file *os.File, limit int64, keep int) (*rotatingFile, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("measuring the daemon log %s: %w", file.Name(), err)
	}
	return &rotatingFile{
		file:  file,
		path:  file.Name(),
		size:  info.Size(),
		limit: limit,
		keep:  keep,
	}, nil
}

// Write appends one record, rotating first when it would not fit.
func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Rotating before the record rather than after it keeps a record whole: a
	// JSON line split across two files parses as neither. The cost is that the
	// file may exceed the bound by less than one record, never more.
	if r.size > 0 && r.size+int64(len(p)) > r.limit {
		if err := r.rotate(); err != nil {
			// A failed rotation must not also cost the record that triggered it, so
			// it is reported here and appending continues. Treating the file as empty
			// is the backoff: the next attempt comes after another maxLogSize bytes
			// rather than on the next record, which on a full disk would be its own
			// runaway.
			r.reportRotationFailure(err)
			r.size = 0
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate moves the current log into the first generation and empties it.
//
// At most maxLogSize bytes are carried over, taken from the end of the file. A
// file larger than that was written before the bound existed, and keeping its
// tail reclaims the disk at once while preserving the most recent records.
func (r *rotatingFile) rotate() error {
	// Drop the oldest generation and shift the rest along. Renaming these is
	// safe in the way renaming the live log is not: no descriptor is open on
	// them.
	oldest := generationPath(r.path, r.keep)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the oldest rotated log %s: %w", oldest, err)
	}
	for generation := r.keep - 1; generation >= 1; generation-- {
		from, to := generationPath(r.path, generation), generationPath(r.path, generation+1)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rotating the daemon log %s to %s: %w", from, to, err)
		}
	}

	if r.keep > 0 {
		if err := copyTail(r.path, generationPath(r.path, 1), r.limit); err != nil {
			return err
		}
	}

	if err := r.file.Truncate(0); err != nil {
		return fmt.Errorf("truncating the daemon log %s: %w", r.path, err)
	}
	r.size = 0
	return nil
}

// reportRotationFailure records a rotation that did not happen, in the log whose
// growth it was meant to bound. It writes directly rather than through the
// logger, whose writer is this one and would deadlock on the held lock. The shape
// matches the JSON handler's, so the file stays one record per line.
func (r *rotatingFile) reportRotationFailure(cause error) {
	_, _ = fmt.Fprintf(r.file,
		"{\"time\":%q,\"level\":\"ERROR\",\"msg\":\"rotating the daemon log\",\"path\":%q,\"error\":%q}\n",
		time.Now().Format(time.RFC3339Nano), r.path, cause.Error())
}

// generationPath names one rotated generation. Generation 1 is the most recent.
func generationPath(path string, generation int) string {
	return path + "." + strconv.Itoa(generation)
}

// copyTail writes the last limit bytes of src to dst. The copy starts at a record
// boundary, because an offset counted back from the end of the file almost
// certainly lands inside a record and half a JSON object parses as nothing.
func copyTail(src, dst string, limit int64) error {
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("reading the daemon log %s: %w", src, err)
	}
	defer func() { _ = source.Close() }()

	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("measuring the daemon log %s: %w", src, err)
	}

	var content io.Reader = source
	if info.Size() > limit {
		if _, err := source.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return fmt.Errorf("seeking in the daemon log %s: %w", src, err)
		}
		buffered := bufio.NewReader(source)
		if _, err := buffered.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading the daemon log %s: %w", src, err)
		}
		content = buffered
	}

	target, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, logFilePerm)
	if err != nil {
		return fmt.Errorf("opening the rotated log %s: %w", dst, err)
	}
	_, copyErr := io.Copy(target, content)
	closeErr := target.Close()
	if copyErr != nil {
		return fmt.Errorf("writing the rotated log %s: %w", dst, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing the rotated log %s: %w", dst, closeErr)
	}
	return nil
}

// LogTail returns the last lines of the daemon log, for an error message that
// has to explain why a daemon did not start.
func LogTail(path string, lines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	// Reading the whole file is acceptable here: this runs once, on a failure
	// path, against a file the size bound above keeps to maxLogSize, and the
	// alternative is seeking logic that is easy to get wrong.
	var tail []byte
	kept := 0
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' && i != len(data)-1 {
			kept++
			if kept == lines {
				tail = data[i+1:]
				break
			}
		}
	}
	if tail == nil {
		tail = data
	}
	return string(tail)
}
