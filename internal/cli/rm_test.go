package cli

import (
	"os"
	"path/filepath"
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

// py: test_stores.py::test_rm_interactive_and_force
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
	contains(t, "no -y", res.Stderr, "in a terminal, or pass -y")
	if !fileExists(stored) || e.LoadGlobal()["stored"] == nil {
		t.Error("a refused rm changed something")
	}

	// 2. With -y, stored copy and registry entry are deleted
	runOK(t, e, testkit.Opts{}, "rm", "stored", "-y")
	notExist(t, stored)
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

// py: test_stores.py::test_rm_keeps_a_script_file_that_lives_in_the_scripts_folder
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

// py: test_stores.py::test_rm_malformed_entry
func TestStores_RmMalformedEntry(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"bad_entry": map[string]any{"description": "x", "inline": "true", "cwd": 123}})
	runOK(t, e, testkit.Opts{}, "rm", "bad_entry", "-y")
	if _, has := e.LoadGlobal()["bad_entry"]; has {
		t.Error("bad_entry is still registered")
	}
}

// py: test_stores.py::test_rm_says_what_it_removes_and_what_happens_to_the_script
func TestStores_RmSaysWhatItRemovesAndWhatHappensToTheScript(t *testing.T) {
	e := testkit.New(t)
	source := e.Script("src.sh", bashShebang)
	noteFile(t, e, "scripts/copied.sh")
	e.WriteStore(e.Store, map[string]any{
		"copied": map[string]any{"script": "scripts/copied.sh"},
		"linked": map[string]any{"script": source},
	})
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "copied")
	eq(t, "decline", rc, 1)
	contains(t, "removes", out, "Removes 'copied' from your global shortcuts: "+filepath.Join(e.Store, "shortcuts.json"))
	contains(t, "copy", out, "Deletes its script copy: "+filepath.Join(e.Store, "scripts", "copied.sh"))
	rc, out = e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("y\n")}}, "rm", "linked")
	eq(t, "accept", rc, 0)
	contains(t, "keeps", out, "Keeps its script: "+source)
	if !fileExists(source) {
		t.Error("the --script file is gone")
	}
	if _, has := e.LoadGlobal()["linked"]; has {
		t.Error("linked is still registered")
	}
}

// py: test_stores.py::test_rm_namespace_removes_it_with_its_scripts_folder_and_approval
func TestStores_RmNamespaceRemovesItWithItsScriptsFolderAndApproval(t *testing.T) {
	e := testkit.New(t)
	imports := filepath.Join(e.Store, "imports")
	e.WriteImport(e.Store, "dk", map[string]any{"version": 2, "shortcuts": map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}}})
	e.WriteFile(filepath.Join(imports, "scripts", "dk", "ps.sh"), bashShebang+"echo ps\n", 0o755)
	e.WriteFile(filepath.Join(imports, "scripts", "dk", "lib", "helper"), "x\n", 0)
	e.WriteImport(e.Store, "keep", map[string]any{"version": 2, "shortcuts": testkit.HI})
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	approvals := filepath.Join(e.Store, "approvals")
	count := func() int { l, _ := os.ReadDir(approvals); return len(l) }
	eq(t, "approvals", count(), 1)

	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "--namespace", "dk")
	eq(t, "decline", rc, 1)
	contains(t, "removes", out, "Removes namespace 'dk' (1 shortcuts): "+filepath.Join(imports, "dk.shortcut"))
	contains(t, "folder", out, "Deletes its scripts folder: "+filepath.Join(imports, "scripts", "dk"))
	eq(t, "still runs", e.Run("r", "dk", "ps").Stdout, "ps\n")
	res := e.Run("rm", "--namespace", "dk")
	eq(t, "no -y", res.Code, 1)
	contains(t, "no -y", res.Stderr, "remove namespace 'dk' in a terminal, or pass -y")
	res = e.Run("rm", "-y", "--namespace", "dk")
	eq(t, "rm -y", res.Code, 0)
	contains(t, "rm -y", res.Stdout, "Removed namespace 'dk'.")
	notExist(t, filepath.Join(imports, "dk.shortcut"))
	notExist(t, filepath.Join(imports, "scripts", "dk"))
	eq(t, "approvals after", count(), 0)
	if !fileExists(filepath.Join(imports, "keep.shortcut")) {
		t.Error("keep.shortcut is gone")
	}
	res = e.Run("rm", "-y", "--namespace", "dk")
	eq(t, "again", res.Code, 1)
	contains(t, "again", res.Stderr, "no namespace named 'dk' in your global store ("+imports+")")
	for _, argv := range [][]string{{"rm", "--namespace", "keep", "hi"}, {"rm", "--local", "--namespace", "keep"}} {
		res = e.Run(argv...)
		eq(t, strings.Join(argv, " "), res.Code, 1)
		contains(t, strings.Join(argv, " "), res.Stderr, "--namespace takes the namespace alone: shortcut rm --namespace keep")
	}
	eq(t, "rm alone", e.Run("rm").Code, 2)
}

