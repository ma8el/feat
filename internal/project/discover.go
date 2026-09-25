package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/ma8el/feat/internal/paths"
)

// Checkout is what an ordinary Git checkout can say about itself.
//
// `feat project init` asks a directory this before it proposes a repository. The
// identity of a repository is the user's to choose, and the branch it develops on and
// the remote it fetches are facts the checkout already holds, which a user asked to
// retype can mistype.
//
// A field this package could not establish is empty rather than guessed. An empty
// remote is a repository with none, and an empty branch is a checkout whose HEAD is
// detached or whose remote publishes no default. The caller decides what to propose
// instead, and says it is proposing rather than reporting.
type Checkout struct {
	// Root is the absolute path of the working tree's root, which is not
	// necessarily the directory that was asked: a subdirectory answers with the
	// tree it is in, and a path reached through a symbolic link comes back
	// resolved, because both are Git's own answer rather than a rewriting of the
	// question.
	Root string
	// Remote is the remote a base policy would fetch: "origin" when there is
	// one, otherwise the only other remote, otherwise empty.
	Remote string
	// RemoteURL is where that remote points, exactly as Git has it written
	// down: an HTTPS URL, an SSH URL, or the `user@host:path` form a clone over
	// SSH usually leaves behind.
	//
	// It is reported rather than interpreted. What a caller does with it is
	// propose the forge whose host it names, which is a proposal about
	// somebody's hosting arrangement rather than a fact about the checkout
	// (ADR-071, ADR-100).
	RemoteURL string
	// DefaultBranch is the branch the remote publishes as its head, or the
	// branch currently checked out when it publishes none.
	DefaultBranch string
}

// Inspect asks the repository containing a directory about itself. A directory that
// is not in a repository is an error: everything else this returns is optional, and
// this is the one answer that decides whether there is anything to configure at all.
func Inspect(ctx context.Context, runner Runner, dir string) (Checkout, error) {
	if runner == nil {
		runner = HostRunner{}
	}

	root, err := runner.Run(ctx, dir, gitExecutable, "rev-parse", "--show-toplevel")
	if err != nil {
		return Checkout{}, fmt.Errorf("%s is not inside a Git repository: %w", dir, err)
	}
	checkout := Checkout{Root: strings.TrimSpace(root)}
	if checkout.Root == "" {
		// A repository with no working tree — a bare one, or one asked through a
		// Git directory — has no ordinary checkout to configure, and reporting
		// the empty answer as success would put an empty host_path in a file.
		return Checkout{}, fmt.Errorf("%s has no working tree, so there is no ordinary checkout to configure", dir)
	}

	checkout.Remote = remoteOf(ctx, runner, checkout.Root)
	checkout.RemoteURL = remoteURLOf(ctx, runner, checkout.Root, checkout.Remote)
	checkout.DefaultBranch = branchOf(ctx, runner, checkout.Root, checkout.Remote)
	return checkout, nil
}

// remoteOf picks the remote a base policy would fetch.
//
// "origin" wins wherever it exists, because that is the name every clone
// creates and the name Feat defaults to. A repository with exactly one other
// remote answers with that one; a repository with several does not, because
// choosing between them is the user's decision and a wrong guess would be
// written into a file as though it had been established.
func remoteOf(ctx context.Context, runner Runner, root string) string {
	output, err := runner.Run(ctx, root, gitExecutable, "remote")
	if err != nil {
		return ""
	}
	remotes := lines(output)
	switch {
	case len(remotes) == 0:
		return ""
	case len(remotes) == 1:
		return remotes[0]
	}
	for _, remote := range remotes {
		if remote == defaultRemote {
			return remote
		}
	}
	return ""
}

// defaultRemote is the remote name every clone creates, and the one Feat
// defaults to when configuration names none.
const defaultRemote = "origin"

