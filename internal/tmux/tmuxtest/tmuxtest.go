// Package tmuxtest provides a fake tmux server for the packages that drive the
// tmux adapter.
//
// The adapter's own tests use an in-package fake to assert exact argument
// vectors. This one exists for its callers — the daemon above all — which need
// to arrange a server state and then observe what Feat records about it. The
// orchestration those callers own is where a half-finished lifecycle becomes
// recoverable or does not, so it must be testable without tmux installed.
//
// The fake plays tmux, which knows Feat's metadata only as opaque user
// options. It therefore imports nothing from internal/tmux and renders each
// list command by reading the format the adapter asked for, so a change to
// those formats cannot silently misalign the fields it answers with.
package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Schema is the metadata version the adapter writes. A terminal may override it
// to represent state left by a different version of Feat.
const Schema = "1"

// Roles a tagged pane may carry.
const (
	RoleAgent = "agent"
	RoleShell = "shell"
)

// Terminal is one tagged task terminal to place on the fake server.
//
// It is the state a daemon restart would find, expressed the way a test wants
// to say it rather than as the option-and-format soup tmux would report.
type Terminal struct {
	Project   string
	Task      string
	Session   string
	Window    string
	Pane      string
	Shell     string // pane id of the on-demand shell pane; empty for none
	Directory string
	Dead      bool
	Exit      int    // exit status, reported only when the pane is dead
	Schema    string // metadata version; empty means the current one
}

type sessionObject struct {
	id      string
	options map[string]string
}

type windowObject struct {
	id      string
	session string
	options map[string]string
}

type paneObject struct {
	id        string
	window    string
	session   string
	directory string
	program   string
	dead      bool
	status    string
	options   map[string]string
}

// Server is a fake tmux server that satisfies the adapter's Runner interface.
type Server struct {
	mu       sync.Mutex
	sessions map[string]*sessionObject
	windows  map[string]*windowObject
	panes    map[string]*paneObject
	calls    [][]string
	sockets  []string
	fail     map[string]error

	nextSession int
	nextWindow  int
	nextPane    int
}

// New returns a server holding the terminals given. No terminals is an empty
// managed server, which is what a restart finds when tmux is gone.
func New(terminals ...Terminal) *Server {
	server := &Server{
		sessions:    make(map[string]*sessionObject),
		windows:     make(map[string]*windowObject),
		panes:       make(map[string]*paneObject),
		fail:        make(map[string]error),
		nextSession: 1,
		nextWindow:  1,
		nextPane:    1,
	}
	for _, terminal := range terminals {
		server.seed(terminal)
	}
	return server
}

// Fail makes one tmux command return err until it is cleared with a nil error.
func (s *Server) Fail(command string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.fail, command)
		return
	}
	s.fail[command] = err
}

// Calls returns every argument vector the adapter has run, in order.
func (s *Server) Calls() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, 0, len(s.calls))
	for _, call := range s.calls {
		out = append(out, append([]string(nil), call...))
	}
	return out
}

// Ran reports whether a command was run. Tests use it to assert that recovery
// observed the world without changing it.
func (s *Server) Ran(command string) bool {
	for _, call := range s.Calls() {
		if len(call) > 0 && call[0] == command {
			return true
		}
	}
	return false
}

// Launch is one program started in a pane, with the working directory it was
// started in.
type Launch struct {
	// Pane is the tmux pane identifier.
	Pane string
	// Directory is the working directory the pane was given.
	Directory string
	// Command is the program followed by its arguments.
	Command []string
}

// Program returns the executable, or an empty string for an empty launch.
func (l Launch) Program() string {
	if len(l.Command) == 0 {
		return ""
	}
	return l.Command[0]
}

// Launches returns every program started in a pane, in order.
//
// It reads the recorded calls rather than the panes, because a test about what
// was launched wants the arguments as they were passed: whether a launch
// carried the right flags is a different question from what the pane is running
// now.
func (s *Server) Launches() []Launch {
	var launches []Launch
	for _, call := range s.Calls() {
		if len(call) == 0 || call[0] != "respawn-pane" {
			continue
		}
		index := indexOf(call, "-c")
		if index < 0 || index+2 > len(call) {
			continue
		}
		launches = append(launches, Launch{
			Pane:      value(call, "-t"),
			Directory: call[index+1],
			Command:   append([]string(nil), call[index+2:]...),
		})
	}
	return launches
}

// Launched returns the first program started in a pane.
func (s *Server) Launched() (Launch, bool) {
	launches := s.Launches()
	if len(launches) == 0 {
		return Launch{}, false
	}
	return launches[0], true
}

