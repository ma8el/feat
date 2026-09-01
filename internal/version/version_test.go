package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// buildInfo assembles what the toolchain would have embedded: a main module
// version, and the vcs settings a build inside a checkout carries.
func buildInfo(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{
		Main:     debug.Module{Path: "github.com/ma8el/feat", Version: mainVersion},
		Settings: settings,
	}
}

func vcs(revision, when, modified string) []debug.BuildSetting {
	return []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.time", Value: when},
		{Key: "vcs.modified", Value: modified},
	}
}

func TestResolveBuildIdentity(t *testing.T) {
	unlinked := Info{Version: devVersion, Commit: unknown, Date: unknown}

	tests := []struct {
		name   string
		linked Info
		build  *debug.BuildInfo
		want   Info
	}{
		{
			// `go install github.com/ma8el/feat/cmd/feat@v0.4.1`: the module
			// version is known and there is no checkout to have a revision.
			name:   "installed binary reports its module version",
			linked: unlinked,
			build:  buildInfo("v0.4.1"),
			want:   Info{Version: "v0.4.1", Commit: unknown, Date: unknown},
		},
		{
			// A build the toolchain could not derive a version for, and which
			// carries no vcs settings either — a git worktree, or -buildvcs=false.
			name:   "devel build keeps the placeholders",
			linked: unlinked,
			build:  buildInfo("(devel)"),
			want:   unlinked,
		},
		{
			// A plain `go build` inside a clean checkout.
			name:   "checkout build reports revision and time",
			linked: unlinked,
			build: buildInfo(
				"v0.0.0-20260830174602-b9c8e9de3781",
				vcs("b9c8e9de37811b0d1b1d4b3f7a2c9e5d0a6f8c31", "2026-08-30T17:46:02Z", "false")...,
			),
			want: Info{
				Version: "v0.0.0-20260830174602-b9c8e9de3781",
				Commit:  "b9c8e9de3781",
				Date:    "2026-08-30T17:46:02Z",
			},
		},
		{
			// The same checkout with uncommitted changes: the toolchain marks
			// the version and we mark the revision it no longer describes.
			name:   "dirty build marks the revision",
			linked: unlinked,
			build: buildInfo(
				"v0.0.0-20260830174602-b9c8e9de3781+dirty",
				vcs("b9c8e9de37811b0d1b1d4b3f7a2c9e5d0a6f8c31", "2026-08-30T17:46:02Z", "true")...,
			),
			want: Info{
				Version: "v0.0.0-20260830174602-b9c8e9de3781+dirty",
				Commit:  "b9c8e9de3781-dirty",
				Date:    "2026-08-30T17:46:02Z",
			},
		},
		{
			// `make build`. The Makefile's LDFLAGS describe the same build more
			// precisely than the toolchain does, so every one of them wins.
			name:   "linked values win over build information",
			linked: Info{Version: "v0.5.0-3-gb9c8e9d", Commit: "b9c8e9d", Date: "2026-08-31T09:00:00Z"},
			build: buildInfo(
				"v0.0.0-20260830174602-b9c8e9de3781",
				vcs("b9c8e9de37811b0d1b1d4b3f7a2c9e5d0a6f8c31", "2026-08-30T17:46:02Z", "false")...,
			),
			want: Info{Version: "v0.5.0-3-gb9c8e9d", Commit: "b9c8e9d", Date: "2026-08-31T09:00:00Z"},
		},
		{
			// A binary with no build information at all: nothing to fall back to.
			name:   "no build information keeps the placeholders",
			linked: unlinked,
			build:  nil,
			want:   unlinked,
		},
		{
			// Each field stands alone: a build can know its revision without
			// knowing its version, and must still report the revision.
			name:   "revision without a version keeps both answers",
			linked: unlinked,
			build: buildInfo(
				"(devel)",
				vcs("b9c8e9de37811b0d1b1d4b3f7a2c9e5d0a6f8c31", "2026-08-30T17:46:02Z", "false")...,
			),
			want: Info{
				Version: devVersion,
				Commit:  "b9c8e9de3781",
				Date:    "2026-08-30T17:46:02Z",
			},
		},
		{
			// And the other way: a linked version does not suppress the commit
			// and date the toolchain knew.
			name:   "a linked version leaves the rest to build information",
			linked: Info{Version: "v0.5.0", Commit: unknown, Date: unknown},
			build: buildInfo(
				"(devel)",
				vcs("b9c8e9de37811b0d1b1d4b3f7a2c9e5d0a6f8c31", "2026-08-30T17:46:02Z", "true")...,
			),
			want: Info{
				Version: "v0.5.0",
				Commit:  "b9c8e9de3781-dirty",
				Date:    "2026-08-30T17:46:02Z",
			},
		},
		{
			// A revision shorter than a pseudo-version's is reported whole.
			name:   "a short revision is not cut",
			linked: unlinked,
			build:  buildInfo("(devel)", vcs("b9c8e9d", "2026-08-30T17:46:02Z", "false")...),
			want: Info{
				Version: devVersion,
				Commit:  "b9c8e9d",
				Date:    "2026-08-30T17:46:02Z",
			},
		},
		{
			// Nothing linked and nothing embedded still renders a whole line.
			name:   "empty values fall back to the placeholders",
			linked: Info{},
			build:  buildInfo(""),
			want:   unlinked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.linked, tt.build)

			if got.Version != tt.want.Version {
				t.Errorf("Version = %q, want %q", got.Version, tt.want.Version)
			}
			if got.Commit != tt.want.Commit {
				t.Errorf("Commit = %q, want %q", got.Commit, tt.want.Commit)
			}
			if got.Date != tt.want.Date {
				t.Errorf("Date = %q, want %q", got.Date, tt.want.Date)
			}
		})
	}
}

func TestGetReportsRuntimeIdentity(t *testing.T) {
	info := Get()

	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; info.Platform() != want {
		t.Errorf("Platform() = %q, want %q", info.Platform(), want)
	}
	for name, field := range map[string]string{"Version": info.Version, "Commit": info.Commit, "Date": info.Date} {
		if field == "" {
			t.Errorf("%s is empty; a build identity always reports something", name)
		}
	}
}

func TestGetPrefersLinkedValues(t *testing.T) {
	restore := func(v, c, d string) { version, commit, date = v, c, d }
	defer restore(version, commit, date)

	version, commit, date = "v9.9.9", "abcdef0", "2026-08-31T09:00:00Z"

	info := Get()
	if info.Version != "v9.9.9" || info.Commit != "abcdef0" || info.Date != "2026-08-31T09:00:00Z" {
		t.Errorf("Get() = %+v, want the linked version, commit, and date", info)
	}
}

func TestStringIncludesBuildIdentity(t *testing.T) {
	info := Get()
	got := info.String()

	for _, want := range []string{info.Version, info.Commit, info.Date, info.GoVersion, info.Platform()} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}
