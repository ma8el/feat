package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/reconcile"
)

// abandonedLaunch arranges the case F2-15 found: a devcontainer launch that
// failed after its container existed.
//
// The refusal it uses is one of the launch's own, and it is deliberately one of
// the late ones — every rule that inspects the container fires after the
// container is up (ADR-033), which is why widening those refusals made this
// class of leftovers more frequent rather than less. What it leaves is a task
// with no session at all and a Compose project on the machine.
func abandonedLaunch(t *testing.T) (*drafting, *domain.Task) {
	t.Helper()

	arranged := arrangeDrafting(t)
	arranged.docker.Answer("inspect --type container --format {{json .Mounts}} c0ffee",
		`[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":true}]`)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	displayed := arranged.resolve(t, draft.ID)
	if _, err := arranged.service.LaunchDraft(context.Background(), draft.ID, api.Confirmation{Fingerprint: displayed.Fingerprint}); err == nil {
		t.Fatal("the launch succeeded, so it left nothing behind to find")
	}

	task := arranged.reload(t, draft.ID)
	if task.Session != nil {
		t.Fatal("the failed launch recorded a session, which is not the case under test")
	}
	arranged.leftBehind(task)
	return arranged, task
}

// leftBehind arranges the Docker of a machine that still holds what the launch
// created: the container it made, exited, and the network beside it.
func (d *drafting) leftBehind(task *domain.Task) {
	identity := agentIdentity(task)
	d.docker.
		Answer("ps --all --format json", `{"ID":"c0ffee","Name":"`+identity+`-dev-1","Service":"dev",`+
			`"State":"exited","Status":"Exited (137) 3 hours ago"}`).
		Answer("network ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}",
			identity+"_default\n").
		Answer("volume ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "").
		Answer("down", "")
}

// stillRunning arranges the same Docker while the container is up.
//
// It is the state ADR-059's evidence 4 measured the control-workspace removal
// failing in: the first cleanup, with the container live, failed with `unlinkat
// …/outbox: permission denied`. leftBehind is the other one — the same container
// hours later, exited — where the second cleanup succeeded.
func (d *drafting) stillRunning(task *domain.Task) {
	identity := agentIdentity(task)
	d.docker.Answer("ps --all --format json", `{"ID":"c0ffee","Name":"`+identity+`-dev-1","Service":"dev",`+
		`"State":"running","Status":"Up 2 hours"}`)
}

// tookThemAway arranges the same Docker once the containers and networks are
// gone.
func (d *drafting) tookThemAway(task *domain.Task) {
	identity := agentIdentity(task)
	d.docker.
		Answer("ps --all --format json", "").
		Answer("network ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "")
}

// switchTo replaces the fixture project's configuration, the way a user editing
// the file does.
func (d *drafting) switchTo(t *testing.T, fixture string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(d.layout.ProjectConfigDir(), "app.yaml"),
		[]byte(fixture), 0o600); err != nil {
		t.Fatalf("replacing the project configuration: %v", err)
	}
}

// planOf resolves what a task of the drafting harness owns.
func (d *drafting) planOf(t *testing.T, task *domain.Task) api.CleanupPlan {
	t.Helper()

	plan, err := d.service.CleanupPlan(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("CleanupPlan: %v", err)
	}
	return plan
}

// TestALaunchThatFailedAfterItsContainerIsStillRemovable is the criterion this
// exists for: what such a launch leaves is removable by name from the task that
// created it.
//
// Before this, cleanup resolved a task's containers from the record alone. A
// launch that fails clears — or never reaches — the session that record lives
// on, so the plan named nothing, the cleanup reported success, and the container
// and its network stayed on the machine with nothing in the product able to name
// them.
func TestALaunchThatFailedAfterItsContainerIsStillRemovable(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	identity := agentIdentity(task)

	plan := arranged.planOf(t, task)
	containers, ok := classOf(plan, reconcile.ClassAgentContainers)
	if !ok {
		t.Fatal("the plan names no agent containers, though the launch left one")
	}
	if containers.Targets[0].Identity != identity {
		t.Errorf("the plan names %q, want the Compose project name this task derives, %q",
			containers.Targets[0].Identity, identity)
	}
	for _, expected := range []string{"container", "network", "Exited (137)"} {
		if !strings.Contains(containers.Targets[0].Detail, expected) {
			t.Errorf("the plan does not say what is there: %q does not mention %q",
				containers.Targets[0].Detail, expected)
		}
	}

	result, err := arranged.service.Cleanup(context.Background(), task.ID,
		selectAll(plan, reconcile.ClassAgentContainers))
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !arranged.docker.Ran("down") {
		t.Errorf("nothing removed the Compose project: %v", arranged.docker.Calls())
	}
	// The Compose project by name, and not counted against the removals: the same
	// class also removes the directory the launch generated to define it, and that
	// is reported too (ADR-037 evidence 16).
	var reported bool
	for _, entry := range result.Removed {
		if entry.Identity == identity {
			reported = entry.Removed
		}
	}
	if !reported {
		t.Errorf("the cleanup reported %+v, want the agent containers of %s removed", result.Removed, identity)
	}
}

// TestArchivingIsRefusedOverALaunchsLeftovers is the other half of the same
// defect.
//
// The dogfood run's cleanup did not merely fail to remove the container: it
// archived the task over it, and an archived task is one reconciliation stops
// looking at. So the resources were left with nothing recording that they
// belonged to anybody, which is exactly what the archive rule exists to prevent
// — it just could not see them.
func TestArchivingIsRefusedOverALaunchsLeftovers(t *testing.T) {
	arranged, task := abandonedLaunch(t)

	plan := arranged.planOf(t, task)
	_, err := arranged.service.Cleanup(context.Background(), task.ID,
		api.CleanupSelection{Token: plan.Token, Archive: true})
	if err == nil {
		t.Fatal("the task was archived while a launch's container was still on the machine")
	}
	if !strings.Contains(err.Error(), reconcile.ClassAgentContainers.Title()) {
		t.Errorf("error = %v, want it to name the containers that stopped the archive", err)
	}
	if after := arranged.reload(t, task.ID); after.Workflow == domain.WorkflowArchived {
		t.Error("a refused archive still archived the task")
	}
}

// TestTheControlWorkspaceIsNotRemovedWhileAContainerHoldsIt is the ordering rule.
//
// On macOS the file-sharing layer holds a directory that is an active bind-mount
// source: the first cleanup of a task whose container was still running failed
// with `unlinkat …/outbox: permission denied`, and the second, after it had
// died, succeeded. The class order removes containers before the workspace, but
// only when a user chose both — so the rule is established rather than assumed,
// and a cleanup that would fail part way through refuses before it removes
// anything.
//
// The arrangement was rewritten rather than kept (G4-11): it refused over the
// exited container of abandonedLaunch, which is the state the second cleanup
// *succeeded* in, so the test that named the ordering rule pinned a refusal the
// ordering does not call for. What holds the workspace is a container that is
// running, and the second half below is now the one the finding is about.
func TestTheControlWorkspaceIsNotRemovedWhileAContainerHoldsIt(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	arranged.stillRunning(task)

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	if !workspace.Exists() {
		t.Fatal("the failed launch left no control workspace, so there is nothing to hold")
	}

	plan := arranged.planOf(t, task)
	_, err = arranged.service.Cleanup(context.Background(), task.ID, selectAll(plan, reconcile.ClassControl))
	if err == nil {
		t.Fatal("the control workspace was removed while a container still mounted it")
	}
	for _, expected := range []string{"control workspace", agentIdentity(task) + "-dev-1", "agent containers"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}
	if !workspace.Exists() {
		t.Error("the refused cleanup removed the workspace anyway")
	}

	// And once the containers are gone, the same choice goes through: the rule is
	// an ordering, not a prohibition.
	arranged.tookThemAway(task)
	after := arranged.planOf(t, task)
	if _, err := arranged.service.Cleanup(context.Background(), task.ID,
		selectAll(after, reconcile.ClassControl)); err != nil {
		t.Fatalf("removing the control workspace of a task with no container: %v", err)
	}
	if workspace.Exists() {
		t.Error("the control workspace is still there")
	}
}

// TestAStoppedContainerDoesNotHoldTheControlWorkspace is G4-11 at the caller.
//
// ADR-057 added `feat task stop`, which keeps a task's containers, and the
// classes of a cleanup are independent choices (FR-CLEAN-002). So stopping a
// task overnight and cleaning up its control workspace alone the next morning is
// an ordinary thing to ask for, and it was refused: "mounted into container
// feat-agent-…-dev-1 (Exited (137) 3 hours ago)". The measurement ADR-059's rule
// comes from is that the removal succeeded once the container had died.
//
// The containers stay on the machine throughout — that is the point. What is
// established is that they have stopped, not that they are gone.
func TestAStoppedContainerDoesNotHoldTheControlWorkspace(t *testing.T) {
	arranged, task := abandonedLaunch(t)

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	if !workspace.Exists() {
		t.Fatal("the failed launch left no control workspace, so there is nothing to remove")
	}

	plan := arranged.planOf(t, task)
	if _, err := arranged.service.Cleanup(context.Background(), task.ID,
		selectAll(plan, reconcile.ClassControl)); err != nil {
		t.Fatalf("the control workspace of a task whose container has exited was not removed: %v", err)
	}
	if workspace.Exists() {
		t.Error("the control workspace is still there")
	}
	if arranged.docker.Ran("down") {
		t.Error("the cleanup removed containers the user did not select")
	}
}

// TestADockerThatCannotBeAskedReleasesNothing is the first of the two branches
// G6-13 found uncovered: the question could not be asked at all.
//
// The sibling below it — a Docker daemon that answers with a failure — has been
// tested since the fix branch. This one returns before that: no Docker binary,
// no project, and the release rule read the nil as "there is nothing to ask
// about" and removed the tree. The difference between the two nils is the whole
// of ADR-059's refusal rule, and only one of them was pinned.
func TestADockerThatCannotBeAskedReleasesNothing(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	arranged.docker.Missing("docker")

	err := arranged.service.controlWorkspaceReleased(context.Background(), task)
	if err == nil {
		t.Fatal("a machine with no Docker was read as nothing holding the workspace")
	}
	for _, expected := range []string{task.ID.String(), "not installed"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}
}

// TestAPlanThatCannotSeeAContainerSaysSoAndIsNotArchivable is the consequence
// pass 0 recorded for the same silent nil.
//
// "the plan names nothing, Archivable stays true, and the user can archive the
// task over a live container exactly as before" — which is ADR-059 evidence 2
// reproduced by a different route: the archive refusal reads the plan, so a plan
// that could see nothing refuses nothing.
func TestAPlanThatCannotSeeAContainerSaysSoAndIsNotArchivable(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	arranged.docker.Missing("docker")

	plan := arranged.planOf(t, task)
	if len(plan.Problems) == 0 {
		t.Fatalf("the plan reports no problem though it could not ask about the containers: %+v", plan)
	}
	if plan.Archivable {
		t.Error("the task is offered for archiving over containers nothing could look for")
	}
	if _, named := classOf(plan, reconcile.ClassAgentContainers); named {
		t.Error("the plan names containers it never managed to ask about")
	}
}

// TestAProjectSwitchedToHostModeStillAsksAboutTheLaunchsContainers is the second
// branch, and the one G6-13's failure scenario is written from.
//
// A task's environment is the one it was launched with. `agent.execution.mode`
// is a line in a file a user edits, and reading it here let an edit made after
// the launch decide whether Feat asked about a container the launch had already
// created — so a project switched to host mode released a workspace a live
// container still mounted, silently, on the one path ADR-059 exists for.
//
// The record is what may answer this, and for a session-less task the record
// does not: the session that would carry the mode was never created. So the
// question is asked.
func TestAProjectSwitchedToHostModeStillAsksAboutTheLaunchsContainers(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	arranged.stillRunning(task)
	arranged.switchTo(t, hostFixture)

	err := arranged.service.controlWorkspaceReleased(context.Background(), task)
	if err == nil {
		t.Fatal("a configuration edit released a workspace a running container still mounts")
	}
	if !strings.Contains(err.Error(), agentIdentity(task)+"-dev-1") {
		t.Errorf("the refusal does not name the container: %v", err)
	}
}

// TestVolumesAreNotReportedUnremovedWithoutSayingWhy is the other consequence of
// the same silent nil (G3-07).
//
// removeVolumes reached the same nil and skipped the removal, so the user's
// confirmed selection came back as Removed: false with no error and no problem —
// a choice declined without a reason, over volumes that are still on the
// machine.
//
// It is checked on the rule rather than through a whole cleanup, for the reason
// the Docker-failure test next to it is: a cleanup re-resolves its plan first
// and would refuse earlier, on the token, for a different reason.
func TestVolumesAreNotReportedUnremovedWithoutSayingWhy(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	arranged.docker.Missing("docker")

	plan := &reconcile.Plan{Targets: []reconcile.Target{{
		Class: reconcile.ClassVolumes, Identity: agentIdentity(task) + "_state", Present: true,
	}}}
	removed, err := arranged.service.removeVolumes(context.Background(), task, plan)
	if err == nil {
		t.Fatalf("the volumes were reported as %+v with no error", removed)
	}
	if !strings.Contains(err.Error(), task.ID.String()) {
		t.Errorf("the failure does not name the task: %v", err)
	}
	for _, entry := range removed {
		if entry.Removed {
			t.Errorf("volume %s was reported removed by a Docker that was never run", entry.Identity)
		}
	}
}

// TestADockerThatCannotAnswerReleasesNothing keeps "establish" from meaning
// "assume".
//
// A Docker that will not say what it holds leaves the question unanswered, and
// removing the tree on an unanswered question is what produced the half-removed
// workspace in the first place.
//
// It is checked on the rule rather than through a whole cleanup, because a
// cleanup would refuse earlier and for a different reason: the plan is resolved
// again immediately before it runs, so a Docker that stopped answering between
// the two produces a plan that no longer names the same resources, and the token
// says so. Both refusals are right; only this one is about the mount.
func TestADockerThatCannotAnswerReleasesNothing(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	arranged.docker.Fail("ps --all --format json", "Cannot connect to the Docker daemon", 1)

	err := arranged.service.controlWorkspaceReleased(context.Background(), task)
	if err == nil {
		t.Fatal("an unanswerable Docker was read as nothing holding the workspace")
	}
	for _, expected := range []string{"could not be observed", task.ID.String(), agentIdentity(task)} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}
}

