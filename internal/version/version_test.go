package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// nothingLinked is a build the Makefile never touched: every field still holds
// the placeholder it was declared with.
var nothingLinked = identity{version: devVersion, commit: unknown, date: unknown}

// embeds assembles what the toolchain would have recorded for a main module
// version, with no vcs settings — the shape of a binary installed from a proxy.
func embeds(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{
		Main:     debug.Module{Version: mainVersion},
		Settings: settings,
	}
}

// vcs is what a build inside a checkout records beside the module version.
func vcs(revision, committed string, modified bool) []debug.BuildSetting {
	recorded := "false"
	if modified {
		recorded = "true"
	}
	return []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.time", Value: committed},
		{Key: "vcs.modified", Value: recorded},
	}
}

const (
	fullRevision  = "b9c8e9de37811b0d1b1d4b3f7a2c9e5d0a6f8c31"
	shortRevision = "b9c8e9de3781"
	committedAt   = "2026-08-30T17:46:02Z"
	pseudo        = "v0.0.0-20260830174602-" + shortRevision
)

func TestResolveBuildIdentity(t *testing.T) {
	tests := []struct {
		name   string
		linked identity
		build  *debug.BuildInfo
		want   identity
	}{
		{
			// `go install github.com/ma8el/feat/cmd/feat@v0.4.1`: a module
			// downloaded from a proxy has a version and no checkout, and a
			// version naming a tag names no single commit.
			name:   "a binary installed at a tag reports its module version",
			linked: nothingLinked,
			build:  embeds("v0.4.1"),
			want:   identity{version: "v0.4.1", commit: unknown, date: unknown},
		},
		{
			// `go install ...@latest` against a repository with no tag, which
			// is the install path a tester takes today. The version the
			// toolchain derived carries the commit and its timestamp, so the
			// binary knows all three fields without a checkout to read.
			name:   "a binary installed at a pseudo-version reads the commit out of it",
			linked: nothingLinked,
			build:  embeds(pseudo),
			want:   identity{version: pseudo, commit: shortRevision, date: committedAt},
		},
		{
			// A prerelease tag is not a pseudo-version, however much it looks
			// like one, and must not be mined for a commit that is not there.
			name:   "a prerelease tag yields no commit",
			linked: nothingLinked,
			build:  embeds("v0.4.1-rc.1"),
			want:   identity{version: "v0.4.1-rc.1", commit: unknown, date: unknown},
		},
		{
			// A build the toolchain could not derive a version for and which
			// carries no vcs settings either: a Git worktree, or -buildvcs=false.
			name:   "a devel build keeps the placeholders",
			linked: nothingLinked,
			build:  embeds(devel),
			want:   nothingLinked,
		},
		{
			// A plain `go build` inside a clean checkout. The vcs settings
			// answer, and the full revision is cut to the length a person reads.
			name:   "a checkout build reports its revision and time",
			linked: nothingLinked,
			build:  embeds(pseudo, vcs(fullRevision, committedAt, false)...),
			want:   identity{version: pseudo, commit: shortRevision, date: committedAt},
		},
		{
			// The same checkout with uncommitted changes. The toolchain marks
			// the version; we mark the revision the tree has moved past, the
			// way `git describe --dirty` does.
			name:   "a dirty checkout build marks the revision",
			linked: nothingLinked,
			build:  embeds(pseudo+dirtyMetadata, vcs(fullRevision, committedAt, true)...),
			want: identity{
				version: pseudo + dirtyMetadata,
				commit:  shortRevision + dirtySuffix,
				date:    committedAt,
			},
		},
		{
			// A dirty tree the toolchain recorded in the version alone. The two
			// fields have to tell the same story whichever source said so.
			name:   "a dirty module version marks the revision it names",
			linked: nothingLinked,
			build:  embeds(pseudo + dirtyMetadata),
			want: identity{
				version: pseudo + dirtyMetadata,
				commit:  shortRevision + dirtySuffix,
				date:    committedAt,
			},
		},
		{
			// `make build`. The LDFLAGS describe the same build more precisely
			// than the toolchain does, so every one of them wins.
			name:   "linked values win over build information",
			linked: identity{version: "v0.5.0-3-gb9c8e9d", commit: "b9c8e9d", date: "2026-08-31T09:00:00Z"},
			build:  embeds(pseudo, vcs(fullRevision, committedAt, false)...),
			want:   identity{version: "v0.5.0-3-gb9c8e9d", commit: "b9c8e9d", date: "2026-08-31T09:00:00Z"},
		},
		{
			// A binary with no build information at all: nothing to fall back to.
			name:   "no build information keeps the placeholders",
			linked: nothingLinked,
			build:  nil,
			want:   nothingLinked,
		},
		{
			// Each field stands alone: a build that knows its revision and not
			// its version still reports the revision.
			name:   "a revision without a version keeps both answers",
			linked: nothingLinked,
			build:  embeds(devel, vcs(fullRevision, committedAt, false)...),
			want:   identity{version: devVersion, commit: shortRevision, date: committedAt},
		},
		{
			// And the other way: a linked version does not suppress the commit
			// and date the toolchain knew.
			name:   "a linked version leaves the rest to build information",
			linked: identity{version: "v0.5.0", commit: unknown, date: unknown},
			build:  embeds(devel, vcs(fullRevision, committedAt, true)...),
			want: identity{
				version: "v0.5.0",
				commit:  shortRevision + dirtySuffix,
				date:    committedAt,
			},
		},
		{
			// A revision shorter than a pseudo-version's is reported whole.
			name:   "a short revision is not cut",
			linked: nothingLinked,
			build:  embeds(devel, vcs("b9c8e9d", committedAt, false)...),
			want:   identity{version: devVersion, commit: "b9c8e9d", date: committedAt},
		},
		{
			// Nothing linked, nothing embedded, and nothing empty: `feat
			// version` still prints a whole line.
			name:   "empty values fall back to the placeholders",
			linked: identity{},
			build:  embeds(""),
			want:   nothingLinked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.linked, tt.build)

			if got.version != tt.want.version {
				t.Errorf("version = %q, want %q", got.version, tt.want.version)
			}
			if got.commit != tt.want.commit {
				t.Errorf("commit = %q, want %q", got.commit, tt.want.commit)
			}
			if got.date != tt.want.date {
				t.Errorf("date = %q, want %q", got.date, tt.want.date)
			}
		})
	}
}

