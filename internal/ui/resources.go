package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// A resource line is a label and a bar, and the two fill the rail exactly. Fixed
// columns rather than proportions, so that the three bars start and end in the
// same place and can be compared by eye.
const (
	railLabel = 7
	// numberWidth is the column the percentage is right-aligned in. Four holds
	// "100%", ">99%", and a machine asking two and a half times its cores for
	// work, so the bars end in the same column whatever the machine is doing.
	numberWidth = 4
	railBar     = railWidth - railLabel - 1 - numberWidth
)

// The bar's own characters. A block for what is in use and a lighter one for
// what is left, so that the extent of the bar is visible without a frame around
// it and an empty bar is still a bar.
const (
	barFull  = "█"
	barEmpty = "░"
)

// machineBlock renders the machine's resources for the foot of the rail.
//
// Three bars and their percentages, and no heading: the labels say what they
// are, and the rail's one heading belongs to the tasks. The question the block
// answers is whether there is room to start another task, and a share is what
// answers it — 48 GiB free is roomy on one disk and nearly nothing on the next,
// and neither the reader nor the screen has room to do the division.
func (m Model) machineBlock() string {
	switch {
	case m.resourceErr != nil:
		// Which read failed is a sentence, and it is in the footer where there
		// is width for one. What belongs here is that these are not figures.
		return mutedStyle.Render(absent + " machine not read")
	case !m.resources.Sampled:
		return mutedStyle.Render(absent + " no machine sample yet")
	}

	machine := m.resources.Machine
	return strings.Join(
		[]string{cpuRow(machine), memoryRow(machine), diskRow(machine)}, "\n")
}

// machineNote is why a figure the rail shows as absent is absent, for the
// footer.
//
// It is a sentence — "machine memory is unavailable: vm_stat reported nothing" —
// and the rail is thirty-two cells wide, so putting it beside the bars would
// truncate it into exactly the silence FR-UI-005 is against. The rail says which
// figure was not measured and the footer says why, and both are on the screen at
// once.
func (m Model) machineNote() string {
	if m.resourceErr != nil {
		if daemonGone(m.resourceErr) {
			// The footer's error line already says this, and says which key
			// answers it. Repeating it a line lower, in the version that names
			// no key, is two thirds of the footer spent on one fact.
			return ""
		}
		return mutedStyle.Render(m.resourceErr.Error())
	}
	note := strings.Join(m.resources.Notes, "; ")
	if note == "" {
		return ""
	}
	return mutedStyle.Render(note)
}

// cpuRow renders the processors in use.
//
// The share is the run-queue average against the core count, which is the one
// measure both supported platforms give: a per-core utilisation percentage is
// not obtainable on macOS from Go without cgo, so this is derived rather than
// read, and it is demand rather than occupancy (ADR-035, ADR-044). It can
// therefore pass 100%, which nothing that was truly a utilisation percentage
// could, and the bar stops at full while the number keeps going.
//
// A machine that did not report its cores gets no bar. There is no share without
// a denominator — a load of four is idle on sixteen cores and saturated on two —
// and a bar drawn against a guess is worse than one not drawn.
func cpuRow(machine api.MachineResources) string {
	if machine.Load == nil || machine.Cores <= 0 {
		return railRow("cpu", "")
	}
	return railRow("cpu", bar(machine.Load.One/float64(machine.Cores)))
}

// memoryRow renders the machine's memory in use.
func memoryRow(machine api.MachineResources) string {
	if machine.Memory == nil {
		return railRow("memory", "")
	}
	return railRow("memory", usedBar(machine.Memory.TotalBytes, machine.Memory.AvailableBytes))
}

// diskRow renders the filesystem holding Feat's state.
//
// That filesystem rather than any other, because it is the one every worktree,
// control workspace, and generated override lands on, and running out of room on
// it is what stops the next task from being created.
func diskRow(machine api.MachineResources) string {
	if machine.Disk == nil {
		return railRow("disk", "")
	}
	return railRow("disk", usedBar(machine.Disk.TotalBytes, machine.Disk.AvailableBytes))
}

// usedBar draws the part of a capacity that is not available.
//
// A capacity of zero is not a full disk and not an empty one; it is a filesystem
// nothing measured, so it draws no bar.
func usedBar(total, available uint64) string {
	if total == 0 || available > total {
		return ""
	}
	return bar(float64(total-available) / float64(total))
}

// bar draws a share of the bar column, with the percentage after it.
//
// The number ends the line rather than sitting in the middle of the bar, where
// it split the blocks either side of it into two runs that read as two
// measurements. It is the label's grey, because it is what the bar already says
// and a second colour would make it a second thing to look at.
//
// A share that is neither nothing nor everything never draws as either, in the
// bar or in the number. Rounding two percent down to an empty bar and to "0%"
// would say the machine is idle, and ninety-nine up to a full bar and "100%"
// would say there is no room left; both are claims the sample did not make.
func bar(share float64) string {
	number := percentage(share)

	// A number wider than its column takes the cells from the bar rather than
	// from the rail. The line is a fixed thirty-two cells whatever happens, and a
	// machine asking for twelve times its processors has a bar with nothing left
	// to say and a number that is the whole of the news.
	width := railBar
	if over := len(number) - numberWidth; over > 0 {
		width -= over
	}
	if missing := numberWidth - len(number); missing > 0 {
		number = strings.Repeat(" ", missing) + number
	}

	cells := filledCells(share, width)
	drawn := barStyle.Render(strings.Repeat(barFull, cells))
	if empty := width - cells; empty > 0 {
		drawn += mutedStyle.Render(strings.Repeat(barEmpty, empty))
	}

	numberStyle := mutedStyle
	if share > 1 {
		// More runnable work than processors to run it, which the bar cannot
		// show because it stops at full. Marked rather than judged: it is still
		// only a number, and Feat refuses nothing over it.
		numberStyle = attentionStyle
	}
	return drawn + " " + numberStyle.Render(number)
}

