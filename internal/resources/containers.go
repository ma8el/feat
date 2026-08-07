package resources

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// dockerProgram is the container tool. Only the host ever runs it.
const dockerProgram = "docker"

// containerFormat asks Docker for one line per container with the two labels
// that attribute it.
//
// The labels are extracted individually rather than read out of Docker's
// comma-joined Labels field, because a label a user's own Compose file sets may
// contain a comma and would then split into values that were never there.
const containerFormat = "{{.ID}}\t{{.Names}}\t{{.Label \"%s\"}}\t{{.Label \"%s\"}}"

// containers lists the Feat-owned containers and what each is using, grouped by
// the task that owns it.
//
// It returns a note rather than an error, because a machine with no container
// runtime has no containers rather than a broken observation, and a Docker that
// refuses must not discard the machine figures collected beside it.
func (o *Observer) containers(ctx context.Context) (map[string][]ContainerUsage, string) {
	if o.labels.selector == "" || o.labels.task == "" {
		return nil, ""
	}

	owned, note := o.ownedContainers(ctx)
	if note != "" || len(owned) == 0 {
		// Nothing of Feat's is running, so `docker stats` is not asked. That call
		// costs between one and two seconds whatever it is given, and paying it to
		// confirm an absence would be paying it on every machine where no task has
		// a container at all (ADR-035, evidence 1).
		return nil, note
	}

	stats, note := o.containerStats(ctx)
	if note != "" {
		return nil, note
	}

	grouped := make(map[string][]ContainerUsage, len(owned))
	for _, container := range owned {
		usage := ContainerUsage{ID: container.id, Name: container.name, Kind: container.kind}
		if measured, ok := stats[container.id]; ok {
			usage.CPUPercent = measured.cpu
			usage.MemoryBytes = measured.memory
		}
		grouped[container.task] = append(grouped[container.task], usage)
	}
	return grouped, ""
}

// ownedContainer is one running container carrying Feat's ownership label.
type ownedContainer struct {
	id   string
	name string
	task string
	kind string
}

// ownedContainers asks Docker which of its running containers are Feat's.
func (o *Observer) ownedContainers(ctx context.Context) ([]ownedContainer, string) {
	format := strings.Replace(containerFormat, "%s", o.labels.task, 1)
	format = strings.Replace(format, "%s", o.labels.kind, 1)

	output, err := o.runner.Run(ctx, Invocation{
		Program:   dockerProgram,
		Arguments: []string{"ps", "--filter", "label=" + o.labels.selector, "--format", format},
	})
	switch {
	case errors.Is(err, ErrNotInstalled):
		// Not a failure. A machine without a container runtime runs no
		// containers, and saying so once is more useful than reporting an error
		// every two seconds for the life of the daemon.
		return nil, ""
	case err != nil:
		return nil, "container usage is unavailable: " + err.Error()
	case !output.Succeeded():
		return nil, "container usage is unavailable: " + firstLine(output.Stderr, output.Stdout)
	}

	var owned []ownedContainer
	for line := range strings.SplitSeq(output.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		for len(fields) < 4 {
			fields = append(fields, "")
		}
		if fields[0] == "" || fields[2] == "" {
			// A container carrying Feat's owner label but no task label belongs to
			// no task. It is left out rather than attributed to a guess.
			continue
		}
		kind := fields[3]
		if kind == "" {
			// The agent's generated override carries no kind label, so a
			// Feat-owned container without one is the environment an agent runs
			// in (ADR-035).
			kind = KindAgent
		}
		owned = append(owned, ownedContainer{id: fields[0], name: fields[1], task: fields[2], kind: kind})
	}
	return owned, ""
}

// measurement is what the container runtime reported for one container.
type measurement struct {
	cpu    float64
	memory uint64
}

// stat is one entry of `docker stats --format json`.
type stat struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
}

// containerStats asks Docker what its running containers are using.
//
// It asks about all of them in one call rather than about each task's in turn:
// the call costs the same either way, and one call per task per sample would
// multiply the slowest thing this package does by the number of tasks a user is
// running — which is exactly the number the dashboard exists to make large.
func (o *Observer) containerStats(ctx context.Context) (map[string]measurement, string) {
	output, err := o.runner.Run(ctx, Invocation{
		Program:   dockerProgram,
		Arguments: []string{"stats", "--no-stream", "--format", "json"},
	})
	switch {
	case errors.Is(err, ErrNotInstalled):
		return nil, ""
	case err != nil:
		return nil, "container usage is unavailable: " + err.Error()
	case !output.Succeeded():
		return nil, "container usage is unavailable: " + firstLine(output.Stderr, output.Stdout)
	}

	measured := make(map[string]measurement)
	for line := range strings.SplitSeq(output.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry stat
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, "container usage could not be read: " + err.Error()
		}
		if entry.ID == "" {
			continue
		}
		measured[entry.ID] = measurement{
			cpu:    parsePercent(entry.CPUPerc),
			memory: parseBytes(memoryUsed(entry.MemUsage)),
		}
	}
	return measured, ""
}

// memoryUsed takes the used half of a "540KiB / 7.653GiB" pair.
//
// The second half is the limit the container runtime measures against, and on
// macOS that is the memory of its own virtual machine rather than the machine's.
// Feat therefore never reads it: a per-task total presented as a share of host
// memory would be a claim nothing measured (ADR-035, evidence 3).
func memoryUsed(usage string) string {
	used, _, found := strings.Cut(usage, "/")
	if !found {
		return strings.TrimSpace(usage)
	}
	return strings.TrimSpace(used)
}

// parsePercent reads "12.34%".
func parsePercent(value string) float64 {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "%"))
	percent, err := strconv.ParseFloat(value, 64)
	if err != nil || percent < 0 {
		return 0
	}
	return percent
}

// byteUnits are the suffixes Docker prints, largest first so that "GiB" is
// matched before "B".
var byteUnits = []struct {
	suffix string
	factor float64
}{
	{"PiB", 1 << 50}, {"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	{"PB", 1e15}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
}

// parseBytes reads a size the container runtime printed.
//
// Docker prints binary units for memory and decimal ones elsewhere, and both are
// accepted rather than one being assumed: reading "1.5GB" as 1.5 GiB would
// overstate a task's memory by seven percent for ever, and nothing would say so.
func parseBytes(value string) uint64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	for _, unit := range byteUnits {
		number, found := strings.CutSuffix(value, unit.suffix)
		if !found {
			continue
		}
		size, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err != nil || size < 0 {
			return 0
		}
		return uint64(size * unit.factor)
	}
	return 0
}

// firstLine returns the first non-empty line of the first non-empty stream.
//
// Docker reports some failures on standard output rather than standard error,
// which slice 8 found the hard way, so both are read (ADR-033, evidence 10).
func firstLine(streams ...string) string {
	for _, stream := range streams {
		for line := range strings.SplitSeq(stream, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				return trimmed
			}
		}
	}
	return "the container runtime reported nothing"
}