// TestALaunchsLeftoversAreReportedByReconciliation is the seeing half of "leaves
// nothing the product cannot see".
//
// Reconciliation asked only about tasks whose record named an environment, so
// this class of resource was in no report at all. Nothing is restarted or
// removed here: the finding names the task the containers were created for and
// the command that resolves them (FR-STATE-004).
func TestALaunchsLeftoversAreReportedByReconciliation(t *testing.T) {
	arranged, task := abandonedLaunch(t)

	before := len(arranged.docker.Calls())
	report, err := arranged.service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var found bool
	for _, finding := range report.Findings {
		if finding.Class != string(reconcile.ClassAgentContainers) {
			continue
		}
		found = true
		if finding.Status != string(reconcile.StatusOrphaned) {
			t.Errorf("status = %q, want orphaned: nothing records these containers", finding.Status)
		}
		if finding.TaskID != task.ID.String() {
			t.Errorf("the finding names task %q, want %q: an orphan of the record still belongs to a task",
				finding.TaskID, task.ID)
		}
		if !strings.Contains(finding.Action, "cleanup") {
			t.Errorf("the finding offers no action a user can take: %q", finding.Action)
		}
	}
	if !found {
		t.Errorf("no finding names the containers the launch left: %+v", report.Findings)
	}
	for _, call := range arranged.docker.Calls()[before:] {
		if call == "down" || call == "up --detach dev" || call == "stop" {
			t.Errorf("reconciliation removed or started something: %q", call)
		}
	}
}

