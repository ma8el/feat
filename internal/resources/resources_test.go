package resources

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner answers the observation commands from fixtures.
//
// It is keyed on the program and its first argument, because that is what
// distinguishes the four commands this package runs and nothing finer would add
// anything.
type fakeRunner struct {
	mu sync.Mutex

	outputs map[string]Output
	errs    map[string]error
	calls   [][]string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outputs: make(map[string]Output), errs: make(map[string]error)}
}

func (f *fakeRunner) answer(key, stdout string) *fakeRunner {
	f.outputs[key] = Output{Stdout: stdout}
	return f
}

func (f *fakeRunner) fail(key string, err error) *fakeRunner {
	f.errs[key] = err
	return f
}

func (f *fakeRunner) refuse(key, stderr string, code int) *fakeRunner {
	f.outputs[key] = Output{Stderr: stderr, ExitCode: code}
	return f
}

func (f *fakeRunner) Run(_ context.Context, invocation Invocation) (Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	call := append([]string{invocation.Program}, invocation.Arguments...)
	f.calls = append(f.calls, call)

	key := invocation.Program
	if len(invocation.Arguments) > 0 {
		key += " " + invocation.Arguments[0]
	}
	if err, failing := f.errs[key]; failing {
		return Output{}, err
	}
	return f.outputs[key], nil
}

// fakeMachine reports whatever a test arranged, including nothing.
type fakeMachine struct {
	machine Machine
	notes   []string
}

func (f fakeMachine) read(context.Context, Runner, string) (Machine, []string) {
	return f.machine, f.notes
}

// observer builds an observer with the machine reader replaced, so that a test
// is about attribution rather than about the machine it happens to run on.
func observer(t *testing.T, runner Runner, machine machineReader) *Observer {
	t.Helper()

	instance := New(Options{
		Runner:         runner,
		DiskPath:       "/state",
		ContainerLabel: "dev.feat.owner=feat",
		TaskLabel:      "dev.feat.task",
		KindLabel:      "dev.feat.kind",
	})
	instance.machine = machine
	return instance
}

const (
	agentTask = "7f3a1c2e-9b4d-4c81-8f2a-1d6b0c5e7a93"
	otherTask = "2c4e6a80-1b3d-4f52-8a7c-9e0d1f2a3b4c"
)

// containerList is what `docker ps` prints for two tasks' containers: one
// agent environment and one application service, plus a container of another
// task that must not be attributed to the first.
const containerList = "aaaa11112222\tfeat-agent-example-7f3a1c2e-dev\t" + agentTask + "\t\n" +
	"bbbb33334444\tfeat-example-7f3a1c2e-api-1\t" + agentTask + "\truntime\n" +
	"cccc55556666\tfeat-agent-example-2c4e6a80-dev\t" + otherTask + "\t\n"

// containerStats is what `docker stats --no-stream --format json` prints.
const containerStats = `{"ID":"aaaa11112222","Name":"feat-agent-example-7f3a1c2e-dev","CPUPerc":"12.50%","MemUsage":"2GiB / 7.653GiB"}
{"ID":"bbbb33334444","Name":"feat-example-7f3a1c2e-api-1","CPUPerc":"3.25%","MemUsage":"512MiB / 7.653GiB"}
{"ID":"cccc55556666","Name":"feat-agent-example-2c4e6a80-dev","CPUPerc":"99.00%","MemUsage":"1GiB / 7.653GiB"}`

