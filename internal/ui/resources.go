package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/api"
)

// machineCard renders whole-machine availability (FR-UI-005).
//
// It answers one question — is there room to start another task — and answers it
// with what was measured rather than with a verdict. Feat enforces no
// concurrency limit in v0 and offers no opinion here: the user decides, and this
// is the line they decide from.
func (m Model) machineCard() string {
	if m.resourceErr != nil {
		return mutedStyle.Render("machine   " + absent + "  (" + m.resourceErr.Error() + ")")
	}
	if !m.resources.Sampled {
		return mutedStyle.Render("machine   " + absent + "  (no sample has been taken yet)")
	}

	machine := m.resources.Machine
	parts := []string{loadField(machine), memoryField(machine), diskField(machine)}

	card := mutedStyle.Render("machine   ") + strings.Join(parts, mutedStyle.Render("   "))
	if note := strings.Join(m.resources.Notes, "; "); note != "" {
		card += "\n" + mutedStyle.Render("          "+note)
	}
	return card
}

// loadField renders the machine's load against its cores.
//
// Load rather than a utilisation percentage, and the core count beside it,
// because the number only means something next to how many processors there are:
// a load of four is idle on a sixteen-core machine and saturated on a two-core
// one. macOS has no per-core utilisation Feat can read without cgo, so this is
// the one measure both platforms report (ADR-035).
func loadField(machine api.MachineResources) string {
	if machine.Load == nil {
		return mutedStyle.Render("load ") + absent
	}
	cores := absent
	if machine.Cores > 0 {
		cores = strconv.Itoa(machine.Cores)
	}
	value := fmt.Sprintf("%.2f", machine.Load.One)
	if machine.Cores > 0 && machine.Load.One > float64(machine.Cores) {
		// More runnable work than processors to run it. Marked rather than
		// judged: it is still only a number, and Feat refuses nothing over it.
		value = attentionStyle.Render(value)
	}
	return mutedStyle.Render("load ") + value + mutedStyle.Render(" of "+cores+" cores")
}

// memoryField renders available memory against the total.
func memoryField(machine api.MachineResources) string {
	if machine.Memory == nil {
		return mutedStyle.Render("memory ") + absent
	}
	return mutedStyle.Render("memory ") + humanBytes(machine.Memory.AvailableBytes) +
		mutedStyle.Render(" free of "+humanBytes(machine.Memory.TotalBytes))
}

// diskField renders free space on the filesystem holding Feat's state.
//
// That filesystem rather than any other, because it is the one every worktree,
// control workspace, and generated override lands on, and running out of room on
// it is what stops the next task from being created.
func diskField(machine api.MachineResources) string {
	if machine.Disk == nil {
		return mutedStyle.Render("disk ") + absent
	}
	return mutedStyle.Render("disk ") + humanBytes(machine.Disk.AvailableBytes) +
		mutedStyle.Render(" free of "+humanBytes(machine.Disk.TotalBytes))
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
