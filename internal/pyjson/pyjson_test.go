package pyjson

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"os"
	"sort"
	"strings"
	"testing"
)

// golden reads ../testdata/approval.json with this package's own decoder (it holds lone-surrogate escapes).
func golden(t *testing.T, set string) []*Object {
	t.Helper()
	raw, err := os.ReadFile("../testdata/approval.json")
	if err != nil {
		t.Fatal(err)
	}
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

func TestGolden_Loads(t *testing.T) {
	cases := golden(t, "loads_cases")
	t.Logf("%d cases", len(cases))
	for _, c := range cases {
		src := str(c, "source")
		t.Run(src, func(t *testing.T) {
			v, err := Parse(src)
			if want, bad := c.Get("error"); bad {
				if err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := DumpsCompact(v); got != str(c, "compact") {
				t.Fatalf("got %s, want %s", got, str(c, "compact"))
			}
		})
	}
}

func TestGolden_LoadsBytes(t *testing.T) {
	cases := golden(t, "loads_bytes_cases")
	t.Logf("%d cases", len(cases))
	for _, c := range cases {
		raw, err := hex.DecodeString(str(c, "raw_hex"))
		if err != nil {
			t.Fatal(err)
		}
		v, err := Decode(raw)
		if want, bad := c.Get("error"); bad {
			var name string
			var se *SyntaxError
			var ue *UTF8Error
			switch {
			case errors.As(err, &se):
				name = "JSONDecodeError: "
			case errors.As(err, &ue):
				name = "UnicodeDecodeError: "
			}
			if err == nil || name+err.Error() != want {
				t.Errorf("%x: error = %v, want %q", raw, err, want)
			}
			continue
		}
		if err != nil || DumpsCompact(v) != str(c, "compact") {
			t.Errorf("%x: got %v, %v", raw, v, err)
		}
	}
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
		_, isInt := v.(*big.Int)
		if isInt != (str(c, "type") == "int") {
			t.Errorf("%s: decoded as %T, Python says %s", lit, v, str(c, "type"))
		}
		if got := DumpsCompact(v); got != str(c, "dumps") {
			t.Errorf("%s: got %s, want %s", lit, got, str(c, "dumps"))
		}
	}
}

func TestGolden_Dumps(t *testing.T) {
	cases := golden(t, "dumps_cases")
	t.Logf("%d cases", len(cases))
	for _, c := range cases {
		v, err := Parse(str(c, "source"))
		if err != nil {
			t.Fatalf("%s: %v", str(c, "source"), err)
		}
		check := func(name string, o Opts, want string) {
			if got := Dumps(v, o); got != want {
				t.Errorf("%s %s: got %q, want %q", str(c, "source"), name, got, want)
			}
		}
		check("compact", Opts{EnsureASCII: true}, str(c, "compact"))
		check("indent2", Opts{Indent: 2, EnsureASCII: true}, strings.TrimSuffix(str(c, "indent2"), "\n"))
		if _, ok := c.Get("indent2_sorted"); ok {
			check("indent2_sorted", Opts{Indent: 2, SortKeys: true, EnsureASCII: true}, strings.TrimSuffix(str(c, "indent2_sorted"), "\n"))
		}
	}
}

// filesJSON is the approval hash input of 4.1: json.dumps(sorted(files.items())), built with Less and WriteString.
func filesJSON(files *Object) string {
	keys := append([]string(nil), files.Keys...)
	sort.Slice(keys, func(i, j int) bool { return Less(keys[i], keys[j]) })
	var b strings.Builder
	b.WriteByte('[')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('[')
		WriteString(&b, k, true)
		b.WriteString(", ")
		WriteString(&b, files.Vals[k].(string), true)
		b.WriteByte(']')
	}
	b.WriteByte(']')
	return b.String()
}

func TestGolden_HashInput(t *testing.T) {
	n := 0
	for _, set := range []string{"hash_cases", "literal_abs_cases"} {
		for _, c := range golden(t, set) {
			n++
			files, _ := c.Get("files")
			text := filesJSON(files.(*Object))
			if text != str(c, "json") {
				t.Errorf("%s: json\n got %q\nwant %q", str(c, "name"), text, str(c, "json"))
			}
			sum := sha256.Sum256([]byte(text))
			if got := hex.EncodeToString(sum[:]); got != str(c, "hash") {
				t.Errorf("%s: hash %s, want %s", str(c, "name"), got, str(c, "hash"))
			}
		}
	}
	t.Logf("%d cases", n)
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
		key := DumpsCompact([]Value{str(c, "store"), str(c, "name"), str(c, "file")})
		if key != str(c, "key") {
			t.Errorf("got %s, want %s", key, str(c, "key"))
		}
	}
	t.Logf("%d of %d cases (namespace keys)", n, len(cases))
}

