package commands

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// fileShortcuts is the "shortcuts" object of a store or namespace file, as the test sees it.
func fileShortcuts(t *testing.T, path string) map[string]any {
	t.Helper()
	doc := decodeObject(t, testkit.ReadFile(t, path))
	s, _ := doc["shortcuts"].(map[string]any)
	return s
}

func TestRunning_CommandWordsAreOrdinaryShortcutNames(t *testing.T) {
	e := testkit.New(t)
	for _, name := range []string{"new", "list", "run", "r", "nano"} {
		runOK(t, e, testkit.Opts{}, "new", name, "--", "echo ran "+name)
		testkit.Eq(t, "run "+name, e.Run("run", name).Stdout, "ran "+name+"\n")
	}
	if !slices.Contains(strings.Fields(e.Run("list", "--names").Stdout), "run") {
		t.Errorf("list --names lacks run")
	}
}

func TestRunning_NewSaysWhenACommandIgnoresArguments(t *testing.T) {
	e := testkit.New(t)
	const hint = "won't see any arguments"
	testkit.Contains(t, "a", e.Run("new", "a", "--", "rg --hidden").Stderr, hint)
	testkit.Contains(t, "b", e.Run("new", "b", "--", "rg", "--hidden").Stderr, hint)
	for _, c := range [][2]string{{"c", `rg "$@"`}, {"d", "echo $1"}, {"e", "echo ${2}"}, {"f", "echo $*"}} {
		res := runOK(t, e, testkit.Opts{}, "new", c[0], "--", c[1])
		if strings.Contains(res.Stderr, hint) {
			t.Errorf("%s: unexpected hint in %q", c[1], res.Stderr)
		}
	}
}

func TestRunning_SeveralWordCommandsKeepPositionalArgumentsLive(t *testing.T) {
	e := testkit.New(t)
	for i, c := range []struct {
		words  []string
		stored string
	}{
		{[]string{"echo", "$1"}, `echo "$1"`},
		{[]string{"printf", "[%s]", "$@"}, `printf '[%s]' "$@"`},
		{[]string{"echo", "a b", "${1}"}, `echo 'a b' "${1}"`},
		{[]string{"echo", "$HOME"}, `echo '$HOME'`},
		{[]string{"echo", `say "$1"`}, `echo "say \"$1\""`},
		{[]string{"echo", "$(id) `id` \\ $1"}, `echo "\$(id) \` + "`id\\`" + ` \\ $1"`},
	} {
		name := "cmd" + string(rune('0'+i))
		runOK(t, e, testkit.Opts{}, append([]string{"new", name, "--"}, c.words...)...)
		testkit.Eq(t, name, e.LoadGlobal()[name].(map[string]any)["inline"], c.stored)
	}
	testkit.Eq(t, "cmd0", e.Run("r", "cmd0", "hello world").Stdout, "hello world\n")
	testkit.Eq(t, "cmd1", e.Run("r", "cmd1", "a b", "c").Stdout, "[a b][c]")
	testkit.Eq(t, "cmd3", e.Run("r", "cmd3").Stdout, "$HOME\n")
	testkit.Eq(t, "cmd5", e.Run("r", "cmd5", "x").Stdout, "$(id) `id` \\ x\n")
}

func TestStores_CreationScopeRules(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Name("fresh_proj"), testkit.Dirs("sub"))

	// 1. The global store by default, wherever you are
	runOK(t, e, in(proj), "new", "anywhere", "--", "echo 1")
	if _, has := e.LoadGlobal()["anywhere"]; !has {
		t.Errorf("anywhere is not global")
	}
	testkit.NotExist(t, filepath.Join(proj, ".shortcuts"))

	// 2. --local creates .shortcuts.local on demand, and what it adds runs once `update` has approved it
	res := runOK(t, e, in(proj), "new", "local_cmd", "--local", "--", "echo local")
	testkit.Contains(t, "stderr", res.Stderr, "needs approving before it runs: shortcut update")
	testkit.Contains(t, "run before update", e.RunWith(in(proj), "run", "local_cmd").Stderr, "isn't approved yet")
	runOK(t, e, in(proj), "update", ".", "-y")
	if _, err := os.Stat(filepath.Join(proj, ".shortcuts.local")); err != nil {
		t.Errorf("no .shortcuts.local: %v", err)
	}
	testkit.NotExist(t, filepath.Join(proj, ".shortcuts"))
	testkit.Eq(t, "run", e.RunWith(in(proj), "run", "local_cmd").Stdout, "local\n")

	// 3. --cwd is stored against the defining file: absolute globally, relative inside the project
	runOK(t, e, testkit.Opts{}, "new", "fixed", "--cwd", e.Tmp, "--", "pwd")
	testkit.Eq(t, "fixed cwd", e.LoadGlobal()["fixed"].(map[string]any)["cwd"], e.Tmp)
	runOK(t, e, in(proj), "new", "in_sub", "--local", "--cwd", "sub", "--", "pwd")
	testkit.Eq(t, "in_sub cwd", fileShortcuts(t, filepath.Join(proj, ".shortcuts.local"))["in_sub"].(map[string]any)["cwd"], "sub")
	e.Run("new", "explicit", "--cwd", "caller", "--", "pwd")
	testkit.Eq(t, "explicit cwd", e.LoadGlobal()["explicit"].(map[string]any)["cwd"], "caller")

	res = e.Run("new", "nowhere", "--cwd", "/nonexistent/path/xyz", "--", "echo 1")
	if res.Code == 0 {
		t.Errorf("nowhere: exit 0")
	}
	testkit.Contains(t, "stderr", res.Stderr, "--cwd directory does not exist")

	// 4. Duplicate name error
	res = e.RunWith(in(proj), "new", "local_cmd", "--local", "--", "echo duplicate")
	if res.Code == 0 {
		t.Errorf("duplicate: exit 0")
	}
	testkit.Contains(t, "stderr", res.Stderr, "already exists in")
}

