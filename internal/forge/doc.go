// Package forge opens merge requests on a Git forge.
//
// It mirrors internal/agent: this package holds the provider-neutral interface,
// and each adapter beneath it holds one forge's own flags and output parsing, so
// adding a forge changes no caller (ADR-070).
//
// Three properties are the point of the boundary:
//
//   - every credentialed provider call is made here, on the trusted host, with
//     the authentication the user already has. The agent environment receives no
//     provider token;
//   - an adapter builds an argument vector and never a command string, so a
//     title the agent wrote is one argument rather than something a shell
//     re-reads;
//   - an adapter records nothing. It returns where the request can be read and
//     the daemon writes that down, because the daemon is the only writer of
//     persistent state (ADR-008).
//
// It does not own the push, which stays in internal/git; the sequencing across a
// task's repositories, which is internal/daemon's; or the words, which the agent
// writes and the user approves before anything reaches here (ADR-070, ADR-073).
package forge
