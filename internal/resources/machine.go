package resources

import (
	"context"
	"runtime"
)

// machineReader reads what the whole host reports.
//
// It is an interface so that a test can arrange a machine this one is not: a
// host whose load cannot be read, whose memory figures are missing, and whose
// state directory is on a filesystem that refuses to answer.
type machineReader interface {
	read(ctx context.Context, runner Runner, diskPath string) (Machine, []string)
}

// hostMachine reads the machine this daemon is running on.
type hostMachine struct{}

var _ machineReader = hostMachine{}

// read asks the host for its load, memory, and disk availability.
//
// Each of the three is asked for independently and each failure becomes a note,
// because they fail for unrelated reasons: a full filesystem says nothing about
// memory, and a user whose disk is nearly full is exactly the user who most
// needs to be told about the other two.
func (hostMachine) read(ctx context.Context, runner Runner, diskPath string) (Machine, []string) {
	machine := Machine{Cores: runtime.NumCPU(), DiskPath: diskPath}
	var notes []string

	if load, err := readLoad(ctx, runner); err != nil {
		notes = append(notes, "machine load is unavailable: "+err.Error())
	} else {
		machine.Load1, machine.Load5, machine.Load15 = load[0], load[1], load[2]
		machine.LoadKnown = true
	}

	if total, available, err := readMemory(ctx, runner); err != nil {
		notes = append(notes, "machine memory is unavailable: "+err.Error())
	} else {
		machine.MemoryTotal, machine.MemoryAvailable = total, available
		machine.MemoryKnown = true
	}

	if diskPath != "" {
		if total, available, err := diskUsage(diskPath); err != nil {
			notes = append(notes, "disk availability for "+diskPath+" is unavailable: "+err.Error())
		} else {
			machine.DiskTotal, machine.DiskAvailable = total, available
			machine.DiskKnown = true
		}
	}
	return machine, notes
}
