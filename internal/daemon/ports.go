package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/runtime"
)

// A host port is global to the machine, so two tasks that both publish one
// cannot run at once. That is the failure the reference project met the moment
// it had a second task: the entry point of one stack held a fixed port, and the
// second task's runtime could not start at all (ADR-065 evidence 8).
//
// Feat therefore allocates the host ports itself, one per reachable service per
// task, from a range the project configures. What a repository declares is which
// of its services a user reaches — the port those services listen on is a fact
// about the project's own Compose files, and the port they appear on is Feat's
// to decide, because it is the only one that has to be unique across the
// machine.
//
// Three rules make the allocation safe to act on:
//
//   - it is recorded on the task before anything is created, so an interruption
//     leaves a record naming a superset of what exists (ADR-029);
//   - it is held for as long as the runtime exists, because the recorded inputs
//     win while there are resources (ADR-034);
//   - it is released when the runtime becomes absent, because a port nothing is
//     bound to belongs to whichever task asks next.

// portNeed is one publication a task's reachable service needs a host port for.
type portNeed struct {
	service       string
	containerPort int
	protocol      string
	hostIP        string
}

// runtimeNeeds is what the task's reachable services have to be published on.
//
// The container ports come from the project's own Compose files, read
// structurally: they are stated there and nowhere else, and asking the user to
// repeat them in Feat's configuration would be asking for a value that can go
// out of date silently. Only the target port, the protocol, and the host address
// are taken. The host port the project wrote is deliberately not read — it is
// the thing an allocated port replaces.
//
// A reachable service whose publication cannot be read is left out rather than
// guessed at, and the task says so: an interpolated entry is a value Feat must
// not resolve, and a port range is several publications where an allocation is
// one.
func runtimeNeeds(cfg *config.Config, documents composeDocuments) []portNeed {
	var needs []portNeed
	seen := make(map[string]bool)

	for _, contribution := range cfg.RuntimeComposition() {
		composition := documents[contribution.RepositoryID]
		for _, name := range contribution.Reachable {
			service, known := composition.Service(name)
			if !known {
				continue
			}
			for _, publication := range service.Ports {
				need := portNeed{
					service:       name,
					containerPort: publication.ContainerPort,
					protocol:      publication.Protocol,
					hostIP:        publication.HostIP,
				}
				// A service two repositories both declare reachable, whose files
				// both publish it, is one service reached at one address.
				key := fmt.Sprintf("%s/%d/%s", need.service, need.containerPort, need.protocol)
				if seen[key] {
					continue
				}
				seen[key] = true
				needs = append(needs, need)
			}
		}
	}
	return needs
}

// reserveAndRecord gives a task the host ports its reachable services need and
// writes them down without letting go in between.
//
// The two halves are one critical section because they are one act. A port is
// chosen by reading what every other task has recorded, so a port that has been
// chosen and not yet saved is held by nobody as far as the next task can see:
// two tasks created at the same moment would both read the same free port and
// both write it down, and the second one's containers could not bind it (G3-01).
// Closing that window means holding the lock across the save, because the save
// is what makes the choice true for anybody else.
//
// The lock is the daemon's rather than the task's for the same reason: per-task
// locks do not serialise two different tasks, which is the whole of the problem.
func (s *service) reserveAndRecord(
	ctx context.Context, task *domain.Task, spec runtime.Spec, needs []portNeed,
	ports config.PortRange, action api.RuntimeAction,
) (*domain.RuntimeEnvironment, error) {
	s.portMu.Lock()
	defer s.portMu.Unlock()

	allocations, err := s.reservePorts(ctx, task, needs, ports)
	if err != nil {
		return nil, err
	}
	if s.reserving != nil {
		// The seam. A test cannot otherwise stand between the two halves: every
		// Docker command the fakes can interrupt runs after both of them, so the
		// window this method exists to close is invisible from outside it.
		s.reserving(task.ID)
	}
	return s.recordRuntimeInputs(ctx, task, spec, allocations, action)
}

