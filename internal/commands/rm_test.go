package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

const bashShebang = "#!/usr/bin/env bash\n"

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

func noteFile(t *testing.T, e *testkit.Env, rel string) string {
	t.Helper()
	return e.WriteFile(filepath.Join(e.Store, rel), bashShebang+"echo stored\n", 0o755)
}

func TestStores_RmInteractiveAndForce(t *testing.T) {
	e := testkit.New(t)
	stored := noteFile(t, e, "scripts/stored.sh")
	ext := e.Script("external_keep.sh", bashShebang+"echo ext\n")
	e.WriteStore(e.Store, map[string]any{
		"stored": map[string]any{"script": "scripts/stored.sh", "description": "Stored shortcut"},
		"linked": map[string]any{"script": ext, "description": "Linked shortcut"},
	})

	// 1. Non-tty without -y should fail
	res := e.Run("rm", "stored")
	if res.Code == 0 {
		t.Error("rm without -y or a terminal succeeded")
	}
	testkit.Contains(t, "no -y", res.Stderr, "in a terminal, or pass -y")
	if !fileExists(stored) || e.LoadGlobal()["stored"] == nil {
		t.Error("a refused rm changed something")
	}

	// 2. With -y, the registry entry is deleted and its script stays
	runOK(t, e, testkit.Opts{}, "rm", "stored", "-y")
	if !fileExists(stored) {
		t.Error("rm deleted a script file")
	}
	if _, has := e.LoadGlobal()["stored"]; has {
		t.Error("stored is still registered")
	}

	// 3. Linked file is preserved when shortcut is removed with -y
	runOK(t, e, testkit.Opts{}, "rm", "linked", "-y")
	if _, has := e.LoadGlobal()["linked"]; has {
		t.Error("linked is still registered")
	}
	if !fileExists(ext) {
		t.Error("Linked external script must never be deleted!")
	}
}

func TestStores_RmKeepsAScriptFileThatLivesInTheScriptsFolder(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{})
	mine := noteFile(t, e, "scripts/mine.sh")
	runOK(t, e, testkit.Opts{}, "new", "m", "--script", mine)
	runOK(t, e, testkit.Opts{}, "rm", "m", "-y")
	if !fileExists(mine) {
		t.Error("rm must only delete copies it made")
	}
}

func TestStores_RmMalformedEntry(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"bad_entry": map[string]any{"description": "x", "inline": "true", "cwd": 123}})
	runOK(t, e, testkit.Opts{}, "rm", "bad_entry", "-y")
	if _, has := e.LoadGlobal()["bad_entry"]; has {
		t.Error("bad_entry is still registered")
	}
}

func TestStores_RmSaysWhatItRemovesAndWhatHappensToTheScript(t *testing.T) {
	e := testkit.New(t)
	source := e.Script("src.sh", bashShebang)
	noteFile(t, e, "scripts/copied.sh")
	e.WriteStore(e.Store, map[string]any{
		"copied": map[string]any{"script": "scripts/copied.sh"},
		"linked": map[string]any{"script": source},
	})
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "copied")
	testkit.Eq(t, "decline", rc, 1)
	testkit.Contains(t, "removes", out, "Removes 'copied' from your global shortcuts: "+filepath.Join(e.Store, "shortcuts.json"))
	testkit.Contains(t, "keeps", out, "Keeps its script: "+filepath.Join(e.Store, "scripts", "copied.sh"))
	rc, out = e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("y\n")}}, "rm", "linked")
	testkit.Eq(t, "accept", rc, 0)
	testkit.Contains(t, "keeps", out, "Keeps its script: "+source)
	if !fileExists(source) {
		t.Error("the --script file is gone")
	}
	if _, has := e.LoadGlobal()["linked"]; has {
		t.Error("linked is still registered")
	}
}

