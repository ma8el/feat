package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
)

// reaching declares the fixture's managed service reachable, which is what asks
// Feat for a host port of its own.
func reaching(fixture string) string {
	return strings.Replace(fixture,
		"      container_path: /srv/api\n      services:\n        - api\n",
		"      container_path: /srv/api\n      services:\n        - api\n"+
			"      reachable:\n        - api\n", 1)
}

// ranged narrows the fixture's host port range, so that a test about a range
// running out does not have to fill a thousand ports to reach the end of one.
func ranged(fixture, ports string) string {
	return strings.Replace(fixture,
		`  project_name_template: "feat-{project_id}-{task_key}"`,
		"  project_name_template: \"feat-{project_id}-{task_key}\"\n  port_range: \""+ports+"\"", 1)
}

// binding sets the host address Feat publishes an allocated port on, for a
// project that wants something other than this machine alone.
func binding(fixture, address string) string {
	return strings.Replace(fixture,
		`  project_name_template: "feat-{project_id}-{task_key}"`,
		"  project_name_template: \"feat-{project_id}-{task_key}\"\n  bind_address: \""+address+"\"", 1)
}

// publishing writes the project's own Compose file, publishing the managed
// service on a fixed host port.
//
// The fixed port is the whole problem: it is global to the machine, so the
// second task to want it cannot start. What Feat does with it is replace it.
func publishing(t *testing.T, arranged *drafting, body string) {
	t.Helper()
	writeCompose(t, filepath.Join(arranged.env.Home, "repos", "app", "compose.yml"), body)
}

// oneReachableService is the ordinary shape: a service the project publishes on
// a port it wrote down itself.
const oneReachableService = `services:
  api:
    image: example/api
    ports:
      - "4000:4000"
`

// allocationsOf returns what a task holds, reloaded from storage.
func allocationsOf(t *testing.T, arranged *drafting, id domain.TaskID) []domain.PortAllocation {
	t.Helper()

	task := arranged.reload(t, id)
	if task.Runtime == nil {
		t.Fatalf("task %s has no runtime record", id)
	}
	return task.Runtime.Allocations
}

// overrideOf reads the generated override of one task.
func overrideOf(t *testing.T, arranged *drafting, id domain.TaskID) string {
	t.Helper()

	task := arranged.reload(t, id)
	document, err := os.ReadFile(task.Runtime.GeneratedOverridePath)
	if err != nil {
		t.Fatalf("reading the generated override of %s: %v", id, err)
	}
	return string(document)
}

// reservationPause is how long the task holding the window open waits for
// another task's reservation to arrive in it.
//
// It is only ever waited out when the allocation is correct. A task that can
// reach the range while another task's choice is unrecorded gets there in the
// time it takes to read a project's configuration; one that cannot is waiting
// for the allocator, which is the property under test. Generous for that reason:
// the cost of being too long is this one test taking two seconds longer, and the
// cost of being too short is reporting a window shut while it is open.
const reservationPause = 2 * time.Second

