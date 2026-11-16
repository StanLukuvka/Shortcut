package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestDoc_Order(t *testing.T) {
	d := doc(t, `{"b": 1, "a": 2, "b": 3}`)
	if got := compactOf(t, d); strings.Join(d.Keys, ",") != "b,a" || got != `{"b":3,"a":2}` {
		t.Fatalf("keys %v, %s", d.Keys, got)
	}
	d.Set("c", []byte("null"))
	d.Delete("b")
	d.Delete("zz")
	if got := compactOf(t, d); got != `{"a":2,"c":null}` {
		t.Fatal(got)
	}
	if d.Has("b") {
		t.Fatal("b not deleted")
	}
}

func TestDoc_RefusesWhatIsNotValidUTF8(t *testing.T) {
	for _, src := range []string{
		`{"k": "\udce9"}`,        // a lone low surrogate, as a bad file name would have been escaped
		`{"k": ["\ud800"]}`,      // lone high surrogate
		`{"k": "\ud800\u0041"}`,  // high surrogate followed by something that is not a low one
		`{"k": "\ud800 \udc00"}`, // a pair needs the escapes side by side
		`{"k": "\udc00"}`,        // lone low surrogate
		`{"k": "\udc00\ud800"}`,  // low before high
		`{"\udcff": 1}`,          // in a key too
		"{\"k\": \"\xe9\"}",      // a raw invalid byte
	} {
		_, err := ParseDoc([]byte(src))
		if !errors.Is(err, ErrNotUTF8) {
			t.Errorf("%q: %v, want ErrNotUTF8", src, err)
		}
	}
	if _, err := ParseDoc([]byte{'"', 0xe9, '"'}); !errors.Is(err, ErrNotUTF8) {
		t.Errorf("raw byte: %v", err)
	}
	for src, want := range map[string]string{
		`"\ud83d\ude00"`:   "\U0001F600", // a proper pair, as ensure_ascii wrote an emoji
		`"\uD83D\uDE00"`:   "\U0001F600",
		"\"\U0001F600\"":   "\U0001F600",
		"\"\\ufffd\"":      "\uFFFD", // an escaped U+FFFD is a real character, not a lost byte
		"\"\uFFFD\"":       "\uFFFD",
		`"a\\udce9"`:       `a\udce9`, // an escaped backslash, then the plain text udce9
		`"\"\ud83d\ude00"`: "\"\U0001F600",
	} {
		got, ok := doc(t, `{"k": `+src+`}`).String("k")
		if !ok || got != want {
			t.Errorf("%s: got %q, %v, want %q", src, got, ok, want)
		}
	}
}

