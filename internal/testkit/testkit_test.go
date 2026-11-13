package testkit

// Tests of testkit itself (not tests of a command). Real shortcut commands don't exist yet, so the "binary" is an
// `sh` script (useBinary) or a plain program.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBinary_BuildsTheModuleOnce(t *testing.T) {
	e := New(t)
	a, b := Binary(t), Binary(t)
	if a != b {
		t.Errorf("built twice: %s, %s", a, b)
	}
	if res := e.Run("--version"); res.Code != 0 || res.Stderr != "" {
		t.Errorf("--version: %+v", res)
	}
}

func TestRun_CodesStreamsAndInput(t *testing.T) {
	e := New(t)
	in := "from stdin"
	res := e.RunCmd(Opts{Input: &in}, "sh", "-c", `cat; echo err >&2; exit 3`)
	if res != (Result{3, "from stdin", "err\n"}) {
		t.Errorf("%+v", res)
	}
	if res := e.RunCmd(Opts{}, "sh", "-c", "kill -9 $$"); res.Code != -9 {
		t.Errorf("signal death = %d, want -9", res.Code)
	}
	if res := e.RunCmd(Opts{}, "sh", "-c", "cat; echo eof"); res.Stdout != "eof\n" {
		t.Errorf("stdin is not /dev/null: %q", res.Stdout)
	}
}

func TestRun_EnvAndCwd(t *testing.T) {
	e := New(t)
	link := filepath.Join(e.Tmp, "link")
	real := e.Folder("real")
	e.Symlink(real, link)
	res := e.RunCmd(Opts{Cwd: link, Env: map[string]string{"X": "1"}}, "sh", "-c", `echo "$HOME|$SHORTCUTS_HOME|$X|$PWD|$(pwd -P)"`)
	want := strings.Join([]string{e.Home, e.Store, "1", link, real}, "|") + "\n"
	if res.Stdout != want {
		t.Errorf("got %q, want %q", res.Stdout, want)
	}
	// PWD defaults to the cwd, not the test binary's.
	if res := e.RunCmd(Opts{}, "sh", "-c", `echo "$PWD"`); res.Stdout != e.Home+"\n" {
		t.Errorf("PWD = %q", res.Stdout)
	}
}

func TestRun_TimeoutEndsTheTest(t *testing.T) {
	e := New(t)
	e.fail = func(err error) { panic(err) }
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(error).Error(), "timed out") {
			t.Errorf("recovered %v", r)
		}
	}()
	e.RunCmd(Opts{Timeout: 200 * time.Millisecond}, "sleep", "30")
}

func TestShell_HasShortcutOnPath(t *testing.T) {
	e := New(t)
	useBinary(t, e, `echo "stand-in $*"`+"\n")
	if res := e.Shell(Opts{}, `shortcut a b; shortcut c`); res.Stdout != "stand-in a b\nstand-in c\n" {
		t.Errorf("%+v", res)
	}
	// Once on PATH it stays once, and a PATH override is respected.
	res := e.Shell(Opts{Env: map[string]string{"PATH": "/usr/bin:/bin"}}, `echo "$PATH"`)
	if res.Stdout != e.ToolBin+":/usr/bin:/bin\n" {
		t.Errorf("PATH = %q", res.Stdout)
	}
}

func TestStart_ReadsStdoutAndSignals(t *testing.T) {
	e := New(t)
	useBinary(t, e, "echo warn >&2\necho ready\nexec sleep 30\n")
	p := e.Start(Opts{})
	line, err := p.Stdout().ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("line %q, %v", line, err)
	}
	p.Signal(syscall.SIGTERM)
	res := p.Wait(10 * time.Second)
	if res.Code != -int(syscall.SIGTERM) || res.Stdout != "" || res.Stderr != "warn\n" {
		t.Errorf("%+v", res)
	}
}

func TestInTerminal_KeysAndWindow(t *testing.T) {
	e := New(t)
	code, out := e.InTerminalCmd(Opts{}, []Key{{Wait: "name? ", Send: []byte("bob\n")}}, "sh", "-c", `printf 'name? '; read x; echo got:$x`)
	if code != 0 || !strings.Contains(out, "got:bob\r\n") {
		t.Errorf("code %d, out %q", code, out)
	}
	// 30x100, a controlling terminal on all three std fds, and its own session.
	code, out = e.InTerminalCmd(Opts{}, nil, "sh", "-c", `stty size; test -t 0 -a -t 1 -a -t 2 && echo tty; ps -o sid= -p $$ | tr -d ' '; echo $$`)
	lines := strings.Fields(out)
	if code != 0 || len(lines) != 5 || lines[0] != "30" || lines[1] != "100" || lines[2] != "tty" || lines[3] != lines[4] {
		t.Errorf("code %d, out %q", code, out)
	}
}

