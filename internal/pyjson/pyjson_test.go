package pyjson

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// golden reads ../testdata/approval.json.
func golden(t *testing.T, set string) []*Object {
	t.Helper()
	raw := testkit.Golden(t, "approval.json")
	v, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	cases, ok := v.(*Object).Get(set)
	if !ok {
		t.Fatalf("approval.json has no %s", set)
	}
	var out []*Object
	for _, c := range cases.([]Value) {
		out = append(out, c.(*Object))
	}
	return out
}

func str(o *Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

// text is the field k, or the bytes of k+"_hex" when the name is not valid UTF-8 and so is given as hex.
func text(t *testing.T, o *Object, k string) string {
	t.Helper()
	if h, ok := o.Get(k + "_hex"); ok {
		b, err := hex.DecodeString(h.(string))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return str(o, k)
}

func TestGolden_Floats(t *testing.T) {
	cases := golden(t, "float_cases")
	t.Logf("%d cases", len(cases))
	for _, c := range cases {
		lit := str(c, "literal")
		v, err := Parse(lit)
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		n, ok := v.(json.Number)
		if !ok || string(n) != lit { // a number keeps the text it was written with
			t.Errorf("%s: decoded as %T %v", lit, v, v)
		}
		if got := DumpsCompact(v); got != lit {
			t.Errorf("%s: written back as %s", lit, got)
		}
	}
}

func TestGolden_KeyJSON(t *testing.T) {
	cases := golden(t, "key_cases")
	n := 0
	for _, c := range cases {
		if str(c, "kind") != "namespace" {
			continue
		}
		if _, bad := c.Get("python_error"); bad {
			continue
		}
		n++
		key := DumpsCompact([]Value{text(t, c, "store"), text(t, c, "name"), text(t, c, "file")})
		if key != str(c, "key") {
			t.Errorf("got %s, want %s", key, str(c, "key"))
		}
	}
	t.Logf("%d of %d cases (namespace keys)", n, len(cases))
}

func TestObjectOrder(t *testing.T) {
	v, err := Parse(`{"b": 1, "a": 2, "b": 3}`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*Object)
	if strings.Join(o.Keys, ",") != "b,a" || DumpsCompact(o) != `{"b": 3, "a": 2}` {
		t.Fatalf("keys %v, %s", o.Keys, DumpsCompact(o))
	}
	o.Set("c", nil)
	o.Delete("b")
	o.Delete("zz")
	if DumpsCompact(o) != `{"a": 2, "c": null}` {
		t.Fatal(DumpsCompact(o))
	}
	if _, ok := o.Get("b"); ok {
		t.Fatal("b not deleted")
	}
}

func TestCompareOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"\xff", "\U00010000", true}, // raw 0xFF is U+DCFF, below U+10000 (bytes would say above)
		{"￿", "\U00010000", true},
		{"a", "ab", true},
		{"ab", "a", false},
		{"", "a", true},
		{"a", "a", false},
		{"B", "a", true},
	} {
		if got := Compare(c.a, c.b) < 0; got != c.want {
			t.Errorf("Compare(%q, %q) < 0 = %v", c.a, c.b, got)
		}
	}
}

func TestReprFloat(t *testing.T) {
	for _, c := range []struct {
		f    float64
		want string
	}{{1e22, "1e+22"}, {1e16, "1e+16"}, {1e15, "1000000000000000.0"}, {0.0001, "0.0001"}, {0.00001, "1e-05"}, {5e-324, "5e-324"}, {100, "100.0"}} {
		if got := ReprFloat(c.f); got != c.want {
			t.Errorf("%v: got %s, want %s", c.f, got, c.want)
		}
	}
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
