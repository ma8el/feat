package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Environment variables that move Feat's directories.
const (
	// EnvConfigHome is the XDG base directory for human-authored configuration.
	EnvConfigHome = "XDG_CONFIG_HOME"
	// EnvDataHome is the XDG base directory for persistent state.
	EnvDataHome = "XDG_DATA_HOME"
	// EnvRuntimeDir is the XDG per-user runtime directory. Linux sets it;
	// macOS does not.
	EnvRuntimeDir = "XDG_RUNTIME_DIR"
	// EnvTempDir is the per-user temporary directory macOS provides instead.
	EnvTempDir = "TMPDIR"
	// EnvRuntimeOverride replaces the resolved runtime directory as a whole.
	//
	// The socket, the ownership lock, and the endpoint record move together,
	// because a socket separated from the lock that proves who owns it could be
	// claimed by a second daemon (ADR-027).
	EnvRuntimeOverride = "FEAT_RUNTIME_DIR"
)

// Names inside the resolved directories.
const (
	dirName          = "feat"
	socketName       = "feat.sock"
	tmuxSocketName   = "tmux.sock"
	lockName         = "daemon.lock"
	endpointName     = "endpoint.json"
	logsDirName      = "logs"
	daemonLogName    = "daemon.log"
	projectsDirName  = "projects"
	controlDirName   = "control"
	executionDirName = "execution"
	runtimeDirName   = "runtime"
)

// Environment is the process state path resolution depends on.
//
// It is passed in rather than read from the process, so that this package has
// no hidden inputs and a test can resolve a layout for a machine it is not
// running on.
type Environment struct {
	// Getenv reads an environment variable. A nil value reads the real process
	// environment.
	Getenv func(string) string
	// Home is the user's home directory. It is used only where no XDG variable
	// applies.
	Home string
	// UID is the user's numeric identifier. It separates one user's runtime
	// directory from another's in a location several users share, such as
	// /tmp.
	UID int
	// GOOS is the operating system whose socket path limit applies. An empty
	// value means the running one.
	GOOS string
}

// Current returns the environment of the running process.
func Current() (Environment, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Environment{}, fmt.Errorf("resolving the home directory: %w", err)
	}
	return Environment{Getenv: os.Getenv, Home: home, UID: os.Getuid(), GOOS: runtime.GOOS}, nil
}

// Layout is the set of directories and files one Feat installation uses.
//
// Every field is an absolute path. Nothing in this package creates, reads, or
// changes any of them: resolving where a file belongs and deciding whether it
// may be used are separate jobs, and the second one belongs to the component
// that owns the file.
type Layout struct {
	// Config holds human-authored YAML configuration.
	Config string
	// State holds JSON snapshots, JSONL events, and Markdown briefs.
	State string
	// Runtime holds the ephemeral ownership files of a running daemon. It is
	// expected to disappear when the machine restarts, which is what makes a
	// leftover record recognisably stale (ADR-027).
	Runtime string
	// Socket is the Unix-domain socket the daemon listens on.
	Socket string
}

// ProjectConfigDir returns the directory holding per-project YAML.
func (l Layout) ProjectConfigDir() string { return filepath.Join(l.Config, projectsDirName) }

// LockFile returns the path of the daemon's ownership lock.
func (l Layout) LockFile() string { return filepath.Join(l.Runtime, lockName) }

// EndpointFile returns the path of the running daemon's endpoint record.
func (l Layout) EndpointFile() string { return filepath.Join(l.Runtime, endpointName) }

// LogFile returns the path of the daemon log.
func (l Layout) LogFile() string { return filepath.Join(l.State, logsDirName, daemonLogName) }

// ControlRoot returns the directory holding every task control workspace. It is
// under the state directory but outside the per-task snapshot directory, because
// this tree is mounted into the agent's environment and must not carry a task's
// snapshot, event log, or stored brief with it (ADR-032).
//
// internal/control builds the per-task path below it and validates the
// identifiers first. This package joins constants only, which keeps it a leaf.
func (l Layout) ControlRoot() string { return filepath.Join(l.State, controlDirName) }

// ExecutionRoot returns the directory holding generated execution inputs. It
// sits beside the control root because the snapshot directory holds only what
// the store owns, and because these files decide what a task's container mounts.
// Unlike the control workspace, this tree is mounted nowhere (ADR-033).
//
// The daemon builds the per-task path below it and validates the identifiers
// first, as internal/control does beneath ControlRoot.
func (l Layout) ExecutionRoot() string { return filepath.Join(l.State, executionDirName) }

// RuntimeRoot returns the directory holding generated application runtime
// inputs. It is separate from the execution root for the reason the two adapters
// are: that one decides what the agent's container reaches, and this one what
// the application runs. Both are host-only and mounted nowhere (ADR-034).
//
// The daemon builds the per-task path below it and validates the identifiers
// first, as internal/control does beneath ControlRoot.
func (l Layout) RuntimeRoot() string { return filepath.Join(l.State, runtimeDirName) }

// TmuxSocket returns the dedicated tmux server socket. It shares the owner-only
// runtime directory with the daemon's ownership files but outlives them:
// stopping the daemon leaves tmux and every task terminal running (ADR-030).
func (l Layout) TmuxSocket() string { return filepath.Join(l.Runtime, tmuxSocketName) }

