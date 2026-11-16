package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestSchema_FieldsMatchTheCode(t *testing.T) {
	s := loadSchema(t)
	var names []string
	for _, f := range Fields() {
		names = append(names, f.Name)
		if got := s.Defs.Entry.Properties[f.Name].Type; got != f.Type {
			t.Errorf("%s: schema type %q, code %q", f.Name, got, f.Type)
		}
	}
	slices.Sort(names)
	if got := sortedKeys(s.Defs.Entry.Properties); !slices.Equal(got, names) {
		t.Errorf("entry: schema %v, code %v", got, names)
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
	e.WriteImport(e.Store, "ns", map[string]any{"version": 1, "description": "x", "shortcuts": map[string]any{"go": entry}})

	// the file is usable and every entry can run
	group, ok := NewContext(e.Home, nil).Store(e.Store).Namespace("ns")
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
