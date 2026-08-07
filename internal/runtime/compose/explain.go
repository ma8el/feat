package compose

import (
	"fmt"
	"regexp"
	"strings"
)

// portPattern finds the host port in a bind failure.
//
// Docker reports "Bind for 0.0.0.0:8080 failed: port is already allocated" and,
// in other versions, "failed to bind host port for 0.0.0.0:8080…". Both put the
// port after the last colon of an address, which is what this captures.
var portPattern = regexp.MustCompile(`[0-9.]+:([0-9]+)`)

// explain adds what Feat knows about a failure the container runtime reported in
// its own terms.
//
// The runtime's message is accurate and describes a resource, not a decision.
// Feat knows which decision produced that resource, and saying so turns "port is
// already allocated" into something the user can act on. Nothing is added when
// Feat has nothing to add: a guess dressed as an explanation is worse than the
// original message.
func (r *Runtime) explain(reported string) string {
	lowered := strings.ToLower(reported)

	switch {
	case strings.Contains(lowered, "port is already allocated"),
		strings.Contains(lowered, "address already in use"),
		strings.Contains(lowered, "failed to bind host port"):
		port := ""
		if match := portPattern.FindStringSubmatch(reported); len(match) == 2 {
			port = " " + match[1]
		}
		return fmt.Sprintf(
			". Host port%s is already taken, most likely by another task's runtime or by the same "+
				"application started by hand: a published port is global to this machine, and Feat does not "+
				"allocate ports of its own in this version. Stop whatever holds the port, or change the "+
				"published port in the project's own Compose files",
			port)

	case strings.Contains(lowered, "container name") && strings.Contains(lowered, "already in use"):
		return ". A container name is global to the Docker daemon, and Feat's generated override resets " +
			"the one the project's Compose files set for these services. A name that survives is set " +
			"somewhere Feat's override does not reach — a static override applied after it, or a name on " +
			"a service this task does not manage"

	case strings.Contains(lowered, "read-only file system"):
		for _, mount := range r.spec.Mounts {
			if !mount.ReadOnly || !strings.Contains(reported, mount.Target+"/") {
				continue
			}
			return fmt.Sprintf(
				". Feat mounted %s read-only because this task selected that repository read-only, and the "+
					"application's own Compose files write inside it. Select the repository read-write for "+
					"this task, or move that path out of %s",
				mount.Target, mount.Target)
		}

	case strings.Contains(lowered, "is outside of rootfs"), strings.Contains(lowered, "no such file or directory"):
		for _, mount := range r.spec.Mounts {
			if !strings.Contains(reported, mount.Target+"/") {
				continue
			}
			return fmt.Sprintf(
				". The project's own Compose files mount something inside %s, and Feat mounts this task's "+
					"own worktree there rather than the ordinary checkout. A worktree holds only what Git "+
					"tracks, so a file that mount expects to find — an ignored .env, for instance — is not "+
					"there. Remove that mount, or make it one the worktree can satisfy",
				mount.Target)
		}
	}
	return ""
}
