package version

import (
	"fmt"
	"runtime"
)

// Build identity, overridden at link time. See the Makefile's LDFLAGS.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
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
	return Info{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
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
