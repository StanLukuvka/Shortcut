package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

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
		`"\ud83d\ude00"`:   "\U0001F600", // a surrogate pair is one emoji
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

func TestDoc_DuplicatesDepthAndWhatIsNotJSON(t *testing.T) {
	d := doc(t, `{"b": 1, "a": 2, "b": 3}`)
	if len(d) != 2 || string(d["b"]) != "3" { // last value
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
	with := func(k, raw string) Doc { return Doc{k: []byte(raw)} }
	for name, d := range map[string]Doc{
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
		if err := (Doc{}).SetString("k", s); !errors.Is(err, ErrNotUTF8) {
			t.Errorf("SetString(%q): %v", s, err)
		}
	}
}

func TestDoc_RoundTripsAnOldStore(t *testing.T) {
	// top-level keys come out sorted; values keep their text (numbers as written, nested objects as they were)
	src := "{\n  \"zed\": 1,\n  \"version\": 1,\n  \"n\": [\n    1.0,\n    12345678901234567890,\n    1e400,\n    -0,\n    2E2\n  ],\n" +
		"  \"alpha\": {},\n  \"list\": [],\n  \"a\": {\n    \"k\": null,\n    \"b\": [],\n    \"c\": {},\n    \"t\": true\n  }\n}"
	want := "{\n  \"a\": {\n    \"k\": null,\n    \"b\": [],\n    \"c\": {},\n    \"t\": true\n  },\n  \"alpha\": {},\n  \"list\": [],\n" +
		"  \"n\": [\n    1.0,\n    12345678901234567890,\n    1e400,\n    -0,\n    2E2\n  ],\n  \"version\": 1,\n  \"zed\": 1\n}\n"
	path := filepath.Join(testkit.New(t).Tmp, "s.json")
	if err := WriteJSONAtomic(path, doc(t, src)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestDoc_WritesTextAsTheCharacters(t *testing.T) {
	d := Doc{}
	for _, kv := range [][2]string{{"html", "<a href=\"x\">&</a>"}, {"accent", "café 中文 😀"}, {"quote", "a\"b\\c\nd\te"}} {
		if err := d.SetString(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	got := compactOf(t, d)
	want := `{"accent":"café 中文 😀","html":"<a href=\"x\">&</a>","quote":"a\"b\\c\nd\te"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if s, ok := doc(t, got).String("accent"); !ok || s != "café 中文 😀" {
		t.Errorf("did not round-trip: %q", s)
	}
}

func doc(t *testing.T, s string) Doc {
	t.Helper()
	d, err := ParseDoc([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func compactOf(t *testing.T, d Doc) string {
	t.Helper()
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
