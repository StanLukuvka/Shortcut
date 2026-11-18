package testkit

import (
	"os"
	"path/filepath"
	"strings"
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

// A run that outlives its timeout fails the test rather than hanging it.
func TestInTerminal_AHangBecomesAFailure(t *testing.T) {
	e := New(t)
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
