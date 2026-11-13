// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-3). Don't hand-edit without checking the plan.

package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
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

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

// py: test_running.py::test_run_inline_with_passthrough_args
func TestRunning_RunInlineWithPassthroughArgs(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"echo": inline("echo name=$0 first=$1 all=$*")})
	for range 2 {
		res := e.Run("run", "echo", "-v", "--option", "arg1", "arg2")
		if res.Code != 0 || strings.TrimSpace(res.Stdout) != "name=echo first=-v all=-v --option arg1 arg2" {
			t.Errorf("exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
		}
	}
}

// py: test_running.py::test_the_environment_says_what_runs_and_from_where
func TestRunning_TheEnvironmentSaysWhatRunsAndFromWhere(t *testing.T) {
	e := testkit.New(t)
	show := `echo "$SHORTCUT_NAME|$SHORTCUT_NAMESPACE|$SHORTCUT_CALLER_CWD|${SC_ARG_OLD-unset}|${SC_ARG_X-unset}"`
	e.WriteImport(e.Store, "dk", map[string]any{"version": 2, "shortcuts": map[string]any{
		"env": map[string]any{"inline": show, "args": []any{map[string]any{"name": "x", "default": "d"}}}}})
	e.WriteStore(e.Store, map[string]any{"env": inline(show)})
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

// py: test_running.py::test_a_name_alone_never_runs_a_shortcut
func TestRunning_ANameAloneNeverRunsAShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": inline("echo hello")})
	for _, argv := range [][]string{{"hello"}, {"nano", "hello"}} {
		res := e.Run(argv...)
		if res.Code != 2 || res.Stdout != "" {
			t.Errorf("%v: exit %d stdout %q", argv, res.Code, res.Stdout)
		}
		contains(t, "stderr", res.Stderr, "unknown command '"+argv[0]+"'")
		contains(t, "stderr", res.Stderr, "shortcut run "+argv[0])
	}
}

// py: test_running.py::test_r_is_short_for_run
func TestRunning_RIsShortForRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hello": inline(`echo "hello $1"`)})
	if out := e.Run("r", "hello", "you").Stdout; out != "hello you\n" {
		t.Errorf("r hello you: %q", out)
	}
	contains(t, "r --help", e.Run("r", "--help").Stdout, "shortcut run|r [--global] [-y] <name>")
}

// py: test_running.py::test_script_shebang_without_exec_and_elf
// The Python script was python3; the same branches (a #! line on a file that can't be executed, no #! line, a #! line
// on an executable) are run with sh and bash scripts.
func TestRunning_ScriptShebangWithoutExecAndElf(t *testing.T) {
	e := testkit.New(t)
	scripts := filepath.Join(e.Store, "scripts")
	e.WriteFile(filepath.Join(scripts, "py_script.sh"), "#!/usr/bin/env sh\necho \"py $((1 + 1))\"\n", 0o644)
	e.WriteFile(filepath.Join(scripts, "no_shebang.sh"), "echo nonexec\n", 0o644)
	e.WriteFile(filepath.Join(scripts, "exec.sh"), "#!/usr/bin/env bash\necho exec_shebang\n", 0o755)
	e.WriteStore(e.Store, map[string]any{
		"py":        map[string]any{"script": "scripts/py_script.sh", "description": "python"},
		"noshebang": map[string]any{"script": "scripts/no_shebang.sh", "description": "noshebang"},
		"exec":      map[string]any{"script": "scripts/exec.sh", "description": "exec"},
	})
	for name, want := range map[string]string{"py": "py 2", "noshebang": "nonexec", "exec": "exec_shebang"} {
		res := e.Run("run", name)
		if res.Code != 0 || !strings.Contains(res.Stdout, want) {
			t.Errorf("%s: exit %d stdout %q stderr %q", name, res.Code, res.Stdout, res.Stderr)
		}
	}
}