func TestSyntaxErrors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"", "Expecting value: line 1 column 1 (char 0)"},
		{"{", "Expecting property name enclosed in double quotes: line 1 column 2 (char 1)"},
		{`{"a" 1}`, "Expecting ':' delimiter: line 1 column 6 (char 5)"},
		{`[1 2]`, "Expecting ',' delimiter: line 1 column 4 (char 3)"},
		{`{"a": 1 "b"}`, "Expecting ',' delimiter: line 1 column 9 (char 8)"},
		{`"abc`, "Unterminated string starting at: line 1 column 1 (char 0)"},
		{`"abc\`, "Unterminated string starting at: line 1 column 1 (char 0)"},
		{"\"a\x01\"", "Invalid control character at: line 1 column 3 (char 2)"},
		{`"\q"`, "Invalid \\escape: line 1 column 2 (char 1)"},
		{`"\u00"`, "Invalid \\uXXXX escape: line 1 column 3 (char 2)"},
		{`"\u00zz"`, "Invalid \\uXXXX escape: line 1 column 3 (char 2)"},
		{`"\ud800\u00zz"`, "Invalid \\uXXXX escape: line 1 column 9 (char 8)"},
		{"{} []", "Extra data: line 1 column 4 (char 3)"},
		{`{"a": 1,}`, "Illegal trailing comma before end of object: line 1 column 8 (char 7)"},
		{`[1, 2 ,  ]`, "Illegal trailing comma before end of array: line 1 column 7 (char 6)"},
		{"\xef\xbb\xbf{}", "Unexpected UTF-8 BOM (decode using utf-8-sig): line 1 column 1 (char 0)"},
		// positions count code points, not bytes: the e-acute and the emoji are one char each
		{"[\"é\U0001F600\", x]", "Expecting value: line 1 column 8 (char 7)"},
		{"{\n\"é\": 1\n  x}", "Expecting ',' delimiter: line 3 column 3 (char 11)"},
		{" {}", "Expecting value: line 1 column 1 (char 0)"}, // only space, tab, newline, CR are whitespace
	} {
		if _, err := Parse(c.src); err == nil || err.Error() != c.want {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
}

func TestUTF8Errors(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want string
	}{
		{"{\"a\": \"\xff\"}", "'utf-8' codec can't decode byte 0xff in position 7: invalid start byte"},
		{"a\xe2\x82", "'utf-8' codec can't decode bytes in position 1-2: unexpected end of data"},
		{"a\xe2", "'utf-8' codec can't decode byte 0xe2 in position 1: unexpected end of data"},
		{"\xe2\x82A", "'utf-8' codec can't decode bytes in position 0-1: invalid continuation byte"},
		{"\xed\xa0\x80", "'utf-8' codec can't decode byte 0xed in position 0: invalid continuation byte"},
		{"\xc0\x80", "'utf-8' codec can't decode byte 0xc0 in position 0: invalid start byte"},
	} {
		if _, err := Decode([]byte(c.raw)); err == nil || err.Error() != c.want {
			t.Errorf("%q: got %v, want %q", c.raw, err, c.want)
		}
	}
}

func TestDepthLimit(t *testing.T) {
	if _, err := Parse(strings.Repeat("[", 999) + strings.Repeat("]", 999)); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(strings.Repeat("[", 5000)); !errors.Is(err, ErrDepth) {
		t.Fatalf("got %v", err)
	}
	if _, err := Parse(strings.Repeat(`{"a":`, 5000)); !errors.Is(err, ErrDepth) {
		t.Fatalf("got %v", err)
	}
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

func TestLoneSurrogates(t *testing.T) {
	v, err := Parse(`"\ud800x\udc00\ud83d\ude00"`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\xed\xa0\x80x\xed\xb0\x80\U0001F600"; v != want {
		t.Fatalf("got %q", v)
	}
	if got := DumpsCompact(v); got != `"\ud800x\udc00\ud83d\ude00"` {
		t.Fatal(got)
	}
	// a raw invalid byte is surrogateescape: 0xE9 is U+DCE9
	if got := DumpsCompact("caf\xe9"); got != `"caf\udce9"` {
		t.Fatal(got)
	}
}

func TestWriteStringWithoutASCII(t *testing.T) {
	var b strings.Builder
	WriteString(&b, "aé\x7f\x01\t\"\\\U0001F600", false)
	if want := "\"aé\x7f\\u0001\\t\\\"\\\\\U0001F600\""; b.String() != want {
		t.Fatalf("got %q", b.String())
	}
}

func TestLess(t *testing.T) {
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
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%q, %q) = %v", c.a, c.b, got)
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

func TestSurrogateEscapeRoundTrip(t *testing.T) {
	v, err := Parse(`"caf\udce9.sh"`)
	if err != nil || v != "caf\xe9.sh" {
		t.Fatalf("got %q, %v", v, err)
	}
	if got := DumpsCompact(v); got != `"caf\udce9.sh"` {
		t.Fatal(got)
	}
	v, _ = Parse(`"\ud800"`)
	if v != "\xed\xa0\x80" || DumpsCompact(v) != `"\ud800"` {
		t.Fatalf("got %q", v)
	}
	d, _ := Parse(`"\udcff.sh"`)
	if Less(d.(string), "\xff.sh") || Less("\xff.sh", d.(string)) {
		t.Fatal("decoded and raw differ")
	}
}