func TestStores_RmNamespaceRemovesItWithItsScriptsFolderAndApproval(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}}))
	e.WriteFile(filepath.Join(imports, "scripts", "dk", "ps.sh"), bashShebang+"echo ps\n", 0o755)
	e.WriteFile(filepath.Join(imports, "scripts", "dk", "lib", "helper"), "x\n", 0)
	e.WriteImport(e.Store, "keep", testkit.NS(testkit.HI))
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	approvals := filepath.Join(e.Store, "approvals")
	count := func() int { l, _ := os.ReadDir(approvals); return len(l) }
	testkit.Eq(t, "approvals", count(), 1)

	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "--namespace", "dk")
	testkit.Eq(t, "decline", rc, 1)
	testkit.Contains(t, "removes", out, "Removes namespace 'dk' (1 shortcuts): "+filepath.Join(imports, "dk.shortcut"))
	testkit.Contains(t, "folder", out, "Deletes its scripts folder: "+filepath.Join(imports, "scripts", "dk"))
	testkit.Eq(t, "still runs", e.Run("r", "dk", "ps").Stdout, "ps\n")
	res := e.Run("rm", "--namespace", "dk")
	testkit.Eq(t, "no -y", res.Code, 1)
	testkit.Contains(t, "no -y", res.Stderr, "remove namespace 'dk' in a terminal, or pass -y")
	res = e.Run("rm", "-y", "--namespace", "dk")
	testkit.Eq(t, "rm -y", res.Code, 0)
	testkit.Contains(t, "rm -y", res.Stdout, "Removed namespace 'dk'.")
	testkit.NotExist(t, filepath.Join(imports, "dk.shortcut"))
	testkit.NotExist(t, filepath.Join(imports, "scripts", "dk"))
	testkit.Eq(t, "approvals after", count(), 0)
	if !fileExists(filepath.Join(imports, "keep.shortcut")) {
		t.Error("keep.shortcut is gone")
	}
	res = e.Run("rm", "-y", "--namespace", "dk")
	testkit.Eq(t, "again", res.Code, 1)
	testkit.Contains(t, "again", res.Stderr, "no namespace named 'dk' in your global store ("+imports+")")
	for _, argv := range [][]string{{"rm", "--namespace", "keep", "hi"}, {"rm", "--local", "--namespace", "keep"}} {
		res = e.Run(argv...)
		testkit.Eq(t, strings.Join(argv, " "), res.Code, 1)
		testkit.Contains(t, strings.Join(argv, " "), res.Stderr, "--namespace takes the namespace alone: shortcut rm --namespace keep")
	}
	testkit.Eq(t, "rm alone", e.Run("rm").Code, 2)
}

func TestStores_RmNamespaceImportedFromElsewhereKeepsItsFiles(t *testing.T) {
	e := testkit.New(t)
	elsewhere := e.WriteFile(filepath.Join(e.Tmp, "mine", "tools.shortcut"), docText(t, testkit.NS(testkit.HI)), 0)
	extra := e.WriteFile(filepath.Join(e.Tmp, "mine", "scripts", "tools", "x"), "x\n", 0)
	e.WriteStore(e.Store, map[string]any{}, map[string]any{"imports": map[string]any{"tools": elsewhere}})
	res := runOK(t, e, testkit.Opts{}, "rm", "-y", "--namespace", "tools")
	testkit.Contains(t, "stops", res.Stdout, "Stops importing 'tools' in "+filepath.Join(e.Store, "shortcuts.json"))
	testkit.Contains(t, "keeps", res.Stdout, "Keeps its file: "+elsewhere)
	if !fileExists(elsewhere) || !fileExists(extra) {
		t.Error("an imported namespace's files were removed")
	}
	if _, has := decodeObject(t, testkit.ReadFile(t, filepath.Join(e.Store, "shortcuts.json")))["imports"]; has {
		t.Error("imports is still in shortcuts.json")
	}
}

func TestStores_RmYStillSaysWhatItRemoved(t *testing.T) {
	e := testkit.New(t)
	noteFile(t, e, "scripts/copied.sh")
	e.WriteStore(e.Store, map[string]any{"copied": map[string]any{"script": "scripts/copied.sh"}})
	res := runOK(t, e, testkit.Opts{}, "rm", "-y", "copied")
	testkit.Contains(t, "removes", res.Stdout, "Removes 'copied' from your global shortcuts: "+filepath.Join(e.Store, "shortcuts.json"))
	testkit.Contains(t, "keeps", res.Stdout, "Keeps its script: "+filepath.Join(e.Store, "scripts", "copied.sh"))
	if !fileExists(filepath.Join(e.Store, "scripts", "copied.sh")) {
		t.Error("rm deleted a script file")
	}
	if _, has := e.LoadGlobal()["copied"]; has {
		t.Error("copied is still registered")
	}
}

