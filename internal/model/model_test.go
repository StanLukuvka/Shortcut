package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func parse(t *testing.T, s string) ordjson.Value {
	t.Helper()
	v, err := ordjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func obj(t *testing.T, s string) *ordjson.Object {
	t.Helper()
	return parse(t, s).(*ordjson.Object)
}

func TestEntry_FromValueMessages(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[]`, "'x' is not a valid entry: expected a dictionary"},
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
		_, err := EntryFromValue("x", parse(t, c.in))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: got %v, want %q", c.in, err, c.want)
		}
	}
}

// compact is v on one line, as the JSON encoder writes it.
func compact(t *testing.T, v ordjson.Value) string {
	t.Helper()
	b, err := ordjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEntry_RoundTripKeepsOrder(t *testing.T) {
	src := `{"description": "d", "script": "s.sh", "cwd": "c", "confirm": true}`
	e, err := EntryFromValue("x", parse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := compact(t, e.ToObject()), compact(t, parse(t, src)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if e.Inline != nil || *e.Script != "s.sh" || !e.Confirm {
		t.Errorf("fields: %+v", e)
	}
	// An empty inline is still set.
	e, err = EntryFromValue("y", parse(t, `{"inline": ""}`))
	if err != nil || e.Inline == nil {
		t.Fatalf("%+v %v", e, err)
	}
	if got := compact(t, e.ToObject()); got != `{"description":"","inline":""}` {
		t.Error(got)
	}
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
		{`{not json`, "invalid character 'n' looking for beginning of object key string (at byte "},
		{"\xef\xbb\xbf{}", "invalid character"},
		{"{\r\n  \"a\": 1,\r\n}", "invalid character '}'"},
		{`{"a": NaN}`, "invalid character 'N'"},
		{"{\"a\": \"\xff\"}", "not valid UTF-8"},
		{`{"a": "\udce9"}`, "not valid UTF-8"},
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
		got := fileError("P", obj(t, c.file), c.name)
		if got != c.want {
			t.Errorf("%s (%q): got %q want %q", c.file, c.name, got, c.want)
		}
	}
}

func TestRegistry_ResolveScriptPath(t *testing.T) {
	env := testkit.New(t)
	base := env.Folder("base")
	env.Folder("outside")
	env.Symlink(filepath.Join(env.Tmp, "outside"), filepath.Join(base, "link"))
	if got, err := ResolveScriptPath(base, "/etc/passwd"); err != nil || got != "/etc/passwd" {
		t.Errorf("absolute: %q %v", got, err)
	}
	if got, err := ResolveScriptPath(base, "a/../b.sh"); err != nil || got != filepath.Join(base, "b.sh") {
		t.Errorf("relative: %q %v", got, err)
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
	data := obj(t, `{"version": 1, "shortcuts": {"test": {"description": "test", "inline": "echo 1"}}}`)
	if err := SaveRegistryAtomic(env.Store, data); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRegistry(env.Store)
	if err != nil {
		t.Fatal(err)
	}
	if compact(t, loaded) != compact(t, data) {
		t.Errorf("loaded %s", compact(t, loaded))
	}

	// Corrupt JSON file protection
	reg := env.WriteFile(filepath.Join(env.Store, "shortcuts.json"), `{"version": 1, corrupt json!`, 0)
	if _, err := LoadRegistry(env.Store); err == nil || !strings.Contains(err.Error(), "corrupt registry") {
		t.Errorf("load: %v", err)
	}
	if err := SaveRegistryAtomic(env.Store, data); err == nil || !strings.Contains(err.Error(), "cannot overwrite corrupt registry") {
		t.Errorf("save: %v", err)
	}
	// Verify the corrupt file was not replaced or overwritten
	if got, _ := os.ReadFile(reg); string(got) != `{"version": 1, corrupt json!` {
		t.Errorf("file is now %q", got)
	}
}

func TestStores_StoreDiscoverySkipsGlobalRoot(t *testing.T) {
	env := testkit.New(t)
	if err := SaveRegistryAtomic(env.Store, obj(t, `{"version": 1, "shortcuts": {}}`)); err != nil {
		t.Fatal(err)
	}
	// Walking up from home or subdirectory must skip ~/.shortcuts
	if got := FindProjectRoot(env.Home); got != "" {
		t.Errorf("home: %q", got)
	}
	deep := filepath.Join(env.Home, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(deep); got != "" {
		t.Errorf("deep: %q", got)
	}
	// Walking up from inside ~/.shortcuts itself must return none
	if got := FindProjectRoot(env.Store); got != "" {
		t.Errorf("store: %q", got)
	}
	// A real local store in a project must be found
	proj := filepath.Join(env.Home, "my_project")
	env.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{})
	sub := filepath.Join(proj, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(sub); got != osutil.Realpath(proj) {
		t.Errorf("sub: %q", got)
	}
}

func TestStores_ProjectRootViaLocalFileOnlyAndNearest(t *testing.T) {
	env := testkit.New(t)
	outer := env.Project(nil, testkit.Name("outer"))
	env.WriteFile(filepath.Join(outer, ".shortcuts.local"), `{"version": 1, "shortcuts": {}}`, 0)
	inner := filepath.Join(outer, "in")
	env.WriteStore(filepath.Join(inner, ".shortcuts"), map[string]any{})
	if got := FindProjectRoot(filepath.Join(inner, "x")); got != inner {
		t.Errorf("nearest: %q", got)
	}
	if got := FindProjectRoot(outer); got != outer {
		t.Errorf("local only: %q", got)
	}
	// a .shortcuts that is a folder is no project
	old := env.Project(nil, testkit.Name("old"), testkit.Dirs(".shortcuts"))
	if got := FindProjectRoot(old); got != "" {
		t.Errorf("a .shortcuts folder: %q", got)
	}
}

func TestStores_LocalProjectRoot(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{}, testkit.Name("p"))
	sub := filepath.Join(proj, "sub")
	os.MkdirAll(sub, 0o755)
	if got, err := LocalProjectRoot(proj); err != nil || got != proj {
		t.Errorf("%q %v", got, err)
	}
	if _, err := LocalProjectRoot(sub); err == nil || err.Error() != sub+" is inside the project at "+proj+". Run this from there." {
		t.Error(err)
	}
	if _, err := LocalProjectRoot(env.Home); err == nil || err.Error() != env.Home+" holds the global store, so it can't hold local shortcuts. Run this inside a project folder." {
		t.Error(err)
	}
	if _, err := LocalProjectRoot(env.Store); err == nil || !strings.Contains(err.Error(), "is inside the global store") {
		t.Error(err)
	}
	victim := env.WriteFile(filepath.Join(env.Tmp, "victim"), "keep\n", 0)
	fresh := env.Folder("fresh")
	env.Symlink(victim, filepath.Join(fresh, ".shortcuts.local"))
	// a symlinked .shortcuts.local makes the folder a project; it is its own root, but the link is refused
	if _, err := LocalProjectRoot(fresh); err == nil || err.Error() != filepath.Join(fresh, ".shortcuts.local")+" is a symlink, so writing it would go somewhere else." {
		t.Error(err)
	}
}

func TestStores_WriteJSONAtomic(t *testing.T) {
	env := testkit.New(t)
	dotfiles := env.Folder("dotfiles")
	target := env.WriteFile(filepath.Join(dotfiles, "shortcuts.json"), `{"version": 1, "shortcuts": {}}`, 0o644)
	link := filepath.Join(env.Store, "shortcuts.json")
	env.Symlink(target, link)
	v := obj(t, `{"version": 1, "shortcuts": {"hi": {"description": "é", "n": 1.0}}}`)
	if err := WriteJSONAtomic(link, v); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}
	got, _ := os.ReadFile(target)
	want := "{\n  \"version\": 1,\n  \"shortcuts\": {\n    \"hi\": {\n      \"description\": \"é\",\n      \"n\": 1.0\n    }\n  }\n}\n"
	if string(got) != want {
		t.Errorf("got %q", got)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dotfiles)
	if len(entries) != 1 || entries[0].Name() != "shortcuts.json" {
		t.Errorf("stray files: %v", entries)
	}
	// a new file is private, like any temporary file
	fresh := filepath.Join(env.Tmp, "new.json")
	if err := WriteJSONAtomic(fresh, v); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(fresh); st.Mode().Perm() != 0o600 {
		t.Errorf("new file mode %v", st.Mode().Perm())
	}
}

func TestStores_WriteJSONAtomicLeavesNoTempFileOnError(t *testing.T) {
	env := testkit.New(t)
	dir := env.Folder("d")
	target := filepath.Join(dir, "x.json")
	os.Mkdir(target, 0o755) // a rename of a file over a folder fails
	if err := WriteJSONAtomic(target, &ordjson.Object{}); err == nil {
		t.Fatal("want an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "x.json" {
		t.Errorf("left behind: %v", entries)
	}
}

func TestStores_LockPathsAndSymlinkedLock(t *testing.T) {
	env := testkit.New(t)
	if got := lockPath(env.Store); got != filepath.Join(env.Store, ".lock") {
		t.Error(got)
	}
	proj := env.Project(map[string]any{})
	// sha256("<realpath>"), hex
	got := lockPath(proj)
	if !strings.HasPrefix(got, filepath.Join(env.Store, "locks")+"/") || !strings.HasSuffix(got, ".lock") || len(filepath.Base(got)) != 64+len(".lock") {
		t.Error(got)
	}
	ran := false
	if err := WithRegistryLock(proj, func() error { ran = true; return nil }); err != nil || !ran {
		t.Errorf("%v %v", ran, err)
	}
	// a symlinked .lock is refused and its victim untouched
	victim := env.WriteFile(filepath.Join(env.Tmp, "victim"), "keep\n", 0)
	env.Symlink(victim, filepath.Join(env.Store, ".lock"))
	ran = false
	if err := WithRegistryLock(env.Store, func() error { ran = true; return nil }); err == nil || ran {
		t.Errorf("ran=%v err=%v", ran, err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
		t.Error("victim changed")
	}
	if err := InitStore(env.Store); err == nil {
		t.Error("InitStore through a symlinked .lock must fail")
	}
}

func TestStores_InitStoreCreatesOnlyAMissingRegistry(t *testing.T) {
	env := testkit.New(t)
	if err := InitStore(env.Store); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(env.Store, "shortcuts.json"))
	if string(b) != "{\n  \"version\": 1,\n  \"shortcuts\": {}\n}\n" {
		t.Errorf("got %q", b)
	}
	env.WriteStore(env.Store, map[string]any{"keep": map[string]any{"inline": "x"}})
	if err := InitStore(env.Store); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(filepath.Join(env.Store, "shortcuts.json")); !strings.Contains(string(b2), "keep") {
		t.Error("an existing registry was replaced")
	}
}

func TestGroup_ShortcutsOverlayAndSource(t *testing.T) {
	g := Group{
		File: "/p/.shortcuts", StoreDir: "/p", LocalFile: "/p/.shortcuts.local",
		Data:      obj(t, `{"shortcuts": {"a": {"inline": "1"}, "b": {"inline": "2"}, "c": {"inline": "3"}}}`),
		LocalData: obj(t, `{"shortcuts": {"b": {"inline": "B"}, "d": {"inline": "4"}}}`),
	}
	if got := strings.Join(g.Shortcuts().Keys, ","); got != "a,b,c,d" {
		t.Errorf("keys %s", got)
	}
	if v, _ := g.Shortcuts().Get("b"); compact(t, v) != `{"inline":"B"}` {
		t.Error("b not overridden")
	}
	for name, want := range map[string]string{"a": "shared", "b": "overridden", "c": "shared", "d": "local"} {
		if got := g.Source(name); got != want {
			t.Errorf("Source(%s) = %s, want %s", name, got, want)
		}
	}
	if g.DefiningFile("b") != "/p/.shortcuts.local" || g.DefiningFile("a") != "/p/.shortcuts" {
		t.Error("DefiningFile")
	}
	if len(newGroup().Shortcuts().Keys) != 0 { // no LocalData (a namespace) and an empty file read as empty
		t.Error("empty group")
	}
	// a non-object "shortcuts" is empty, not a crash
	if n := len((Group{Data: obj(t, `{"shortcuts": []}`)}).Shortcuts().Keys); n != 0 {
		t.Error(n)
	}
}

func TestGroup_Names(t *testing.T) {
	env := testkit.New(t)
	ns := Group{Name: "docker", File: filepath.Join(env.Store, "imports", "docker.shortcut"), StoreDir: env.Store}
	if ns.Display("ps") != "docker ps" || ns.Label() != "docker" || ns.UpdateCmd() != "shortcut update docker" ||
		ns.ScriptBase() != filepath.Join(env.Store, "imports") || ns.ScriptsDir() != filepath.Join(env.Store, "imports", "scripts", "docker") ||
		!ns.NeedsApproval() {
		t.Errorf("%+v", ns)
	}
	global := Group{File: filepath.Join(env.Store, "shortcuts.json"), StoreDir: env.Store}
	if global.NeedsApproval() || global.IsProject() || global.Display("x") != "x" || global.ScriptBase() != env.Store ||
		global.ScriptsDir() != filepath.Join(env.Store, "scripts") || global.UpdateCmd() != "shortcut update" {
		t.Errorf("%+v", global)
	}
	proj := Group{File: filepath.Join(env.Tmp, "p", ".shortcuts"), StoreDir: filepath.Join(env.Tmp, "p")}
	if !proj.IsProject() || !proj.NeedsApproval() || proj.Label() != proj.Key() || proj.Key() != proj.StoreDir {
		t.Errorf("%+v", proj)
	}
}

func TestStores_LoadStoreProjectAndNamespaces(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"p": map[string]any{"inline": "echo p"}, "zed": map[string]any{"inline": "x"}})
	env.WriteImport(filepath.Join(proj, ".shortcuts"), "zed", testkit.DockerPS) // a namespace that is also a shortcut
	env.WriteImport(filepath.Join(proj, ".shortcuts"), "b", testkit.DockerPS)
	env.WriteImport(filepath.Join(proj, ".shortcuts"), "a", testkit.KubeGet)
	store := LoadStore(proj)
	var names []string
	for _, n := range store.Namespaces {
		names = append(names, n.Name)
		if n.Group.Error != nil {
			t.Errorf("%s: %s", n.Name, *n.Group.Error)
		}
		if n.Group.StoreDir != proj || n.Group.Name != n.Name {
			t.Errorf("%+v", n.Group)
		}
	}
	if strings.Join(names, ",") != "a,b,zed" {
		t.Errorf("namespaces %v", names)
	}
	if want := "'zed' is both a shortcut and a namespace in " + proj + "; rename one"; len(store.Problems) != 1 || store.Problems[0] != want {
		t.Errorf("problems %v", store.Problems)
	}
	if g, ok := store.Namespace("a"); !ok || g.File != filepath.Join(proj, "imports", "a.shortcut") {
		t.Errorf("%+v", g)
	}
	if len(store.Groups()) != 4 {
		t.Error("Groups")
	}
	if store.Main.Error != nil || store.Main.LocalFile != filepath.Join(proj, ".shortcuts.local") {
		t.Errorf("%+v", store.Main)
	}
}

func TestStores_LoadStoreImportProblems(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(nil)
	file := filepath.Join(proj, ".shortcuts")
	env.WriteRegistryRaw(file, `{"version": 1, "shortcuts": {}, "imports": {"num": 5, "plain": "x.json", "bare": "dir/.shortcut", "ok": "~/x.shortcut", "Bad Name": "w.shortcut"}}`)
	store := LoadStore(proj)
	want := []string{
		"import 'num' in " + file + " must be a path",
		"import 'plain' in " + file + " must be a .shortcut file",
		"import 'bare' in " + file + ": a namespace file needs a name, e.g. docker.shortcut",
	}
	if strings.Join(store.Problems, "|") != strings.Join(want, "|") {
		t.Errorf("problems\n%q\nwant\n%q", store.Problems, want)
	}
	byName := map[string]Group{}
	for _, n := range store.Namespaces {
		byName[n.Name] = n.Group
	}
	if g := byName["ok"]; g.File != filepath.Join(env.Home, "x.shortcut") || g.Error == nil || *g.Error != "registry not found: "+g.File {
		t.Errorf("ok: %+v", g)
	}
	if g := byName["Bad Name"]; g.Error == nil || !strings.HasPrefix(*g.Error, "invalid shortcut name 'Bad Name'") {
		t.Errorf("Bad Name: %+v", g)
	}
}

func TestStores_GlobalStoreImportsFolderAndTwice(t *testing.T) {
	env := testkit.New(t)
	env.WriteStore(env.Store, map[string]any{}, map[string]any{"imports": map[string]any{"docker": "elsewhere/docker.shortcut"}})
	env.WriteImport(env.Store, "docker", testkit.DockerPS)
	env.WriteFile(filepath.Join(env.Store, "imports", ".shortcut"), "{}", 0) // the bare one
	env.WriteFile(filepath.Join(env.Store, "imports", "notes.txt"), "x", 0)
	store := LoadStore(env.Store)
	if want := "namespace 'docker' is imported twice in " + env.Store; len(store.Problems) != 1 || store.Problems[0] != want {
		t.Errorf("problems %v", store.Problems)
	}
	if len(store.Namespaces) != 2 || store.Namespaces[0].Name != "" || store.Namespaces[1].Name != "docker" {
		t.Fatalf("namespaces %+v", store.Namespaces)
	}
	bare := store.Namespaces[0].Group
	if bare.Error == nil || *bare.Error != filepath.Join(env.Store, "imports", ".shortcut")+": a namespace file needs a name, e.g. docker.shortcut" {
		t.Errorf("bare: %v", bare.Error)
	}
	if store.Main.LocalFile != "" || store.Main.Error != nil {
		t.Errorf("main %+v", store.Main)
	}
}

func TestStores_LoadProjectGroupErrors(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"a": map[string]any{"inline": "1"}})
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {}, "imports": {}}`, 0)
	g := LoadProjectGroup(proj)
	if want := filepath.Join(proj, ".shortcuts.local") + " can't have 'imports': namespaces belong in .shortcuts"; g.Error == nil || *g.Error != want {
		t.Errorf("%v", g.Error)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{not json`, 0)
	if g = LoadProjectGroup(proj); g.Error == nil || !strings.HasPrefix(*g.Error, "corrupt registry "+filepath.Join(proj, ".shortcuts.local")+": ") {
		t.Errorf("%v", g.Error)
	}
	// the shared file's error comes first, and the shared data is whatever read
	env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {}, "extra": 1}`, 0)
	if g = LoadProjectGroup(proj); g.Error == nil || *g.Error != filepath.Join(proj, ".shortcuts")+" has an unknown field: extra" {
		t.Errorf("%v", g.Error)
	}
	// an absent .shortcuts is just empty
	only := env.Project(nil, testkit.Name("only"))
	env.WriteFile(filepath.Join(only, ".shortcuts.local"), `{"version": 1, "shortcuts": {"m": {"inline": "x"}}}`, 0)
	if g = LoadProjectGroup(only); g.Error != nil || len(g.Shortcuts().Keys) != 1 {
		t.Errorf("%+v", g)
	}
	if g = ReloadGroup(g); g.LocalFile == "" {
		t.Error("ReloadGroup of a project lost its local file")
	}
}

func TestStores_EntryErrors(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{
		"ok":     map[string]any{"script": "bin/tool"},
		"gone":   map[string]any{"script": "bin/nope"},
		"escape": map[string]any{"script": "../x"},
		"bad":    map[string]any{"inline": "a", "script": "b"},
		"a b":    map[string]any{"inline": "x"},
		"isdir":  map[string]any{"script": "bin"},
	}, testkit.Files(map[string]any{"bin/tool": "x"}))
	g := LoadStore(proj).Main
	var got []string
	for _, e := range EntryErrors(g) {
		got = append(got, e[:min(len(e), 60)])
	}
	if len(got) != 5 {
		t.Fatalf("errors: %q", EntryErrors(g))
	}
	joined := strings.Join(EntryErrors(g), "\n")
	for _, want := range []string{
		"'gone' is not a valid entry: script not found: bin/nope",
		"path traversal detected: '../x' resolves to",
		"'bad' is not a valid entry: it needs exactly one of 'inline' and 'script'",
		"invalid shortcut name 'a b'.",
		"'isdir' is not a valid entry: script not found: bin",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "'ok'") {
		t.Errorf("a valid entry was reported:\n%s", joined)
	}
}

func TestKey_TellsNonUTF8NamesApart(t *testing.T) {
	store := testkit.New(t).Tmp
	keys := map[string]string{}
	for _, file := range []string{"caf\xe9", "caf\xea", "café", `caf\udce9`, "\xed\xa0\x80", "\xed\xb2\x80", "\xed\x80\x80", "\xed\xc0\x80", "\xed\xff\xbf", "\xed\xbf\xbf", "\xed\xa0"} {
		g := Group{Name: "ns", StoreDir: store, File: filepath.Join(store, file)}
		k := g.Key()
		if prev, dup := keys[k]; dup {
			t.Errorf("%q and %q share the key %s", prev, file, k)
		}
		keys[k] = file
		if RecordName(k) == "" {
			t.Fatal("no record name")
		}
	}
	names := map[string]bool{}
	for k := range keys {
		names[RecordName(k)] = true
	}
	if len(names) != len(keys) {
		t.Errorf("%d record names for %d keys", len(names), len(keys))
	}
	if got, want := keyText("a", "é😀\xff"), "["+strconv.QuoteToASCII("a")+", "+strconv.QuoteToASCII("é😀\xff")+"]"; got != want {
		t.Errorf("got %s want %s", got, want)
	}
	if got, want := keyText("a", "x"), `["a", "x"]`; got != want {
		t.Errorf("got %s want %s", got, want)
	}
	if got, want := keyText("é😀\xff"), `["\u00e9\U0001f600\xff"]`; got != want {
		t.Errorf("got %s want %s", got, want)
	}
	// U+FFFD written out is not the raw byte it replaces
	a, b := Group{Name: "ns", StoreDir: store, File: filepath.Join(store, "caf\xe9.shortcut")}, Group{Name: "ns", StoreDir: store, File: filepath.Join(store, "caf\xef\xbf\xbd.shortcut")}
	if a.Key() == b.Key() || RecordName(a.Key()) == RecordName(b.Key()) {
		t.Errorf("%s and %s share a record", a.Key(), b.Key())
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

func TestWriteJSONAtomic_RefusesInvalidUTF8AndKeepsTheOldFile(t *testing.T) {
	path := filepath.Join(testkit.New(t).Tmp, "s.json")
	old := "{\n  \"version\": 1\n}\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &ordjson.Object{}
	o.Set("name", "caf\xe9")
	if err := WriteJSONAtomic(path, o); !errors.Is(err, ordjson.ErrNotUTF8) {
		t.Fatalf("got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != old {
		t.Errorf("file changed: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*.tmp")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
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
		g, _ = LoadStore(store).Namespace("n")
		if c.want == nil && g.Error != nil || c.want != nil && (g.Error == nil || *g.Error != c.want(ns)) {
			t.Errorf("namespace %s: %v", c.body, g.Error)
		}
	}
}

// newGroup is a Group with an empty file, as LoadGroup gives for a missing one.
func newGroup() Group { return Group{Data: &ordjson.Object{}} }