// TestEachTaskIsPublishedOnItsOwnHostPort is the first acceptance criterion of
// this slice, at the daemon.
//
// Two tasks of one project run the same application. A host port is global to
// the machine, so the fixed one their Compose file writes down could serve one
// of them; each is therefore given a port of its own, and neither publishes the
// one the project wrote.
//
// They start concurrently, because sequentially is how this passed while the
// invariant it names was broken (G6-04). A port is chosen by reading what every
// other task has recorded, so two tasks can only be given the same one when the
// second reads between the first's choice and the first's record — and two
// starts one after the other never do.
//
// The daemon's own seam is what puts them there: `reserving` runs in that gap,
// and the first task waits in it while the second runs its whole reservation.
// With the allocator held across the choice and the record, the second cannot
// reach the range at all until the first has let go, and the wait is what that
// looks like from here.
func TestEachTaskIsPublishedOnItsOwnHostPort(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, oneReachableService)

	first := arranged.launched(t, "Add a rate limit")
	second := arranged.launched(t, "Add an export job")
	arranged.answerFor(first, "running", "Up 2 seconds")
	arranged.answerFor(second, "running", "Up 2 seconds")

	// Each task performs one action, so each announces its choice exactly once.
	chosen := map[domain.TaskID]chan struct{}{
		first.ID:  make(chan struct{}),
		second.ID: make(chan struct{}),
	}
	arranged.service.reserving = func(id domain.TaskID) {
		close(chosen[id])
		if id != first.ID {
			return
		}
		select {
		case <-chosen[second.ID]:
			// The window is open: the second task read the range while this
			// task's choice existed nowhere but in memory.
		case <-time.After(reservationPause):
			// The window is shut: the second task cannot read the range until
			// this one has written its choice down and released the allocator.
		}
	}

	starting := make(chan error, 2)
	start := func(id domain.TaskID) {
		_, err := arranged.service.Runtime(context.Background(), id, api.RuntimeStart)
		starting <- err
	}

	go start(first.ID)
	select {
	case <-chosen[first.ID]:
	case err := <-starting:
		t.Fatalf("the first task finished without reserving a host port: %v", err)
	}
	go start(second.ID)

	for range 2 {
		if err := <-starting; err != nil {
			t.Fatalf("starting two tasks at once: %v", err)
		}
	}

	held := map[domain.TaskID]int{}
	for _, task := range []*domain.Task{first, second} {
		allocations := allocationsOf(t, arranged, task.ID)
		if len(allocations) != 1 {
			t.Fatalf("task %s holds %d host ports, want one for its one reachable service: %+v",
				task.Key(), len(allocations), allocations)
		}
		allocation := allocations[0]
		if allocation.Service != "api" || allocation.ContainerPort != 4000 {
			t.Errorf("task %s published %+v, and the project's own file publishes api's 4000",
				task.Key(), allocation)
		}
		if allocation.HostPort == 4000 {
			t.Errorf("task %s kept the fixed host port the project wrote, which one task at a time "+
				"can hold", task.Key())
		}
		held[task.ID] = allocation.HostPort
	}
	if held[first.ID] == held[second.ID] {
		t.Fatalf("both tasks were given host port %d, so the second could not start", held[first.ID])
	}
	// Both reservations went through the seam, or this proved nothing about the
	// window they were meant to meet in.
	for _, task := range []*domain.Task{first, second} {
		select {
		case <-chosen[task.ID]:
		default:
			t.Fatalf("task %s never reached the allocator", task.Key())
		}
	}

	// And each task's own document publishes its own port and no other.
	for _, task := range []*domain.Task{first, second} {
		document := overrideOf(t, arranged, task.ID)
		if !strings.Contains(document, `published: "`+strconv.Itoa(held[task.ID])+`"`) {
			t.Errorf("the override of task %s does not publish its allocated port:\n%s",
				task.Key(), document)
		}
		if strings.Contains(document, `published: "4000"`) {
			t.Errorf("the override of task %s republishes the project's fixed port:\n%s",
				task.Key(), document)
		}
	}
}

// TestThreeTasksStartedAtOnceEachHoldTheirOwnPorts is the acceptance criterion in
// its own words, without a seam to arrange the moment.
//
// Three tasks running at once is what the criterion says and what the reference
// project met the defect with (ADR-065), so the ordinary path is driven
// concurrently here, with nothing holding anything open: whatever interleaving
// the scheduler produces has to leave three tasks able to bind what they were
// given. It is the weaker of the two — a race it does not happen to hit still
// passes — and it is the one that needs no test hook to stay true.
func TestThreeTasksStartedAtOnceEachHoldTheirOwnPorts(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, oneReachableService)

	tasks := []*domain.Task{
		arranged.launched(t, "Add a rate limit"),
		arranged.launched(t, "Add an export job"),
		arranged.launched(t, "Add a webhook"),
	}
	for _, task := range tasks {
		arranged.answerFor(task, "running", "Up 2 seconds")
	}

	starting := make(chan error, len(tasks))
	for _, task := range tasks {
		go func() {
			_, err := arranged.service.Runtime(context.Background(), task.ID, api.RuntimeStart)
			starting <- err
		}()
	}
	for range tasks {
		if err := <-starting; err != nil {
			t.Fatalf("starting three tasks at once: %v", err)
		}
	}

	holder := map[int]domain.TaskKey{}
	for _, task := range tasks {
		allocations := allocationsOf(t, arranged, task.ID)
		if len(allocations) != 1 {
			t.Fatalf("task %s holds %d host ports, want one: %+v",
				task.Key(), len(allocations), allocations)
		}
		port := allocations[0].HostPort
		if other, taken := holder[port]; taken {
			t.Fatalf("tasks %s and %s were both given host port %d, so only one of them can start",
				other, task.Key(), port)
		}
		holder[port] = task.Key()
	}
}

