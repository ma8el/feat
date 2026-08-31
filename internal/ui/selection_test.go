package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// newerTask is a task created after the fixtures, which the newest-first list
// puts at the top and every index after it therefore moves by one.
//
// It is what the dashboard's own launch path produces, and what `feat implement`
// in a second terminal, another client, or a stream event that arrives between a
// key press and the request it makes produces too.
func newerTask() api.Task {
	task := liveTask()
	task.ID = "d41d8cd9-9999-8888-7777-666655554444"
	task.Key = "d41d8cd9"
	task.ProjectID = "example"
	task.Title = "Rotate the export credentials"
	task.Attention = "none"
	task.CreatedAt = dashboardOrigin.Add(time.Hour)
	task.UpdatedAt = task.CreatedAt
	return task
}

// newerDraft is newerTask as a draft, so that a test about a destructive key can
// let the wrong task actually be destroyed rather than be saved by a refusal.
func newerDraft() api.Task {
	task := newerTask()
	task.Workflow = "draft"
	task.Session = nil
	task.Repositories = nil
	return task
}

// railSelection is which of the given tasks the rail draws its marker beside.
//
// It reads the rendered rail rather than the model, because the defect these
// tests are about was the rail and the main region naming different tasks: a
// test that asked the model twice would have agreed with itself either way.
func railSelection(t *testing.T, m Model, tasks ...api.Task) []string {
	t.Helper()

	var marked []string
	for _, line := range strings.Split(ansi.Strip(m.railView(4*len(m.tasks)+8)), "\n") {
		if !strings.HasPrefix(line, "▸") {
			continue
		}
		for _, task := range tasks {
			if strings.Contains(line, task.Key) {
				marked = append(marked, task.Key)
			}
		}
	}
	return marked
}

// TestARefreshCannotRePointTheSelection is G2-04.
//
// The selection was an index into a list re-sorted newest-first on every read,
// and nothing re-derived it from the task it was supposed to name. A task
// appearing moved every index after it, so the selection silently landed on a
// task the user had not chosen — and the rail's marker and the main region's
// header then named different tasks, which is the one thing FR-UI-001 requires
// the list to be right about.
func TestARefreshCannotRePointTheSelection(t *testing.T) {
	first, second := liveTask(), otherTask()
	model := sized(dashboard(newFakeBackend(), first, second), 120, 32)

	moved := press(t, model, "J")
	if moved.selected != second.ID {
		t.Fatalf("J selected %s, want %s", moved.selected, second.Key)
	}

	// A task created anywhere at all is listed first, because the list is
	// newest-first. Nothing the user did produced this.
	newest := newerTask()
	refreshed, _ := moved.Update(tasksMsg{tasks: []api.Task{first, second, newest}})
	after := refreshed.(Model)

	if after.selected != second.ID {
		t.Errorf("a refresh moved the selection to %s, want %s", after.selected, second.Key)
	}
	if got, ok := after.subject(); !ok || got.ID != second.ID {
		t.Errorf("the subject after a refresh is %s, want %s", got.Key, second.Key)
	}
	if marked := railSelection(t, after, first, second, newest); len(marked) != 1 ||
		marked[0] != second.Key {
		t.Errorf("the rail marks %v, want only %s", marked, second.Key)
	}
}

// TestADestructiveKeyActsOnTheTaskTheRailNames is G2-04's consequence.
//
// The refresh that moves the selection needs no key press to arrive: a stream
// event between the keystroke and the action was enough for the user to destroy
// something they had not selected. Two drafts, so that the wrong one is
// cancelled rather than saved by cancel's refusal to touch a launched task.
//
// `x` asks now, so the destruction is on the `y` — but the question is the same
// key press and the same subject, and the task it names is what the rest of this
// test is about.
func TestADestructiveKeyActsOnTheTaskTheRailNames(t *testing.T) {
	draft, newest := pendingDraft(), newerDraft()
	backend := newFakeBackend()
	model := sized(dashboard(backend, draft), 120, 32)

	if marked := railSelection(t, model, draft); len(marked) != 1 {
		t.Fatalf("the rail marks %v, want the only task there is", marked)
	}

	// The refresh lands between the user deciding to cancel and their finger
	// reaching the key.
	refreshed, _ := model.Update(tasksMsg{tasks: []api.Task{draft, newest}})
	asking := press(t, refreshed.(Model), "x")
	if asking.cancelling != draft.ID {
		t.Fatalf("x asked about %s, want the selected draft %s", asking.cancelling, draft.Key)
	}

	cancelled := press(t, asking, "y")
	if got := backend.cancelled; len(got) != 1 || got[0] != draft.ID {
		t.Errorf("x cancelled %v, want only the selected draft %s", got, draft.Key)
	}
	if !strings.Contains(cancelled.status, draft.Key) {
		t.Errorf("the status line says %q, want it to name %s", cancelled.status, draft.Key)
	}
}

