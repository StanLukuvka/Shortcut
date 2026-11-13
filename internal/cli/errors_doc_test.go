// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (Q-2). Don't hand-edit without checking the plan.
package cli

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const entryPrefix = "is not a valid entry: "

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// lits is the fixed text of a message: a string literal, literals joined by +, or the format of a Sprintf.
// nil when the message is built some other way.
func lits(e ast.Expr) []string {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, _ := strconv.Unquote(x.Value)
			return []string{s}
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return append(lits(x.X), lits(x.Y)...)
		}
	case *ast.ParenExpr:
		return lits(x.X)
	case *ast.CallExpr:
		if callName(x.Fun) == "fmt.Sprintf" && len(x.Args) > 0 {
			return lits(x.Args[0])
		}
	}
	return nil
}

func callName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name + "." + x.Sel.Name
		}
	}
	return ""
}

// goMessages: each message passed to Die, errors.New, fmt.Errorf, appended to problems, or returned by FileError.
func goMessages(t *testing.T) [][]string {
	var found [][]string
	for _, dir := range []string{"../model", "../approval", "."} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := goparser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			add := func(e ast.Expr) {
				if l := lits(e); l != nil {
					found = append(found, l)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					switch name := callName(x.Fun); {
					case name == "Die" || name == "errors.New" || name == "fmt.Errorf":
						if len(x.Args) > 0 {
							add(x.Args[0])
						}
					case name == "append" && len(x.Args) == 2:
						if id, ok := x.Args[0].(*ast.Ident); ok && id.Name == "problems" {
							add(x.Args[1])
						}
					}
				case *ast.FuncDecl:
					if x.Name.Name == "FileError" && x.Body != nil {
						ast.Inspect(x.Body, func(m ast.Node) bool {
							if r, ok := m.(*ast.ReturnStmt); ok {
								for _, v := range r.Results {
									add(v)
								}
							}
							return true
						})
					}
				}
				return true
			})
		}
	}
	return found
}

// py: test_errors.py::test_every_message_is_in_the_table
func TestEveryMessageIsInTheTable(t *testing.T) {
	raw, err := os.ReadFile("../../docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	found := goMessages(t)
	if len(found) <= 80 {
		t.Fatalf("found only %d messages", len(found))
	}
	t.Logf("%d messages", len(found))
	for _, whole := range found {
		for _, text := range whole {
			for _, part := range strings.Split(text, entryPrefix) {
				for _, piece := range formatVerb.Split(part, -1) {
					piece = strings.Trim(piece, " .:'")
					if len(piece) >= 6 && !strings.Contains(doc, piece) {
						t.Errorf("not in docs/errors.md: %q", piece)
					}
				}
			}
		}
	}
}
