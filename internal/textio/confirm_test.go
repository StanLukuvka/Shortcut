// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-2). Don't hand-edit without checking the plan.

package textio

import (
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestAsk_Answers(t *testing.T) {
	e := testkit.New(t)
	for _, c := range []struct {
		input  string
		accept []string
		want   bool
		out    string
	}{
		{"y\n", nil, true, ""}, {" YES \n", nil, true, ""}, {"n\n", nil, false, ""}, {"maybe\n", nil, false, ""},
		{"\n", nil, false, ""}, {"y", nil, true, ""}, // an unfinished last line counts, as input() returns it
		{"", nil, false, "\n"}, // end of input: a newline on stdout, answer no
		{"delete\n", []string{"delete"}, true, ""}, {"y\n", []string{"delete"}, false, ""},
	} {
		s, out, errs := capture(t, e, c.input)
		if got := Ask(s, "go on? \x1b[2K", c.accept); got != c.want {
			t.Errorf("Ask(%q, %q) = %v", c.input, c.accept, got)
		}
		s.Flush()
		if errs() != "go on? ^[[2K" || out() != c.out { // the prompt cannot carry an escape
			t.Errorf("Ask(%q): stderr %q stdout %q", c.input, errs(), out())
		}
	}
	s, _, _ := capture(t, e, "y\nn\nrest")
	if !Ask(s, "", nil) || Ask(s, "", nil) {
		t.Error("two questions on one input must read one line each")
	}
}

func TestConfirm_WithoutATerminalAndWithYes(t *testing.T) {
	e := testkit.New(t)
	c := Confirm{Question: "q?", NoTerminal: "run it in a terminal, or pass -y", Message: "this changes things"}
	s, out, errs := capture(t, e, "y\n") // stdin is a file: no terminal, so the input is never read
	if ok, err := c.Granted(s, false); ok || err == nil || err.Error() != c.NoTerminal {
		t.Errorf("no terminal: %v, %v", ok, err)
	}
	if ok, err := c.Granted(s, true); !ok || err != nil {
		t.Errorf("-y: %v, %v", ok, err)
	}
	s.Flush()
	if out() != "" || errs() != "" {
		t.Errorf("-y without TellOnYes prints nothing: %q %q", out(), errs())
	}
	c.TellOnYes = true
	if ok, _ := c.Granted(s, true); !ok {
		t.Error("-y")
	}
	s.Flush()
	if out() != "this changes things\n" {
		t.Errorf("TellOnYes: %q", out())
	}
}

func TestAsk_ATerminalSession(t *testing.T) {
	e := testkit.New(t)
	run := func(keys []testkit.Key) string {
		code, out := e.InTerminalCmd(testkit.Opts{Env: map[string]string{"TEXTIO_HELPER": "confirm"}}, keys, os.Args[0], "-test.run=^$")
		if code != 0 {
			t.Fatalf("exit %d: %q", code, out)
		}
		return out
	}
	// The message sits above the question, an escape in it is shown as ^[, and `[y/N]` marks the default.
	out := run([]testkit.Key{{Wait: "[y/N]", Send: []byte("y\n")}})
	if !strings.Contains(out, "will run ^[[2Kit\r\nrun 'go'? [y/N] ") || !strings.Contains(out, "granted=true err=<nil>") || strings.Contains(out, "Aborted.") {
		t.Errorf("yes: %q", out)
	}
	out = run([]testkit.Key{{Wait: "[y/N]", Send: []byte("n\n")}})
	if !strings.Contains(out, "Aborted.") || !strings.Contains(out, "granted=false") {
		t.Errorf("no: %q", out)
	}
	out = run([]testkit.Key{{Wait: "[y/N]", Send: []byte{4}}}) // Ctrl-D: end of input
	if !strings.Contains(out, "\n\r\nAborted.") && !strings.Contains(out, "Aborted.") || !strings.Contains(out, "granted=false") {
		t.Errorf("EOF: %q", out)
	}
	out = run([]testkit.Key{{Wait: "[y/N]", Signal: syscall.SIGINT}}) // C23: Ctrl-C answers no, the process lives
	if !strings.Contains(out, "Aborted.") || !strings.Contains(out, "granted=false") {
		t.Errorf("Ctrl-C: %q", out)
	}
}