func TestInTerminal_SignalAndDoKeys(t *testing.T) {
	e := New(t)
	done := false
	code, out := e.InTerminalCmd(Opts{}, []Key{
		{Wait: "ready", Do: func() { done = true }},
		{Wait: "ready", Signal: syscall.SIGINT},
	}, "sh", "-c", `trap 'echo caught; exit 3' INT; echo ready; while :; do sleep 0.05; done`)
	if code != 3 || !strings.Contains(out, "caught") || !done {
		t.Errorf("code %d, done %v, out %q", code, done, out)
	}
	// A key that never matches ends at the deadline: with a short Timeout, the test fails rather than hangs.
	e.fail = func(err error) { panic(err) }
	defer func() {
		if r := recover(); r == nil {
			t.Error("no failure for a run that outlives its timeout")
		}
	}()
	e.InTerminalCmd(Opts{Timeout: 300 * time.Millisecond}, nil, "sleep", "30")
}

func TestInTerminal_StdoutToFile(t *testing.T) {
	e := New(t)
	useBinary(t, e, `printf 'name? ' >&2; read x; echo "out:$x"; test -t 1 && echo tty || echo notty`+"\n")
	file := filepath.Join(e.Tmp, "out.txt")
	code, out := e.InTerminal(Opts{StdoutTo: file}, []Key{{Wait: "name? ", Send: []byte("amy\n")}})
	got, _ := os.ReadFile(file)
	if code != 0 || string(got) != "out:amy\nnotty\n" || strings.Contains(out, "out:amy\r\nnotty") {
		t.Errorf("code %d, file %q, terminal %q", code, got, out)
	}
	if !strings.Contains(out, "name? ") {
		t.Errorf("the prompt (stderr) should stay on the terminal: %q", out)
	}
}

func TestBashComplete_RunsTheCompletionScript(t *testing.T) {
	e := New(t)
	useBinary(t, e, `[ "$1" = completion ] && echo '_shortcut() { COMPREPLY=("${COMP_WORDS[COMP_CWORD]}-one" two); }'`+"\n")
	if got, want := e.BashComplete("a b", "x"), []string{"x-one", "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestFixtures_RegistryHelpers(t *testing.T) {
	e := New(t)
	proj := e.Project(GO)
	store := filepath.Join(proj, ".shortcuts")
	if e.registryPath(store) != store || e.registryPath(e.Store) != filepath.Join(e.Store, "shortcuts.json") ||
		e.registryPath(proj) != filepath.Join(proj, "shortcuts.json") {
		t.Error("RegistryPath")
	}
	e.WriteStore(e.Store, OWN)
	if got := e.LoadGlobal(); !reflect.DeepEqual(got, decoded(t, OWN)) {
		t.Errorf("LoadGlobal = %v", got)
	}
	// A project lists an import in .shortcuts; the global store just finds it.
	p := e.WriteImport(store, "docker", DockerPS)
	raw, _ := os.ReadFile(store)
	if p != filepath.Join(proj, "imports", "docker.shortcut") || !strings.Contains(string(raw), `"imports":{"docker":"imports/docker.shortcut"}`) {
		t.Errorf("project import: %s, store %s", p, raw)
	}
	if g := e.WriteImport(e.Store, "kube", KubeGet); g != filepath.Join(e.Store, "imports", "kube.shortcut") {
		t.Errorf("global import: %s", g)
	}
	// <, > and & stay as written, and a raw file keeps its key order.
	e.writeRegistry(e.Store, map[string]any{"a": "<&>"})
	e.WriteRegistryRaw(e.Store, `{"z":1,"a":2}`)
	if b, _ := os.ReadFile(filepath.Join(e.Store, "shortcuts.json")); string(b) != `{"z":1,"a":2}` {
		t.Errorf("raw = %s", b)
	}
}

func TestNew_UnreadableDirsDontBreakCleanup(t *testing.T) {
	e := New(t)
	d := e.Folder("locked")
	e.WriteFile(filepath.Join(d, "f"), "x", 0)
	if err := os.Chmod(d, 0); err != nil {
		t.Fatal(err)
	}
}

// A program started with CloseFDs finds those descriptors closed, not on /dev/null (sh reports the failed write).
func TestRun_CloseFDsStartsTheChildWithThemClosed(t *testing.T) {
	e := New(t)
	script := `if echo hi 2>/dev/null; then echo open >&2; else echo closed >&2; fi; if [ -e /dev/fd/0 ]; then echo in-open >&2; else echo in-closed >&2; fi`
	res := e.RunCmd(Opts{CloseFDs: []int{1}}, "sh", "-c", script)
	if res.Stdout != "" || !strings.Contains(res.Stderr, "closed\n") {
		t.Errorf("stdout closed: %+v", res)
	}
	if res := e.RunCmd(Opts{}, "sh", "-c", script); res.Stdout != "hi\n" || !strings.Contains(res.Stderr, "in-open") {
		t.Errorf("nothing closed: %+v", res)
	}
	if res := e.RunCmd(Opts{CloseFDs: []int{0}}, "sh", "-c", script); !strings.Contains(res.Stderr, "in-closed") {
		t.Errorf("stdin closed: %+v", res)
	}
}
