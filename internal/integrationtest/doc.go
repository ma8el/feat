// Package integrationtest states which real tools an integration run demands.
//
// The opt-in tier — the tests gated on FEAT_INTEGRATION — is the only place
// Feat proves anything about Git, tmux, Docker, or a container boundary. Every
// file in that tier used to downgrade a missing or unanswering tool to t.Skip,
// and non-verbose `go test` prints "ok" for a package whose selected tests all
// skipped. So `make check`, the gate CLAUDE.md declares work complete
// against, passed identically on a machine that proved everything and one that
// proved nothing: stopping Docker Desktop silently removed every
// container-boundary proof and the gate still went green.
//
// A run therefore names the tools it demands, in FEAT_INTEGRATION_REQUIRE. A
// demanded tool that is missing or unanswering fails the run rather than
// skipping it. An undemanded one still skips, which is how macOS CI runs the
// tier against a runner image with no Docker daemon, and how nobody is made to
// install an authenticated Claude Code to run the tier at all.
//
// The demand is a declaration rather than a discovery. A probe that decided for
// itself what this machine ought to have would be reporting on the machine
// again, which is the defect; naming the tools in the Makefile and in the
// workflow means a run that proves less than the last one has to say so in a
// reviewable file.
//
// It contains no runtime code and is not listed in
// docs/06-technical-architecture.md. It is a plain package rather than a test
// file because eight packages share it, and it reaches os/exec through none of
// its own code: probing a tool belongs to the test that needs it, under the
// process-execution rule in .golangci.yml.
package integrationtest
