package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func noteFile(t *testing.T, e *testkit.Env, rel string) string {
	t.Helper()
	return e.WriteFile(filepath.Join(e.Store, rel), bashShebang+"echo stored\n", 0o755)
}

func TestProjectFiles_RmWithoutLocalNeverReachesAProjectEntry(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	before := testkit.ReadFile(t, filepath.Join(proj, ".shortcuts.local"))
	for _, args := range [][]string{{"rm", "keep", "-y"}, {"rm", "mine", "-y"}} {
		res := e.RunWith(in(proj), args...)
		testkit.Eq(t, strings.Join(args, " "), res.Code, 1)
		testkit.Contains(t, strings.Join(args, " "), res.Stderr, "shortcut '"+args[1]+"' not found.")
	}
	testkit.Eq(t, "local file", testkit.ReadFile(t, filepath.Join(proj, ".shortcuts.local")), before)
}

func TestProjectFiles_RmLocalOfAnOverrideBringsTheSharedEntryBack(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	testkit.Eq(t, "rm", e.RunWith(in(proj), "rm", "--local", "swap", "-y").Code, 0)
	testkit.Eq(t, "run", e.RunWith(in(proj), "r", "-y", ".", "swap").Stdout, "shared swap\n")
}

func TestProjectFiles_RmLocalSaysWhatItRemoves(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	rc, out := e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "--local", "swap")
	testkit.Eq(t, "exit", rc, 1)
	testkit.Contains(t, "removes", out, "Removes your 'swap' from "+filepath.Join(proj, ".shortcuts.local")+".")
}

func TestProjectFiles_RmLocalOfANameOnlyInTheSharedFileFails(t *testing.T) {
	e := testkit.New(t)
	proj := overlaid(t, e)
	res := e.RunWith(in(proj), "rm", "--local", "keep", "-y")
	testkit.Eq(t, "exit", res.Code, 1)
	testkit.Contains(t, "err", res.Stderr, "'keep' isn't in .shortcuts.local, and shortcut doesn't change .shortcuts")
	testkit.Eq(t, "still shared", e.RunWith(in(proj), "r", ".", "keep").Stdout, "shared keep\n")
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

func TestStores_RmSaysWhatItRemoves(t *testing.T) {
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
	rc, out = e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("y\n")}}, "rm", "linked")
	testkit.Eq(t, "accept", rc, 0)
	if !fileExists(source) {
		t.Error("the --script file is gone")
	}
	if _, has := e.LoadGlobal()["linked"]; has {
		t.Error("linked is still registered")
	}
}

func TestStores_RmNamespaceRemovesItWithItsScriptsFolder(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}}))
	e.WriteFile(filepath.Join(imports, "scripts", "dk", "ps.sh"), bashShebang+"echo ps\n", 0o755)
	e.WriteFile(filepath.Join(imports, "scripts", "dk", "lib", "helper"), "x\n", 0)
	e.WriteImport(e.Store, "keep", testkit.NS(testkit.HI))

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

// rm -y that cannot say what it removed removes nothing: stdout on /dev/full exits 1 with the entry still there.
func TestRm_YWithStdoutOnDevFullRemovesNothing(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"keep": testkit.Inline("true")})
	imports := filepath.Join(e.Store, "imports")
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}}))
	e.WriteFile(filepath.Join(imports, "scripts", "dk", "ps.sh"), bashShebang+"echo ps\n", 0o755)
	for _, args := range [][]string{{"rm", "-y", "keep"}, {"rm", "--namespace", "dk", "-y"}} {
		res := e.RunCmd(testkit.Opts{}, append([]string{"sh", "-c", `exec "$@" >/dev/full`, "sh", testkit.Binary(t)}, args...)...)
		if res.Code != 1 {
			t.Errorf("%v: exit %d stderr %q", args, res.Code, res.Stderr)
		}
	}
	if _, has := e.LoadGlobal()["keep"]; !has {
		t.Error("rm -y removed the entry")
	}
	for _, p := range []string{filepath.Join(imports, "dk.shortcut"), filepath.Join(imports, "scripts", "dk", "ps.sh")} {
		if !fileExists(p) {
			t.Errorf("rm --namespace -y removed %s", p)
		}
	}
}

// A question whose prompt cannot be shown is not asked: stdin a terminal, stderr on /dev/full, rm removes nothing.
func TestRm_AQuestionThatCannotBeShownIsNotAsked(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"keep": testkit.Inline("true")})
	rc, out := e.InTerminalCmd(testkit.Opts{}, []testkit.Key{{Wait: "", Send: y("y\n")}}, "sh", "-c", `exec "$@" 2>/dev/full`, "sh", testkit.Binary(t), "rm", "keep")
	if rc != 1 {
		t.Errorf("exit %d\n%s", rc, out)
	}
	if _, has := e.LoadGlobal()["keep"]; !has {
		t.Error("rm removed the entry without asking")
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

func TestRm_NamespaceWithANameIsRefused(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("rm", "--namespace", "podman", "top")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "--namespace takes the namespace alone: shortcut rm --namespace podman")
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

func TestStores_RmFromAProjectNamesWhereTheEntryIs(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"build": testkit.Inline("echo b")}, testkit.Dirs("sub"))
	e.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version":1,"shortcuts":{"mine":{"inline":"echo m"}}}`, 0o644)
	o := testkit.Opts{Cwd: filepath.Join(proj, "sub")}
	res := e.RunWith(o, "rm", "build", "-y")
	testkit.Eq(t, "shared exit", res.Code, 1)
	testkit.Contains(t, "shared", res.Stderr, "shortcut 'build' not found. The project in "+proj+" has it; rm changes only your global store and your .shortcuts.local")
	res = e.RunWith(o, "rm", "mine", "-y")
	testkit.Eq(t, "local exit", res.Code, 1)
	testkit.Contains(t, "local", res.Stderr, "Your .shortcuts.local in "+proj+" has it: shortcut rm --local mine, from "+proj)
}