func TestFromPseudoVersionReadsOnlyADerivedVersion(t *testing.T) {
	tests := []struct {
		moduleVersion string
		wantCommit    string
		wantDate      string
	}{
		{moduleVersion: pseudo, wantCommit: shortRevision, wantDate: committedAt},
		{moduleVersion: pseudo + dirtyMetadata, wantCommit: shortRevision, wantDate: committedAt},
		// The form the toolchain derives from a commit that follows a tag.
		{moduleVersion: "v0.5.1-0.20260830174602-" + shortRevision, wantCommit: shortRevision, wantDate: committedAt},
		{moduleVersion: "v0.5.1-rc.1.0.20260830174602-" + shortRevision, wantCommit: shortRevision, wantDate: committedAt},
		// Tags, an empty version, and a revision of the wrong length name no
		// commit the caller may report.
		{moduleVersion: "v0.4.1"},
		{moduleVersion: "v0.4.1+incompatible"},
		{moduleVersion: ""},
		{moduleVersion: "v0.0.0-20260830174602-b9c8e9de37"},
		{moduleVersion: "v0.0.0-2026083017460-" + shortRevision},
		{moduleVersion: "v0.0.0-20260830174602-B9C8E9DE3781"},
		// Fourteen digits that are not a date: the revision beside them is
		// still a revision.
		{moduleVersion: "v0.0.0-99999999999999-" + shortRevision, wantCommit: shortRevision},
	}

	for _, tt := range tests {
		t.Run(tt.moduleVersion, func(t *testing.T) {
			commit, date := fromPseudoVersion(tt.moduleVersion)

			if commit != tt.wantCommit {
				t.Errorf("commit = %q, want %q", commit, tt.wantCommit)
			}
			if date != tt.wantDate {
				t.Errorf("date = %q, want %q", date, tt.wantDate)
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

// TestGetReadsThisBinarysBuildInformation pins the wiring: the linked values and
// the information the toolchain embedded in *this* binary, resolved the one way.
//
// What it can prove depends on the machine — a test binary built inside a Git
// worktree carries no vcs settings to find, which is most of why
// integration_test.go builds one somewhere the toolchain will stamp.
func TestGetReadsThisBinarysBuildInformation(t *testing.T) {
	embedded, _ := debug.ReadBuildInfo()
	want := resolve(identity{version: version, commit: commit, date: date}, embedded)

	got := Get()
	if got.Version != want.version || got.Commit != want.commit || got.Date != want.date {
		t.Errorf("Get() reported %q/%q/%q, want %q/%q/%q",
			got.Version, got.Commit, got.Date, want.version, want.commit, want.date)
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