// TestADraftIsNotDestroyedWithoutAYes is the other half of the same gate entry.
//
// Naming the selection by identifier stopped `x` acting on a task the rail was
// not marking; it put nothing on the screen naming what the key was about to
// destroy. A draft's brief is text somebody typed and Feat holds the only copy,
// while `C` — cleanup, the neighbouring key on the same footer — confirms per
// resource class for containers and volumes that a repository can produce
// again.
//
// The question has to be answerable both ways, and it has to name its subject:
// the event this whole area is about is a refresh arriving in the middle, and a
// footer reading "cancel this draft?" over a rail whose marker has since moved
// is the defect with a confirmation drawn on it.
func TestADraftIsNotDestroyedWithoutAYes(t *testing.T) {
	draft := pendingDraft()
	backend := newFakeBackend()
	model := sized(dashboard(backend, draft), 120, 32)

	asking := press(t, model, "x")
	if len(backend.cancelled) != 0 {
		t.Fatalf("x cancelled %v before anything was answered", backend.cancelled)
	}
	if drawn := flowed(asking.frameFooter(120)); !strings.Contains(drawn, draft.Key) ||
		!strings.Contains(drawn, "y to confirm") {
		t.Errorf("the footer asks %q, want a question naming %s and the key that answers it",
			drawn, draft.Key)
	}

	// Any other key is a no, as it is for the stop confirmation and the runtime
	// destroy: a question about something irreversible is not answered by
	// whatever the user pressed next.
	kept := press(t, asking, "n")
	if len(backend.cancelled) != 0 {
		t.Errorf("a draft was cancelled by an answer that was not a yes: %v", backend.cancelled)
	}
	if kept.cancelling != "" {
		t.Error("the question is still pending after it was answered")
	}
	if !strings.Contains(kept.status, draft.Key) {
		t.Errorf("the status line says %q, want it to name the draft that was kept", kept.status)
	}
}

// TestALaunchedTaskIsTheOneTheRailPointsAt is G2-04 on the dashboard's own
// launch path, which is the shortest way to reproduce it.
//
// Preparation names the task it launched in the footer and made it the
// selection; the refresh that followed listed it first and moved the marker onto
// whatever had been first before.
func TestALaunchedTaskIsTheOneTheRailPointsAt(t *testing.T) {
	first, second := liveTask(), otherTask()
	launched := newerTask()
	// The user is not on the first row, so that the launched task landing at the
	// top of a newest-first list moves what every row holds.
	model := press(t, sized(dashboard(newFakeBackend(), first, second), 120, 32), "J")

	prepared, _ := model.Update(preparedMsg{task: &launched})
	refreshed, _ := prepared.(Model).Update(
		tasksMsg{tasks: []api.Task{first, second, launched}})
	after := refreshed.(Model)

	if after.selected != launched.ID {
		t.Errorf("the selection is %s, want the launched task %s", after.selected, launched.Key)
	}
	if marked := railSelection(t, after, first, second, launched); len(marked) != 1 ||
		marked[0] != launched.Key {
		t.Errorf("the rail marks %v while the footer says %q", marked, after.status)
	}
}

// TestASelectionWhoseTaskLeavesTheListIsReported checks the other half of the
// same rule.
//
// A task can leave the list without the user doing anything — a cleanup here, a
// cancellation from another terminal. The selection is not moved onto whatever
// now occupies the row, because that is the defect; it is said, in the words of
// the task that went.
func TestASelectionWhoseTaskLeavesTheListIsReported(t *testing.T) {
	first, second := liveTask(), otherTask()
	model := sized(dashboard(newFakeBackend(), first, second), 120, 32)

	moved := press(t, model, "J")
	refreshed, _ := moved.Update(tasksMsg{tasks: []api.Task{first}})
	after := refreshed.(Model)

	if marked := railSelection(t, after, first, second); len(marked) != 0 {
		t.Errorf("the rail marks %v after the selected task left the list, want nothing", marked)
	}
	if _, ok := after.subject(); ok {
		t.Error("an action would still have found a subject after the selected task left the list")
	}
	if !strings.Contains(after.status, second.Key) {
		t.Errorf("the status line says %q, want it to name the task that went", after.status)
	}
}

