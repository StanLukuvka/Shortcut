// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-5). Don't hand-edit without checking the plan.

package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func parse(t *testing.T, s string) pyjson.Value {
	t.Helper()
	v, err := pyjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func obj(t *testing.T, s string) *pyjson.Object {
	t.Helper()
	return parse(t, s).(*pyjson.Object)
}

func TestEntry_FromValueMessages(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[]`, "'x' is not a valid entry: expected a dictionary"},
		{`{"inline": "a", "zed": 1, "bar": 2}`, "'x' is not a valid entry: unknown field: bar, zed"},
		{`{"inline": "a", "description": null}`, "'x' is not a valid entry: 'description' must be a string"},
		{`{"inline": 1}`, "'x' is not a valid entry: 'inline' must be a string"},
		{`{"inline": "a", "cwd": 5}`, "'x' is not a valid entry: 'cwd' must be a string"},
		{`{"inline": "a", "confirm": "yes"}`, "'x' is not a valid entry: 'confirm' must be a boolean"},
		{`{"inline": "a", "export": null}`, "'x' is not a valid entry: 'export' must be a boolean"},
		{`{}`, "'x' is not a valid entry: it needs exactly one of 'inline' and 'script'"},
		{`{"inline": "a", "script": "b"}`, "'x' is not a valid entry: it needs exactly one of 'inline' and 'script'"},
		{`{"inline": "a", "args": {}}`, "'x' is not a valid entry: 'args' must be a list"},
		{`{"inline": "a", "from": {"source": "s"}}`, "'x' is not a valid entry: 'from' must be an object with a string 'source' and 'name'"},
		{`{"inline": "a", "from": {"source": "s", "name": 1}}`, "'x' is not a valid entry: 'from' must be an object with a string 'source' and 'name'"},
		{`{"inline": "a", "from": {"source": "s", "name": "n", "x": "y"}}`, "'x' is not a valid entry: 'from' must be an object with a string 'source' and 'name'"},
	}
	for _, c := range cases {
		_, err := EntryFromValue("x", parse(t, c.in))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: got %v, want %q", c.in, err, c.want)
		}
	}
}

func TestEntry_ValidateArgsMessages(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[1]`, "every entry in 'args' needs a 'name' (letters, digits, _ and -)"},
		{`[{"name": "1a"}]`, "every entry in 'args' needs a 'name' (letters, digits, _ and -)"},
		{`[{"name": "a\n"}]`, "every entry in 'args' needs a 'name' (letters, digits, _ and -)"},
		{`[{"name": "a", "x": 1, "b": 1}]`, "unknown args field: b, x"},
		{`[{"name": "a", "default": 1}]`, "args 'default' must be a string"},
		{`[{"name": "a", "desc": 1}]`, "args 'desc' must be a string"},
		{`[{"name": "a", "required": 1}]`, "args 'required' must be a boolean"},
		{`[{"name": "a", "rest": "y"}]`, "args 'rest' must be a boolean"},
		{`[{"name": "a", "rest": true}, {"name": "b"}]`, "only the last entry in 'args' can be 'rest'"},
		{`[{"name": "a", "required": true, "default": "d"}]`, "arg 'a' can't be both 'required' and have a 'default'"},
		{`[{"name": "a"}, {"name": "a"}]`, "arg 'a' is declared twice"},
		{`[{"name": "a-b"}, {"name": "a_b"}]`, "args 'a-b' and 'a_b' would both be SC_ARG_A_B"},
	}
	for _, c := range cases {
		err := ValidateArgs("x", parse(t, c.in))
		if want := "'x' is not a valid entry: " + c.want; err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", c.in, err, want)
		}
	}
	if err := ValidateArgs("x", parse(t, `[{"name": "a", "rest": true, "desc": "d", "default": "z"}]`)); err != nil {
		t.Error(err)
	}
}