func TestDoc_KeepsOrderAndNumberText(t *testing.T) {
	d := doc(t, `{"zed": 1, "version": 1, "n": [1.0, 12345678901234567890, 1e400, 0, -0, 2E2], "alpha": {}, "list": [], "a": {"k": 1, "b": [], "c": {}}}`)
	if got := strings.Join(d.Keys, ","); got != "zed,version,n,alpha,list,a" {
		t.Errorf("key order %s", got)
	}
	for k, want := range map[string]string{
		"n": `[1.0,12345678901234567890,1e400,0,-0,2E2]`, "alpha": `{}`, "list": `[]`, "a": `{"k":1,"b":[],"c":{}}`,
	} {
		if got := string(d.Raw[k]); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}

func TestDoc_DuplicatesDepthAndWhatIsNotJSON(t *testing.T) {
	d := doc(t, `{"b": 1, "a": 2, "b": 3}`)
	if strings.Join(d.Keys, ",") != "b,a" || string(d.Raw["b"]) != "3" { // first place, last value
		t.Errorf("%v", d)
	}
	for _, src := range []string{
		strings.Repeat("[", 20000), strings.Repeat("[", 20000) + strings.Repeat("]", 20000), // deeper than encoding/json reads
		"\xef\xbb\xbf{}", `{"a": NaN}`, `[Infinity]`, `[-Infinity]`, `{} x`, `{"a": 1,}`, `[1 2]`, ``, `{`, `"a`, `01`, "\"a\x01\"",
		`[]`, `1`, // valid JSON, but not an object
	} {
		if _, err := ParseDoc([]byte(src)); err == nil {
			t.Errorf("%.30q loaded", src)
		}
	}
}

func TestDoc_RefusesInvalidUTF8OnWrite(t *testing.T) {
	bad := "caf\xe9"
	with := func(k, raw string) *Doc {
		d := &Doc{}
		d.Set(k, []byte(raw))
		return d
	}
	for name, d := range map[string]*Doc{
		"key":           with(bad, `"x"`),
		"value":         with("k", `"`+bad+`"`),
		"in an array":   with("k", `["ok","`+bad+`"]`),
		"in an object":  with("a", `{"b":[{"c":"`+bad+`"}]}`),
		"two bad bytes": with("k", "\"\xff\xfe\""),
		"half a rune":   with("k", "\"\xe2\x82\""),
		"WTF-8 triple":  with("k", "\"\xed\xa0\x80\""), // an encoded surrogate is not UTF-8 either
	} {
		if out, err := d.Bytes(); !errors.Is(err, ErrNotUTF8) || out != nil {
			t.Errorf("%s: got %q, %v", name, out, err)
		}
	}
	for _, s := range []string{bad, "\xff\xfe", "\xe2\x82", "\xed\xa0\x80"} {
		if err := (&Doc{}).SetString("k", s); !errors.Is(err, ErrNotUTF8) {
			t.Errorf("SetString(%q): %v", s, err)
		}
	}
}

func TestDoc_RoundTripsAnOldStore(t *testing.T) {
	// indent 2, key order as written (not sorted), numbers as written, empty containers on one line
	src := "{\n  \"zed\": 1,\n  \"version\": 1,\n  \"n\": [\n    1.0,\n    12345678901234567890,\n    1e400,\n    -0,\n    2E2\n  ],\n" +
		"  \"alpha\": {},\n  \"list\": [],\n  \"a\": {\n    \"k\": null,\n    \"b\": [],\n    \"c\": {},\n    \"t\": true\n  }\n}"
	path := filepath.Join(testkit.New(t).Tmp, "s.json")
	if err := WriteJSONAtomic(path, doc(t, src)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != src+"\n" {
		t.Errorf("got %s\nwant %s", got, src)
	}
}

func TestDoc_WritesTextAsTheCharacters(t *testing.T) {
	d := &Doc{}
	for _, kv := range [][2]string{{"html", "<a href=\"x\">&</a>"}, {"accent", "café 中文 😀"}, {"quote", "a\"b\\c\nd\te"}} {
		if err := d.SetString(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	got := compactOf(t, d)
	want := `{"html":"<a href=\"x\">&</a>","accent":"café 中文 😀","quote":"a\"b\\c\nd\te"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if s, ok := doc(t, got).String("accent"); !ok || s != "café 中文 😀" {
		t.Errorf("did not round-trip: %q", s)
	}
}

func TestDoc_Normal(t *testing.T) {
	d := doc(t, `{"b": {"z": 1, "a": [{"y": 1, "x": 2}]}, "a": 1}`)
	got, err := d.Normal()
	want := "{\n  \"a\": 1,\n  \"b\": {\n    \"a\": [\n      {\n        \"x\": 2,\n        \"y\": 1\n      }\n    ],\n    \"z\": 1\n  }\n}\n"
	if err != nil || string(got) != want {
		t.Errorf("got %q, %v", got, err)
	}
	if first := compactOf(t, d); first != `{"b":{"z":1,"a":[{"y":1,"x":2}]},"a":1}` {
		t.Errorf("Normal changed the original: %s", first)
	}
}

// The next three literals are what the writer produced before Doc, for a hostile file: a key repeated at the top
// and inside an entry, escapes, HTML characters, U+2028, number text, empty containers and a surrogate pair.
const hostileSrc = "{\"zed\": 1.0, \"version\": 1, \"shortcuts\": {\"a\": {\"inline\": \"x<>&é\\u00e9 \\u2028\\u0007\\ud83d\\ude00😀\\t\\n\\\"\\\\/\", \"future\": {\"b\": [1e400, -0, {\"z\": 1, \"a\": 2, \"z\": 3}, true, false, null, {}, [], [{\"k\":1,\"k\":2}]]}}, \"a2\": {\"description\": \"d\", \"inline\": \"y\"}}, \"n\": [1.0, \"é \"], \"zed\": 2}"

// rewritten is hostileSrc with one entry added, as a store file is written.
const rewritten = "{\n  \"zed\": 2,\n  \"version\": 1,\n  \"shortcuts\": {\n    \"a\": {\n      \"inline\": \"x<>&éé \\u2028\\u0007😀😀\\t\\n\\\"\\\\/\",\n      \"future\": {\n        \"b\": [\n          1e400,\n          -0,\n          {\n            \"z\": 3,\n            \"a\": 2\n          },\n          true,\n          false,\n          null,\n          {},\n          [],\n          [\n            {\n              \"k\": 2\n            }\n          ]\n        ]\n      }\n    },\n    \"a2\": {\n      \"description\": \"d\",\n      \"inline\": \"y\"\n    },\n    \"c\": {\n      \"description\": \"\",\n      \"inline\": \"true\"\n    }\n  },\n  \"n\": [\n    1.0,\n    \"é \"\n  ]\n}\n"

// normalized is the same document as approval hashes it.
const normalized = "{\n  \"n\": [\n    1.0,\n    \"é \"\n  ],\n  \"shortcuts\": {\n    \"a\": {\n      \"future\": {\n        \"b\": [\n          1e400,\n          -0,\n          {\n            \"a\": 2,\n            \"z\": 3\n          },\n          true,\n          false,\n          null,\n          {},\n          [],\n          [\n            {\n              \"k\": 2\n            }\n          ]\n        ]\n      },\n      \"inline\": \"x<>&éé \\u2028\\u0007😀😀\\t\\n\\\"\\\\/\"\n    },\n    \"a2\": {\n      \"description\": \"d\",\n      \"inline\": \"y\"\n    },\n    \"c\": {\n      \"description\": \"\",\n      \"inline\": \"true\"\n    }\n  },\n  \"version\": 1,\n  \"zed\": 2\n}\n"

func rewrittenHostile(t *testing.T) *Doc {
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
	if err := WriteJSONAtomic(path, rewrittenHostile(t)); err != nil {
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
	items := ItemsOf(doc(t, `{"shortcuts": {"a": {"inline": "x", "description": 5, "cwd": null}, "b": {"inline": "x", "description": "d"}, "c": 7}}`))
	for _, c := range []struct{ name, field, want string }{
		{"a", "description", "5"}, {"a", "cwd", ""}, {"a", "inline", "x"}, {"a", "script", ""},
		{"b", "description", "d"}, {"c", "description", ""},
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
