package textio

import (
	"os"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func diffOn(t *testing.T, e *testkit.Env, tty bool, lines string, env map[string]string) string {
	t.Helper()
	env["TEXTIO_HELPER"], env["TEXTIO_LINES"] = "diff", lines
	o := testkit.Opts{Env: env}
	if tty {
		code, out := e.InTerminalCmd(o, nil, os.Args[0], "-test.run=^$")
		if code != 0 {
			t.Fatalf("exit %d: %q", code, out)
		}
		return out
	}
	res := e.RunCmd(o, os.Args[0], "-test.run=^$")
	if res.Code != 0 {
		t.Fatalf("exit %d: %q", res.Code, res.Stderr)
	}
	return res.Stdout
}

func TestShowDiff_NoTerminalIsPlainSafeText(t *testing.T) {
	e := testkit.New(t)
	got := diffOn(t, e, false, "6", map[string]string{"PAGER": "echo pager ran", "LINES": "2"})
	if want := "--- a/f\n+++ b/f\n@@ -1 +1 @@\n+added ^[[2J\n-removed\n context\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShowDiff_ATerminalGetsColour(t *testing.T) {
	e := testkit.New(t)
	got := diffOn(t, e, true, "6", map[string]string{"PAGER": "echo pager ran"}) // 6 lines < 30 rows: no pager
	for _, want := range []string{"\r\n--- a/f\r\n", "+++ b/f\r\n", "@@ -1 +1 @@\r\n", "\x1b[32m+added ^[[2J\x1b[0m\r\n", "\x1b[31m-removed\x1b[0m\r\n", " context\r\n"} {
		if !strings.Contains("\r\n"+got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "pager ran") {
		t.Error("a short diff must not use the pager")
	}
}

// C24: pager only on a terminal, with PAGER set, and at least as many lines as rows (30 on testkit's terminal).
func TestShowDiff_Pager(t *testing.T) {
	e := testkit.New(t)
	for _, c := range []struct {
		name, lines string
		env         map[string]string
		pager       bool // the pager ran
		printed     bool // the diff was printed directly
	}{
		{"used", "30", map[string]string{"PAGER": "sh -c 'cat >/dev/null; echo pager ran'"}, true, false},
		{"one line short", "29", map[string]string{"PAGER": "sh -c 'cat >/dev/null; echo pager ran'"}, false, true},
		{"LINES decides the rows", "5", map[string]string{"PAGER": "sh -c 'cat >/dev/null; echo pager ran'", "LINES": "5"}, true, false},
		{"unset", "40", map[string]string{}, false, true},
		{"empty", "40", map[string]string{"PAGER": ""}, false, true},
		{"a failing pager falls through to printing", "40", map[string]string{"PAGER": "sh -c 'cat >/dev/null; echo pager ran; exit 1'"}, true, true},
		{"not found", "40", map[string]string{"PAGER": "no-such-pager-zz9"}, false, true},
		{"bad split", "40", map[string]string{"PAGER": "'unterminated"}, false, true},
		{"blank", "40", map[string]string{"PAGER": "  "}, false, true},
		{"LESS defaults to FRX", "40", map[string]string{"PAGER": "sh -c 'cat >/dev/null; echo less=$LESS'"}, false, false},
		{"LESS is kept", "40", map[string]string{"PAGER": "sh -c 'cat >/dev/null; echo less=$LESS'", "LESS": "-X"}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := diffOn(t, e, true, c.lines, c.env)
			if ran := strings.Contains(got, "pager ran"); ran != c.pager {
				t.Errorf("pager ran = %v in %q", ran, got)
			}
			if printed := strings.Contains(got, "+added"); printed != c.printed {
				t.Errorf("printed = %v in %q", printed, got)
			}
			if strings.HasPrefix(c.name, "LESS") {
				want := map[string]string{"LESS defaults to FRX": "less=FRX", "LESS is kept": "less=-X"}[c.name]
				if !strings.Contains(got, want+"\r\n") {
					t.Errorf("want %q in %q", want, got)
				}
			}
		})
	}
}

// The pager gets the same escapes as a Writer: a non-UTF-8 byte (0x9b is CSI on an 8-bit terminal) never reaches it raw.
func TestShowDiff_PagerGetsEscapedBytes(t *testing.T) {
	e := testkit.New(t)
	got := diffOn(t, e, true, "30", map[string]string{"PAGER": "cat", "TEXTIO_RAW": "1"})
	if !strings.Contains(got, `a\udc9bb.sh`) || strings.Contains(got, "\x9b") {
		t.Errorf("got %q", got)
	}
}
