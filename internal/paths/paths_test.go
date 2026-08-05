package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv builds an environment whose variables come from a map, so that a test
// never depends on the variables of the process running it.
func testEnv(home string, uid int, goos string, vars map[string]string) Environment {
	return Environment{
		Getenv: func(key string) string { return vars[key] },
		Home:   home,
		UID:    uid,
		GOOS:   goos,
	}
}

func TestResolveHonoursXDGVariables(t *testing.T) {
	env := testEnv("/base/dev", 501, "linux", map[string]string{
		EnvConfigHome: "/xdg/config",
		EnvDataHome:   "/xdg/data",
		EnvRuntimeDir: "/run/user/501",
	})

	layout, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := Layout{
		Config:  "/xdg/config/feat",
		State:   "/xdg/data/feat",
		Runtime: "/run/user/501/feat",
		Socket:  "/run/user/501/feat/feat.sock",
	}
	if layout != want {
		t.Errorf("layout = %+v, want %+v", layout, want)
	}
}

func TestResolveFallsBackToHome(t *testing.T) {
	env := testEnv("/base/dev", 501, "linux", nil)

	layout, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if layout.Config != "/base/dev/.config/feat" {
		t.Errorf("Config = %q", layout.Config)
	}
	if layout.State != "/base/dev/.local/share/feat" {
		t.Errorf("State = %q", layout.State)
	}
	// No XDG runtime directory and no TMPDIR leaves the shared location, where
	// the user id is what keeps two users apart.
	if layout.Runtime != "/tmp/feat-501" {
		t.Errorf("Runtime = %q", layout.Runtime)
	}
}

// TestResolveIgnoresRelativeXDGValues pins the XDG rule that a relative value
// is invalid and must be ignored. Honouring one would resolve state against
// whatever directory the process happens to be started in.
func TestResolveIgnoresRelativeXDGValues(t *testing.T) {
	env := testEnv("/base/dev", 7, "linux", map[string]string{
		EnvConfigHome: "relative/config",
		EnvDataHome:   "../data",
		EnvRuntimeDir: "run",
		EnvTempDir:    "tmp",
	})

	layout, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for _, test := range []struct{ name, got, want string }{
		{"Config", layout.Config, "/base/dev/.config/feat"},
		{"State", layout.State, "/base/dev/.local/share/feat"},
		{"Runtime", layout.Runtime, "/tmp/feat-7"},
	} {
		if test.got != test.want {
			t.Errorf("%s = %q, want %q", test.name, test.got, test.want)
		}
	}
}

// TestResolveUsesPerUserTempDirectory covers macOS, which has no XDG runtime
// directory but does give every user their own temporary directory.
func TestResolveUsesPerUserTempDirectory(t *testing.T) {
	env := testEnv("/base/mac", 501, "darwin", map[string]string{
		EnvTempDir: "/var/folders/ab/xyz/T/",
	})

	layout, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if layout.Runtime != "/var/folders/ab/xyz/T/feat-501" {
		t.Errorf("Runtime = %q", layout.Runtime)
	}
	if layout.Socket != "/var/folders/ab/xyz/T/feat-501/feat.sock" {
		t.Errorf("Socket = %q", layout.Socket)
	}
}

func TestResolveRuntimeOverride(t *testing.T) {
	env := testEnv("/base/dev", 501, "linux", map[string]string{
		EnvRuntimeDir:      "/run/user/501",
		EnvRuntimeOverride: "/somewhere/else/",
	})

	layout, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The override replaces the directory as a whole: the socket, the lock, and
	// the endpoint record stay together (ADR-027).
	if layout.Runtime != "/somewhere/else" {
		t.Errorf("Runtime = %q", layout.Runtime)
	}
	if got, want := layout.LockFile(), "/somewhere/else/daemon.lock"; got != want {
		t.Errorf("LockFile = %q, want %q", got, want)
	}
	if got, want := layout.EndpointFile(), "/somewhere/else/endpoint.json"; got != want {
		t.Errorf("EndpointFile = %q, want %q", got, want)
	}
	if got, want := layout.TmuxSocket(), "/somewhere/else/tmux.sock"; got != want {
		t.Errorf("TmuxSocket = %q, want %q", got, want)
	}
}

func TestResolveRejectsRelativeRuntimeOverride(t *testing.T) {
	env := testEnv("/base/dev", 501, "linux", map[string]string{EnvRuntimeOverride: "runtime"})

	_, err := Resolve(env)
	if err == nil {
		t.Fatal("a relative override was accepted")
	}
	if !strings.Contains(err.Error(), EnvRuntimeOverride) {
		t.Errorf("error does not name the variable to fix: %v", err)
	}
}

func TestResolveRejectsMissingHome(t *testing.T) {
	env := testEnv("", 501, "linux", nil)

	if _, err := Resolve(env); err == nil {
		t.Fatal("a layout resolved without a home directory")
	}
}