// TestEveryManagedServiceIsToldWhereItsSiblingsAre is the second acceptance
// criterion at the daemon: an application finds its own task's services.
//
// The address differs per task, so nothing baked into an image and nothing
// written in the project's own file can be right for more than one of them. It
// reaches the services twice over, and both are needed: in the container's
// environment, for a service that reads FEAT_URL_ itself, and in the environment
// of the Compose process, for a project that maps it to its own name with a
// "${...}" — which is the only way for a service whose framework exposes
// variables under a prefix of its own.
func TestEveryManagedServiceIsToldWhereItsSiblingsAre(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, oneReachableService)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	port := allocationsOf(t, arranged, task.ID)[0].HostPort
	address := "http://localhost:" + strconv.Itoa(port)

	document := overrideOf(t, arranged, task.ID)
	for _, required := range []string{
		`"FEAT_URL_API": "` + address + `"`,
		`"FEAT_PORT_API": "` + strconv.Itoa(port) + `"`,
	} {
		if !strings.Contains(document, required) {
			t.Errorf("the generated override does not carry %s:\n%s", required, document)
		}
	}

	var interpolable bool
	for _, invocation := range arranged.runtimes.Invocations() {
		for _, entry := range invocation.Environment {
			if entry == "FEAT_URL_API="+address {
				interpolable = true
			}
		}
	}
	if !interpolable {
		t.Error("no Compose command carried the allocated address, so a project cannot interpolate " +
			"it into its own files under its own name")
	}
}

// TestNoManagedServicePublishesAPortFeatDidNotAllocate is the fifth acceptance
// criterion.
//
// A service the project did not declare reachable publishes nothing, and so does
// a service Compose started because a managed one depends on it. Both would
// otherwise carry a fixed port to the host, which is the same one-task-per-
// machine failure as the entry point's, arriving one service over (ADR-034
// evidence 12).
func TestNoManagedServicePublishesAPortFeatDidNotAllocate(t *testing.T) {
	// admin is managed and not reachable; postgres is not managed at all and is
	// there because a managed service depends on it.
	managing := strings.Replace(reaching(runtimeFixture),
		"      services:\n        - api\n      reachable:",
		"      services:\n        - api\n        - admin\n      reachable:", 1)

	arranged := arrangeConfigured(t, managing)
	publishing(t, arranged, `services:
  api:
    image: example/api
    ports:
      - "4000:4000"
  admin:
    image: example/admin
    ports:
      - "4100:4100"
  postgres:
    image: example/postgres
    ports:
      - "4200:4200"
`)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.runtimes.
		Answer("config --services", "api\nadmin\npostgres").
		Answer("up --detach api admin", "")
	arranged.act(t, task.ID, api.RuntimeStart)

	document := overrideOf(t, arranged, task.ID)
	if count := strings.Count(document, "ports: !reset []"); count != 2 {
		t.Errorf("%d services publish nothing, want the managed service that is not reachable and "+
			"the dependency:\n%s", count, document)
	}
	for _, fixed := range []string{`"4100"`, `"4200"`} {
		if strings.Contains(document, fixed) {
			t.Errorf("the generated override keeps the fixed host port %s:\n%s", fixed, document)
		}
	}
}

