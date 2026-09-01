package version_test

import (
	"archive/zip"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/integrationtest"
)

// identityModule is the throwaway module these tests build and install: this
// package's own sources, under a main that prints what they report.
const identityModule = "feat.test/identity"

// TestRealBuildIdentityIsStampedByTheToolchain builds this package's source in a
// throwaway Git repository and asks the resulting binary what build it is.
//
// The unit tests hand resolve a *debug.BuildInfo we wrote ourselves, which
// proves what we do with build information and nothing about what the toolchain
// actually records. This is the other half, and it cannot be had inside the
// repository: `go build` stamps no vcs settings at all when the module sits in a
// Git worktree — which every Feat task checkout is, and which is why a plain
// build here still reports "dev". A fresh repository is somewhere the toolchain
// will answer.
//
// The module it builds is a copy of this package's sources, so what runs is the
// working tree rather than whatever is committed, and it has no requirements, so
// the build needs no network and no module cache.
func TestRealBuildIdentityIsStampedByTheToolchain(t *testing.T) {
	goTool := requireToolchain(t)
	requireGit(t)

	root := t.TempDir()
	module := filepath.Join(root, "module")
	binaries := filepath.Join(root, "bin")
	for _, dir := range []string{module, binaries} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("preparing %s: %v", dir, err)
		}
	}

	writeIdentityModule(t, module)
	run(t, module, "git", "init", "--quiet", "--initial-branch=main")
	run(t, module, "git", "add", "--all")
	run(t, module, "git", "commit", "--quiet", "--message", "the build under test")

	revision := run(t, module, "git", "rev-parse", "HEAD")
	short := revision[:12]
	committed := parseTime(t, run(t, module, "git", "show", "--no-patch", "--format=%cI", "HEAD"))

	clean := build(t, goTool, module, filepath.Join(binaries, "clean"))

	if clean.commit != short {
		t.Errorf("commit = %q, want the revision the toolchain stamped, %q", clean.commit, short)
	}
	if got := parseTime(t, clean.date); !got.Equal(committed) {
		t.Errorf("date = %q, want the commit time %s", clean.date, committed.Format(time.RFC3339))
	}
	if !strings.HasSuffix(clean.version, "-"+short) {
		t.Errorf("version = %q, want the pseudo-version ending in the revision %q", clean.version, short)
	}
	if strings.Contains(clean.line, "dirty") {
		t.Errorf("a clean build reported %q, which claims uncommitted changes it did not have", clean.line)
	}
	for _, want := range []string{clean.version, clean.commit, clean.date} {
		if !strings.Contains(clean.line, want) {
			t.Errorf("`feat version` line %q is missing %q", clean.line, want)
		}
	}

	// The same commit, with the working tree moved past it.
	appendLine(t, filepath.Join(module, "main.go"), "\n// The tree has moved past the commit it names.\n")
	dirty := build(t, goTool, module, filepath.Join(binaries, "dirty"))

	if want := short + "-dirty"; dirty.commit != want {
		t.Errorf("commit = %q, want %q", dirty.commit, want)
	}
	if !strings.HasSuffix(dirty.version, "+dirty") {
		t.Errorf("version = %q, want the toolchain's +dirty marker", dirty.version)
	}
	if got := parseTime(t, dirty.date); !got.Equal(committed) {
		t.Errorf("date = %q, want the commit time %s", dirty.date, committed.Format(time.RFC3339))
	}
}

// requireToolchain ends the test unless the run is opted in, and returns the Go
// toolchain these tests build with. It also settles the environment they share:
// nothing here reads a developer's configuration or reaches the network.
func requireToolchain(t *testing.T) string {
	t.Helper()

	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that build and install a real binary", integrationtest.Env)
	}

	goTool, err := exec.LookPath("go")
	if err != nil {
		// Not a skip: this tier is opted in, `go test` ran, and a Go toolchain
		// that cannot be found again is a broken machine rather than a modest
		// one.
		t.Fatalf("the Go toolchain is not on PATH, so no binary can be built: %v", err)
	}

	for name, value := range map[string]string{
		"GOFLAGS": "",
		"GOWORK":  "off",
		// The module under test has no requirements, so nothing legitimate
		// needs the network; asking for it would mean something has gone wrong.
		"GOPROXY":     "off",
		"GOTOOLCHAIN": "local",
	} {
		t.Setenv(name, value)
	}
	return goTool
}

