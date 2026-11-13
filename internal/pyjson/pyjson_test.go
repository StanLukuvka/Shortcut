package pyjson

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestObjectOrder(t *testing.T) {
	v, err := Parse(`{"b": 1, "a": 2, "b": 3}`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*Object)
	if got := marshal(t, o); strings.Join(o.Keys, ",") != "b,a" || got != `{"b":3,"a":2}` {
		t.Fatalf("keys %v, %s", o.Keys, got)
	}
	o.Set("c", nil)
	o.Delete("b")
	o.Delete("zz")
	if got := marshal(t, o); got != `{"a":2,"c":null}` {
		t.Fatal(got)
	}
	if _, ok := o.Get("b"); ok {
		t.Fatal("b not deleted")
	}
}

func marshal(t *testing.T, v Value) string {
	t.Helper()
	b, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDecode_RefusesWhatIsNotValidUTF8(t *testing.T) {
	for _, src := range []string{
		`{"a": "\udce9"}`,   // what the Python version wrote for a file name caf<0xe9>
		`["\ud800"]`,        // lone high surrogate
		`["\ud800\u0041"]`,  // high surrogate followed by something that is not a low one
		`["\ud800 \udc00"]`, // a pair needs the escapes side by side
		`["\udc00"]`,        // lone low surrogate
		`["\udc00\ud800"]`,  // low before high
		`{"\udcff": 1}`,     // in a key too
		"{\"a\": \"\xe9\"}", // a raw invalid byte
	} {
		_, err := Decode([]byte(src))
		if err == nil {
			t.Errorf("%q loaded", src)
		}
		if !errors.Is(err, ErrNotUTF8) {
			t.Errorf("%q: %v, want ErrNotUTF8", src, err)
		}
	}
	if _, err := Decode([]byte{'"', 0xe9, '"'}); !errors.Is(err, ErrNotUTF8) {
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
		v, err := Parse(src)
		if err != nil || v != want {
			t.Errorf("%s: got %q, %v, want %q", src, v, err, want)
		}
	}
}

func TestDecode_PythonWrittenStoreKeepsOrderAndNumberText(t *testing.T) {
	src := `{"zed": 1, "version": 1, "n": [1.0, 12345678901234567890, 1e400, 0, -0, 2E2], "alpha": {}, "list": [], "a": {"k": 1, "b": [], "c": {}}}`
	v, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*Object)
	if got := strings.Join(o.Keys, ","); got != "zed,version,n,alpha,list,a" {
		t.Errorf("key order %s", got)
	}
	for i, want := range []string{"1.0", "12345678901234567890", "1e400", "0", "-0", "2E2"} {
		if got := o.Vals["n"].([]Value)[i]; got != json.Number(want) {
			t.Errorf("number %d: %#v, want %s", i, got, want)
		}
	}
	if o.Vals["alpha"].(*Object).Vals == nil || o.Vals["list"].([]Value) == nil {
		t.Error("{} and [] must not be nil")
	}
}

func TestDecode_DuplicatesDepthAndWhatIsNotJSON(t *testing.T) {
	v, err := Parse(`{"b": 1, "a": 2, "b": 3}`)
	if err != nil {
		t.Fatal(err)
	}
	if o := v.(*Object); strings.Join(o.Keys, ",") != "b,a" || o.Vals["b"] != json.Number("3") { // first place, last value
		t.Errorf("%v", o)
	}
	if v, _ := Parse(`{}`); v.(*Object).Vals == nil {
		t.Error("{} has nil Vals")
	}
	for _, src := range []string{
		strings.Repeat("[", 20000), strings.Repeat("[", 20000) + strings.Repeat("]", 20000), // deeper than encoding/json reads
		"\xef\xbb\xbf{}", `{"a": NaN}`, `[Infinity]`, `[-Infinity]`, `{} x`, `{"a": 1,}`, `[1 2]`, ``, `{`, `"a`, `01`, "\"a\x01\"",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%.30q loaded", src)
		}
	}
}