// TestADestroyReleasesThePortsItHeld is the third acceptance criterion.
//
// A destroyed runtime holds nothing, so its ports belong to whichever task asks
// next — and until it is destroyed they belong to it, whatever else happens.
func TestADestroyReleasesThePortsItHeld(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, oneReachableService)

	first := arranged.launched(t, "Add a rate limit")
	arranged.answerFor(first, "running", "Up 2 seconds")
	arranged.act(t, first.ID, api.RuntimeStart)
	held := allocationsOf(t, arranged, first.ID)[0].HostPort

	// A second task takes a different port while the first holds its own.
	second := arranged.launched(t, "Add an export job")
	arranged.answerFor(second, "running", "Up 2 seconds")
	arranged.act(t, second.ID, api.RuntimeStart)
	if taken := allocationsOf(t, arranged, second.ID)[0].HostPort; taken == held {
		t.Fatalf("the second task was given host port %d while the first task's containers hold it", taken)
	}

	// The destroy gives it back: `ps` answers with nothing, so the runtime is
	// observed absent, which is what a released port means.
	arranged.runtimes.Answer("ps --all --format json", "")
	arranged.act(t, first.ID, api.RuntimeDestroy)

	if allocations := allocationsOf(t, arranged, first.ID); len(allocations) != 0 {
		t.Fatalf("a destroyed runtime still holds %+v", allocations)
	}

	// And the released port is given to the next task that asks.
	third := arranged.launched(t, "Add a webhook")
	arranged.answerFor(third, "running", "Up 2 seconds")
	arranged.act(t, third.ID, api.RuntimeStart)
	if taken := allocationsOf(t, arranged, third.ID)[0].HostPort; taken != held {
		t.Errorf("the third task was given host port %d, and %d was released by the destroy: a port "+
			"nothing holds should be reused rather than leaked", taken, held)
	}
}

// TestAllocatedPortsSurviveEveryLaterAction covers the other half of the third
// criterion: a port is not reallocated while the runtime holding it exists.
//
// The recorded inputs win while there are resources, for the reason every other
// input does — a re-resolved port would move a task's address out from under the
// containers bound to it — and here that rule is what keeps the port held
// against every other task as well.
func TestAllocatedPortsSurviveEveryLaterAction(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, oneReachableService)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeCreate)
	held := allocationsOf(t, arranged, task.ID)[0].HostPort

	for _, action := range []api.RuntimeAction{
		api.RuntimeStart, api.RuntimeObserve, api.RuntimeStop, api.RuntimeStart,
	} {
		arranged.act(t, task.ID, action)
		if now := allocationsOf(t, arranged, task.ID)[0].HostPort; now != held {
			t.Fatalf("after %s the task holds host port %d, and its containers were created bound "+
				"to %d", action, now, held)
		}
	}
}

// TestAStaleObservationDoesNotGiveAwayATasksPorts is the defect three tasks of
// the reference project found.
//
// The poller lists the tasks outside any lock and asks Docker about each in
// turn, so a create that finishes while it is asking leaves it holding an answer
// about the world as it was before: nothing existed, therefore the runtime is
// absent. Written down, that releases the host ports the create had just
// allocated — while the containers created with them are bound to those ports —
// and the next task is given them.
//
// The state alone would have survived it, because the next poll corrects the
// state. A released port is not corrected by anything.
func TestAStaleObservationDoesNotGiveAwayATasksPorts(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, oneReachableService)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")

	// The copy a poll would have started from: this task as it was before
	// anything was created for it, which is what a status of a new task leaves
	// behind and what the poller's own listing holds while Docker is answering.
	arranged.runtimes.Answer("ps --all --format json", "")
	arranged.act(t, task.ID, api.RuntimeObserve)
	stale := arranged.reload(t, task.ID)

	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeCreate)
	held := allocationsOf(t, arranged, task.ID)
	if len(held) != 1 {
		t.Fatalf("the create allocated %+v, want one host port", held)
	}

	// The poll's own answer, from before the create: this Compose project holds
	// nothing at all.
	arranged.runtimes.Answer("ps --all --format json", "")
	if _, err := arranged.service.observeRuntime(context.Background(), stale); err != nil {
		t.Fatalf("observing: %v", err)
	}

	after := allocationsOf(t, arranged, task.ID)
	if len(after) != len(held) || after[0].HostPort != held[0].HostPort {
		t.Errorf("an observation taken before the create released the ports it allocated: %+v, "+
			"want %+v", after, held)
	}
	if state := arranged.reload(t, task.ID).Runtime.State; state == domain.RuntimeAbsent {
		t.Errorf("the runtime is recorded absent, and its containers were created after the " +
			"observation that said so")
	}
}

