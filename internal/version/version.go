package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
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

	// shortCommitLen is the revision length a Go pseudo-version uses, so the
	// commit of a checkout build reads as the tail of its version.
	shortCommitLen = 12

	// dirtySuffix marks a revision that the working tree had already moved past.
	dirtySuffix = "-dirty"
)

// Info describes the running binary.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	OS        string
	Arch      string
}

// Get returns the build identity of the running binary.
func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		// A binary stripped of its build information answers for nothing.
		bi = nil
	}

	info := resolve(Info{Version: version, Commit: commit, Date: date}, bi)
	info.GoVersion = runtime.Version()
	info.OS = runtime.GOOS
	info.Arch = runtime.GOARCH
	return info
}

// resolve settles version, commit, and date one field at a time: a value the
// linker supplied wins, then whatever the toolchain embedded, then the
// placeholder. Each field is independent, because a build can know its revision
// without knowing its version, and learning one must not blank the other.
func resolve(linked Info, bi *debug.BuildInfo) Info {
	var embedded Info
	if bi != nil {
		embedded = fromBuildInfo(bi)
	}

	return Info{
		Version: firstKnown(linked.Version, embedded.Version, devVersion),
		Commit:  firstKnown(linked.Commit, embedded.Commit, unknown),
		Date:    firstKnown(linked.Date, embedded.Date, unknown),
	}
}

// firstKnown returns the linked value unless it is absent — empty, or still the
// placeholder the Makefile falls back to when Git could not answer either.
func firstKnown(linked, embedded, placeholder string) string {
	if linked != "" && linked != placeholder {
		return linked
	}
	if embedded != "" {
		return embedded
	}
	return placeholder
}

// fromBuildInfo reads the identity the toolchain embedded: the module version
// of a binary installed at a tag, and the revision and commit timestamp of one
// built inside a checkout. Fields it cannot answer come back empty so that the
// caller keeps what it already knew.
func fromBuildInfo(bi *debug.BuildInfo) Info {
	var info Info
	if v := bi.Main.Version; v != devel {
		info.Version = v
	}

	var revision string
	var modified bool
	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.time":
			info.Date = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}

	if revision != "" {
		info.Commit = shorten(revision)
		if modified {
			info.Commit += dirtySuffix
		}
	}
	return info
}

// shorten cuts a full revision down to the length a pseudo-version carries,
// leaving anything already shorter alone.
func shorten(revision string) string {
	if len(revision) <= shortCommitLen {
		return revision
	}
	return revision[:shortCommitLen]
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