func TestStores_DecliningRmExits1(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"keep": testkit.Inline("true")})
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "keep")
	testkit.Eq(t, "exit", rc, 1)
	testkit.Contains(t, "out", out, "Aborted.")
	if _, has := e.LoadGlobal()["keep"]; !has {
		t.Error("a declined rm removed it")
	}
}

func TestStores_AnEntryWithBothInlineAndScriptIsRefused(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Store, "both.sh"), bashShebang+"echo script\n", 0)
	e.WriteStore(e.Store, map[string]any{
		"both": map[string]any{"description": "x", "inline": "echo inline", "script": "both.sh"}})
	for _, words := range [][]string{{"r", "both"}, {"show", "both"}} {
		res := e.Run(words...)
		testkit.Eq(t, strings.Join(words, " "), res.Code, 1)
		testkit.Contains(t, strings.Join(words, " "), res.Stderr, "exactly one of 'inline' and 'script'")
		if strings.Contains(res.Stdout, "inline\n") {
			t.Errorf("%v: stdout %q", words, res.Stdout)
		}
	}
	runOK(t, e, testkit.Opts{}, "rm", "both", "-y")
	if _, has := e.LoadGlobal()["both"]; has {
		t.Error("both is still registered")
	}
}

func TestStores_UnknownTopLevelFieldStopsAGlobalShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": testkit.Inline("echo ran")}, map[string]any{"future_safety": true})
	res := e.Run("r", "hi")
	testkit.Eq(t, "run", res.Code, 1)
	testkit.Contains(t, "run", res.Stderr, "unknown field: future_safety")
	if strings.Contains(res.Stdout, "ran") {
		t.Error("it ran")
	}
	runOK(t, e, testkit.Opts{}, "rm", "hi", "-y")
}

func TestStores_NamespaceFlagKeepsEachGroupsScriptsApart(t *testing.T) {
	e := testkit.New(t)
	for _, p := range [][2]string{{"docker", "dock"}, {"kube", "kub"}} {
		source := e.WriteFile(filepath.Join(e.Store, "imports", "scripts", p[0], "delete.sh"), bashShebang+"echo "+p[1]+"\n", 0o755)
		runOK(t, e, testkit.Opts{}, "new", "delete", "--script", source, "--namespace", p[0], "--desc", "d")
		runOK(t, e, testkit.Opts{}, "update", p[0], "-y")
	}
	testkit.Eq(t, "docker", e.Run("r", "docker", "delete").Stdout, "dock\n")
	testkit.Eq(t, "kube", e.Run("r", "kube", "delete").Stdout, "kub\n")

	cp := filepath.Join(e.Store, "imports", "scripts", "docker", "delete.sh")
	testkit.Eq(t, "script_path", e.Run("show", "--field", "script_path", "docker", "delete").Stdout, cp+"\n")
	runOK(t, e, testkit.Opts{}, "rm", "docker", "delete", "-y")
	if !fileExists(cp) || !fileExists(filepath.Join(e.Store, "imports", "scripts", "kube", "delete.sh")) {
		t.Error("rm deleted a script file")
	}
}

