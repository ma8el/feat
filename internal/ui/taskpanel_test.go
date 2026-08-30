package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// TestTheTaskPanelCarriesBothHalvesOnce is the merge.
//
// Detail and review were two tabs that shared their subject, their header, their
// workflow, their repository list, and their check summary, and neither filled
// the main region on its own (ADR-042). One panel has to carry what FR-UI-003
// requires of task detail and what FR-REV-001 requires of review — and carry the
// shared parts once, which is the reason for merging them.
func TestTheTaskPanelCarriesBothHalvesOnce(t *testing.T) {
	panel := reviewScreen(t, newFakeBackend()).taskPanel()

	for requirement, want := range map[string]string{
		// FR-UI-003, which ADR-086 made a requirement about the tabs rather than
		// about this panel: the repository/base mapping, the runtime, and the
		// checks are here; the tmux target is shown by the terminal tab rather
		// than named on this one, and the brief has a tab of its own.
		"the base commit":     "1a2b3c4d5e6f",
		"the branch":          "feat/7f3a1c2e-add-a-scheduled-export-job",
		"the runtime":         "absent",
		"the compose project": "feat-agent-example-7f3a1c2e",
		// FR-REV-001 and FR-REV-004: the comparison, and the exits a review has
		// now that it records no decision (ADR-086).
		"the head commit":  "001122334455",
		"the line counts":  "+214 -36",
		"the exits":        "a to attach and revise",
		"the check detail": "Feat ran this",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("the panel is missing %s (%q):\n%s", requirement, want, panel)
		}
	}

	// And the shared parts appear once. Detail and review each listed the
	// repositories with their own subset of the same facts, and each stated the
	// workflow in its header; showing both was the duplication that made two
	// thin tabs look like two different things.
	for what, needle := range map[string]string{
		"the workflow":      "workflow ",
		"a repository":      "/srv/worktrees/example/7f3a1c2e/core",
		"the check summary": "run by Feat",
	} {
		if got := strings.Count(panel, needle); got != 1 {
			t.Errorf("%s appears %d times, want once:\n%s", what, got, panel)
		}
	}
}

// TestTextFeatDidNotWriteCannotBreakTheFrame is the reported defect.
//
// A check's detail and a task's brief are text from outside: a captured command's
// output, a file the user wrote. `go test` separates its columns with tabs, and a
// tab is one byte of zero display width that the terminal draws as a jump to the
// next multiple of eight. Every measurement the dashboard makes — the wrap, the
// cut to the region, the padding before the border — agreed that those lines fit,
// and the terminal drew them across the border, through the rail, and down the
// rest of the frame.
func TestTextFeatDidNotWriteCannotBreakTheFrame(t *testing.T) {
	backend := newFakeBackend()
	status := reviewed()
	status.Review.Checks[0].Detail = "go test -race ./...\n" +
		"ok\tgithub.com/ma8el/feat/internal/api\t1.383s\n" +
		"ok\tgithub.com/ma8el/feat/internal/daemon\t12.703s [no tests to run]\n" +
		"?\tgithub.com/ma8el/feat/internal/agent/agenttest\t[no test files]"
	// A carriage return does the same thing more violently: it puts the rest of
	// the line back at the terminal's left edge, over whatever is already there.
	status.Task.Brief = "Progress was reported like this:\r100%\vand then some\bmore."
	backend.reviewStatus = status

	model := sized(press(t, dashboard(backend, status.Task), "v"), 120, 40)
	width, _ := model.mainRegionSize()

	// The whole panel rather than the window the scroll happens to show, because
	// the defect is in what the region is asked to draw and any line of it can be
	// scrolled to.
	panel := model.wrappedPanel(width)
	for name, control := range map[string]string{
		"a tab": "\t", "a carriage return": "\r", "a vertical tab": "\v",
		"a backspace": "\b",
	} {
		if strings.Contains(panel, control) {
			t.Errorf("the panel carries %s, which it cannot measure and the terminal will not draw as one cell:\n%s",
				name, panel)
		}
	}
	for i, line := range strings.Split(panel, "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Errorf("panel line %d is %d cells against a region of %d: %q", i, got, width, line)
		}
	}

	// The columns the tabs were holding apart are still held apart, at the stops
	// the terminal would have used: the detail is indented six, so "ok" ends at
	// column eight and the path starts at sixteen. A tab dropped rather than
	// expanded would have run `go test`'s three columns together.
	if !strings.Contains(panel, "      ok        github.com/ma8el/feat/internal/api") {
		t.Errorf("the tabbed columns did not survive as spacing:\n%s", panel)
	}

	// And the frame around it holds: every line is the width the layout claims.
	for i, line := range strings.Split(model.View(), "\n") {
		if got := ansi.StringWidth(line); got > 120 {
			t.Errorf("line %d is %d cells wide against a terminal of 120: %q", i, got, line)
		}
	}
}

