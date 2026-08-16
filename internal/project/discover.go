package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Checkout is what an ordinary Git checkout can say about itself.
//
// It is what `feat project init` asks a directory before it proposes a
// repository: the identity of a repository is the user's to choose, but the
// branch it develops on and the remote it fetches are facts the checkout
// already holds, and asking a user to retype a fact their tools know is how a
// configuration file acquires a value that was never true.
//
// A field this package could not establish is empty rather than guessed. An
// empty remote is a repository with none, and an empty branch is a checkout
// whose HEAD is detached or whose remote publishes no default; the caller
// decides what to propose instead, and says that it is proposing rather than
// reporting.
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
	// DefaultBranch is the branch the remote publishes as its head, or the
	// branch currently checked out when it publishes none.
	DefaultBranch string
}

// Inspect asks the repository containing a directory about itself.
//
// A directory that is not in a repository is an error, because the caller asked
// about a repository and there is none: everything else this returns is
// optional, and this is the one answer that decides whether there is anything
// to configure at all.
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
// They are found because leaving them out cost the reference project everything
// that matters. Its `docker-compose.dev.yml` files carry the bind mounts a task
// worktree replaces, the reset of a published port, and, in one repository, the
// only service anybody runs; the base files beside them build a static image.
// Offering only the base names proposed a runtime with no container path, no
// reachable service worth the name, and the production build of a frontend —
// which is the configuration ADR-065 evidence 1 describes, arrived at by the
// command meant to prevent it.
var composeOverlayPattern = regexp.MustCompile(`^(compose|docker-compose)\.[^/]+\.ya?ml$`)

// composeSubdirectories are the directories a project keeps its Compose files
// in, beside the root of a checkout.
//
// `.devcontainer` is among them because the Dev Containers specification puts a
// project's container definition there, and a project following it often keeps
// a Compose file in that directory. That is where the claim stops: the
// specification's own file is `devcontainer.json`, which may point its
// `dockerComposeFile` anywhere, and Feat neither reads it nor implements that
// specification — Feat's `devcontainer` execution mode means the agent runs in
// a configured Compose service and is its own idea. So this is a place worth
// looking rather than a rule about what will be found there.
var composeSubdirectories = []string{".", ".devcontainer", "docker"}

// ComposeFiles returns the Compose files present under a directory.
//
// It looks one level deep, in the places a project keeps them, and it returns
// what exists rather than what might: the caller offers these as candidates and
// the user confirms or replaces them, so a file this misses costs a user one
// line of typing and a file this invents would be a path in a configuration
// that does not resolve.
//
// The order is the order they should be offered in. A base file comes before
// the overlays that layer over it, because that is the order they are listed in
// to Compose, and the overlays are sorted so that two runs propose the same
// thing in the same sequence.
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
// Only the keys of the top-level `services` mapping are read. Nothing else in
// the file is looked at, and no value is ever carried out of it: a Compose file
// names environment files, image registries, and sometimes a password that
// should not have been written there, and none of that has any business
// reaching a suggestion (docs/05-security-model.md).
//
// It is deliberately best effort and returns no error. A file that does not
// parse, uses a feature this does not understand, or is not there yet is a file
// this has nothing to suggest from, and the caller asks the question without a
// suggestion. Whether the file and service really exist is `feat doctor`'s
// answer, which it gets from Compose itself rather than from a partial reading.
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
// Four things are read and nothing else: service keys, the container targets of
// bind mounts whose source is the repository itself, whether a service is built
// from the repository, and whether it publishes a host port. No `environment`
// value, no `build.args` entry, and no `env_file` is opened, and an entry
// containing a "${...}" is left unread rather than resolved — Feat could not
// derive it without interpolating, so the user is asked instead.
type Composition struct {
	// Services are what the files declare, in name order.
	Services []ComposeService
	// Undecided names the entries left unread because they interpolate. It is
	// what turns "Feat proposed nothing" into "Feat could not tell, and here is
	// where to look".
	Undecided []string
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
	// BuildsFromSource reports that the service's build context is the
	// repository. Such a service bakes its code with COPY and has no mount to
	// replace, so where its code comes from is decided by the build context
	// alone (ADR-065 evidence 4).
	BuildsFromSource bool
	// Publishes reports that the service publishes at least one host port,
	// which is what makes it a candidate for the reachable declaration.
	Publishes bool
}

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
		if service, known := c.Service(name); known && service.Publishes {
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

// ComposeComposition reads the given Compose files structurally.
//
// The root is the repository's checkout, which is what a relative path inside
// these files resolves against: it is the `project_directory` Feat gives this
// repository's include entry, so reading them any other way would answer a
// different question from the one Compose will be asked.
//
// It is best effort and returns no error, for the reason ComposeServices does:
// a file that does not parse, uses a feature this does not model, or is not
// there yet is a file with nothing to propose from, and the caller asks its
// question without a proposal.
func ComposeComposition(root string, files ...string) Composition {
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
			composition.mergeService(position, root, file, document.Services[name])
		}
	}
	sort.Strings(composition.Undecided)
	return composition
}

// mergeService folds one file's entry for a service into what is known of it.
func (c *Composition) mergeService(position int, root, file string, raw yaml.RawMessage) {
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
			}
			continue
		}
		if !isRepositoryRoot(root, source) {
			continue
		}
		if !contains(service.SourceTargets, target) {
			service.SourceTargets = append(service.SourceTargets, target)
		}
	}

	if context, ok := buildContext(entry.Build); ok {
		service.BuildsFromSource = isRepositoryRoot(root, context)
	} else if interpolated(entry.Build) {
		c.Undecided = append(c.Undecided, where+": its build context")
	}

	for _, port := range entry.Ports {
		if interpolated(port) {
			c.Undecided = append(c.Undecided, where+": a published port")
			continue
		}
		service.Publishes = true
	}
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
func buildContext(raw yaml.RawMessage) (string, bool) {
	if len(raw) == 0 || interpolated(raw) {
		return "", false
	}

	var short string
	if err := yaml.Unmarshal(raw, &short); err == nil {
		return short, short != ""
	}

	// Only the context. build.args is where a project puts a value it did not
	// want in an image, and Feat has no business reading one.
	var long struct {
		Context string `yaml:"context"`
	}
	if err := yaml.Unmarshal(raw, &long); err != nil {
		return "", false
	}
	return long.Context, long.Context != ""
}

// interpolated reports an entry Feat must not read, because reading it would
// mean resolving a "${...}" it has no values for.
func interpolated(raw yaml.RawMessage) bool {
	return strings.Contains(string(raw), "${")
}

// isRepositoryRoot reports whether a path written in a Compose file names the
// repository itself.
//
// A relative path is resolved against the repository's checkout rather than
// against the file's own directory, because that is the `project_directory`
// Feat gives this repository's include entry. An absolute path is taken as it
// stands, and a "~" is not expanded: Compose does not expand one either, so a
// path starting with one names a directory called "~".
func isRepositoryRoot(root, value string) bool {
	if root == "" || value == "" {
		return false
	}
	resolved := value
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	return filepath.Clean(resolved) == filepath.Clean(root)
}

// sortedNames returns a service mapping's keys in order, so that a proposal is
// the same proposal twice.
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