func TestStores_CliWritesKeepAnApprovedNamespaceApproved(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "a", "--namespace", "dk", "--", "echo a")
	runOK(t, e, testkit.Opts{}, "update", "dk", "-y")

	res := runOK(t, e, testkit.Opts{}, "new", "ps", "--namespace", "dk", "--", "echo ps")
	if strings.Contains(res.Stderr, "needs approving") {
		t.Errorf("new: %q", res.Stderr)
	}
	testkit.Eq(t, "ps", e.Run("r", "dk", "ps").Stdout, "ps\n")

	runOK(t, e, testkit.Opts{}, "rm", "dk", "ps", "-y")
	testkit.Eq(t, "a", e.Run("r", "dk", "a").Stdout, "a\n")

	source := e.WriteFile(filepath.Join(e.Tmp, "src.sh"), bashShebang+"echo copied\n", 0)
	runOK(t, e, testkit.Opts{}, "new", "cp", "--script", source, "--namespace", "dk", "--desc", "d")
	testkit.Eq(t, "cp", e.Run("r", "dk", "cp").Stdout, "copied\n")

	// A hand edit still needs update, and a later CLI write doesn't approve it on the way
	path := filepath.Join(e.Store, "imports", "dk.shortcut")
	data := decodeObject(t, testkit.ReadFile(t, path))
	data["shortcuts"].(map[string]any)["a"].(map[string]any)["inline"] = "echo edited"
	e.WriteFile(path, docText(t, data), 0)
	testkit.Contains(t, "edited", e.Run("r", "dk", "a").Stderr, "changed since you approved it")
	res = e.Run("new", "more", "--namespace", "dk", "--", "echo more")
	testkit.Contains(t, "more", res.Stderr, "needs approving before it runs: shortcut update dk")
	testkit.Contains(t, "run more", e.Run("r", "dk", "more").Stderr, "changed since you approved it")
	runOK(t, e, testkit.Opts{}, "rm", "dk", "more", "-y")
	testkit.Contains(t, "run a", e.Run("r", "dk", "a").Stderr, "changed since you approved it")
}

func TestStores_RmInABrokenProjectNeverReachesAGlobalOne(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo global")})
	proj := e.Project(map[string]any{"build": testkit.Inline("echo project")}, testkit.Approved())
	e.CorruptStore(filepath.Join(proj, ".shortcuts"))
	res := e.RunWith(in(proj), "rm", "build", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "Inside this project rm only takes --local")
	testkit.Contains(t, "stderr", res.Stderr, "for your own: shortcut rm --global build")
	if _, has := e.LoadGlobal()["build"]; !has {
		t.Error("rm reached the global one")
	}
	runOK(t, e, in(proj), "rm", "--global", "build", "-y")
	if _, has := e.LoadGlobal()["build"]; has {
		t.Error("rm --global left it")
	}
}

func TestStores_LocalRefusesAnywhereInTheGlobalStore(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{})
	for _, folder := range []string{e.Store, filepath.Join(e.Store, "scripts")} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, cmd := range [][]string{{"new", "z", "--local", "--", "true"}, {"rm", "--local", "z", "-y"}} {
			res := e.RunWith(testkit.Opts{Cwd: folder, Timeout: 10e9}, cmd...)
			testkit.Eq(t, folder+" "+strings.Join(cmd, " "), res.Code, 1)
			testkit.Contains(t, strings.Join(cmd, " "), res.Stderr, "global store")
		}
	}
	testkit.Eq(t, "global", len(e.LoadGlobal()), 0)
	testkit.NotExist(t, filepath.Join(e.Store, ".shortcuts.local"))
}

func TestStores_GlobalFlagReachesAHiddenShortcut(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	script := e.WriteFile(filepath.Join(e.Tmp, "tool.sh"), bashShebang, 0)
	runOK(t, e, testkit.Opts{}, "new", "build", "--", "echo global")
	runOK(t, e, in(proj), "new", "build", "--local", "--", "echo project")
	runOK(t, e, testkit.Opts{}, "new", "tool", "--script", script, "--desc", "d")
	runOK(t, e, in(proj), "new", "tool", "--script", script, "--desc", "d", "--local")
	e.Approve(filepath.Join(proj, ".shortcuts"))

	testkit.Contains(t, "show", e.RunWith(in(proj), "show", "build").Stdout, "echo project")
	res := e.RunWith(in(proj), "show", "--global", "build")
	testkit.Contains(t, "show --global", res.Stdout, "Scope:       global\n")
	testkit.Contains(t, "show --global", res.Stdout, "echo global")
	testkit.Eq(t, "script_path", e.RunWith(in(proj), "show", "--global", "--field", "script_path", "tool").Stdout, script+"\n")

	runOK(t, e, in(proj), "rm", "build", "--global", "-y")
	if _, has := e.LoadGlobal()["build"]; has {
		t.Error("build is still global")
	}
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", "build").Stdout, "project\n")
	// The project still has one: every --global command says so
	hint := "The project in " + proj + " has its own 'build': leave out --global to use it."
	for _, args := range [][]string{{"show", "--global", "build"}, {"r", "--global", "build"},
		{"rm", "--global", "-y", "build"}, {"show", "--json", "--global", "build"}} {
		res := e.RunWith(in(proj), args...)
		testkit.Eq(t, strings.Join(args, " "), res.Code, 1)
		testkit.Contains(t, strings.Join(args, " "), res.Stderr, "shortcut 'build' not found.")
		testkit.Contains(t, strings.Join(args, " "), res.Stderr, hint)
	}
	if strings.Contains(e.RunWith(in(proj), "show", "--global", "nothing").Stderr, "has its own") {
		t.Error("a name the project lacks gets the hint")
	}
	if strings.Contains(e.RunWith(in(e.Tmp), "show", "build").Stderr, "has its own") {
		t.Error("outside a project gets the hint")
	}
}

