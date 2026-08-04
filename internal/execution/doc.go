// Package execution defines the interface behind which host-native and
// devcontainer agent execution are interchangeable.
//
// The conceptual contract is in docs/06-technical-architecture.md:
//
//	Validate(ctx) error
//	Prepare(ctx, task) error
//	Command(ctx, spec) (*exec.Cmd, error)
//	Shell(ctx, task) (*exec.Cmd, error)
//	Observe(ctx, task) (ExecutionState, error)
//	Destroy(ctx, task) error
//
// Agent execution and application runtime are separate concepts even when both
// use Docker Compose. This package covers only where the agent runs;
// internal/runtime covers the application under development.
//
// Command specifications are argument vectors, never interpolated shell
// strings.
//
// Delivered by slice 8, with the host implementation in slice 14.
package execution