func TestStores_NewInHomeRefusesLocalStore(t *testing.T) {
	e := testkit.New(t)
	res := e.RunWith(in(e.Home), "new", "hello", "--local", "--", "echo hi")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "holds the global store")
	if _, err := os.Stat(filepath.Join(e.Store, "shortcuts.json")); err == nil {
		if _, has := e.LoadGlobal()["hello"]; has {
			t.Errorf("hello was stored")
		}
	}
}

func TestStores_NewTakesOneSource(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	source := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	for _, c := range []struct {
		args    []string
		message string
	}{
		{[]string{"new", "x", "--script", source, "--", "true"}, "use one of"},
		{[]string{"new", "x", "--script", "", "--", "true"}, "needs a file name"},
		{[]string{"new", "x", "--script", ""}, "needs a file name"},
	} {
		res := e.RunWith(in(proj), c.args...)
		if res.Code == 0 {
			t.Errorf("%v: exit 0", c.args)
		}
		testkit.Contains(t, strings.Join(c.args, " "), res.Stderr, c.message)
	}
	testkit.NotExist(t, filepath.Join(proj, ".shortcuts.local"))
	testkit.NotExist(t, filepath.Join(e.Store, "shortcuts.json"))
}

func TestStores_AddAndLinkPaths(t *testing.T) {
	e := testkit.New(t)
	// 1. Link absolute external file
	ext := e.Script("external.sh", "#!/usr/bin/env bash\necho external\n")
	runOK(t, e, testkit.Opts{}, "new", "linked", "--script", ext, "--desc", "Linked script")
	testkit.NotExist(t, filepath.Join(e.Store, "scripts", "linked.sh"))
	testkit.Eq(t, "linked", fileShortcuts(t, filepath.Join(e.Store, "shortcuts.json"))["linked"].(map[string]any)["script"], realpath(t, ext))

	// 2. Path containment check
	if _, err := store.ResolveScriptPath(e.Store, "../../../etc/passwd"); err == nil || !strings.Contains(err.Error(), "path traversal detected") {
		t.Errorf("traversal: %v", err)
	}
}

func TestStores_NewOneLiner(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "hello", "--", "echo", "hello", "world")
	hello := e.LoadGlobal()["hello"].(map[string]any)
	testkit.Eq(t, "inline", hello["inline"], "echo hello world")
	testkit.Eq(t, "description", hello["description"], "echo hello world")
}

