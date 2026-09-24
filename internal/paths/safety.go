package paths

import (
	"path/filepath"
	"strings"
)

// broadPaths are directories Feat must never own. Feat creates and later removes
// task worktrees under a configured root, so a root resolving to a shared
// directory turns a routine cleanup into a destructive one. Every name here is
// one component deep, which Depth already rejects; a later entry may not be.
var broadPaths = map[string]bool{
	"/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true,
	"/home": true, "/lib": true, "/media": true, "/mnt": true, "/opt": true,
	"/private": true, "/root": true, "/sbin": true, "/srv": true, "/sys": true,
	"/tmp": true, "/usr": true, "/Users": true, "/var": true,
}

// Broad reports whether a path is a directory Feat must not create or remove
// task resources under. Configuration validation and the code that removes
// directories ask the same question of one list. A relative path is broad,
// because Feat must not act on a path it cannot resolve.
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

// Under reports whether target is root itself or a path inside it. Both are
// cleaned first, so "a/b/../b" and "a/b" are one directory. Neither is resolved
// through symbolic links: the caller about to create or remove something asks
// that of the filesystem rather than of a string.
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
