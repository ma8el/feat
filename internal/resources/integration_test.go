package resources

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

const envIntegration = "FEAT_INTEGRATION"

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to observe this machine for real", envIntegration)
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

// TestRealProcessUsageIsMeasured checks the process half against real ps output
// and a real process.
//
// The test's own process is the subtree, so there is certainly something to
// find, and its memory is certainly not zero. The processor figure needs two
// samples, which is the point of taking two.
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

	// Burn a measurable amount of processor time between the samples.
	deadline := time.Now().Add(200 * time.Millisecond)
	total := 0
	for time.Now().Before(deadline) {
		total++
	}
	_ = total

	second := observer.Observe(context.Background(), []Target{{Task: "self", PIDs: []int{self}}})
	if !second.Tasks[0].CPUKnown {
		t.Fatalf("the second sample reports no processor use: %v", second.Notes)
	}
	if second.Tasks[0].CPUPercent <= 0 {
		t.Errorf("a process that just spun for 200ms reports %.2f%% of a core",
			second.Tasks[0].CPUPercent)
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
		t.Skip("docker is not installed")
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
		t.Skipf("this machine cannot run a container to measure: %v\n%s", err, output)
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