// filledCells is how much of a bar of this width is drawn as in use.
func filledCells(share float64, width int) int {
	share = math.Max(0, math.Min(1, share))

	cells := int(math.Round(share * float64(width)))
	if cells == 0 && share > 0 {
		cells = 1
	}
	if cells == width && share < 1 {
		cells = width - 1
	}
	return cells
}

// percentage renders a share, refusing the two roundings that would misreport it.
func percentage(share float64) string {
	rounded := int(math.Round(share * 100))
	switch {
	case rounded == 0 && share > 0:
		return "<1%"
	case rounded == 100 && share < 1:
		return ">99%"
	default:
		return strconv.Itoa(rounded) + "%"
	}
}

// railRow lays out one resource line.
//
// A metric with no bar says it was not measured rather than drawing an empty
// one — the rule ADR-031 set for the task list holds here, where a bar at zero
// would be the most readable false claim on the screen.
func railRow(label, drawn string) string {
	if drawn == "" {
		drawn = mutedStyle.Render(absent + " not measured")
	}
	return mutedStyle.Render(pad(label, railLabel)) + drawn
}

// taskResources finds one task's aggregate in the current sample.
func (m Model) taskResources(id string) (api.TaskResources, bool) {
	for _, usage := range m.resources.Tasks {
		if usage.TaskID == id {
			return usage, true
		}
	}
	return api.TaskResources{}, false
}

// resourceCell renders one task's totals for the task list.
//
// A task nothing measured shows nothing. That is not the same as a task using
// nothing, and a draft — which owns no container and no process — is the
// commonest example: it has not been measured because there is nothing there to
// measure.
func (m Model) resourceCell(task api.Task) string {
	usage, found := m.taskResources(task.ID)
	if !found {
		return absent
	}

	cpu := absent
	if usage.CPUPercent != nil {
		cpu = fmt.Sprintf("%.0f%%", *usage.CPUPercent)
	}
	memory := absent
	if usage.MemoryBytes != nil {
		memory = humanBytes(*usage.MemoryBytes)
	}
	if cpu == absent && memory == absent {
		return absent
	}
	return cpu + " " + memory
}

// resourceDetail renders one task's usage with room to explain it.
//
// The two halves are named because they are not measured against the same thing.
// A container's memory is what the container runtime reported, and on macOS that
// is memory inside its own virtual machine rather than a share of the memory in
// the card above; the process half is host memory. Adding them says what this
// task is using, and saying only the sum would invite the wrong comparison
// (ADR-035).
func (m Model) resourceDetail(task api.Task) string {
	if m.resourceErr != nil {
		return absent + "  " + mutedStyle.Render("("+m.resourceErr.Error()+")")
	}
	usage, found := m.taskResources(task.ID)
	if !found || (usage.CPUPercent == nil && usage.MemoryBytes == nil) {
		return absent + "  " + mutedStyle.Render("(nothing of this task's has been measured)")
	}

	var out strings.Builder
	out.WriteString(m.resourceCell(task))

	var halves []string
	if len(usage.Containers) > 0 {
		halves = append(halves, humanBytes(usage.ContainerBytes)+" in "+
			count(len(usage.Containers), "container", "containers")+
			" (as the container runtime reports it)")
	}
	if usage.Processes > 0 {
		halves = append(halves, humanBytes(usage.ProcessBytes)+" in "+
			count(usage.Processes, "host process", "host processes"))
	}
	if len(halves) > 0 {
		out.WriteString("\n  " + mutedStyle.Render(strings.Join(halves, ", ")))
	}

	for _, container := range usage.Containers {
		out.WriteString("\n  " + mutedStyle.Render(container.Kind+"  ") + container.Name +
			mutedStyle.Render(fmt.Sprintf("  %.0f%% %s", container.CPUPercent, humanBytes(container.MemoryBytes))))
	}
	return out.String()
}

// count renders "1 container" and "3 containers".
//
// Both forms are given rather than derived, because "process" does not become
// its plural by adding one letter, and a screen that misspelled the word is a
// screen somebody stops reading.
func count(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

// byteUnits are rendered in binary multiples, which is what both the kernel and
// the container runtime measure in.
var byteUnits = []struct {
	suffix string
	factor float64
}{
	{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
}

// bytes renders a size in the largest unit that keeps it readable.
func humanBytes(size uint64) string {
	value := float64(size)
	for _, unit := range byteUnits {
		if value < unit.factor {
			continue
		}
		scaled := value / unit.factor
		if scaled < 10 {
			return fmt.Sprintf("%.1f %s", scaled, unit.suffix)
		}
		return fmt.Sprintf("%.0f %s", scaled, unit.suffix)
	}
	return strconv.FormatUint(size, 10) + " B"
}