// Sockets returns the socket every call was sent to.
func (s *Server) Sockets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sockets...)
}

// Run executes one tmux command.
func (s *Server) Run(_ context.Context, socket string, args ...string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, append([]string(nil), args...))
	s.sockets = append(s.sockets, socket)
	if len(args) == 0 {
		return "", errors.New("tmuxtest: missing command")
	}
	if err := s.fail[args[0]]; err != nil {
		return "", err
	}

	switch args[0] {
	case "list-sessions":
		return s.list(format(args), s.sessionValues()), nil
	case "list-windows":
		return s.list(format(args), s.windowValues()), nil
	case "list-panes":
		return s.list(format(args), s.paneValues()), nil
	case "new-session":
		return s.newSession(args), nil
	case "new-window":
		return s.newWindow(args)
	case "split-window":
		return s.splitWindow(args)
	case "set-option":
		return "", s.setOption(args)
	case "respawn-pane":
		return "", s.respawnPane(args)
	case "kill-session":
		s.killSession(value(args, "-t"))
		return "", nil
	case "kill-window":
		s.killWindow(value(args, "-t"))
		return "", nil
	case "kill-pane":
		delete(s.panes, value(args, "-t"))
		return "", nil
	default:
		return "", fmt.Errorf("tmuxtest: unexpected command %q", strings.Join(args, " "))
	}
}

func (s *Server) seed(terminal Terminal) {
	schema := terminal.Schema
	if schema == "" {
		schema = Schema
	}
	if _, exists := s.sessions[terminal.Session]; !exists {
		s.sessions[terminal.Session] = &sessionObject{id: terminal.Session, options: map[string]string{
			"@feat_managed":    "1",
			"@feat_schema":     schema,
			"@feat_project_id": terminal.Project,
		}}
	}
	s.windows[terminal.Window] = &windowObject{id: terminal.Window, session: terminal.Session, options: map[string]string{
		"@feat_managed":    "1",
		"@feat_schema":     schema,
		"@feat_project_id": terminal.Project,
		"@feat_task_id":    terminal.Task,
	}}
	s.panes[terminal.Pane] = s.seedPane(terminal, schema, terminal.Pane, RoleAgent)
	if terminal.Shell != "" {
		shell := terminal
		shell.Dead = false
		s.panes[terminal.Shell] = s.seedPane(shell, schema, terminal.Shell, RoleShell)
	}
}

func (s *Server) seedPane(terminal Terminal, schema, id, role string) *paneObject {
	status := ""
	if terminal.Dead {
		status = strconv.Itoa(terminal.Exit)
	}
	return &paneObject{
		id: id, window: terminal.Window, session: terminal.Session,
		directory: terminal.Directory, dead: terminal.Dead, status: status,
		options: map[string]string{
			"@feat_managed":    "1",
			"@feat_schema":     schema,
			"@feat_project_id": terminal.Project,
			"@feat_task_id":    terminal.Task,
			"@feat_pane_role":  role,
		},
	}
}

// newSession starts the server, as the first task terminal does.
func (s *Server) newSession(args []string) string {
	session := fmt.Sprintf("$%d", s.nextSession)
	s.nextSession++
	s.sessions[session] = &sessionObject{id: session, options: make(map[string]string)}
	window, pane := s.create(session, value(args, "-c"))
	return session + "\t" + window + "\t" + pane
}

func (s *Server) newWindow(args []string) (string, error) {
	session := value(args, "-t")
	if _, exists := s.sessions[session]; !exists {
		return "", fmt.Errorf("tmuxtest: no session %q", session)
	}
	window, pane := s.create(session, value(args, "-c"))
	return session + "\t" + window + "\t" + pane, nil
}

func (s *Server) splitWindow(args []string) (string, error) {
	target, exists := s.panes[value(args, "-t")]
	if !exists {
		return "", fmt.Errorf("tmuxtest: no pane %q to split", value(args, "-t"))
	}
	pane := fmt.Sprintf("%%%d", s.nextPane)
	s.nextPane++
	s.panes[pane] = &paneObject{
		id: pane, window: target.window, session: target.session,
		directory: value(args, "-c"), options: make(map[string]string),
	}
	return target.session + "\t" + target.window + "\t" + pane, nil
}