func TestStores_NewScriptInsideTheStoreIsKeptRelative(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	inside := e.WriteFile(filepath.Join(imports, "dk", "ps.sh"), "#!/usr/bin/env bash\necho ps\n", 0o755)
	kept := e.WriteFile(filepath.Join(imports, "scripts", "dk", "kept.sh"), "#!/usr/bin/env bash\n", 0o755)
	outside := e.Script("out.sh", "#!/usr/bin/env bash\necho out\n")
	mine := e.WriteFile(filepath.Join(e.Store, "tools", "mine.sh"), "#!/usr/bin/env bash\necho mine\n", 0o755)
	for _, argv := range [][]string{
		{"--namespace", "dk", "ps", "--script", inside},
		{"--namespace", "dk", "out", "--script", outside},
		{"--namespace", "dk", "kept", "--script", kept},
		{"mine", "--script", mine},
	} {
		runOK(t, e, testkit.Opts{}, append([]string{"new"}, argv...)...)
	}
	dk := fileShortcuts(t, filepath.Join(imports, "dk.shortcut"))
	testkit.Eq(t, "ps", dk["ps"].(map[string]any)["script"], "dk/ps.sh")
	testkit.Eq(t, "kept", dk["kept"].(map[string]any)["script"], "scripts/dk/kept.sh")
	testkit.Eq(t, "out", dk["out"].(map[string]any)["script"], realpath(t, outside))
	testkit.Eq(t, "mine", e.LoadGlobal()["mine"].(map[string]any)["script"], "tools/mine.sh")
	testkit.Eq(t, "run mine", e.Run("r", "mine").Stdout, "mine\n")
	e.Run("update", "-y", "dk")
	testkit.Eq(t, "run dk ps", e.Run("r", "dk", "ps").Stdout, "ps\n")
}

func TestStores_NamespaceFlagCreatesANamespace(t *testing.T) {
	e := testkit.New(t)
	res := runOK(t, e, testkit.Opts{}, "new", "delete", "--namespace", "docker", "--desc", "Remove one", "--", `echo "rm $1"`)
	testkit.Contains(t, "stderr", res.Stderr, "needs approving before it runs: shortcut update docker")
	doc := decodeObject(t, testkit.ReadFile(t, filepath.Join(e.Store, "imports", "docker.shortcut")))
	testkit.Eq(t, "version", doc["version"], float64(1))
	testkit.Eq(t, "inline", doc["shortcuts"].(map[string]any)["delete"].(map[string]any)["inline"], `echo "rm $1"`)
	if _, has := e.LoadGlobal()["delete"]; has {
		t.Errorf("delete is global")
	}

	testkit.Contains(t, "run", e.Run("r", "docker", "delete", "x").Stderr, "isn't approved yet")
	testkit.Eq(t, "update", e.Run("update", "docker", "-y").Code, 0)
	testkit.Eq(t, "run approved", e.Run("r", "docker", "delete", "x").Stdout, "rm x\n")

	testkit.Eq(t, "second", e.Run("new", "ps", "--namespace", "docker", "--", "echo ps").Code, 0)
	res = e.Run("new", "ps", "--namespace", "docker", "--", "echo again")
	testkit.Eq(t, "again exit", res.Code, 1)
	testkit.Contains(t, "stderr", res.Stderr, "already exists")
}

func TestStores_NamespaceFlagCollisions(t *testing.T) {
	e := testkit.New(t)
	e.Run("new", "docker", "--", "echo plain")
	res := e.Run("new", "ps", "--namespace", "docker", "--", "echo ps")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "'docker' is a shortcut")
	testkit.NotExist(t, filepath.Join(e.Store, "imports"))

	res = e.Run("new", "ps", "--namespace", "not valid", "--", "echo ps")
	testkit.Eq(t, "invalid exit", res.Code, 1)
	testkit.Contains(t, "stderr", res.Stderr, "invalid shortcut name")

	e.Run("new", "ps", "--namespace", "kube", "--", "echo ps")
	res = e.Run("new", "kube", "--", "echo plain")
	testkit.Eq(t, "kube exit", res.Code, 1)
	testkit.Contains(t, "stderr", res.Stderr, "'kube' is a namespace")
}

func TestStores_NamespaceFlagIsRefusedWithLocal(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	res := e.RunWith(in(proj), "new", "where", "--namespace", "dev", "--local", "--", "pwd")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "--namespace can't be used with --local")
	testkit.NotExist(t, filepath.Join(proj, ".shortcuts.local"))
}