// TestAStaleObservationDoesNotSurviveADestroyAndRecreate is the same defect
// arriving through the guard against it.
//
// A destroy and the create after it leave a record that looks exactly like the
// one the poll started from: the same Compose project, running again, one
// allocation — and the same port number, because the destroy releases 21000 and
// the create takes the lowest free port, which is 21000. Every field the guard
// used to compare therefore matched, and the answer from before the pair was
// applied to the record after it, recording absent and releasing a port the new
// containers are bound to (G3-05, ADR-065 evidence 16).
//
// The identical shape is asserted rather than assumed. It is the whole of what
// this test is about: a guard that compared the record's contents — the ports,
// their count, the state, the moment on a clock that has not moved — would pass
// every one of those comparisons here.
func TestAStaleObservationDoesNotSurviveADestroyAndRecreate(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, oneReachableService)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeCreate)

	// What a poll holds while Docker is answering: this task, running, holding
	// the port its containers were created with.
	stale := arranged.reload(t, task.ID)

	// And what the user does while it waits. The destroy is observed through a
	// `ps` that answers nothing, which is what makes the runtime absent and its
	// port free.
	arranged.runtimes.Answer("ps --all --format json", "")
	arranged.act(t, task.ID, api.RuntimeDestroy)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeCreate)

	recreated := arranged.reload(t, task.ID).Runtime
	if recreated.Identity != stale.Runtime.Identity || recreated.State != stale.Runtime.State ||
		recreated.Health != stale.Runtime.Health ||
		len(recreated.Allocations) != len(stale.Runtime.Allocations) {
		t.Fatalf("the re-created runtime is %s/%s/%s with %d allocations and the destroyed one was "+
			"%s/%s/%s with %d, so this is not the collision the guard is blind to",
			recreated.Identity, recreated.State, recreated.Health, len(recreated.Allocations),
			stale.Runtime.Identity, stale.Runtime.State, stale.Runtime.Health,
			len(stale.Runtime.Allocations))
	}
	if recreated.Allocations[0] != stale.Runtime.Allocations[0] {
		t.Fatalf("the re-created runtime holds %+v and the destroyed one held %+v: this test needs the "+
			"port the destroy released to be the one the create took back",
			recreated.Allocations[0], stale.Runtime.Allocations[0])
	}

	// The poll's own answer, from before the destroy: this Compose project holds
	// nothing at all.
	arranged.runtimes.Answer("ps --all --format json", "")
	if _, err := arranged.service.observeRuntime(context.Background(), stale); err != nil {
		t.Fatalf("observing: %v", err)
	}

	if held := allocationsOf(t, arranged, task.ID); len(held) != 1 {
		t.Errorf("an observation taken before the destroy released the host ports the re-created "+
			"containers are bound to: %+v", held)
	}
	if state := arranged.reload(t, task.ID).Runtime.State; state == domain.RuntimeAbsent {
		t.Errorf("the runtime is recorded absent, and it was created again after the observation " +
			"that said so")
	}
}

