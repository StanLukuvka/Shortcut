package testkit

import (
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

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

func TestBashComplete_RunsTheCompletionScript(t *testing.T) {
	e := New(t)
	useBinary(t, e, `[ "$1" = completion ] && echo '_shortcut() { COMPREPLY=("${COMP_WORDS[COMP_CWORD]}-one" two); }'`+"\n")
	if got, want := e.BashComplete("a b", "x"), []string{"x-one", "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
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
