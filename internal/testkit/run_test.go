package testkit

import (
	"path/filepath"
	"strings"
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

// A program started with CloseFDs finds that descriptor closed, not on /dev/null (sh reports the failed write).
func TestRun_CloseFDsStartsTheChildWithThemClosed(t *testing.T) {
	e := New(t)
	res := e.RunCmd(Opts{CloseFDs: []int{1}}, "sh", "-c", `if echo hi 2>/dev/null; then echo open >&2; else echo closed >&2; fi`)
	if res.Stdout != "" || res.Stderr != "closed\n" {
		t.Errorf("stdout closed: %+v", res)
	}
}