// TestTheFirstTaskListSelectsSomething keeps the dashboard startable.
//
// The rule above — a refresh never moves the selection — has exactly one
// exception, and this is it: a dashboard that has never had a task list has
// nothing selected, and a rail with no marker is not something to open on.
func TestTheFirstTaskListSelectsSomething(t *testing.T) {
	first, second := liveTask(), otherTask()
	model := sized(dashboard(newFakeBackend(), first, second), 120, 32)

	if model.selected != first.ID {
		t.Errorf("the first task list selected %s, want the newest task %s",
			model.selected, first.Key)
	}
}

// TestAReviewResponseForAnotherTaskIsDropped is G2-05.
//
// A comparison walks every one of a task's worktrees, so one asked for before
// the user moved on arrives after. It carried no task identifier and was applied
// unconditionally, so the panel drew one task's agent report, check results,
// repository rows and expanded commands under another task's name — and `d` and
// `e` opened the wrong worktree.
func TestAReviewResponseForAnotherTaskIsDropped(t *testing.T) {
	slow := reviewed()
	first, second := slow.Task, otherTask()

	backend := newFakeBackend()
	backend.reviewStatus = slow
	model := sized(dashboard(backend, first, second), 120, 32)

	panel := press(t, model, "T")
	if panel.review.task != first.ID {
		t.Fatalf("the panel opened on %s, want %s", panel.review.task, first.Key)
	}

	// The user moves on before the first comparison returns. The second task's
	// own comparison is small and comes back at once.
	backend.reviewStatus = api.ReviewStatus{Task: second}
	moved := press(t, panel, "J")
	if moved.review.task != second.ID {
		t.Fatalf("J left the panel on %s, want %s", moved.review.task, second.Key)
	}

	applied, _ := moved.Update(reviewMsg{task: first.ID, action: api.ReviewObserve, status: slow})
	after := applied.(Model)

	if after.review.status.Task.ID != second.ID {
		t.Errorf("the panel holds %s's comparison under %s's name",
			after.review.status.Task.Key, second.Key)
	}
	if len(after.review.status.Repositories) != 0 {
		t.Errorf("the panel drew %d repository rows, want the selected task's none",
			len(after.review.status.Repositories))
	}
	// The agent report and the head commit are the review's own, rather than the
	// task binding's, so they are what a response landing under the wrong name
	// would put on the screen.
	drawn := flowed(after.taskPanel())
	for _, leaked := range []string{"Added the export job and its retry policy", "001122334455"} {
		if strings.Contains(drawn, leaked) {
			t.Errorf("the panel shows the other task's %q:\n%s", leaked, drawn)
		}
	}
}

// TestARuntimeResponseForAnotherTaskIsDropped is G2-05 on the runtime screen,
// which is the case ADR-041 wrote the re-open-on-selection rule for: one task's
// service table, allocated ports and retained volume names under another task's
// heading.
func TestARuntimeResponseForAnotherTaskIsDropped(t *testing.T) {
	first, second := liveTask(), otherTask()
	slow := api.RuntimeStatus{
		Task: first,
		Services: []api.RuntimeService{
			{Name: "exporter", Container: "c0ffee", State: "running",
				Status: "Up 2 minutes", Health: "unknown", Managed: true},
		},
	}

	backend := newFakeBackend()
	backend.runtimeStatus = slow
	model := sized(dashboard(backend, first, second), 120, 32)

	screen := press(t, model, "R")
	if screen.runtime.task != first.ID {
		t.Fatalf("runtime opened on %s, want %s", screen.runtime.task, first.Key)
	}

	backend.runtimeStatus = api.RuntimeStatus{Task: second}
	moved := press(t, screen, "J")
	if moved.runtime.task != second.ID {
		t.Fatalf("J left runtime on %s, want %s", moved.runtime.task, second.Key)
	}

	applied, _ := moved.Update(runtimeMsg{task: first.ID, action: api.RuntimeObserve, status: slow})
	after := applied.(Model)

	if after.runtime.status.Task.ID != second.ID {
		t.Errorf("runtime holds %s's observation under %s's name",
			after.runtime.status.Task.Key, second.Key)
	}
	if drawn := flowed(after.runtimeBody()); strings.Contains(drawn, "exporter") {
		t.Errorf("runtime shows the other task's services:\n%s", drawn)
	}
}

