package claude

import "fmt"

// The Claude Code version this adapter was verified against.
//
// docs/06-technical-architecture.md requires that the exact CLI flags and hook
// schemas be verified against the installed version rather than assumed. These
// constants record which version that verification was performed on, so the
// claim is falsifiable: the flags in settings.go, the hook event names in
// hooks.go, and the payload fields in parse.go were all read from this build.
//
// A newer version is expected to work and is not refused. It is reported,
// because the failure mode of a changed hook schema is silence — a session that
// runs perfectly well while Feat never hears from it again — and silence is
// worth a warning that names its likely cause.
//
// The flags this adapter passes were checked against the same build:
// --settings, --append-system-prompt-file, and the initial prompt argument.
// --append-system-prompt-file is registered but not listed in the top-level
// help, which is the one flag here worth re-checking when this range moves.
const (
	// VerifiedMajor and VerifiedMinor are the release line this adapter was
	// checked against.
	VerifiedMajor = 2
	VerifiedMinor = 1
	// VerifiedPatch is the exact build the hook payloads were read from.
	VerifiedPatch = 220
)

// Verified renders the version this adapter was checked against.
func Verified() string {
	return fmt.Sprintf("%d.%d.%d", VerifiedMajor, VerifiedMinor, VerifiedPatch)
}

// Unverified reports whether an installed version is outside the range this
// adapter was checked against, and why it is worth saying so.
//
// Only the major version is treated as a boundary. Hook names and payload
// fields have been stable within a major release, and warning about every patch
// would make the warning meaningless long before it was ever true.
func (v Version) Unverified() string {
	if !v.Parsed {
		return "Feat could not read the installed Claude Code version from " + quoted(v.Text) +
			", so it cannot tell whether this build's hook integration matches it"
	}
	if v.Major == VerifiedMajor {
		return ""
	}
	return fmt.Sprintf(
		"Claude Code %s is a different major version from the %s this build's hooks were verified against; "+
			"if task state stops updating, the hook schema is the first thing to check",
		v.String(), Verified())
}

func quoted(value string) string { return fmt.Sprintf("%q", value) }
