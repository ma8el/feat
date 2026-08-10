package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// Limits for one request. The daemon is local, so a request that takes longer
// than this is stuck rather than slow.
const (
	requestTimeout = 10 * time.Second
	// maxResponseBody bounds a response, so that a broken daemon cannot make a
	// client allocate without limit.
	maxResponseBody = 32 << 20
)

// host appears in the request URL because net/http requires one. The daemon
// never looks at it: the socket path decides who is answering.
const host = "feat"

// Client talks to the local daemon over its Unix-domain socket.
//
// It is a transport and nothing else: it does not start a daemon, read
// persistent state, or hold a domain type. Starting a daemon belongs to
// internal/daemon, which knows how, and internal/cli, which decides when.
type Client struct {
	socket string
	http   *http.Client
}

// New returns a client for the daemon listening on the given socket.
func New(socket string) *Client {
	return &Client{
		socket: socket,
		http: &http.Client{
			// No client timeout: the event stream is meant to stay open, and
			// every other call bounds itself with a context instead.
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var dialer net.Dialer
					return dialer.DialContext(ctx, "unix", socket)
				},
				// A local socket gains nothing from compression, and a TUI keeps
				// a stream and a few requests going at once.
				DisableCompression: true,
				MaxIdleConns:       4,
				IdleConnTimeout:    90 * time.Second,
			},
		},
	}
}

// Socket returns the path the client talks to.
func (c *Client) Socket() string { return c.socket }