func TestStores_ConcurrentNewKeepsEveryEntry(t *testing.T) {
	e := testkit.New(t)
	var procs []*testkit.Proc
	var want []string
	for i := range 16 {
		name := "n" + string(rune('a'+i))
		want = append(want, name)
		procs = append(procs, e.Start(testkit.Opts{}, "new", name, "--", "echo "+name))
	}
	for _, p := range procs {
		if res := p.Wait(30 * time.Second); res.Code != 0 {
			t.Errorf("exit %d: %s", res.Code, res.Stderr)
		}
	}
	var got []string
	for name := range e.LoadGlobal() {
		got = append(got, name)
	}
	slices.Sort(got)
	testkit.Eq(t, "entries", got, want)
}

func TestStores_ASymlinkedStoreFileIsWrittenWhereItLeads(t *testing.T) {
	e := testkit.New(t)
	dotfiles := e.Folder("dotfiles")
	target := e.WriteFile(filepath.Join(dotfiles, "shortcuts.json"), `{"version": 1, "shortcuts": {}}`, 0o644)
	link := filepath.Join(e.Store, "shortcuts.json")
	e.Symlink(target, link)
	runOK(t, e, testkit.Opts{}, "new", "hi", "--", "echo hi")
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is gone: %v", err)
	}
	if _, has := fileShortcuts(t, target)["hi"]; !has {
		t.Errorf("hi is not in the target")
	}
	if st, err := os.Stat(target); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("mode: %v %v", st, err)
	}
	entries, _ := os.ReadDir(dotfiles)
	if len(entries) != 1 || entries[0].Name() != "shortcuts.json" {
		t.Errorf("dotfiles holds %v", entries)
	}
}

func TestStores_UnknownFieldsAreErrors(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, nil) // an empty store
	for _, c := range []struct {
		name, message string
		data          map[string]any
	}{
		{"top", "unknown field: owner", map[string]any{"version": 1, "owner": "x", "shortcuts": map[string]any{}}},
		{"entry", "unknown field: sandbox", testkit.NS(map[string]any{
			"a": map[string]any{"inline": "true", "sandbox": true}})},
		{"args", "unknown field: args", testkit.NS(map[string]any{
			"a": map[string]any{"inline": "true", "args": []any{map[string]any{"name": "x"}}}})},
	} {
		e.WriteImport(e.Store, c.name, c.data)
		res := e.Run("update", c.name, "-y")
		testkit.Eq(t, c.name, res.Code, 1)
		testkit.Contains(t, c.name, res.Stderr, c.message)
		testkit.Contains(t, c.name+" list", e.Run("list").Stderr, c.message)
	}
}

func TestStores_UpdateRefusesEntriesThatCannotRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, nil) // an empty store
	for _, c := range []struct {
		name, shortcut, message string
		entry                   map[string]any
	}{
		{"neither", "a", "exactly one of 'inline' and 'script'", map[string]any{"description": "x"}},
		{"both", "a", "exactly one of 'inline' and 'script'", map[string]any{"inline": "true", "script": "a.sh"}},
		{"missing", "a", "script not found: scripts/a.sh", map[string]any{"script": "scripts/a.sh"}},
		{"escape", "a", "path traversal", map[string]any{"script": "../../outside.sh"}},
		{"badname", "a!", "invalid shortcut name", map[string]any{"inline": "true"}},
	} {
		e.WriteImport(e.Store, c.name, testkit.NS(map[string]any{c.shortcut: c.entry}))
		res := e.Run("update", c.name, "-y")
		testkit.Eq(t, c.name, res.Code, 1)
		testkit.Contains(t, c.name, res.Stderr, c.message)
		refused := e.Run("r", c.name, c.shortcut)
		testkit.Eq(t, c.name+" run", refused.Code, 1)
		want := "isn't approved yet"
		if strings.Contains(c.message, "exactly one") {
			want = c.message
		}
		testkit.Contains(t, c.name+" run", refused.Stderr, want)
	}
}

func TestStores_DefaultHomeStoreIsNeverAProject(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, nil) // with the default store: ~/.shortcuts
	env := map[string]string{"SHORTCUTS_HOME": filepath.Join(e.Tmp, "custom_store")}
	work := filepath.Join(e.Home, "work")
	os.Mkdir(work, 0o755)
	res := e.RunWith(testkit.Opts{Cwd: e.Home, Env: env}, "new", "here", "--local", "--", "echo 1")
	testkit.Eq(t, "home exit", res.Code, 1)
	testkit.Contains(t, "stderr", res.Stderr, "holds the global store")
	testkit.Eq(t, "global", e.LoadGlobal(), map[string]any{})
	runOK(t, e, testkit.Opts{Cwd: work, Env: env}, "new", "here", "--local", "--", "echo 1")
	if _, err := os.Stat(filepath.Join(work, ".shortcuts.local")); err != nil {
		t.Errorf("no .shortcuts.local: %v", err)
	}
	testkit.Eq(t, "global", e.LoadGlobal(), map[string]any{})
}