// TestResolveRejectsUnbindableSocketPath covers the failure that is otherwise
// reported as "invalid argument" by bind, with no mention of a length.
func TestResolveRejectsUnbindableSocketPath(t *testing.T) {
	long := "/" + strings.Repeat("d", 120)

	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			env := testEnv("/base/dev", 501, goos, map[string]string{EnvRuntimeOverride: long})

			_, err := Resolve(env)

			var tooLong *SocketTooLongError
			if !errors.As(err, &tooLong) {
				t.Fatalf("error = %v, want a *SocketTooLongError", err)
			}
			if !strings.Contains(tooLong.Error(), EnvRuntimeOverride) {
				t.Errorf("error does not say how to fix it: %v", tooLong)
			}
		})
	}
}

// TestSocketPathLimitBoundary checks the exact byte where a path stops fitting,
// because an off-by-one here is a bind failure on one platform only.
func TestSocketPathLimitBoundary(t *testing.T) {
	for _, test := range []struct {
		goos  string
		limit int
	}{
		{"darwin", 104},
		{"linux", 108},
	} {
		t.Run(test.goos, func(t *testing.T) {
			longest := "/" + strings.Repeat("d", test.limit-2)
			if err := checkSocketPath(longest, test.goos); err != nil {
				t.Errorf("a %d-byte path was rejected: %v", len(longest), err)
			}
			if err := checkSocketPath(longest+"x", test.goos); err == nil {
				t.Errorf("a %d-byte path was accepted", len(longest)+1)
			}
		})
	}
}

// TestResolveCreatesNothing pins the package's contract: it answers where a
// file belongs. Creating a directory here would mean every command that only
// wants to print a path leaves state behind.
func TestResolveCreatesNothing(t *testing.T) {
	home := t.TempDir()
	env := testEnv(home, 501, "linux", map[string]string{
		EnvConfigHome:      filepath.Join(home, "config"),
		EnvDataHome:        filepath.Join(home, "data"),
		EnvRuntimeOverride: filepath.Join(home, "run"),
	})

	layout, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for _, path := range []string{
		layout.Config, layout.State, layout.Runtime, layout.Socket,
		layout.ProjectConfigDir(), layout.LockFile(), layout.EndpointFile(), layout.LogFile(), layout.TmuxSocket(),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after Resolve; err = %v", path, err)
		}
	}
}

func TestLayoutPaths(t *testing.T) {
	layout := Layout{Config: "/c/feat", State: "/s/feat", Runtime: "/r/feat", Socket: "/r/feat/feat.sock"}

	for _, test := range []struct{ name, got, want string }{
		{"ProjectConfigDir", layout.ProjectConfigDir(), "/c/feat/projects"},
		{"LogFile", layout.LogFile(), "/s/feat/logs/daemon.log"},
		{"LockFile", layout.LockFile(), "/r/feat/daemon.lock"},
		{"EndpointFile", layout.EndpointFile(), "/r/feat/endpoint.json"},
		{"TmuxSocket", layout.TmuxSocket(), "/r/feat/tmux.sock"},
	} {
		if test.got != test.want {
			t.Errorf("%s = %q, want %q", test.name, test.got, test.want)
		}
	}
}

func TestExpand(t *testing.T) {
	env := testEnv("/base/dev", 501, "linux", nil)

	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{"home alone", "~", "/base/dev"},
		{"under home", "~/code/app", "/base/dev/code/app"},
		{"absolute path", "/opt/app/", "/opt/app"},
		{"relative path", "projects/../app", "app"},
		{"tilde inside a path", "/opt/~/app", "/opt/~/app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := env.Expand(test.in)
			if err != nil {
				t.Fatalf("Expand(%q): %v", test.in, err)
			}
			if got != test.want {
				t.Errorf("Expand(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

// TestExpandRejectsAnotherUsersHome keeps configuration from reaching into a
// home directory Feat cannot resolve. Silently treating "~other" as a relative
// directory named "~other" would be worse than refusing it.
func TestExpandRejectsAnotherUsersHome(t *testing.T) {
	env := testEnv("/base/dev", 501, "linux", nil)

	for _, path := range []string{"~other", "~other/projects", "~root/.ssh"} {
		if got, err := env.Expand(path); err == nil {
			t.Errorf("Expand(%q) = %q, want an error", path, got)
		}
	}
}

func TestExpandRejectsEmptyPath(t *testing.T) {
	env := testEnv("/base/dev", 501, "linux", nil)

	if _, err := env.Expand(""); err == nil {
		t.Fatal("an empty path was accepted")
	}
}

func TestExpandRequiresHomeOnlyWhenUsed(t *testing.T) {
	env := testEnv("", 501, "linux", nil)

	if got, err := env.Expand("/opt/app"); err != nil || got != "/opt/app" {
		t.Errorf("Expand(%q) = %q, %v; an absolute path needs no home directory", "/opt/app", got, err)
	}
	if _, err := env.Expand("~/app"); err == nil {
		t.Error("~ expanded without a home directory")
	}
}

func TestCurrentReadsTheProcess(t *testing.T) {
	env, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	if !filepath.IsAbs(env.Home) {
		t.Errorf("Home = %q, want an absolute path", env.Home)
	}
	if env.GOOS == "" {
		t.Error("GOOS is empty")
	}
	if env.Getenv == nil {
		t.Error("Getenv is nil, so lookups would fall through to the process anyway")
	}
}
