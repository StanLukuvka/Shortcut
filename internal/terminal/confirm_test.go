package terminal

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
		input string
		want  bool
		out   string
	}{
		{"y\n", true, ""}, {" YES \n", true, ""}, {"n\n", false, ""}, {"maybe\n", false, ""},
		{"\n", false, ""}, {"y", true, ""}, // an unfinished last line counts
		{"", false, "\n"}, // end of input: a newline on stdout, answer no
	} {
		s, out, errs := capture(t, e, c.input)
		if got, err := Ask(s, "go on? "); got != c.want || err != nil {
			t.Errorf("Ask(%q) = %v, %v", c.input, got, err)
		}
		if errs() != "go on? " || out() != c.out { // the prompt is written as it is
			t.Errorf("Ask(%q): stderr %q stdout %q", c.input, errs(), out())
		}
	}
	s, _, _ := capture(t, e, "y\nn\nrest")
	first, _ := Ask(s, "")
	second, _ := Ask(s, "")
	if !first || second {
		t.Error("two questions on one input must read one line each")
	}
}

func TestAsk_AReadErrorIsAnError(t *testing.T) {
	e := testkit.New(t)
	s, _, _ := capture(t, e, "")
	dir, err := os.Open(e.Tmp) // reading a folder fails with EISDIR, which is not end of input
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	s.In = dir
	if ok, err := Ask(s, "go on? "); ok || err == nil {
		t.Errorf("a failed read: %v, %v", ok, err)
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
	if out() != "" || errs() != "" {
		t.Errorf("-y without TellOnYes prints nothing: %q %q", out(), errs())
	}
	c.TellOnYes = true
	if ok, _ := c.Granted(s, true); !ok {
		t.Error("-y")
	}
	if out() != "this changes things\n" {
		t.Errorf("TellOnYes: %q", out())
	}
}

func TestAsk_ATerminalSession(t *testing.T) {
	e := testkit.New(t)
	run := func(keys []testkit.Key) string {
		code, out := e.InTerminalCmd(testkit.Opts{Env: map[string]string{"TERM_HELPER": "confirm"}}, keys, testkit.SelfArgs()...)
		if code != 0 {
			t.Fatalf("exit %d: %q", code, out)
		}
		return out
	}
	// The message sits above the question, it is written as it is, and `[y/N]` marks the default.
	out := run([]testkit.Key{{Wait: "[y/N]", Send: []byte("y\n")}})
	if !strings.Contains(out, "will run \x1b[2Kit\r\nrun 'go'? [y/N] ") || !strings.Contains(out, "granted=true err=<nil>") || strings.Contains(out, "Aborted.") {
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
	out = run([]testkit.Key{{Wait: "[y/N]", Signal: syscall.SIGINT}}) // Ctrl-C answers no, the process lives
	if !strings.Contains(out, "Aborted.") || !strings.Contains(out, "granted=false") {
		t.Errorf("Ctrl-C: %q", out)
	}
}