// TestAConfirmedTaskIsNotToldNothingIsLostWhileItsContainerIsNamed is the last
// residual of F2-02.
//
// One report said both things. The terminal finding of a task with no session
// offered "clean it up and prepare the task again; its agent never ran, so
// nothing it did is lost", and the finding under it named the container that
// launch had left on the machine. The reassurance is true of a task interrupted
// before its container existed and false of the one this whole path exists for,
// and the two are the same shape in the record — so the answer the pass already
// has is what decides which sentence a user reads.
func TestAConfirmedTaskIsNotToldNothingIsLostWhileItsContainerIsNamed(t *testing.T) {
	arranged, task := abandonedLaunch(t)

	report, err := arranged.service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	terminal, found := terminalFinding(report, task)
	if !found {
		t.Fatalf("no terminal finding names task %s: %+v", task.ID, report.Findings)
	}
	if strings.Contains(terminal.Action, "nothing it did is lost") {
		t.Errorf("the report says nothing was lost and names the container in the same pass: %q",
			terminal.Action)
	}
	if !strings.Contains(terminal.Action, agentIdentity(task)+"-dev-1") {
		t.Errorf("the finding does not say what the launch left: %q", terminal.Action)
	}

	// And the reassurance survives for the task it was written for: the same
	// task once nothing of it is on the machine.
	arranged.tookThemAway(task)
	report, err = arranged.service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	terminal, found = terminalFinding(report, task)
	if !found {
		t.Fatalf("no terminal finding names task %s: %+v", task.ID, report.Findings)
	}
	if !strings.Contains(terminal.Action, "nothing it did is lost") {
		t.Errorf("a task that left nothing is not told so: %q", terminal.Action)
	}
}