func TestEncode_RefusesInvalidUTF8(t *testing.T) {
	bad := "caf\xe9"
	obj := func(k string, v Value) *Object {
		o := &Object{}
		o.Set(k, v)
		return o
	}
	for name, v := range map[string]Value{
		"string":         bad,
		"key":            obj(bad, "x"),
		"value":          obj("k", bad),
		"in an array":    []Value{"ok", bad},
		"in an object":   obj("a", obj("b", []Value{obj("c", bad)})),
		"two bad bytes":  "\xff\xfe",
		"half a rune":    "\xe2\x82",
		"a WTF-8 triple": "\xed\xa0\x80", // an encoded surrogate is not UTF-8 either
	} {
		for _, f := range []func(Value) ([]byte, error){Marshal, MarshalIndent} {
			if out, err := f(v); !errors.Is(err, ErrNotUTF8) || out != nil {
				t.Errorf("%s: got %q, %v", name, out, err)
			}
		}
	}
	if _, err := json.Marshal(obj("k", bad)); !errors.Is(err, ErrNotUTF8) { // straight through encoding/json too
		t.Errorf("json.Marshal: %v", err)
	}
}

func TestEncode_RoundTripsAPythonWrittenStore(t *testing.T) {
	// indent 2, key order as written (not sorted), numbers as written, empty containers on one line
	src := "{\n  \"zed\": 1,\n  \"version\": 1,\n  \"n\": [\n    1.0,\n    12345678901234567890,\n    1e400,\n    -0,\n    2E2\n  ],\n" +
		"  \"alpha\": {},\n  \"list\": [],\n  \"a\": {\n    \"k\": null,\n    \"b\": [],\n    \"c\": {},\n    \"t\": true\n  }\n}"
	v, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalIndent(v)
	if err != nil || string(got) != src {
		t.Errorf("got %s, %v\nwant %s", got, err, src)
	}
}

func TestEncode_WritesTextAsTheCharacters(t *testing.T) {
	o := &Object{}
	o.Set("html", "<a href=\"x\">&</a>")
	o.Set("accent", "café 中文 😀")
	o.Set("quote", "a\"b\\c\nd\te")
	got := marshal(t, o)
	want := `{"html":"<a href=\"x\">&</a>","accent":"café 中文 😀","quote":"a\"b\\c\nd\te"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	back, err := Parse(got)
	if err != nil || back.(*Object).Vals["accent"] != "café 中文 😀" {
		t.Errorf("did not round-trip: %v %v", back, err)
	}
}

func TestSorted(t *testing.T) {
	v, _ := Parse(`{"b": {"z": 1, "a": [{"y": 1, "x": 2}]}, "a": 1}`)
	got := marshal(t, v.(*Object).Sorted())
	if want := `{"a":1,"b":{"a":[{"x":2,"y":1}],"z":1}}`; got != want {
		t.Errorf("got %s", got)
	}
	if first := marshal(t, v); first != `{"b":{"z":1,"a":[{"y":1,"x":2}]},"a":1}` {
		t.Errorf("Sorted changed the original: %s", first)
	}
}

func TestEncode_RefusesInvalidUTF8InAMap(t *testing.T) {
	for name, v := range map[string]Value{
		"value":  map[string]any{"k": "caf\xe9"},
		"key":    map[string]any{"caf\xe9": "x"},
		"nested": []Value{map[string]any{"a": map[string]any{"b": []any{"\xff"}}}},
	} {
		if out, err := Marshal(v); !errors.Is(err, ErrNotUTF8) || out != nil {
			t.Errorf("%s: %q, %v", name, out, err)
		}
	}
	if got := marshal(t, map[string]any{"k": "café"}); got != `{"k":"café"}` {
		t.Errorf("%s", got)
	}
}