// TestUsageIsAttributedToTheTaskThatOwnsIt checks that a container reaches the
// task whose label it carries, and no other.
//
// Attribution comes from Feat's own ownership labels rather than from a record,
// which is what lets an application's containers be found without the daemon
// remembering which ones Compose created for it.
func TestUsageIsAttributedToTheTaskThatOwnsIt(t *testing.T) {
	runner := newFakeRunner().
		answer("docker ps", containerList).
		answer("docker stats", containerStats).
		answer("ps -A", "")

	sample := observer(t, runner, fakeMachine{}).Observe(context.Background(), []Target{
		{Task: agentTask}, {Task: otherTask},
	})

	if len(sample.Tasks) != 2 {
		t.Fatalf("sampled %d tasks, want 2", len(sample.Tasks))
	}

	first := sample.Tasks[0]
	if len(first.Containers) != 2 {
		t.Fatalf("task %s owns %d containers, want 2: %+v", agentTask, len(first.Containers), first.Containers)
	}
	if !first.CPUKnown || first.CPUPercent != 15.75 {
		t.Errorf("task %s uses %.2f%% (known %v), want 15.75%%", agentTask, first.CPUPercent, first.CPUKnown)
	}
	if want := uint64(2<<30 + 512<<20); first.ContainerMemoryBytes != want {
		t.Errorf("task %s uses %d bytes in containers, want %d", agentTask, first.ContainerMemoryBytes, want)
	}

	// The kinds separate the agent's own environment from the application the
	// user is testing. Both belong to the task; only one is what they are
	// testing.
	kinds := map[string]string{}
	for _, container := range first.Containers {
		kinds[container.Name] = container.Kind
	}
	if kinds["feat-agent-example-7f3a1c2e-dev"] != KindAgent {
		t.Errorf("the agent's container is %q, want %q", kinds["feat-agent-example-7f3a1c2e-dev"], KindAgent)
	}
	if kinds["feat-example-7f3a1c2e-api-1"] != KindRuntime {
		t.Errorf("the application's container is %q, want %q", kinds["feat-example-7f3a1c2e-api-1"], KindRuntime)
	}

	// The other task's 99% must not have leaked into the first one's total.
	if second := sample.Tasks[1]; len(second.Containers) != 1 {
		t.Errorf("task %s owns %d containers, want 1", otherTask, len(second.Containers))
	}
}

// TestNothingIsMeasuredForATaskThatOwnsNothing checks that absence is reported
// as absence.
//
// A draft owns no container and no process. Reporting it as using nothing would
// be a measurement nobody took, which is the rule ADR-028 established for
// diagnostics and ADR-031 carried into the dashboard.
func TestNothingIsMeasuredForATaskThatOwnsNothing(t *testing.T) {
	runner := newFakeRunner().answer("docker ps", "").answer("ps -A", "")

	sample := observer(t, runner, fakeMachine{}).Observe(context.Background(), []Target{{Task: agentTask}})

	if len(sample.Tasks) != 1 {
		t.Fatalf("sampled %d tasks, want 1", len(sample.Tasks))
	}
	if sample.Tasks[0].Observed() {
		t.Errorf("a task owning nothing reports usage: %+v", sample.Tasks[0])
	}
}

// TestNothingRunningCostsNoContainerStats checks that the slow call is not made
// when there is nothing to make it about.
//
// `docker stats` takes between one and two seconds whatever it is given, so
// paying it to confirm an absence would be paying it on every machine where no
// task has a container at all (ADR-035).
func TestNothingRunningCostsNoContainerStats(t *testing.T) {
	runner := newFakeRunner().answer("docker ps", "").answer("ps -A", "")

	observer(t, runner, fakeMachine{}).Observe(context.Background(), []Target{{Task: agentTask}})

	for _, call := range runner.calls {
		if len(call) > 1 && call[0] == dockerProgram && call[1] == "stats" {
			t.Fatalf("asked for container statistics with no containers to ask about: %v", call)
		}
	}
}

