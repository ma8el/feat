package compose

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is an installed Docker Compose version.
//
// Parsed reports whether it could be read at all. An unreadable version is not
// treated as too old: refusing to launch because a tool printed something
// unfamiliar would make every Docker release an outage, and the failure it
// guards against announces itself clearly when it happens.
type Version struct {
	Major, Minor int
	Text         string
	Parsed       bool
}

// ParseVersion reads what `docker compose version --short` printed.
func ParseVersion(text string) Version {
	version := Version{Text: strings.TrimSpace(text)}

	digits := strings.TrimPrefix(version.Text, "v")
	fields := strings.SplitN(digits, ".", 3)
	if len(fields) < 2 {
		return version
	}
	major, err := strconv.Atoi(fields[0])
	if err != nil {
		return version
	}
	minor, err := strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil {
		return version
	}
	version.Major, version.Minor, version.Parsed = major, minor, true
	return version
}

// Below reports whether the version is older than the given minimum. An
// unparsed version is never below one: see the type's own comment.
func (v Version) Below(minimum Version) bool {
	if !v.Parsed {
		return false
	}
	if v.Major != minimum.Major {
		return v.Major < minimum.Major
	}
	return v.Minor < minimum.Minor
}

// String renders the version for a message.
func (v Version) String() string {
	if !v.Parsed {
		if v.Text == "" {
			return "an unreported version"
		}
		return strconv.Quote(v.Text)
	}
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}
