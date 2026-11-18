package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestRunning_NewSaysWhenACommandIgnoresArguments(t *testing.T) {
	e := testkit.New(t)
	const hint = "won't see any arguments"
	testkit.Contains(t, "a", e.Run("new", "a", "--", "rg --hidden").Stderr, hint)
	for _, c := range [][2]string{{"c", `rg "$@"`}, {"d", "echo $1"}, {"e", "echo ${2}"}, {"f", "echo $*"}} {
		res := runOK(t, e, testkit.Opts{}, "new", c[0], "--", c[1])
		if strings.Contains(res.Stderr, hint) {
			t.Errorf("%s: unexpected hint in %q", c[1], res.Stderr)
		}
	}
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

	// 2. --local creates .shortcuts.local on demand
	runOK(t, e, in(proj), "new", "local_cmd", "--local", "--", "echo local")
	if _, err := os.Stat(filepath.Join(proj, ".shortcuts.local")); err != nil {
		t.Errorf("no .shortcuts.local: %v", err)
	}
	testkit.NotExist(t, filepath.Join(proj, ".shortcuts"))
	testkit.Eq(t, "run", e.RunWith(in(proj), "run", ".", "local_cmd").Stdout, "local\n")

	// 3. --cwd is stored against the defining file: absolute globally, relative inside the project
	runOK(t, e, testkit.Opts{}, "new", "fixed", "--cwd", e.Tmp, "--", "pwd")
	testkit.Eq(t, "fixed cwd", e.LoadGlobal()["fixed"].(map[string]any)["cwd"], e.Tmp)
	runOK(t, e, in(proj), "new", "in_sub", "--local", "--cwd", "sub", "--", "pwd")
	testkit.Eq(t, "in_sub cwd", fileShortcuts(t, filepath.Join(proj, ".shortcuts.local"))["in_sub"].(map[string]any)["cwd"], "sub")
	e.Run("new", "explicit", "--cwd", "caller", "--", "pwd")
	testkit.Eq(t, "explicit cwd", e.LoadGlobal()["explicit"].(map[string]any)["cwd"], "caller")
}

// Each refusal exits as shown, says why, and writes nothing.
func TestNew_Refusals(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	dup := e.Project(nil, testkit.Name("dup"))
	runOK(t, e, in(dup), "new", "taken", "--local", "--", "true")
	plain := e.WriteFile(filepath.Join(e.Tmp, "plain.sh"), "#!/bin/sh\n", 0o644)
	exec := e.Script("tool.sh", bashShebang)
	for _, c := range []struct {
		name string
		cwd  string
		args []string
		code int
		msg  string
	}{
		{"not executable", "", []string{"x", "--script", plain}, 1, "error: " + plain + " is not executable. Make it so: chmod +x " + plain},
		{"script and command", proj, []string{"x", "--script", exec, "--", "true"}, 1, "use one of"},
		{"empty script and command", proj, []string{"x", "--script", "", "--", "true"}, 1, "needs a file name"},
		{"empty script", proj, []string{"x", "--script", ""}, 1, "needs a file name"},
		{"several words", "", []string{"hello", "--", "echo", "hello", "world"}, 2, "error: put the command after -- in single quotes, as one word: -- 'echo hello world'\n"},
		{"namespace with local", proj, []string{"where", "--namespace", "dev", "--local", "--", "pwd"}, 1, "--namespace can't be used with --local"},
		{"local without a command", proj, []string{"x", "--local", "--desc", "d"}, 2, "after '--'"},
		{"empty desc", "", []string{"plain", "--desc", ""}, 1, "error: --desc can't be empty\n"},
		{"empty desc=", "", []string{"x", "--desc=", "--", "true"}, 1, "error: --desc can't be empty\n"},
		{"empty desc with script", "", []string{"x", "--script", exec, "--desc", ""}, 1, "error: --desc can't be empty\n"},
		{"empty namespace", "", []string{"x", "--namespace", "", "--", "true"}, 1, "error: --namespace can't be empty\n"},
		{"empty namespace=", "", []string{"x", "--namespace=", "--desc", "d"}, 1, "error: --namespace can't be empty\n"},
		{"cwd that is not there", "", []string{"nowhere", "--cwd", "/nonexistent/path/xyz", "--", "echo 1"}, 1, "--cwd directory does not exist"},
		{"duplicate name", dup, []string{"taken", "--local", "--", "echo duplicate"}, 1, "already exists in"},
	} {
		res := e.RunWith(in(c.cwd), append([]string{"new"}, c.args...)...)
		if res.Code != c.code || res.Stdout != "" || !strings.Contains(res.Stderr, c.msg) {
			t.Errorf("%s: exit %d stdout %q stderr %q; want exit %d stderr with %q", c.name, res.Code, res.Stdout, res.Stderr, c.code, c.msg)
		}
	}
	testkit.NotExist(t, filepath.Join(proj, ".shortcuts.local"))
	testkit.NotExist(t, filepath.Join(e.Store, "shortcuts.json"))
	testkit.Eq(t, "taken is still the one entry", fileShortcuts(t, filepath.Join(dup, ".shortcuts.local"))["taken"].(map[string]any)["inline"], "true")
}

