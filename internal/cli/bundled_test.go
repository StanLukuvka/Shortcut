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

func TestBundled_Templates(t *testing.T) {
	e := testkit.New(t)
	search := filepath.Join(templatesDir, "scripts", "search.sh")
	if _, err := os.Stat(filepath.Join(templatesDir, ".shortcuts")); err != nil {
		t.Fatal(err)
	}
	// search must print usage and exit 1 when given no pattern
	res := e.RunCmd(testkit.Opts{}, search)
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "usage:")
	// search finds pattern when given
	e.WriteFile(filepath.Join(e.Tmp, "target_search_me.txt"), "hello", 0)
	res = e.RunCmd(testkit.Opts{}, search, "search_me", e.Tmp)
	res.WantCode(t, 0)
	testkit.Contains(t, "stdout", res.Stdout, "target_search_me.txt")

	res = runOK(t, e, testkit.Opts{}, "init", "--global", templatesDir, "-y")
	testkit.Contains(t, "stdout", res.Stdout, "search")
	testkit.NotContains(t, "stdout", res.Stdout, "Skipped")
	for _, name := range []string{"edit", "nano", "logged", "desktop"} {
		if _, ok := e.LoadGlobal()[name]; !ok {
			t.Errorf("%s was not imported", name)
		}
	}
}

func TestBundled_NanoLoggedAndDesktop(t *testing.T) {
	testkit.Need(t, "python3")
	e := testkit.New(t)
	e.InstallBundled()
	runOK(t, e, testkit.Opts{}, "new", "job", "--desc", "A job", "--", "echo job output")
	runOK(t, e, testkit.Opts{}, "new", "failing", "--", "echo out; echo err >&2; exit 3")

	// logged is a plain wrapper: the output reaches you and the file, with the shortcut's exit code
	state := filepath.Join(e.Tmp, "state")
	res := e.RunWith(testkit.Opts{Env: map[string]string{"XDG_STATE_HOME": state}}, "r", "logged", "failing")
	res.WantCode(t, 3)
	testkit.Contains(t, "stdout", res.Stdout, "out\n")
	raw, err := os.ReadFile(filepath.Join(state, "shortcut", "failing.log"))
	if err != nil {
		t.Fatal(err)
	}
	testkit.Contains(t, "log", string(raw), "out\n")
	testkit.Contains(t, "log", string(raw), "err\n")

	// nano wins over whatever editor is configured, and finds what a fixed folder couldn't
	e.WriteFile(filepath.Join(e.ToolBin, "nano"), "#!/bin/sh\necho \"nano opened $1\"\n", 0o755)
	res = e.RunWith(testkit.Opts{Env: map[string]string{"VISUAL": "false", "EDITOR": "false"}}, "r", "nano", "job")
	res.WantCode(t, 0)
	testkit.Contains(t, "stdout", res.Stdout, "nano opened")

	res = e.Run("r", "desktop")
	res.WantCode(t, 2)
	testkit.Contains(t, "stderr", res.Stderr, "usage: shortcut r desktop <shortcut...>")
}

