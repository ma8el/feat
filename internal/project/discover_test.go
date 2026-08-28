package project_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/project"
)

// composeFixture is one repository's own Compose files, with everything the
// structural read has to answer and everything it must not touch.
//
// The base file bakes the application into an image and publishes a port
// through a proxy; the development overlay replaces the baked code with a bind
// mount, which is the arrangement `feat project init` has to read a container
// path out of. The secret, the environment file, and the build argument are
// there to be left alone.
const composeFixture = `services:
  api:
    build: .
    environment:
      DATABASE_PASSWORD: "not-a-value-feat-may-read"
      API_TOKEN: "also-not"
    env_file:
      - .env
    expose:
      - "8000"

  proxy:
    image: nginx:1.27-alpine
    ports:
      - "8000:80"
    volumes:
      - ./nginx/default.conf:/etc/nginx/conf.d/default.conf:ro

  cache:
    image: redis:7
    volumes:
      - cache-data:/data

volumes:
  cache-data:
`

const composeOverlayFixture = `services:
  api:
    build:
      context: .
      dockerfile: Dockerfile.dev
      args:
        BUILD_SECRET: "not-a-value-feat-may-read"
    volumes:
      - ./:/app
      - api-venv:/venv

  cache:
    ports:
      - "${CACHE_PORT}:6379"

volumes:
  api-venv:
`

// repositoryWith writes a repository's Compose files and returns its root. A
// name may carry a directory, which is made.
func repositoryWith(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("making the directory of %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return root
}

// TestTheSubdirectoriesAreSearchedInOrder covers the two places beside a
// checkout that nothing else did.
//
// Where a file was found is not part of what discovery returns, so the order is
// the only thing separating a checkout's own Compose files from the ones under
// `.devcontainer` and `docker` — and a caller that needs to tell them apart has
// nothing else to go on.
func TestTheSubdirectoriesAreSearchedInOrder(t *testing.T) {
	root := repositoryWith(t, map[string]string{
		"docker-compose.yml":                 "services: {}\n",
		".devcontainer/docker-compose.yml":   "services: {}\n",
		".devcontainer/compose.override.yml": "services: {}\n",
		"docker/docker-compose.yml":          "services: {}\n",
	})

	found := project.ComposeFiles(root)
	names := make([]string, 0, len(found))
	for _, file := range found {
		names = append(names, filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file)))
	}

	// The checkout's own first, then the container definition, then the docker
	// directory: the order the places are searched in, and the order a caller
	// that wants one of them has to tell them apart by.
	want := []string{
		filepath.Join(filepath.Base(root), "docker-compose.yml"),
		".devcontainer/docker-compose.yml",
		".devcontainer/compose.override.yml",
		"docker/docker-compose.yml",
	}
	if !slices.Equal(names, want) {
		t.Errorf("discovery found %v, want %v", names, want)
	}
}

// TestOverlaysAreFoundBesideTheirBaseFile is the defect a real run found.
//
// The reference project keeps everything that matters in `docker-compose.dev.yml`:
// the bind mounts a task worktree replaces, the reset of a published port, and,
// in one repository, the only service anybody runs — the base file beside it
// builds a static image. Offering only the four names Compose looks for by
// default proposed a runtime with no container path at all, which is the
// configuration ADR-065 evidence 1 describes, reached through the command meant
// to prevent it.
func TestOverlaysAreFoundBesideTheirBaseFile(t *testing.T) {
	root := repositoryWith(t, map[string]string{
		"docker-compose.yml":      "services: {}\n",
		"docker-compose.dev.yml":  "services: {}\n",
		"docker-compose.prod.yml": "services: {}\n",
		// Neither of these is a Compose file, and a proposal that named one
		// would be a path in a configuration that does not resolve.
		"docker-compose.yml.bak": "services: {}\n",
		"notes.yml":              "anything\n",
	})

	found := project.ComposeFiles(root)
	names := make([]string, 0, len(found))
	for _, file := range found {
		names = append(names, filepath.Base(file))
	}

	// The base first: it is the order they are listed to Compose in, and the
	// order a user is asked to accept them in.
	want := []string{"docker-compose.yml", "docker-compose.dev.yml", "docker-compose.prod.yml"}
	if !slices.Equal(names, want) {
		t.Errorf("discovery found %v, want %v", names, want)
	}
}

