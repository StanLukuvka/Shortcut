package repocheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// root is the module root, two levels above this package (go test runs in the package's folder).
const root = "../.."

// goFiles is every .go file of this repository, as a path below root with forward slashes, sorted: the test files
// when tests is true, the others when it is false. It skips .git, .lad, testdata and any folder below root with its
// own .git (a file or a folder): another checkout, such as a worktree under .claude/worktrees, is not this
// repository's code.
func goFiles(t *testing.T, tests bool) []string {
	t.Helper()
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".lad", "testdata":
				return filepath.SkipDir
			}
			if p != abs {
				if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") && strings.HasSuffix(p, "_test.go") == tests {
			rel, err := filepath.Rel(abs, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
