package daemon

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ma8el/feat/internal/paths"
)

// stateDirPerm and logFilePerm keep the log as private as the state it describes:
// it names projects, tasks, and paths.
const (
	stateDirPerm os.FileMode = 0o700
	logFilePerm  os.FileMode = 0o600
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
// did. Structured JSON is the format because these records are read by a person
// looking for one task's history among several concurrent ones.
func OpenLog(layout paths.Layout, level slog.Level, alsoStderr bool) (*Log, error) {
	file, err := openLogFile(layout)
	if err != nil {
		return nil, err
	}

	var out io.Writer = file
	if alsoStderr {
		// The foreground daemon is normally run by a person or a service
		// manager, and both expect to see something on standard error.
		out = io.MultiWriter(file, os.Stderr)
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

// LogTail returns the last lines of the daemon log, for an error message that
// has to explain why a daemon did not start.
func LogTail(path string, lines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	// Reading the whole file is acceptable here: this runs once, on a failure
	// path, and the alternative is seeking logic that is easy to get wrong.
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
