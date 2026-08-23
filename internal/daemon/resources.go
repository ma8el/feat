package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/resources"
	runtimecompose "github.com/ma8el/feat/internal/runtime/compose"
)

// defaultResourceInterval is how often the machine and the tasks are sampled
// when no project configures an interval.
//
// It matches the dashboard's own refresh, so a screen that re-reads every two
// seconds is reading a figure that is at most one sample old.
const defaultResourceInterval = 2 * time.Second

// minimumResourceInterval bounds how eager a project may ask Feat to be.
//
// Asking the container runtime what its containers are using takes between one
// and two seconds however it is asked, measured rather than assumed, so an
// interval below this would mean a sampler that is always sampling (ADR-035).
const minimumResourceInterval = time.Second

// Resources returns the most recent sample.
//
// It never fails. A daemon that has not sampled yet says so, and a sample that
// could only be taken in part carries its notes: the acceptance criterion is
// that collection failure degrades gracefully, and a request that returned an
// error would be a dashboard that showed nothing rather than what it has.
func (s *service) Resources(_ context.Context) (api.ResourceReport, error) {
	s.sampleMu.Lock()
	sample, sampled := s.sample, s.sampled
	s.sampleMu.Unlock()

	if !sampled {
		return api.ResourceReport{
			Notes: []string{"no resource sample has been taken yet"},
		}, nil
	}
	return resourceReport(sample), nil
}

// resourceReport maps a sample onto the wire.
//
// Every figure the observer marked unknown becomes a null rather than a zero,
// which is the whole reason the observer marks them: an unmeasured value is
// never rendered as a measured one (ADR-028, ADR-031).
func resourceReport(sample resources.Sample) api.ResourceReport {
	report := api.ResourceReport{
		Machine:     api.MachineResources{Cores: sample.Machine.Cores},
		Notes:       sample.Notes,
		CollectedAt: sample.TakenAt,
		Sampled:     true,
	}
	if sample.Machine.LoadKnown {
		report.Machine.Load = &api.LoadAverage{
			One: sample.Machine.Load1, Five: sample.Machine.Load5, Fifteen: sample.Machine.Load15,
		}
	}
	if sample.Machine.MemoryKnown {
		report.Machine.Memory = &api.Capacity{
			TotalBytes:     sample.Machine.MemoryTotal,
			AvailableBytes: sample.Machine.MemoryAvailable,
		}
	}
	if sample.Machine.DiskKnown {
		report.Machine.Disk = &api.DiskCapacity{
			Path:           sample.Machine.DiskPath,
			TotalBytes:     sample.Machine.DiskTotal,
			AvailableBytes: sample.Machine.DiskAvailable,
		}
	}

	report.Tasks = make([]api.TaskResources, 0, len(sample.Tasks))
	for _, usage := range sample.Tasks {
		task := api.TaskResources{
			TaskID:         usage.Task,
			ContainerBytes: usage.ContainerMemoryBytes,
			ProcessBytes:   usage.ProcessMemoryBytes,
			Containers:     make([]api.ContainerResources, 0, len(usage.Containers)),
			Processes:      usage.Processes,
		}
		if usage.CPUKnown {
			percent := usage.CPUPercent
			task.CPUPercent = &percent
		}
		if usage.MemoryKnown {
			memory := usage.MemoryBytes
			task.MemoryBytes = &memory
		}
		for _, container := range usage.Containers {
			task.Containers = append(task.Containers, api.ContainerResources{
				ID:          container.ID,
				Name:        container.Name,
				Kind:        container.Kind,
				CPUPercent:  container.CPUPercent,
				MemoryBytes: container.MemoryBytes,
			})
		}
		report.Tasks = append(report.Tasks, task)
	}
	return report
}

// sampleResources takes one sample and keeps it.
//
// Nothing is persisted. Derived resource samples are not part of the stored
// state (docs/06-technical-architecture.md), and nothing is published either: a
// figure that changes every two seconds would make every dashboard re-read every
// two seconds through the event stream as well as through its own refresh, which
// is a loop the dashboard has paid for once.
func (s *service) sampleResources(ctx context.Context) {
	targets, err := s.resourceTargets(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "resolving the tasks to sample resources for", slog.Any("error", err))
	}

	sample := s.observer.Observe(ctx, targets)

	s.sampleMu.Lock()
	s.sample, s.sampled = sample, true
	s.sampleMu.Unlock()
}

