// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-8). Don't hand-edit without checking the plan.

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

var templatesDir, _ = filepath.Abs("../../templates")

// installBundled is Python's install_bundled, through --bundled: the templates imported, with a `shortcut` on PATH.
func installBundled(t *testing.T, e *testkit.Env) {
	t.Helper()
	e.Shell(testkit.Opts{}, "true") // puts the `shortcut` link in ToolBin
	e.Vars["PATH"] = e.ToolBin + ":" + e.Vars["PATH"]
	res := runOK(t, e, testkit.Opts{}, "init", "--global", "--bundled", "-y")
	notContains(t, "stdout", res.Stdout, "Skipped")
}

func needPython(t *testing.T) {
	t.Helper()
	if testkit.New(t).RunCmd(testkit.Opts{}, "sh", "-c", "command -v python3").Code != 0 {
		t.Skip("python3 is not installed")
	}
}

// treeOf is every file under root: its path, mode and bytes.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range filesUnder(t, root) {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(filepath.Join(root, rel))
		out[rel] = info.Mode().String() + "\n" + string(data)
	}
	return out
}

// py: test_bundled.py::test_bundled_templates
func TestBundled_Templates(t *testing.T) {
	e := testkit.New(t)
	search := filepath.Join(templatesDir, "scripts", "search.sh")
	if _, err := os.Stat(filepath.Join(templatesDir, ".shortcuts")); err != nil {
		t.Fatal(err)
	}
	// search must print usage and exit 1 when given no pattern
	res := e.RunCmd(testkit.Opts{}, search)
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "usage:")
	// search finds pattern when given
	e.WriteFile(filepath.Join(e.Tmp, "target_search_me.txt"), "hello", 0)
	res = e.RunCmd(testkit.Opts{}, search, "search_me", e.Tmp)
	eq(t, "exit", res.Code, 0)
	contains(t, "stdout", res.Stdout, "target_search_me.txt")

	res = runOK(t, e, testkit.Opts{}, "init", "--global", templatesDir, "-y")
	contains(t, "stdout", res.Stdout, "search")
	notContains(t, "stdout", res.Stdout, "Skipped")
	for _, name := range []string{"edit", "nano", "logged", "desktop"} {
		if _, ok := e.LoadGlobal()[name]; !ok {
			t.Errorf("%s was not imported", name)
		}
	}
}

// py: test_bundled.py::test_bundled_nano_logged_and_desktop
func TestBundled_NanoLoggedAndDesktop(t *testing.T) {
	needPython(t)
	e := testkit.New(t)
	installBundled(t, e)
	runOK(t, e, testkit.Opts{}, "new", "job", "--desc", "A job", "--", "echo job output")
	runOK(t, e, testkit.Opts{}, "new", "failing", "--", "echo out; echo err >&2; exit 3")

	// logged is a plain wrapper: the output reaches you and the file, with the shortcut's exit code
	state := filepath.Join(e.Tmp, "state")
	res := e.RunWith(testkit.Opts{Env: map[string]string{"XDG_STATE_HOME": state}}, "r", "logged", "failing")
	eq(t, "exit", res.Code, 3)
	contains(t, "stdout", res.Stdout, "out\n")
	raw, err := os.ReadFile(filepath.Join(state, "shortcut", "failing.log"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "log", string(raw), "out\n")
	contains(t, "log", string(raw), "err\n")

	// nano wins over whatever editor is configured, and finds what a fixed folder couldn't
	e.WriteFile(filepath.Join(e.ToolBin, "nano"), "#!/bin/sh\necho \"nano opened $1\"\n", 0o755)
	res = e.RunWith(testkit.Opts{Env: map[string]string{"VISUAL": "false", "EDITOR": "false"}}, "r", "nano", "job")
	eq(t, "exit", res.Code, 0)
	contains(t, "stdout", res.Stdout, "nano opened")

	res = e.Run("r", "desktop")
	eq(t, "exit", res.Code, 2)
	contains(t, "stderr", res.Stderr, "usage: shortcut r desktop <shortcut...>")
}

// py: test_bundled.py::test_bundled_desktop_entry_quoting
func TestBundled_DesktopEntryQuoting(t *testing.T) {
	needPython(t)
	e := testkit.New(t)
	installBundled(t, e)
	runOK(t, e, testkit.Opts{}, "new", "desk_test", "--desc", "Run 100% \"special\" `app` with $VAR", "--", "echo", "desktop_ok")
	res := runOK(t, e, testkit.Opts{}, "r", "desktop", "desk_test")
	contains(t, "stdout", res.Stdout, "Created desktop entry")

	raw, err := os.ReadFile(filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-desk_test.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	eq(t, "first line", lines[0], "[Desktop Entry]")
	has := func(line string) {
		t.Helper()
		for _, l := range lines {
			if l == line {
				return
			}
		}
		t.Errorf("no line %q in %q", line, lines)
	}
	has("Name=Run 100% \"special\" `app` with $VAR")
	has("Icon=utilities-terminal")
	has("Terminal=true")
	exec := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "Exec=") {
			exec = l
			break
		}
	}
	contains(t, "Exec", exec, e.ToolBin+"/shortcut run --global desk_test")
	contains(t, "Exec", exec, `\"Press Enter to close...\"`)
	contains(t, "Exec", exec, `rc=\$?`)
	contains(t, "Exec", exec, `\$rc`)
}

