package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
)

// sampled is a report describing a machine with room on it and one task using
// part of that room.
func sampled() api.ResourceReport {
	cpu := 143.5
	memory := uint64(2_684_354_560)
	return api.ResourceReport{
		Machine: api.MachineResources{
			Cores:  10,
			Load:   &api.LoadAverage{One: 3.6, Five: 3.46, Fifteen: 3.76},
			Memory: &api.Capacity{TotalBytes: 17_179_869_184, AvailableBytes: 4_294_967_296},
			Disk: &api.DiskCapacity{
				Path: "/state", TotalBytes: 1_000_000_000_000, AvailableBytes: 400_000_000_000,
			},
		},
		Tasks: []api.TaskResources{{
			TaskID:         liveTask().ID,
			CPUPercent:     &cpu,
			MemoryBytes:    &memory,
			ContainerBytes: 2_147_483_648,
			ProcessBytes:   536_870_912,
			Containers: []api.ContainerResources{
				{ID: "aaaa1111", Name: "feat-agent-example-7f3a1c2e-dev", Kind: "agent",
					CPUPercent: 12.5, MemoryBytes: 2_147_483_648},
			},
			Processes: 7,
		}},
		Sampled: true,
	}
}

// withResources loads a dashboard's resource sample the way the daemon's answer
// would.
func withResources(model Model, report api.ResourceReport, err error) Model {
	updated, _ := model.Update(resourcesMsg{report: report, err: err})
	return updated.(Model)
}

// TestTheDashboardShowsWholeMachineResources is FR-UI-005's first half.
//
// The card answers one question — is there room to start another task — and
// answers it with what was measured. Load is reported against the core count
// because the number means nothing without it: four is idle on sixteen cores and
// saturated on two.
func TestTheDashboardShowsWholeMachineResources(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil)

	view := content(model)
	for _, want := range []string{"3.60", "10 cores", "4.0 GiB", "16 GiB", "373 GiB"} {
		if !strings.Contains(view, want) {
			t.Errorf("the machine card does not show %q:\n%s", want, view)
		}
	}
}

// TestTheTaskRowShowsItsOwnTotals is FR-UI-005's second half.
func TestTheTaskRowShowsItsOwnTotals(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil)

	view := content(model)
	for _, want := range []string{"144%", "2.5 GiB"} {
		if !strings.Contains(view, want) {
			t.Errorf("the task row does not show %q:\n%s", want, view)
		}
	}
}

// TestTheTaskDetailSeparatesContainerFromProcessMemory checks that the two
// halves are named rather than only summed.
//
// They are not measured against the same thing: a container's memory is what the
// container runtime reported, and on macOS that is memory inside its own virtual
// machine rather than a share of the machine in the card above. Saying only the
// sum would invite exactly that comparison.
func TestTheTaskDetailSeparatesContainerFromProcessMemory(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil)
	model.selected = liveTask().ID
	model.screen = screenTask

	view := content(model)
	for _, want := range []string{
		"1 container", "7 host processes", "container runtime", "feat-agent-example-7f3a1c2e-dev", "agent",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the task detail does not show %q:\n%s", want, view)
		}
	}
}

// TestAnUnmeasuredTaskShowsNothingRatherThanZero is the honesty rule at the
// dashboard.
//
// A draft owns no container and no process, so nothing was measured for it. A
// row saying "0% 0 B" would be a claim, which is what ADR-028 and ADR-031
// forbid.
func TestAnUnmeasuredTaskShowsNothingRatherThanZero(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), pendingDraft()), sampled(), nil)

	view := content(model)
	if strings.Contains(view, "0 B") {
		t.Errorf("a task nothing measured reports a size:\n%s", view)
	}
	if !strings.Contains(view, absent) {
		t.Errorf("a task nothing measured is not marked absent:\n%s", view)
	}
}

// TestAFailedResourceReadDoesNotHideTheTasks is the second acceptance criterion
// at the dashboard.
//
// Metrics are observational. A dashboard that refused to draw because it could
// not measure memory would be the opposite of what they are for, and the tasks
// are what the user opened it to see.
func TestAFailedResourceReadDoesNotHideTheTasks(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), api.ResourceReport{},
		errors.New("the daemon could not be reached for resources"))

	view := content(model)
	if !strings.Contains(view, liveTask().Title) {
		t.Errorf("a failed resource read hid the task list:\n%s", view)
	}
	if !strings.Contains(view, "could not be reached") {
		t.Errorf("a failed resource read is not explained where the figures would be:\n%s", view)
	}
}

// TestASampleThatIsMissingFiguresSaysSo checks that a partly readable machine
// renders the parts it could read.
func TestASampleThatIsMissingFiguresSaysSo(t *testing.T) {
	report := api.ResourceReport{
		Machine: api.MachineResources{Cores: 10},
		Notes:   []string{"machine memory is unavailable: vm_stat reported nothing"},
		Sampled: true,
	}
	model := withResources(dashboard(newFakeBackend(), liveTask()), report, nil)

	view := content(model)
	if !strings.Contains(view, "vm_stat reported nothing") {
		t.Errorf("the note explaining a missing figure is not shown:\n%s", view)
	}
	if !strings.Contains(view, liveTask().Title) {
		t.Errorf("a partly readable machine hid the task list:\n%s", view)
	}
}

// TestAttentionBadgesMarkTheTasksThatMayNeedTheUser is FR-UI-004's TUI half.
//
// The badge is what makes attention readable across a screen of tasks: a user
// scanning the dashboard is looking for the one task that stopped, and a column
// of words all beginning with the same letters is not something an eye finds.
func TestAttentionBadgesMarkTheTasksThatMayNeedTheUser(t *testing.T) {
	waiting := liveTask()
	waiting.Attention = "needs_input"

	model := withResources(dashboard(newFakeBackend(), waiting), sampled(), nil)

	view := content(model)
	if !strings.Contains(view, badgeNeedsInput) {
		t.Errorf("a task that needs input carries no badge:\n%s", view)
	}
	if !strings.Contains(view, "1 task may need you") {
		t.Errorf("the dashboard does not summarise how many tasks may need the user:\n%s", view)
	}

	// A task that needs nothing is not badged, or the badge would mean nothing.
	quiet := liveTask()
	quiet.Attention = "none"
	calm := content(withResources(dashboard(newFakeBackend(), quiet), sampled(), nil))
	if strings.Contains(calm, "may need you") {
		t.Errorf("a dashboard with nothing waiting still summons the user:\n%s", calm)
	}
}

// TestSizesAreRendered pins the one piece of arithmetic on this screen.
func TestSizesAreRendered(t *testing.T) {
	for size, want := range map[uint64]string{
		512:               "512 B",
		2_147_483_648:     "2.0 GiB",
		17_179_869_184:    "16 GiB",
		1_000_000_000_000: "931 GiB",
	} {
		if got := humanBytes(size); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", size, got, want)
		}
	}
}