// TestCollectionFailureDegradesGracefully is the rule that a metric never blocks
// a task.
//
// Every source is asked independently. A machine that cannot report its memory,
// a Docker that refuses, and a ps that is not there produce three notes and one
// sample, rather than one failure and no sample: the figures that could be taken
// are worth more than the ones that could not are worth losing.
func TestCollectionFailureDegradesGracefully(t *testing.T) {
	runner := newFakeRunner().
		refuse("docker ps", "Cannot connect to the Docker daemon", 1).
		fail("ps -A", errors.New("ps: cannot open the process table"))

	instance := observer(t, runner, fakeMachine{
		machine: Machine{Cores: 10, DiskPath: "/state", DiskTotal: 100, DiskAvailable: 40, DiskKnown: true},
		notes:   []string{"machine memory is unavailable: vm_stat reported nothing"},
	})

	sample := instance.Observe(context.Background(), []Target{{Task: agentTask, PIDs: []int{4321}}})

	if len(sample.Notes) != 3 {
		t.Fatalf("a sample with three broken sources carries %d notes, want 3: %v",
			len(sample.Notes), sample.Notes)
	}
	if !sample.Machine.DiskKnown {
		t.Error("the disk figures were discarded because memory could not be read")
	}
	if sample.Machine.MemoryKnown {
		t.Error("memory is reported as known when it could not be read")
	}
	if len(sample.Tasks) != 1 || sample.Tasks[0].Observed() {
		t.Errorf("a task reports usage nothing measured: %+v", sample.Tasks)
	}
	if sample.TakenAt.IsZero() {
		t.Error("a degraded sample has no time, so nothing can tell how old it is")
	}
}

// TestAnAbsentContainerRuntimeIsNotAFailure checks that a machine without Docker
// is a machine with no containers.
//
// Reporting it as an error would put a note on every sample for the life of the
// daemon, on a machine where nothing is wrong.
func TestAnAbsentContainerRuntimeIsNotAFailure(t *testing.T) {
	runner := newFakeRunner().
		fail("docker ps", ErrNotInstalled).
		answer("ps -A", "")

	sample := observer(t, runner, fakeMachine{}).Observe(context.Background(), []Target{{Task: agentTask}})

	for _, note := range sample.Notes {
		if strings.Contains(note, "container") {
			t.Errorf("an absent container runtime produced the note %q", note)
		}
	}
}

// TestProcessCPUIsADifferenceBetweenTwoSamples checks the one figure that cannot
// be read once.
//
// ps's own %cpu column means different things on the two supported platforms — a
// decaying recent average on macOS, a lifetime average on Linux — so Feat
// differences cumulative processor time instead. The first sample therefore has
// no processor figure and says so rather than reporting zero.
func TestProcessCPUIsADifferenceBetweenTwoSamples(t *testing.T) {
	runner := newFakeRunner().answer("docker ps", "")
	instance := observer(t, runner, fakeMachine{})

	base := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	instance.now = func() time.Time { return base }
	runner.answer("ps -A", "4321 1 8192 0:10.00\n4322 4321 4096 0:05.00\n")

	first := instance.Observe(context.Background(), []Target{{Task: agentTask, PIDs: []int{4321}}})
	if first.Tasks[0].CPUKnown {
		t.Error("the first sample reports processor use it could not have measured")
	}
	if want := uint64(12288 * 1024); first.Tasks[0].ProcessMemoryBytes != want {
		t.Errorf("the first sample reports %d bytes of process memory, want %d",
			first.Tasks[0].ProcessMemoryBytes, want)
	}
	if first.Tasks[0].Processes != 2 {
		t.Errorf("the first sample counts %d processes, want the pane and its child",
			first.Tasks[0].Processes)
	}

	// Ten seconds later, the subtree has used four more seconds of processor
	// time: three in the pane and one in its child, so forty percent of one core.
	instance.now = func() time.Time { return base.Add(10 * time.Second) }
	runner.answer("ps -A", "4321 1 8192 0:13.00\n4322 4321 4096 0:06.00\n")

	second := instance.Observe(context.Background(), []Target{{Task: agentTask, PIDs: []int{4321}}})
	if !second.Tasks[0].CPUKnown {
		t.Fatal("the second sample reports no processor use")
	}
	if got := second.Tasks[0].CPUPercent; got < 39.9 || got > 40.1 {
		t.Errorf("the subtree used %.2f%% of one core, want 40%%", got)
	}
}