// TestAComposeFileIsReadForWhatItProposesAndNothingElse is the discipline
// ADR-065 gives the derivation: Feat reads structure to propose configuration,
// and reads no value at all.
func TestAComposeFileIsReadForWhatItProposesAndNothingElse(t *testing.T) {
	root := repositoryWith(t, map[string]string{
		"docker-compose.yml":     composeFixture,
		"docker-compose.dev.yml": composeOverlayFixture,
	})
	composition := project.ComposeComposition(root, root,
		filepath.Join(root, "docker-compose.yml"),
		filepath.Join(root, "docker-compose.dev.yml"))

	if names := composition.Names(); !slices.Equal(names, []string{"api", "cache", "proxy"}) {
		t.Fatalf("the services read are %v, want every one the files declare, in order", names)
	}

	// The mount the development overlay adds, which is what the generated
	// override has to replace by target or the services keep running the user's
	// ordinary checkout (ADR-065 evidence 7).
	target, agreed := composition.SourceTarget([]string{"api", "proxy"})
	if !agreed || target != "/app" {
		t.Errorf("the proposed container path is %q (agreed=%t), want /app", target, agreed)
	}
	// Not the proxy's configuration file, which is a mount of one file out of
	// the repository rather than of the repository.
	proxy, _ := composition.Service("proxy")
	if len(proxy.SourceTargets) != 0 {
		t.Errorf("a mount of one file inside the repository was read as the repository: %v",
			proxy.SourceTargets)
	}
	// Nor the named volumes, which belong to the Compose project rather than to
	// the host filesystem.
	cache, _ := composition.Service("cache")
	if len(cache.SourceTargets) != 0 {
		t.Errorf("a named volume was read as a bind mount: %v", cache.SourceTargets)
	}

	// The service built from this repository, which runs the code its image was
	// built with rather than anything mounted (ADR-065 evidence 4).
	api, _ := composition.Service("api")
	if !api.BuildsFromSource {
		t.Error("the service built from this repository is not reported as built from it")
	}
	if cache.BuildsFromSource {
		t.Error("a service built from nothing is reported as built from this repository")
	}

	// The service that publishes a host port, which is the candidate for the
	// reachable declaration.
	if published := composition.Published(composition.Names()); !slices.Equal(published, []string{"proxy"}) {
		t.Errorf("the services that publish a port are %v, want [proxy]", published)
	}

	// And the entry Feat could not derive: it interpolates, so it is named
	// rather than resolved.
	undecided := strings.Join(composition.Undecided, "; ")
	if !strings.Contains(undecided, "cache") || !strings.Contains(undecided, "published port") {
		t.Errorf("the interpolated port is not reported as unread: %v", composition.Undecided)
	}
}

