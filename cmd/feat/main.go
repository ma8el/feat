// Command feat is the process entrypoint for the Feat development control
// plane. One binary provides the daemon, the TUI client, and the CLI clients;
// see docs/06-technical-architecture.md for the process model.
//
// This file stays deliberately thin. The command tree lives in internal/cli so
// that it can be constructed and exercised by tests without a process boundary.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ma8el/feat/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
