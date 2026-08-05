package paths

import (
	"path/filepath"
	"testing"
)

// TestBroadDirectoriesAreRefused checks the list that configuration validation
// and the Git adapter share.
//
// It is one list on purpose: the package that decides whether a configured
// worktree root is acceptable and the package that creates and later removes
// directories under it must answer the same question the same way.
func TestBroadDirectoriesAreRefused(t *testing.T) {
	broad := []string{
		"/", "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/media", "/mnt",
		"/opt", "/private", "/root", "/sbin", "/srv", "/sys", "/tmp", "/usr",
		"/Users", "/var",
		// Not on the list, but one component deep, which is the rule the list
		// illustrates rather than an exception to it.
		"/anything",
		// Trailing separators and unclean forms are the same directories.
		"/usr/", "/var/./", "/opt/../opt",
		// A path Feat cannot resolve is a path it must not act on.
		"", "relative/path", ".",
	}
	for _, path := range broad {
		if !Broad(path) {
			t.Errorf("Broad(%q) = false, want true: it is a directory Feat must not own", path)
		}
	}

	owned := []string{
		"/srv/feat/worktrees",
		"/var/lib/feat",
		"/opt/feat/state",
		filepath.Join("/state", "feat"),
	}
	for _, path := range owned {
		if Broad(path) {
			t.Errorf("Broad(%q) = true, want false: it is deep enough to be a directory Feat owns", path)
		}
	}
}

// TestDepthCountsComponentsBelowTheRoot checks the rule the list rests on.
func TestDepthCountsComponentsBelowTheRoot(t *testing.T) {
	for path, want := range map[string]int{
		"/":            0,
		"/usr":         1,
		"/usr/local":   2,
		"/usr/local/":  2,
		"/a/b/c/d":     4,
		"/a/./b/../b":  2,
		"":             0,
		"relative/two": 2,
	} {
		if got := Depth(path); got != want {
			t.Errorf("Depth(%q) = %d, want %d", path, got, want)
		}
	}
}

// TestUnderIsDirectional checks the containment test cleanup depends on.
//
// A sibling whose name merely starts with the root's name is the case worth
// stating: "/state/feat-old" is not inside "/state/feat", and treating it as
// though it were would put somebody else's directory in a removal plan.
func TestUnderIsDirectional(t *testing.T) {
	const root = "/state/feat"

	for _, tc := range []struct {
		target string
		want   bool
	}{
		{root, true},
		{root + "/worktrees", true},
		{root + "/worktrees/task/repo", true},
		{root + "/./worktrees", true},
		{"/state/feat-old/worktrees", false},
		{"/state", false},
		{"/state/other", false},
		{root + "/../elsewhere", false},
		{"", false},
	} {
		if got := Under(root, tc.target); got != tc.want {
			t.Errorf("Under(%q, %q) = %t, want %t", root, tc.target, got, tc.want)
		}
	}

	if Under("", "/state/feat") {
		t.Error("Under with no root reported containment")
	}
}