func TestBundled_DesktopEntryQuoting(t *testing.T) {
	testkit.Need(t, "python3")
	e := testkit.New(t)
	e.InstallBundled()
	runOK(t, e, testkit.Opts{}, "new", "desk_test", "--desc", "Run 100% \"special\" `app` with $VAR", "--", "echo", "desktop_ok")
	res := runOK(t, e, testkit.Opts{}, "r", "desktop", "desk_test")
	testkit.Contains(t, "stdout", res.Stdout, "Created desktop entry")

	raw, err := os.ReadFile(filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-desk_test.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	testkit.Eq(t, "first line", lines[0], "[Desktop Entry]")
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
	testkit.Contains(t, "Exec", exec, e.ToolBin+"/shortcut run --global desk_test")
	testkit.Contains(t, "Exec", exec, `\"Press Enter to close...\"`)
	testkit.Contains(t, "Exec", exec, `rc=\$?`)
	testkit.Contains(t, "Exec", exec, `\$rc`)
}

func TestBundled_EditOpensTheScriptOrTheDefiningFile(t *testing.T) {
	testkit.Need(t, "python3")
	e := testkit.New(t)
	e.InstallBundled()
	script := e.Script("tool.sh", "#!/usr/bin/env bash\n")
	runOK(t, e, testkit.Opts{}, "new", "tool", "--copy", script, "--desc", "A tool")
	runOK(t, e, testkit.Opts{}, "new", "line", "--", "true")
	e.WriteFile(filepath.Join(e.ToolBin, "opener"), "#!/bin/sh\necho \"$1 opened $2\"\n", 0o755)
	env := map[string]string{"VISUAL": "opener first"}

	res := e.RunWith(testkit.Opts{Env: env}, "r", "edit", "tool")
	testkit.Eq(t, "tool", res.Stdout, "first opened "+filepath.Join(e.Store, "scripts", "tool.sh")+"\n")
	res = e.RunWith(testkit.Opts{Env: env}, "r", "edit", "line")
	testkit.Eq(t, "line", res.Stdout, "first opened "+filepath.Join(e.Store, "shortcuts.json")+"\n")

	proj := e.Project(map[string]any{"line": testkit.Inline("true")}, testkit.Approved())
	res = e.RunWith(testkit.Opts{Cwd: proj, Env: env}, "r", "edit", "line")
	testkit.Eq(t, "project line", res.Stdout, "first opened "+filepath.Join(proj, ".shortcuts")+"\n")
	res = e.RunWith(testkit.Opts{Cwd: proj, Env: env}, "r", "edit", "--global", "line")
	testkit.Eq(t, "global line", res.Stdout, "first opened "+filepath.Join(e.Store, "shortcuts.json")+"\n")

	res = e.RunWith(testkit.Opts{Env: env}, "r", "edit", "nothing")
	res.WantCode(t, 1)
	testkit.Eq(t, "stdout", res.Stdout, "")
	testkit.Contains(t, "stderr", res.Stderr, "not found")
}

func TestBundled_ScriptsDontImportFromTheCallersFolder(t *testing.T) {
	testkit.Need(t, "python3")
	// python3 -c puts the current folder first on sys.path; a json.py there would run unapproved
	e := testkit.New(t)
	e.InstallBundled()
	runOK(t, e, testkit.Opts{}, "new", "job", "--", "true")
	here := filepath.Join(e.Tmp, "planted")
	marker := filepath.Join(e.Tmp, "imported")
	e.WriteFile(filepath.Join(here, "json.py"), "open("+"'"+marker+"'"+", 'w').close()\n", 0)
	e.WriteFile(filepath.Join(e.ToolBin, "opener"), "#!/bin/sh\necho \"opened $1\"\n", 0o755)

	runOK(t, e, testkit.Opts{Cwd: here, Env: map[string]string{"VISUAL": "opener"}}, "r", "edit", "job")
	runOK(t, e, in(here), "r", "desktop", "job")
	testkit.NotExist(t, marker)
}

func TestBundled_DesktopRefusesAProjectShortcut(t *testing.T) {
	testkit.Need(t, "python3")
	e := testkit.New(t)
	e.InstallBundled()
	proj := e.Project(nil)
	runOK(t, e, in(proj), "new", "local_desk", "--local", "--", "echo 1")
	res := e.RunWith(in(proj), "r", "desktop", "local_desk")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "not found")
	testkit.NotExist(t, filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-local_desk.desktop"))
}

func TestBundled_DesktopRemoveDeletesOnlyItsOwnLauncher(t *testing.T) {
	testkit.Need(t, "python3")
	e := testkit.New(t)
	e.InstallBundled()
	runOK(t, e, testkit.Opts{}, "new", "app", "--desc", "An app", "--", "echo", "app")
	runOK(t, e, testkit.Opts{}, "r", "desktop", "app")
	launcher := filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-app.desktop")

	// rm leaves launchers alone: they belong to the desktop shortcut
	runOK(t, e, testkit.Opts{}, "rm", "app", "-y")
	if _, err := os.Stat(launcher); err != nil {
		t.Errorf("rm removed the launcher: %v", err)
	}
	runOK(t, e, testkit.Opts{}, "r", "desktop", "--remove", "app")
	testkit.NotExist(t, launcher)

	// A launcher it didn't make is never deleted
	other := e.WriteFile(filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-mine.desktop"), "[Desktop Entry]\nName=mine\n", 0)
	res := e.Run("r", "desktop", "--remove", "mine")
	res.WantCode(t, 1)
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a launcher it didn't make was removed: %v", err)
	}
}

func TestBundledDesktop_TakesANamespaceMember(t *testing.T) {
	testkit.Need(t, "python3")
	e := testkit.New(t)
	e.InstallBundled()
	runOK(t, e, testkit.Opts{}, "new", "--namespace", "ns", "job", "--desc", "NS job", "--", "echo", "ns_ok")
	runOK(t, e, testkit.Opts{}, "r", "desktop", "ns", "job")
	desktop := filepath.Join(e.Vars["XDG_DATA_HOME"], "applications", "shortcut-ns.job.desktop")
	raw, err := os.ReadFile(desktop)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Contains(t, "entry", string(raw), "\nName=NS job\n")
	testkit.Contains(t, "entry", string(raw), "\nX-Shortcut-Name=ns job\n")
	testkit.Contains(t, "entry", string(raw), "run --global ns job;")
	runOK(t, e, testkit.Opts{}, "r", "desktop", "--remove", "ns", "job")
	testkit.NotExist(t, desktop)
}

// --bundled installs what a folder import of templates/ does: the same shortcuts.json, script bytes and modes.
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
	testkit.Eq(t, "store", bundled, treeOf(t, e.Store))
	testkit.Eq(t, "scripts/search.sh mode", strings.SplitN(bundled["scripts/search.sh"], "\n", 2)[0], "-rwxr-xr-x")
}

