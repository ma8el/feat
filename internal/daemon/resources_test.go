package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/resources"
)

// brokenResources is a machine on which nothing can be measured.
//
// Every command fails, which is the state the first two acceptance criteria of
// this slice are about: a metric must never block a task, and a collection
// failure must degrade rather than break something.
type brokenResources struct {
	mu    sync.Mutex
	calls int
}

func (b *brokenResources) Run(context.Context, resources.Invocation) (resources.Output, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return resources.Output{}, errors.New("this machine reports nothing")
}

func (b *brokenResources) ran() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// TestMetricsNeverBlockTaskCreation is the first slice 10 acceptance criterion.
//
// The whole lifecycle runs on a machine where every observation command fails:
// the draft is planned, launched, and taken to working, and the daemon still
// answers a request for resources. Nothing about capacity is consulted anywhere,
// because Feat enforces no concurrency limit in v0 (FR-UI-005).
func TestMetricsNeverBlockTaskCreation(t *testing.T) {
	broken := &brokenResources{}
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.Resources = broken
		// Sampling is disabled so that the test is about the path a task takes
		// rather than about a background goroutine racing it.
		options.ResourceInterval = -1
	})
	live.start(t)

	task := live.load(t)
	if task.Workflow != domain.WorkflowWorking {
		t.Fatalf("the task is %s, want working: a metric stopped a launch", task.Workflow)
	}

	report, err := live.service.Resources(context.Background())
	if err != nil {
		t.Fatalf("asking for resources on a machine that measures nothing failed: %v", err)
	}
	if report.Sampled {
		t.Errorf("a daemon that has taken no sample reports one: %+v", report)
	}
	if len(report.Notes) == 0 {
		t.Error("a daemon with no sample says nothing about why")
	}
}

// TestASampleThatFailsEntirelyIsStillASample is the second acceptance criterion
// at the daemon.
//
// The sample is taken, kept, and served with its notes. A request that returned
// an error would be a dashboard that showed nothing rather than what it has, and
// the figures a broken machine can still report — its core count — are the ones
// most worth having.
func TestASampleThatFailsEntirelyIsStillASample(t *testing.T) {
	broken := &brokenResources{}
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.Resources = broken
		options.ResourceInterval = -1
	})
	live.start(t)

	live.service.sampleResources(context.Background())

	report, err := live.service.Resources(context.Background())
	if err != nil {
		t.Fatalf("Resources: %v", err)
	}
	if !report.Sampled {
		t.Fatal("a sample that could measure nothing was not recorded as a sample")
	}
	if len(report.Notes) == 0 {
		t.Error("a sample that measured nothing carries no note saying so")
	}
	if report.Machine.Cores <= 0 {
		t.Error("a degraded sample does not even report the machine's core count")
	}
	if report.Machine.Load != nil || report.Machine.Memory != nil {
		t.Errorf("a sample reports figures it could not measure: %+v", report.Machine)
	}
	if broken.ran() == 0 {
		t.Error("the sample was never attempted")
	}
}

// TestReadingResourcesRunsNoCommand checks that the slow work happens on the
// sampler's schedule and never inside a request.
//
// Asking the container runtime what it is using costs between one and two
// seconds. A dashboard reads this every two seconds and after every event, so a
// request that collected would be a request a metric could stall (ADR-035).
func TestReadingResourcesRunsNoCommand(t *testing.T) {
	broken := &brokenResources{}
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.Resources = broken
		options.ResourceInterval = -1
	})
	live.start(t)

	before := broken.ran()
	for range 5 {
		if _, err := live.service.Resources(context.Background()); err != nil {
			t.Fatalf("Resources: %v", err)
		}
	}
	if broken.ran() != before {
		t.Errorf("reading resources ran %d observation commands", broken.ran()-before)
	}
}

// TestSamplingPublishesNothing checks that a figure which moves every two
// seconds does not become an event that moves every two seconds.
//
// The dashboard re-reads state on every event, so a sample that published would
// make every task a permanent source of reads — the shape slice 6 paid for once
// and slice 9 pinned for the runtime poller.
func TestSamplingPublishesNothing(t *testing.T) {
	broken := &brokenResources{}
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.Resources = broken
		options.ResourceInterval = -1
	})
	live.start(t)

	before := live.service.bus.Sequence()
	for range 3 {
		live.service.sampleResources(context.Background())
	}
	if after := live.service.bus.Sequence(); after != before {
		t.Errorf("sampling published %d events", after-before)
	}
}

