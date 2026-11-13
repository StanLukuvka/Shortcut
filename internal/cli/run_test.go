package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// runOK runs the binary and fails the test unless it exits 0: for set-up that goes through a command.
func runOK(t *testing.T, e *testkit.Env, o testkit.Opts, args ...string) testkit.Result {
	t.Helper()
	res := e.RunWith(o, args...)
	if res.Code != 0 {
		t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, res.Code, res.Stdout, res.Stderr)
	}
	return res
}

func in(dir string) testkit.Opts { return testkit.Opts{Cwd: dir} }

func TestRunning_RunInlineWithPassthroughArgs(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"echo": testkit.Inline("echo name=$0 first=$1 all=$*")})
	for range 2 {
		res := e.Run("run", "echo", "-v", "--option", "arg1", "arg2")
		if res.Code != 0 || strings.TrimSpace(res.Stdout) != "name=echo first=-v all=-v --option arg1 arg2" {
			t.Errorf("exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
		}
	}
}

func TestRunning_TheEnvironmentSaysWhatRunsAndFromWhere(t *testing.T) {
	e := testkit.New(t)
	show := `echo "$SHORTCUT_NAME|$SHORTCUT_NAMESPACE|$SHORTCUT_CALLER_CWD|${SC_ARG_OLD-unset}|${SC_ARG_X-unset}"`
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{
		"env": map[string]any{"inline": show, "args": []any{map[string]any{"name": "x", "default": "d"}}}}))
	e.WriteStore(e.Store, map[string]any{"env": testkit.Inline(show)})
	runOK(t, e, testkit.Opts{}, "update", "-y", "dk")
	caller := e.Folder("caller")
	outer := map[string]string{"SC_ARG_OLD": "outer", "SC_ARG_X": "outer", "SHORTCUT_NAMESPACE": "outer"}
	res := e.RunWith(testkit.Opts{Cwd: caller, Env: outer}, "r", "dk", "env")
	if want := "dk env|dk|" + caller + "|unset|d\n"; res.Stdout != want {
		t.Errorf("stdout %q, want %q (%s)", res.Stdout, want, res.Stderr)
	}
	res = e.RunWith(testkit.Opts{Cwd: caller, Env: outer}, "r", "env")
	if want := "env||" + caller + "|unset|unset\n"; res.Stdout != want {
		t.Errorf("stdout %q, want %q (%s)", res.Stdout, want, res.Stderr)
	}
}

func TestRunning_APresetShortcutNameIsReplacedOnce(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"cnt": testkit.Inline(`env | grep -c '^SHORTCUT_NAME='; echo "$SHORTCUT_NAME"`)})
	res := e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_NAME": "outer"}}, "r", "cnt")
	testkit.Eq(t, "stdout", res.Stdout, "1\ncnt\n")
}

func TestRunning_ANameAloneNeverRunsAShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": testkit.Inline("echo hello")})
	for _, argv := range [][]string{{"hello"}, {"nano", "hello"}} {
		res := e.Run(argv...)
		if res.Code != 2 || res.Stdout != "" {
			t.Errorf("%v: exit %d stdout %q", argv, res.Code, res.Stdout)
		}
		testkit.Contains(t, "stderr", res.Stderr, "unknown command '"+argv[0]+"'")
		testkit.Contains(t, "stderr", res.Stderr, "shortcut run "+argv[0])
	}
}

func TestRunning_RIsShortForRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": testkit.Inline(`echo "hello $1"`)})
	if out := e.Run("r", "hello", "you").Stdout; out != "hello you\n" {
		t.Errorf("r hello you: %q", out)
	}
	testkit.Contains(t, "r --help", e.Run("r", "--help").Stdout, "shortcut run|r [--global] [-y] <name>")
}

