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
	// Viewers is how many attached clients are looking at this task's window,
	// which is what tmux answers with window_active_clients. It is how a test
	// arranges a user who is watching one task while others run unwatched.
	Viewers int
	// PID is the process tmux started in the agent pane, so a test can arrange
	// the process tree a resource observer would walk.
	PID int
}

type sessionObject struct {
	id      string
	options map[string]string
}

type windowObject struct {
	id      string
	session string
	viewers int
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
	pid       int
	options   map[string]string
	// content is what a capture of this pane returns, and cursorX/cursorY where
	// tmux reports the cursor. A pane nothing has been written to captures as
	// empty, which is what a freshly created one shows.
	content          string
	cursorX, cursorY int
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

	// size is each window's cell size, set by a resize and reported by a
	// measurement, so that a test can check the size Feat asked for is the size
	// it draws into.
	size map[string][2]int
	// buffers holds what was staged for a paste, and pastes records what reached
	// each pane, so that a test can assert on delivered input rather than on the
	// command that delivered it.
	buffers map[string]string
	pastes  map[string][]string
	keys    map[string][]string
	typed   map[string][]string
	zoomed  map[string]string
}

// New returns a server holding the terminals given. No terminals is an empty
// managed server, which is what a restart finds when tmux is gone.
func New(terminals ...Terminal) *Server {
	server := &Server{
		sessions:    make(map[string]*sessionObject),
		windows:     make(map[string]*windowObject),
		panes:       make(map[string]*paneObject),
		fail:        make(map[string]error),
		size:        make(map[string][2]int),
		buffers:     make(map[string]string),
		pastes:      make(map[string][]string),
		keys:        make(map[string][]string),
		typed:       make(map[string][]string),
		zoomed:      make(map[string]string),
		nextSession: 1,
		nextWindow:  1,
		nextPane:    1,
	}
	for _, terminal := range terminals {
		server.seed(terminal)
	}
	return server
}

// Watch sets how many attached clients are looking at one window, which is what
// tmux answers with window_active_clients.
//
// It is how a test arranges a user who is watching one task while their other
// tasks run unwatched, which is the distinction notification suppression turns
// on: a session-level answer would silence every task the moment one of them was
// being looked at.
func (s *Server) Watch(window string, viewers int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record, exists := s.windows[window]; exists {
		record.viewers = viewers
	}
}

// SetPanePID sets the process tmux started in a pane, which is where a resource
// observer begins walking a task's process tree.
func (s *Server) SetPanePID(pane string, pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record, exists := s.panes[pane]; exists {
		record.pid = pid
	}
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
	case "display-message":
		return s.measure(args)
	case "capture-pane":
		return s.capture(args)
	case "send-keys":
		return "", s.sendKeys(args)
	case "set-buffer":
		s.buffers[value(args, "-b")] = last(args)
		return "", nil
	case "paste-buffer":
		return "", s.pasteBuffer(args)
	case "set-window-option":
		// Unsetting window-size is how Feat releases a pinned window. tmux then
		// resizes it when a client attaches, which this fake stands in for by
		// forgetting the size: nothing here has a client to be sized to.
		if len(args) > 1 && args[1] == "-u" {
			delete(s.size, value(args, "-t"))
		}
		return "", nil
	case "resize-window":
		return "", s.resizeWindow(args)
	case "resize-pane":
		return "", s.resizePane(args)
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
	s.windows[terminal.Window] = &windowObject{
		id: terminal.Window, session: terminal.Session, viewers: terminal.Viewers,
		options: map[string]string{
			"@feat_managed":    "1",
			"@feat_schema":     schema,
			"@feat_project_id": terminal.Project,
			"@feat_task_id":    terminal.Task,
		},
	}
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
		directory: terminal.Directory, dead: terminal.Dead, status: status, pid: terminal.PID,
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
			"window_active_clients": strconv.Itoa(record.viewers),
		}, record.options))
	}
	return out
}

func (s *Server) paneValues() []map[string]string {
	out := make([]map[string]string, 0, len(s.panes))
	for _, id := range sortedKeys(s.panes) {
		record := s.panes[id]
		width, height, left, top := s.geometry(record)
		out = append(out, merge(map[string]string{
			"session_id":        record.session,
			"window_id":         record.window,
			"pane_id":           record.id,
			"pane_dead":         flag(record.dead),
			"pane_dead_status":  record.status,
			"pane_current_path": record.directory,
			"pane_pid":          strconv.Itoa(record.pid),
			"pane_left":         strconv.Itoa(left),
			"pane_top":          strconv.Itoa(top),
			"pane_width":        strconv.Itoa(width),
			"pane_height":       strconv.Itoa(height),
			"pane_active":       flag(s.activePane(record) == record.id),
			"cursor_x":          strconv.Itoa(record.cursorX),
			"cursor_y":          strconv.Itoa(record.cursorY),
		}, record.options))
	}
	return out
}

