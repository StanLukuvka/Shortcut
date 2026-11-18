package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// compactOf is d on one line, as the JSON encoder writes it.
func compactOf(t *testing.T, d platform.Doc) string {
	t.Helper()
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEntry_ValidateNameRefusesATrailingNewline(t *testing.T) {
	for _, ok := range []string{"a", "A9", "a_b-c", strings.Repeat("a", 64)} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a\n", "_a", "-a", "a b", "é", strings.Repeat("a", 65)} {
		err := ValidateName(bad)
		if err == nil || !strings.HasPrefix(err.Error(), "invalid shortcut name '"+bad+"'. Must match ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ (alphanumeric start") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestRegistry_LoadRegistryFile(t *testing.T) {
	env := testkit.New(t)
	p := filepath.Join(env.Tmp, "r.json")
	cases := []struct{ file, want string }{
		{`[]`, "corrupt registry " + p + ": root must be a JSON object"},
		{`{}`, p + ` needs "version": 1`},
		{`{"shortcuts": {}}`, p + ` needs "version": 1`},
		{`{"version": 0}`, p + ` needs "version": 1`},
		{`{"version": -1}`, p + ` needs "version": 1`},
		{`{"version": -5}`, p + ` needs "version": 1`},
		{`{"version": "1"}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 1"},
		{`{"version": "2"}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 1"},
		{`{"version": 1.0}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 1"},
		{`{"version": null}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 1"},
		{`{"version": true}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 1"},
		{`{"version": 2}`, p + " is format version 2, newer than this shortcut reads; upgrade shortcut"},
		{`{"version": 3}`, p + " is format version 3, newer than this shortcut reads; upgrade shortcut"},
		{`{"version": 99999999999999999999999}`, p + " is format version 99999999999999999999999, newer than this shortcut reads; upgrade shortcut"},
	}
	for _, c := range cases {
		env.WriteFile(p, c.file, 0)
		_, err := LoadRegistryFile(p)
		if err == nil || err.Error() != c.want {
			t.Errorf("%q: got %v\nwant %s", c.file, err, c.want)
		}
	}
	// Go's wording after the path, and the file is named; text that is not valid UTF-8 says so
	for _, c := range []struct{ file, want string }{
		{`{not json`, "invalid character 'n' looking for beginning of object key string"},
		{"\xef\xbb\xbf{}", "invalid character"},
		{"{\r\n  \"a\": 1,\r\n}", "invalid character '}'"},
		{`{"a": NaN}`, "invalid character 'N'"},
		{"{\"a\": \"\xff\"}", "not valid UTF-8"},
	} {
		env.WriteFile(p, c.file, 0)
		_, err := LoadRegistryFile(p)
		if err == nil || !strings.HasPrefix(err.Error(), "corrupt registry "+p+": ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want it to name %s and say %q", c.file, err, p, c.want)
		}
	}
	env.WriteFile(p, "{\r\n  \"version\": 1,\r\n  \"shortcuts\": {}\r\n}\r\n", 0) // CRLF line ends are plain white space
	if _, err := LoadRegistryFile(p); err != nil {
		t.Errorf("a CRLF registry: %v", err)
	}
	for _, ok := range []string{`{"version": 1}`, `{"version": 1, "x": 1}`} {
		env.WriteFile(p, ok, 0)
		if _, err := LoadRegistryFile(p); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	_, err := LoadRegistryFile(filepath.Join(env.Tmp, "gone.json"))
	if err == nil || err.Error() != "registry not found: "+filepath.Join(env.Tmp, "gone.json") {
		t.Error(err)
	}
}

func TestRegistry_FileError(t *testing.T) {
	cases := []struct{ file, name, want string }{
		{`{"version": 1, "shortcuts": {}}`, "", ""},
		{`{"version": 1, "shortcuts": {}, "imports": {}, "description": "d"}`, "", ""},
		{`{"shortcuts": []}`, "", "corrupt registry P: 'shortcuts' must be an object"},
		{`{"shortcuts": null}`, "", "corrupt registry P: 'shortcuts' must be an object"},
		{`{"description": 1}`, "", "corrupt registry P: 'description' must be a string"},
		{`{"zed": 1, "bar": 1, "shortcuts": {}}`, "", "P has an unknown field: bar, zed"},
		{`{"version": 1, "shortcuts": {}}`, "ns", ""},
		{`{"version": 1, "shortcuts": {}, "imports": {}}`, "ns", "P has its own 'imports', but namespaces don't nest"},
		{`{"version": 1}`, "ns", "P needs 'shortcuts'"},
	}
	for _, c := range cases {
		got := fileError("P", doc(t, c.file), c.name)
		if got != c.want {
			t.Errorf("%s (%q): got %q want %q", c.file, c.name, got, c.want)
		}
	}
}

func TestRegistry_ResolveScriptPath(t *testing.T) {
	env := testkit.New(t)
	base := env.Folder("base")
	env.Folder("outside")
	env.Folder("base/a")
	env.Symlink(filepath.Join(env.Tmp, "outside"), filepath.Join(base, "link"))
	if got, err := ResolveScriptPath(base, "/etc/passwd"); err != nil || got != "/etc/passwd" {
		t.Errorf("absolute: %q %v", got, err)
	}
	if got, err := ResolveScriptPath(base, "a/../b.sh"); err != nil || got != filepath.Join(base, "b.sh") {
		t.Errorf("relative: %q %v", got, err)
	}
	env.Symlink("loop2", filepath.Join(base, "loop1"))
	env.Symlink("loop1", filepath.Join(base, "loop2"))
	if got, err := ResolveScriptPath(base, "loop1/x.sh"); err == nil { // a link loop is an error, not a path kept as written
		t.Errorf("loop: %q", got)
	}
	if got, err := ResolveScriptPath(base, "nodir/x.sh"); err == nil {
		t.Errorf("missing folder: %q", got)
	}
	for _, bad := range []string{"../x", "link/x", "a/../../x"} {
		_, err := ResolveScriptPath(base, bad)
		if err == nil || !strings.HasPrefix(err.Error(), "path traversal detected: '"+bad+"' resolves to '") ||
			!strings.HasSuffix(err.Error(), "', outside '"+base+"'.") {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestStores_AtomicRegistrySaveAndCorruptProtection(t *testing.T) {
	env := testkit.New(t)
	data := doc(t, `{"version": 1, "shortcuts": {"test": {"description": "test", "inline": "echo 1"}}}`)
	path := filepath.Join(env.Store, "shortcuts.json")
	if err := os.MkdirAll(env.Store, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := SaveRegistryFile(path, data); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRegistryFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if compactOf(t, loaded) != compactOf(t, data) {
		t.Errorf("loaded %s", compactOf(t, loaded))
	}

	// Corrupt JSON file protection
	reg := env.WriteFile(filepath.Join(env.Store, "shortcuts.json"), `{"version": 1, corrupt json!`, 0)
	if _, err := LoadRegistryFile(path); err == nil || !strings.Contains(err.Error(), "corrupt registry") {
		t.Errorf("load: %v", err)
	}
	if err := SaveRegistryFile(path, data); err == nil || !strings.Contains(err.Error(), "cannot overwrite corrupt registry") {
		t.Errorf("save: %v", err)
	}
	// Verify the corrupt file was not replaced or overwritten
	if got, _ := os.ReadFile(reg); string(got) != `{"version": 1, corrupt json!` {
		t.Errorf("file is now %q", got)
	}
}

func TestResolveScriptPath_NonUTF8DirectoriesStayApart(t *testing.T) {
	base := testkit.New(t).Tmp
	for _, d := range []string{"d\xe9", "d\xea"} {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Skip("file system refuses non-UTF-8 names")
		}
	}
	a, errA := ResolveScriptPath(base, "d\xe9/run.sh")
	b, errB := ResolveScriptPath(base, "d\xea/run.sh")
	if errA != nil || errB != nil || a == b || !strings.HasSuffix(a, "d\xe9/run.sh") || !strings.HasSuffix(b, "d\xea/run.sh") {
		t.Errorf("%q %v / %q %v", a, errA, b, errB)
	}
}

// A store's shortcuts.json, a project's .shortcuts and .shortcuts.local, and a namespace .shortcut all need
// "version": 1.
func TestStores_VersionRequiredInEveryFile(t *testing.T) {
	env := testkit.New(t)
	need := func(f string) string { return f + ` needs "version": 1` }
	newer := func(f, n string) string {
		return f + " is format version " + n + ", newer than this shortcut reads; upgrade shortcut"
	}
	whole := func(f string) string {
		return "corrupt registry " + f + ": 'version' must be a whole number, such as 1"
	}
	cases := []struct {
		body string
		want func(string) string // "" = loads
	}{
		{`{"shortcuts": {}}`, need},
		{`{"version": 0, "shortcuts": {}}`, need},
		{`{"version": -1, "shortcuts": {}}`, need},
		{`{"version": 1, "shortcuts": {}}`, nil},
		{`{"version": 2, "shortcuts": {}}`, func(f string) string { return newer(f, "2") }},
		{`{"version": 3, "shortcuts": {}}`, func(f string) string { return newer(f, "3") }},
		{`{"version": "1", "shortcuts": {}}`, whole},
		{`{"version": 1.0, "shortcuts": {}}`, whole},
		{`{"version": null, "shortcuts": {}}`, whole},
	}
	for i, c := range cases {
		proj := env.Project(nil, testkit.Name(fmt.Sprintf("p%d", i)))
		shared, local := filepath.Join(proj, ".shortcuts"), filepath.Join(proj, ".shortcuts.local")
		store := env.Store
		ns := filepath.Join(store, "imports", "n.shortcut")
		for _, f := range []string{shared, local, ns, filepath.Join(store, "shortcuts.json")} {
			env.WriteFile(f, c.body, 0)
			_, err := LoadRegistryFile(f)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("%s in %s: %v", c.body, f, err)
			case c.want != nil && (err == nil || err.Error() != c.want(f)):
				t.Errorf("%s in %s: got %v, want %q", c.body, f, err, c.want(f))
			}
		}
		// the project and namespace loaders report the same text
		env.WriteFile(shared, `{"version": 1, "shortcuts": {}}`, 0)
		g := LoadProjectGroup(proj)
		if c.want == nil && g.Error != nil || c.want != nil && (g.Error == nil || *g.Error != c.want(local)) {
			t.Errorf("project %s: %v", c.body, g.Error)
		}
		env.WriteFile(filepath.Join(store, "shortcuts.json"), `{"version": 1, "shortcuts": {}, "imports": {"n": "`+ns+`"}}`, 0)
		g, _ = NewContext(env.Home, nil).Store(store).Namespace("n")
		if c.want == nil && g.Error != nil || c.want != nil && (g.Error == nil || *g.Error != c.want(ns)) {
			t.Errorf("namespace %s: %v", c.body, g.Error)
		}
	}
}