// The same branches (a #! line on a file that can't be executed, no #! line, a #! line
// on an executable) are run with sh and bash scripts.
func TestRunning_ScriptShebangWithoutExecAndElf(t *testing.T) {
	e := testkit.New(t)
	scripts := filepath.Join(e.Store, "scripts")
	e.WriteFile(filepath.Join(scripts, "py_script.sh"), "#!/usr/bin/env sh\necho \"py $((1 + 1))\"\n", 0o644)
	e.WriteFile(filepath.Join(scripts, "no_shebang.sh"), "echo nonexec\n", 0o644)
	e.WriteFile(filepath.Join(scripts, "exec.sh"), "#!/usr/bin/env bash\necho exec_shebang\n", 0o755)
	e.WriteFile(filepath.Join(scripts, "blank.sh"), "#! \t \necho blank_shebang\n", 0o644)
	e.WriteStore(e.Store, map[string]any{
		"blank":     map[string]any{"script": "scripts/blank.sh"},
		"py":        map[string]any{"script": "scripts/py_script.sh", "description": "python"},
		"noshebang": map[string]any{"script": "scripts/no_shebang.sh", "description": "noshebang"},
		"exec":      map[string]any{"script": "scripts/exec.sh", "description": "exec"},
	})
	for name, want := range map[string]string{"blank": "blank_shebang", "py": "py 2", "noshebang": "nonexec", "exec": "exec_shebang"} {
		res := e.Run("run", name)
		if res.Code != 0 || !strings.Contains(res.Stdout, want) {
			t.Errorf("%s: exit %d stdout %q stderr %q", name, res.Code, res.Stdout, res.Stderr)
		}
	}
}

// An executable with no #! is exec'd as it is; only an executable text file the kernel refuses (ENOEXEC) goes to bash.
func TestRunning_ExecutableWithoutShebang(t *testing.T) {
	e := testkit.New(t)
	scripts := filepath.Join(e.Store, "scripts")
	var bin []byte
	for _, p := range []string{"/usr/bin/true", "/bin/true"} {
		if b, err := os.ReadFile(p); err == nil {
			bin = b
			break
		}
	}
	if bin == nil {
		t.Skip("no true binary")
	}
	e.WriteFile(filepath.Join(scripts, "text.sh"), "echo hi\n", 0o755)
	e.WriteFile(filepath.Join(scripts, "bin"), bin, 0o755)
	e.WriteFile(filepath.Join(scripts, "macho"), append([]byte("\xcf\xfa\xed\xfe"), make([]byte, 64)...), 0o755)
	e.WriteStore(e.Store, map[string]any{
		"text":  map[string]any{"script": "scripts/text.sh"},
		"bin":   map[string]any{"script": "scripts/bin"},
		"macho": map[string]any{"script": "scripts/macho"},
	})
	if res := e.Run("run", "text"); res.Code != 0 || res.Stdout != "hi\n" {
		t.Errorf("text: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if res := e.Run("run", "bin"); res.Code != 0 || res.Stdout != "" {
		t.Errorf("bin: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	// A binary the kernel will not run (another system's format) is not handed to bash.
	res := e.Run("run", "macho")
	testkit.Eq(t, "macho exit", res.Code, 1)
	testkit.Eq(t, "macho stdout", res.Stdout, "")
	testkit.Contains(t, "macho stderr", res.Stderr, "exec format error")
	if strings.Contains(res.Stderr, "bash") || strings.Contains(res.Stderr, "line ") {
		t.Errorf("macho was run by bash: %q", res.Stderr)
	}
}

func TestRunning_RunHelp(t *testing.T) {
	e := testkit.New(t)
	for _, flag := range []string{"--help", "-h"} {
		res := e.Run("run", flag)
		res.WantCode(t, 0)
		testkit.Contains(t, "run "+flag, res.Stdout, "shortcut run|r [--global] [-y] <name> [args...]")
	}
	e.WriteStore(e.Store, map[string]any{"echo_args": testkit.Inline(`echo "args: $@"`)})
	res := e.Run("run", "echo_args", "--help")
	res.WantCode(t, 0)
	testkit.Contains(t, "stdout", res.Stdout, "args: --help")
}

func TestRunning_RunCwdModes(t *testing.T) {
	e := testkit.New(t)
	fixed := e.Folder("fixed")
	proj := e.Project(map[string]any{
		"default": testkit.Inline("pwd"),
		"caller":  map[string]any{"inline": "pwd", "cwd": "caller"},
		"root":    map[string]any{"inline": "pwd", "cwd": "."},
		"sibling": map[string]any{"inline": "pwd", "cwd": "frontend"},
		"fixed":   map[string]any{"inline": "pwd", "cwd": fixed},
		"gone":    map[string]any{"inline": "pwd", "cwd": "nope"},
	}, testkit.Dirs("frontend", "src"), testkit.Approved())
	here := filepath.Join(proj, "src")
	for _, c := range [][2]string{{"default", here}, {"caller", here}, {"root", proj}, {"sibling", filepath.Join(proj, "frontend")}, {"fixed", fixed}} {
		res := e.RunWith(in(here), "r", c[0])
		if res.Stdout != c[1]+"\n" {
			t.Errorf("%s: stdout %q, want %q", c[0], res.Stdout, c[1]+"\n")
		}
		testkit.Contains(t, c[0], res.Stderr, "→ "+c[1]+"\n")
	}
	res := e.RunWith(in(here), "r", "gone")
	res.WantCode(t, 1)
	testkit.Contains(t, "gone", res.Stderr, "run directory does not exist")
}

func TestRunning_RunDirectoryLine(t *testing.T) {
	e := testkit.New(t)
	work := e.Folder("work")
	e.WriteStore(e.Store, map[string]any{"pwd": testkit.Inline("pwd")})
	res := e.RunWith(in(work), "run", "pwd")
	if res.Stdout != work+"\n" || res.Stderr != "→ "+work+"\n" {
		t.Errorf("stdout %q stderr %q", res.Stdout, res.Stderr)
	}
	res = e.RunWith(testkit.Opts{Cwd: work, Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}, "run", "pwd")
	if res.Stderr != "" {
		t.Errorf("SHORTCUT_SHOW_DIR=0: stderr %q", res.Stderr)
	}
	_, out := e.InTerminal(in(work), nil, "run", "pwd")
	testkit.Contains(t, "on a terminal", out, "\x1b[2m→ "+work+"\x1b[0m")
}

func TestRunning_ShowPrintsTheEntryScriptCwdAndArgs(t *testing.T) {
	e := testkit.New(t)
	script := e.Script("shown.sh", "#!/usr/bin/env bash\necho show output running\n")
	e.WriteStore(e.Store, map[string]any{"shown": map[string]any{"script": script, "description": "Show sample", "cwd": e.Tmp,
		"args": []any{map[string]any{"name": "target", "required": true}, map[string]any{"name": "mode", "default": "fast"}}}})
	res := e.Run("show", "shown")
	res.WantCode(t, 0)
	testkit.Contains(t, "show", res.Stdout, "Description: Show sample\n")
	testkit.Contains(t, "show", res.Stdout, "echo show output running")
	testkit.Contains(t, "show", res.Stdout, "Run CWD:     "+e.Tmp+"\n")
	testkit.Contains(t, "show", res.Stdout, "Usage:       shortcut r shown <target> [mode]\n")
}

func TestRunning_ShowSaysWhenTheRunDirectoryOrScriptIsGone(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"gone": map[string]any{"inline": "true", "cwd": "/no/such/folder"}})
	testkit.Contains(t, "cwd", e.Run("show", "gone").Stdout, "/no/such/folder")
	e.WriteStore(e.Store, map[string]any{"gone": map[string]any{"script": "scripts/none.sh"}})
	testkit.Contains(t, "script", e.Run("show", "gone").Stdout, "[file missing:")
}

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var info map[string]any
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, text)
	}
	return info
}

