package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestStores_StoreDiscoverySkipsGlobalRoot(t *testing.T) {
	env := testkit.New(t)
	if err := os.MkdirAll(env.Store, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := SaveRegistryFile(filepath.Join(env.Store, "shortcuts.json"), doc(t, `{"version": 1, "shortcuts": {}}`)); err != nil {
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

// The symlink refusal itself is platform's (TestLock_ASymlinkedLockIsRefused).
func TestStores_LockIsInTheGlobalStore(t *testing.T) {
	env := testkit.New(t)
	if err := NewContext(env.Home, nil).WithLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(env.Store, ".lock")); err != nil {
		t.Errorf("no lock at <global>/.lock: %v", err)
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
	if g := byName["ok"]; g.File != filepath.Join(env.Home, "x.shortcut") || g.Error == nil || *g.Error != "open "+g.File+": no such file or directory" {
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

func TestStore_NotAFileOrFolderWhereOneIsReadIsAnError(t *testing.T) {
	env := testkit.New(t)
	if err := os.MkdirAll(filepath.Join(env.Store, "shortcuts.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.WriteFile(filepath.Join(env.Store, "imports"), "x", 0)
	store := NewContext(env.Home, nil).Store(env.Store)
	if store.Main.Error == nil || !strings.HasPrefix(*store.Main.Error, "read "+store.Main.File+": ") {
		t.Errorf("main: %+v", store.Main.Error)
	}
	if len(store.Problems) != 1 || !strings.Contains(store.Problems[0], filepath.Join(env.Store, "imports")) {
		t.Errorf("problems: %q", store.Problems)
	}
}

func TestStore_NoFileAndNoImportsFolderIsAnEmptyStore(t *testing.T) {
	env := testkit.New(t)
	store := NewContext(env.Home, nil).Store(env.Store)
	if store.Main.Error != nil || len(store.Problems) != 0 || len(store.Namespaces) != 0 {
		t.Errorf("%+v", store)
	}
}

func TestContext_StoreIsLoadedOnce(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"a": testkit.Inline("x")})
	c := NewContext(e.Home, nil)
	if _, has := c.Store(e.Store).Main.Shortcuts().Get("a"); !has {
		t.Fatal("a missing")
	}
	e.WriteStore(e.Store, map[string]any{"a": testkit.Inline("x"), "b": testkit.Inline("y")})
	if _, has := c.Store(e.Store).Main.Shortcuts().Get("b"); has {
		t.Error("a cached store was read again")
	}
	if _, has := c.ReadStore(e.Store).Main.Shortcuts().Get("b"); !has {
		t.Error("ReadStore did not see the new file")
	}
	if _, has := c.Store(e.Store).Main.Shortcuts().Get("b"); has {
		t.Error("ReadStore changed the cache")
	}
	if _, has := NewContext(e.Home, nil).Store(e.Store).Main.Shortcuts().Get("b"); !has {
		t.Error("a new Context did not see the new file")
	}
}

func TestContext_ProjectAndGlobalResolvedOnce(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"x": testkit.Inline("x")})
	c := NewContext(proj, nil)
	if c.Project != mustReal(t, proj) || c.Global != e.Store {
		t.Errorf("project %q global %q", c.Project, c.Global)
	}
	if got := NewContext(filepath.Join(e.Home, "elsewhere"), nil).Project; got != "" {
		t.Errorf("no project: %q", got)
	}
}
