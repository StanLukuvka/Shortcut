package model

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The schema's pieces, as docs/shortcut.schema.json holds them.
type schemaProps map[string]struct {
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Pattern     string      `json:"pattern"`
	Required    []string    `json:"required"`
	Properties  schemaProps `json:"properties"`
}

type schemaDoc struct {
	Properties struct {
		Version struct {
			Const float64 `json:"const"`
		} `json:"version"`
		Shortcuts struct {
			PropertyNames struct {
				Pattern string `json:"pattern"`
			} `json:"propertyNames"`
		} `json:"shortcuts"`
	} `json:"properties"`
	Defs struct {
		Entry struct{ Properties schemaProps } `json:"entry"`
		Arg   struct{ Properties schemaProps } `json:"arg"`
	} `json:"$defs"`
}

func loadSchema(t *testing.T) schemaDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "shortcut.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s schemaDoc
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// modelSource is the text of this package's non-test files.
func modelSource(t *testing.T) string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	var b strings.Builder
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

// argFields finds the case list of ValidateArgs that holds "rest": the fields an arg may have.
func argFields(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "entry.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok || found != nil {
			return true
		}
		var fields []string
		for _, e := range cc.List {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, _ := strconv.Unquote(lit.Value)
				fields = append(fields, s)
			}
		}
		if slices.Contains(fields, "rest") {
			found = fields
		}
		return true
	})
	if found == nil {
		t.Fatal("no list of args fields in entry.go")
	}
	slices.Sort(found)
	return found
}

func TestSchema_FieldsMatchTheCode(t *testing.T) {
	s := loadSchema(t)
	if got, want := sortedKeys(s.Defs.Entry.Properties), sortedKeys(knownFields); !slices.Equal(got, want) {
		t.Errorf("entry: schema %v, code %v", got, want)
	}
	if got, want := sortedKeys(s.Defs.Arg.Properties), argFields(t); !slices.Equal(got, want) {
		t.Errorf("arg: schema %v, code %v", got, want)
	}
}

func TestSchema_FormatMatchesTheCode(t *testing.T) {
	s := loadSchema(t)
	src := modelSource(t)
	if int(s.Properties.Version.Const) != RegistryVersion {
		t.Errorf("schema version %v, code %d", s.Properties.Version.Const, RegistryVersion)
	}
	if p := s.Properties.Shortcuts.PropertyNames.Pattern; !strings.Contains(src, p) {
		t.Errorf("the name pattern %s is not in the code", p)
	}
	if p := strings.Trim(s.Defs.Arg.Properties["name"].Pattern, "^$"); !strings.Contains(src, p) {
		t.Errorf("the arg name pattern %s is not in the code", p)
	}
}

var schemaSamples = map[string]any{"string": "x", "boolean": true, "array": []any{}}

func TestSchema_EveryFieldIsAccepted(t *testing.T) {
	e := testkit.New(t)
	s := loadSchema(t)
	entry := map[string]any{}
	for k, v := range s.Defs.Entry.Properties {
		entry[k] = schemaSamples[v.Type]
	}
	delete(entry, "script")
	arg := map[string]any{}
	for k, v := range s.Defs.Arg.Properties {
		arg[k] = schemaSamples[v.Type]
	}
	arg["name"] = "x"
	arg["required"] = false // required can't go with a default
	entry["args"] = []any{arg}
	e.WriteImport(e.Store, "ns", map[string]any{"version": 1, "description": "x", "shortcuts": map[string]any{"go": entry}})

	// what `update ns -y` checks before it approves: the file is usable and every entry can run
	group, ok := LoadStore(e.Store).Namespace("ns")
	if !ok {
		t.Fatal("no namespace ns")
	}
	if group.Error != nil {
		t.Fatalf("namespace error: %s", *group.Error)
	}
	if errs := EntryErrors(group); len(errs) > 0 {
		t.Errorf("entry errors: %v", errs)
	}
}