// geometry places a pane in its window, tiling left to right the way a
// horizontal split does. It is what lets a caller compose a window from its
// panes without a real tmux to lay them out.
func (s *Server) geometry(pane *paneObject) (width, height, left, top int) {
	size, ok := s.size[pane.window]
	if !ok {
		size = [2]int{80, 24}
	}

	siblings := make([]string, 0, 2)
	for _, id := range sortedKeys(s.panes) {
		if s.panes[id].window == pane.window {
			siblings = append(siblings, id)
		}
	}
	if len(siblings) == 0 {
		return size[0], size[1], 0, 0
	}

	// Equal columns with a one-cell divider between them, which is the layout a
	// -h split produces and the one Feat's shell pane uses.
	each := (size[0] - (len(siblings) - 1)) / len(siblings)
	for i, id := range siblings {
		if id == pane.id {
			return each, size[1], i * (each + 1), 0
		}
	}
	return each, size[1], 0, 0
}

// activePane is the first pane of a window, which is the agent's.
func (s *Server) activePane(pane *paneObject) string {
	for _, id := range sortedKeys(s.panes) {
		if s.panes[id].window == pane.window {
			return id
		}
	}
	return pane.id
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

// SetPaneContent arranges what a capture of one pane returns.
func (s *Server) SetPaneContent(pane, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if object, ok := s.panes[pane]; ok {
		object.content = content
	}
}

// PaneSize reports the size a window was resized to, and false when none was.
func (s *Server) PaneSize(window string) ([2]int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, ok := s.size[window]
	return size, ok
}

// Keys reports the key names delivered to one pane, in order.
func (s *Server) Keys(pane string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys[pane]...)
}

// Pastes reports the text delivered to one pane, in order.
func (s *Server) Pastes(pane string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.pastes[pane]...)
}

// Buffers reports the tmux buffers still staged, so that a test can check a
// paste took its own buffer away with it.
func (s *Server) Buffers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.buffers))
	for name := range s.buffers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resizePane toggles the zoom of a pane's window.
func (s *Server) resizePane(args []string) error {
	target := value(args, "-t")
	pane, ok := s.panes[target]
	if !ok {
		if window, found := s.windows[target]; found {
			s.zoomed[window.id] = ""
			return nil
		}
		return fmt.Errorf("tmuxtest: no such pane or window %q", target)
	}
	if s.zoomed[pane.window] == pane.id {
		s.zoomed[pane.window] = ""
		return nil
	}
	s.zoomed[pane.window] = pane.id
	return nil
}

// Zoomed reports which pane of a window is zoomed, empty when none is.
func (s *Server) Zoomed(window string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.zoomed[window]
}

// measure answers the format display-message is given.
//
// The format is rendered from a map of what the target is, rather than
// recognised one query at a time: the adapter combines and splits these queries
// as it learns what it needs, and a fake that matches on substrings answers the
// old shape confidently after the real one has changed. A field this does not
// model is an error here rather than an empty column that fails later as a
// parse.
func (s *Server) measure(args []string) (string, error) {
	target := value(args, "-t")
	values, err := s.measurements(target)
	if err != nil {
		return "", err
	}
	// display-message -p -t <target> <format>: the format is the last argument.
	return renderFormat(args[len(args)-1], values)
}