// Resolve returns the layout the environment implies.
//
// Configuration and state follow the XDG base directory specification, which
// requires a relative value to be ignored rather than honoured. The runtime
// directory falls back from the XDG variable to the per-user temporary
// directory to /tmp, because macOS provides no XDG runtime directory.
func Resolve(env Environment) (Layout, error) {
	config, err := env.xdgDir(EnvConfigHome, ".config")
	if err != nil {
		return Layout{}, err
	}
	state, err := env.xdgDir(EnvDataHome, ".local", "share")
	if err != nil {
		return Layout{}, err
	}
	runtimeDir, err := env.runtimeDir()
	if err != nil {
		return Layout{}, err
	}

	layout := Layout{
		Config:  config,
		State:   state,
		Runtime: runtimeDir,
		Socket:  filepath.Join(runtimeDir, socketName),
	}
	if err := checkSocketPath(layout.Socket, env.goos()); err != nil {
		return Layout{}, err
	}
	if err := checkSocketPath(layout.TmuxSocket(), env.goos()); err != nil {
		return Layout{}, err
	}
	return layout, nil
}

// Expand resolves a leading "~" against the user's home directory and cleans the
// result. A "~other" form is rejected rather than resolved, because
// configuration reaching into another user's home is more likely a mistake than
// an intention.
func (e Environment) Expand(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		if strings.HasPrefix(path, "~") {
			return "", fmt.Errorf("path %q expands another user's home directory, which Feat does not resolve", path)
		}
		return filepath.Clean(path), nil
	}

	home, err := e.home()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[len("~/"):]), nil
}

// xdgDir resolves one XDG base directory, or the given path under the home
// directory when the variable is unset or relative.
func (e Environment) xdgDir(key string, fallback ...string) (string, error) {
	if value := e.lookup(key); value != "" {
		if filepath.IsAbs(value) {
			return filepath.Join(value, dirName), nil
		}
		// The XDG specification says a relative value is invalid and must be
		// ignored, so this is a fallback rather than an error.
	}

	home, err := e.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, append(fallback, dirName)...)...), nil
}

// runtimeDir resolves the directory holding the daemon's ownership files.
func (e Environment) runtimeDir() (string, error) {
	if override := e.lookup(EnvRuntimeOverride); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("%s must be an absolute path, but is %q", EnvRuntimeOverride, override)
		}
		cleaned := filepath.Clean(override)
		if err := e.checkOwnable(cleaned); err != nil {
			return "", err
		}
		return cleaned, nil
	}
	if dir := e.lookup(EnvRuntimeDir); filepath.IsAbs(dir) {
		return filepath.Join(dir, dirName), nil
	}
	// TMPDIR is per-user on macOS and /tmp is shared, so the user id goes in
	// the name either way. One user's daemon must never resolve to another's
	// ownership files.
	if dir := e.lookup(EnvTempDir); filepath.IsAbs(dir) {
		return filepath.Join(dir, dirName+"-"+strconv.Itoa(e.UID)), nil
	}
	return filepath.Join("/tmp", dirName+"-"+strconv.Itoa(e.UID)), nil
}

// checkOwnable refuses a runtime directory the daemon must not claim as its own.
//
// The branches above append a component of Feat's own, so the daemon ends up
// owning a directory it created. The override replaces the path entirely, and
// the daemon then restricts it to this user, so FEAT_RUNTIME_DIR=$HOME chmods
// the home directory to 0700.
//
// Broad answers for /tmp, /var, and every one-component path, but not for the
// home directory, which is deep enough and is an ordinary worktree root. Widening
// Broad would start refusing worktree roots that are fine, so the one directory
// the difference turns on is named here instead.
func (e Environment) checkOwnable(dir string) error {
	if Broad(dir) {
		return fmt.Errorf(
			"%s names %s, which Feat must not take over: the daemon restricts its runtime directory to this "+
				"user and fills it with the daemon socket, the ownership lock, the endpoint record, and the "+
				"tmux socket. Name a directory that is Feat's alone, such as one inside that one",
			EnvRuntimeOverride, dir)
	}
	home, err := e.home()
	if err == nil && dir == home {
		return fmt.Errorf(
			"%s names %s, which is your home directory, and Feat would take it over: the daemon restricts "+
				"its runtime directory to this user and fills it with the daemon socket, the ownership lock, "+
				"the endpoint record, and the tmux socket. Name a directory inside it instead",
			EnvRuntimeOverride, dir)
	}
	return nil
}

func (e Environment) lookup(key string) string {
	if e.Getenv == nil {
		return os.Getenv(key)
	}
	return e.Getenv(key)
}

func (e Environment) home() (string, error) {
	if !filepath.IsAbs(e.Home) {
		return "", fmt.Errorf("home directory %q is not an absolute path", e.Home)
	}
	return filepath.Clean(e.Home), nil
}

func (e Environment) goos() string {
	if e.GOOS == "" {
		return runtime.GOOS
	}
	return e.GOOS
}

// SocketTooLongError reports a socket path the operating system cannot bind. The
// kernel copies the path into a fixed-size field, so an over-long one fails at
// bind time with a message that says nothing about length.
type SocketTooLongError struct {
	// Path is the resolved socket path.
	Path string
	// Limit is the number of bytes the platform reserves for it, including the
	// terminating zero byte.
	Limit int
}

func (e *SocketTooLongError) Error() string {
	return fmt.Sprintf(
		"socket path %s is %d bytes, and this platform allows %d: set %s to a shorter directory",
		e.Path, len(e.Path), e.Limit-1, EnvRuntimeOverride,
	)
}

// checkSocketPath reports whether the path fits the platform's socket address.
func checkSocketPath(path, goos string) error {
	limit := socketPathLimit(goos)
	if len(path)+1 > limit {
		return &SocketTooLongError{Path: path, Limit: limit}
	}
	return nil
}

// socketPathLimit returns the size of the platform's socket path field,
// including the terminating zero byte.
func socketPathLimit(goos string) int {
	if goos == "darwin" {
		return 104
	}
	return 108
}