// --replace goes the same way as for a folder: with nothing but a .shortcuts in it, it is refused.
func TestBundled_ReplaceIsRefusedLikeForAProjectFolder(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("init", "--global", "--bundled", "--replace", "-y")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "--replace is for a folder of namespaces")
}

// --bundled takes no source, and needs --global.
func TestBundled_RefusesASourceAndAMissingGlobal(t *testing.T) {
	e := testkit.New(t)
	src := e.Collection(testkit.HI, testkit.Name("source_repo"))
	res := e.Run("init", "--global", "--bundled", src, "-y")
	res.WantCode(t, 1)
	testkit.Eq(t, "stderr", res.Stderr, "error: --bundled takes no source\n")
	res = e.Run("init", "--bundled", src)
	res.WantCode(t, 1)
	testkit.Eq(t, "stderr", res.Stderr, "error: --bundled takes no source\n")
	res = e.Run("init", "--bundled", "-y")
	res.WantCode(t, 1)
	testkit.Eq(t, "stderr", res.Stderr, "error: --bundled copies the bundled shortcuts into your global store: add --global.\n")
	if _, err := os.Stat(filepath.Join(e.Store, "shortcuts.json")); err == nil {
		t.Error("a refused --bundled created the global store")
	}
}

// The three completion scripts offer --bundled after init.
func TestBundled_CompletionScriptsListIt(t *testing.T) {
	e := testkit.New(t)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		want := "--bundled"
		if shell == "fish" {
			want = "-l bundled" // fish has no spelled-out long options
		}
		testkit.Contains(t, shell, e.Run("completion", shell).Stdout, want)
		if shell == "bash" {
			var got []string
			for _, w := range e.BashComplete("init", "--b") {
				got = append(got, w)
			}
			testkit.Eq(t, "bash words", got, []string{"--bundled"})
		}
	}
}

// The temporary folder is gone after success, a "no" and an error.
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
	testkit.Eq(t, "decline exit", code, 1)
	gone("after a no")
	code, out := e.InTerminal(o, []testkit.Key{{Wait: "import? [y/N]", Signal: syscall.SIGINT}}, "init", "--global", "--bundled")
	testkit.Eq(t, "ctrl-c exit", code, 1)
	testkit.Contains(t, "ctrl-c", out, "Aborted.")
	gone("after Ctrl-C")
	// an error: no terminal and no -y
	res := e.RunWith(o, "init", "--global", "--bundled")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "import the bundled shortcuts in a terminal, or pass -y")
	gone("after an error")
	// success
	runOK(t, e, o, "init", "--global", "--bundled", "-y")
	gone("after success")
	if !bytes.Contains([]byte(strings.Join(globalNames(e), " ")), []byte("search")) {
		t.Errorf("nothing was installed: %v", globalNames(e))
	}
}