// TestEveryFormOfPublishedPortIsRead covers what Feat allocates a host port
// against.
//
// The container port is what an allocated host port is joined to, so it has to
// be read out of every syntax a project may have written it in — and the ones
// that cannot be read have to be reported as unread rather than guessed at. A
// port range is several publications where an allocation is one, and an
// interpolated entry is a value Feat must never resolve.
//
// The host port the project wrote is deliberately not among what is read. It is
// the thing an allocated port replaces, and reading it would only invite writing
// it back.
func TestEveryFormOfPublishedPortIsRead(t *testing.T) {
	root := repositoryWith(t, map[string]string{"compose.yaml": `services:
  short:
    image: alpine
    ports:
      - "9000:80"
  addressed:
    image: alpine
    ports:
      - "127.0.0.1:9001:81/udp"
  long:
    image: alpine
    ports:
      - target: 82
        published: "9002"
        protocol: tcp
        host_ip: 127.0.0.1
  container-only:
    image: alpine
    ports:
      - "83"
  ranged:
    image: alpine
    ports:
      - "9010-9012:84-86"
`})
	composition := project.ComposeComposition(root, root, filepath.Join(root, "compose.yaml"))

	for name, want := range map[string]project.Publication{
		"short":          {ContainerPort: 80, Protocol: "tcp"},
		"addressed":      {ContainerPort: 81, Protocol: "udp", HostIP: "127.0.0.1"},
		"long":           {ContainerPort: 82, Protocol: "tcp", HostIP: "127.0.0.1"},
		"container-only": {ContainerPort: 83, Protocol: "tcp"},
	} {
		service, known := composition.Service(name)
		if !known {
			t.Fatalf("the service %s was not read at all", name)
		}
		if !slices.Equal(service.Ports, []project.Publication{want}) {
			t.Errorf("the publications of %s are %+v, want %+v", name, service.Ports, want)
		}
	}

	if ranged, _ := composition.Service("ranged"); len(ranged.Ports) != 0 {
		t.Errorf("a port range was read as one publication: %+v", ranged.Ports)
	}
	if undecided := strings.Join(composition.Undecided, "; "); !strings.Contains(undecided, "ranged") {
		t.Errorf("the port range is not reported as unread: %v", composition.Undecided)
	}
}

// TestNoValueFromAComposeFileReachesAProposal is ADR-062's rule, checked against
// the whole derived result rather than against the fields it happens to
// expose.
//
// A Compose file names environment files, build arguments, and sometimes a
// password that should not have been written there. None of it has any business
// reaching a suggestion, and the way to keep it out is to never read it.
func TestNoValueFromAComposeFileReachesAProposal(t *testing.T) {
	root := repositoryWith(t, map[string]string{
		"docker-compose.yml":     composeFixture,
		"docker-compose.dev.yml": composeOverlayFixture,
	})
	composition := project.ComposeComposition(root, root,
		filepath.Join(root, "docker-compose.yml"),
		filepath.Join(root, "docker-compose.dev.yml"))

	rendered := strings.Join(append(composition.Names(), composition.Undecided...), " ")
	for _, service := range composition.Services {
		rendered += " " + strings.Join(service.SourceTargets, " ")
	}
	for _, forbidden := range []string{
		"not-a-value-feat-may-read", "DATABASE_PASSWORD", "API_TOKEN", "BUILD_SECRET", ".env",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("a value Feat must not read reached a proposal: %q in %q", forbidden, rendered)
		}
	}
}

// TestServicesThatDisagreeProposeNothing keeps a wrong proposal out of a file.
//
// Two services mounting one repository at two paths is a project Feat has
// nothing to propose for. Choosing one of them would put a path in a
// configuration that is wrong for the other service and looks exactly as
// established as every other value in it.
func TestServicesThatDisagreeProposeNothing(t *testing.T) {
	root := repositoryWith(t, map[string]string{"compose.yaml": `services:
  api:
    image: alpine
    volumes:
      - ./:/app
  worker:
    image: alpine
    volumes:
      - .:/srv/worker
`})
	composition := project.ComposeComposition(root, root, filepath.Join(root, "compose.yaml"))

	if target, agreed := composition.SourceTarget([]string{"api", "worker"}); agreed {
		t.Errorf("two services mounting the repository at two paths proposed %q", target)
	}
	// Each still knows its own, so a caller asking about one service gets an
	// answer.
	if target, agreed := composition.SourceTarget([]string{"api"}); !agreed || target != "/app" {
		t.Errorf("one service's own mount is %q (agreed=%t), want /app", target, agreed)
	}
}

