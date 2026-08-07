package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/runtime"
)

// mount decodes one binding of `docker inspect`. It is the wire shape of
// runtime.ObservedMount, which is what every caller outside this package sees.
type mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Writable    bool   `json:"RW"`
}

// Inspect reports what the running containers turned out to mount.
//
// It exists for one failure that is otherwise silent. A repository's
// container_path is the path the *agent's* Compose files mount it at, and the
// application's Compose files are a different set that may use another path.
// Compose merges by target, so a path that disagrees leaves the base file's own
// mount in place and adds Feat's beside it — and the services then run the
// user's ordinary checkout while every record Feat keeps about the task is
// correct. The user changes something, nothing happens, and nothing anywhere
// says why (ADR-034, and ADR-033 evidence 1 for the same shape one zone over).
//
// It reads the containers rather than the resolved Compose configuration, for
// the reason ADR-033 gives: `docker compose config` renders the values of the
// project's environment files, and a container is evidence about what exists
// rather than a claim about what was asked for.
//
// This reports and does not refuse. The application runtime is inside the
// trusted host zone: mounting a checkout here is a correctness problem rather
// than a boundary breach, and refusing would stop a project whose Compose files
// legitimately mount a checkout Feat has no task worktree for.
func (r *Runtime) Inspect(ctx context.Context, state runtime.State) (runtime.Report, error) {
	var report runtime.Report

	for _, service := range state.Services {
		if service.Container == "" {
			continue
		}
		mounts, err := r.mounts(ctx, service.Container)
		if err != nil {
			return runtime.Report{}, err
		}
		for _, one := range mounts {
			one.Service = service.Name
			report.Mounts = append(report.Mounts, one)
		}
	}

	report.Notes = r.notes(report.Mounts)
	return report, nil
}

// mounts asks one container what it mounts.
func (r *Runtime) mounts(ctx context.Context, container string) ([]runtime.ObservedMount, error) {
	output, err := r.runner.Run(ctx, runtime.Invocation{
		Program: r.docker,
		Arguments: []string{
			"inspect", "--type", "container", "--format", "{{json .Mounts}}", container,
		},
	})
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		return nil, fmt.Errorf("inspecting container %s of Compose project %s failed: %s",
			container, r.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	trimmed := strings.TrimSpace(output.Stdout)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var decoded []mount
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, fmt.Errorf("reading the mounts of container %s: %w", container, err)
	}

	observed := make([]runtime.ObservedMount, 0, len(decoded))
	for _, one := range decoded {
		observed = append(observed, runtime.ObservedMount{
			Type: one.Type, Name: one.Name, Source: one.Source,
			Destination: one.Destination, Writable: one.Writable,
		})
	}
	return observed, nil
}

// notes says, in Feat's terms, what is worth knowing about what just started.
//
// One note per repository rather than one per mount: a project with four
// services mounting the same checkout has one problem, and four copies of it
// would be a wall of text a user learns to skip.
func (r *Runtime) notes(mounts []runtime.ObservedMount) []string {
	affected := make(map[string][]string)

	for _, observed := range mounts {
		checkout := r.checkout(observed)
		if checkout == "" {
			continue
		}
		affected[checkout] = append(affected[checkout], observed.Service)
	}
	if len(affected) == 0 {
		return nil
	}

	checkouts := make([]string, 0, len(affected))
	for checkout := range affected {
		checkouts = append(checkouts, checkout)
	}
	sort.Strings(checkouts)

	notes := make([]string, 0, len(checkouts))
	for _, checkout := range checkouts {
		services := unique(affected[checkout])
		notes = append(notes, fmt.Sprintf(
			"%s %s the ordinary checkout %s rather than this task's worktree, so a change the task makes "+
				"will not appear there. Feat mounts each task worktree at the container_path its repository "+
				"configures, and Compose replaces a mount only when the target matches; set that "+
				"container_path to the path these Compose files already use",
			strings.Join(services, " and "), verb(len(services)), checkout))
	}
	return notes
}

// checkout reports the ordinary checkout a mount exposes, and "" otherwise.
//
// The checkout itself, anything containing it, and anything inside it all count,
// because each of them puts the user's working copy in front of the application
// instead of the task's worktree.
func (r *Runtime) checkout(observed runtime.ObservedMount) string {
	if observed.Type != "bind" || observed.Source == "" {
		return ""
	}
	for _, checkout := range r.spec.ForbiddenSources {
		if samePath(observed.Source, checkout) ||
			contains(observed.Source, checkout) || contains(checkout, observed.Source) {
			return checkout
		}
	}
	return ""
}

// verb agrees with the number of services a note names.
func verb(services int) string {
	if services == 1 {
		return "mounts"
	}
	return "mount"
}

// unique returns the values in order, without repeats.
func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// virtualPrefixes are the paths a container runtime puts in front of a host path
// when it reports one back.
//
// Docker Desktop shares the host filesystem through its own virtual machine, so
// a bind source can be reported as /host_mnt/Users/... rather than /Users/....
// The prefixes are stripped explicitly rather than matched by suffix: a suffix
// comparison would make /elsewhere/repos/api look like the configured
// /repos/api, and a check that reports a repository the user does not have is a
// check they will learn to ignore.
var virtualPrefixes = []string{"/host_mnt", "/run/desktop/mnt/host"}

// normalize strips a container runtime's own prefix and cleans the path.
func normalize(value string) string {
	cleaned := path.Clean(value)
	for _, prefix := range virtualPrefixes {
		if cleaned == prefix {
			return "/"
		}
		if after, found := strings.CutPrefix(cleaned, prefix+"/"); found {
			return path.Clean("/" + after)
		}
	}
	return cleaned
}

// samePath reports whether two paths name the same location.
func samePath(a, b string) bool { return normalize(a) == normalize(b) }

// contains reports whether the outer path holds the inner one.
func contains(outer, inner string) bool {
	outer, inner = normalize(outer), normalize(inner)
	if outer == "/" {
		return true
	}
	return strings.HasPrefix(inner, outer+"/")
}