// TestAnExhaustedRangeNamesWhatHoldsIt is the fourth acceptance criterion.
//
// What a user does about an exhausted range is destroy a runtime they have
// finished with or widen the range, so the message names the tasks holding it
// and both of those actions. A thousand port numbers would be a fact rather than
// a diagnosis.
func TestAnExhaustedRangeNamesWhatHoldsIt(t *testing.T) {
	arranged := arrangeConfigured(t, ranged(reaching(runtimeFixture), "21000-21000"))
	publishing(t, arranged, oneReachableService)

	first := arranged.launched(t, "Add a rate limit")
	arranged.answerFor(first, "running", "Up 2 seconds")
	arranged.act(t, first.ID, api.RuntimeStart)

	second := arranged.launched(t, "Add an export job")
	arranged.answerFor(second, "running", "Up 2 seconds")

	_, err := arranged.service.Runtime(context.Background(), second.ID, api.RuntimeCreate)
	if err == nil {
		t.Fatal("a second task was created out of a range holding one port")
	}
	message := err.Error()
	for _, required := range []string{
		"21000-21000",                   // the range that ran out
		first.Key().String(),            // and what is holding it
		"runtime.port_range",            // one way out
		"Destroy the runtime of a task", // and the other
	} {
		if !strings.Contains(message, required) {
			t.Errorf("the exhausted range does not say %q: %s", required, message)
		}
	}
}

// TestAnAllocatedPortIsBoundWhereTheTaskSaysItIs is G4-01, end to end.
//
// The defect was not that the wrong address was chosen; it was that no address
// was written at all. Compose reads a publication with no host_ip as every
// interface, so omitting the key published every reachable service of every
// running task on every network the machine was joined to — and on the Docker
// bridge, which is one task's agent able to dial another task's database — while
// the override comment, FEAT_URL_ and the address the task reported all said
// localhost. Nothing in the product printed 0.0.0.0, so nothing contradicted it.
//
// Each case checks the two halves against each other: the address the generated
// document binds, and the address the record says the service is reached at. A
// test that only read one of them is the test that was there.
func TestAnAllocatedPortIsBoundWhereTheTaskSaysItIs(t *testing.T) {
	for name, testCase := range map[string]struct {
		configure func(string) string
		compose   string
		bound     string
		reached   string
	}{
		// The ordinary publication, and the one both in-repo fixtures and the
		// product's own goldens contain: the project's file names no address, so
		// Feat's default decides, and the default is this machine alone.
		"a publication naming no address": {
			configure: func(fixture string) string { return fixture },
			compose:   oneReachableService,
			bound:     "127.0.0.1",
			reached:   "localhost",
		},
		// The project said who reaches its service. Feat replaces the host port,
		// because a fixed one is one task at a time, and leaves the address
		// alone, because who may reach a service is not Feat's to widen.
		"a publication naming one": {
			configure: func(fixture string) string { return fixture },
			compose: `services:
  api:
    image: example/api
    ports:
      - "192.168.64.7:4000:4000"
`,
			bound:   "192.168.64.7",
			reached: "192.168.64.7",
		},
		// The user with a phone on the same network who wants it to reach a dev
		// server, having said so. The binding widens; the address a user is given
		// stays the name that reaches it from here.
		"a project that asked for every interface": {
			configure: func(fixture string) string { return binding(fixture, "0.0.0.0") },
			compose:   oneReachableService,
			bound:     "0.0.0.0",
			reached:   "localhost",
		},
	} {
		t.Run(name, func(t *testing.T) {
			arranged := arrangeConfigured(t, testCase.configure(reaching(runtimeFixture)))
			publishing(t, arranged, testCase.compose)

			task := arranged.launched(t)
			arranged.answerFor(task, "running", "Up 2 seconds")
			arranged.act(t, task.ID, api.RuntimeStart)

			allocations := allocationsOf(t, arranged, task.ID)
			if len(allocations) != 1 {
				t.Fatalf("the task holds %d host ports, want one: %+v", len(allocations), allocations)
			}
			allocation := allocations[0]

			if allocation.HostIP != testCase.bound {
				t.Errorf("the task records its service bound on %q, want %q: the record is what every "+
					"surface reads, so an address missing from it is missing from all of them",
					allocation.HostIP, testCase.bound)
			}
			address := testCase.reached + ":" + strconv.Itoa(allocation.HostPort)
			if allocation.Address() != address {
				t.Errorf("the task says its service is reached at %q, want %q",
					allocation.Address(), address)
			}

			document := overrideOf(t, arranged, task.ID)
			if bound := `host_ip: "` + testCase.bound + `"`; !strings.Contains(document, bound) {
				t.Errorf("the generated override does not carry %s, so Compose binds every interface "+
					"the machine has:\n%s", bound, document)
			}
			// The comment and the variable are the two places the address was
			// claimed while nothing bound it. They have to agree with the binding
			// above, or one of them is the sentence this test exists to remove.
			if comment := "reached at " + address; !strings.Contains(document, comment) {
				t.Errorf("the generated override does not say %q beside the port it publishes:\n%s",
					comment, document)
			}
			if url := `"FEAT_URL_API": "http://` + address + `"`; !strings.Contains(document, url) {
				t.Errorf("the generated override does not carry %s:\n%s", url, document)
			}
		})
	}
}

