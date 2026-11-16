package testkit

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// layers is the import table: for each package (a path from the module root, "." for the root), the module-internal
// packages its non-test files may import. Stdlib is always allowed. A package not listed fails, so a new one gets placed here.
var layers = map[string][]string{
	"cmd/shortcut":            {"internal/cli"},
	"internal/platform":       {},
	"internal/store":          {"internal/platform"},
	"internal/textio":         {"internal/platform"},
	"internal/store/approval": {"internal/store", "internal/platform"},
	"internal/cli/argparse":   {},
	"internal/cli":            {"internal/cli/argparse", "internal/store/approval", "internal/store", "internal/textio", "internal/platform"},
	"internal/testkit":        {},
}

// The packages may only import what the table says, read from the import lines of their non-test files.
func TestLayers_PackagesImportOnlyWhatTheTableAllows(t *testing.T) {
	root := moduleRoot()
	const module = "github.com/StanLukuvka/Shortcut"
	fset := token.NewFileSet()
	var bad []string
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".lad", "testdata":
				return filepath.SkipDir
			}
			if isNestedRepo(root, p) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		pkg, _ := filepath.Rel(root, filepath.Dir(p))
		pkg = filepath.ToSlash(pkg)
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		seen[pkg] = true
		allowed, listed := layers[pkg]
		if !listed {
			bad = append(bad, "package "+pkg+" is not in the layer table; place it in layers_test.go")
			return nil
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			rel, internal := strings.CutPrefix(path, module)
			if !internal {
				continue // stdlib: no dependencies exist (deps_test.go)
			}
			if rel = strings.TrimPrefix(rel, "/"); rel == "" {
				rel = "."
			}
			ok := false
			for _, a := range allowed {
				ok = ok || a == rel
			}
			if !ok {
				bad = append(bad, "package "+pkg+" ("+filepath.ToSlash(p[len(root)+1:])+") imports "+rel+", which the layer table does not allow it")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing may be left in the table for a package that is gone.
	var all []string
	for pkg := range layers {
		all = append(all, pkg)
	}
	sort.Strings(all)
	for _, pkg := range all {
		if !seen[pkg] {
			bad = append(bad, "layer table lists "+pkg+", which has no non-test .go files")
		}
	}
	for _, b := range bad {
		t.Error(b)
	}
}

// isNestedRepo is true for a folder below root that holds its own .git (a file or a folder): another checkout, such as
// a worktree under .claude/worktrees, is not this repository's code.
func isNestedRepo(root, dir string) bool {
	if dir == root {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}