// py: test_bundled.py::test_bundled_edit_opens_the_script_or_the_defining_file
func TestBundled_EditOpensTheScriptOrTheDefiningFile(t *testing.T) {
	needPython(t)
	e := testkit.New(t)
	installBundled(t, e)
	script := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	runOK(t, e, testkit.Opts{}, "new", "tool", "--copy", script, "--desc", "A tool")
	runOK(t, e, testkit.Opts{}, "new", "line", "--", "true")
	e.WriteFile(filepath.Join(e.ToolBin, "opener"), "#!/bin/sh\necho \"$1 opened $2\"\n", 0o755)
	env := map[string]string{"VISUAL": "opener first"}

	res := e.RunWith(testkit.Opts{Env: env}, "r", "edit", "tool")
	eq(t, "tool", res.Stdout, "first opened "+filepath.Join(e.Store, "scripts", "tool.sh")+"\n")
	res = e.RunWith(testkit.Opts{Env: env}, "r", "edit", "line")
	eq(t, "line", res.Stdout, "first opened "+filepath.Join(e.Store, "shortcuts.json")+"\n")

	proj := e.Project(map[string]any{"line": inline("true")}, testkit.Approved())
	res = e.RunWith(testkit.Opts{Cwd: proj, Env: env}, "r", "edit", "line")
	eq(t, "project line", res.Stdout, "first opened "+filepath.Join(proj, ".shortcuts")+"\n")
	res = e.RunWith(testkit.Opts{Cwd: proj, Env: env}, "r", "edit", "--global", "line")
	eq(t, "global line", res.Stdout, "first opened "+filepath.Join(e.Store, "shortcuts.json")+"\n")

	res = e.RunWith(testkit.Opts{Env: env}, "r", "edit", "nothing")
	eq(t, "exit", res.Code, 1)
	eq(t, "stdout", res.Stdout, "")
	contains(t, "stderr", res.Stderr, "not found")
}

// py: test_bundled.py::test_bundled_scripts_dont_import_from_the_callers_folder
func TestBundled_ScriptsDontImportFromTheCallersFolder(t *testing.T) {
	needPython(t)
	// python3 -c puts the current folder first on sys.path; a json.py there would run unapproved
	e := testkit.New(t)
	installBundled(t, e)
	runOK(t, e, testkit.Opts{}, "new", "job", "--", "true")
	here := filepath.Join(e.Tmp, "planted")
	marker := filepath.Join(e.Tmp, "imported")
	e.WriteFile(filepath.Join(here, "json.py"), "open("+"'"+marker+"'"+", 'w').close()\n", 0)
	e.WriteFile(filepath.Join(e.ToolBin, "opener"), "#!/bin/sh\necho \"opened $1\"\n", 0o755)

	runOK(t, e, testkit.Opts{Cwd: here, Env: map[string]string{"VISUAL": "opener"}}, "r", "edit", "job")
	runOK(t, e, in(here), "r", "desktop", "job")
	notExist(t, marker)
}

// py: test_bundled.py::test_bundled_desktop_refuses_a_project_shortcut
func TestBundled_DesktopRefusesAProjectShortcut(t *testing.T) {
	needPython(t)
	e := testkit.New(t)
	installBundled(t, e)
	proj := e.Project(nil)
	runOK(t, e, in(proj), "new", "local_desk", "--local", "--", "echo 1")
	res := e.RunWith(in(proj), "r", "desktop", "local_desk")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "not found")
	notExist(t, filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-local_desk.desktop"))
}

// py: test_bundled.py::test_desktop_remove_deletes_only_its_own_launcher
func TestBundled_DesktopRemoveDeletesOnlyItsOwnLauncher(t *testing.T) {
	needPython(t)
	e := testkit.New(t)
	installBundled(t, e)
	runOK(t, e, testkit.Opts{}, "new", "app", "--desc", "An app", "--", "echo", "app")
	runOK(t, e, testkit.Opts{}, "r", "desktop", "app")
	launcher := filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-app.desktop")

	// rm leaves launchers alone: they belong to the desktop shortcut
	runOK(t, e, testkit.Opts{}, "rm", "app", "-y")
	if _, err := os.Stat(launcher); err != nil {
		t.Errorf("rm removed the launcher: %v", err)
	}
	runOK(t, e, testkit.Opts{}, "r", "desktop", "--remove", "app")
	notExist(t, launcher)

	// A launcher it didn't make is never deleted
	other := e.WriteFile(filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-mine.desktop"), "[Desktop Entry]\nName=mine\n", 0)
	res := e.Run("r", "desktop", "--remove", "mine")
	eq(t, "exit", res.Code, 1)
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a launcher it didn't make was removed: %v", err)
	}
}