// py: test_stores.py::test_rm_namespace_imported_from_elsewhere_keeps_its_files
func TestStores_RmNamespaceImportedFromElsewhereKeepsItsFiles(t *testing.T) {
	e := testkit.New(t)
	elsewhere := e.WriteFile(filepath.Join(e.Tmp, "mine", "tools.shortcut"), docText(t, map[string]any{"version": 2, "shortcuts": testkit.HI}), 0)
	extra := e.WriteFile(filepath.Join(e.Tmp, "mine", "scripts", "tools", "x"), "x\n", 0)
	e.WriteStore(e.Store, map[string]any{}, map[string]any{"imports": map[string]any{"tools": elsewhere}})
	res := runOK(t, e, testkit.Opts{}, "rm", "-y", "--namespace", "tools")
	contains(t, "stops", res.Stdout, "Stops importing 'tools' in "+filepath.Join(e.Store, "shortcuts.json"))
	contains(t, "keeps", res.Stdout, "Keeps its file: "+elsewhere)
	if !fileExists(elsewhere) || !fileExists(extra) {
		t.Error("an imported namespace's files were removed")
	}
	if _, has := decodeObject(t, readFile(t, filepath.Join(e.Store, "shortcuts.json")))["imports"]; has {
		t.Error("imports is still in shortcuts.json")
	}
}

// py: test_stores.py::test_rm_y_still_says_what_it_removed
func TestStores_RmYStillSaysWhatItRemoved(t *testing.T) {
	e := testkit.New(t)
	noteFile(t, e, "scripts/copied.sh")
	e.WriteStore(e.Store, map[string]any{"copied": map[string]any{"script": "scripts/copied.sh"}})
	res := runOK(t, e, testkit.Opts{}, "rm", "-y", "copied")
	contains(t, "removes", res.Stdout, "Removes 'copied' from your global shortcuts: "+filepath.Join(e.Store, "shortcuts.json"))
	contains(t, "copy", res.Stdout, "Deletes its script copy: "+filepath.Join(e.Store, "scripts", "copied.sh"))
	if _, has := e.LoadGlobal()["copied"]; has {
		t.Error("copied is still registered")
	}
}

// py: test_stores.py::test_declining_rm_exits_1
func TestStores_DecliningRmExits1(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"keep": inline("true")})
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "[y/N]", Send: y("n\n")}}, "rm", "keep")
	eq(t, "exit", rc, 1)
	contains(t, "out", out, "Aborted.")
	if _, has := e.LoadGlobal()["keep"]; !has {
		t.Error("a declined rm removed it")
	}
}

// py: test_stores.py::test_an_entry_with_both_inline_and_script_is_refused
func TestStores_AnEntryWithBothInlineAndScriptIsRefused(t *testing.T) {
	e := testkit.New(t)
	e.WriteFile(filepath.Join(e.Store, "both.sh"), bashShebang+"echo script\n", 0)
	e.WriteRegistry(e.Store, map[string]any{"version": 2, "shortcuts": map[string]any{
		"both": map[string]any{"description": "x", "inline": "echo inline", "script": "both.sh"}}})
	for _, words := range [][]string{{"r", "both"}, {"show", "both"}, {"location", "both"}} {
		res := e.Run(words...)
		eq(t, strings.Join(words, " "), res.Code, 1)
		contains(t, strings.Join(words, " "), res.Stderr, "exactly one of 'inline' and 'script'")
		if strings.Contains(res.Stdout, "inline\n") {
			t.Errorf("%v: stdout %q", words, res.Stdout)
		}
	}
	runOK(t, e, testkit.Opts{}, "rm", "both", "-y")
	if _, has := e.LoadGlobal()["both"]; has {
		t.Error("both is still registered")
	}
}

// py: test_stores.py::test_unknown_top_level_field_stops_a_global_shortcut
func TestStores_UnknownTopLevelFieldStopsAGlobalShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": inline("echo ran")}, map[string]any{"future_safety": true})
	res := e.Run("r", "hi")
	eq(t, "run", res.Code, 1)
	contains(t, "run", res.Stderr, "unknown field: future_safety")
	if strings.Contains(res.Stdout, "ran") {
		t.Error("it ran")
	}
	runOK(t, e, testkit.Opts{}, "rm", "hi", "-y")
}