// terminalFinding returns the terminal finding about one task.
func terminalFinding(report api.Reconciliation, task *domain.Task) (api.ReconciliationFinding, bool) {
	for _, finding := range report.Findings {
		if finding.Class == string(reconcile.ClassTerminal) && finding.TaskID == task.ID.String() {
			return finding, true
		}
	}
	return api.ReconciliationFinding{}, false
}

// TestAnUnreadableProjectStillFindsWhatTheLaunchLeft is why the configuration is
// read as a way of not asking rather than as a way of answering.
//
// The name a launch used depends on the two identifiers and on nothing else, so
// a project file that cannot be read does not make the containers unfindable —
// and the file is often exactly what changed, since a change to it is what makes
// a launch slow enough to be interrupted. What the configuration saves is the
// pointless query about a host-mode task, and that is all it may cost when it
// goes missing.
func TestAnUnreadableProjectStillFindsWhatTheLaunchLeft(t *testing.T) {
	arranged, task := abandonedLaunch(t)
	if err := os.WriteFile(filepath.Join(arranged.layout.ProjectConfigDir(), "app.yaml"),
		[]byte("this is not a project\n"), 0o600); err != nil {
		t.Fatalf("replacing the project configuration: %v", err)
	}

	plan := arranged.planOf(t, task)
	containers, ok := classOf(plan, reconcile.ClassAgentContainers)
	if !ok {
		t.Fatalf("the plan names no agent containers: %+v", plan.Problems)
	}
	if containers.Targets[0].Identity != agentIdentity(task) {
		t.Errorf("the plan names %q, want %q", containers.Targets[0].Identity, agentIdentity(task))
	}
}

