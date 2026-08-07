package daemon

import "github.com/ma8el/feat/internal/execution"

// executionState is what an execution adapter reported.
//
// It is aliased so that reconciliation and cleanup read in the daemon's own
// terms rather than naming the adapter package in every signature. The adapter
// the name refers to is still the only thing that produces one.
type executionState = execution.State