func TestStores_LocalOnlyUsesAStoreInThisDirectory(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"up": testkit.Inline("true")}, testkit.Dirs("sub"))
	registry := filepath.Join(proj, ".shortcuts")
	before := testkit.ReadFile(t, registry)
	source := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	os.Chmod(source, 0o644)
	sub := filepath.Join(proj, "sub")
	for _, args := range [][]string{
		{"new", "x", "--local", "--", "true"},
		{"new", "x", "--script", source, "--desc", "d", "--local"},
	} {
		res := e.RunWith(in(sub), args...)
		testkit.Eq(t, strings.Join(args, " "), res.Code, 1)
		testkit.Contains(t, "stderr", res.Stderr, "is inside the project at")
	}
	testkit.NotExist(t, filepath.Join(sub, ".shortcuts.local"))
	testkit.Eq(t, "registry", testkit.ReadFile(t, registry), before)
}

func TestStores_NewerStoreIsRefusedNotRewritten(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": testkit.Inline("echo hi")}, map[string]any{"version": 3})
	file := filepath.Join(e.Store, "shortcuts.json")
	before := testkit.ReadFile(t, file)
	for _, args := range [][]string{{"r", "hi"}, {"new", "more", "--", "echo more"}} {
		res := e.Run(args...)
		testkit.Eq(t, strings.Join(args, " "), res.Code, 1)
		testkit.Contains(t, "stderr", res.Stderr, "upgrade shortcut")
	}
	testkit.Eq(t, "file", testkit.ReadFile(t, file), before)
}

// A namespace entry is named with its namespace, as rm names it.
func TestNew_CreatedMessageNamesTheNamespace(t *testing.T) {
	e := testkit.New(t)
	res := runOK(t, e, testkit.Opts{}, "new", "ps", "--namespace", "docker", "--", "echo ps")
	testkit.Contains(t, "inline", res.Stdout, "Created inline shortcut 'docker ps'.")
	res = runOK(t, e, testkit.Opts{}, "new", "up", "--namespace", "docker", "--script", e.Script("up.sh", "#!/bin/sh\n"))
	testkit.Contains(t, "script", res.Stdout, "Created shortcut 'docker up' using")
}

// An empty --desc or --namespace is an error, not "absent", and nothing is written.
func TestNew_EmptyFlagsAreErrors(t *testing.T) {
	e := testkit.New(t)
	script := e.Script("f.sh", "#!/bin/sh\necho hi\n")
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"new", "plain", "--desc", ""}, "error: --desc can't be empty\n"},
		{[]string{"new", "x", "--desc=", "--", "true"}, "error: --desc can't be empty\n"},
		{[]string{"new", "x", "--script", script, "--desc", ""}, "error: --desc can't be empty\n"},
		{[]string{"new", "x", "--namespace", "", "--", "true"}, "error: --namespace can't be empty\n"},
		{[]string{"new", "x", "--namespace=", "--desc", "d"}, "error: --namespace can't be empty\n"},
	} {
		res := e.Run(c.args...)
		if res.Code != 1 || res.Stdout != "" || res.Stderr != c.msg {
			t.Errorf("%v: exit %d stdout %q stderr %q", c.args, res.Code, res.Stdout, res.Stderr)
		}
	}
	testkit.NotExist(t, filepath.Join(e.Store, "shortcuts.json"))
	runOK(t, e, testkit.Opts{}, "new", "dflt", "--script", script)
	testkit.Eq(t, "no desc", e.LoadGlobal()["dflt"].(map[string]any)["description"], "(no description)")
}

func TestList_NamesComeOutInByteOrder(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"B": testkit.Inline("true"), "a-1": testkit.Inline("true"), "a": testkit.Inline("true"), "10": testkit.Inline("true"),
	})
	got := strings.Fields(runOK(t, e, testkit.Opts{}, "list", "--names").Stdout)
	if want := []string{"10", "B", "a", "a-1"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