// remoteURLOf reads where a remote points. It is the fetch URL, which `get-url`
// reports without `--push`, because every clone has one and a repository's merge
// requests are opened where its code is read from.
//
// A repository with no remote is asked nothing, and a remote Git will not answer for
// answers nothing. Neither is a failure: the caller proposes from this and asks where
// it has nothing to propose.
func remoteURLOf(ctx context.Context, runner Runner, root, remote string) string {
	if remote == "" {
		return ""
	}
	output, err := runner.Run(ctx, root, gitExecutable, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// branchOf resolves the branch a base policy would use.
//
// The remote's own head is asked first: it is what the hosting side calls the
// default branch, and it is right even in a checkout that is sitting on a
// feature branch — which is the checkout a user is most likely to run the
// wizard from. It is a local ref, so nothing here reaches the network.
func branchOf(ctx context.Context, runner Runner, root, remote string) string {
	if remote != "" {
		head, err := runner.Run(ctx, root, gitExecutable,
			"symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
		if err == nil {
			// The ref reads "origin/main", and a branch name may itself contain
			// a slash, so only the remote's own prefix is removed.
			if branch := strings.TrimPrefix(strings.TrimSpace(head), remote+"/"); branch != "" {
				return branch
			}
		}
	}

	current, err := runner.Run(ctx, root, gitExecutable, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		// A detached HEAD answers nothing, which is not a failure: a worktree
		// checked out at a commit is a normal state for a repository to be in.
		return ""
	}
	return strings.TrimSpace(current)
}

// composeFileNames are the file names Docker Compose itself looks for, in its
// own order of preference.
var composeFileNames = []string{
	"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
}

// composeOverlayPattern matches the overlays a project keeps beside those: the
// development one, the production one, the extra one for a particular machine.
//
// They are found because a real project keeps in them the bind mounts a task worktree
// replaces, the reset of a published port, and sometimes the only service anybody
// runs, while the base files beside them build a static image. Offering only the base
// names proposed a runtime with no container path and no reachable service worth the
// name, which is the configuration ADR-065 evidence 1 describes.
var composeOverlayPattern = regexp.MustCompile(`^(compose|docker-compose)\.[^/]+\.ya?ml$`)

// composeSubdirectories are the directories a project keeps its Compose files
// in, beside the root of a checkout.
//
// `.devcontainer` is among them because the Dev Containers specification puts a
// project's container definition there, and a project following it often keeps a
// Compose file in that directory. Feat neither reads `devcontainer.json` nor
// implements that specification, and its own `devcontainer` mode only means the agent
// runs in a configured Compose service. So this is a place worth looking rather than
// a rule about what will be found there.
var composeSubdirectories = []string{".", ".devcontainer", "docker"}

// ComposeFiles returns the Compose files present under a directory.
//
// It looks one level deep, in the places a project keeps them, and returns what
// exists rather than what might. The caller offers these as candidates and the user
// confirms or replaces them, so a file this misses costs one line of typing and a
// file this invents would be a path in a configuration that does not resolve.
//
// The order is the order they should be offered in. A base file comes before the
// overlays that layer over it, because that is the order Compose is given them, and
// the overlays are sorted so two runs propose the same sequence.
func ComposeFiles(dir string) []string {
	var found []string
	for _, subdirectory := range composeSubdirectories {
		for _, name := range composeFileNames {
			if path, ok := composeFile(dir, subdirectory, name); ok {
				found = append(found, path)
			}
		}
		found = append(found, composeOverlays(dir, subdirectory)...)
	}
	return found
}

// composeOverlays returns the overlay files of one directory, in name order.
func composeOverlays(dir, subdirectory string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, subdirectory))
	if err != nil {
		return nil
	}

	var found []string
	for _, entry := range entries {
		if entry.IsDir() || !composeOverlayPattern.MatchString(entry.Name()) {
			continue
		}
		if path, ok := composeFile(dir, subdirectory, entry.Name()); ok {
			found = append(found, path)
		}
	}
	sort.Strings(found)
	return found
}

// composeFile returns one candidate path when a readable file is there.
func composeFile(dir, subdirectory, name string) (string, bool) {
	candidate := filepath.Join(dir, subdirectory, name)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return filepath.Clean(candidate), true
}

// maxComposeFileBytes bounds a Compose file read for its service names. A
// Compose file is a few kilobytes; a file this size is something else, and
// reading it into memory to look for a mapping key is not worth doing.
const maxComposeFileBytes = 1 << 20

// ComposeServices returns the service names the given Compose files declare.
//
// Only the keys of the top-level `services` mapping are read, and no value is ever
// carried out of the file. A Compose file names environment files, image registries,
// and sometimes a password that should not have been written there
// (docs/05-security-model.md).
//
// It is deliberately best effort and returns no error. A file that does not parse,
// uses a feature this does not understand, or is not there yet leaves the caller
// asking its question without a suggestion. Whether the file and service really exist
// is `feat doctor`'s answer, which it gets from Compose itself.
func ComposeServices(files ...string) []string {
	seen := make(map[string]bool)
	var names []string

	for _, file := range files {
		for _, name := range serviceNames(file) {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// serviceNames reads one Compose file's service names.
func serviceNames(file string) []string {
	document, ok := readCompose(file)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(document.Services))
	for name := range document.Services {
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Composition is what a repository's own Compose files say about themselves.
//
// It is read to propose configuration, never to act: a value derived here
// becomes configuration only when the user accepts it into their own YAML, and
// nothing derived here is persisted in Feat's own state (ADR-065).
//
// Five things are read and nothing else: service keys, the bind mounts of the
// repository — the container targets of those whose source is the repository itself,
// the paths inside it that others name, and the ones writing into where Feat mounts a
// task's worktree — whether a service is built from the repository, and the container
// ports it publishes to the host.
//
// No `environment` value, no `build.args` entry, and no `env_file` is opened. An
// entry containing a "${...}" is left unread rather than resolved, because Feat could
// not derive it without interpolating, so the user is asked instead.
type Composition struct {
	// Services are what the files declare, in name order.
	Services []ComposeService
	// Mounts are the paths inside the repository that a bind mount names one at a time,
	// rather than mounting the repository itself, which is what SourceTargets holds.
	//
	// They are the mounts a task cannot take for granted. A task works in a worktree,
	// and a worktree holds only what Git tracks, so a mount naming an ignored `.env` or
	// a `node_modules` built in place names something that will not be there. A file a
	// build step creates is a legitimate absence, so this is read to report and never to
	// refuse (internal/runtime/compose/explain.go).
	Mounts []MountedPath
	// Targets are the bind mounts that write into where Feat mounts a task's worktree,
	// rather than out of the repository, which is what Mounts holds.
	//
	// The container runtime has to create the mount point, and on a runtime whose binds
	// cross a virtual machine that path resolves outside the container's rootfs and it
	// will not create a file there. Unlike a mount in Mounts, which a build step may yet
	// satisfy, nothing in the container can repair this one: the failure precedes every
	// command in it, and the container is never created. Which runtimes do that is not
	// settled here, so the severity is taken from the runtime where the check runs
	// (ADR-098).
	//
	// Only the entries whose mount point would have to be a file are here, which is what
	// mountPointFor establishes. What is bound over it decides nothing, because a
	// `/dev/null` and a real file need the same thing.
	//
	// Only a caller that supplies ComposeReader.ContainerPath gets any of these: where
	// Feat mounts no worktree, there is no substitution for a mount to fall foul of.
	Targets []MountedTarget
	// UnreadMounts names the bind mounts left unread because they interpolate. They are
	// in Undecided as well, and separately here so a report about mounts can say what it
	// could not read rather than claim a coverage it does not have.
	UnreadMounts []string
	// Undecided names the entries left unread because they interpolate, so "Feat
	// proposed nothing" comes with somewhere to look.
	Undecided []string
}

// MountedPath is one path inside the repository that a bind mount names.
type MountedPath struct {
	// Path is the absolute host path the mount's source resolves to, resolved
	// the way Compose will resolve it.
	Path string
	// Where is the file and the service that wrote the entry, in the words the unread
	// entries are named in, because a reader sent to look at one has the same problem
	// either way.
	Where string
}

// MountedTarget is one container path a bind mount writes to, inside where Feat
// mounts a task's worktree.
type MountedTarget struct {
	// Target is the container path the entry names, cleaned. It is a path in
	// the container rather than on this host, and it is never resolved against
	// either.
	Target string
	// Relative is Target under the container path: the repository-relative path
	// a task's worktree would have to hold for the mount point to be creatable,
	// and so the path to ask Git about.
	Relative string
	// Source is the entry's source as it was written, which is how a reader sent to the
	// file finds the line. What it points at is not what fails, because a `/dev/null`
	// masking a file and the file itself fail alike, but that it is not a directory is
	// why this entry is here at all (mountPointFor).
	Source string
	// Where is the file and the service that wrote the entry, as MountedPath
	// names it.
	Where string
}

// ComposeService is one service, as much of it as Feat reads.
type ComposeService struct {
	// Name is the service name.
	Name string
	// SourceTargets are the container paths at which this service mounts the
	// repository itself, in the order they were read. They are the candidates
	// for the repository's runtime container path: the mount Feat's generated
	// override has to replace by target, or the services keep running the
	// user's ordinary checkout.
	SourceTargets []string
	// BuildContext is where the service's image is built from, as an absolute
	// host path resolved the way Compose will resolve it: against the
	// repository's own checkout, which is the project directory of its include
	// entry. It is empty when the files name no build context or name one Feat
	// could not read without interpolating.
	//
	// It is the whole of what decides what such a service runs, so it is what a
	// task's own worktree has to replace.
	BuildContext string
	// BuildsFromSource reports that the build context is the repository, or a
	// directory inside it. Such a service bakes its code with COPY and has no
	// mount to replace, so where its code comes from is decided by the build
	// context alone (ADR-065 evidence 4).
	BuildsFromSource bool
	// Ports are the publications this service declares, in the order they were
	// read. A service with any of them is a candidate for the reachable
	// declaration, and the container port of each is what Feat publishes a host
	// port of its own onto.
	//
	// The host port the project wrote is deliberately not among them. It is
	// global to the machine, so it is the thing an allocated port replaces:
	// reading it would only invite writing it back.
	Ports []Publication
}

// Publication is one port a service exposes to the host, as much of it as Feat
// reads.
//
// Only what an allocation has to preserve is kept. The container port decides
// what the host port is joined to; the protocol decides whether the publication
// is the same one at all, because a host port is per protocol; and the host
// address decides which interface it appears on, which is a project's own
// decision about who can reach the service and not Feat's to widen.
type Publication struct {
	// ContainerPort is the port inside the container.
	ContainerPort int
	// Protocol is "tcp" or "udp". Compose defaults to tcp, and so does this.
	Protocol string
	// HostIP is the address the project publishes on, empty when it names none.
	HostIP string
}

// Publishes reports whether the service exposes anything to the host.
func (s ComposeService) Publishes() bool { return len(s.Ports) > 0 }

// Service returns one service of a composition.
func (c Composition) Service(name string) (ComposeService, bool) {
	for _, service := range c.Services {
		if service.Name == name {
			return service, true
		}
	}
	return ComposeService{}, false
}

// SourceTarget returns the container path the named services agree they mount
// the repository at, and whether they agree on exactly one.
//
// Disagreement is not resolved. Two services mounting one repository at two
// paths is a project Feat has nothing to propose for, and proposing one of them
// would put a path in a file that is wrong for the other service and looks as
// established as any other value in it.
func (c Composition) SourceTarget(services []string) (string, bool) {
	found := ""
	for _, name := range services {
		service, known := c.Service(name)
		if !known {
			continue
		}
		for _, target := range service.SourceTargets {
			if found != "" && found != target {
				return "", false
			}
			found = target
		}
	}
	return found, found != ""
}

// Published returns the named services that publish a host port, in the order
// given.
func (c Composition) Published(services []string) []string {
	var found []string
	for _, name := range services {
		if service, known := c.Service(name); known && service.Publishes() {
			found = append(found, name)
		}
	}
	return found
}

// Names returns every service name of a composition.
func (c Composition) Names() []string {
	names := make([]string, 0, len(c.Services))
	for _, service := range c.Services {
		names = append(names, service.Name)
	}
	return names
}

// ComposeReader reads Compose files structurally, for one repository. The three
// fields are what a path inside those files needs to mean what it will mean to
// Compose, because reading them any other way answers a different question from the
// one Compose is going to be asked.
type ComposeReader struct {
	// Env is what a leading "~" expands against.
	//
	// Compose expands one — measured against Docker Compose v2.40, which renders
	// `~/.claude` as the user's home directory — so a reader that did not would
	// place such a path somewhere it will never be. Only the current user's home
	// is expandable, because that is all paths.Expand resolves: a "~other" is
	// left unread rather than guessed at.
	Env paths.Environment
	// ProjectDir is what a relative path resolves against: the
	// `project_directory` Compose will be given.
	ProjectDir string
	// Repository is the checkout being asked about, whose own mounts and build
	// contexts are the ones worth reporting.
	//
	// It is separate from ProjectDir because the two are one directory only by
	// coincidence. A repository's application files are given that repository's checkout
	// as their project directory, so a caller sets both to it. The agent's own Compose
	// files are given the directory of the first of them and are asked about each
	// configured repository in turn, so there the two differ and a reader that assumed
	// one would answer about the wrong repository.
	Repository string
	// ContainerPath is where a task's worktree of Repository is mounted inside
	// the container these files describe.
	//
	// It is empty unless the caller knows Feat will mount one there, which is the whole
	// of what turns Composition.Targets on. A reader deriving a container path does not
	// have one yet, a repository whose services bake their code has none, and a
	// repository no task takes gets no worktree. In each of those, a mount into that
	// path is the project's own arrangement standing as it was written, and nothing here
	// has anything to say about it.
	//
	// Like Repository, it is per repository rather than per file: a devcontainer
	// holds every repository a task takes, and each is mounted at its own path.
	ContainerPath string
}

// Read reads the given Compose files.
//
// It is best effort and returns no error, for the reason ComposeServices does:
// a file that does not parse, uses a feature this does not model, or is not
// there yet is a file with nothing to propose from, and the caller asks its
// question without a proposal.
func (r ComposeReader) Read(files ...string) Composition {
	var composition Composition
	index := make(map[string]int)

	for _, file := range files {
		document, ok := readCompose(file)
		if !ok {
			continue
		}
		for _, name := range sortedNames(document.Services) {
			position, known := index[name]
			if !known {
				position = len(composition.Services)
				index[name] = position
				composition.Services = append(composition.Services, ComposeService{Name: name})
			}
			// Later files merge over earlier ones, exactly as Compose merges
			// them: the dev overlay adds the mount the base image baked in.
			r.mergeService(&composition, position, file, document.Services[name])
		}
	}
	sort.Strings(composition.Undecided)
	sort.Strings(composition.UnreadMounts)
	return composition
}

// mergeService folds one file's entry for a service into what is known of it.
func (r ComposeReader) mergeService(c *Composition, position int, file string, raw yaml.RawMessage) {
	var entry composeServiceDocument
	if err := yaml.Unmarshal(raw, &entry); err != nil {
		return
	}
	service := &c.Services[position]
	where := file + ": service " + service.Name

	for _, volume := range entry.Volumes {
		source, target, ok := bindMount(volume)
		if !ok {
			if interpolated(volume) {
				c.Undecided = append(c.Undecided, where+": a volume")
				c.unread(where + ": a volume interpolates, so it was not read")
			}
			continue
		}
		resolved, ok := r.absolutePath(source)
		if !ok {
			// A path this could not resolve is not a path it may place. The only
			// way to reach this is a "~" that is not the current user's home,
			// which paths.Expand refuses rather than guesses at, or a machine
			// whose home directory cannot be established at all.
			c.unread(where + ": a volume names a path outside this user's home, so it was not read")
			continue
		}
		if relative, inside := insideContainerPath(r.ContainerPath, target); inside {
			// An entry writing into where Feat mounts the worktree. It is asked before
			// the question below because it fails before the container exists, so the
			// softer finding would report the same line at a severity it has outgrown.
			//
			// Only where the mount point would have to be a file, which is measured
			// rather than reasoned (see checkMountTargets). A directory the runtime
			// creates and the container starts, which is the ordinary soft case the
			// fall-through below reports.
			switch mountPointFor(resolved) {
			case mountPointFile:
				c.targeted(MountedTarget{
					Target: path.Clean(target), Relative: relative, Source: source, Where: where,
				})
				continue
			case mountPointUnknown:
				c.unread(where + ": a volume writes into " + path.Clean(r.ContainerPath) +
					" from a source this could not examine, so it was not read")
				continue
			case mountPointDirectory:
				// Nothing fails, so this entry is only whatever the questions
				// below make of it.
			}
		}
		if resolved == filepath.Clean(r.Repository) {
			if !contains(service.SourceTargets, target) {
				service.SourceTargets = append(service.SourceTargets, target)
			}
			continue
		}
		// Everything else that comes out of the repository. A mount of one file or
		// one directory inside it is not a candidate for the container path, because a
		// whole worktree mounted at one target would not replace it, but it is still a
		// path the mount needs to be there.
		if within(r.Repository, resolved) {
			c.mounted(MountedPath{Path: resolved, Where: where})
		}
	}

	switch context, read, undecided := buildContext(entry.Build); {
	case read:
		// A context this cannot resolve leaves the service with none, which is
		// the conservative half of the answer: an unread context is not
		// redirected at a task's worktree, and a service Feat says nothing about
		// builds exactly as its own files say it does.
		if resolved, ok := r.absolutePath(context); ok {
			service.BuildContext = resolved
			service.BuildsFromSource = within(r.Repository, resolved)
		}
	case undecided:
		c.Undecided = append(c.Undecided, where+": its build context")
	}

	for _, port := range entry.Ports {
		publication, ok := publishedPort(port)
		if !ok {
			// A publication Feat could not read is named rather than guessed at.
			// It is the entry an allocation would have replaced, so a caller that
			// allocates has to say that this one was left alone, and a caller that
			// proposes has to ask.
			c.Undecided = append(c.Undecided, where+": a published port")
			continue
		}
		if !containsPublication(service.Ports, publication) {
			service.Ports = append(service.Ports, publication)
		}
	}
}

// unread records one bind mount this could not read, once.
//
// Once per service and file, however many of that service's volumes were unreadable
// for the same reason: they are named by where they were written, so a service with
// three interpolated sources would otherwise repeat one sentence three times. The
// sentence carries its own reason, because a reader sent to look at the entry needs
// to know it interpolates.
func (c *Composition) unread(entry string) {
	if !contains(c.UnreadMounts, entry) {
		c.UnreadMounts = append(c.UnreadMounts, entry)
	}
}

// mounted records one path a bind mount needs, once.
//
// By path rather than by entry: Compose files that layer over one another repeat
// a service's volumes, and the same path bound by a base and by the overlay
// beside it is one path a worktree either holds or does not. The first place it
// was written is the one named, because that is where a reader starts looking.
func (c *Composition) mounted(one MountedPath) {
	for _, existing := range c.Mounts {
		if existing.Path == one.Path {
			return
		}
	}
	c.Mounts = append(c.Mounts, one)
}

// targeted records one mount point a bind mount needs created, once.
//
// By target rather than by entry or by source, because the target is what the
// container runtime has to create. Compose files that layer over one another
// repeat a service's volumes, and two entries writing to one target are one
// mount point — Compose keeps the last of them — while one source written to two
// targets is two. The first place it was written is the one named, because that
// is where a reader starts looking.
func (c *Composition) targeted(one MountedTarget) {
	for _, existing := range c.Targets {
		if existing.Target == one.Target {
			return
		}
	}
	c.Targets = append(c.Targets, one)
}

// containsPublication reports whether a service already declares a publication.
//
// Compose files that layer over one another repeat a service's ports, and the
// same container port declared twice is one publication rather than two: a
// second allocation for it would bind a host port nothing routes through.
func containsPublication(ports []Publication, one Publication) bool {
	for _, port := range ports {
		if port == one {
			return true
		}
	}
	return false
}

// defaultProtocol is what Compose assumes when a publication names none.
const defaultProtocol = "tcp"

// publishedPort reads one `ports` entry, in either syntax.
//
// It reports the container port, the protocol, and the host address, and
// nothing else: the host port a project wrote is what an allocated one
// replaces, so reading it would only invite writing it back.
//
// It reports a failure for an entry it cannot read, which includes an
// interpolated one and a port range. A range publishes several ports at once
// and Feat allocates one at a time; expanding one here would silently reserve
// as many host ports as the range is wide, and the honest answer is that this
// entry was not read.
func publishedPort(raw yaml.RawMessage) (Publication, bool) {
	if interpolated(raw) {
		return Publication{}, false
	}

	var short string
	if err := yaml.Unmarshal(raw, &short); err == nil {
		return shortPublication(short)
	}

	var long struct {
		Target   any    `yaml:"target"`
		Protocol string `yaml:"protocol"`
		HostIP   string `yaml:"host_ip"`
	}
	if err := yaml.Unmarshal(raw, &long); err != nil {
		return Publication{}, false
	}
	port, ok := portNumber(fmt.Sprintf("%v", long.Target))
	if !ok {
		return Publication{}, false
	}
	return Publication{
		ContainerPort: port,
		Protocol:      protocolOr(long.Protocol),
		HostIP:        long.HostIP,
	}, true
}

// shortPublication reads the "[[ip:]host:]container[/protocol]" syntax.
//
// The container port is the last colon-separated element, whatever precedes it,
// because that is the one part every form of the short syntax has. An IPv6
// address contains colons of its own and is written in brackets, which is why
// the address is taken as everything before the last two separators rather than
// as the first element.
func shortPublication(value string) (Publication, bool) {
	protocol := defaultProtocol
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		protocol = protocolOr(value[slash+1:])
		value = value[:slash]
	}

	parts := strings.Split(value, ":")
	port, ok := portNumber(parts[len(parts)-1])
	if !ok {
		return Publication{}, false
	}
	address := ""
	if len(parts) > 2 {
		address = strings.Join(parts[:len(parts)-2], ":")
	}
	return Publication{ContainerPort: port, Protocol: protocol, HostIP: address}, true
}

// portNumber reads a port, refusing anything that is not one whole port.
func portNumber(value string) (int, bool) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > maxPort {
		return 0, false
	}
	return port, true
}

// maxPort is the highest port number there is.
const maxPort = 65535

// protocolOr defaults an unnamed protocol to Compose's own.
func protocolOr(value string) string {
	if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
		return trimmed
	}
	return defaultProtocol
}

// composeDocument is the part of a Compose file Feat reads.
type composeDocument struct {
	Services map[string]yaml.RawMessage `yaml:"services"`
}

// composeServiceDocument is the part of one service Feat reads. Everything else
// in the mapping — the environment above all — is left in the file.
type composeServiceDocument struct {
	Build   yaml.RawMessage   `yaml:"build"`
	Volumes []yaml.RawMessage `yaml:"volumes"`
	Ports   []yaml.RawMessage `yaml:"ports"`
}

// readCompose reads one Compose file's services.
func readCompose(file string) (composeDocument, bool) {
	info, err := os.Stat(file)
	if err != nil || info.IsDir() || info.Size() > maxComposeFileBytes {
		return composeDocument{}, false
	}
	data, err := os.ReadFile(file) // #nosec G304 -- the user named this file, and only structure is read
	if err != nil {
		return composeDocument{}, false
	}

	var document composeDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		return composeDocument{}, false
	}
	return document, true
}

// bindMount reads one volume entry as a host source and a container target.
//
// Both syntaxes Compose accepts are read. A named volume is not a bind mount
// and is reported as neither, which is the same answer as an entry this does
// not understand: the caller proposes from what was read and asks about the
// rest.
func bindMount(raw yaml.RawMessage) (source, target string, ok bool) {
	if interpolated(raw) {
		return "", "", false
	}

	var short string
	if err := yaml.Unmarshal(raw, &short); err == nil {
		// "source:target" or "source:target:mode". A Windows drive letter is not
		// a case this has: these are the user's own Unix paths.
		parts := strings.Split(short, ":")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		if !strings.HasPrefix(parts[0], ".") && !strings.HasPrefix(parts[0], "/") &&
			!strings.HasPrefix(parts[0], "~") {
			// A named volume, which belongs to the Compose project rather than to
			// the host filesystem.
			return "", "", false
		}
		return parts[0], parts[1], true
	}

	var long struct {
		Type   string `yaml:"type"`
		Source string `yaml:"source"`
		Target string `yaml:"target"`
	}
	if err := yaml.Unmarshal(raw, &long); err != nil {
		return "", "", false
	}
	if long.Type != "bind" || long.Source == "" || long.Target == "" {
		return "", "", false
	}
	return long.Source, long.Target, true
}

// buildContext reads a service's build context, in either syntax.
//
// It reports the context, whether it was read, and whether it was left unread
// because reading it would mean resolving a "${...}".
//
// The interpolation is judged on the context alone rather than on the whole `build`
// mapping. A service can write a plain `context: .` beside a `build.args` entry
// carrying a "${...}", a value Feat never reads, and taking the mapping as one value
// would make the plainest build context undecidable. Such a service is often a
// multi-stage build with no mount to replace, so its build context is the only thing
// that decides what it runs (ADR-065 evidence 4).
func buildContext(raw yaml.RawMessage) (context string, read, undecided bool) {
	if len(raw) == 0 {
		return "", false, false
	}

	var short string
	if err := yaml.Unmarshal(raw, &short); err == nil {
		if strings.Contains(short, "${") {
			return "", false, true
		}
		return short, short != "", false
	}

	// Only the context. build.args is where a project puts a value it did not
	// want in an image, and Feat has no business reading one.
	var long struct {
		Context string `yaml:"context"`
	}
	if err := yaml.Unmarshal(raw, &long); err != nil {
		return "", false, false
	}
	if strings.Contains(long.Context, "${") {
		return "", false, true
	}
	return long.Context, long.Context != "", false
}

// interpolated reports an entry Feat must not read, because reading it would
// mean resolving a "${...}" it has no values for.
func interpolated(raw yaml.RawMessage) bool {
	return strings.Contains(string(raw), "${")
}

// absolutePath resolves a path written in a Compose file the way Compose will
// resolve it, and reports whether it could.
//
// Three forms, and each is resolved the way Compose resolves it. A leading "~"
// expands against the user's home directory, because Compose expands one, measured
// against Docker Compose v2.40 rendering `~/.claude` as the home directory. A reader
// that joined it to the project directory would put the home directory inside the
// repository. An absolute path is taken as it stands, and everything else resolves
// against the project directory rather than against the file's own directory, because
// that is the `project_directory` Compose is given.
//
// It fails for a "~other", which paths.Expand refuses rather than resolves, because
// configuration reaching into another user's home is more likely a mistake than an
// intention, and on a machine whose home directory cannot be established. A caller
// must not place a path this could not resolve, because resolving one is what makes
// it mean what Compose will mean by it.
func (r ComposeReader) absolutePath(value string) (string, bool) {
	switch {
	case value == "":
		return "", false
	case strings.HasPrefix(value, "~"):
		expanded, err := r.Env.Expand(value)
		if err != nil {
			return "", false
		}
		return filepath.Clean(expanded), true
	case filepath.IsAbs(value):
		return filepath.Clean(value), true
	default:
		return filepath.Clean(filepath.Join(r.ProjectDir, value)), true
	}
}

// within reports whether a resolved path is the repository or lies inside it.
//
// Inside counts, because a build context of ./frontend in a repository that
// holds several is that repository's code as surely as its root is, and the
// task's worktree holds the same subdirectory. A context above the checkout, or
// beside it, is not.
func within(repository, resolved string) bool {
	if repository == "" || resolved == "" {
		return false
	}
	repository = filepath.Clean(repository)
	return resolved == repository || strings.HasPrefix(resolved, repository+string(filepath.Separator))
}

// mountPointKind is what a container runtime would have to create for a bind
// mount's target, which its source decides.
type mountPointKind int

const (
	// mountPointDirectory is a mount point every measured runtime creates
	// without complaint, even inside a bind mount.
	mountPointDirectory mountPointKind = iota
	// mountPointFile is one some runtimes refuse to create inside a bind mount.
	// Which ones is not this package's answer to give here; see
	// RefusesFileMountPoint.
	mountPointFile
	// mountPointUnknown is a source this could not examine, which is neither
	// answer and must not be reported as either.
	mountPointUnknown
)

// mountPointFor reports what kind of mount point a bind mount's target needs.
//
// It says what the runtime would have to create and nothing about what that
// costs. Inside a bind-mounted worktree, with the target absent:
//
//   - a source that is not a directory — `/dev/null`, a regular file — needs a
//     file created at the target;
//   - a source that is a directory needs a directory. So does a source that does
//     not exist, which a runtime creates as one, and so do a named volume and a
//     tmpfs, which are not bind mounts and are not read here at all.
//
// So the source's kind decides this even though its contents decide nothing: a
// `/dev/null` bound over a file and a real file bound over it need the same
// thing, and the same entry pointed at a directory needs something else.
//
// Whether needing a file is a problem is the runtime's answer rather than this one's.
// Docker Desktop refuses to create a file mount point there, because its binds cross
// a virtual machine and the path resolves outside the container's rootfs, while a
// native Linux daemon creates the file and the container starts. Neither refuses a
// directory (ADR-098, evidence 10).
//
// That question is asked where the severity is decided
// (project.RefusesFileMountPoint, checkMountTargets), and it cannot be asked here: a
// ComposeReader has no runner, because reading a Compose file structurally should not
// need to run anything. This function stats one path, which is a fact about the entry
// in front of it, while the runtime is a fact about the machine, asked once for a
// whole diagnosis. A diagnosis that could not ask, because the daemon is absent or
// stopped, still reads these entries exactly as it does now.
//
// The file-or-directory test itself is why the check is usable at all, on every
// runtime. A rule that skipped it would name every `node_modules` bind and every
// cache directory, which is the commonest mount shape there is.
func mountPointFor(source string) mountPointKind {
	info, err := os.Stat(source)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return mountPointDirectory
	case err != nil:
		return mountPointUnknown
	case info.IsDir():
		return mountPointDirectory
	default:
		return mountPointFile
	}
}