func TestRunning_ShowJSONPrintsAnEntryShowRefuses(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"bad": map[string]any{"inline": "true", "cwd": 123}})
	testkit.Contains(t, "show bad", e.Run("show", "bad").Stderr, "'cwd' must be a string")
	res := e.Run("show", "--json", "bad")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}
	info := decodeObject(t, res.Stdout)
	testkit.Eq(t, "entry", info["entry"], map[string]any{"inline": "true", "cwd": float64(123)})
	testkit.Contains(t, "error", info["error"].(string), "'cwd' must be a string")
	testkit.Eq(t, "name", info["name"], "bad")
	for _, k := range []string{"cwd", "run_cwd", "script_path"} {
		testkit.Eq(t, k, info[k], nil)
	}
}

func TestRunning_ShowJSONIsOneObjectWithWhereItResolves(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"s": map[string]any{"script": "scripts/s.sh", "cwd": "/tmp"}, "c": testkit.Inline("true")})
	info := decodeObject(t, e.Run("show", "--json", "s").Stdout)
	for k, want := range map[string]any{"name": "s", "namespace": nil, "scope": "global", "defined_in": filepath.Join(e.Store, "shortcuts.json"),
		"source": nil, "script_path": filepath.Join(e.Store, "scripts", "s.sh"), "cwd": "/tmp", "run_cwd": "/tmp", "approval": "ok",
		"error": nil, "hides_global": false, "entry": map[string]any{"script": "scripts/s.sh", "cwd": "/tmp"}} {
		testkit.Eq(t, k, info[k], want)
	}
	info = decodeObject(t, e.RunWith(in(e.Tmp), "show", "--json", "c").Stdout)
	testkit.Eq(t, "cwd", info["cwd"], nil)
	testkit.Eq(t, "run_cwd", info["run_cwd"], e.Tmp)
}