// TestATitleWithALineBreakCannotAddARailRow is the same defect where it would be
// worst.
//
// The rail counts the lines it draws to pin its foot and to cut a list that does
// not fit. A title is a user's sentence about their own work — pasted from an
// issue, written into a brief — and one carrying a line break would have made the
// rail's arithmetic wrong about its own entries.
func TestATitleWithALineBreakCannotAddARailRow(t *testing.T) {
	task := liveTask()
	task.Title = "Add a scheduled\nexport\tjob"

	model := sized(dashboard(newFakeBackend(), task), 120, 32)

	// The entry itself, because the rail's foot is pinned by padding the list to
	// the region: an entry that grew a third line would push a task off the bottom
	// and the rail would still be exactly as tall as it claims.
	entry := model.railEntry(task, true)
	if got := strings.Count(entry, "\n"); got != 2 {
		t.Errorf("the entry is %d lines, want the two FR-UI-002 lays out: %q", got, entry)
	}
	if strings.ContainsAny(entry, "\t") {
		t.Errorf("the rail drew a tab: %q", entry)
	}

	rail := model.railView(20)
	if got := len(strings.Split(rail, "\n")); got != 20 {
		t.Errorf("the rail is %d lines against a region of 20:\n%s", got, rail)
	}
}

// TestAnErrorCannotPushTheFooterApart is the same defect in the one part of the
// frame that holds still.
//
// The footer is a fixed number of rows, and the regions above it are sized
// against that count. A wrapped error carries whatever it wrapped — a command's
// output, with its line breaks — and was written into the footer whole.
func TestAnErrorCannotPushTheFooterApart(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)
	model.err = errors.New("reading tasks failed: the daemon said\n\tstatus 500\n\tno such task\r")

	// Both footers: the frame's, which the three regions are sized against, and
	// the stacked fallback's, which the narrow layout is.
	for _, footer := range []struct {
		what string
		body string
		want int
	}{
		{"the frame's footer", model.frameFooter(120), footerHeight},
		{"the stacked footer", model.footer(keyHints(keyHint("q", "quit"))), stackedFooterHeight},
	} {
		if got := len(strings.Split(footer.body, "\n")); got != footer.want {
			t.Errorf("%s is %d lines with an error in it, want %d:\n%s",
				footer.what, got, footer.want, footer.body)
		}
		for _, line := range strings.Split(footer.body, "\n") {
			if got := ansi.StringWidth(line); got > 120 {
				t.Errorf("a line of %s is %d cells against a terminal of 120: %q",
					footer.what, got, line)
			}
		}
		// It still says what went wrong, up to the width there is for it.
		if !strings.Contains(ansi.Strip(footer.body), "reading tasks failed") {
			t.Errorf("%s cut the error before it said anything:\n%s", footer.what, footer.body)
		}
	}
}

// TestALabelWiderThanItsColumnKeepsItsLine is a defect the merge made visible.
//
// The label column has a fixed width, and lipgloss wraps rather than overflows:
// "compose project" came out as "compose" and then "project" against the panel's
// left edge, where it read as a heading rather than a label. That label went with
// the environment section (ADR-086) and the widest one left fills the column
// exactly, which is the same branch.
func TestALabelWiderThanItsColumnKeepsItsLine(t *testing.T) {
	panel := ansi.Strip(reviewScreen(t, newFakeBackend()).taskPanel())

	for _, line := range strings.Split(panel, "\n") {
		if strings.HasPrefix(line, "says ") {
			t.Errorf("a wrapped label became its own line:\n%s", panel)
		}
	}
	if !strings.Contains(panel, "the agent says Added the export job") {
		t.Errorf("a label as wide as its column did not keep its value beside it:\n%s", panel)
	}
}

