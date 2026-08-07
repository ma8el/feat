package resources

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

// The macOS tools that report what the machine is doing.
//
// Both are given as absolute paths. A daemon started in the background does not
// inherit the user's interactive shell, and /usr/sbin is not on every PATH.
const (
	sysctlProgram = "/usr/sbin/sysctl"
	vmStatProgram = "/usr/bin/vm_stat"
)

// readLoad reports the machine's run-queue averages.
//
// macOS has no processor-utilisation figure that Go can read without cgo: the
// Mach call that carries it, host_processor_info, is not reachable from pure Go,
// and the usual library returns "not implemented" in its no-cgo build. Feat
// therefore reports load on both platforms rather than a percentage on one and
// something differently defined on the other (ADR-035, and the reason this
// function exists on Linux too).
func readLoad(ctx context.Context, runner Runner) ([3]float64, error) {
	output, err := runner.Run(ctx, Invocation{
		Program:   sysctlProgram,
		Arguments: []string{"-n", "vm.loadavg"},
	})
	if err != nil {
		return [3]float64{}, err
	}
	if !output.Succeeded() {
		return [3]float64{}, fmt.Errorf("%s", firstLine(output.Stderr, output.Stdout))
	}
	return parseLoad(output.Stdout)
}

// parseLoad reads "{ 3.60 3.46 3.76 }".
func parseLoad(output string) ([3]float64, error) {
	fields := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(output))
	if len(fields) < 3 {
		return [3]float64{}, fmt.Errorf("vm.loadavg reported %q, want three averages", strings.TrimSpace(output))
	}

	var load [3]float64
	for i := range load {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return [3]float64{}, fmt.Errorf("vm.loadavg reported %q, want three averages", strings.TrimSpace(output))
		}
		load[i] = value
	}
	return load, nil
}

// readMemory reports the machine's total and available memory.
//
// The total comes from a sysctl and the availability from vm_stat, because macOS
// counts memory in pages held by the Mach virtual-memory system rather than in a
// file anything can read.
func readMemory(ctx context.Context, runner Runner) (total, available uint64, err error) {
	sizeOutput, err := runner.Run(ctx, Invocation{
		Program:   sysctlProgram,
		Arguments: []string{"-n", "hw.memsize"},
	})
	if err != nil {
		return 0, 0, err
	}
	if !sizeOutput.Succeeded() {
		return 0, 0, fmt.Errorf("%s", firstLine(sizeOutput.Stderr, sizeOutput.Stdout))
	}
	total, err = strconv.ParseUint(strings.TrimSpace(sizeOutput.Stdout), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("hw.memsize reported %q, want a size in bytes",
			strings.TrimSpace(sizeOutput.Stdout))
	}

	statOutput, err := runner.Run(ctx, Invocation{Program: vmStatProgram})
	if err != nil {
		return 0, 0, err
	}
	if !statOutput.Succeeded() {
		return 0, 0, fmt.Errorf("%s", firstLine(statOutput.Stderr, statOutput.Stdout))
	}
	available, err = parseAvailableMemory(statOutput.Stdout)
	if err != nil {
		return 0, 0, err
	}
	return total, available, nil
}

// availablePages are the counters that make up memory the kernel can hand out
// without paging anything to disk.
//
// Purgeable pages are deliberately not added: vm_stat already counts them inside
// the active and inactive figures, so adding them would count the same memory
// twice and report a machine as having more free memory than it has.
var availablePages = []string{"Pages free", "Pages inactive", "Pages speculative"}

// parseAvailableMemory reads vm_stat's page counters.
func parseAvailableMemory(output string) (uint64, error) {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("vm_stat reported nothing")
	}

	pageSize, err := parsePageSize(lines[0])
	if err != nil {
		return 0, err
	}

	counted := 0
	var pages uint64
	for _, line := range lines[1:] {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		for _, wanted := range availablePages {
			if name != wanted {
				continue
			}
			count, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(value), "."), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("vm_stat reported %q for %q, want a page count",
					strings.TrimSpace(value), name)
			}
			pages += count
			counted++
		}
	}
	if counted != len(availablePages) {
		return 0, fmt.Errorf("vm_stat reported %d of the %d page counters available memory is made of",
			counted, len(availablePages))
	}
	return pages * pageSize, nil
}

// parsePageSize reads "Mach Virtual Memory Statistics: (page size of 16384 bytes)".
//
// The size is read rather than assumed, because it is not the same on every Mac
// and a wrong one would misreport free memory by a factor of four with nothing
// to say so.
func parsePageSize(heading string) (uint64, error) {
	const marker = "page size of "
	_, after, found := strings.Cut(heading, marker)
	if !found {
		return 0, fmt.Errorf("vm_stat did not report its page size")
	}
	fields := strings.Fields(after)
	if len(fields) == 0 {
		return 0, fmt.Errorf("vm_stat did not report its page size")
	}
	size, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || size == 0 {
		return 0, fmt.Errorf("vm_stat reported the page size %q", fields[0])
	}
	return size, nil
}

// diskUsage reports the size and free space of the filesystem holding a path.
func diskUsage(path string) (total, available uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	block := uint64(stat.Bsize)
	return stat.Blocks * block, stat.Bavail * block, nil
}