// TestARecycledProcessIdentifierIsNotNegativeUse checks the one way a difference
// can go wrong: an identifier reused by a different process between samples.
func TestARecycledProcessIdentifierIsNotNegativeUse(t *testing.T) {
	runner := newFakeRunner().answer("docker ps", "")
	instance := observer(t, runner, fakeMachine{})

	base := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	instance.now = func() time.Time { return base }
	runner.answer("ps -A", "4321 1 8192 5:00.00\n")
	instance.Observe(context.Background(), []Target{{Task: agentTask, PIDs: []int{4321}}})

	instance.now = func() time.Time { return base.Add(10 * time.Second) }
	runner.answer("ps -A", "4321 1 8192 0:01.00\n")
	sample := instance.Observe(context.Background(), []Target{{Task: agentTask, PIDs: []int{4321}}})

	if sample.Tasks[0].CPUPercent < 0 {
		t.Errorf("a recycled identifier reports %.2f%% of a core", sample.Tasks[0].CPUPercent)
	}
	if sample.Tasks[0].CPUKnown {
		t.Errorf("a recycled identifier's history was reported as this process's use: %+v", sample.Tasks[0])
	}
}

// TestTheProcessTreeIsWalkedFromThePane checks that a task's work is what its
// terminal started, not the terminal itself.
//
// Counting the pane alone would report a task compiling its project as using
// nothing at all, because the pane's own shell is asleep while its child works.
func TestTheProcessTreeIsWalkedFromThePane(t *testing.T) {
	runner := newFakeRunner().answer("docker ps", "").answer("ps -A",
		// pane, its shell, the shell's child, and an unrelated process.
		"4321 1 1024 0:01.00\n4322 4321 2048 0:01.00\n4323 4322 4096 0:01.00\n9999 1 8192 0:01.00\n")

	sample := observer(t, runner, fakeMachine{}).
		Observe(context.Background(), []Target{{Task: agentTask, PIDs: []int{4321}}})

	if got := sample.Tasks[0].Processes; got != 3 {
		t.Errorf("the subtree holds %d processes, want 3", got)
	}
	if want := uint64((1024 + 2048 + 4096) * 1024); sample.Tasks[0].ProcessMemoryBytes != want {
		t.Errorf("the subtree uses %d bytes, want %d", sample.Tasks[0].ProcessMemoryBytes, want)
	}
}

// TestByteSizesAreReadInTheUnitsTheyWerePrintedIn pins the parsing of what the
// container runtime prints.
//
// Reading "1.5GB" as 1.5 GiB would overstate a task's memory by seven percent
// for ever, and nothing would say so.
func TestByteSizesAreReadInTheUnitsTheyWerePrintedIn(t *testing.T) {
	for value, want := range map[string]uint64{
		"512B":     512,
		"540KiB":   540 * 1024,
		"2GiB":     2 << 30,
		"7.653GiB": 8_217_346_179,
		"1.5GB":    1_500_000_000,
		"":         0,
		"unknown":  0,
	} {
		if got := parseBytes(value); got != want {
			t.Errorf("parseBytes(%q) = %d, want %d", value, got, want)
		}
	}

	// Only the used half is read. The limit beside it is the container runtime's
	// own, which on macOS is its virtual machine's rather than the machine's.
	if got := memoryUsed("540KiB / 7.653GiB"); got != "540KiB" {
		t.Errorf("memoryUsed read %q, want the used half alone", got)
	}
}

// TestProcessTimesAreReadInBothPlatformsFormats pins the other parsing this
// package depends on.
//
// Linux prints [dd-]hh:mm:ss and macOS prints [dd-][hh:]mm:ss.cc, so the fields
// are read from the right rather than counted from the left.
func TestProcessTimesAreReadInBothPlatformsFormats(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"0:05.00":    5 * time.Second,
		"147:46.35":  147*time.Minute + 46*time.Second + 350*time.Millisecond,
		"01:02:03":   time.Hour + 2*time.Minute + 3*time.Second,
		"2-01:02:03": 48*time.Hour + time.Hour + 2*time.Minute + 3*time.Second,
	} {
		got, ok := parseProcessTime(value)
		if !ok || got != want {
			t.Errorf("parseProcessTime(%q) = %s, %v; want %s, true", value, got, ok, want)
		}
	}
	if _, ok := parseProcessTime("not a time"); ok {
		t.Error("parseProcessTime accepted something that is not a time")
	}
}
