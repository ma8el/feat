package resources

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotInstalled reports an executable this host does not have.
//
// It is a distinct error because an absent Docker is not a broken Docker: a
// machine with no container runtime has no containers to attribute, which is an
// answer rather than a failure.
var ErrNotInstalled = errors.New("not installed on this host")

// Sample is one observation of the machine and of the tasks Feat manages.
//
// Nothing here is derived from what Feat asked for. Every figure is something a
// tool reported, and a figure no tool reported is absent rather than zero: a
// dashboard that printed "0 MiB" where it had not looked would be making a
// claim, which is the rule ADR-028 established for diagnostics.
type Sample struct {
	// Machine is what the whole host reported.
	Machine Machine
	// Tasks are the per-task aggregates, in the order the targets were given.
	Tasks []TaskUsage
	// Notes are what could not be collected, in terms a user can act on. A
	// sample with notes is still a sample: one unreadable source never discards
	// the others.
	Notes []string
	// TakenAt is when the sample was taken.
	TakenAt time.Time
	// Took is how long collecting it needed. The daemon uses it as the floor for
	// the next interval, because a container runtime that answers slowly must not
	// make samples pile up.
	Took time.Duration
}

// Machine is what the whole host reported.
type Machine struct {
	// Cores is how many processors the machine has.
	Cores int

	// Load1, Load5, and Load15 are the run-queue averages, valid when LoadKnown
	// is true.
	//
	// Feat reports load rather than a utilisation percentage because a per-core
	// percentage is not obtainable on macOS without cgo, and one measure on both
	// platforms is worth more than two that look alike and are not (ADR-035).
	Load1, Load5, Load15 float64
	LoadKnown            bool

	// MemoryTotal and MemoryAvailable are in bytes, valid when MemoryKnown is
	// true.
	MemoryTotal, MemoryAvailable uint64
	MemoryKnown                  bool

	// DiskPath is the filesystem the figures below describe: the state directory,
	// because that is where Feat's worktrees, control workspaces, and generated
	// overrides live.
	DiskPath                 string
	DiskTotal, DiskAvailable uint64
	DiskKnown                bool
}

// TaskUsage is what one task's containers and processes reported.
//
// Container and process figures are kept apart as well as summed. On macOS a
// container's memory is memory inside the container runtime's own virtual
// machine, measured against that machine's limit rather than the host's, so
// presenting the sum as a share of host memory would be a claim nothing
// measured (ADR-035, evidence 3).
type TaskUsage struct {
	// Task is the identifier the caller attributes this usage to.
	Task string

	// CPUPercent is the total, in percent of one core, valid when CPUKnown is
	// true. A container using two cores fully reports 200.
	CPUPercent float64
	CPUKnown   bool

	// MemoryBytes is the total resident memory, valid when MemoryKnown is true.
	MemoryBytes uint64
	MemoryKnown bool

	// ContainerMemoryBytes and ProcessMemoryBytes are the two halves of it.
	ContainerMemoryBytes uint64
	ProcessMemoryBytes   uint64

	// Containers are the task's own containers, whatever they run.
	Containers []ContainerUsage
	// Processes counts the host processes attributed to the task, including the
	// descendants of its terminal panes.
	Processes int
}

// Observed reports whether anything at all was measured for the task.
func (u TaskUsage) Observed() bool { return u.CPUKnown || u.MemoryKnown }

// ContainerUsage is one container Feat owns.
type ContainerUsage struct {
	// ID and Name are what the container runtime called it.
	ID, Name string
	// Kind separates an application service from the agent's own environment.
	// Both are Feat's, and only one of them is what the user is testing.
	Kind string
	// CPUPercent is in percent of one core, as the container runtime reported it.
	CPUPercent float64
	// MemoryBytes is resident memory, as the container runtime reported it.
	MemoryBytes uint64
}

// Container kinds.
const (
	// KindRuntime is a container of a task's application runtime.
	KindRuntime = "runtime"
	// KindAgent is the container an agent session runs in.
	//
	// The agent's generated override carries no kind label, so a Feat-owned
	// container without one is the agent's. Inferring it rather than requiring
	// the label keeps a container an older build created classifiable, which a
	// new label could not do (ADR-035).
	KindAgent = "agent"
)

// Target is one task whose usage the caller wants aggregated.
//
// The caller resolves it, because deciding what a task owns needs persistent
// state and this package has none.
type Target struct {
	// Task is the identifier usage is attributed to. It is opaque here.
	Task string
	// PIDs are the host processes whose subtrees belong to the task, which for
	// v0 are its tmux panes.
	PIDs []int
}

// Invocation is a host command.
type Invocation struct {
	// Program is the executable, resolved on the daemon's own path.
	Program string
	// Arguments are its arguments, each already one vector element.
	Arguments []string
}

