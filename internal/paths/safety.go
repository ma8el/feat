package paths

import (
	"path/filepath"
	"strings"
)

// broadPaths are directories Feat must never own.
//
// Feat creates task worktrees under a configured root and later removes them,
// so a root that resolves to a shared directory turns a routine cleanup into a
// destructive one. Every name below is one component deep, which Depth already
// rejects; the list is kept because it says which directories the rule is about,
// and because a later entry may well be deeper than one component.
var broadPaths = map[string]bool{
	"/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true,
	"/home": true, "/lib": true, "/media": true, "/mnt": true, "/opt": true,
	"/private": true, "/root": true, "/sbin": true, "/srv": true, "/sys": true,
	"/tmp": true, "/usr": true, "/Users": true, "/var": true,
}

// Broad reports whether a path is a directory Feat must not create or remove
// task resources under.
//
// It is deliberately the same question for the package that validates a
// configured worktree root and for the package that creates and later removes
// directories beneath it: one list, one answer, checked in both places.
//
// A relative path is broad, because a path Feat cannot resolve is a path it must
// not act on.
func Broad(path string) bool {
	if path == "" {
		return true
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return true
	}
	return Depth(cleaned) < 2 || broadPaths[filepath.ToSlash(cleaned)]
}

// Depth counts the path components below the filesystem root.
func Depth(path string) int {
	trimmed := strings.Trim(filepath.ToSlash(filepath.Clean(path)), "/")
	if trimmed == "" || trimmed == "." {
		return 0
	}
	return len(strings.Split(trimmed, "/"))
}

// Under reports whether target is root itself or a path inside it.
//
// Both are cleaned first, so "a/b/../b" and "a/b" are the same directory. Neither
// is resolved through symbolic links: that is a question about the filesystem as
// it is right now, and the caller that is about to create or remove something
// asks it of the filesystem rather than of a string.
func Under(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return true
	}
	return strings.HasPrefix(target, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}
