// Package client is the Unix-socket API client used by the CLI and the TUI.
//
// It dials the daemon socket, performs HTTP/JSON requests, and consumes the SSE
// event stream. Opening the dashboard starts the daemon in the background when
// the socket is absent.
//
// Dependency rule: client depends on internal/api DTOs and internal/domain. It
// must not import the daemon, the store, or any adapter; clients never touch
// persistent state directly.
//
// Delivered by slice 2.
package client