// requireGit adds the demand for Git, which only the build half makes.
func requireGit(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Git, "git is not installed")
	}

	for name, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_AUTHOR_NAME":     "Feat Test",
		"GIT_AUTHOR_EMAIL":    "test@example.invalid",
		"GIT_COMMITTER_NAME":  "Feat Test",
		"GIT_COMMITTER_EMAIL": "test@example.invalid",
		"GIT_TERMINAL_PROMPT": "0",
	} {
		t.Setenv(name, value)
	}
}

// reported is what a built or installed binary said about itself.
type reported struct {
	version string
	commit  string
	date    string
	line    string
}

// build compiles the throwaway module and runs it, returning what it reported.
func build(t *testing.T, goTool, module, output string) reported {
	t.Helper()

	run(t, module, goTool, "build", "-o", output, ".")
	return report(t, run(t, module, output))
}

// report reads the four lines the throwaway module's main prints.
func report(t *testing.T, printed string) reported {
	t.Helper()

	fields := strings.Split(printed, "\n")
	if len(fields) != 4 {
		t.Fatalf("the binary printed %d lines, want 4:\n%s", len(fields), printed)
	}
	return reported{version: fields[0], commit: fields[1], date: fields[2], line: fields[3]}
}

// writeIdentityModule copies this package into a module of its own, under a main
// that prints what the package reports.
func writeIdentityModule(t *testing.T, dir string) {
	t.Helper()

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing this package's sources: %v", err)
	}

	packageDir := filepath.Join(dir, "version")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatalf("preparing %s: %v", packageDir, err)
	}

	copied := 0
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("reading %s: %v", source, err)
		}
		if err := os.WriteFile(filepath.Join(packageDir, source), content, 0o644); err != nil {
			t.Fatalf("writing %s: %v", source, err)
		}
		copied++
	}
	if copied == 0 {
		t.Fatalf("no sources found in %s to build", mustGetwd(t))
	}

	write(t, filepath.Join(dir, "go.mod"), "module "+identityModule+"\n\ngo "+goDirective(t)+"\n")
	write(t, filepath.Join(dir, "main.go"), `package main

import (
	"fmt"

	"`+identityModule+`/version"
)

func main() {
	info := version.Get()
	fmt.Println(info.Version)
	fmt.Println(info.Commit)
	fmt.Println(info.Date)
	fmt.Println(info)
}
`)
}

// goDirective is the language version the repository builds against, so the copy
// compiles under exactly what the original does.
func goDirective(t *testing.T) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("reading the repository's go.mod: %v", err)
	}

	directive := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(content)
	if directive == nil {
		t.Fatal("the repository's go.mod states no go directive")
	}
	return string(directive[1])
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	write(t, path, string(content)+line)
}

// run executes an argument vector in dir and returns its trimmed output.
func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()

	command := exec.Command(name, args...)
	command.Dir = dir

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("`%s %s` in %s: %v\n%s", name, strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("%q is not a timestamp: %v", value, err)
	}
	return parsed
}

func mustGetwd(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	return dir
}

// These are the identity a module downloaded from a proxy carries: a version,
// and nothing else. The pseudo-version is the shape `go install ...@latest`
// resolves to against a repository that has published no tag, which is what
// this one has published.
const (
	installedPseudo   = "v0.0.0-20260830174602-b9c8e9de3781"
	installedRevision = "b9c8e9de3781"
	installedTime     = "2026-08-30T17:46:02Z"
	installedTag      = "v0.4.1"
)

