package version_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/integrationtest"
)

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
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that use real Git", integrationtest.Env)
	}
	if _, err := exec.LookPath("git"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Git, "git is not installed")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		// Not a skip: this tier is opted in, `go test` ran, and a Go toolchain
		// that cannot be found again is a broken machine rather than a modest
		// one.
		t.Fatalf("the Go toolchain is not on PATH, so no binary can be built: %v", err)
	}

	// Neither Git nor the build reads the developer's own configuration.
	for name, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_AUTHOR_NAME":     "Feat Test",
		"GIT_AUTHOR_EMAIL":    "test@example.invalid",
		"GIT_COMMITTER_NAME":  "Feat Test",
		"GIT_COMMITTER_EMAIL": "test@example.invalid",
		"GIT_TERMINAL_PROMPT": "0",
		"GOFLAGS":             "",
		"GOWORK":              "off",
		// The module has no requirements, so nothing legitimate needs the
		// network; asking for it would mean something has gone wrong.
		"GOPROXY":     "off",
		"GOTOOLCHAIN": "local",
	} {
		t.Setenv(name, value)
	}

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

// reported is what the built binary said about itself.
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

	printed := run(t, module, output)
	fields := strings.Split(printed, "\n")
	if len(fields) != 4 {
		t.Fatalf("the built binary printed %d lines, want 4:\n%s", len(fields), printed)
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

	write(t, filepath.Join(dir, "go.mod"), "module feat.test/identity\n\ngo "+goDirective(t)+"\n")
	write(t, filepath.Join(dir, "main.go"), `package main

import (
	"fmt"

	"feat.test/identity/version"
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