// TestARefreshBetweenARequestAndItsResponseChangesNothing is the interleaving
// G6-58 says the suite never drove.
//
// The response is for the task that is still selected, so it is applied — a
// screen that dropped everything after a refresh would be as wrong as one that
// dropped nothing — and the refresh that arrived in the middle did not move the
// selection out from under it.
func TestARefreshBetweenARequestAndItsResponseChangesNothing(t *testing.T) {
	status := reviewed()
	subject := status.Task

	backend := newFakeBackend()
	backend.reviewStatus = status
	model := sized(dashboard(backend, subject), 120, 32)

	panel := press(t, model, "T")
	refreshed, _ := panel.Update(tasksMsg{tasks: []api.Task{subject, newerTask()}})
	applied, _ := refreshed.(Model).Update(
		reviewMsg{task: subject.ID, action: api.ReviewObserve, status: status})
	after := applied.(Model)

	if after.selected != subject.ID {
		t.Errorf("the refresh moved the selection to %s, want %s", after.selected, subject.Key)
	}
	if marked := railSelection(t, after, subject, newerTask()); len(marked) != 1 ||
		marked[0] != subject.Key {
		t.Errorf("the refresh moved the rail's marker to %v, want %s", marked, subject.Key)
	}
	if !after.review.loaded || after.review.status.Task.ID != subject.ID {
		t.Errorf("the response for the selected task was dropped: loaded=%v task=%s",
			after.review.loaded, after.review.status.Task.Key)
	}
}

// TestLeavingRuntimeOpensThePanelOnTheSelectedTask is G2-03.
//
// Leaving runtime for the panel set the screen rather than opening it, so the
// panel was drawn for the selected task while the review model still held
// whatever task it was last opened on. The heading named one task; the agent
// report, the check results, the repository rows — matched by repository
// identifier, so within one project they filled in — and every review key
// belonged to another. Pressing `A` there approved the task the screen was not
// about.
//
// It was `esc` that did it, and `T` is what leaves runtime for the panel now
// (ADR-089). The defect is not about which key: it is about a screen assigned
// rather than opened, and every route into the panel goes through openTask.
func TestLeavingRuntimeOpensThePanelOnTheSelectedTask(t *testing.T) {
	status := reviewed()
	first := status.Task
	second := otherTask()
	second.Workflow = "verification_failed"

	backend := newFakeBackend()
	backend.reviewStatus = status
	model := sized(dashboard(backend, first, second), 120, 32)

	panel := press(t, model, "T")
	if panel.review.task != first.ID {
		t.Fatalf("the panel opened on %s, want %s", panel.review.task, first.Key)
	}

	backend.reviewStatus = api.ReviewStatus{Task: second}
	// Two views along: the brief sits between the panel and the runtime.
	runtime := press(t, press(t, panel, "L"), "L")
	if runtime.screen != screenRuntime {
		t.Fatalf("L twice from the panel reached %v, want runtime", runtime.screen)
	}
	moved := press(t, runtime, "J")
	if moved.selected != second.ID {
		t.Fatalf("J selected %s, want %s", moved.selected, second.Key)
	}

	back := press(t, moved, "T")
	if back.screen != screenTask {
		t.Fatalf("T from runtime reached %v, want the task panel", back.screen)
	}
	if back.review.task != second.ID {
		t.Errorf("the panel is headed %s and reviewing %s", second.Key, back.review.task)
	}

	press(t, back, "V")
	if got := backend.reviewCalls; len(got) == 0 ||
		got[len(got)-1] != string(api.ReviewVerify)+" "+second.ID {
		t.Errorf("V ran the checks of %v, want %s — the task the panel names", got, second.Key)
	}
}