// TestARelativePathIsReadAgainstTheRepository covers the reading that has to
// match how Compose will resolve it.
//
// A repository's include entry carries its own checkout as the project
// directory, so that is what a relative path inside its files resolves against.
// Reading it against the file's own directory would answer a different question
// from the one Compose is going to be asked.
func TestARelativePathIsReadAgainstTheRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docker"), 0o700); err != nil {
		t.Fatalf("creating the subdirectory: %v", err)
	}
	file := filepath.Join(root, "docker", "compose.yaml")
	if err := os.WriteFile(file, []byte(`services:
  api:
    build: .
    volumes:
      - ./:/app
`), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	composition := project.ComposeComposition(root, root, file)
	api, known := composition.Service("api")
	if !known {
		t.Fatal("the service in a Compose file one directory down was not read")
	}
	if !api.BuildsFromSource {
		t.Error("a build context of \".\" was not read against the repository")
	}
	if !slices.Equal(api.SourceTargets, []string{"/app"}) {
		t.Errorf("the mount targets are %v, want [/app]", api.SourceTargets)
	}
}

// TestABuildContextIsReadBesideAnInterpolatedArgument is the service this whole
// reading exists for, in the shape the reference project writes it.
//
// Its frontend is a multi-stage build ending in nginx: it mounts nothing
// anywhere, so its build context is the only thing that decides what it runs
// (ADR-065 evidence 4). The context is a plain ".", and beside it is a
// build argument carrying a "${...}" — a value Feat never reads and has no
// business reading. Judging the interpolation on the whole `build` mapping made
// the plainest build context in the project unreadable, and a reader that cannot
// see it cannot see the failure it exists to find.
func TestABuildContextIsReadBesideAnInterpolatedArgument(t *testing.T) {
	root := repositoryWith(t, map[string]string{"docker-compose.yml": `services:
  site:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        APP_API_BASE_URL: ${APP_API_BASE_URL:-http://localhost:8000}
    ports:
      - "8080:8080"

  generated:
    build:
      context: ${SOMEWHERE}
`})
	composition := project.ComposeComposition(root, root, filepath.Join(root, "docker-compose.yml"))

	site, known := composition.Service("site")
	if !known {
		t.Fatal("the service was not read at all")
	}
	if !site.BuildsFromSource || site.BuildContext != root {
		t.Errorf("the build context was read as %q, built from source %t: a context beside an "+
			"interpolated argument is still a context Feat can read",
			site.BuildContext, site.BuildsFromSource)
	}
	// And the argument itself stays in the file.
	if rendered := strings.Join(composition.Undecided, "; "); strings.Contains(rendered, "APP_API_BASE_URL") {
		t.Errorf("a build argument reached a proposal: %q", rendered)
	}

	// A context that does interpolate is still left unread and named, because
	// resolving that one would mean interpolating it.
	generated, known := composition.Service("generated")
	if !known {
		t.Fatal("the second service was not read at all")
	}
	if generated.BuildContext != "" || generated.BuildsFromSource {
		t.Errorf("an interpolated build context was resolved to %q", generated.BuildContext)
	}
	if rendered := strings.Join(composition.Undecided, "; "); !strings.Contains(rendered, "build context") {
		t.Errorf("an interpolated build context is not reported as unread: %v", composition.Undecided)
	}
}

// TestABuildContextInsideTheRepositoryIsTheRepositorys covers the monorepo
// shape, and the one path that is not.
//
// A context of ./frontend is that repository's code as surely as its root is,
// and the task's worktree holds the same subdirectory. A context beside the
// checkout is somebody else's.
func TestABuildContextInsideTheRepositoryIsTheRepositorys(t *testing.T) {
	root := repositoryWith(t, map[string]string{"compose.yaml": `services:
  web:
    build: ./site
  vendored:
    build: ../elsewhere
`})
	composition := project.ComposeComposition(root, root, filepath.Join(root, "compose.yaml"))

	web, _ := composition.Service("web")
	if !web.BuildsFromSource || web.BuildContext != filepath.Join(root, "site") {
		t.Errorf("a build context inside the repository was read as %q, built from source %t",
			web.BuildContext, web.BuildsFromSource)
	}
	vendored, _ := composition.Service("vendored")
	if vendored.BuildsFromSource {
		t.Errorf("a build context outside the repository at %q is reported as the repository's",
			vendored.BuildContext)
	}
}

