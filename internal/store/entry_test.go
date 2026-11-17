package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

var schemaSamples = map[string]any{"string": "x", "boolean": true, "array": []any{}}

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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
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

// The schema's pieces, as docs/shortcut.schema.json holds them.
type schemaProps map[string]struct {
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Pattern     string      `json:"pattern"`
	Required    []string    `json:"required"`
	Properties  schemaProps `json:"properties"`
}

func rewrittenHostile(t *testing.T) platform.Doc {
	t.Helper()
	reg := doc(t, hostileSrc)
	shortcuts, ok := reg.Object("shortcuts")
	if !ok {
		t.Fatal("no shortcuts")
	}
	inline := "true"
	raw, err := (&Entry{Inline: &inline}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	shortcuts["c"] = raw
	if raw, err = shortcuts.Bytes(); err != nil {
		t.Fatal(err)
	}
	reg["shortcuts"] = raw
	return reg
}

// rewritten is hostileSrc with one entry added, as a store file is written: top-level keys sorted, every value as it was.
const rewritten = "{\n  \"n\": [\n    1.0,\n    \"é \"\n  ],\n  \"shortcuts\": {\n    \"a\": {\n      \"inline\": \"x<>&é\\u00e9 \\u2028\\u0007\\ud83d\\ude00😀\\t\\n\\\"\\\\/\",\n      \"future\": {\n        \"b\": [\n          1e400,\n          -0,\n          {\n            \"z\": 1,\n            \"a\": 2,\n            \"z\": 3\n          },\n          true,\n          false,\n          null,\n          {},\n          [],\n          [\n            {\n              \"k\": 1,\n              \"k\": 2\n            }\n          ]\n        ]\n      }\n    },\n    \"a2\": {\n      \"description\": \"d\",\n      \"inline\": \"y\"\n    },\n    \"c\": {\n      \"inline\": \"true\"\n    }\n  },\n  \"version\": 1,\n  \"zed\": 2\n}\n"

// The next two literals are a hostile file and what writing makes of it: a key repeated at the top
// and inside an entry, escapes, HTML characters, U+2028, number text, empty containers and a surrogate pair.
const hostileSrc = "{\"zed\": 1.0, \"version\": 1, \"shortcuts\": {\"a\": {\"inline\": \"x<>&é\\u00e9 \\u2028\\u0007\\ud83d\\ude00😀\\t\\n\\\"\\\\/\", \"future\": {\"b\": [1e400, -0, {\"z\": 1, \"a\": 2, \"z\": 3}, true, false, null, {}, [], [{\"k\":1,\"k\":2}]]}}, \"a2\": {\"description\": \"d\", \"inline\": \"y\"}}, \"n\": [1.0, \"é \"], \"zed\": 2}"

func TestEntry_FromValueMessages(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[]`, "'x' is not a valid entry: expected a JSON object"},
		{`{"inline": "a", "zed": 1, "bar": 2}`, "'x' is not a valid entry: unknown field: bar, zed"},
		{`{"inline": "a", "export": true, "from": {}}`, "'x' is not a valid entry: unknown field: export, from"},
		{`{"inline": "a", "description": null}`, "'x' is not a valid entry: 'description' must be a string"},
		{`{"inline": 1}`, "'x' is not a valid entry: 'inline' must be a string"},
		{`{"inline": "a", "cwd": 5}`, "'x' is not a valid entry: 'cwd' must be a string"},
		{`{"inline": "a", "cwd": null}`, "'x' is not a valid entry: 'cwd' must be a string"},
		{`{"inline": null}`, "'x' is not a valid entry: 'inline' must be a string"},
		{`{"script": null}`, "'x' is not a valid entry: 'script' must be a string"},
		{`{"inline": "a", "icon": "x", "created": "x", "created_by": "x"}`, "'x' is not a valid entry: unknown field: created, created_by, icon"},
		{`{"inline": "a", "args": [{"name": "a"}]}`, "'x' is not a valid entry: unknown field: args"},
		{`{"inline": "a", "confirm": "yes"}`, "'x' is not a valid entry: 'confirm' must be a boolean"},
		{`{}`, "'x' is not a valid entry: it needs exactly one of 'inline' and 'script'"},
		{`{"inline": "a", "script": "b"}`, "'x' is not a valid entry: it needs exactly one of 'inline' and 'script'"},
	}
	for _, c := range cases {
		_, err := DecodeEntry("x", []byte(c.in))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: got %v, want %q", c.in, err, c.want)
		}
	}
}

func TestEntry_EmptyInlineIsStillSet(t *testing.T) {
	e, err := DecodeEntry("y", []byte(`{"inline": ""}`))
	if err != nil || e.Inline == nil {
		t.Fatalf("%+v %v", e, err)
	}
	if got, _ := e.Raw(); string(got) != `{"inline":""}` {
		t.Error(string(got))
	}
}

func TestDoc_RewriteKeepsUntouchedEntries(t *testing.T) {
	path := filepath.Join(testkit.New(t).Tmp, "s.json")
	if err := platform.WriteJSONAtomic(path, rewrittenHostile(t)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != rewritten {
		t.Errorf("got  %q\nwant %q", got, rewritten)
	}
}

func TestDecodeEntry_KeepsWellTypedFieldsOfABrokenEntry(t *testing.T) {
	e, err := DecodeEntry("x", []byte(`{"script": "s.sh", "cwd": "sub", "future": 1, "confirm": "yes"}`))
	if err == nil || err.Error() != "'x' is not a valid entry: unknown field: future" {
		t.Fatalf("error %v", err)
	}
	if e == nil || e.Script == nil || *e.Script != "s.sh" || e.Cwd == nil || *e.Cwd != "sub" || e.Confirm {
		t.Errorf("entry %+v", e)
	}
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

func TestFormat_IsFrozenForItsVersion(t *testing.T) {
	var got []string
	for _, k := range FileKeys() {
		got = append(got, "file "+k)
	}
	for _, f := range Fields() {
		got = append(got, "entry "+f.Name+" "+f.Type)
	}
	path := fmt.Sprintf("testdata/format-v%d.txt", RegistryVersion)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: there is no frozen list for version %d; add one", err, RegistryVersion)
	}
	var want []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			want = append(want, l)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("the file format changed but RegistryVersion is still %d: bump it and add testdata/format-v<new>.txt\nfrozen: %q\nnow:    %q", RegistryVersion, want, got)
	}
}