// py: test_stores.py::test_namespace_flag_keeps_each_groups_scripts_apart
func TestStores_NamespaceFlagKeepsEachGroupsScriptsApart(t *testing.T) {
	e := testkit.New(t)
	source := filepath.Join(e.Tmp, "src.sh")
	for _, p := range [][2]string{{"docker", "dock"}, {"kube", "kub"}} {
		e.WriteFile(source, bashShebang+"echo "+p[1]+"\n", 0)
		runOK(t, e, testkit.Opts{}, "new", "delete", "--copy", source, "--namespace", p[0], "--desc", "d")
		runOK(t, e, testkit.Opts{}, "update", p[0], "-y")
	}
	eq(t, "docker", e.Run("r", "docker", "delete").Stdout, "dock\n")
	eq(t, "kube", e.Run("r", "kube", "delete").Stdout, "kub\n")

	cp := filepath.Join(e.Store, "imports", "scripts", "docker", "delete.sh")
	eq(t, "location", e.Run("location", "docker", "delete").Stdout, cp+"\n")
	runOK(t, e, testkit.Opts{}, "rm", "docker", "delete", "-y")
	notExist(t, cp)
	if !fileExists(filepath.Join(e.Store, "imports", "scripts", "kube", "delete.sh")) {
		t.Error("kube's copy is gone")
	}
}

// py: test_stores.py::test_cli_writes_keep_an_approved_namespace_approved
func TestStores_CliWritesKeepAnApprovedNamespaceApproved(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "a", "--namespace", "dk", "--", "echo a")
	runOK(t, e, testkit.Opts{}, "update", "dk", "-y")

	res := runOK(t, e, testkit.Opts{}, "new", "ps", "--namespace", "dk", "--", "echo ps")
	if strings.Contains(res.Stderr, "needs approving") {
		t.Errorf("new: %q", res.Stderr)
	}
	eq(t, "ps", e.Run("r", "dk", "ps").Stdout, "ps\n")

	runOK(t, e, testkit.Opts{}, "rm", "dk", "ps", "-y")
	eq(t, "a", e.Run("r", "dk", "a").Stdout, "a\n")

	source := e.WriteFile(filepath.Join(e.Tmp, "src.sh"), bashShebang+"echo copied\n", 0)
	runOK(t, e, testkit.Opts{}, "new", "cp", "--copy", source, "--namespace", "dk", "--desc", "d")
	eq(t, "cp", e.Run("r", "dk", "cp").Stdout, "copied\n")

	// A hand edit still needs update, and a later CLI write doesn't approve it on the way
	path := filepath.Join(e.Store, "imports", "dk.shortcut")
	data := decodeObject(t, readFile(t, path))
	data["shortcuts"].(map[string]any)["a"].(map[string]any)["inline"] = "echo edited"
	e.WriteFile(path, docText(t, data), 0)
	contains(t, "edited", e.Run("r", "dk", "a").Stderr, "changed since you approved it")
	res = e.Run("new", "more", "--namespace", "dk", "--", "echo more")
	contains(t, "more", res.Stderr, "needs approving before it runs: shortcut update dk")
	contains(t, "run more", e.Run("r", "dk", "more").Stderr, "changed since you approved it")
	runOK(t, e, testkit.Opts{}, "rm", "dk", "more", "-y")
	contains(t, "run a", e.Run("r", "dk", "a").Stderr, "changed since you approved it")
}

// py: test_stores.py::test_rm_in_a_broken_project_never_reaches_a_global_one
func TestStores_RmInABrokenProjectNeverReachesAGlobalOne(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": inline("echo global")})
	proj := e.Project(map[string]any{"build": inline("echo project")}, testkit.Approved())
	e.CorruptStore(filepath.Join(proj, ".shortcuts"))
	res := e.RunWith(in(proj), "rm", "build", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "Inside this project rm only takes --local")
	contains(t, "stderr", res.Stderr, "for your own: shortcut rm --global build")
	if _, has := e.LoadGlobal()["build"]; !has {
		t.Error("rm reached the global one")
	}
	runOK(t, e, in(proj), "rm", "--global", "build", "-y")
	if _, has := e.LoadGlobal()["build"]; has {
		t.Error("rm --global left it")
	}
}

