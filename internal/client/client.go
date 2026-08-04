package client

import (
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

// Tasks returns every task of every project.
func (c *Client) Tasks(ctx context.Context) ([]api.Task, error) {
	return fetch[[]api.Task](ctx, c, "/tasks")
}

// Task returns one task, addressed by task identifier alone.
func (c *Client) Task(ctx context.Context, id string) (api.Task, error) {
	return fetch[api.Task](ctx, c, "/tasks/"+url.PathEscape(id))
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