// TestLeavingTheTaskPanelAsksTmuxForTheSelectedTasksPane is the same rule as
// TestLeavingRuntimeOpensThePanelOnTheSelectedTask, on the view next door.
//
// Both views are left for something that holds one task's answer, and neither is
// re-opened by assigning a screen. The panel is left through selectTab, which
// discards the frame the terminal was holding and asks tmux for the selected
// task's. Setting the screen directly left the previous task's pane on the
// screen under the new task's name, with nothing outstanding to replace it.
//
// The half that is asserted here is the request. terminalBody's own guard makes
// the stale pane unreadable — "asking tmux what this pane shows…" — which is the
// safe half and is not the fix: a dashboard that says that forever is a
// dashboard with no terminal in it.
func TestLeavingTheTaskPanelAsksTmuxForTheSelectedTasksPane(t *testing.T) {
	first, second := liveTask(), otherTask()

	backend := newFakeBackend()
	backend.reviewStatus = api.ReviewStatus{Task: first}
	model := terminalDashboard(t, backend, first, second)

	loaded, _ := model.Update(terminalFrameMsg{task: first.ID, frame: twoPaneFrame()})
	shown := loaded.(Model)
	if !shown.terminal.loaded || shown.terminal.task != first.ID {
		t.Fatalf("the terminal holds %s loaded=%v, want %s's pane",
			shown.terminal.task, shown.terminal.loaded, first.Key)
	}

	backend.reviewStatus = api.ReviewStatus{Task: second}
	moved := press(t, press(t, shown, "T"), "J")
	if moved.selected != second.ID {
		t.Fatalf("J selected %s, want %s", moved.selected, second.Key)
	}

	asked := len(backend.frameTasks)
	back := press(t, moved, "A")

	if back.screen != screenTerminal {
		t.Fatalf("A from the task panel reached %v, want the terminal", back.screen)
	}
	if got := backend.frameTasks[asked:]; len(got) != 1 || got[0] != second.ID {
		t.Errorf("A from the task panel asked tmux for %v, want one frame for the selected task %s",
			got, second.Key)
	}
	if back.terminal.task != second.ID {
		t.Errorf("the terminal still holds %s's pane after leaving the panel on %s",
			back.terminal.task, second.Key)
	}
}

// TestLeavingTheTaskPanelRestartsThePollItLeftBehind is the other half of the
// same return.
//
// The poll stops when the tab does: a tick that arrives while another view has
// the main region clears terminal.polling, so a dashboard costs the daemon
// nothing while nobody is watching a pane. Nothing else starts it again.
// Returning by assigning a screen therefore left the terminal tab open with no
// poll behind it — one frame old at best, and never updated again, which reads
// as an agent that has stopped working.
func TestLeavingTheTaskPanelRestartsThePollItLeftBehind(t *testing.T) {
	first, second := liveTask(), otherTask()

	backend := newFakeBackend()
	backend.reviewStatus = api.ReviewStatus{Task: first}
	model := terminalDashboard(t, backend, first, second)
	if !model.terminal.polling {
		t.Fatalf("the first task list did not start the poll")
	}

	backend.reviewStatus = api.ReviewStatus{Task: second}
	moved := press(t, press(t, model, "T"), "J")

	// The tick already scheduled arrives while the panel has the region, which is
	// what stops the poll.
	stopped, _ := moved.Update(terminalTickMsg{})
	waiting := stopped.(Model)
	if waiting.terminal.polling {
		t.Fatalf("a tick on the task panel left the poll running")
	}

	updated, cmd := waiting.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	back := updated.(Model)

	if back.screen != screenTerminal {
		t.Fatalf("A from the task panel reached %v, want the terminal", back.screen)
	}
	if !back.terminal.polling {
		t.Error("A returned to the terminal with no poll behind it, " +
			"so the pane it draws is never asked about again")
	}
	if cmd == nil {
		t.Fatal("A from the task panel produced no command, so nothing asks tmux anything")
	}
}

