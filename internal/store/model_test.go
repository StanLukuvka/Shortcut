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

func doc(t *testing.T, s string) platform.Doc {
	t.Helper()
	d, err := platform.ParseDoc([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEntry_FromValueMessages(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[]`, "'x' is not a valid entry: expected a JSON object"},
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
		_, err := DecodeEntry("x", []byte(c.in))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: got %v, want %q", c.in, err, c.want)
		}
	}
}

func TestEntry_EmptyInlineIsStillSet(t *testing.T) {
	e, err := DecodeEntry("y", []byte(`{"inline": ""}`))
	if err != nil || e.Inline == nil {
		t.Fatalf("%+v %v", e, err)
	}
	if got, _ := e.Raw(); string(got) != `{"inline":""}` {
		t.Error(string(got))
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
	if err := SaveRegistryAtomic(env.Store, data); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRegistry(env.Store)
	if err != nil {
		t.Fatal(err)
	}
	if compactOf(t, loaded) != compactOf(t, data) {
		t.Errorf("loaded %s", compactOf(t, loaded))
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
	if err := SaveRegistryAtomic(env.Store, doc(t, `{"version": 1, "shortcuts": {}}`)); err != nil {
		t.Fatal(err)
	}
	// Walking up from home or subdirectory must skip ~/.shortcuts
	if got, _ := NewContext(env.Home, nil).FindProjectRoot(env.Home); got != "" {
		t.Errorf("home: %q", got)
	}
	deep := filepath.Join(env.Home, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewContext(deep, nil).FindProjectRoot(deep); got != "" {
		t.Errorf("deep: %q", got)
	}
	// Walking up from inside ~/.shortcuts itself must return none
	if got, _ := NewContext(env.Store, nil).FindProjectRoot(env.Store); got != "" {
		t.Errorf("store: %q", got)
	}
	// A real local store in a project must be found
	proj := filepath.Join(env.Home, "my_project")
	env.WriteStore(filepath.Join(proj, ".shortcuts"), map[string]any{})
	sub := filepath.Join(proj, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewContext(sub, nil).FindProjectRoot(sub); got != mustReal(t, proj) {
		t.Errorf("sub: %q", got)
	}
}

func TestStores_ProjectRootViaLocalFileOnlyAndNearest(t *testing.T) {
	env := testkit.New(t)
	outer := env.Project(nil, testkit.Name("outer"))
	env.WriteFile(filepath.Join(outer, ".shortcuts.local"), `{"version": 1, "shortcuts": {}}`, 0)
	inner := filepath.Join(outer, "in")
	env.WriteStore(filepath.Join(inner, ".shortcuts"), map[string]any{})
	if err := os.Mkdir(filepath.Join(inner, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewContext(filepath.Join(inner, "x"), nil).FindProjectRoot(filepath.Join(inner, "x")); got != inner {
		t.Errorf("nearest: %q", got)
	}
	if got, _ := NewContext(outer, nil).FindProjectRoot(outer); got != outer {
		t.Errorf("local only: %q", got)
	}
	// a .shortcuts that is a folder is no project
	old := env.Project(nil, testkit.Name("old"), testkit.Dirs(".shortcuts"))
	if got, _ := NewContext(old, nil).FindProjectRoot(old); got != "" {
		t.Errorf("a .shortcuts folder: %q", got)
	}
}

func TestStores_LocalProjectRoot(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{}, testkit.Name("p"))
	sub := filepath.Join(proj, "sub")
	os.MkdirAll(sub, 0o755)
	if got, err := NewContext(proj, nil).LocalProjectRoot(); err != nil || got != proj {
		t.Errorf("%q %v", got, err)
	}
	if _, err := NewContext(sub, nil).LocalProjectRoot(); err == nil || err.Error() != sub+" is inside the project at "+proj+". Run this from there." {
		t.Error(err)
	}
	if _, err := NewContext(env.Home, nil).LocalProjectRoot(); err == nil || err.Error() != env.Home+" holds the global store, so it can't hold local shortcuts. Run this inside a project folder." {
		t.Error(err)
	}
	if _, err := NewContext(env.Store, nil).LocalProjectRoot(); err == nil || !strings.Contains(err.Error(), "is inside the global store") {
		t.Error(err)
	}
	victim := env.WriteFile(filepath.Join(env.Tmp, "victim"), "keep\n", 0)
	fresh := env.Folder("fresh")
	env.Symlink(victim, filepath.Join(fresh, ".shortcuts.local"))
	// a symlinked .shortcuts.local makes the folder a project; it is its own root, but the link is refused
	if _, err := NewContext(fresh, nil).LocalProjectRoot(); err == nil || err.Error() != filepath.Join(fresh, ".shortcuts.local")+" is a symlink, so writing it would go somewhere else." {
		t.Error(err)
	}
}

func TestStores_WriteJSONAtomic(t *testing.T) {
	env := testkit.New(t)
	dotfiles := env.Folder("dotfiles")
	target := env.WriteFile(filepath.Join(dotfiles, "shortcuts.json"), `{"version": 1, "shortcuts": {}}`, 0o644)
	link := filepath.Join(env.Store, "shortcuts.json")
	env.Symlink(target, link)
	v := doc(t, `{"version": 1, "shortcuts": {"hi": {"description": "é", "n": 1.0}}}`)
	if err := platform.WriteJSONAtomic(link, v); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}
	got, _ := os.ReadFile(target)
	want := "{\n  \"shortcuts\": {\n    \"hi\": {\n      \"description\": \"é\",\n      \"n\": 1.0\n    }\n  },\n  \"version\": 1\n}\n"
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
	if err := platform.WriteJSONAtomic(fresh, v); err != nil {
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
	if err := platform.WriteJSONAtomic(target, platform.Doc{}); err == nil {
		t.Fatal("want an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "x.json" {
		t.Errorf("left behind: %v", entries)
	}
}

func TestStores_LockIsInTheGlobalStoreAndASymlinkedLockIsRefused(t *testing.T) {
	env := testkit.New(t)
	c := NewContext(env.Home, nil)
	// the lock is <global>/.lock; a symlink there is refused and its victim untouched
	ran := false
	victim := env.WriteFile(filepath.Join(env.Tmp, "victim"), "keep\n", 0)
	env.Symlink(victim, filepath.Join(env.Store, ".lock"))
	if err := c.WithLock(func() error { ran = true; return nil }); err == nil || ran {
		t.Errorf("ran=%v err=%v", ran, err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
		t.Error("victim changed")
	}
	if err := c.WithLock(func() error { return nil }); err == nil {
		t.Error("WithLock through a symlinked .lock must fail")
	}
}

func TestGroup_ShortcutsOverlayAndSource(t *testing.T) {
	shared := doc(t, `{"shortcuts": {"a": {"inline": "1"}, "b": {"inline": "2"}, "c": {"inline": "3"}}}`)
	local := doc(t, `{"shortcuts": {"b": {"inline": "B"}, "d": {"inline": "4"}}}`)
	g := Group{
		Kind: KindProject, InProject: true, File: "/p/.shortcuts", StoreDir: "/p", LocalFile: "/p/.shortcuts.local",
		Doc: shared, Shared: ItemsOf(shared), Local: ItemsOf(local),
	}
	if got := strings.Join(g.Shortcuts().Names(), ","); got != "a,b,c,d" {
		t.Errorf("keys %s", got)
	}
	if it, _ := g.Shortcuts().Get("b"); string(it.Raw) != `{"inline": "B"}` {
		t.Error("b not overridden")
	}
	for name, want := range map[string]string{"a": "shared", "b": "overridden", "c": "shared", "d": "local"} {
		if got := string(g.Source(name)); got != want {
			t.Errorf("Source(%s) = %s, want %s", name, got, want)
		}
	}
	if g.DefiningFile("b") != "/p/.shortcuts.local" || g.DefiningFile("a") != "/p/.shortcuts" {
		t.Error("DefiningFile")
	}
	if len(newGroup().Shortcuts()) != 0 { // no local file (a namespace) and an empty file read as empty
		t.Error("empty group")
	}
	// a non-object "shortcuts" is empty, not a crash
	if d := doc(t, `{"shortcuts": []}`); len((Group{Doc: d, Shared: ItemsOf(d)}).Shortcuts()) != 0 {
		t.Error("shortcuts that is not an object")
	}
}

func TestGroup_Names(t *testing.T) {
	env := testkit.New(t)
	ns := Group{Kind: KindNamespace, Name: "docker", File: filepath.Join(env.Store, "imports", "docker.shortcut"), StoreDir: env.Store}
	if ns.Display("ps") != "docker ps" || ns.Label() != "docker" ||
		ns.ScriptBase() != filepath.Join(env.Store, "imports") || ns.ScriptsDir() != filepath.Join(env.Store, "imports", "scripts", "docker") {
		t.Errorf("%+v", ns)
	}
	global := Group{Kind: KindGlobal, File: filepath.Join(env.Store, "shortcuts.json"), StoreDir: env.Store}
	if global.InProject || global.Display("x") != "x" || global.ScriptBase() != env.Store ||
		global.ScriptsDir() != filepath.Join(env.Store, "scripts") {
		t.Errorf("%+v", global)
	}
	proj := Group{Kind: KindProject, InProject: true, File: filepath.Join(env.Tmp, "p", ".shortcuts"), StoreDir: filepath.Join(env.Tmp, "p")}
	if !proj.InProject || proj.Label() != proj.StoreDir {
		t.Errorf("%+v", proj)
	}
}

func TestStores_LoadStoreProjectAndNamespaces(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"p": map[string]any{"inline": "echo p"}, "zed": map[string]any{"inline": "x"}})
	env.WriteImport(filepath.Join(proj, ".shortcuts"), "zed", testkit.DockerPS) // a namespace that is also a shortcut
	env.WriteImport(filepath.Join(proj, ".shortcuts"), "b", testkit.DockerPS)
	env.WriteImport(filepath.Join(proj, ".shortcuts"), "a", testkit.KubeGet)
	store := NewContext(env.Home, nil).Store(proj)
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
	store := NewContext(env.Home, nil).Store(proj)
	want := []string{
		"import 'bare' in " + file + ": a namespace file needs a name, e.g. docker.shortcut",
		"import 'num' in " + file + " must be a path",
		"import 'plain' in " + file + " must be a .shortcut file",
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
	store := NewContext(env.Home, nil).Store(env.Store)
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
	if g = LoadProjectGroup(only); g.Error != nil || len(g.Shortcuts()) != 1 {
		t.Errorf("%+v", g)
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
	}, testkit.Files(map[string]any{"bin/tool": "x"}), testkit.Mode(0o755))
	g := NewContext(env.Home, nil).Store(proj).Main
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

// newGroup is a Group with an empty file, as LoadGroup gives for a missing one.
func newGroup() Group { return Group{Doc: platform.Doc{}} }

func TestGroup_KindFromLoaders(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{})
	if g := LoadGroup(filepath.Join(e.Store, "shortcuts.json"), e.Store, ""); g.Kind != KindGlobal || g.InProject {
		t.Errorf("global: %+v", g)
	}
	proj := e.Project(map[string]any{"x": testkit.Inline("x")})
	if g := LoadProjectGroup(proj); g.Kind != KindProject || !g.InProject {
		t.Errorf("project: %+v", g)
	}
	ns := filepath.Join(e.Home, "n.shortcut")
	e.WriteFile(ns, `{"version": 1, "shortcuts": {}}`, 0)
	e.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {}, "imports": {"n": "`+ns+`"}}`, 0)
	e.WriteFile(filepath.Join(e.Store, "shortcuts.json"), `{"version": 1, "shortcuts": {}, "imports": {"n": "`+ns+`"}}`, 0)
	c := NewContext(proj, nil)
	if g, ok := c.Store(c.Project).Namespace("n"); !ok || g.Kind != KindNamespace || !g.InProject {
		t.Errorf("project namespace: %+v", g)
	}
	if g, ok := c.Store(e.Store).Namespace("n"); !ok || g.Kind != KindNamespace || g.InProject {
		t.Errorf("global namespace: %+v", g)
	}
}

// compactOf is d on one line, as the JSON encoder writes it.
func compactOf(t *testing.T, d platform.Doc) string {
	t.Helper()
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStore_AnUnreadableImportsFolderIsAProblem(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any folder")
	}
	env := testkit.New(t)
	imports := filepath.Join(env.Store, "imports")
	if err := os.MkdirAll(imports, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(imports, 0); err != nil {
		t.Fatal(err)
	}
	problems := NewContext(env.Home, nil).Store(env.Store).Problems
	if len(problems) != 1 || !strings.Contains(problems[0], imports) {
		t.Errorf("problems: %q", problems)
	}
}