// insideContainerPath reports whether a mount target lies inside the path a
// task's worktree is mounted at, and what it is relative to it.
//
// Strictly inside. A target equal to the container path is the repository's own
// mount, which Feat replaces rather than fails on, and which is what
// ComposeService.SourceTargets exists to collect.
//
// It works in "path" rather than "path/filepath" because both of these are the
// container's paths and neither is this machine's. On the two platforms Feat targets
// the two packages agree, so this says what the values mean rather than fixing
// anything: a path read out of a Compose file is resolved by a container runtime.
func insideContainerPath(containerPath, target string) (string, bool) {
	if containerPath == "" || target == "" {
		return "", false
	}
	parent := strings.TrimSuffix(path.Clean(containerPath), "/")
	if parent == "" {
		// A container path of "/" holds everything, which would report every
		// mount in the file. Configuration refuses it (config.checkContainerPath),
		// so this is the total-function half rather than a case to handle.
		return "", false
	}
	cleaned := path.Clean(target)
	if !strings.HasPrefix(cleaned, parent+"/") {
		return "", false
	}
	relative := strings.TrimPrefix(cleaned, parent+"/")
	if relative == "" || relative == "." || strings.HasPrefix(relative, "..") {
		return "", false
	}
	return relative, true
}

// sortedNames returns a service mapping's keys in order, so a proposal is the same
// proposal twice.
func sortedNames(services map[string]yaml.RawMessage) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// contains reports whether a list already holds a value.
func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// lines splits command output into non-empty trimmed lines.
func lines(output string) []string {
	var found []string
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			found = append(found, trimmed)
		}
	}
	return found
}
