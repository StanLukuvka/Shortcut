package testkit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// violations lists what in a test file bypasses the hub: importing os/exec, or calling t.TempDir, t.Setenv,
// os.Setenv or os.MkdirTemp. The sandbox rule only holds if every child and every temp dir comes from testkit.
func violations(fset *token.FileSet, f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" {
			out = append(out, fset.Position(imp.Pos()).String()+": imports os/exec; start processes through testkit")
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		pkg, _ := sel.X.(*ast.Ident)
		switch {
		case name == "TempDir" || name == "Setenv" && !(pkg != nil && pkg.Name == "os"):
			out = append(out, fset.Position(call.Pos()).String()+": calls "+name+"; use testkit.New")
		case pkg != nil && pkg.Name == "os" && (name == "Setenv" || name == "MkdirTemp"):
			out = append(out, fset.Position(call.Pos()).String()+": calls os."+name+"; use testkit.New")
		}
		return true
	})
	return out
}

func TestFixtures_TestsStayOnTheHub(t *testing.T) {
	// The rule itself: it must see each thing it is there to stop.
	src := `package x
import ("os"; "os/exec"; "testing")
func TestX(t *testing.T) {
	t.TempDir(); t.Setenv("A", "b"); os.Setenv("A", "b"); os.MkdirTemp("", ""); exec.Command("x")
	os.Getenv("HOME")
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := violations(fset, f); len(got) != 5 {
		t.Fatalf("rule found %d of 5 planted violations: %v", len(got), got)
	}

	// The tree: every _test.go outside internal/testkit.
	root := moduleRoot()
	fset = token.NewFileSet()
	var bad []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			switch {
			case rel == "internal/testkit", d.Name() == ".git", d.Name() == ".lad", d.Name() == "testdata", isNestedRepo(root, p):
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		bad = append(bad, violations(fset, f)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Error(b)
	}
}