// reservePorts chooses the host ports a task's reachable services need.
//
// The caller holds s.portMu across this and the record that follows it, because
// a choice nobody has saved yet is a choice no other task can see.
//
// A runtime with resources keeps what it was created with, whatever the project
// says now. Re-resolving it would move a task's address while its containers
// were bound to the old one, which is the same rule the recorded inputs already
// follow for everything else a runtime was created from (ADR-034).
func (s *service) reservePorts(
	ctx context.Context, task *domain.Task, needs []portNeed, ports config.PortRange,
) ([]domain.PortAllocation, error) {
	if task.Runtime != nil && task.Runtime.State != domain.RuntimeAbsent {
		return task.Runtime.Allocations, nil
	}
	if len(needs) == 0 {
		return nil, nil
	}

	held, err := s.heldPorts(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	var keep []domain.PortAllocation
	if task.Runtime != nil {
		keep = task.Runtime.Allocations
	}
	return allocate(task, needs, keep, held, ports)
}

// holder is the task holding one host port, for a message that has to name it.
type holder struct {
	task domain.TaskKey
	port int
}

// heldPorts are the host ports every other task's runtime has reserved.
//
// Every task is asked, not only the ones of this project: a host port is global
// to the machine, and two projects whose ranges overlap are two projects that
// would otherwise be given the same port.
func (s *service) heldPorts(ctx context.Context, exclude domain.TaskID) (map[portKey]holder, error) {
	tasks, err := s.Tasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading which host ports the other tasks hold: %w", err)
	}

	held := make(map[portKey]holder)
	for _, task := range tasks {
		if task.ID == exclude || task.Runtime == nil {
			continue
		}
		for _, allocation := range task.Runtime.Allocations {
			held[portKey{port: allocation.HostPort, protocol: allocation.Protocol}] = holder{
				task: task.Key(), port: allocation.HostPort,
			}
		}
	}
	return held, nil
}

// portKey identifies a host port as the machine sees it: per protocol, and
// across every address, because a service bound to one address and a service
// bound to all of them collide on the same number.
type portKey struct {
	port     int
	protocol string
}

// allocate chooses a host port for each need.
//
// A port the task already held is kept when it is still free, so that the
// address a user was shown before they created anything is the address they get.
// Everything else takes the lowest free port in the range, which makes the
// allocation deterministic: the same tasks in the same order produce the same
// ports, and a test can say which.
func allocate(
	task *domain.Task, needs []portNeed, keep []domain.PortAllocation,
	held map[portKey]holder, ports config.PortRange,
) ([]domain.PortAllocation, error) {
	if ports.Empty() {
		return nil, fmt.Errorf("%w: task %s reaches %d of its services, and its project allocates no host "+
			"ports to publish them on: set runtime.port_range", api.ErrInvalid, task.ID, len(needs))
	}

	taken := make(map[portKey]bool, len(needs))
	allocations := make([]domain.PortAllocation, 0, len(needs))

	for _, need := range needs {
		port, err := choosePort(need, keep, held, taken, ports)
		if err != nil {
			return nil, fmt.Errorf("%w: task %s cannot publish its service %s: %w",
				api.ErrInvalid, task.ID, need.service, exhausted(err, held, ports))
		}
		taken[portKey{port: port, protocol: need.protocol}] = true
		allocations = append(allocations, domain.PortAllocation{
			Service:       need.service,
			ContainerPort: need.containerPort,
			HostPort:      port,
			Protocol:      need.protocol,
			HostIP:        need.hostIP,
		})
	}
	return allocations, nil
}

// errRangeExhausted reports that every port in the range is spoken for.
var errRangeExhausted = errors.New("every host port in the range is already held")

// choosePort picks one free host port.
func choosePort(
	need portNeed, keep []domain.PortAllocation, held map[portKey]holder,
	taken map[portKey]bool, ports config.PortRange,
) (int, error) {
	free := func(port int) bool {
		key := portKey{port: port, protocol: need.protocol}
		_, byAnother := held[key]
		return !byAnother && !taken[key]
	}

	for _, previous := range keep {
		if previous.Service != need.service || previous.ContainerPort != need.containerPort ||
			previous.Protocol != need.protocol {
			continue
		}
		if ports.Contains(previous.HostPort) && free(previous.HostPort) {
			return previous.HostPort, nil
		}
	}
	for port := ports.First; port <= ports.Last; port++ {
		if free(port) {
			return port, nil
		}
	}
	return 0, errRangeExhausted
}

