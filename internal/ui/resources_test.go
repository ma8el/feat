package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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
// The block answers one question — is there room to start another task — and
// answers it as a share of each of the three things that would stop the next one
// starting. A load of 3.6 on ten cores, twelve of sixteen GiB of memory in use,
// and 600 of 1000 GB of disk.
func TestTheDashboardShowsWholeMachineResources(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil)

	view := ansi.Strip(content(model))
	for _, want := range []string{"cpu", "memory", "disk", "36%", "75%", "60%"} {
		if !strings.Contains(view, want) {
			t.Errorf("the machine block does not show %q:\n%s", want, view)
		}
	}
}

// TestAMachineLineIsABarAndItsPercentage is the shape of a resource line.
//
// The bar is the share in use and the number after it says the same thing
// exactly, which is why it is in the label's grey rather than the bar's colour:
// one measurement, read as a shape and confirmed as a figure.
func TestAMachineLineIsABarAndItsPercentage(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil)

	for _, want := range []struct {
		metric string
		bar    string
	}{
		// Seven, fifteen, and twelve cells of twenty.
		{"cpu", "███████░░░░░░░░░░░░░  36%"},
		{"memory", "███████████████░░░░░  75%"},
		{"disk", "████████████░░░░░░░░  60%"},
	} {
		if got := ansi.Strip(model.machineBlock()); !strings.Contains(got, pad(want.metric, railLabel)+want.bar) {
			t.Errorf("the %s line is not %q:\n%s", want.metric, want.bar, got)
		}
	}
}

// TestAMachineLineFillsTheRailExactly keeps the three bars comparable.
//
// They are read against each other, so they start and end in the same column;
// a line wider than the rail would also be cut by the region that draws it.
func TestAMachineLineFillsTheRailExactly(t *testing.T) {
	report := sampled()
	report.Machine.Load = &api.LoadAverage{One: 128.5}

	block := withResources(dashboard(newFakeBackend(), liveTask()), report, nil).machineBlock()
	for _, line := range strings.Split(block, "\n") {
		if got := ansi.StringWidth(line); got != railWidth {
			t.Errorf("a machine line is %d cells of the rail's %d: %q", got, railWidth, ansi.Strip(line))
		}
	}
}

// TestABarIsNeverEmptyOrFullByRounding is the honesty rule applied to a shape
// and to the number on it.
//
// Rounding two percent down to an empty bar and to "0%" would say the machine is
// idle, and rounding ninety-nine up to a full bar and "100%" would say there is
// no room left. Neither is what the sample found, and the bar is what gets read
// first.
func TestABarIsNeverEmptyOrFullByRounding(t *testing.T) {
	for _, want := range []struct {
		share float64
		bar   string
	}{
		{0.002, "█░░░░░░░░░░░░░░░░░░░  <1%"},
		{0.999, "███████████████████░ >99%"},
		{0, "░░░░░░░░░░░░░░░░░░░░   0%"},
		{1, "████████████████████ 100%"},
	} {
		if got := ansi.Strip(bar(want.share)); got != want.bar {
			t.Errorf("a share of %v drew %q, want %q", want.share, got, want.bar)
		}
	}
}

// TestAnUnmeasuredCapacityDrawsNoBar is the same rule where there is nothing to
// draw from.
//
// A bar at zero is the most readable false claim this screen could make: it says
// the disk is empty. A figure nothing measured is shown as absent (FR-UI-005),
// and so is the shape of it.
func TestAnUnmeasuredCapacityDrawsNoBar(t *testing.T) {
	report := api.ResourceReport{
		Machine: api.MachineResources{Cores: 10, Load: &api.LoadAverage{One: 3.6}},
		Notes:   []string{"machine memory is unavailable: vm_stat reported nothing"},
		Sampled: true,
	}
	block := ansi.Strip(withResources(dashboard(newFakeBackend(), liveTask()), report, nil).machineBlock())

	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "memory") && !strings.HasPrefix(line, "disk") {
			continue
		}
		if strings.ContainsAny(line, "█░") {
			t.Errorf("an unmeasured capacity drew a bar: %q", line)
		}
		if !strings.Contains(line, absent) {
			t.Errorf("an unmeasured capacity is not marked absent: %q", line)
		}
	}
}

