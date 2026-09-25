package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// A resource line is a label and a bar, and the two fill the rail exactly.
// Fixed columns rather than proportions, so that the three bars start and end
// in the same place and can be compared by eye.
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

// machineBlock renders the machine's resources for the foot of the rail. Three
// bars and their percentages, and no heading: the labels say what they are, and
// the rail's one heading belongs to the tasks. A share answers whether there is
// room to start another task, where 48 GiB free is roomy on one disk and nearly
// nothing on the next.
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
// footer. It is a sentence — "machine memory is unavailable: vm_stat reported
// nothing" — and the rail is thirty-two cells wide, so beside the bars it would
// be truncated into the silence FR-UI-005 is against. The rail says which
// figure was not measured and the footer says why.
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

// cpuRow renders the processors in use. The share is the run-queue average
// against the core count, which is the one measure both supported platforms
// give: a per-core utilisation percentage is not obtainable on macOS from Go
// without cgo, so this is demand rather than occupancy (ADR-035, ADR-044). It
// can pass 100%, and the bar stops at full while the number keeps going.
//
// A machine that did not report its cores gets no bar. A load of four is idle
// on sixteen cores and saturated on two, so there is no share without a
// denominator.
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

// diskRow renders the filesystem holding Feat's state. That filesystem rather
// than any other, because it is the one every worktree, control workspace, and
// generated override lands on, and running out of room on it stops the next
// task from being created.
func diskRow(machine api.MachineResources) string {
	if machine.Disk == nil {
		return railRow("disk", "")
	}
	return railRow("disk", usedBar(machine.Disk.TotalBytes, machine.Disk.AvailableBytes))
}

// usedBar draws the part of a capacity that is not available. A capacity of
// zero is not a full disk and not an empty one but a filesystem nothing
// measured, so it draws no bar.
func usedBar(total, available uint64) string {
	if total == 0 || available > total {
		return ""
	}
	return bar(float64(total-available) / float64(total))
}

// bar draws a share of the bar column, with the percentage after it. The number
// ends the line rather than sitting in the middle of the bar, where it split
// the blocks either side into two runs that read as two measurements. It is the
// label's grey, because it says what the bar already says.
//
// A share that is neither nothing nor everything never draws as either.
// Rounding two percent down to an empty bar and "0%" would say the machine is
// idle, and ninety-nine up to "100%" would say there is no room left.
func bar(share float64) string {
	number := percentage(share)

	// A number wider than its column takes the cells from the bar rather than
	// from the rail, whose line is a fixed thirty-two cells. A machine asking for
	// twelve times its processors has a bar with nothing left to say.
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
		// More runnable work than processors to run it, which the bar cannot show
		// because it stops at full. Marked rather than judged: Feat refuses nothing
		// over this number.
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

// percentage renders a share, refusing the two roundings that would misreport
// it.
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

// railRow lays out one resource line. A metric with no bar says it was not
// measured rather than drawing an empty one, because a bar at zero would be the
// most readable false claim on the screen (ADR-031).
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

// resourceDetail renders one task's totals, in the rail's vocabulary and not
// its bars (FR-UI-005, ADR-086). No bar, because the rail's bars are shares of
// this host and these are not: a container's memory is what the container
// runtime reported, inside its own virtual machine on macOS, and a bar against
// the host's total would invite the comparison ADR-035 refuses.
//
// A task nothing measured shows nothing rather than zero. A draft is the
// commonest case, because it owns no container and no process.
func (m Model) resourceDetail(task api.Task) string {
	if m.resourceErr != nil {
		return absent + "  " + mutedStyle.Render("("+m.resourceErr.Error()+")")
	}
	usage, found := m.taskResources(task.ID)
	if !found || (usage.CPUPercent == nil && usage.MemoryBytes == nil) {
		return absent + "  " + mutedStyle.Render("(nothing of this task's has been measured)")
	}

	cpu := absent
	if usage.CPUPercent != nil {
		cpu = fmt.Sprintf("%.0f%%", *usage.CPUPercent)
	}
	memory := absent
	if usage.MemoryBytes != nil {
		memory = humanBytes(*usage.MemoryBytes)
	}
	return mutedStyle.Render("cpu ") + cpu + mutedStyle.Render("   memory ") + memory
}

// count renders "1 container" and "3 containers". Both forms are given rather
// than derived, because "process" does not become its plural by adding one
// letter.
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

// humanBytes renders a size in the largest unit that keeps it readable.
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
