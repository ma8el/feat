package version

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Build identity, overridden at link time. See the Makefile's LDFLAGS.
//
// A binary installed with `go install github.com/ma8el/feat/cmd/feat@latest`
// never sees those flags, so each of these keeps its placeholder and the build
// information the toolchain embedded answers for it instead.
var (
	version = devVersion
	commit  = unknown
	date    = unknown
)

const (
	// devVersion and unknown are what a field says when no source knows it.
	devVersion = "dev"
	unknown    = "unknown"

	// devel is what the toolchain records as the main module's version when it
	// could not derive one from version control. It names no build, so it is
	// worth less than the placeholder it would replace.
	devel = "(devel)"

	// shortCommitLen is the revision length a Go pseudo-version carries, and
	// the length the rest of Feat renders a commit at.
	shortCommitLen = 12

	// dirtySuffix marks a revision the working tree had already moved past.
	// `git describe --dirty` spells it the same way, so a linked identity and
	// an embedded one say it identically.
	dirtySuffix = "-dirty"

	// dirtyMetadata is how the toolchain says the same thing in a module
	// version: semantic-version build metadata appended to the pseudo-version.
	dirtyMetadata = "+dirty"
)

// pseudoVersion matches the tail of a module version the toolchain derived from
// a commit rather than a tag: a 14-digit UTC timestamp and a 12-character
// revision, after whatever base version precedes them.
//
//	v0.0.0-20260830174602-b9c8e9de3781
//	v0.5.1-0.20260830174602-b9c8e9de3781
//
// The separator before the timestamp is a dash where the version was derived
// from no tag at all and a dot where it follows one.
var pseudoVersion = regexp.MustCompile(`[-.](\d{14})-([0-9a-f]{12})$`)

// pseudoVersionTime is the timestamp layout inside a pseudo-version. It has no
// zone because it is always UTC.
const pseudoVersionTime = "20060102150405"

// Info describes the running binary.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	OS        string
	Arch      string
}

// identity is the part of Info that a build has to be told: the three fields
// with a link-time source and an embedded one. The rest of Info is the running
// process describing itself and is never in doubt.
type identity struct {
	version string
	commit  string
	date    string
}

// Get returns the build identity of the running binary.
func Get() Info {
	// A binary carrying no build information reads back as a nil *BuildInfo,
	// which answers nothing and leaves the linked values standing.
	embedded, _ := debug.ReadBuildInfo()

	build := resolve(identity{version: version, commit: commit, date: date}, embedded)
	return Info{
		Version:   build.version,
		Commit:    build.commit,
		Date:      build.date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// resolve settles version, commit, and date one field at a time. Each is
// independent, because a build can know its revision without knowing its
// version, and learning one must not blank the other.
func resolve(linked identity, bi *debug.BuildInfo) identity {
	var embedded identity
	if bi != nil {
		embedded = fromBuildInfo(bi)
	}

	return identity{
		version: firstKnown(linked.version, embedded.version, devVersion),
		commit:  firstKnown(linked.commit, embedded.commit, unknown),
		date:    firstKnown(linked.date, embedded.date, unknown),
	}
}

// firstKnown returns the linked value, then the embedded one, then the
// placeholder that says nobody knew.
//
// Absent means empty, or still the placeholder the variable is declared with.
// The Makefile's own fallbacks are those same two words on purpose — `git
// describe ... || echo dev`, `git rev-parse ... || echo unknown` — so a `make
// build` on a machine where Git could not answer counts as a build with nothing
// linked, and the toolchain gets its turn. The price is that `-X ...version=dev`
// cannot insist on the literal word, which nothing wants to.
func firstKnown(linked, embedded, placeholder string) string {
	if linked != "" && linked != placeholder {
		return linked
	}
	if embedded != "" {
		return embedded
	}
	return placeholder
}

// fromBuildInfo reads the identity the toolchain embedded. Fields it cannot
// answer come back empty, so that the caller keeps whatever it already knew.
//
// There are two sources and they overlap. The main module's version is present
// in every module build, and when the toolchain derived it from a commit it
// carries that commit and its timestamp — which is all an installed binary ever
// learns, because a module downloaded from a proxy has no checkout to have vcs
// settings. A build inside a checkout has those settings too, and they are the
// better answer: a full revision, and whether the tree had moved past it.
//
// The timestamp both sources carry is the revision's, not the moment the
// compiler ran; the toolchain records no build time at all. That is the contract
// ADR-092 states for the field across every kind of build: the commit's date
// wherever the source can date itself, and a build clock only where nothing else
// saw the build, which is `make build` alone.
func fromBuildInfo(bi *debug.BuildInfo) identity {
	var embedded identity
	if v := bi.Main.Version; v != devel {
		embedded.version = v
		embedded.commit, embedded.date = fromPseudoVersion(v)
	}
	dirty := strings.HasSuffix(embedded.version, dirtyMetadata)

	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" {
				embedded.commit = shorten(setting.Value)
			}
		case "vcs.time":
			if setting.Value != "" {
				embedded.date = setting.Value
			}
		case "vcs.modified":
			dirty = dirty || setting.Value == "true"
		}
	}

	if dirty && embedded.commit != "" {
		embedded.commit += dirtySuffix
	}
	return embedded
}

// fromPseudoVersion reads the revision and its timestamp out of a module version
// the toolchain derived from a commit. A version that names a tag instead —
// v0.4.1, or v0.4.1-rc.1 — describes no single commit, and comes back empty.
func fromPseudoVersion(moduleVersion string) (revision, timestamp string) {
	base, _, _ := strings.Cut(moduleVersion, "+")

	match := pseudoVersion.FindStringSubmatch(base)
	if match == nil {
		return "", ""
	}

	stamped, err := time.Parse(pseudoVersionTime, match[1])
	if err != nil {
		// The digits are a timestamp by shape and not by value, which says
		// nothing about the revision beside them.
		return match[2], ""
	}
	return match[2], stamped.UTC().Format(time.RFC3339)
}

// shorten renders a revision at the length a person reads, which is the length
// a pseudo-version carries.
func shorten(revision string) string {
	return revision[:min(shortCommitLen, len(revision))]
}

// Platform returns the operating system and architecture as "os/arch".
func (i Info) Platform() string {
	return i.OS + "/" + i.Arch
}

// String renders a single-line summary suitable for `feat version`.
func (i Info) String() string {
	return fmt.Sprintf("feat %s (commit %s, built %s, %s %s)",
		i.Version, i.Commit, i.Date, i.GoVersion, i.Platform())
}