// TestATaskWithNoContainerIsNotAskedAbout keeps the derivation from becoming a
// scan.
//
// A draft has had nothing created for it and a project that runs its agent on
// this host has no Compose project at all, so neither is a question for Docker.
// It is checked at the adapter: that Docker was never asked is a stronger
// statement than that the answer was empty.
func TestATaskWithNoContainerIsNotAskedAbout(t *testing.T) {
	arranged := arrangeDrafting(t)
	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	arranged.resolve(t, draft.ID)

	before := len(arranged.docker.Calls())
	if _, err := arranged.service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if asked := arranged.docker.Calls()[before:]; len(asked) != 0 {
		t.Errorf("reconciliation asked Docker about a draft: %v", asked)
	}
}

// TestCleanupRemovesTheGeneratedExecutionInput is ADR-037 evidence 16's criterion for the
// execution root.
//
// A launch generates `<state>/execution/<project-id>/<task-id>/compose.override.yaml`
// and nothing ever removed the directory holding it, so every task that had ever
// launched left one: 47 of the 48 under the execution root of the dogfood machine
// belonged to tasks that had been cleaned up and archived. The override is the
// document the destroy is run against, so it goes with the Compose project it
// defines.
func TestCleanupRemovesTheGeneratedExecutionInput(t *testing.T) {
	arranged, task := abandonedLaunch(t)

	directory, err := arranged.service.executionDirectory(task)
	if err != nil {
		t.Fatalf("resolving the execution directory: %v", err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("the launch generated no execution input, so there is nothing to remove: %v", err)
	}

	plan := arranged.planOf(t, task)
	result, err := arranged.service.Cleanup(context.Background(), task.ID,
		selectAll(plan, reconcile.ClassAgentContainers))
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("the generated execution input %s is still there: %v", directory, err)
	}

	// And it is reported. A directory Feat deleted is part of the answer to what
	// a cleanup removed, even though no target named it.
	var reported bool
	for _, entry := range result.Removed {
		if entry.Identity == directory {
			reported = true
			if entry.Class != string(reconcile.ClassAgentContainers) {
				t.Errorf("the generated input was reported under %q, want the class that removed it", entry.Class)
			}
			if !entry.Removed {
				t.Error("the generated input was reported as not removed")
			}
		}
	}
	if !reported {
		t.Errorf("the cleanup removed %s without reporting it: %+v", directory, result.Removed)
	}
}