// Close releases the connections the client is keeping open.
//
// A daemon draining for shutdown waits on connections that are still open, so a
// client that is finished should say so rather than leave them to time out.
func (c *Client) Close() {
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

// Health reports the daemon's status.
func (c *Client) Health(ctx context.Context) (api.Health, error) {
	return fetch[api.Health](ctx, c, "/health")
}

// Projects returns every registered project.
func (c *Client) Projects(ctx context.Context) ([]api.Project, error) {
	return fetch[[]api.Project](ctx, c, "/projects")
}

// Project returns one project.
func (c *Client) Project(ctx context.Context, id string) (api.Project, error) {
	return fetch[api.Project](ctx, c, "/projects/"+url.PathEscape(id))
}

// RegisterProject records a project from its configuration file.
//
// Only the identifier is sent. The daemon reads the configuration from the
// directory it resolved for itself, so a client never hands it a path.
func (c *Client) RegisterProject(ctx context.Context, id string) (api.Registration, error) {
	return send[api.Registration](ctx, c, "/projects", api.RegisterProject{ProjectID: id})
}

// Tasks returns every task of every project.
func (c *Client) Tasks(ctx context.Context) ([]api.Task, error) {
	return fetch[[]api.Task](ctx, c, "/tasks")
}

// Resources returns the most recent resource sample.
//
// The daemon samples on its own schedule and this reads what it has, so a client
// that asks often does not make the machine work harder.
func (c *Client) Resources(ctx context.Context) (api.ResourceReport, error) {
	return fetch[api.ResourceReport](ctx, c, "/resources")
}

// Task returns one task, addressed by task identifier alone.
func (c *Client) Task(ctx context.Context, id string) (api.Task, error) {
	return fetch[api.Task](ctx, c, "/tasks/"+url.PathEscape(id))
}

// AttachInfo resolves the task's live native tmux target. The client process,
// not the daemon, attaches so the user's terminal streams stay native.
func (c *Client) AttachInfo(ctx context.Context, id string) (api.AttachInfo, error) {
	return send[api.AttachInfo](ctx, c, "/tasks/"+url.PathEscape(id)+"/attach-info", struct{}{})
}

// Shell opens or finds the task's shell pane and returns its target.
//
// Only the task is named. The daemon decides which program runs and where, so
// no client hands it something to execute.
func (c *Client) Shell(ctx context.Context, id string) (api.AttachInfo, error) {
	return send[api.AttachInfo](ctx, c, "/tasks/"+url.PathEscape(id)+"/shell", struct{}{})
}

// TerminalFrame asks for one rendered view of a task's pane.
//
// The size is the region the caller will draw into, which the daemon sets the
// pane to before capturing.
func (c *Client) TerminalFrame(ctx context.Context, id string, view api.TerminalView) (api.TerminalFrame, error) {
	return send[api.TerminalFrame](ctx, c, "/tasks/"+url.PathEscape(id)+"/terminal", view)
}

// SendTerminalInput delivers keys or typed text to a task's pane.
func (c *Client) SendTerminalInput(ctx context.Context, id string, input api.TerminalInput) error {
	_, err := send[struct{}](ctx, c, "/tasks/"+url.PathEscape(id)+"/terminal/input", input)
	return err
}

// Runtime performs one manual application-runtime action.
//
// Only the task and the action are named. Which services a task has, which
// Compose files define them, and what the command turns out to be are the
// daemon's to resolve, for the reason the shell endpoint takes nothing to
// execute.
func (c *Client) Runtime(ctx context.Context, id string, action api.RuntimeAction) (api.RuntimeStatus, error) {
	path := "/tasks/" + url.PathEscape(id) + "/runtime/" + url.PathEscape(string(action))
	if action == api.RuntimeDestroy {
		return send[api.RuntimeStatus](ctx, c, path, api.DestroyRuntime{Confirm: true})
	}
	return send[api.RuntimeStatus](ctx, c, path, struct{}{})
}

// Review performs one review action and returns what the task's review shows.
//
// Every action takes an empty body: what a user asked for is in the path, and
// the commands the response carries are the project's own, expanded by the
// daemon (ADR-036).
func (c *Client) Review(ctx context.Context, id string, action api.ReviewAction) (api.ReviewStatus, error) {
	path := "/tasks/" + url.PathEscape(id) + "/review/" + url.PathEscape(string(action))
	return send[api.ReviewStatus](ctx, c, path, struct{}{})
}

// RuntimeLogs returns the command that opens the task's normal Compose logs.
//
// The caller runs it with its own terminal, and checks it first: the daemon is
// the same user, and a client that ran whatever it was handed would be one
// nobody could reason about (FR-RUN-006).
func (c *Client) RuntimeLogs(ctx context.Context, id string) (api.RuntimeCommand, error) {
	return send[api.RuntimeCommand](ctx, c, "/tasks/"+url.PathEscape(id)+"/runtime/logs-info", struct{}{})
}

// Reconciliation returns the daemon's most recent reconciliation pass without
// asking it to run another.
func (c *Client) Reconciliation(ctx context.Context) (api.Reconciliation, error) {
	return fetch[api.Reconciliation](ctx, c, "/reconciliation")
}

// Reconcile asks the daemon to compare persisted state with the machine again.
//
// It changes nothing but observations: the daemon repairs, restarts, and adopts
// nothing, so this is safe to call from a screen a user is looking at.
func (c *Client) Reconcile(ctx context.Context) (api.Reconciliation, error) {
	return send[api.Reconciliation](ctx, c, "/reconciliation", struct{}{})
}

// CleanupPlan resolves what a task owns, removing nothing.
func (c *Client) CleanupPlan(ctx context.Context, id string) (api.CleanupPlan, error) {
	return send[api.CleanupPlan](ctx, c, "/tasks/"+url.PathEscape(id)+"/cleanup/plan", struct{}{})
}

// Cleanup removes the classes a selection names.
//
// The selection carries the token of the plan that was displayed and the exact
// warnings the user accepted, so the daemon can refuse a plan that has changed
// and a confirmation that no longer covers what is true (FR-CLEAN-003).
func (c *Client) Cleanup(
	ctx context.Context, id string, selection api.CleanupSelection,
) (api.CleanupStatus, error) {
	return send[api.CleanupStatus](ctx, c, "/tasks/"+url.PathEscape(id)+"/cleanup/execute", selection)
}

// Resume continues a task's recorded agent session.
func (c *Client) Resume(ctx context.Context, id string) (api.Task, error) {
	return send[api.Task](ctx, c, "/tasks/"+url.PathEscape(id)+"/resume", struct{}{})
}

// CreateDraft records a new task draft and creates nothing else.
//
// An imported Markdown brief is read by this process and sent as content: the
// daemon never opens a file a caller named.
func (c *Client) CreateDraft(ctx context.Context, request api.CreateDraft) (api.Task, error) {
	return send[api.Task](ctx, c, "/task-drafts", request)
}

// UpdateDraft replaces a draft's title, brief, and repository selection.
func (c *Client) UpdateDraft(ctx context.Context, id string, request api.UpdateDraft) (api.Task, error) {
	return replace[api.Task](ctx, c, "/task-drafts/"+url.PathEscape(id), request)
}

// PlanDraft resolves the draft's bases and proposes its branches and worktree
// paths, creating nothing.
func (c *Client) PlanDraft(ctx context.Context, id string) (api.DraftPlan, error) {
	return send[api.DraftPlan](ctx, c, "/task-drafts/"+url.PathEscape(id)+"/plan", struct{}{})
}

// LaunchDraft confirms a draft, carrying the fingerprint of the plan that was
// displayed so that what is created is what the user saw.
func (c *Client) LaunchDraft(ctx context.Context, id, fingerprint string) (api.Task, error) {
	return send[api.Task](ctx, c, "/task-drafts/"+url.PathEscape(id)+"/launch",
		api.LaunchDraft{Fingerprint: fingerprint})
}

// CancelDraft abandons a draft.
func (c *Client) CancelDraft(ctx context.Context, id string) (api.Task, error) {
	return remove[api.Task](ctx, c, "/task-drafts/"+url.PathEscape(id))
}

// fetch performs one GET and decodes the response.
func fetch[T any](ctx context.Context, c *Client, path string) (T, error) {
	var payload T

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	response, err := c.get(ctx, path, nil)
	if err != nil {
		return payload, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	if err := failed(response, path); err != nil {
		return payload, err
	}

	body := io.LimitReader(response.Body, maxResponseBody)
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return payload, fmt.Errorf("reading the daemon's response to %s: %w", path, err)
	}
	return payload, nil
}

// send performs one POST and decodes the response.
func send[T any](ctx context.Context, c *Client, path string, payload any) (T, error) {
	return submit[T](ctx, c, http.MethodPost, path, payload)
}

// replace performs one PUT and decodes the response.
func replace[T any](ctx context.Context, c *Client, path string, payload any) (T, error) {
	return submit[T](ctx, c, http.MethodPut, path, payload)
}

// remove performs one DELETE and decodes the response.
func remove[T any](ctx context.Context, c *Client, path string) (T, error) {
	return submit[T](ctx, c, http.MethodDelete, path, nil)
}

// submit performs one request that carries a body and decodes the response.
func submit[T any](ctx context.Context, c *Client, method, path string, payload any) (T, error) {
	var result T

	var reader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return result, fmt.Errorf("building a request for %s: %w", path, err)
		}
		reader = bytes.NewReader(body)
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method,
		"http://"+host+"/"+api.Version+path, reader)
	if err != nil {
		return result, fmt.Errorf("building a request for %s: %w", path, err)
	}
	if reader != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return result, c.describe(err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	if err := failed(response, path); err != nil {
		return result, err
	}
	// A no-content reply has nothing to decode, and decoding it anyway reports
	// the empty body as a broken one. That surfaced as an EOF error on every
	// keystroke sent to a focused terminal: the input endpoint answers 204, which
	// is a success the client was reading as a failure.
	if response.StatusCode == http.StatusNoContent {
		return result, nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(&result); err != nil {
		return result, fmt.Errorf("reading the daemon's response to %s: %w", path, err)
	}
	return result, nil
}

// get issues a GET request against the local API.
func (c *Client) get(ctx context.Context, path string, header http.Header) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/"+api.Version+path, nil)
	if err != nil {
		return nil, fmt.Errorf("building a request for %s: %w", path, err)
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, c.describe(err)
	}
	return response, nil
}

// describe turns a transport failure into something the user can act on.
//
// The common case is that no daemon is running, which is not an error the user
// caused: a client that reports "connection refused" makes them find that out
// for themselves.
func (c *Client) describe(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("%w on %s", ErrDaemonNotRunning, c.socket)
	}
	return fmt.Errorf("talking to the daemon on %s: %w", c.socket, err)
}

// failed converts an error response into a *StatusError.
func failed(response *http.Response, path string) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}

	status := &StatusError{Status: response.StatusCode, Path: path}

	var envelope struct {
		Error api.Error `json:"error"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
	if err == nil && json.Unmarshal(body, &envelope) == nil {
		status.Code = envelope.Error.Code
		status.Message = envelope.Error.Message
	}
	return status
}