// py: test_stores.py::test_rm_local_hides_a_name_when_the_project_file_cant_be_read
func TestStores_RmLocalHidesANameWhenTheProjectFileCantBeRead(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"build": inline("echo project")}, testkit.Approved())
	registry := filepath.Join(proj, ".shortcuts")
	e.CorruptStore(registry)
	broken := readFile(t, registry)
	rc, out := e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Send: y("y\n")}}, "rm", "--local", "build")
	eq(t, "exit", rc, 0)
	contains(t, "out", out, ".shortcuts stays as it is.")
	contains(t, "out", out, `Hides 'build' from you: writes "build": null to`)
	local := localData(t, proj)
	if v, has := local["build"]; !has || v != nil || len(local) != 1 {
		t.Errorf("local = %v", local)
	}
	eq(t, "shared untouched", readFile(t, registry), broken)
}

// py: test_stores.py::test_local_refuses_anywhere_in_the_global_store
func TestStores_LocalRefusesAnywhereInTheGlobalStore(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{})
	for _, folder := range []string{e.Store, filepath.Join(e.Store, "scripts")} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, cmd := range [][]string{{"new", "z", "--local", "--", "true"}, {"rm", "--local", "z", "-y"}} {
			res := e.RunWith(testkit.Opts{Cwd: folder, Timeout: 10e9}, cmd...)
			eq(t, folder+" "+strings.Join(cmd, " "), res.Code, 1)
			contains(t, strings.Join(cmd, " "), res.Stderr, "global store")
		}
	}
	eq(t, "global", len(e.LoadGlobal()), 0)
	notExist(t, filepath.Join(e.Store, ".shortcuts.local"))
}

// py: test_stores.py::test_global_flag_reaches_a_hidden_shortcut
func TestStores_GlobalFlagReachesAHiddenShortcut(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{})
	script := e.WriteFile(filepath.Join(e.Tmp, "tool.sh"), bashShebang, 0)
	runOK(t, e, testkit.Opts{}, "new", "build", "--", "echo global")
	runOK(t, e, in(proj), "new", "build", "--local", "--", "echo project")
	runOK(t, e, testkit.Opts{}, "new", "tool", "--copy", script, "--desc", "d")
	runOK(t, e, in(proj), "new", "tool", "--script", script, "--desc", "d", "--local")
	e.Approve(filepath.Join(proj, ".shortcuts"))

	contains(t, "show", e.RunWith(in(proj), "show", "build").Stdout, "echo project")
	res := e.RunWith(in(proj), "show", "--global", "build")
	contains(t, "show --global", res.Stdout, "Scope:       global\n")
	contains(t, "show --global", res.Stdout, "echo global")
	eq(t, "location", e.RunWith(in(proj), "location", "--global", "tool").Stdout, filepath.Join(e.Store, "scripts", "tool.sh")+"\n")

	runOK(t, e, in(proj), "rm", "build", "--global", "-y")
	if _, has := e.LoadGlobal()["build"]; has {
		t.Error("build is still global")
	}
	eq(t, "run", e.RunWith(in(proj), "r", "build").Stdout, "project\n")
	// The project still has one: every --global command says so
	hint := "The project in " + proj + " has its own 'build': leave out --global to use it."
	for _, args := range [][]string{{"show", "--global", "build"}, {"r", "--global", "build"}, {"location", "--global", "build"},
		{"rm", "--global", "-y", "build"}, {"show", "--json", "--global", "build"}} {
		res := e.RunWith(in(proj), args...)
		eq(t, strings.Join(args, " "), res.Code, 1)
		contains(t, strings.Join(args, " "), res.Stderr, "shortcut 'build' not found.")
		contains(t, strings.Join(args, " "), res.Stderr, hint)
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
	e.WriteImport(e.Store, "dk", map[string]any{"version": 2, "shortcuts": testkit.HI})
	e.Symlink(other, filepath.Join(e.Store, "imports", "scripts", "dk"))
	e.WriteFile(filepath.Join(e.Store, "imports", "scripts", "keep", "x"), "x\n", 0)
	runOK(t, e, testkit.Opts{}, "rm", "--namespace", "dk", "-y")
	notExist(t, filepath.Join(e.Store, "imports", "dk.shortcut"))
	notExist(t, filepath.Join(e.Store, "imports", "scripts", "dk"))
	if !fileExists(filepath.Join(other, "ps.sh")) || !fileExists(filepath.Join(e.Store, "imports", "scripts", "keep", "x")) {
		t.Error("a file outside the namespace was deleted")
	}
}

func TestRm_NamespaceWithANameSaysHowToRemoveOneEntry(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("rm", "--namespace", "podman", "top")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "--namespace takes the namespace alone: shortcut rm --namespace podman. To remove one entry: shortcut rm podman top")
}
