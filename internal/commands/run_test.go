package commands

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
	show := `echo "$SHORTCUT_NAME|$SHORTCUT_NAMESPACE|$SHORTCUT_CALLER_CWD"`
	e.WriteImport(e.Store, "dk", testkit.NS(map[string]any{
		"env": testkit.Inline(show)}))
	e.WriteStore(e.Store, map[string]any{"env": testkit.Inline(show)})
	caller := e.Folder("caller")
	outer := map[string]string{"SHORTCUT_NAMESPACE": "outer"}
	res := e.RunWith(testkit.Opts{Cwd: caller, Env: outer}, "r", "dk", "env")
	if want := "dk env|dk|" + caller + "\n"; res.Stdout != want {
		t.Errorf("stdout %q, want %q (%s)", res.Stdout, want, res.Stderr)
	}
	res = e.RunWith(testkit.Opts{Cwd: caller, Env: outer}, "r", "env")
	if want := "env||" + caller + "\n"; res.Stdout != want {
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
	testkit.Contains(t, "r --help", e.Run("r", "--help").Stdout, "shortcut run|r [-y] [.] <name>")
}

// A script is handed to the kernel as it is: its #! line runs it, a binary runs, a file the kernel refuses says so,
// and one that is not executable stops with the chmod command.
func TestRunning_AScriptRunsAsItIs(t *testing.T) {
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
	e.WriteFile(filepath.Join(scripts, "exec.sh"), "#!/usr/bin/env bash\necho exec_shebang\n", 0o755)
	e.WriteFile(filepath.Join(scripts, "plain.sh"), "#!/usr/bin/env sh\necho plain\n", 0o644)
	e.WriteFile(filepath.Join(scripts, "bin"), bin, 0o755)
	e.WriteFile(filepath.Join(scripts, "macho"), append([]byte("\xcf\xfa\xed\xfe"), make([]byte, 64)...), 0o755)
	e.WriteStore(e.Store, map[string]any{
		"exec":  map[string]any{"script": "scripts/exec.sh"},
		"plain": map[string]any{"script": "scripts/plain.sh"},
		"bin":   map[string]any{"script": "scripts/bin"},
		"macho": map[string]any{"script": "scripts/macho"},
	})
	if res := e.Run("run", "exec"); res.Code != 0 || res.Stdout != "exec_shebang\n" {
		t.Errorf("exec: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if res := e.Run("run", "bin"); res.Code != 0 || res.Stdout != "" {
		t.Errorf("bin: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	res := e.Run("run", "plain")
	testkit.Eq(t, "plain exit", res.Code, 1)
	testkit.Eq(t, "plain stdout", res.Stdout, "")
	path := filepath.Join(scripts, "plain.sh")
	testkit.Contains(t, "plain stderr", res.Stderr, "error: "+path+" is not executable. Make it so: chmod +x "+path)
	// A binary the kernel will not run (another system's format) is not handed to bash.
	res = e.Run("run", "macho")
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
		testkit.Contains(t, "run "+flag, res.Stdout, "shortcut run|r [-y] [.] <name> [args...]")
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
	}, testkit.Dirs("frontend", "src"))
	here := filepath.Join(proj, "src")
	for _, c := range [][2]string{{"default", here}, {"caller", here}, {"root", proj}, {"sibling", filepath.Join(proj, "frontend")}, {"fixed", fixed}} {
		res := e.RunWith(in(here), "r", ".", c[0])
		if res.Stdout != c[1]+"\n" {
			t.Errorf("%s: stdout %q, want %q", c[0], res.Stdout, c[1]+"\n")
		}
		testkit.Contains(t, c[0], res.Stderr, "→ "+c[1]+"\n")
	}
	res := e.RunWith(in(here), "r", ".", "gone")
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

func TestRunning_ShowPrintsTheEntryScriptAndCwd(t *testing.T) {
	e := testkit.New(t)
	script := e.Script("shown.sh", "#!/usr/bin/env bash\necho show output running\n")
	e.WriteStore(e.Store, map[string]any{"shown": map[string]any{"script": script, "description": "Show sample", "cwd": e.Tmp}})
	res := e.Run("show", "shown")
	res.WantCode(t, 0)
	testkit.Contains(t, "show", res.Stdout, "Description: Show sample\n")
	testkit.Contains(t, "show", res.Stdout, "echo show output running")
	testkit.Contains(t, "show", res.Stdout, "Run CWD:     "+e.Tmp+"\n")
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
		"source": nil, "script_path": filepath.Join(e.Store, "scripts", "s.sh"), "cwd": "/tmp", "run_cwd": "/tmp",
		"error": nil, "entry": map[string]any{"script": "scripts/s.sh", "cwd": "/tmp"}} {
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
		"defined_in": filepath.Join(e.Store, "imports", "dk.shortcut"), "cwd": ".", "run_cwd": filepath.Join(e.Store, "imports")} {
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

func TestRunning_EveryWordReachesTheShortcutAFirstDoubleDashIncluded(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"words": testkit.Inline(`printf "[%s]" "$@"; echo`)})
	testkit.Eq(t, "--", e.Run("r", "words", "--", "-5", "x").Stdout, "[--][-5][x]\n")
	testkit.Eq(t, "flags", e.Run("r", "words", "--force", "", "a b").Stdout, "[--force][][a b]\n")
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

// The "-> cwd" line goes to stderr with a bad byte escaped (as \xNN). A global shortcut still runs from a folder
// whose name is not valid UTF-8.
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

// run reaches the global store without the dot and only the project with it, for a name and for a group, and each side
// says where the other has the name. Nothing runs on a miss.
func TestRunning_TheDotSelectsTheScope(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"build": testkit.Inline("echo global-build"), "gonly": testkit.Inline("echo gonly")})
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{"ps": testkit.Inline("echo global-ps")}))
	proj := e.Project(map[string]any{"build": testkit.Inline("echo project-build"), "ponly": testkit.Inline("echo ponly")})
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "terraform", testkit.NS(map[string]any{"plan": testkit.Inline("echo plan")}))
	e.WriteImport(filepath.Join(proj, ".shortcuts"), "docker", testkit.NS(map[string]any{"delete": testkit.Inline("echo project-delete")}))
	o := testkit.Opts{Cwd: proj, Env: map[string]string{"SHORTCUT_SHOW_DIR": "0"}}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"r", "build"}, "global-build\n"},
		{[]string{"r", "gonly"}, "gonly\n"},
		{[]string{"r", "docker", "ps"}, "global-ps\n"},
		{[]string{"r", ".", "build"}, "project-build\n"},
		{[]string{"r", ".", "ponly"}, "ponly\n"},
		{[]string{"r", ".", "terraform", "plan"}, "plan\n"},
		{[]string{"r", ".", "docker", "delete"}, "project-delete\n"},
		{[]string{"r", "-y", ".", "build"}, "project-build\n"},
	} {
		testkit.Eq(t, strings.Join(c.args, " "), e.RunWith(o, c.args...).Stdout, c.want)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"r", "ponly"}, "shortcut 'ponly' not found. The project in " + proj + " has it: shortcut r . ponly"},
		{[]string{"r", "terraform", "plan"}, "The project in " + proj + " has it: shortcut r . terraform plan"},
		{[]string{"r", "docker", "delete"}, "'delete' not found in namespace 'docker'. It has: ps. The project in " + proj + " has it: shortcut r . docker delete"},
		{[]string{"r", ".", "gonly"}, "shortcut 'gonly' not found. Your global store has it: shortcut r gonly"},
		{[]string{"r", ".", "docker", "ps"}, "Your global store has it: shortcut r docker ps"},
	} {
		res := e.RunWith(o, c.args...)
		testkit.Eq(t, strings.Join(c.args, " ")+" exit", res.Code, 1)
		testkit.Eq(t, strings.Join(c.args, " ")+" stdout", res.Stdout, "")
		testkit.Contains(t, strings.Join(c.args, " "), res.Stderr, c.want)
	}

	// The dot outside every project, and the dot alone
	res := e.RunWith(testkit.Opts{Cwd: e.Tmp}, "r", ".", "build")
	testkit.Eq(t, "outside exit", res.Code, 1)
	testkit.Eq(t, "outside stdout", res.Stdout, "")
	testkit.Contains(t, "outside", res.Stderr, "error: no project here: no .shortcuts or .shortcuts.local in this folder or its parents")
	e.RunWith(o, "r", ".").WantCode(t, 2)
	// the dot is only the first word after the options: later, it is the shortcut's
	e.WriteStore(e.Store, map[string]any{"echoes": testkit.Inline(`echo "$@"`)})
	testkit.Eq(t, "late dot", e.RunWith(o, "r", "echoes", ".").Stdout, ".\n")
}