// TestRealInstalledIdentityComesFromTheModuleVersion installs the module from a
// proxy and asks the installed binary what build it is.
//
// This is the case the fallback exists for, and the sibling test cannot reach
// it: a build inside a checkout has vcs settings, and a module downloaded from a
// proxy never does. Everything an installed binary knows it knows from its
// module version, so the test also reads the binary's own build information back
// with `go version -m` and refuses to pass if a revision was recorded there —
// otherwise a checkout could be answering, and the proof would be of the other
// test's case wearing this one's name.
//
// The proxy is a directory of files served over file://, which is a complete
// module proxy for one module and needs no network.
func TestRealInstalledIdentityComesFromTheModuleVersion(t *testing.T) {
	goTool := requireToolchain(t)
	root := t.TempDir()

	// The install writes into a module cache of its own, whose files are
	// read-only: left in place they would defeat the temporary directory's own
	// removal, so the toolchain is asked to take them back.
	cache := filepath.Join(root, "cache")
	t.Setenv("GOMODCACHE", cache)
	t.Cleanup(func() {
		clean := exec.Command(goTool, "clean", "-modcache")
		clean.Dir = root
		if output, err := clean.CombinedOutput(); err != nil {
			t.Errorf("returning the temporary module cache: %v\n%s", err, output)
		}
	})

	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("preparing %s: %v", source, err)
	}
	writeIdentityModule(t, source)

	t.Run("installed at a pseudo-version", func(t *testing.T) {
		// `go install feat.test/identity@latest`, against a proxy publishing
		// one version, which is the query and the answer a tester gets today.
		installed := install(t, goTool, root, source, installedPseudo, "latest")

		if installed.version != installedPseudo {
			t.Errorf("version = %q, want %q", installed.version, installedPseudo)
		}
		if installed.commit != installedRevision {
			t.Errorf("commit = %q, want the revision inside the module version, %q",
				installed.commit, installedRevision)
		}
		if installed.date != installedTime {
			t.Errorf("date = %q, want the timestamp inside the module version, %q",
				installed.date, installedTime)
		}
	})

	t.Run("installed at a tag", func(t *testing.T) {
		// A tag names a release rather than a commit, and the binary says so
		// rather than inventing one.
		installed := install(t, goTool, root, source, installedTag, installedTag)

		if installed.version != installedTag {
			t.Errorf("version = %q, want %q", installed.version, installedTag)
		}
		if installed.commit != "unknown" || installed.date != "unknown" {
			t.Errorf("commit/date = %q/%q, want unknown/unknown: a tag names no commit",
				installed.commit, installed.date)
		}
	})
}

// install publishes the module at one version to a proxy of its own, installs it
// with the given query, and returns what the installed binary reported.
func install(t *testing.T, goTool, root, source, version, query string) reported {
	t.Helper()

	dir := filepath.Join(root, "install-"+version)
	proxy := filepath.Join(dir, "proxy")
	binaries := filepath.Join(dir, "bin")
	for _, made := range []string{proxy, binaries} {
		if err := os.MkdirAll(made, 0o755); err != nil {
			t.Fatalf("preparing %s: %v", made, err)
		}
	}
	publish(t, proxy, source, version)

	t.Setenv("GOBIN", binaries)
	t.Setenv("GOPROXY", (&url.URL{Scheme: "file", Path: proxy}).String())
	// The module is served from a directory rather than fetched, so there is no
	// checksum database to ask and nothing to ask it about.
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GONOSUMDB", "*")
	t.Setenv("GOPRIVATE", "")

	run(t, dir, goTool, "install", identityModule+"@"+query)

	binary := filepath.Join(binaries, "identity")
	installed := report(t, run(t, dir, binary))

	// What the toolchain recorded in the binary, as opposed to what the binary
	// says about itself: a module version and no checkout behind it.
	embedded := run(t, dir, goTool, "version", "-m", binary)
	if !strings.Contains(embedded, version) {
		t.Errorf("the installed binary records no version %q:\n%s", version, embedded)
	}
	if strings.Contains(embedded, "vcs.revision") {
		t.Fatalf("the installed binary carries vcs settings, so this proves nothing "+
			"about an installed one:\n%s", embedded)
	}
	return installed
}

// publish writes the module at one version into a proxy directory: the three
// files a proxy serves per version, and the two that answer a query for one.
func publish(t *testing.T, proxy, source, version string) {
	t.Helper()

	dir := filepath.Join(proxy, filepath.FromSlash(identityModule), "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("preparing %s: %v", dir, err)
	}

	goMod, err := os.ReadFile(filepath.Join(source, "go.mod"))
	if err != nil {
		t.Fatalf("reading the module's go.mod: %v", err)
	}

	info := `{"Version":"` + version + `","Time":"` + installedTime + `"}` + "\n"
	write(t, filepath.Join(dir, version+".mod"), string(goMod))
	write(t, filepath.Join(dir, version+".info"), info)
	write(t, filepath.Join(dir, "list"), version+"\n")
	// A version query is answered from @latest where the proxy offers one.
	write(t, filepath.Join(proxy, filepath.FromSlash(identityModule), "@latest"), info)

	writeModuleZip(t, filepath.Join(dir, version+".zip"), source, version)
}

// writeModuleZip packages the module the way a proxy serves it: every file under
// a single `<module>@<version>/` prefix.
func writeModuleZip(t *testing.T, path, source, version string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("closing %s: %v", path, err)
		}
	}()

	archive := zip.NewWriter(file)
	prefix := identityModule + "@" + version + "/"

	err = filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		entryWriter, err := archive.Create(prefix + filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		_, err = entryWriter.Write(content)
		return err
	})
	if err != nil {
		t.Fatalf("packaging %s: %v", path, err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("finishing %s: %v", path, err)
	}
}
