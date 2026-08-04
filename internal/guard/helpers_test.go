package guard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the module root directory.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine the location of the guard package")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the module root: %v", root, err)
	}
	return root
}

// skippedDirs are excluded from repository-wide source scans. testdata is
// excluded because fixtures legitimately contain sample paths and identifiers
// that never reach the binary.
var skippedDirs = map[string]bool{
	".git":         true,
	"bin":          true,
	"dist":         true,
	"node_modules": true,
	"testdata":     true,
	"vendor":       true,
}

// goFiles returns every Go source file in the repository, as paths relative to
// the module root and using forward slashes.
func goFiles(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skippedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files found under %s", root)
	}
	return files
}

// parseGoFile parses one repository-relative Go file.
func parseGoFile(t *testing.T, root, rel string) (*token.FileSet, *ast.File) {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	return fset, parsed
}

// hasPrefixAny reports whether rel starts with any of the given path prefixes.
func hasPrefixAny(rel string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}