// TestFeatReviewResolvesAShortKeyToTheTaskItNames is G2-06.
//
// `feat review <task>` documents and accepts a task's eight-character short key.
// It went straight into the selection, which is compared against the whole
// identifier, so the panel said "this task is no longer listed" while `A`, `C`
// and `V` went on working — the one screen saying the task did not exist was the
// screen from which approving it succeeded.
//
// The daemon resolves the reference, so the dashboard adopts what the first
// response carries rather than resolving it a second way of its own.
func TestFeatReviewResolvesAShortKeyToTheTaskItNames(t *testing.T) {
	status := reviewed()
	subject := status.Task

	backend := newFakeBackend()
	backend.tasks = []api.Task{subject}
	backend.reviewStatus = status

	opened := New(Options{
		Backend: backend,
		Daemon:  Daemon{Version: "v0.0.0-test", Socket: "/run/feat/feat.sock"},
		Now:     func() time.Time { return dashboardNow },
		Review:  subject.Key,
	})
	listed, _ := sized(opened, 120, 32).Update(tasksMsg{tasks: backend.tasks})
	waiting := listed.(Model)

	if _, ok := waiting.subject(); ok {
		t.Fatal("a short key matched a whole identifier before anything resolved it")
	}
	if marked := railSelection(t, waiting, subject); len(marked) != 0 {
		t.Errorf("the rail marks %v before the reference was resolved", marked)
	}

	applied, _ := waiting.Update(
		reviewMsg{task: subject.Key, action: api.ReviewObserve, status: status})
	after := applied.(Model)

	if after.selected != subject.ID {
		t.Errorf("the selection is %q, want the identifier %s", after.selected, subject.ID)
	}
	if after.review.task != subject.ID {
		t.Errorf("the review is on %q, want the identifier %s", after.review.task, subject.ID)
	}
	if drawn := flowed(after.taskPanel()); strings.Contains(drawn, "no longer listed") {
		t.Errorf("the panel still says the task is not listed:\n%s", drawn)
	}
	if marked := railSelection(t, after, subject); len(marked) != 1 || marked[0] != subject.Key {
		t.Errorf("the rail marks %v, want %s", marked, subject.Key)
	}

	// The keys that begin with the subject find one now. `t` on a working agent
	// asks before it interrupts, so this is the question rather than the stop.
	stopping := press(t, after, "t")
	if stopping.stopping != subject.ID {
		t.Errorf("t reached %q, want the task the panel is about", stopping.stopping)
	}
}

// TestTheTerminalDoesNotDrawAnotherTasksPane is G2-03's shape on the tab the
// dashboard opens on.
//
// applyFrame drops a frame that arrives after the selection moved. The frame
// already held when it moved is the other half: returning to the terminal from
// the task panel put the previous task's pane back on the screen under the new
// task's name, with no request outstanding to replace it.
func TestTheTerminalDoesNotDrawAnotherTasksPane(t *testing.T) {
	first, second := liveTask(), otherTask()

	backend := newFakeBackend()
	backend.reviewStatus = api.ReviewStatus{Task: first}
	model := terminalDashboard(t, backend, first, second)

	loaded, _ := model.Update(terminalFrameMsg{task: first.ID, frame: twoPaneFrame()})
	shown := loaded.(Model)
	if body := ansi.Strip(shown.terminalBody(87, 6)); !strings.Contains(body, "agent line one") {
		t.Fatalf("the selected task's pane was not drawn:\n%s", body)
	}

	// Open the panel, move to the second task, and come back. Nothing has asked
	// tmux what the second task's pane shows.
	backend.reviewStatus = api.ReviewStatus{Task: second}
	back := press(t, press(t, press(t, shown, "T"), "J"), "A")
	if back.screen != screenTerminal {
		t.Fatalf("A from the task panel reached %v, want the terminal", back.screen)
	}
	if got, _ := back.subject(); got.ID != second.ID {
		t.Fatalf("the subject is %s, want %s", got.Key, second.Key)
	}

	if body := ansi.Strip(back.terminalBody(87, 6)); strings.Contains(body, "agent line one") {
		t.Errorf("the terminal draws %s's pane under %s's name:\n%s", first.Key, second.Key, body)
	}

	// And the state itself, however it is reached: a frame held for one task
	// while the selection names another is not drawn. Leaving the tab is one way
	// in; every overlay that closes back onto it is another.
	held := shown
	held.selected = second.ID
	if body := ansi.Strip(held.terminalBody(87, 6)); strings.Contains(body, "agent line one") {
		t.Errorf("a frame held for %s was drawn under %s's name:\n%s", first.Key, second.Key, body)
	}
}