// resourceTargets names the tasks worth attributing usage to, and the processes
// each owns.
//
// The processes come from tmux, because a task's host-side work is whatever its
// panes started. The containers do not: they are found by Feat's own ownership
// labels, so a task's application services are attributed without the daemon
// having to remember which containers Compose created for it.
//
// A tmux that cannot be read costs the process half of the sample and not the
// container half, which is why the error is returned rather than thrown: a
// sample of what could be seen is worth more than none.
func (s *service) resourceTargets(ctx context.Context) ([]resources.Target, error) {
	tasks, err := s.Tasks(ctx)
	if err != nil {
		return nil, err
	}

	var live []*domain.Task
	for _, task := range tasks {
		if task.Session == nil || task.Workflow == domain.WorkflowArchived {
			// A draft owns no container and no process, and an archived task's
			// resources are gone or are cleanup's to explain. Neither is reported
			// as using zero, because neither was measured.
			continue
		}
		live = append(live, task)
	}
	if len(live) == 0 {
		return nil, nil
	}

	panes := make(map[domain.TaskID][]int, len(live))
	found, discoverErr := s.terminals.Discover(ctx)
	// A quarantined terminal contributes no processes and costs the healthy ones
	// nothing: measuring what can be measured is the same rule everywhere else
	// in this pass, where a machine whose load cannot be read still reports its
	// memory.
	for _, terminal := range found.Terminals {
		pids := make([]int, 0, 2)
		if terminal.Agent.PID > 0 {
			pids = append(pids, terminal.Agent.PID)
		}
		if terminal.Shell != nil && terminal.Shell.PID > 0 {
			pids = append(pids, terminal.Shell.PID)
		}
		panes[terminal.Task] = pids
	}

	targets := make([]resources.Target, 0, len(live))
	for _, task := range live {
		targets = append(targets, resources.Target{Task: task.ID.String(), PIDs: panes[task.ID]})
	}
	return targets, discoverErr
}

// resourceInterval is how long to wait before the next sample.
//
// It is the shortest interval any registered project asks for, floored so that a
// container runtime which takes longer to answer than the interval cannot make
// samples pile up behind each other. The interval being per-project
// configuration for a machine-wide measurement is an oddity of the configuration
// model rather than a design: the most eager project wins, which is the reading
// that never makes one project's setting weaken another's (ADR-035).
func (s *service) resourceInterval(ctx context.Context, took time.Duration) time.Duration {
	interval := defaultResourceInterval
	floor := minimumResourceInterval

	switch {
	case s.resourceOverride > 0:
		// A test asked for a specific cadence, and the floor that protects a real
		// Docker from being asked faster than it can answer would defeat the point
		// of asking for one.
		interval, floor = s.resourceOverride, s.resourceOverride
	default:
		// The default applies only when nothing is registered. Every registered
		// project has a configured interval, because loading fills the documented
		// default into one that says nothing, so the shortest of them is what the
		// machine is asked for.
		configured := time.Duration(0)
		projects, err := s.store.Projects().List(ctx)
		if err == nil {
			for _, project := range projects {
				cfg, err := config.Load(s.layout.ProjectConfigDir(), project.ID.String(), s.configOptions())
				if err != nil {
					continue
				}
				if asked := cfg.Resources.Sample(); asked > 0 && (configured == 0 || asked < configured) {
					configured = asked
				}
			}
		}
		if configured > 0 {
			interval = configured
		}
	}

	if interval < floor {
		interval = floor
	}
	if took > interval {
		return took
	}
	return interval
}

// resourcePoller owns the goroutine that samples resources.
type resourcePoller struct {
	once   sync.Once
	cancel context.CancelFunc
	done   chan struct{}
}

// start begins sampling in the background.
func (p *resourcePoller) start(ctx context.Context, service *service) {
	polling, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		service.watchResources(polling)
	}()
}

// stop ends sampling and waits for it to finish.
func (p *resourcePoller) stop() {
	p.once.Do(func() {
		if p.cancel == nil {
			return
		}
		p.cancel()
		<-p.done
	})
}

// watchResources samples until the context ends.
//
// It sleeps for an interval it recomputes after each sample rather than ticking,
// so that a slow sample delays the next one instead of queueing behind it.
func (s *service) watchResources(ctx context.Context) {
	for {
		started := s.now()
		s.sampleResources(ctx)
		wait := s.resourceInterval(ctx, s.now().Sub(started))

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// resourceObserver builds the observer for this daemon.
//
// The label names come from the two Compose adapters, because they are what each
// writes onto the resources it creates. The observer itself is given them as
// strings: it treats an agent's container and an application's alike, which is
// what keeps it from having to know that they are different things (ADR-035).
func resourceObserver(opts Options) *resources.Observer {
	return resources.New(resources.Options{
		Runner:         opts.Resources,
		DiskPath:       opts.Layout.State,
		ContainerLabel: runtimecompose.LabelOwner + "=" + runtimecompose.OwnerValue,
		TaskLabel:      runtimecompose.LabelTask,
		KindLabel:      runtimecompose.LabelKind,
		Now:            opts.Now,
	})
}