// What rm removes is only what the entry or namespace owns, and never through a link.

func TestRm_NeverFollowsALinkOutOfTheStore(t *testing.T) {
	e := testkit.New(t)
	victim := e.WriteFile(filepath.Join(e.Tmp, "victim.sh"), bashShebang+"echo victim\n", 0o755)
	folder := e.Folder("victim-dir")
	e.WriteFile(filepath.Join(folder, "keep"), "x\n", 0)
	// a script copy that is a link, one that climbs out of scripts/, and a script that is a folder
	e.Symlink(victim, filepath.Join(e.Store, "scripts", "linked.sh"))
	e.WriteFile(filepath.Join(e.Store, "outside.sh"), bashShebang, 0o755)
	e.WriteFile(filepath.Join(e.Store, "scripts", "dir", "inner"), "x\n", 0)
	e.WriteStore(e.Store, map[string]any{
		"linked":  map[string]any{"script": "scripts/linked.sh"},
		"climbs":  map[string]any{"script": "scripts/../outside.sh"},
		"isadir":  map[string]any{"script": "scripts/dir"},
		"missing": map[string]any{"script": "scripts/none.sh"},
	})
	for _, name := range []string{"linked", "climbs", "isadir", "missing"} {
		runOK(t, e, testkit.Opts{}, "rm", name, "-y")
	}
	if len(e.LoadGlobal()) != 0 {
		t.Errorf("entries left: %v", e.LoadGlobal())
	}
	for _, p := range []string{victim, filepath.Join(e.Store, "outside.sh"), filepath.Join(e.Store, "scripts", "dir", "inner")} {
		if !fileExists(p) {
			t.Errorf("%s was deleted", p)
		}
	}
}

func TestRm_NamespaceWithALinkedScriptsFolderLosesOnlyTheLink(t *testing.T) {
	e := testkit.New(t)
	other := e.Folder("other")
	e.WriteFile(filepath.Join(other, "ps.sh"), bashShebang, 0o755)
	e.WriteImport(e.Store, "dk", testkit.NS(testkit.HI))
	e.Symlink(other, filepath.Join(e.Store, "imports", "scripts", "dk"))
	e.WriteFile(filepath.Join(e.Store, "imports", "scripts", "keep", "x"), "x\n", 0)
	runOK(t, e, testkit.Opts{}, "rm", "--namespace", "dk", "-y")
	testkit.NotExist(t, filepath.Join(e.Store, "imports", "dk.shortcut"))
	testkit.NotExist(t, filepath.Join(e.Store, "imports", "scripts", "dk"))
	if !fileExists(filepath.Join(other, "ps.sh")) || !fileExists(filepath.Join(e.Store, "imports", "scripts", "keep", "x")) {
		t.Error("a file outside the namespace was deleted")
	}
}

func TestRm_NamespaceWithANameSaysHowToRemoveOneEntry(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("rm", "--namespace", "podman", "top")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "--namespace takes the namespace alone: shortcut rm --namespace podman. To remove one entry: shortcut rm podman top")
}

func TestRm_EmptyNamespaceIsAnErrorAndRemovesNothing(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "ps", "--namespace", "dk", "--", "echo ps")
	before := treeOf(t, e.Tmp)
	res := e.Run("rm", "--namespace", "", "-y")
	if res.Code != 1 || res.Stdout != "" || res.Stderr == "" {
		t.Errorf("exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if !reflect.DeepEqual(treeOf(t, e.Tmp), before) {
		t.Error("rm --namespace '' changed the store")
	}
}