// TestThePanelDropsWhatTheRailAndTheTabsAlreadyCarry is ADR-086's first
// decision.
//
// Attention, agent state as a word, and elapsed time are four cells to the left
// in the rail; the runtime's detail is a whole tab; the tmux target is one
// constant and three object ids nobody reads; and the environment section ended
// in two lines of explanation identical on every task. A panel repeating them
// was thirty-five lines before its brief began.
func TestThePanelDropsWhatTheRailAndTheTabsAlreadyCarry(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask())
	model.selected = liveTask().ID
	model.screen = screenTask

	panel := ansi.Strip(model.taskPanel())
	for what, label := range map[string]string{
		"attention, which is a badge in the rail":   "attention",
		"elapsed time, which the rail carries":      "elapsed",
		"the brief's source, which moves to it":     "source",
		"the tmux session, window, and pane":        "tmux",
		"the tmux socket":                           "socket",
		"the compose service":                       "service",
		"the container's own uptime phrase":         "container",
		"the compose project as a field of its own": "compose project",
	} {
		for _, line := range strings.Split(panel, "\n") {
			if strings.HasPrefix(line, "  "+label+" ") {
				t.Errorf("the panel still has a field for %s:\n%s", what, panel)
			}
		}
	}

	for what, value := range map[string]string{
		"the task's uuid":                    liveTask().ID,
		"the tmux socket":                    "/run/feat/tmux.sock",
		"the tmux pane":                      "%7",
		"the user the agent runs as":         "coder",
		"the generated override's paragraph": "container_name",
		"the runtime's apology":              "only when you ask",
	} {
		if strings.Contains(panel, value) {
			t.Errorf("the panel still shows %s (%q):\n%s", what, value, panel)
		}
	}
}

// TestTheAgentFieldHasOneShapePerThingItCanSay is what the environment section
// collapsed into.
//
// What runs, where, and in what: the compose project on a continuation line
// because it is a name a user types into a tool on the trusted host, and the
// container's state appended only when it is not simply running. That last is
// the state 9d found in the log four times — reconciliation observing an agent
// container as not running — and the process word cannot express it.
func TestTheAgentFieldHasOneShapePerThingItCanSay(t *testing.T) {
	host := liveTask()
	host.Session.ExecutionMode = "host"
	host.Session.Execution = nil

	stopped := liveTask()
	stopped.Session.Process = "stopped"
	stopped.Session.Execution.Running = false

	for _, shape := range []struct {
		what string
		task api.Task
		want string
	}{
		{"a containerised session", liveTask(), "running · claude in devcontainer"},
		// "in host" read as the name of a container nobody had configured.
		{"a host-native session", host, "running · claude on the host"},
		{"a container that stopped under it", stopped,
			"stopped · claude in devcontainer · container not running"},
		{"a task that has no session yet", pendingDraft(), absent + "  (no terminal yet)"},
	} {
		model := dashboard(newFakeBackend(), shape.task)
		model.selected = shape.task.ID
		model.screen = screenTask

		panel := ansi.Strip(model.taskPanel())
		if !strings.Contains(panel, shape.want) {
			t.Errorf("the agent field for %s does not read %q:\n%s", shape.what, shape.want, panel)
		}
	}

	// The one state worth interrupting for is coloured as one. Nothing else on
	// the field is: a container that is running says nothing extra at all.
	loud := dashboard(newFakeBackend(), stopped)
	loud.selected = stopped.ID
	if panel := loud.taskPanel(); !strings.Contains(panel, attentionStyle.Render("container not running")) {
		t.Errorf("a container that is not running is not marked as needing attention:\n%s", panel)
	}
	calm := dashboard(newFakeBackend(), liveTask())
	calm.selected = liveTask().ID
	panel := ansi.Strip(calm.taskPanel())
	for what, value := range map[string]string{
		"a state it is not in":       "container not running",
		"Docker's own uptime phrase": "Up 4 minutes",
	} {
		if strings.Contains(panel, value) {
			t.Errorf("a running container reported %s:\n%s", what, panel)
		}
	}
}

// TestTheComposeProjectKeepsALineOfItsOwnAtEveryWidth is why it is a
// continuation line rather than part of the value.
//
// It is about fifty cells against a value column of thirty-nine at the minimum
// width, so inside the value the wrap would break it in a different place at
// every terminal size. Broken deliberately, the field is the same shape in all
// of them.
func TestTheComposeProjectKeepsALineOfItsOwnAtEveryWidth(t *testing.T) {
	identity := liveTask().Session.Execution.Identity

	for _, width := range []int{minimumWidth, 120, 160} {
		model := sized(dashboard(newFakeBackend(), liveTask()), width, 40)
		model.selected = liveTask().ID
		model.screen = screenTask

		region, _ := model.mainRegionSize()
		panel := ansi.Strip(model.wrappedPanel(region))

		var own bool
		for _, line := range strings.Split(panel, "\n") {
			if strings.Contains(line, identity) && strings.Contains(line, "claude in") {
				t.Errorf("at %d columns the compose project shares the agent's line:\n%s", width, line)
			}
			if strings.TrimRight(line, " ") == strings.Repeat(" ", fieldValueColumn)+identity {
				own = true
			}
		}
		if !own {
			t.Errorf("at %d columns the compose project is not on a line of its own under the value:\n%s",
				width, panel)
		}
	}
}