// py: test_running.py::test_run_help
func TestRunning_RunHelp(t *testing.T) {
	e := testkit.New(t)
	for _, flag := range []string{"--help", "-h"} {
		res := e.Run("run", flag)
		if res.Code != 0 {
			t.Errorf("run %s: exit %d", flag, res.Code)
		}
		contains(t, "run "+flag, res.Stdout, "shortcut run|r [--global] [-y] <name> [args...]")
	}
	e.WriteStore(e.Store, map[string]any{"echo_args": inline(`echo "args: $@"`)})
	res := e.Run("run", "echo_args", "--help")
	if res.Code != 0 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stdout", res.Stdout, "args: --help")
}

// py: test_running.py::test_run_cwd_modes
func TestRunning_RunCwdModes(t *testing.T) {
	e := testkit.New(t)
	fixed := e.Folder("fixed")
	proj := e.Project(map[string]any{
		"default": inline("pwd"),
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
		contains(t, c[0], res.Stderr, "→ "+c[1]+"\n")
	}
	res := e.RunWith(in(here), "r", "gone")
	if res.Code != 1 {
		t.Errorf("gone: exit %d", res.Code)
	}
	contains(t, "gone", res.Stderr, "run directory does not exist")
}

// py: test_running.py::test_run_directory_line
func TestRunning_RunDirectoryLine(t *testing.T) {
	e := testkit.New(t)
	work := e.Folder("work")
	e.WriteStore(e.Store, map[string]any{"pwd": inline("pwd")})
	res := e.RunWith(in(work), "run", "pwd")
	if res.Stdout != work+"\n" || res.Stderr != "→ "+work+"\n" {
		t.Errorf("stdout %q stderr %q", res.Stdout, res.Stderr)
	}
	res = e.RunWith(testkit.Opts{Cwd: work, Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}, "run", "pwd")
	if res.Stderr != "" {
		t.Errorf("SHORTCUT_SHOW_DIR=0: stderr %q", res.Stderr)
	}
	_, out := e.InTerminal(in(work), nil, "run", "pwd")
	contains(t, "on a terminal", out, "\x1b[2m→ "+work+"\x1b[0m")
}

// py: test_running.py::test_show_prints_the_entry_script_cwd_and_args
func TestRunning_ShowPrintsTheEntryScriptCwdAndArgs(t *testing.T) {
	e := testkit.New(t)
	script := e.Script("shown.sh", "#!/usr/bin/env bash\necho show output running\n")
	e.WriteStore(e.Store, map[string]any{"shown": map[string]any{"script": script, "description": "Show sample", "cwd": e.Tmp,
		"args": []any{map[string]any{"name": "target", "required": true}, map[string]any{"name": "mode", "default": "fast"}}}})
	res := e.Run("show", "shown")
	if res.Code != 0 {
		t.Errorf("exit %d: %s", res.Code, res.Stderr)
	}
	contains(t, "show", res.Stdout, "Description: Show sample\n")
	contains(t, "show", res.Stdout, "echo show output running")
	contains(t, "show", res.Stdout, "Run CWD:     "+e.Tmp+"\n")
	contains(t, "show", res.Stdout, "Usage:       shortcut r shown <target> [mode]\n")
}

// py: test_running.py::test_show_says_when_the_run_directory_or_script_is_gone
func TestRunning_ShowSaysWhenTheRunDirectoryOrScriptIsGone(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"gone": map[string]any{"inline": "true", "cwd": "/no/such/folder"}})
	contains(t, "cwd", e.Run("show", "gone").Stdout, "/no/such/folder")
	e.WriteStore(e.Store, map[string]any{"gone": map[string]any{"script": "scripts/none.sh"}})
	contains(t, "script", e.Run("show", "gone").Stdout, "[file missing:")
}

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var info map[string]any
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, text)
	}
	return info
}