// Output is what a command produced.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Succeeded reports whether the command exited cleanly.
func (o Output) Succeeded() bool { return o.ExitCode == 0 }

// Runner executes host commands for the observer.
//
// It is an interface for the reason git.Runner and runtime.Runner are: a test
// can arrange a Docker that is missing, one that answers slowly, and one that
// reports a container in a state this machine is not in.
type Runner interface {
	// Run executes the command and returns what it produced. A command that ran
	// and exited non-zero is not an error; only one that could not be started at
	// all fails, with ErrNotInstalled when the executable is absent.
	Run(ctx context.Context, invocation Invocation) (Output, error)
}

// Options configure an observer.
type Options struct {
	// Runner executes the host commands. A nil value runs them for real.
	Runner Runner
	// DiskPath is the filesystem whose availability is reported. It is normally
	// the state directory.
	DiskPath string
	// ContainerLabel selects the containers Feat owns, as a label filter such as
	// "dev.feat.owner=feat".
	ContainerLabel string
	// TaskLabel is the label whose value attributes a container to a task.
	TaskLabel string
	// KindLabel is the label whose value separates an application runtime from
	// the agent's environment.
	KindLabel string
	// Now supplies the current time. A nil value uses the wall clock.
	Now func() time.Time
}

// Observer samples the machine and the tasks it is given.
//
// It is stateful for one reason: process CPU is a difference between two
// readings of cumulative processor time, which is the only definition that means
// the same thing on macOS and on Linux. The first sample therefore reports
// process memory and no process CPU, and says so through CPUKnown rather than
// by reporting zero.
type Observer struct {
	runner  Runner
	disk    string
	labels  labelNames
	now     func() time.Time
	machine machineReader

	mu sync.Mutex
	// previous is the last reading of each process's cumulative processor time.
	previous processTimes
	// previousAt is when that reading was taken.
	previousAt time.Time
}

// labelNames are the labels that identify and attribute a Feat-owned container.
type labelNames struct {
	selector string
	task     string
	kind     string
}

// New creates an observer.
func New(opts Options) *Observer {
	runner := opts.Runner
	if runner == nil {
		runner = HostRunner{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Observer{
		runner: runner,
		disk:   opts.DiskPath,
		labels: labelNames{
			selector: opts.ContainerLabel,
			task:     opts.TaskLabel,
			kind:     opts.KindLabel,
		},
		now:     now,
		machine: hostMachine{},
	}
}

// Observe takes one sample.
//
// It never returns an error. Every source is asked independently, and one that
// cannot answer becomes a note rather than a failed sample: the acceptance
// criterion is that collection failure degrades gracefully, and a caller that
// had to handle an error would eventually handle it by showing nothing at all.
func (o *Observer) Observe(ctx context.Context, targets []Target) Sample {
	started := o.now()
	sample := Sample{TakenAt: started}

	machine, notes := o.machine.read(ctx, o.runner, o.disk)
	sample.Machine = machine
	sample.Notes = append(sample.Notes, notes...)

	containers, note := o.containers(ctx)
	if note != "" {
		sample.Notes = append(sample.Notes, note)
	}
	processes, note := o.processes(ctx, started)
	if note != "" {
		sample.Notes = append(sample.Notes, note)
	}

	sample.Tasks = aggregate(targets, containers, processes)
	sample.Took = o.now().Sub(started)
	return sample
}

// aggregate sums one task's containers and processes.
func aggregate(targets []Target, containers map[string][]ContainerUsage, processes *processTree) []TaskUsage {
	usage := make([]TaskUsage, 0, len(targets))

	for _, target := range targets {
		task := TaskUsage{Task: target.Task}

		owned := append([]ContainerUsage(nil), containers[target.Task]...)
		sort.Slice(owned, func(i, j int) bool { return owned[i].Name < owned[j].Name })
		for _, container := range owned {
			task.CPUPercent += container.CPUPercent
			task.ContainerMemoryBytes += container.MemoryBytes
			task.CPUKnown = true
			task.MemoryKnown = true
		}
		task.Containers = owned

		if processes != nil {
			for _, pid := range target.PIDs {
				subtree := processes.subtree(pid)
				task.Processes += subtree.count
				task.ProcessMemoryBytes += subtree.memory
				if subtree.count > 0 {
					task.MemoryKnown = true
				}
				if subtree.cpuKnown {
					task.CPUPercent += subtree.cpu
					task.CPUKnown = true
				}
			}
		}

		task.MemoryBytes = task.ContainerMemoryBytes + task.ProcessMemoryBytes
		usage = append(usage, task)
	}
	return usage
}
