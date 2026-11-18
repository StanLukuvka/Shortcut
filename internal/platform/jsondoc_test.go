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
		"{\"k\": \"\xe9\"}", // a raw invalid byte
		"{\"\xff\": 1}",     // in a key too
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

func TestWriteJSONAtomic(t *testing.T) {
	v := doc(t, `{"version": 1, "shortcuts": {"hi": {"description": "é", "n": 1.0}}}`)
	t.Run("a symlinked file is written where it leads, with its mode, and leaves nothing behind", func(t *testing.T) {
		env := testkit.New(t)
		dotfiles := env.Folder("dotfiles")
		target := env.WriteFile(filepath.Join(dotfiles, "shortcuts.json"), `{"version": 1, "shortcuts": {}}`, 0o644)
		link := filepath.Join(env.Store, "shortcuts.json")
		env.Symlink(target, link)
		if err := WriteJSONAtomic(link, v); err != nil {
			t.Fatal(err)
		}
		if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
			t.Error("the link was replaced")
		}
		want := "{\n  \"shortcuts\": {\n    \"hi\": {\n      \"description\": \"é\",\n      \"n\": 1.0\n    }\n  },\n  \"version\": 1\n}\n"
		if got, _ := os.ReadFile(target); string(got) != want {
			t.Errorf("got %q", got)
		}
		if st, _ := os.Stat(target); st.Mode().Perm() != 0o644 {
			t.Errorf("mode %v", st.Mode().Perm())
		}
		if entries, _ := os.ReadDir(dotfiles); len(entries) != 1 || entries[0].Name() != "shortcuts.json" {
			t.Errorf("stray files: %v", entries)
		}
	})
	t.Run("a new file is private, like any temporary file", func(t *testing.T) {
		fresh := filepath.Join(testkit.New(t).Tmp, "new.json")
		if err := WriteJSONAtomic(fresh, v); err != nil {
			t.Fatal(err)
		}
		if st, _ := os.Stat(fresh); st.Mode().Perm() != 0o600 {
			t.Errorf("new file mode %v", st.Mode().Perm())
		}
	})
	t.Run("an error leaves no temporary file", func(t *testing.T) {
		dir := testkit.New(t).Folder("d")
		target := filepath.Join(dir, "x.json")
		os.Mkdir(target, 0o755) // a rename of a file over a folder fails
		if err := WriteJSONAtomic(target, Doc{}); err == nil {
			t.Fatal("want an error")
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "x.json" {
			t.Errorf("left behind: %v", entries)
		}
	})
}

func TestDoc_WritesTextAsTheCharacters(t *testing.T) {
	d := Doc{}
	for _, kv := range [][2]string{{"html", "<a href=\"x\">&</a>"}, {"accent", "café 中文 😀"}, {"quote", "a\"b\\c\nd\te"}} {
		d.SetString(kv[0], kv[1])
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