func TestStores_NewScriptInsideTheStoreIsKeptRelative(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	inside := e.WriteFile(filepath.Join(imports, "dk", "ps.sh"), bashShebang+"echo ps\n", 0o755)
	kept := e.WriteFile(filepath.Join(imports, "scripts", "dk", "kept.sh"), bashShebang, 0o755)
	outside := e.Script("out.sh", bashShebang+"echo out\n")
	mine := e.WriteFile(filepath.Join(e.Store, "tools", "mine.sh"), bashShebang+"echo mine\n", 0o755)
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
	testkit.Eq(t, "run dk ps", e.Run("r", "dk", "ps").Stdout, "ps\n")
}

func TestStores_NamespaceFlagCreatesANamespace(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "delete", "--namespace", "docker", "--desc", "Remove one", "--", `echo "rm $1"`)
	doc := decodeObject(t, testkit.ReadFile(t, filepath.Join(e.Store, "imports", "docker.shortcut")))
	testkit.Eq(t, "version", doc["version"], float64(1))
	testkit.Eq(t, "inline", doc["shortcuts"].(map[string]any)["delete"].(map[string]any)["inline"], `echo "rm $1"`)

	testkit.NotExist(t, filepath.Join(e.Store, "shortcuts.json")) // only the namespace's own file is made
	testkit.Eq(t, "run", e.Run("r", "docker", "delete", "x").Stdout, "rm x\n")

	testkit.Eq(t, "second", e.Run("new", "ps", "--namespace", "docker", "--", "echo ps").Code, 0)
	res := e.Run("new", "ps", "--namespace", "docker", "--", "echo again")
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
	proj := e.Project(map[string]any{"up": testkit.Inline("echo up")}, testkit.Dirs("sub"))
	registry := filepath.Join(proj, ".shortcuts")
	before := testkit.ReadFile(t, registry)
	source := e.Script("tool.sh", bashShebang)
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
	testkit.Eq(t, "up", e.RunWith(in(sub), "r", ".", "up").Stdout, "up\n")
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

// `--cwd link/..` is the parent of where the link leads, not the folder holding the link.
func TestStores_CwdThroughALinkThenDotDotIsPhysical(t *testing.T) {
	e := testkit.New(t)
	real := filepath.Join(e.Tmp, "a", "b", "c")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	e.Symlink(real, filepath.Join(e.Tmp, "link"))
	runOK(t, e, testkit.Opts{}, "new", "up", "--cwd", e.Tmp+"/link/..", "--", "pwd") // not filepath.Join: it would clean the ".." away
	testkit.Eq(t, "cwd", e.LoadGlobal()["up"].(map[string]any)["cwd"], realpath(t, filepath.Join(e.Tmp, "a", "b")))
}

