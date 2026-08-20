package resources

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/integrationtest"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to observe this machine for real", integrationtest.Env)
	}
}

// TestRealMachineIsReadable checks the figures this build takes from the machine
// it is running on.
//
// The parsers are pinned by unit tests against fixtures, which prove that Feat
// reads a given output correctly. This proves the other half: that the output is
// what Feat expects. Both matter, and slice 8 found out the hard way that only
// the second one catches a tool that answers somewhere else.
func TestRealMachineIsReadable(t *testing.T) {
	requireIntegration(t)

	state, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolving a path to measure: %v", err)
	}

	machine, notes := hostMachine{}.read(context.Background(), HostRunner{}, state)
	if len(notes) != 0 {
		t.Fatalf("this %s machine could not be read: %v", runtime.GOOS, notes)
	}

	if machine.Cores <= 0 {
		t.Errorf("the machine reports %d cores", machine.Cores)
	}
	if !machine.LoadKnown || machine.Load1 < 0 {
		t.Errorf("the machine's load is %v (known %v)", machine.Load1, machine.LoadKnown)
	}
	if !machine.MemoryKnown || machine.MemoryTotal == 0 {
		t.Errorf("the machine reports %d bytes of memory", machine.MemoryTotal)
	}
	if machine.MemoryAvailable > machine.MemoryTotal {
		t.Errorf("the machine reports %d bytes available of %d total",
			machine.MemoryAvailable, machine.MemoryTotal)
	}
	if !machine.DiskKnown || machine.DiskTotal == 0 {
		t.Errorf("the filesystem holding %s reports %d bytes", state, machine.DiskTotal)
	}
	if machine.DiskAvailable > machine.DiskTotal {
		t.Errorf("the filesystem reports %d bytes available of %d total",
			machine.DiskAvailable, machine.DiskTotal)
	}
}

// spin burns processor time in this process for a period.
//
// It is deliberately wall-clock bounded rather than a fixed number of
// iterations, so that a slow or a loaded machine spends the same time being
// measured as a fast one.
func spin(d time.Duration) {
	deadline := time.Now().Add(d)
	total := 0
	for time.Now().Before(deadline) {
		total++
	}
	_ = total
}

// TestRealProcessUsageIsMeasured checks the process half against real ps output
// and a real process.
//
// The test's own process is the subtree, so there is certainly something to
// find, and its memory is certainly not zero. The processor figure needs two
// samples, which is the point of taking two.
//
// How long it must spin is a statement about ps rather than about Feat. Linux
// reports cumulative processor time in whole seconds and macOS in centiseconds,
// so the 200ms this test first used was a tenth of what Linux can represent at
// all and reported zero there — correctly (ADR-035 evidence 16). It therefore
// spins past the coarser platform's resolution and keeps going, bounded, rather
// than for one period chosen on the platform with the finer clock.
func TestRealProcessUsageIsMeasured(t *testing.T) {
	requireIntegration(t)

	observer := New(Options{})
	self := os.Getpid()

	first := observer.Observe(context.Background(), []Target{{Task: "self", PIDs: []int{self}}})
	if first.Tasks[0].Processes == 0 {
		t.Fatalf("ps did not report this test's own process: %v", first.Notes)
	}
	if first.Tasks[0].ProcessMemoryBytes == 0 {
		t.Errorf("this test's own process is reported as using no memory")
	}
	if first.Tasks[0].CPUKnown {
		t.Errorf("the first sample reports processor use it could not have measured")
	}

	// A full second of processor time raises a whole-second counter by at least
	// one whatever the offset it started at, so one round is enough on an idle
	// machine. The rounds exist for a shared runner, where wall-clock spinning
	// buys less than its own duration in processor time.
	var (
		second  Sample
		rounds  int
		giveUp  = time.Now().Add(30 * time.Second)
		spinFor = 1500 * time.Millisecond
	)
	for {
		spin(spinFor)
		rounds++

		second = observer.Observe(context.Background(), []Target{{Task: "self", PIDs: []int{self}}})
		if second.Tasks[0].CPUKnown && second.Tasks[0].CPUPercent > 0 {
			break
		}
		if time.Now().After(giveUp) {
			t.Fatalf("after %d rounds of %s spinning, this process reports known=%v %.2f%% of a core: %v",
				rounds, spinFor, second.Tasks[0].CPUKnown, second.Tasks[0].CPUPercent, second.Notes)
		}
	}
}

// TestRealContainerUsageIsMeasured checks the container half against real
// Docker.
//
// It creates a container carrying Feat's ownership labels and requires the
// observer to find it, attribute it to the task the label names, and report a
// memory figure — which is the whole path from `docker ps` through `docker
// stats` to a per-task total. The parsers cannot be proved right against a
// fixture alone: what Docker prints, and in which units, is a statement about
// Docker.
func TestRealContainerUsageIsMeasured(t *testing.T) {
	requireIntegration(t)
	if _, err := exec.LookPath(dockerProgram); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "docker is not installed")
	}

	const (
		task = "3b9f6c11-0d7a-4c2e-8f51-6a4d2b8e0c37"
		name = "feat-resources-integration"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	remove := exec.CommandContext(ctx, dockerProgram, "rm", "--force", name)
	_ = remove.Run()

	create := exec.CommandContext(ctx, dockerProgram, "run", "--detach", "--name", name,
		"--label", "dev.feat.owner=feat",
		"--label", "dev.feat.task="+task,
		"--label", "dev.feat.kind=runtime",
		"alpine", "sh", "-c", "while true; do sleep 1; done")
	if output, err := create.CombinedOutput(); err != nil {
		// A `docker run` that fails is the sharpest form of the gate's old
		// silence: Docker is installed, the probe above passed, and the proof
		// still vanished. It is a failure when the run demanded Docker.
		integrationtest.Unavailable(t, integrationtest.Docker,
			"this machine cannot run a container to measure: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command(dockerProgram, "rm", "--force", name).Run()
	})

	observer := New(Options{
		ContainerLabel: "dev.feat.owner=feat",
		TaskLabel:      "dev.feat.task",
		KindLabel:      "dev.feat.kind",
	})
	sample := observer.Observe(ctx, []Target{{Task: task}})

	usage := sample.Tasks[0]
	if len(usage.Containers) != 1 {
		t.Fatalf("the observer found %d containers for the task, want 1: %+v\nnotes: %v",
			len(usage.Containers), usage.Containers, sample.Notes)
	}
	if usage.Containers[0].Name != name {
		t.Errorf("the container is %q, want %q", usage.Containers[0].Name, name)
	}
	if usage.Containers[0].Kind != KindRuntime {
		t.Errorf("the container is %q, want %q", usage.Containers[0].Kind, KindRuntime)
	}
	if !usage.MemoryKnown || usage.ContainerMemoryBytes == 0 {
		t.Errorf("a running container reports %d bytes of memory (known %v). "+
			"Either docker stats printed a size in units this build does not read, or it was never asked",
			usage.ContainerMemoryBytes, usage.MemoryKnown)
	}
	if !usage.CPUKnown {
		t.Errorf("a running container reports no processor use")
	}
}