// exhausted says what is holding the range, in the terms a user can act on.
//
// The tasks, not the ports: a thousand numbers is not a diagnosis, and what a
// user does about an exhausted range is destroy a runtime they are done with or
// widen the range.
func exhausted(err error, held map[portKey]holder, ports config.PortRange) error {
	if !errors.Is(err, errRangeExhausted) {
		return err
	}

	counts := make(map[domain.TaskKey]int)
	for key, holder := range held {
		if ports.Contains(key.port) {
			counts[holder.task]++
		}
	}
	keys := make([]domain.TaskKey, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	// Enough to recognise which tasks to look at, and not the whole list: the
	// point of the sentence is what to do next.
	const named = 5
	var holders []string
	for i, key := range keys {
		if i == named {
			holders = append(holders, fmt.Sprintf("and %d more", len(keys)-named))
			break
		}
		holders = append(holders, fmt.Sprintf("%s holds %d", key, counts[key]))
	}

	if len(holders) == 0 {
		return fmt.Errorf("every host port in %s is held, and no task holds one: widen runtime.port_range",
			ports)
	}
	return fmt.Errorf("every host port in %s is held: %s. Destroy the runtime of a task you have "+
		"finished with, which releases its ports, or widen runtime.port_range",
		ports, names(holders))
}

// reachabilityNotes say which services a user asked to reach and will not.
//
// A reachable service with no allocation is one whose publication Feat could not
// read in the project's own Compose files: it interpolates, it is a range, or
// the files define no published port for that service at all. Every one of them
// is silent otherwise — the services start, the application serves, and the
// address the user expected answers nothing — which is the failure the whole
// runtime section is written against (ADR-065 evidence 7).
//
// It is read from the record rather than resolved again, so what it says and
// what the generated override publishes come from one place.
func reachabilityNotes(cfg *config.Config, record *domain.RuntimeEnvironment) []string {
	var unreachable []string
	for _, service := range cfg.RuntimeReachable() {
		if _, allocated := record.Allocation(service); !allocated {
			unreachable = append(unreachable, service)
		}
	}
	if len(unreachable) == 0 {
		return nil
	}

	subject := fmt.Sprintf("the %s service is declared reachable", unreachable[0])
	object, verb := "it", "it publishes"
	if len(unreachable) > 1 {
		subject = fmt.Sprintf("the %s services are declared reachable", names(unreachable))
		object, verb = "them", "they publish"
	}
	return []string{fmt.Sprintf(
		"%s, and the project's own Compose files publish no port Feat could read for %s, so %s nothing. "+
			"An entry containing a \"${...}\" is one Feat must not resolve, and a port range is several "+
			"publications where an allocated port is one: write the container port plainly, or remove the "+
			"service from runtime.reachable",
		subject, object, verb)}
}

// withAllocations puts the recorded host ports into the specification the
// generated documents are written from.
//
// Both halves of the same fact: the publications Compose is asked for, and the
// address each managed service is told its siblings are at. They are derived
// from the record rather than from the allocation that produced it, so what the
// document publishes and what the task says it published cannot disagree.
func withAllocations(spec runtime.Spec, allocations []domain.PortAllocation) runtime.Spec {
	managed := make(map[string]bool, len(spec.Services))
	for _, service := range spec.Services {
		managed[service] = true
	}

	published := make([]domain.PortAllocation, 0, len(allocations))
	publications := make([]runtime.Publication, 0, len(allocations))
	for _, allocation := range allocations {
		if !managed[allocation.Service] {
			// A project that stopped managing a service since the runtime was
			// created. The port stays recorded and released with the rest; what it
			// must not do is name a service in a document that has none, which is
			// the same rule the mounts and the build contexts follow.
			continue
		}
		published = append(published, allocation)
		publications = append(publications, runtime.Publication{
			Service:       allocation.Service,
			ContainerPort: allocation.ContainerPort,
			HostPort:      allocation.HostPort,
			Protocol:      allocation.Protocol,
			HostIP:        allocation.HostIP,
			Description:   "allocated for this task; reached at " + allocation.Address(),
		})
	}
	spec.Publications = publications

	for name, value := range allocationVariables(published) {
		spec.Variables[name] = value
	}
	return spec
}

// allocationVariables tell every managed service where its siblings are.
//
// A service reaches another through the host, at the port Feat allocated, which
// is a different number for every task — so a value baked into an image or
// written into a project's own file cannot be right for more than one task at a
// time. FEAT_URL_<service> is the address, and FEAT_PORT_<service> the port
// alone, for a project that assembles its own.
//
// A service publishing more than one port also gets one pair per port, named by
// the container port, because the unsuffixed pair can only name one of them.
func allocationVariables(allocations []domain.PortAllocation) map[string]string {
	variables := make(map[string]string)
	counts := make(map[string]int)
	for _, allocation := range allocations {
		counts[allocation.Service]++
	}

	primary := make(map[string]bool)
	for _, allocation := range allocations {
		port := strconv.Itoa(allocation.HostPort)
		url, addressable := allocation.URL()

		if !primary[allocation.Service] {
			primary[allocation.Service] = true
			variables[domain.PortVariable(allocation.Service)] = port
			if addressable {
				variables[domain.URLVariable(allocation.Service)] = url
			}
		}
		if counts[allocation.Service] < 2 {
			continue
		}
		suffix := "_" + strconv.Itoa(allocation.ContainerPort)
		variables[domain.PortVariable(allocation.Service)+suffix] = port
		if addressable {
			variables[domain.URLVariable(allocation.Service)+suffix] = url
		}
	}
	return variables
}