// TestNoTaskIsPublishedOnEveryInterfaceByOmission is the same defect stated as
// the property that failed, rather than as three arrangements.
//
// A publication with no host_ip is the widest binding there is, chosen by saying
// nothing — which is how this shipped. Counting the addresses against the ports
// fails whether the key is dropped for one service or for all of them, and it
// fails for a service nobody wrote a case for.
func TestNoTaskIsPublishedOnEveryInterfaceByOmission(t *testing.T) {
	managing := strings.Replace(reaching(runtimeFixture),
		"      services:\n        - api\n      reachable:\n        - api\n",
		"      services:\n        - api\n        - admin\n      reachable:\n        - api\n        - admin\n", 1)

	arranged := arrangeConfigured(t, managing)
	publishing(t, arranged, `services:
  api:
    image: example/api
    ports:
      - "4000:4000"
  admin:
    image: example/admin
    ports:
      - "4100:4100"
`)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.runtimes.
		Answer("config --services", "api\nadmin").
		Answer("up --detach api admin", "")
	arranged.act(t, task.ID, api.RuntimeStart)

	document := overrideOf(t, arranged, task.ID)
	published := strings.Count(document, "\n        published: ")
	bound := strings.Count(document, "\n        host_ip: ")
	if published != 2 {
		t.Fatalf("%d ports are published, want the 2 reachable services:\n%s", published, document)
	}
	if published != bound {
		t.Errorf("%d ports are published and %d name an address: a publication with no host_ip is "+
			"bound on every interface the machine has\n%s", published, bound, document)
	}

	for _, allocation := range allocationsOf(t, arranged, task.ID) {
		if allocation.HostIP == "" {
			t.Errorf("the allocation for %s records no address, so nothing Feat says about it can be "+
				"checked against what Compose bound: %+v", allocation.Service, allocation)
		}
	}
}

// TestAReachableServiceWithNoReadablePortSaysSo is the case where the project
// asks for something Feat must not work out for itself.
//
// An interpolated publication is a value Feat may not resolve — resolving one
// means reading the environment files the security model forbids — so the
// service is published on nothing at all. Every way of being wrong here is
// silent otherwise: the services start, the application serves, and the address
// the user expected answers nothing.
func TestAReachableServiceWithNoReadablePortSaysSo(t *testing.T) {
	arranged := arrangeConfigured(t, reaching(runtimeFixture))
	publishing(t, arranged, `services:
  api:
    image: example/api
    ports:
      - "${API_PORT}:4000"
`)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	result := arranged.act(t, task.ID, api.RuntimeStart)

	if allocations := allocationsOf(t, arranged, task.ID); len(allocations) != 0 {
		t.Fatalf("Feat allocated a port for a publication it could not read: %+v", allocations)
	}
	if !notesMention(result.Notes, "declared reachable") {
		t.Errorf("the start said nothing about the service that will not be reachable: %v", result.Notes)
	}
	if !notesMention(result.Notes, "api") {
		t.Errorf("the note does not name the service: %v", result.Notes)
	}
}
