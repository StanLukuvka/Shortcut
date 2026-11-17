package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInTerminal_KeysAndWindow(t *testing.T) {
	e := New(t)
	code, out := e.InTerminalCmd(Opts{}, []Key{{Wait: "name? ", Send: []byte("bob\n")}}, "sh", "-c", `printf 'name? '; read x; echo got:$x`)
	if code != 0 || !strings.Contains(out, "got:bob\r\n") {
		t.Errorf("code %d, out %q", code, out)
	}
	// 30x100, a controlling terminal on all three std fds, and its own session.
	code, out = e.InTerminalCmd(Opts{}, nil, "sh", "-c", `stty size; test -t 0 -a -t 1 -a -t 2 && echo tty; ps -o stat= -p $$ | grep -q s && echo leader`)
	lines := strings.Fields(out)
	if code != 0 || len(lines) != 4 || lines[0] != "30" || lines[1] != "100" || lines[2] != "tty" || lines[3] != "leader" {
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