func TestRunning_ShowJSONNamesANamespaceEntryBare(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{"ps": map[string]any{"inline": "true", "cwd": "."}}))
	info := decodeObject(t, e.Run("show", "--json", "dk", "ps").Stdout)
	for k, want := range map[string]any{"name": "ps", "namespace": "dk", "scope": "namespace",
		"defined_in": filepath.Join(e.Store, "imports", "dk.shortcut"), "cwd": ".", "run_cwd": filepath.Join(e.Store, "imports"), "approval": "new"} {
		testkit.Eq(t, k, info[k], want)
	}
}

func TestRunning_ARunThatCantStartIsAnError(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, testkit.GO)
	res := e.RunWith(testkit.Opts{Env: map[string]string{"PATH": e.Tmp}}, "r", "go")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "error:")
}

func TestRunning_InvalidArgsDeclarationIsAnError(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"bad": map[string]any{"inline": "true", "args": []any{
		map[string]any{"name": "a", "rest": true}, map[string]any{"name": "b"}}}})
	res := e.Run("r", "bad")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "only the last entry in 'args' can be 'rest'")
	e.WriteStore(e.Store, map[string]any{"both": map[string]any{"inline": "true", "args": []any{
		map[string]any{"name": "a", "required": true, "default": "x"}}}})
	res = e.Run("r", "both")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "arg 'a' can't be both 'required' and have a 'default'")
}

func TestRunning_AnEmptyWordDoesntFillARequiredArgument(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{
		"wipe": map[string]any{"inline": `echo "gone: /$1"`, "args": []any{map[string]any{"name": "target", "required": true}}},
		"opt":  map[string]any{"inline": `echo "[$1]"`, "args": []any{map[string]any{"name": "x", "default": ""}}},
	})
	res := e.Run("r", "wipe", "")
	if res.Code != 2 || res.Stdout != "" {
		t.Errorf("exit %d stdout %q", res.Code, res.Stdout)
	}
	testkit.Contains(t, "stderr", res.Stderr, "error: <target> can't be empty\n")
	if out := e.Run("r", "opt", "").Stdout; out != "[]\n" {
		t.Errorf("opt: %q", out)
	}
}

func TestRunning_AScriptWithWindowsLineEndingsSaysSo(t *testing.T) {
	e := testkit.New(t)
	script := e.Script("crlf.sh", "#!/usr/bin/env bash\r\necho hi\r\n")
	e.WriteStore(e.Store, map[string]any{"crlf": map[string]any{"script": script}})
	res := e.Run("r", "crlf")
	res.WantCode(t, 1)
	testkit.Contains(t, "stderr", res.Stderr, "error: "+script+" has Windows line endings")
	testkit.Contains(t, "stderr", res.Stderr, "sed -i 's/\\r$//' "+script)
}

func TestRunning_AHandWrittenInvalidNameDoesntRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"a b": testkit.Inline("echo ran")})
	res := e.Run("r", "a b")
	if res.Code != 1 || res.Stdout != "" {
		t.Errorf("exit %d stdout %q", res.Code, res.Stdout)
	}
	testkit.Contains(t, "stderr", res.Stderr, "invalid shortcut name 'a b'")
}