// py: test_running.py::test_show_json_prints_an_entry_show_refuses
func TestRunning_ShowJSONPrintsAnEntryShowRefuses(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"bad": map[string]any{"inline": "true", "cwd": 123}})
	contains(t, "show bad", e.Run("show", "bad").Stderr, "'cwd' must be a string")
	res := e.Run("show", "--json", "bad")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}
	info := decodeObject(t, res.Stdout)
	eq(t, "entry", info["entry"], map[string]any{"inline": "true", "cwd": float64(123)})
	contains(t, "error", info["error"].(string), "'cwd' must be a string")
	eq(t, "name", info["name"], "bad")
	for _, k := range []string{"cwd", "run_cwd", "script_path"} {
		eq(t, k, info[k], nil)
	}
}

// py: test_running.py::test_show_json_is_one_object_with_where_it_resolves
func TestRunning_ShowJSONIsOneObjectWithWhereItResolves(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"s": map[string]any{"script": "scripts/s.sh", "cwd": "/tmp"}, "c": inline("true")})
	info := decodeObject(t, e.Run("show", "--json", "s").Stdout)
	for k, want := range map[string]any{"name": "s", "namespace": nil, "scope": "global", "defined_in": filepath.Join(e.Store, "shortcuts.json"),
		"source": nil, "script_path": filepath.Join(e.Store, "scripts", "s.sh"), "cwd": "/tmp", "run_cwd": "/tmp", "approval": "ok",
		"error": nil, "hides_global": false, "entry": map[string]any{"script": "scripts/s.sh", "cwd": "/tmp"}} {
		eq(t, k, info[k], want)
	}
	info = decodeObject(t, e.RunWith(in(e.Tmp), "show", "--json", "c").Stdout)
	eq(t, "cwd", info["cwd"], nil)
	eq(t, "run_cwd", info["run_cwd"], e.Tmp)
}

// py: test_running.py::test_show_json_names_a_namespace_entry_bare
func TestRunning_ShowJSONNamesANamespaceEntryBare(t *testing.T) {
	e := testkit.New(t)
	e.WriteImport(e.Store, "dk", map[string]any{"version": 2, "shortcuts": map[string]any{"ps": map[string]any{"inline": "true", "cwd": "."}}})
	info := decodeObject(t, e.Run("show", "--json", "dk", "ps").Stdout)
	for k, want := range map[string]any{"name": "ps", "namespace": "dk", "scope": "namespace",
		"defined_in": filepath.Join(e.Store, "imports", "dk.shortcut"), "cwd": ".", "run_cwd": filepath.Join(e.Store, "imports"), "approval": "new"} {
		eq(t, k, info[k], want)
	}
}

// py: test_running.py::test_a_run_that_cant_start_is_an_error
func TestRunning_ARunThatCantStartIsAnError(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, testkit.GO)
	res := e.RunWith(testkit.Opts{Env: map[string]string{"PATH": e.Tmp}}, "r", "go")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stderr", res.Stderr, "error:")
}

// py: test_running.py::test_invalid_args_declaration_is_an_error
func TestRunning_InvalidArgsDeclarationIsAnError(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"bad": map[string]any{"inline": "true", "args": []any{
		map[string]any{"name": "a", "rest": true}, map[string]any{"name": "b"}}}})
	res := e.Run("r", "bad")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stderr", res.Stderr, "only the last entry in 'args' can be 'rest'")
	e.WriteStore(e.Store, map[string]any{"both": map[string]any{"inline": "true", "args": []any{
		map[string]any{"name": "a", "required": true, "default": "x"}}}})
	res = e.Run("r", "both")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stderr", res.Stderr, "arg 'a' can't be both 'required' and have a 'default'")
}

// py: test_running.py::test_an_empty_word_doesnt_fill_a_required_argument
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
	contains(t, "stderr", res.Stderr, "error: <target> can't be empty\n")
	if out := e.Run("r", "opt", "").Stdout; out != "[]\n" {
		t.Errorf("opt: %q", out)
	}
}