// py: test_bundled.py::test_bundled_desktop_takes_a_namespace_member
func TestBundledDesktop_TakesANamespaceMember(t *testing.T) {
	needPython(t)
	e := testkit.New(t)
	installBundled(t, e)
	runOK(t, e, testkit.Opts{}, "new", "--namespace", "ns", "job", "--desc", "NS job", "--", "echo", "ns_ok")
	runOK(t, e, testkit.Opts{}, "r", "desktop", "ns", "job")
	desktop := filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-ns.job.desktop")
	raw, err := os.ReadFile(desktop)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "entry", string(raw), "\nName=NS job\n")
	contains(t, "entry", string(raw), "\nX-Shortcut-Name=ns job\n")
	contains(t, "entry", string(raw), "run --global ns job;")
	runOK(t, e, testkit.Opts{}, "r", "desktop", "--remove", "ns", "job")
	notExist(t, desktop)
}

// --bundled installs what a folder import of templates/ does: the same shortcuts.json, script bytes and modes (no py counterpart).
func TestBundled_SameAsImportingTheTemplatesFolder(t *testing.T) {
	e := testkit.New(t)
	res := runOK(t, e, testkit.Opts{}, "init", "--global", "--bundled", "-y")
	if !strings.Contains(res.Stdout, "the bundled shortcuts") {
		t.Errorf("the review does not name its source: %q", res.Stdout)
	}
	bundled := treeOf(t, e.Store)
	if len(bundled) < 5 {
		t.Fatalf("too little installed: %v", filesUnder(t, e.Store))
	}
	if err := os.RemoveAll(e.Store); err != nil {
		t.Fatal(err)
	}
	runOK(t, e, testkit.Opts{}, "init", "--global", templatesDir, "-y")
	eq(t, "store", bundled, treeOf(t, e.Store))
	eq(t, "scripts/search.sh mode", strings.SplitN(bundled["scripts/search.sh"], "\n", 2)[0], "-rwxr-xr-x")
}

// --replace goes the same way as for a folder: with nothing but a .shortcuts in it, it is refused (no py counterpart).
func TestBundled_ReplaceIsRefusedLikeForAProjectFolder(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("init", "--global", "--bundled", "--replace", "-y")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "--replace is for a folder of namespaces")
}

// --bundled takes no source, and needs --global (no py counterpart).
func TestBundled_RefusesASourceAndAMissingGlobal(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(hi(), testkit.Name("source_repo"))
	res := e.Run("init", "--global", "--bundled", src, "-y")
	eq(t, "exit", res.Code, 1)
	eq(t, "stderr", res.Stderr, "error: --bundled takes no source\n")
	res = e.Run("init", "--bundled", src)
	eq(t, "exit", res.Code, 1)
	eq(t, "stderr", res.Stderr, "error: --bundled takes no source\n")
	res = e.Run("init", "--bundled", "-y")
	eq(t, "exit", res.Code, 1)
	eq(t, "stderr", res.Stderr, "error: --bundled copies the bundled shortcuts into your global store: add --global.\n")
	if _, err := os.Stat(filepath.Join(e.Store, "shortcuts.json")); err == nil {
		t.Error("a refused --bundled created the global store")
	}
}

// The three completion scripts offer --bundled after init (no py counterpart).
func TestBundled_CompletionScriptsListIt(t *testing.T) {
	e := testkit.New(t)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		want := "--bundled"
		if shell == "fish" {
			want = "-l bundled" // fish has no spelled-out long options
		}
		contains(t, shell, e.Run("completion", shell).Stdout, want)
		if shell == "bash" {
			var got []string
			for _, w := range e.BashComplete("init", "--b") {
				got = append(got, w)
			}
			eq(t, "bash words", got, []string{"--bundled"})
		}
	}
}

// The temporary folder is gone after success, a "no" and an error (no py counterpart).
func TestBundled_TempFolderIsRemoved(t *testing.T) {
	e := testkit.New(t)
	tmp := e.Folder("tmpdir")
	o := testkit.Opts{Env: map[string]string{"TMPDIR": tmp}}
	gone := func(when string) {
		t.Helper()
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("%s: the temporary folder is still there: %v", when, left)
		}
	}
	// declined, and Ctrl-C at the question
	code, _ := e.InTerminal(o, []testkit.Key{{Wait: "import? [y/N]", Send: []byte("n\n")}}, "init", "--global", "--bundled")
	eq(t, "decline exit", code, 1)
	gone("after a no")
	code, out := e.InTerminal(o, []testkit.Key{{Wait: "import? [y/N]", Signal: syscall.SIGINT}}, "init", "--global", "--bundled")
	eq(t, "ctrl-c exit", code, 1)
	contains(t, "ctrl-c", out, "Aborted.")
	gone("after Ctrl-C")
	// an error: no terminal and no -y
	res := e.RunWith(o, "init", "--global", "--bundled")
	eq(t, "exit", res.Code, 1)
	contains(t, "stderr", res.Stderr, "import the bundled shortcuts in a terminal, or pass -y")
	gone("after an error")
	// success
	runOK(t, e, o, "init", "--global", "--bundled", "-y")
	gone("after success")
	if !bytes.Contains([]byte(strings.Join(globalNames(e), " ")), []byte("search")) {
		t.Errorf("nothing was installed: %v", globalNames(e))
	}
}
