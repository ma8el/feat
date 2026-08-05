package git

import "strings"

// listOf renders names for a message, so that a report about several
// repositories reads as a sentence rather than as a slice.
func listOf(values []string) string {
	switch len(values) {
	case 0:
		return "none"
	case 1:
		return values[0]
	default:
		return strings.Join(values[:len(values)-1], ", ") + " and " + values[len(values)-1]
	}
}

// plural returns the suffix that makes "repositor" agree with a count.
func plural(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}