func TestRunning_ShowDirTakesZeroOrOne(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"hi": testkit.Inline("echo hi")})
	for v, want := range map[string]string{"0": "", "1": "→ "} {
		res := e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": v}}, "r", "hi")
		if res.Code != 0 || res.Stdout != "hi\n" || (want == "") != (res.Stderr == "") || !strings.HasPrefix(res.Stderr, want) {
			t.Errorf("%q: %+v", v, res)
		}
	}
	for _, v := range []string{"", "2", "yes"} {
		res := e.RunWith(testkit.Opts{Env: map[string]string{"SHORTCUT_SHOW_DIR": v}}, "r", "hi")
		if res.Code != 1 || res.Stdout != "" || res.Stderr != "error: SHORTCUT_SHOW_DIR must be 0 or 1, not '"+v+"'\n" {
			t.Errorf("%q: %+v", v, res)
		}
	}
}

// A folder above the cwd that can't be searched is an error for the project scope, and the global scope doesn't care.
func TestRunning_AProjectLookupThatCantTellDoesntTouchTheGlobalScope(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can search any folder")
	}
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"x": map[string]any{"inline": "echo ran", "cwd": e.Home}})
	child := filepath.Join(e.Folder("locked"), "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// the shell enters the folder first and then locks its parent: the cwd stays, but nothing above it can be searched
	script := fmt.Sprintf(`cd '%s' && chmod 000 .. && { shortcut r . x; echo "project=$?"; shortcut r x; echo "global=$?"; shortcut list; echo "list=$?"; } 2>&1`, child)
	res := e.Shell(testkit.Opts{Cwd: e.Home}, script)
	testkit.Contains(t, "project scope", res.Stdout, "permission denied")
	testkit.Contains(t, "project scope", res.Stdout, "project=1\n")
	testkit.Contains(t, "global scope", res.Stdout, "ran\nglobal=0\n")
	testkit.Contains(t, "list", res.Stdout, "warning: ")
	testkit.Contains(t, "list", res.Stdout, "list=0\n")
}
