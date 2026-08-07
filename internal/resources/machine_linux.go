package resources

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// The files Linux reports the machine's state in. Nothing is executed here: the
// kernel publishes both, and reading a file cannot fail in the ways a process
// can.
const (
	loadAverageFile = "/proc/loadavg"
	memoryInfoFile  = "/proc/meminfo"
)

// readLoad reports the machine's run-queue averages.
//
// Linux does publish processor times, in /proc/stat, and a true utilisation
// percentage could be computed from two readings of them. Feat reports load here
// anyway, because macOS has no equivalent Go can reach without cgo and a
// dashboard that shows a percentage on one platform and a load average on the
// other would be showing two different things under one heading (ADR-035).
func readLoad(_ context.Context, _ Runner) ([3]float64, error) {
	raw, err := os.ReadFile(loadAverageFile)
	if err != nil {
		return [3]float64{}, err
	}

	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return [3]float64{}, fmt.Errorf("%s reported %q, want three averages",
			loadAverageFile, strings.TrimSpace(string(raw)))
	}

	var load [3]float64
	for i := range load {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return [3]float64{}, fmt.Errorf("%s reported %q, want three averages",
				loadAverageFile, strings.TrimSpace(string(raw)))
		}
		load[i] = value
	}
	return load, nil
}

// readMemory reports the machine's total and available memory.
//
// MemAvailable is the kernel's own estimate of what a new process could get
// without swapping, which is a better answer than MemFree and is the one the
// kernel would rather be asked.
func readMemory(_ context.Context, _ Runner) (total, available uint64, err error) {
	raw, err := os.ReadFile(memoryInfoFile)
	if err != nil {
		return 0, 0, err
	}

	wanted := map[string]*uint64{"MemTotal": &total, "MemAvailable": &available}
	found := 0
	for line := range strings.SplitSeq(string(raw), "\n") {
		name, value, split := strings.Cut(line, ":")
		field, ok := wanted[strings.TrimSpace(name)]
		if !split || !ok || *field != 0 {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		size, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("%s reported %q for %s, want a size in kibibytes",
				memoryInfoFile, strings.TrimSpace(value), strings.TrimSpace(name))
		}
		// /proc/meminfo counts in kibibytes.
		*field = size * 1024
		found++
	}
	if found != len(wanted) {
		return 0, 0, fmt.Errorf("%s reported %d of the %d figures available memory is made of",
			memoryInfoFile, found, len(wanted))
	}
	return total, available, nil
}

// diskUsage reports the size and free space of the filesystem holding a path.
//
// Linux types the block size as a signed integer, and a negative one would wrap
// into an enormous block and report a filesystem larger than the machine. It
// cannot happen on a working kernel, which is the reason to say so here rather
// than to convert and hope: a figure this build cannot trust is reported as
// unmeasured, which is what every other unavailable figure does (ADR-035).
func diskUsage(path string) (total, available uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	if stat.Bsize <= 0 {
		return 0, 0, fmt.Errorf("the filesystem holding %s reports a block size of %d", path, stat.Bsize)
	}
	block := uint64(stat.Bsize)
	return stat.Blocks * block, stat.Bavail * block, nil
}