// py: test_running.py::test_a_script_with_windows_line_endings_says_so
func TestRunning_AScriptWithWindowsLineEndingsSaysSo(t *testing.T) {
	e := testkit.New(t)
	script := e.Script("crlf.sh", "#!/usr/bin/env bash\r\necho hi\r\n")
	e.WriteStore(e.Store, map[string]any{"crlf": map[string]any{"script": script}})
	res := e.Run("r", "crlf")
	if res.Code != 1 {
		t.Errorf("exit %d", res.Code)
	}
	contains(t, "stderr", res.Stderr, "error: "+script+" has Windows line endings")
	contains(t, "stderr", res.Stderr, "sed -i 's/\\r$//' "+script)
}

// py: test_running.py::test_a_hand_written_invalid_name_doesnt_run
func TestRunning_AHandWrittenInvalidNameDoesntRun(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"a b": inline("echo ran")})
	res := e.Run("r", "a b")
	if res.Code != 1 || res.Stdout != "" {
		t.Errorf("exit %d stdout %q", res.Code, res.Stdout)
	}
	contains(t, "stderr", res.Stderr, "invalid shortcut name 'a b'")
}

// py: test_running.py::test_declared_args
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
		"free": inline(`echo "$#"`),
		"one":  map[string]any{"inline": "true", "args": []any{map[string]any{"name": "only"}}},
	})
	lines := func(args ...string) []string {
		return strings.Split(strings.TrimSuffix(e.Run(args...).Stdout, "\n"), "\n")
	}

	res := e.Run("r", "kill")
	if res.Code != 2 {
		t.Errorf("missing: exit %d", res.Code)
	}
	contains(t, "missing", res.Stderr, "missing <container>")
	contains(t, "missing", res.Stderr, "usage: shortcut r kill <container> [signal] [extra...]")
	// With a desc on any argument, the error lists them all
	contains(t, "list", res.Stderr, "  container  required       Which one\n")
	contains(t, "list", res.Stderr, "  signal     default: TERM\n")
	contains(t, "list", res.Stderr, "  extra...   optional\n")

	eq(t, "web", lines("r", "kill", "web"), []string{"[web][TERM]", "c=web s=TERM e="})
	eq(t, "all", lines("r", "kill", "web", "KILL", "a b", "c"), []string{"[web][KILL][a b][c]", "c=web s=KILL e='a b' c"})
	// Flags are neither counted nor moved, and a default keeps its position before them
	eq(t, "flag", lines("r", "kill", "web", "--force")[0], "[web][TERM][--force]")
	// A skipped optional keeps later defaults in their own position
	eq(t, "two", e.Run("r", "two").Stdout, "2: B\n")

	res = e.Run("r", "one", "a", "b")
	if res.Code != 2 {
		t.Errorf("too many: exit %d", res.Code)
	}
	contains(t, "too many", res.Stderr, "too many arguments")
	contains(t, "too many", res.Stderr, "usage: shortcut r one [only]")
	if strings.Contains(res.Stderr, "  only") {
		t.Errorf("a list with no desc anywhere: %q", res.Stderr)
	}

	// After --, every word is a value, even one starting with a dash; the -- itself is dropped
	contains(t, "-5", e.Run("r", "kill", "-5").Stderr, "missing <container>")
	eq(t, "--", lines("r", "kill", "--force", "--", "-5", "-x"), []string{"[--force][-5][-x]", "c=-5 s=-x e="})
	eq(t, "two --", e.Run("r", "two", "--", "-a").Stdout, "2:-a B\n")

	// No `args` key: no checking, and -- passes through
	eq(t, "free", e.Run("r", "free", "1", "2", "3").Stdout, "3\n")
	eq(t, "free --", e.Run("r", "free", "--", "x").Stdout, "2\n")
	shown := e.Run("show", "kill").Stdout
	contains(t, "show", shown, "Usage:       shortcut r kill <container> [signal] [extra...]\n")
	contains(t, "show", shown, "Arguments:\n  container  required       Which one\n  signal     default: TERM\n  extra...   optional\n")
	if strings.Contains(e.Run("show", "free").Stdout, "Arguments:") {
		t.Errorf("show free lists arguments")
	}
}

// py: test_running.py::test_a_closed_pipe_stops_quietly
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
