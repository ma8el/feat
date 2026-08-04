// Package client is the Unix-socket API client used by the CLI and the TUI.
//
// It dials the daemon socket, performs HTTP/JSON requests, and consumes the SSE
// event stream.
//
// Dependency rule: client depends on the internal/api wire types and nothing
// else of Feat's. It must not import the daemon, the store, or any adapter;
// clients never touch persistent state directly.
//
// Starting a daemon is deliberately not part of this package. internal/daemon
// knows how to start one and internal/cli decides when to, which keeps process
// execution in the packages where the argument-vector and capability rules are
// stated and tested.
package client
