package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// The next three literals are a hostile file and what writing and approval make of it: a key repeated at the top
// and inside an entry, escapes, HTML characters, U+2028, number text, empty containers and a surrogate pair.
const hostileSrc = "{\"zed\": 1.0, \"version\": 1, \"shortcuts\": {\"a\": {\"inline\": \"x<>&é\\u00e9 \\u2028\\u0007\\ud83d\\ude00😀\\t\\n\\\"\\\\/\", \"future\": {\"b\": [1e400, -0, {\"z\": 1, \"a\": 2, \"z\": 3}, true, false, null, {}, [], [{\"k\":1,\"k\":2}]]}}, \"a2\": {\"description\": \"d\", \"inline\": \"y\"}}, \"n\": [1.0, \"é \"], \"zed\": 2}"

// rewritten is hostileSrc with one entry added, as a store file is written.
const rewritten = "{\n  \"zed\": 2,\n  \"version\": 1,\n  \"shortcuts\": {\n    \"a\": {\n      \"inline\": \"x<>&éé \\u2028\\u0007😀😀\\t\\n\\\"\\\\/\",\n      \"future\": {\n        \"b\": [\n          1e400,\n          -0,\n          {\n            \"z\": 3,\n            \"a\": 2\n          },\n          true,\n          false,\n          null,\n          {},\n          [],\n          [\n            {\n              \"k\": 2\n            }\n          ]\n        ]\n      }\n    },\n    \"a2\": {\n      \"description\": \"d\",\n      \"inline\": \"y\"\n    },\n    \"c\": {\n      \"description\": \"\",\n      \"inline\": \"true\"\n    }\n  },\n  \"n\": [\n    1.0,\n    \"é \"\n  ]\n}\n"

// normalized is the same document as approval hashes it.
const normalized = "{\n  \"n\": [\n    1.0,\n    \"é \"\n  ],\n  \"shortcuts\": {\n    \"a\": {\n      \"future\": {\n        \"b\": [\n          1e400,\n          -0,\n          {\n            \"a\": 2,\n            \"z\": 3\n          },\n          true,\n          false,\n          null,\n          {},\n          [],\n          [\n            {\n              \"k\": 2\n            }\n          ]\n        ]\n      },\n      \"inline\": \"x<>&éé \\u2028\\u0007😀😀\\t\\n\\\"\\\\/\"\n    },\n    \"a2\": {\n      \"description\": \"d\",\n      \"inline\": \"y\"\n    },\n    \"c\": {\n      \"description\": \"\",\n      \"inline\": \"true\"\n    }\n  },\n  \"version\": 1,\n  \"zed\": 2\n}\n"

func rewrittenHostile(t *testing.T) *platform.Doc {
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
	shortcuts.Set("c", raw)
	if raw, err = shortcuts.Bytes(); err != nil {
		t.Fatal(err)
	}
	reg.Set("shortcuts", raw)
	return reg
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

func TestDoc_NormalIsSortedAndIndented(t *testing.T) {
	if got, err := rewrittenHostile(t).Normal(); err != nil || string(got) != normalized {
		t.Errorf("got  %q, %v\nwant %q", got, err, normalized)
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

func TestItem_TextShowsAWrongTypeAsWritten(t *testing.T) {
	items := ItemsOf(doc(t, `{"shortcuts": {"a": {"inline": "x", "description": 5, "cwd": null}, "b": {"inline": "x", "description": "d"}, "c": 7, "d": {"inline": "x", "description": ["a", "b"]}}}`))
	for _, c := range []struct{ name, field, want string }{
		{"a", "description", "5"}, {"a", "cwd", ""}, {"a", "inline", "x"}, {"a", "script", ""},
		{"b", "description", "d"}, {"c", "description", ""},
		{"d", "description", `["a","b"]`},
	} {
		item, _ := items.Get(c.name)
		if got := item.Text(c.field); got != c.want {
			t.Errorf("%s.%s = %q, want %q", c.name, c.field, got, c.want)
		}
	}
	if c, _ := items.Get("c"); c.Entry != nil || c.Err == nil {
		t.Errorf("a non-object entry: %+v", c)
	}
}
