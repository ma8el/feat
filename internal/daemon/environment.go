package daemon

import "github.com/ma8el/feat/internal/execution"

// executionState is what an execution adapter reported. It is aliased so
// reconciliation and cleanup read in the daemon's own terms rather than naming
// the adapter package in every signature.
type executionState = execution.State
