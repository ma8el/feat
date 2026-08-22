package domain

import (
	"errors"
	"strings"
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

// TestPortsAreHeldUntilNothingIsLeft is what makes several tasks able to run one
// application.
//
// A host port is global to the machine, so an allocation is only worth anything
// while it is held against every other task — and holding one after the
// containers are gone would leak a port nothing is bound to. Both halves are
// checked here because they are one rule: the runtime's own state decides.
func TestPortsAreHeldUntilNothingIsLeft(t *testing.T) {
	held := inputs("feat-example-7f3a1c2e")
	held.Allocations = []PortAllocation{
		{Service: "api", ContainerPort: 8000, HostPort: 21000, Protocol: "tcp"},
	}
	runtime := NewRuntimeEnvironment(held)

	// A runtime nothing has created holds nothing: the ports are chosen again,
	// against what the other tasks hold, when there is something to publish.
	if !runtime.ReleasePorts(origin) {
		t.Error("a runtime that has created nothing kept a host port no container is bound to")
	}
	runtime.Allocations = held.Allocations

	if err := runtime.Observe(RuntimeRunning, HealthUnknown, origin); err != nil {
		t.Fatalf("observing: %v", err)
	}
	if runtime.ReleasePorts(origin) {
		t.Fatal("a running runtime released the port its containers are bound to")
	}
	if _, allocated := runtime.Allocation("api"); !allocated {
		t.Fatalf("the runtime lost its allocation: %+v", runtime.Allocations)
	}

	if err := runtime.Observe(RuntimeAbsent, HealthUnknown, origin); err != nil {
		t.Fatalf("observing: %v", err)
	}
	if !runtime.ReleasePorts(origin) {
		t.Fatal("a runtime with nothing in it kept its host port, which no other task can then have")
	}
	if runtime.ReleasePorts(origin) {
		t.Error("releasing nothing reported a change, which would rewrite the record on every poll")
	}
}

// TestAGeneratedVariableNamesItsServiceSafely pins the rendering configuration
// refuses collisions against.
//
// The rule is lossy on purpose — an environment variable name has no room for
// the dots and hyphens a Compose service name allows — and both halves of it
// have to be one rule: the daemon generates these names and configuration
// refuses two services that would produce the same one.
func TestAGeneratedVariableNamesItsServiceSafely(t *testing.T) {
	for service, want := range map[string]string{
		"api":     "FEAT_HOST_PORT_API",
		"web-app": "FEAT_HOST_PORT_WEB_APP",
		"web.app": "FEAT_HOST_PORT_WEB_APP",
		"Api2":    "FEAT_HOST_PORT_API2",
	} {
		if got := PortVariable(service); got != want {
			t.Errorf("the port variable of %q is %q, want %q", service, got, want)
		}
	}
	if got := URLVariable("web-app"); got != "FEAT_HOST_URL_WEB_APP" {
		t.Errorf("the URL variable of web-app is %q", got)
	}
}

// TestAGeneratedVariableSaysTheAddressIsTheHostsPins the prefix rather than the
// rendering, because the prefix is the whole of what a user sees at the moment
// this value is misused.
//
// The variable carries a published port, which belongs to the host's network
// namespace: read inside a container that address is the container's own
// loopback, and with Feat's default binding the host's port is not reachable
// from a container at all. Under FEAT_URL_ nothing about the name said so, and
// a service that called a sibling at ${FEAT_URL_api} got a connection refused
// against itself — a failure with no message naming the cause. Documentation is
// not present where the string is typed; the name is (G4-08).
func TestAGeneratedVariableSaysTheAddressIsTheHosts(t *testing.T) {
	for _, generated := range []string{PortVariable("api"), URLVariable("api")} {
		if !strings.HasPrefix(generated, "FEAT_HOST_") {
			t.Errorf("the generated variable %q does not say the address is the host's, so a service "+
				"reading it into a sibling call has nothing telling it not to", generated)
		}
	}
	if got, want := PortVariable("api"), "FEAT_HOST_PORT_API"; got != want {
		t.Errorf("the port variable of api is %q, want %q", got, want)
	}
	if got, want := URLVariable("api"), "FEAT_HOST_URL_API"; got != want {
		t.Errorf("the URL variable of api is %q, want %q", got, want)
	}
}

// TestAnAllocationSaysWhereItIsReached keeps the address a user is given, and
// the one a service is told, the same value.
//
// The loopback addresses are said as localhost, which is the name that reaches
// them and the one a user types; an address of this machine that is not loopback
// is said as itself, because it is where the service is and nowhere else is.
func TestAnAllocationSaysWhereItIsReached(t *testing.T) {
	for name, testCase := range map[string]struct {
		allocation  PortAllocation
		address     string
		url         string
		addressable bool
	}{
		"the loopback address": {
			allocation:  PortAllocation{HostPort: 21001, Protocol: "tcp", HostIP: "127.0.0.1"},
			address:     "localhost:21001",
			url:         "http://localhost:21001",
			addressable: true,
		},
		"its IPv6 counterpart": {
			allocation:  PortAllocation{HostPort: 21002, Protocol: "tcp", HostIP: "::1"},
			address:     "localhost:21002",
			url:         "http://localhost:21002",
			addressable: true,
		},
		// A binding on every interface is reached at localhost from here, which
		// is the half of it this address answers. What else such a binding
		// allows is said where the project's configuration is printed.
		"every address": {
			allocation:  PortAllocation{HostPort: 21003, Protocol: "tcp", HostIP: "0.0.0.0"},
			address:     "localhost:21003",
			url:         "http://localhost:21003",
			addressable: true,
		},
		// One address of the machine, which is not this machine's loopback: the
		// service is there and a user told localhost could not reach it.
		"one the project chose": {
			allocation:  PortAllocation{HostPort: 21004, Protocol: "tcp", HostIP: "192.168.64.7"},
			address:     "192.168.64.7:21004",
			url:         "http://192.168.64.7:21004",
			addressable: true,
		},
		// A record written before Feat had a bind address of its own. Its
		// containers were given every interface, which is reached at localhost.
		"a record from before there was one": {
			allocation:  PortAllocation{HostPort: 21000, Protocol: "tcp"},
			address:     "localhost:21000",
			url:         "http://localhost:21000",
			addressable: true,
		},
		"a datagram port": {
			allocation: PortAllocation{HostPort: 21005, Protocol: "udp", HostIP: "127.0.0.1"},
			address:    "localhost:21005",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := testCase.allocation.Address(); got != testCase.address {
				t.Errorf("the address is %q, want %q", got, testCase.address)
			}
			url, addressable := testCase.allocation.URL()
			if addressable != testCase.addressable || url != testCase.url {
				t.Errorf("the URL is %q (%t), want %q (%t)",
					url, addressable, testCase.url, testCase.addressable)
			}
		})
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

// TestEveryChangeToARuntimeAdvancesItsGeneration is what lets a reader of this
// record tell it apart from one that has been changed back into its own shape.
//
// A destroy and the create after it leave the identity, the state, the health
// and the port numbers exactly as they were, so nothing about the record's
// contents says it is a different one — and an answer about the record before
// the pair, applied after it, releases ports that are bound (ADR-065 evidence
// 16). The generation is the only thing that can say so, and it can only say it
// if every change moves it.
//
// The clock is deliberately held still throughout, because that is the case a
// timestamp cannot answer and this exists to answer: the daemon reads its clock
// once per operation, and two operations can share a reading.
func TestEveryChangeToARuntimeAdvancesItsGeneration(t *testing.T) {
	held := inputs("feat-example-7f3a1c2e")
	held.Allocations = []PortAllocation{
		{Service: "api", ContainerPort: 8000, HostPort: 21000, Protocol: "tcp"},
	}
	runtime := NewRuntimeEnvironment(held)

	seen := map[uint64]string{}
	moved := func(what string) {
		t.Helper()
		if before, repeated := seen[runtime.Generation]; repeated {
			t.Errorf("%s left the generation at %d, which %s already had, so the two are "+
				"indistinguishable to anything holding an answer about the earlier one",
				what, runtime.Generation, before)
		}
		seen[runtime.Generation] = what
	}
	moved("a new runtime")

	if err := runtime.ReplaceInputs(inputs("feat-example-renamed"), origin); err != nil {
		t.Fatalf("re-resolving the inputs: %v", err)
	}
	moved("re-resolved inputs")

	if changed := runtime.ResolveProvenance(
		[]ServiceProvenance{{Service: "api", Repositories: []string{"core"}}}, origin); !changed {
		t.Fatal("resolving a provenance the record did not have reported no change")
	}
	moved("a resolved provenance")

	if err := runtime.Observe(RuntimeRunning, HealthHealthy, origin); err != nil {
		t.Fatalf("observing: %v", err)
	}
	moved("an observation")

	runtime.ObserveResources(nil, []string{"feat-example-renamed_default"}, nil, origin)
	moved("observed resources")

	runtime.Allocations = held.Allocations
	if err := runtime.Observe(RuntimeAbsent, HealthUnknown, origin); err != nil {
		t.Fatalf("observing: %v", err)
	}
	moved("an observation of nothing")

	if !runtime.ReleasePorts(origin) {
		t.Fatal("an absent runtime kept its host ports")
	}
	moved("released ports")

	// And a call that changes nothing does not move it, or every poll of an
	// unchanged runtime would look like somebody acting on it.
	unchanged := runtime.Generation
	if runtime.ReleasePorts(origin) || runtime.ResolveProvenance(runtime.Provenance, origin) {
		t.Fatal("a call that reported no change changed something")
	}
	if runtime.Generation != unchanged {
		t.Errorf("the generation moved to %d without a change, and a poll that saw it move would "+
			"discard an answer that is still good", runtime.Generation)
	}
}
