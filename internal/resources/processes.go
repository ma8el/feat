package resources

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// psProgram lists the host's processes.
const psProgram = "ps"

// processTimes is the last reading of each process's cumulative processor time.
type processTimes map[int]time.Duration

// processNode is one host process.
type processNode struct {
	pid, ppid int
	// memory is resident set size in bytes.
	memory uint64
	// cpu is percent of one core since the previous sample, valid when cpuKnown
	// is true.
	cpu      float64
	cpuKnown bool
}

// processTree is the host's processes, indexed for subtree walks.
type processTree struct {
	nodes    map[int]*processNode
	children map[int][]int
}

// subtreeUsage is what one process and its descendants are using.
type subtreeUsage struct {
	count    int
	memory   uint64
	cpu      float64
	cpuKnown bool
}

// processes reads the host's processes and works out what each has used since
// the previous sample.
//
// Processor use is a difference between two readings of cumulative time rather
// than the `%cpu` column, because that column means different things on the two
// supported platforms: a decaying recent average on macOS and a lifetime average
// on Linux. A difference means the same thing on both, and it is the only one of
// the three that answers "what is this task doing now" (ADR-035, evidence 6).
func (o *Observer) processes(ctx context.Context, at time.Time) (*processTree, string) {
	output, err := o.runner.Run(ctx, Invocation{
		Program:   psProgram,
		Arguments: []string{"-A", "-o", "pid=,ppid=,rss=,time="},
	})
	switch {
	case errors.Is(err, ErrNotInstalled):
		return nil, "process usage is unavailable: " + psProgram + " is not installed on this host"
	case err != nil:
		return nil, "process usage is unavailable: " + err.Error()
	case !output.Succeeded():
		return nil, "process usage is unavailable: " + firstLine(output.Stderr, output.Stdout)
	}

	tree, times := parseProcesses(output.Stdout)

	o.mu.Lock()
	previous, previousAt := o.previous, o.previousAt
	o.previous, o.previousAt = times, at
	o.mu.Unlock()

	elapsed := at.Sub(previousAt)
	if previous == nil || elapsed <= 0 {
		// The first sample has nothing to difference against, so every process
		// reports memory and no processor use. Saying that through cpuKnown rather
		// than reporting zero is the same rule the rest of this package follows.
		return tree, ""
	}
	for pid, node := range tree.nodes {
		was, seen := previous[pid]
		if !seen {
			continue
		}
		used := times[pid] - was
		if used < 0 {
			// The identifier was reused by a different process between samples.
			// Its history belongs to something that no longer exists, so this
			// sample reports no processor use for it rather than a negative one.
			continue
		}
		node.cpu = 100 * used.Seconds() / elapsed.Seconds()
		node.cpuKnown = true
	}
	return tree, ""
}

// parseProcesses reads what ps printed into a tree and a time reading.
//
// A line that cannot be read is skipped rather than failing the sample: ps
// prints one line per process on a machine whose process table is changing while
// it prints, and one unreadable line must not cost the other five hundred.
func parseProcesses(output string) (*processTree, processTimes) {
	tree := &processTree{nodes: make(map[int]*processNode), children: make(map[int][]int)}
	times := make(processTimes)

	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		resident, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			continue
		}
		used, ok := parseProcessTime(fields[3])
		if !ok {
			continue
		}

		// ps reports the resident set in kibibytes on both supported platforms.
		tree.nodes[pid] = &processNode{pid: pid, ppid: ppid, memory: resident * 1024}
		tree.children[ppid] = append(tree.children[ppid], pid)
		times[pid] = used
	}
	return tree, times
}

// parseProcessTime reads ps's cumulative processor time.
//
// The format is [dd-]hh:mm:ss on Linux and [dd-][hh:]mm:ss.cc on macOS, so the
// fields are read from the right rather than by counting them from the left.
func parseProcessTime(value string) (time.Duration, bool) {
	days := 0.0
	if before, after, found := strings.Cut(value, "-"); found {
		parsed, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, false
		}
		days, value = parsed, after
	}

	fields := strings.Split(value, ":")
	if len(fields) == 0 || len(fields) > 3 {
		return 0, false
	}

	seconds := days * 24 * 60 * 60
	multiplier := 1.0
	for i := len(fields) - 1; i >= 0; i-- {
		part, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return 0, false
		}
		seconds += part * multiplier
		multiplier *= 60
	}
	return time.Duration(seconds * float64(time.Second)), true
}

// subtree sums one process and its descendants.
//
// A task owns a terminal pane, and what the pane is really doing is whatever it
// started: a shell, an agent, and whatever the agent ran. Counting the pane
// alone would report a task compiling its project as using nothing at all.
func (t *processTree) subtree(pid int) subtreeUsage {
	var usage subtreeUsage
	if t == nil {
		return usage
	}

	seen := make(map[int]bool)
	stack := []int{pid}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[current] {
			// A process table read while it is changing can describe a cycle. It
			// is walked once rather than trusted to be a tree.
			continue
		}
		seen[current] = true

		node, known := t.nodes[current]
		if !known {
			continue
		}
		usage.count++
		usage.memory += node.memory
		if node.cpuKnown {
			usage.cpu += node.cpu
			usage.cpuKnown = true
		}
		stack = append(stack, t.children[current]...)
	}
	return usage
}