// TestAProjectsExecutionDirectoryOutlivesItsLastTask is the boundary the
// worktree walk already stops at, applied to the root ADR-037 evidence 16 adds.
//
// A project outlives every task in it, and the next task of that project is
// created inside its directory. Removing it because it is momentarily empty
// would delete a directory the next launch recreates.
func TestAProjectsExecutionDirectoryOutlivesItsLastTask(t *testing.T) {
	arranged, task := abandonedLaunch(t)

	directory, err := arranged.service.executionDirectory(task)
	if err != nil {
		t.Fatalf("resolving the execution directory: %v", err)
	}
	project := filepath.Dir(directory)

	plan := arranged.planOf(t, task)
	if _, err := arranged.service.Cleanup(context.Background(), task.ID,
		selectAll(plan, reconcile.ClassAgentContainers)); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(project); err != nil {
		t.Errorf("the project's own execution directory %s was removed with its last task: %v", project, err)
	}
	if _, err := os.Stat(arranged.layout.ExecutionRoot()); err != nil {
		t.Errorf("the execution root %s was removed: %v", arranged.layout.ExecutionRoot(), err)
	}
}

// TestGeneratedInputsAreOnlyRemovedFromTheirOwnRoot is the check that runs
// again immediately before a directory tree is deleted.
//
// The path reaching it is computed from a validated project and task identifier
// under a root the daemon resolved, so nothing a caller supplies can name one of
// these. The check is here because the cost of it is nothing next to the cost of
// being wrong, which is the rule the control workspace's own removal states.
func TestGeneratedInputsAreOnlyRemovedFromTheirOwnRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	for _, refused := range []struct {
		name      string
		directory string
	}{
		{"the root itself", root},
		{"a project directory", filepath.Join(root, "app")},
		{"below a task", filepath.Join(root, "app", "task", "deeper")},
		{"outside the root", filepath.Join(outside, "app", "task")},
		{"a relative path", filepath.Join("app", "task")},
		{"a shared directory", "/tmp"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			if err := os.MkdirAll(refused.directory, 0o700); err != nil && filepath.IsAbs(refused.directory) {
				t.Fatalf("arranging %s: %v", refused.directory, err)
			}
			removed, err := removeGeneratedInputs(refused.directory, root)
			if err == nil {
				t.Fatalf("removing %s was allowed", refused.directory)
			}
			if removed {
				t.Errorf("a refusal reported %s as removed", refused.directory)
			}
			if filepath.IsAbs(refused.directory) {
				if _, err := os.Stat(refused.directory); err != nil {
					t.Errorf("the refused directory %s was removed anyway: %v", refused.directory, err)
				}
			}
		})
	}

	// And the shape it is for goes through, twice: a directory that is not there
	// is not an error, because a cleanup re-run must finish rather than refuse.
	task := filepath.Join(root, "app", "task")
	if err := os.MkdirAll(task, 0o700); err != nil {
		t.Fatalf("arranging %s: %v", task, err)
	}
	removed, err := removeGeneratedInputs(task, root)
	if err != nil || !removed {
		t.Fatalf("removeGeneratedInputs(%s) = %v, %v; want it removed", task, removed, err)
	}
	removed, err = removeGeneratedInputs(task, root)
	if err != nil || removed {
		t.Fatalf("removing a directory that is already gone = %v, %v; want no error and nothing removed", removed, err)
	}
}