// TestTheMountsIntoARepositoryAreCollected covers what the reader used to
// discard.
//
// Everything that is not the repository root was dropped, because it is not a
// candidate for the container path. It is still a path the mount needs: a task
// works in a worktree and a worktree holds only what Git tracks, so a bind of an
// ignored file is a bind of something that will not be there. Whether it is
// tracked is Git's answer and `feat doctor`'s question; this reads the paths.
func TestTheMountsIntoARepositoryAreCollected(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "api")
	devcontainer := filepath.Join(root, "devcontainer")
	for _, dir := range []string{repository, devcontainer} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}
	file := filepath.Join(devcontainer, "compose.yaml")
	if err := os.WriteFile(file, []byte(`services:
  dev:
    image: alpine
    volumes:
      - ../api:/srv/api
      - ../api/.env:/srv/api/.env
      - ../api/node_modules:/srv/api/node_modules
      - ./Dockerfile:/src/Dockerfile:ro
      - agent-config:/var/agent/config
      - ~/.config/agent:/var/agent/user
      - ${TOOLS}/bin:/usr/local/bin
`), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	composition := project.ComposeComposition(devcontainer, repository, file)

	var paths []string
	for _, mount := range composition.Mounts {
		paths = append(paths, mount.Path)
	}
	// The two that come out of the repository, and nothing else. The repository
	// itself is the container path question and is answered by SourceTargets; the
	// Dockerfile beside the Compose file and the named volume are not this
	// repository's at all; and the home-relative source is one this does not
	// resolve, so it is not one this may place. Joining a "~" to the project
	// directory put the user's home inside the repository and reported the mount
	// Feat's own devcontainer recommends as a path a worktree would not hold.
	want := []string{filepath.Join(repository, ".env"), filepath.Join(repository, "node_modules")}
	if !slices.Equal(paths, want) {
		t.Errorf("the paths a mount needs are %v, want %v", paths, want)
	}
	if dev, _ := composition.Service("dev"); !slices.Equal(dev.SourceTargets, []string{"/srv/api"}) {
		t.Errorf("the repository's own mount is %v, want [/srv/api]", dev.SourceTargets)
	}
	// And each is attributed, because a reader sent to look at one has to know
	// which file and which service wrote it.
	for _, mount := range composition.Mounts {
		if !strings.Contains(mount.Where, file) || !strings.Contains(mount.Where, "dev") {
			t.Errorf("the mount of %s is attributed to %q", mount.Path, mount.Where)
		}
	}

	// The interpolated entry is named as unread rather than passed over: a report
	// on mounts that stayed silent about it would claim it had read them all.
	if !slices.Equal(composition.UnreadMounts, []string{file + ": service dev: a volume"}) {
		t.Errorf("the entry that interpolates is reported as %v", composition.UnreadMounts)
	}
}

// TestAFileThatCannotBeReadProposesNothing keeps discovery best effort.
//
// A file that does not parse, uses a feature this does not model, or is not
// there yet is a file with nothing to propose from. The caller asks its question
// without a proposal, and `feat doctor` asks Compose itself later.
func TestAFileThatCannotBeReadProposesNothing(t *testing.T) {
	root := repositoryWith(t, map[string]string{"compose.yaml": "services: [this is not a mapping]\n"})

	for _, file := range []string{
		filepath.Join(root, "compose.yaml"),
		filepath.Join(root, "does-not-exist.yaml"),
	} {
		if composition := project.ComposeComposition(root, root, file); len(composition.Services) != 0 {
			t.Errorf("%s proposed %v", file, composition.Names())
		}
	}
}
