package domain

import (
	"errors"
	"testing"
)

// inputs is a plausible set of runtime inputs.
func inputs(identity string) RuntimeInputs {
	return RuntimeInputs{
		Provider: "compose",
		Identity: identity,
		Composition: []RuntimeSource{{
			Repository: "core",
			Directory:  "/repos/example/core",
			Files:      []string{"/repos/example/core/compose.yaml"},
		}},
		GeneratedIncludePath:  "/state/runtime/example/7f3a1c2e/compose.include.yaml",
		GeneratedOverridePath: "/state/runtime/example/7f3a1c2e/compose.override.yaml",
		Services:              []string{"api"},
	}
}

// TestANewRuntimeHasObservedNothing keeps a record of what Feat intends from
// claiming to be a record of what exists.
func TestANewRuntimeHasObservedNothing(t *testing.T) {
	runtime := NewRuntimeEnvironment(inputs("feat-example-7f3a1c2e"))

	if runtime.State != RuntimeAbsent {
		t.Errorf("a runtime nothing has created is %q, want absent", runtime.State)
	}
	if runtime.Health != HealthUnknown {
		t.Errorf("its health is %q, and nothing has looked", runtime.Health)
	}
	if err := runtime.Validate(TaskID("7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c")); err != nil {
		t.Errorf("a new runtime does not validate: %v", err)
	}
}

// TestRecordedInputsChangeOnlyWhileNothingExists is what keeps an action from
// reaching a different Compose project than the one it was told about.
//
// A user may edit their configuration at any time. While the task owns
// resources, the inputs those resources were created from are what a stop or a
// destroy must use; once nothing is left, re-resolving can orphan nothing and
// the fixed configuration is the one the user wants.
func TestRecordedInputsChangeOnlyWhileNothingExists(t *testing.T) {
	runtime := NewRuntimeEnvironment(inputs("feat-example-7f3a1c2e"))

	if err := runtime.ReplaceInputs(inputs("renamed-7f3a1c2e"), origin); err != nil {
		t.Fatalf("an absent runtime refused new inputs: %v", err)
	}
	if runtime.Identity != "renamed-7f3a1c2e" {
		t.Errorf("the identity is %q, want the re-resolved one", runtime.Identity)
	}

	if err := runtime.Observe(RuntimeRunning, HealthUnknown, origin); err != nil {
		t.Fatalf("observing: %v", err)
	}
	err := runtime.ReplaceInputs(inputs("renamed-again"), origin)
	if !errors.Is(err, ErrInvariant) {
		t.Fatalf("a running runtime accepted different inputs: %v", err)
	}
	if runtime.Identity != "renamed-7f3a1c2e" {
		t.Errorf("the refused change was applied anyway: %q", runtime.Identity)
	}
}

// TestObservedResourcesAreSeparateFromState keeps the two questions apart.
//
// The state says whether the application is up; the resources say what exists
// because of it, which is what a user needs in order to reach it and what
// cleanup needs in order to say what it would retain.
func TestObservedResourcesAreSeparateFromState(t *testing.T) {
	runtime := NewRuntimeEnvironment(inputs("feat-example-7f3a1c2e"))

	runtime.ObserveResources(
		[]PortAssignment{{Service: "api", ContainerPort: 8000, HostPort: 8080}},
		[]string{"feat-example-7f3a1c2e_default"},
		[]string{"feat-example-7f3a1c2e_pgdata"},
		origin,
	)

	if runtime.State != RuntimeAbsent {
		t.Errorf("recording resources changed the state to %q", runtime.State)
	}
	if len(runtime.Ports) != 1 || len(runtime.Networks) != 1 || len(runtime.Volumes) != 1 {
		t.Fatalf("the observed resources were not recorded: %+v", runtime)
	}
	if runtime.ObservedAt.IsZero() {
		t.Error("an observation without a time cannot be aged out")
	}
}