// TestProcessorDemandOverTheCoreCountIsMarked keeps the one judgement the block
// makes, and the cue that this is demand rather than occupancy.
//
// The share is the run-queue average against the core count, so it can pass a
// hundred percent, which nothing that was truly a utilisation percentage could.
// The bar stops at full and the number keeps going, and Feat refuses nothing
// over it.
func TestProcessorDemandOverTheCoreCountIsMarked(t *testing.T) {
	report := sampled()
	report.Machine.Load = &api.LoadAverage{One: 24.5}

	block := withResources(dashboard(newFakeBackend(), liveTask()), report, nil).machineBlock()
	if !strings.Contains(ansi.Strip(block), pad("cpu", railLabel)+"████████████████████ 245%") {
		t.Errorf("an overloaded machine is not drawn at full:\n%s", ansi.Strip(block))
	}
	if !strings.Contains(block, attentionStyle.Render("245%")) {
		t.Errorf("demand over the core count is not marked:\n%s", block)
	}
}

// TestTheTaskPanelShowsItsOwnTotals is FR-UI-005's second half.
//
// It reads the panel rather than a list row: the wide table that carried a
// resource column per task was the overview page, and the rail that replaced it
// carries what answers which task to go to next.
func TestTheTaskPanelShowsItsOwnTotals(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil)
	model.selected = liveTask().ID

	view := model.taskPanel()
	for _, want := range []string{"144%", "2.5 GiB"} {
		if !strings.Contains(view, want) {
			t.Errorf("the task panel does not show %q:\n%s", want, view)
		}
	}
}

// TestTheTaskPanelSaysWhichFigureIsWhich takes the rail's vocabulary without its
// bars (ADR-086).
//
// "2% 448 MiB" left the reader to infer each meaning from its unit, which is the
// machine block's defect before ADR-044 one level down. No bar goes with the
// words: the rail's bars are shares of this host and these figures are not, and
// one drawn against the host's total would invite the comparison ADR-035
// refuses.
func TestTheTaskPanelSaysWhichFigureIsWhich(t *testing.T) {
	model := withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil)
	model.selected = liveTask().ID
	model.screen = screenTask

	panel := ansi.Strip(model.taskPanel())
	if !strings.Contains(panel, "cpu 144%   memory 2.5 GiB") {
		t.Errorf("the panel's two figures are not labelled:\n%s", panel)
	}
	if strings.ContainsAny(panel, barFull+barEmpty) {
		t.Errorf("a per-task figure was drawn as a share of this host:\n%s", panel)
	}

	// The breakdown and the per-container rows are the same total in two further
	// forms, and the container's name is the compose project with a suffix.
	for what, gone := range map[string]string{
		"the breakdown's container half": "container runtime",
		"the breakdown's process half":   "7 host processes",
		"a per-container row":            "feat-agent-example-7f3a1c2e-dev",
	} {
		if strings.Contains(panel, gone) {
			t.Errorf("the panel still repeats the total as %s:\n%s", what, panel)
		}
	}
}

// TestAFigureNothingMeasuredIsShownAbsentBesideOneThatWas keeps FR-UI-005's
// honesty rule inside a labelled line.
//
// The two figures come from different collectors, and one of them failing is not
// the other one being zero.
func TestAFigureNothingMeasuredIsShownAbsentBesideOneThatWas(t *testing.T) {
	report := sampled()
	report.Tasks[0].MemoryBytes = nil

	model := withResources(dashboard(newFakeBackend(), liveTask()), report, nil)
	model.selected = liveTask().ID
	model.screen = screenTask

	panel := ansi.Strip(model.taskPanel())
	if !strings.Contains(panel, "cpu 144%   memory "+absent) {
		t.Errorf("an unmeasured figure is not shown absent beside the one that was measured:\n%s", panel)
	}
	if strings.Contains(panel, "0 B") {
		t.Errorf("an unmeasured figure was rendered as a size:\n%s", panel)
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
	if !strings.Contains(view, liveTask().Key) {
		t.Errorf("a failed resource read hid the task list:\n%s", view)
	}
	if !strings.Contains(view, "could not be reached") {
		t.Errorf("a failed resource read is not explained where the figures would be:\n%s", view)
	}
}

// TestTheSampleNotesReachTheLayoutFooter keeps FR-UI-005's honesty where the
// dashboard is actually used.
//
// The notes explaining an absent figure used to be on the machine card, which
// lived on the overview page, which the three-region layout never drew. They
// belong beside the figures they explain: an absent figure with no reason next to
// it is the same silence the rule is against.
func TestTheSampleNotesReachTheLayoutFooter(t *testing.T) {
	report := api.ResourceReport{
		Machine: api.MachineResources{Cores: 10},
		Notes:   []string{"machine memory is unavailable: vm_stat reported nothing"},
		Sampled: true,
	}
	model := sized(withResources(dashboard(newFakeBackend(), liveTask()), report, nil), 200, 32)

	if view := model.View(); !strings.Contains(view, "vm_stat reported nothing") {
		t.Errorf("the layout does not explain the missing figure:\n%s", view)
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
	if !strings.Contains(view, liveTask().Key) {
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