// TestADraftPanelHasNoContinuationLines checks the shape a task without a
// session leaves.
//
// A draft owns no environment, so there is no compose project to put under the
// agent field and nothing to say about a container that does not exist.
func TestADraftPanelHasNoContinuationLines(t *testing.T) {
	model := dashboard(newFakeBackend(), pendingDraft())
	model.selected = pendingDraft().ID
	model.screen = screenTask

	panel := ansi.Strip(model.taskPanel())
	for _, line := range strings.Split(panel, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, strings.Repeat(" ", fieldValueColumn)) {
			t.Errorf("a draft's panel has a continuation line under a field: %q\n%s", line, panel)
		}
	}
}

// TestTheTaskPanelScrollsRatherThanHidingWhatIsBelow keeps what does not fit
// reachable.
//
// The panel is shorter than the region for a one-repository task since the brief
// took a tab of its own, and a task with two repositories and a captured check
// detail still outgrows it. A region that clipped that in silence would read as a
// task with nothing under the fields.
func TestTheTaskPanelScrollsRatherThanHidingWhatIsBelow(t *testing.T) {
	model := sized(reviewScreen(t, newFakeBackend()), 120, 32)

	width, height := model.mainRegionSize()
	top := model.taskBody(width, height)
	if !strings.Contains(top, "pgup/pgdn") {
		t.Fatalf("a clipped panel does not say it was clipped:\n%s", top)
	}

	// Down until the last of the checks is reached, which must happen within the
	// panel's length rather than never.
	at := model
	for range 20 {
		at = press(t, at, "pgdown")
		if strings.Contains(at.taskBody(width, height), "the agent reported this") {
			if back := press(t, at, "pgup"); back.review.scroll >= at.review.scroll {
				t.Errorf("pgup did not move back: %d then %d", at.review.scroll, back.review.scroll)
			}
			return
		}
	}
	t.Errorf("the foot of the panel was never reached by scrolling:\n%s", at.taskBody(width, height))
}

// TestTheTaskPanelDoesNotScrollForAOneRepositoryTask is what moving the brief
// bought.
//
// The brief is unbounded and the fields are not, so the panel was a scroller
// before the document that made it scroll had been reached. The common case now
// fits, which is ADR-041's fourth piece of evidence restored: a field is where it
// was last time.
func TestTheTaskPanelDoesNotScrollForAOneRepositoryTask(t *testing.T) {
	task := liveTask()
	task.Repositories = task.Repositories[:1]
	task.Brief = strings.Repeat("A brief long enough to have filled the region on its own.\n", 40)

	model := sized(dashboard(newFakeBackend(), task), 120, 32)
	model.selected = task.ID
	model.screen = screenTask

	width, height := model.mainRegionSize()
	if body := model.taskBody(width, height); strings.Contains(body, "pgup/pgdn") {
		t.Errorf("the panel for a one-repository task still scrolls:\n%s", body)
	}
}

// TestScrollingStopsAtTheEndOfThePanel keeps a held key from building up an
// offset that takes as many presses to undo.
func TestScrollingStopsAtTheEndOfThePanel(t *testing.T) {
	model := sized(reviewScreen(t, newFakeBackend()), 120, 32)

	at := model
	for range 30 {
		at = press(t, at, "pgdown")
	}
	settled := at.review.scroll
	if settled == 0 {
		t.Fatalf("this panel does not scroll at all, so there is no end to stop at")
	}

	if again := press(t, at, "pgdown"); again.review.scroll != settled {
		t.Errorf("scrolling past the end moved from %d to %d", settled, again.review.scroll)
	}
	// And one press back is one page back — or the top, on a panel with less
	// than a page left to give — rather than the many presses it would take to
	// unwind an unbounded offset.
	want := max(settled-panelPage, 0)
	if back := press(t, at, "pgup"); back.review.scroll != want {
		t.Errorf("pgup from the end left the offset at %d, want %d", back.review.scroll, want)
	}
}

// TestSOpensTheShellOnTheTaskPanelToo records a key that used to mean two things.
//
// `s` ran the configured status command here and opened the task's shell
// everywhere else. What that command printed was a line or two on the screen the
// TUI had just left, gone before it could be read, and the panel already states
// what it would have said (ADR-045). The command itself is still configured and
// still expanded — `feat review` prints it — so what this test pins is the key,
// not the feature.
func TestSOpensTheShellOnTheTaskPanelToo(t *testing.T) {
	backend := newFakeBackend()
	model := reviewScreen(t, backend)

	press(t, model, "s")

	if len(backend.reviewRan) != 0 {
		t.Errorf("s ran a review command from the task panel: %+v", backend.reviewRan)
	}
	if len(backend.shells) != 1 {
		t.Errorf("s opened %d shells from the task panel, want one", len(backend.shells))
	}
}