// TestOnlyLiveTasksAreSampled checks that a draft is not reported as using
// nothing.
//
// A draft owns no container and no process, so there is nothing to measure; a
// row of zeroes beside it would be a measurement nobody took.
func TestOnlyLiveTasksAreSampled(t *testing.T) {
	broken := &brokenResources{}
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.Resources = broken
		options.ResourceInterval = -1
	})
	live.start(t)

	draft, err := live.service.CreateDraft(context.Background(), api.DraftRequest{
		Project: "app", Title: "Another idea", Brief: "Not launched.",
		Source: domain.TaskSource{Kind: domain.SourcePrompt},
	})
	if err != nil {
		t.Fatalf("creating a second draft: %v", err)
	}

	targets, err := live.service.resourceTargets(context.Background())
	if err != nil {
		t.Fatalf("resolving the sampling targets: %v", err)
	}
	for _, target := range targets {
		if target.Task == draft.ID.String() {
			t.Errorf("a draft is sampled: %+v", target)
		}
	}
	if len(targets) != 1 {
		t.Fatalf("sampled %d tasks, want the one that was launched", len(targets))
	}
}

// TestATaskIsSampledThroughItsTerminalsProcesses checks that the process side of
// a task's usage starts where its panes do.
func TestATaskIsSampledThroughItsTerminalsProcesses(t *testing.T) {
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.ResourceInterval = -1
	})
	live.start(t)

	task := live.load(t)
	live.tmux.SetPanePID(task.Session.Tmux.Pane, 4321)

	targets, err := live.service.resourceTargets(context.Background())
	if err != nil {
		t.Fatalf("resolving the sampling targets: %v", err)
	}
	if len(targets) != 1 || len(targets[0].PIDs) != 1 || targets[0].PIDs[0] != 4321 {
		t.Fatalf("the task's sampling target is %+v, want the pane's own process", targets)
	}
}

// TestTheSamplingIntervalIsFlooredByTheLastSample checks that a container
// runtime which answers slowly cannot make samples pile up.
//
// `docker stats` takes between one and two seconds, measured rather than
// assumed, and the configured default is two. A sampler that ticked regardless
// would spend a machine's time asking questions it had not finished answering.
func TestTheSamplingIntervalIsFlooredByTheLastSample(t *testing.T) {
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.ResourceInterval = -1
	})

	ctx := context.Background()
	if got := live.service.resourceInterval(ctx, 0); got < minimumResourceInterval {
		t.Errorf("the sampling interval is %s, below the floor of %s", got, minimumResourceInterval)
	}
	if got, slow := live.service.resourceInterval(ctx, 9*time.Second), 9*time.Second; got != slow {
		t.Errorf("after a sample taking %s the next one waits %s, want %s", slow, got, slow)
	}
}

// TestAProjectsConfiguredIntervalIsRead checks that the configuration field this
// slice gives its first reader is actually read.
func TestAProjectsConfiguredIntervalIsRead(t *testing.T) {
	fixture := hostFixture + `
resources:
  sample_interval: 30s
`
	live := launchWith(t, fixture, installed(), true, func(options *Options) {
		options.ResourceInterval = -1
	})

	if got := live.service.resourceInterval(context.Background(), 0); got != 30*time.Second {
		t.Errorf("the sampling interval is %s, want the configured 30s", got)
	}
}

// TestAnUnreadableSampleDoesNotHideTheTasks checks what a client is given when
// half the machine answered.
func TestAnUnreadableSampleDoesNotHideTheTasks(t *testing.T) {
	live := launchWith(t, hostFixture, installed(), true, func(options *Options) {
		options.Resources = &brokenResources{}
		options.ResourceInterval = -1
	})
	live.start(t)
	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	live.service.sampleResources(context.Background())
	report, err := live.service.Resources(context.Background())
	if err != nil {
		t.Fatalf("Resources: %v", err)
	}

	task := live.load(t)
	var found bool
	for _, usage := range report.Tasks {
		if usage.TaskID == task.ID.String() {
			found = true
			if usage.CPUPercent != nil || usage.MemoryBytes != nil {
				t.Errorf("a task on an unmeasurable machine reports figures: %+v", usage)
			}
		}
	}
	if !found {
		t.Errorf("the launched task is missing from the report: %+v", report.Tasks)
	}
	if !strings.Contains(strings.Join(report.Notes, " "), "unavailable") {
		t.Errorf("the report does not say what could not be collected: %v", report.Notes)
	}
}