func TestEntry_RoundTripKeepsArgsAndOrder(t *testing.T) {
	src := `{"description": "d", "script": "s.sh", "cwd": "c", "confirm": true, "export": true,` +
		` "args": [{"name": "a", "desc": "x", "required": true}, {"name": "b"}], "from": {"source": "/p", "name": "n"}}`
	e, err := EntryFromValue("x", parse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pyjson.DumpsCompact(e.ToObject()), pyjson.DumpsCompact(parse(t, src)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if e.Inline != nil || *e.Script != "s.sh" || e.Origin.Name != "n" || !e.Confirm || len(e.Args) != 2 {
		t.Errorf("fields: %+v", e)
	}
	// An empty inline is still set; an empty args list is not "absent".
	e, err = EntryFromValue("y", parse(t, `{"inline": "", "args": []}`))
	if err != nil || e.Inline == nil || e.Args == nil {
		t.Fatalf("%+v %v", e, err)
	}
	if got := pyjson.DumpsCompact(e.ToObject()); got != `{"description": "", "inline": "", "args": []}` {
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

func TestEntry_PlacementError(t *testing.T) {
	exp := &Entry{Name: "x", Export: true}
	if got := PlacementError(exp, false, true); got != "'x' is not a valid entry: 'export' is only for .shortcuts and .shortcuts.local" {
		t.Error(got)
	}
	from := &Entry{Name: "x", Origin: &Origin{"s", "n"}}
	if got := PlacementError(from, true, false); got != "'x' is not a valid entry: 'from' is only for the global store's shortcuts.json" {
		t.Error(got)
	}
	if PlacementError(exp, true, false) != "" || PlacementError(from, false, true) != "" {
		t.Error("allowed placements refused")
	}
}

func TestRegistry_LoadRegistryFile(t *testing.T) {
	env := testkit.New(t)
	p := filepath.Join(env.Tmp, "r.json")
	cases := []struct{ file, want string }{
		{`{not json`, "corrupt registry " + p + ": Expecting property name enclosed in double quotes: line 1 column 2 (char 1)"},
		{`[]`, "corrupt registry " + p + ": root must be a JSON object"},
		{"\xef\xbb\xbf{}", "corrupt registry " + p + ": Unexpected UTF-8 BOM (decode using utf-8-sig): line 1 column 1 (char 0)"},
		{"{\"a\": \"\xff\"}", "corrupt registry " + p + ": 'utf-8' codec can't decode byte 0xff in position 7: invalid start byte"},
		{"{\r\n  \"a\": 1,\r\n}", "corrupt registry " + p + ": Illegal trailing comma before end of object: line 2 column 9 (char 10)"},
		{`{"version": "2"}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 2"},
		{`{"version": 2.0}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 2"},
		{`{"version": null}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 2"},
		{`{"version": true}`, "corrupt registry " + p + ": 'version' must be a whole number, such as 2"},
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
	for _, ok := range []string{`{}`, `{"version": 0}`, `{"version": 1, "x": 1}`, `{"version": 2}`, `{"version": -5}`} {
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
		{`{"version": 2, "shortcuts": {}}`, "", ""},
		{`{"version": 2, "shortcuts": {}, "imports": {}, "description": "d"}`, "", ""},
		{`{"shortcuts": []}`, "", "corrupt registry P: 'shortcuts' must be an object"},
		{`{"shortcuts": null}`, "", "corrupt registry P: 'shortcuts' must be an object"},
		{`{"description": 1}`, "", "corrupt registry P: 'description' must be a string"},
		{`{"zed": 1, "bar": 1, "shortcuts": {}}`, "", "P has an unknown field: bar, zed"},
		{`{"version": 2, "shortcuts": {}}`, "ns", ""},
		{`{"version": 2, "shortcuts": {}, "imports": {}}`, "ns", "P has its own 'imports', but namespaces don't nest"},
		{`{"shortcuts": {}}`, "ns", `P needs "version": 2`},
		{`{"version": 1, "shortcuts": {}}`, "ns", `P needs "version": 2`},
		{`{"version": 2}`, "ns", "P needs 'shortcuts'"},
	}
	for _, c := range cases {
		got := FileError("P", obj(t, c.file), c.name)
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

// py: test_stores.py::test_atomic_registry_save_and_corrupt_protection
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
	if pyjson.DumpsCompact(loaded) != pyjson.DumpsCompact(data) {
		t.Errorf("loaded %s", pyjson.DumpsCompact(loaded))
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

// py: test_stores.py::test_store_discovery_skips_global_root
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
	if got := FindProjectRoot(sub); got != pyos.Realpath(proj) {
		t.Errorf("sub: %q", got)
	}
}

func TestStores_ProjectRootViaLocalFileOnlyAndNearest(t *testing.T) {
	env := testkit.New(t)
	outer := env.Project(nil, testkit.Name("outer"))
	env.WriteFile(filepath.Join(outer, ".shortcuts.local"), `{"version": 2, "shortcuts": {}}`, 0)
	inner := filepath.Join(outer, "in")
	env.WriteStore(filepath.Join(inner, ".shortcuts"), map[string]any{})
	if got := FindProjectRoot(filepath.Join(inner, "x")); got != inner {
		t.Errorf("nearest: %q", got)
	}
	if got := FindProjectRoot(outer); got != outer {
		t.Errorf("local only: %q", got)
	}
	// a .shortcuts that is a folder (shortcut 1.x) is no project
	old := env.Project(nil, testkit.Name("old"), testkit.Dirs(".shortcuts"))
	if got := FindProjectRoot(old); got != "" {
		t.Errorf("1.x folder: %q", got)
	}
	if got := OldProjectFolder(old); got != filepath.Join(old, ".shortcuts") {
		t.Errorf("OldProjectFolder: %q", got)
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
	target := env.WriteFile(filepath.Join(dotfiles, "shortcuts.json"), `{"version": 2, "shortcuts": {}}`, 0o644)
	link := filepath.Join(env.Store, "shortcuts.json")
	env.Symlink(target, link)
	v := obj(t, `{"version": 2, "shortcuts": {"hi": {"description": "é", "n": 1.0}}}`)
	if err := WriteJSONAtomic(link, v); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}
	got, _ := os.ReadFile(target)
	want := "{\n  \"version\": 2,\n  \"shortcuts\": {\n    \"hi\": {\n      \"description\": \"\\u00e9\",\n      \"n\": 1.0\n    }\n  }\n}\n"
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
	// a new file is private, like Python's NamedTemporaryFile
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
	if err := WriteJSONAtomic(target, &pyjson.Object{}); err == nil {
		t.Fatal("want an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "x.json" {
		t.Errorf("left behind: %v", entries)
	}
}

func TestStores_LockPathsAndSymlinkedLock(t *testing.T) {
	env := testkit.New(t)
	if got := LockPath(env.Store); got != filepath.Join(env.Store, ".lock") {
		t.Error(got)
	}
	proj := env.Project(map[string]any{})
	// sha256("<realpath>") from Python: hashlib.sha256(os.path.realpath(proj).encode()).hexdigest()
	got := LockPath(proj)
	if !strings.HasPrefix(got, filepath.Join(env.Store, "locks")+"/") || !strings.HasSuffix(got, ".lock") || len(filepath.Base(got)) != 64+len(".lock") {
		t.Error(got)
	}
	ran := false
	if err := WithRegistryLock(proj, func() error { ran = true; return nil }); err != nil || !ran {
		t.Errorf("%v %v", ran, err)
	}
	// a symlinked .lock is refused and its victim untouched (test_symlink_safety.py:34-41)
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
	if string(b) != "{\n  \"version\": 2,\n  \"shortcuts\": {}\n}\n" {
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

func TestGroup_ShortcutsOverlaySourceAndHiding(t *testing.T) {
	g := Group{
		File: "/p/.shortcuts", StoreDir: "/p", LocalFile: "/p/.shortcuts.local",
		Data:      obj(t, `{"shortcuts": {"a": {"inline": "1"}, "b": {"inline": "2"}, "c": {"inline": "3"}, "n": null}}`),
		LocalData: obj(t, `{"shortcuts": {"b": {"inline": "B"}, "c": null, "d": {"inline": "4"}, "z": null}}`),
	}
	if got := strings.Join(g.Shortcuts().Keys, ","); got != "a,b,n,d" {
		t.Errorf("keys %s", got)
	}
	if v, _ := g.Shortcuts().Get("b"); pyjson.DumpsCompact(v) != `{"inline": "B"}` {
		t.Error("b not overridden")
	}
	for name, want := range map[string]string{"a": "shared", "b": "overridden", "c": "hidden", "d": "local", "z": "hidden"} {
		if got := g.Source(name); got != want {
			t.Errorf("Source(%s) = %s, want %s", name, got, want)
		}
	}
	if g.DefiningFile("b") != "/p/.shortcuts.local" || g.DefiningFile("a") != "/p/.shortcuts" {
		t.Error("DefiningFile")
	}
	var zero Group // a Group with no data reads as empty
	if len(zero.Shortcuts().Keys) != 0 {
		t.Error("zero group")
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
	env.WriteRegistryRaw(file, `{"version": 2, "shortcuts": {}, "imports": {"num": 5, "plain": "x.json", "bare": "dir/.shortcut", "ok": "~/x.shortcut", "Bad Name": "w.shortcut"}}`)
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
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 2, "shortcuts": {}, "imports": {}}`, 0)
	g := LoadProjectGroup(proj)
	if want := filepath.Join(proj, ".shortcuts.local") + " can't have 'imports': namespaces belong in .shortcuts"; g.Error == nil || *g.Error != want {
		t.Errorf("%v", g.Error)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{not json`, 0)
	if g = LoadProjectGroup(proj); g.Error == nil || !strings.HasPrefix(*g.Error, "corrupt registry "+filepath.Join(proj, ".shortcuts.local")+": ") {
		t.Errorf("%v", g.Error)
	}
	// the shared file's error comes first, and the shared data is whatever read
	env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 2, "shortcuts": {}, "extra": 1}`, 0)
	if g = LoadProjectGroup(proj); g.Error == nil || *g.Error != filepath.Join(proj, ".shortcuts")+" has an unknown field: extra" {
		t.Errorf("%v", g.Error)
	}
	// an absent .shortcuts is just empty
	only := env.Project(nil, testkit.Name("only"))
	env.WriteFile(filepath.Join(only, ".shortcuts.local"), `{"version": 2, "shortcuts": {"m": {"inline": "x"}}}`, 0)
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
		"export": map[string]any{"inline": "x", "export": true},
		"from":   map[string]any{"inline": "x", "from": map[string]any{"source": "s", "name": "n"}},
		"a b":    map[string]any{"inline": "x"},
		"isdir":  map[string]any{"script": "bin"},
	}, testkit.Files(map[string]any{"bin/tool": "x"}))
	g := LoadStore(proj).Main
	var got []string
	for _, e := range EntryErrors(g) {
		got = append(got, e[:min(len(e), 60)])
	}
	if len(got) != 6 {
		t.Fatalf("errors: %q", EntryErrors(g))
	}
	joined := strings.Join(EntryErrors(g), "\n")
	for _, want := range []string{
		"'gone' is not a valid entry: script not found: bin/nope",
		"path traversal detected: '../x' resolves to",
		"'bad' is not a valid entry: it needs exactly one of 'inline' and 'script'",
		"'from' is only for the global store's shortcuts.json",
		"invalid shortcut name 'a b'.",
		"'isdir' is not a valid entry: script not found: bin",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "'ok'") || strings.Contains(joined, "'export'") {
		t.Errorf("a valid entry was reported:\n%s", joined)
	}
}