// measurements is everything display-message can be asked about one target.
func (s *Server) measurements(target string) (map[string]string, error) {
	if window, ok := s.windows[target]; ok {
		size := s.windowSize(window.id)
		return merge(map[string]string{
			"session_id":            window.session,
			"window_id":             window.id,
			"window_width":          strconv.Itoa(size[0]),
			"window_height":         strconv.Itoa(size[1]),
			"window_zoomed_flag":    flag(s.zoomed[window.id] != ""),
			"window_panes":          strconv.Itoa(s.windowPanes(window.id)),
			"window_active_clients": strconv.Itoa(window.viewers),
		}, window.options), nil
	}

	pane, ok := s.panes[target]
	if !ok {
		return nil, fmt.Errorf("tmuxtest: no such pane or window %q", target)
	}

	size := s.windowSize(pane.window)
	width, height, left, top := s.geometry(pane)
	zoomed := s.zoomed[pane.window]
	if zoomed == pane.id {
		// A zoomed pane fills its window, which is the only reason to zoom one.
		width, height = size[0], size[1]
	}
	return merge(map[string]string{
		"session_id":         pane.session,
		"window_id":          pane.window,
		"pane_id":            pane.id,
		"window_width":       strconv.Itoa(size[0]),
		"window_height":      strconv.Itoa(size[1]),
		"window_zoomed_flag": flag(zoomed != ""),
		"window_panes":       strconv.Itoa(s.windowPanes(pane.window)),
		"pane_active":        flag(zoomed == pane.id || (zoomed == "" && s.activePane(pane) == pane.id)),
		"pane_width":         strconv.Itoa(width),
		"pane_height":        strconv.Itoa(height),
		"pane_left":          strconv.Itoa(left),
		"pane_top":           strconv.Itoa(top),
		"cursor_x":           strconv.Itoa(pane.cursorX),
		"cursor_y":           strconv.Itoa(pane.cursorY),
		"pane_dead":          flag(pane.dead),
		"pane_dead_status":   pane.status,
		"pane_current_path":  pane.directory,
		"pane_pid":           strconv.Itoa(pane.pid),
	}, pane.options), nil
}

// windowSize is the size a window was resized to, or the server's default.
func (s *Server) windowSize(window string) [2]int {
	if size, ok := s.size[window]; ok {
		return size
	}
	return [2]int{80, 24}
}

// windowPanes counts the panes a window holds.
func (s *Server) windowPanes(window string) int {
	panes := 0
	for _, pane := range s.panes {
		if pane.window == window {
			panes++
		}
	}
	return panes
}

// renderFormat fills one tmux format string from a map of values.
func renderFormat(format string, values map[string]string) (string, error) {
	fields := strings.Split(format, "\t")
	rendered := make([]string, 0, len(fields))
	for _, field := range fields {
		name := strings.TrimSuffix(strings.TrimPrefix(field, "#{"), "}")
		value, ok := values[name]
		if !ok {
			return "", fmt.Errorf("tmuxtest: nothing here models #{%s}", name)
		}
		rendered = append(rendered, value)
	}
	return strings.Join(rendered, "\t"), nil
}

func (s *Server) capture(args []string) (string, error) {
	pane, ok := s.panes[value(args, "-t")]
	if !ok {
		return "", fmt.Errorf("tmuxtest: no such pane %q", value(args, "-t"))
	}
	return pane.content, nil
}

func (s *Server) sendKeys(args []string) error {
	target := value(args, "-t")
	if _, ok := s.panes[target]; !ok {
		return fmt.Errorf("tmuxtest: no such pane %q", target)
	}

	// -l is literal text rather than key names: it is what typing sends, and a
	// test asserting on delivered input has to be able to tell the two apart.
	literal := false
	for _, arg := range args {
		if arg == "-l" {
			literal = true
		}
	}

	for i, arg := range args {
		if arg == "--" {
			if literal {
				s.typed[target] = append(s.typed[target], args[i+1:]...)
				return nil
			}
			s.keys[target] = append(s.keys[target], args[i+1:]...)
			return nil
		}
	}
	return errors.New("tmuxtest: send-keys carried no terminator")
}

// Typed reports the text delivered to one pane as typing, in order.
func (s *Server) Typed(pane string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.typed[pane]...)
}

func (s *Server) pasteBuffer(args []string) error {
	target := value(args, "-t")
	if _, ok := s.panes[target]; !ok {
		return fmt.Errorf("tmuxtest: no such pane %q", target)
	}
	name := value(args, "-b")
	text, staged := s.buffers[name]
	if !staged {
		return fmt.Errorf("tmuxtest: no buffer %q to paste", name)
	}
	s.pastes[target] = append(s.pastes[target], text)
	// -d deletes the buffer, which is what keeps Feat out of the user's stack.
	for _, arg := range args {
		if arg == "-d" {
			delete(s.buffers, name)
		}
	}
	return nil
}

func (s *Server) resizeWindow(args []string) error {
	target := value(args, "-t")
	if _, ok := s.windows[target]; !ok {
		return fmt.Errorf("tmuxtest: no such window %q", target)
	}
	width, err := strconv.Atoi(value(args, "-x"))
	if err != nil {
		return fmt.Errorf("tmuxtest: resize width %q: %w", value(args, "-x"), err)
	}
	height, err := strconv.Atoi(value(args, "-y"))
	if err != nil {
		return fmt.Errorf("tmuxtest: resize height %q: %w", value(args, "-y"), err)
	}
	s.size[target] = [2]int{width, height}
	return nil
}

// last is the final argument, which is where set-buffer carries its text.
func last(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}