func (s *Server) create(session, directory string) (string, string) {
	window := fmt.Sprintf("@%d", s.nextWindow)
	pane := fmt.Sprintf("%%%d", s.nextPane)
	s.nextWindow++
	s.nextPane++
	s.windows[window] = &windowObject{id: window, session: session, options: make(map[string]string)}
	s.panes[pane] = &paneObject{
		id: pane, window: window, session: session,
		directory: directory, options: make(map[string]string),
	}
	return window, pane
}

func (s *Server) setOption(args []string) error {
	index := indexOf(args, "-t")
	if index < 0 || len(args) <= index+3 {
		return fmt.Errorf("tmuxtest: malformed set-option %q", strings.Join(args, " "))
	}
	target, option, setting := args[index+1], args[index+2], args[index+3]

	switch {
	case contains(args, "-p"):
		pane, exists := s.panes[target]
		if !exists {
			return fmt.Errorf("tmuxtest: no such pane: %s", target)
		}
		pane.options[option] = setting
	case contains(args, "-w"):
		window, exists := s.windows[target]
		if !exists {
			return fmt.Errorf("tmuxtest: no such window: %s", target)
		}
		window.options[option] = setting
	default:
		session, exists := s.sessions[target]
		if !exists {
			return fmt.Errorf("tmuxtest: no such session: %s", target)
		}
		session.options[option] = setting
	}
	return nil
}

// respawnPane replaces a pane's process. The pane keeps its identity and its
// options, which is what lets the adapter tag a pane before starting anything
// in it.
func (s *Server) respawnPane(args []string) error {
	pane, exists := s.panes[value(args, "-t")]
	if !exists {
		return fmt.Errorf("tmuxtest: no such pane: %s", value(args, "-t"))
	}
	index := indexOf(args, "-c")
	if index < 0 || index+2 > len(args) {
		return errors.New("tmuxtest: respawn-pane without a working directory and command")
	}
	command := args[index+2:]
	if len(command) == 0 {
		return errors.New("tmuxtest: respawn-pane without a command")
	}
	pane.directory = args[index+1]
	pane.program = command[0]
	return nil
}

func (s *Server) killSession(id string) {
	delete(s.sessions, id)
	for window, record := range s.windows {
		if record.session == id {
			s.killWindow(window)
		}
	}
}

func (s *Server) killWindow(id string) {
	delete(s.windows, id)
	for pane, record := range s.panes {
		if record.window == id {
			delete(s.panes, pane)
		}
	}
}

func (s *Server) sessionValues() []map[string]string {
	out := make([]map[string]string, 0, len(s.sessions))
	for _, id := range sortedKeys(s.sessions) {
		record := s.sessions[id]
		out = append(out, merge(map[string]string{"session_id": record.id}, record.options))
	}
	return out
}

func (s *Server) windowValues() []map[string]string {
	out := make([]map[string]string, 0, len(s.windows))
	for _, id := range sortedKeys(s.windows) {
		record := s.windows[id]
		out = append(out, merge(map[string]string{
			"session_id": record.session, "window_id": record.id,
		}, record.options))
	}
	return out
}

func (s *Server) paneValues() []map[string]string {
	out := make([]map[string]string, 0, len(s.panes))
	for _, id := range sortedKeys(s.panes) {
		record := s.panes[id]
		out = append(out, merge(map[string]string{
			"session_id":        record.session,
			"window_id":         record.window,
			"pane_id":           record.id,
			"pane_dead":         flag(record.dead),
			"pane_dead_status":  record.status,
			"pane_current_path": record.directory,
		}, record.options))
	}
	return out
}

// list renders one record per line in the format the adapter asked for.
func (s *Server) list(format string, records []map[string]string) string {
	lines := make([]string, 0, len(records))
	for _, values := range records {
		fields := strings.Split(format, "\t")
		rendered := make([]string, 0, len(fields))
		for _, field := range fields {
			rendered = append(rendered, values[strings.TrimSuffix(strings.TrimPrefix(field, "#{"), "}")])
		}
		lines = append(lines, strings.Join(rendered, "\t"))
	}
	return strings.Join(lines, "\n")
}

func format(args []string) string { return value(args, "-F") }

func value(args []string, name string) string {
	index := indexOf(args, name)
	if index < 0 || index+1 >= len(args) {
		return ""
	}
	return args[index+1]
}

func indexOf(args []string, value string) int {
	for i, argument := range args {
		if argument == value {
			return i
		}
	}
	return -1
}

func contains(args []string, value string) bool { return indexOf(args, value) >= 0 }

func flag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func merge(base, options map[string]string) map[string]string {
	for name, value := range options {
		base[name] = value
	}
	return base
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