func TestRunning_DeclaredArgs(t *testing.T) {
	e := testkit.New(t)
	script := `printf "[%s]" "$@"; echo; echo "c=$SC_ARG_CONTAINER s=$SC_ARG_SIGNAL e=$SC_ARG_EXTRA"`
	e.WriteStore(e.Store, map[string]any{
		"kill": map[string]any{"inline": script, "args": []any{
			map[string]any{"name": "container", "required": true, "desc": "Which one"},
			map[string]any{"name": "signal", "default": "TERM"},
			map[string]any{"name": "extra", "rest": true},
		}},
		"two":  map[string]any{"inline": `echo "$#:$*"`, "args": []any{map[string]any{"name": "a"}, map[string]any{"name": "b", "default": "B"}}},
		"free": testkit.Inline(`echo "$#"`),
		"one":  map[string]any{"inline": "true", "args": []any{map[string]any{"name": "only"}}},
	})
	lines := func(args ...string) []string {
		return strings.Split(strings.TrimSuffix(e.Run(args...).Stdout, "\n"), "\n")
	}

	res := e.Run("r", "kill")
	res.WantCode(t, 2)
	testkit.Contains(t, "missing", res.Stderr, "missing <container>")
	testkit.Contains(t, "missing", res.Stderr, "usage: shortcut r kill <container> [signal] [extra...]")
	// With a desc on any argument, the error lists them all
	testkit.Contains(t, "list", res.Stderr, "  container  required       Which one\n")
	testkit.Contains(t, "list", res.Stderr, "  signal     default: TERM\n")
	testkit.Contains(t, "list", res.Stderr, "  extra...   optional\n")

	testkit.Eq(t, "web", lines("r", "kill", "web"), []string{"[web][TERM]", "c=web s=TERM e="})
	testkit.Eq(t, "all", lines("r", "kill", "web", "KILL", "a b", "c"), []string{"[web][KILL][a b][c]", "c=web s=KILL e='a b' c"})
	// Flags are neither counted nor moved, and a default keeps its position before them
	testkit.Eq(t, "flag", lines("r", "kill", "web", "--force")[0], "[web][TERM][--force]")
	// A skipped optional keeps later defaults in their own position
	testkit.Eq(t, "two", e.Run("r", "two").Stdout, "2: B\n")

	res = e.Run("r", "one", "a", "b")
	res.WantCode(t, 2)
	testkit.Contains(t, "too many", res.Stderr, "too many arguments")
	testkit.Contains(t, "too many", res.Stderr, "usage: shortcut r one [only]")
	if strings.Contains(res.Stderr, "  only") {
		t.Errorf("a list with no desc anywhere: %q", res.Stderr)
	}

	// After --, every word is a value, even one starting with a dash; the -- itself is dropped
	testkit.Contains(t, "-5", e.Run("r", "kill", "-5").Stderr, "missing <container>")
	testkit.Eq(t, "--", lines("r", "kill", "--force", "--", "-5", "-x"), []string{"[--force][-5][-x]", "c=-5 s=-x e="})
	testkit.Eq(t, "two --", e.Run("r", "two", "--", "-a").Stdout, "2:-a B\n")

	// No `args` key: no checking, and -- passes through
	testkit.Eq(t, "free", e.Run("r", "free", "1", "2", "3").Stdout, "3\n")
	testkit.Eq(t, "free --", e.Run("r", "free", "--", "x").Stdout, "2\n")
	shown := e.Run("show", "kill").Stdout
	testkit.Contains(t, "show", shown, "Usage:       shortcut r kill <container> [signal] [extra...]\n")
	testkit.Contains(t, "show", shown, "Arguments:\n  container  required       Which one\n  signal     default: TERM\n  extra...   optional\n")
	if strings.Contains(e.Run("show", "free").Stdout, "Arguments:") {
		t.Errorf("show free lists arguments")
	}
}

func TestRunning_AClosedPipeStopsQuietly(t *testing.T) {
	e := testkit.New(t)
	// More than a pipe holds, so shortcut is still writing when the reader goes
	shortcuts := map[string]any{}
	for i := 0; i < 2000; i++ {
		shortcuts[fmt.Sprintf("s%d", i)] = map[string]any{"inline": "true", "description": strings.Repeat("x", 60)}
	}
	e.WriteStore(e.Store, shortcuts)
	res := e.Shell(testkit.Opts{}, "set -o pipefail; shortcut list | true")
	if res.Code != 141 || res.Stderr != "" {
		t.Errorf("exit %d stderr %q", res.Code, res.Stderr)
	}
}

// The "-> cwd" line goes to stderr with a bad byte escaped (as \xNN). A global shortcut needs no approval, so it
// still runs from a folder whose name is not valid UTF-8 (a project there cannot be approved).
func TestRun_CwdLineEscapesNonUTF8(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": testkit.Inline("true")})
	dir := filepath.Join(e.Tmp, "p\xffroj")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	res := e.RunWith(testkit.Opts{Cwd: dir}, "run", "hello")
	res.WantCode(t, 0)
	testkit.Contains(t, "stderr", res.Stderr, "p\\xffroj")
}