// An external script is linked, not copied; with no --desc there is no description. A script given as a link stays
// that link: the stored path is the one given, not where it leads.
func TestStores_NewScriptKeepsTheLinkItWasGiven(t *testing.T) {
	e := testkit.New(t)
	ext := e.Script("external.sh", bashShebang+"echo external\n")
	runOK(t, e, testkit.Opts{}, "new", "ext", "--script", ext, "--desc", "Linked script")
	testkit.NotExist(t, filepath.Join(e.Store, "scripts", "ext.sh"))
	testkit.Eq(t, "ext", fileShortcuts(t, filepath.Join(e.Store, "shortcuts.json"))["ext"].(map[string]any)["script"], realpath(t, ext))
	runOK(t, e, testkit.Opts{}, "new", "dflt", "--script", ext)
	testkit.Eq(t, "no desc", e.LoadGlobal()["dflt"].(map[string]any)["description"], nil)
	target := e.Script("target.sh", "#!/bin/sh\necho t\n")
	link := filepath.Join(e.Tmp, "link.sh")
	e.Symlink(target, link)
	runOK(t, e, testkit.Opts{}, "new", "linked", "--script", link)
	testkit.Eq(t, "script", fileShortcuts(t, filepath.Join(e.Store, "shortcuts.json"))["linked"].(map[string]any)["script"], link)
}

func TestProjectFiles_NewLocalOverridesASharedName(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	runOK(t, e, in(proj), "new", "keep", "--local", "--", "echo mine now")
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", ".", "keep").Stdout, "mine now\n")
}

func TestProjectFiles_NewLocalTakesACommandAndAddRecordsThePath(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{}, testkit.Files(map[string]string{"tools/run.sh": bashShebang}), testkit.Mode(0o755))
	outside := e.Script("outside.sh", bashShebang)
	testkit.Eq(t, "in", e.RunWith(in(proj), "new", "in", "--script", "tools/run.sh", "--desc", "d", "--local").Code, 0)
	testkit.Eq(t, "out", e.RunWith(in(proj), "new", "out", "--script", outside, "--desc", "d", "--local").Code, 0)
	testkit.Eq(t, "in script", localData(t, proj)["in"].(map[string]any)["script"], "tools/run.sh")
	testkit.Eq(t, "out script", localData(t, proj)["out"].(map[string]any)["script"], outside)
	var names []string
	entries, _ := os.ReadDir(proj)
	for _, en := range entries {
		names = append(names, en.Name())
	}
	slices.Sort(names)
	testkit.Eq(t, "files", names, []string{".shortcuts", ".shortcuts.local", "tools"})
}

func TestProjectFiles_TheCliNeverWritesTheSharedFile(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	before := testkit.ReadFile(t, filepath.Join(proj, ".shortcuts"))
	source := e.Script("tool.sh", bashShebang)
	for _, args := range [][]string{
		{"new", "a", "--local", "--", "echo a"},
		{"new", "b", "--script", source, "--desc", "d", "--local"},
		{"rm", "--local", "swap", "-y"},
	} {
		testkit.Eq(t, strings.Join(args, " "), e.RunWith(in(proj), args...).Code, 0)
	}
	testkit.Eq(t, "shared", testkit.ReadFile(t, filepath.Join(proj, ".shortcuts")), before)
}

func TestSymlinkSafety_LocalNeverWritesThroughACommittedSymlink(t *testing.T) {
	e := testkit.New(t)
	victim := e.WriteFile(filepath.Join(e.Tmp, "victim"), "keep\n", 0)
	proj := e.Project(map[string]any{})
	e.Symlink(victim, filepath.Join(proj, ".shortcuts.local"))
	res := e.RunWith(in(proj), "new", "build", "--local", "--", "true")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "is a symlink")
	testkit.Eq(t, "victim", testkit.ReadFile(t, victim), "keep\n")
}
